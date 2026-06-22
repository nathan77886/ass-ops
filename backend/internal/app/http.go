package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

var ErrNotFound = errors.New("not found")

var approvalWebhookHTTPClient = &http.Client{Timeout: 5 * time.Second}

var errAgentPlanNotApproved = errors.New("agent task requires an approved plan before execution")

const (
	contextDirMode  os.FileMode = 0o750
	contextFileMode os.FileMode = 0o600
)

type Server struct {
	cfg            Config
	store          *Store
	log            *slog.Logger
	webhookLimiter *webhookRateLimiter
}

func NewServer(cfg Config, store *Store, log *slog.Logger) *Server {
	return &Server{cfg: cfg, store: store, log: log, webhookLimiter: newWebhookRateLimiter(60, time.Minute)}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cors)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r.Post("/api/auth/login", s.login)
	r.Post("/api/webhooks/gitea/{id}", s.receiveGiteaWebhook)
	r.Post("/api/webhooks/github/{id}", s.receiveGitHubWebhook)

	r.Group(func(r chi.Router) {
		r.Use(s.auth)
		r.Get("/api/auth/me", s.me)
		r.Post("/api/projects", s.createProject)
		r.Get("/api/projects", s.listProjects)
		r.Get("/api/project-templates", s.listProjectTemplates)
		r.Get("/api/project-templates/{id}", s.getProjectTemplate)
		r.Post("/api/project-templates/{id}/preview", s.previewProjectTemplate)
		r.Post("/api/project-templates/{id}/create-project", s.createProjectFromTemplate)
		r.Get("/api/project-template-runs", s.listProjectTemplateRuns)
		r.Post("/api/project-template-runs/{id}/retry-provision", s.retryProjectTemplateProvision)
		r.Get("/api/provider-accounts", s.listProviderAccounts)
		r.Post("/api/provider-accounts", s.createProviderAccount)
		r.Get("/api/provider-accounts/{id}", s.getProviderAccount)
		r.Patch("/api/provider-accounts/{id}", s.updateProviderAccount)
		r.Post("/api/provider-accounts/{id}/check", s.checkProviderAccount)
		r.Post("/api/provider-accounts/{id}/rotate-token-env", s.rotateProviderAccountTokenEnv)
		r.Get("/api/projects/{id}", s.getProject)
		r.Patch("/api/projects/{id}", s.updateProject)
		r.Get("/api/asset-graph-views", s.listAssetGraphViews)
		r.Post("/api/asset-graph-views", s.createAssetGraphView)
		r.Patch("/api/asset-graph-views/{id}", s.updateAssetGraphView)
		r.Delete("/api/asset-graph-views/{id}", s.deleteAssetGraphView)
		r.Get("/api/assets", s.listAssets)
		r.Get("/api/assets/graph", s.listAssetGraph)
		r.Post("/api/asset-relations", s.createAssetRelation)
		r.Delete("/api/asset-relations/{id}", s.deleteAssetRelation)
		r.Get("/api/assets/{id}/relations", s.listAssetRelations)
		r.Get("/api/assets/{id}/status-snapshots", s.listAssetStatusSnapshots)
		r.Get("/api/assets/{id}/dependencies", s.listAssetDependencies)
		r.Get("/api/projects/{id}/assets", s.listProjectAssets)
		r.Post("/api/projects/{id}/git-repositories", s.createGitRepository)
		r.Get("/api/projects/{id}/git-repositories", s.listGitRepositories)
		r.Get("/api/git-repositories/{id}", s.getGitRepository)
		r.Patch("/api/git-repositories/{id}", s.updateGitRepository)
		r.Post("/api/git-repositories/{id}/sync", s.createRepositorySync)
		r.Post("/api/git-repositories/{id}/tags", s.createRepositoryTag)
		r.Get("/api/git-repositories/{id}/repo-sync-assets", s.listRepoSyncAssets)
		r.Post("/api/git-repositories/{id}/repo-sync-assets", s.createRepoSyncAsset)
		r.Post("/api/git-repositories/{id}/remotes", s.createGitRemote)
		r.Get("/api/git-repositories/{id}/remotes", s.listGitRemotes)
		r.Get("/api/git-remotes/{id}", s.getGitRemote)
		r.Put("/api/git-remotes/{id}", s.updateGitRemote)
		r.Patch("/api/git-remotes/{id}", s.updateGitRemote)
		r.Get("/api/git-remotes/{id}/github-actions", s.listGitHubActions)
		r.Post("/api/git-remotes/{id}/sync", s.createRemoteOperation("repo.sync"))
		r.Post("/api/git-remotes/{id}/tag", s.createRemoteOperation("repo.tag"))
		r.Post("/api/git-remotes/{id}/github-actions/sync", s.createRemoteOperation("github.actions.sync"))
		r.Get("/api/repo-sync-runs", s.listRepoSyncRuns)
		r.Post("/api/repo-sync-runs/{id}/rerun", s.rerunRepoSyncRun)
		r.Get("/api/repo-sync-assets/{id}", s.getRepoSyncAsset)
		r.Patch("/api/repo-sync-assets/{id}", s.updateRepoSyncAsset)
		r.Post("/api/repo-sync-assets/{id}/archive", s.archiveRepoSyncAsset)
		r.Post("/api/repo-sync-assets/{id}/restore", s.restoreRepoSyncAsset)
		r.Post("/api/repo-sync-assets/{id}/run", s.runRepoSyncAsset)
		r.Get("/api/repo-tag-runs", s.listRepoTagRuns)
		r.Get("/api/operation-approvals", s.listOperationApprovals)
		r.Get("/api/operation-approvals/summary", s.getOperationApprovalSummary)
		r.Get("/api/operation-approvals/reminder-candidates", s.listOperationApprovalReminderCandidates)
		r.Get("/api/operation-approval-rules", s.listOperationApprovalRules)
		r.Post("/api/operation-approval-rules", s.createOperationApprovalRule)
		r.Patch("/api/operation-approval-rules/{id}", s.updateOperationApprovalRule)
		r.Get("/api/operation-approval-rules/{id}/audits", s.listOperationApprovalRuleAudits)
		r.Get("/api/operation-approval-views", s.listOperationApprovalViews)
		r.Post("/api/operation-approval-views", s.createOperationApprovalView)
		r.Patch("/api/operation-approval-views/{id}", s.updateOperationApprovalView)
		r.Delete("/api/operation-approval-views/{id}", s.deleteOperationApprovalView)
		r.Get("/api/operation-approvals/{id}", s.getOperationApproval)
		r.Post("/api/operation-approvals/{id}/approve", s.approveOperationApproval)
		r.Post("/api/operation-approvals/{id}/reject", s.rejectOperationApproval)
		r.Post("/api/operation-approvals/{id}/remind", s.remindOperationApproval)
		r.Post("/api/operation-approvals/{id}/delegations", s.createOperationApprovalDelegation)
		r.Post("/api/operation-approvals/{id}/delegations/{delegationID}/revoke", s.revokeOperationApprovalDelegation)
		r.Get("/api/operations", s.listOperations)
		r.Get("/api/worker-queue/summary", s.getWorkerQueueSummary)
		r.Get("/api/operations/{id}", s.getOperation)
		r.Get("/api/operations/{id}/logs", s.getOperationLogs)
		r.Get("/api/operations/{id}/logs/stream", s.streamOperationLogs)
		r.Post("/api/operations/{id}/cancel", s.cancelOperation)
		r.Post("/api/worker-nodes/test-job", s.createNodeTestJob)
		r.Get("/api/ai-runtimes", s.listAIRuntimes)
		r.Post("/api/ai-runtimes", s.createAIRuntime)
		r.Post("/api/ai-runtimes/{id}/verify", s.verifyAIRuntime)
		r.Post("/api/projects/{id}/agent/tasks", s.createAgentTask)
		r.Get("/api/projects/{id}/agent/tasks", s.listAgentTasks)
		r.Get("/api/agent/tasks/{id}", s.getAgentTask)
		r.Get("/api/agent/tasks/{id}/tool-calls", s.listAgentTaskToolCalls)
		r.Post("/api/agent/tasks/{id}/generate-plan", s.generatePlan)
		r.Post("/api/agent/tasks/{id}/approve-plan", s.approvePlan)
		r.Post("/api/agent/tasks/{id}/execute", s.executePlan)
		r.Post("/api/projects/{id}/argo/connections", s.createArgoConnection)
		r.Get("/api/projects/{id}/argo/connections", s.listArgoConnections)
		r.Post("/api/argo/connections/{id}/apps/sync", s.syncArgoApps)
		r.Get("/api/projects/{id}/argo/apps", s.listArgoApps)
		r.Get("/api/projects/{id}/deployment-targets", s.listDeploymentTargets)
		r.Get("/api/projects/{id}/deployment-records", s.listDeploymentRecords)
		r.Get("/api/projects/{id}/rollback-points", s.listRollbackPoints)
		r.Post("/api/projects/{id}/webhook-connections", s.createWebhookConnection)
		r.Get("/api/projects/{id}/webhook-connections", s.listWebhookConnections)
		r.Get("/api/projects/{id}/webhook-events", s.listWebhookEvents)
		r.Post("/api/webhook-connections/{id}/rotate-secret", s.rotateWebhookConnectionSecret)
		r.Post("/api/webhook-events/{id}/replay", s.replayWebhookEvent)
		r.Post("/api/projects/{id}/ssh-machines", s.createSSHMachine)
		r.Get("/api/projects/{id}/ssh-machines", s.listSSHMachines)
		r.Post("/api/ssh-machines/{id}/commands", s.createSSHCommand)
		r.Get("/api/ssh-command-runs", s.listSSHCommandRuns)
		r.Post("/api/projects/{id}/context/generate", s.generateContext)
	})

	r.Post("/api/worker-nodes/register", s.registerNode)
	r.Post("/api/worker-nodes/heartbeat", s.nodeHeartbeat)
	r.Post("/api/worker-nodes/jobs/claim", s.claimJob)
	r.Post("/api/worker-nodes/jobs/{id}/logs", s.nodeJobLog)
	r.Post("/api/worker-nodes/jobs/{id}/complete", s.nodeJobComplete)
	r.Post("/api/worker-nodes/jobs/{id}/fail", s.nodeJobFail)
	return r
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "authorization, content-type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type webhookRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]webhookRateBucket
}

type webhookRateBucket struct {
	count     int
	resetAt   time.Time
	updatedAt time.Time
}

func newWebhookRateLimiter(limit int, window time.Duration) *webhookRateLimiter {
	return &webhookRateLimiter{limit: limit, window: window, buckets: map[string]webhookRateBucket{}}
}

func (l *webhookRateLimiter) allow(key string, now time.Time) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.buckets[key]
	if bucket.resetAt.IsZero() || now.After(bucket.resetAt) {
		bucket = webhookRateBucket{resetAt: now.Add(l.window)}
	}
	bucket.count++
	bucket.updatedAt = now
	l.buckets[key] = bucket
	for bucketKey, stale := range l.buckets {
		if now.Sub(stale.updatedAt) > 2*l.window {
			delete(l.buckets, bucketKey)
		}
	}
	return bucket.count <= l.limit
}

func webhookRateLimitKey(r *http.Request, connectionID string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		host = "unknown"
	}
	return host + ":" + connectionID
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := s.store.UserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	token, err := s.signJWT(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r)})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenText := bearerTokenFromRequest(r)
		if tokenText == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token, err := jwt.Parse(tokenText, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(s.cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		sub, err := token.Claims.GetSubject()
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token subject")
			return
		}
		user, err := s.store.UserByID(r.Context(), sub)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	})
}

func bearerTokenFromRequest(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	if strings.HasSuffix(r.URL.Path, "/logs/stream") {
		return strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return ""
}

func (s *Server) signJWT(userID string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   userID,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	})
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

type userContextKey struct{}

func currentUser(r *http.Request) *User {
	user, _ := r.Context().Value(userContextKey{}).(*User)
	return user
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project"}, "create") {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start transaction")
		return
	}
	defer tx.Rollback()
	project, err := queryOne(r.Context(), tx, `
		INSERT INTO projects(name, slug, description)
		VALUES ($1, $2, $3)
		RETURNING *`, req.Name, req.Slug, req.Description)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create project")
		return
	}
	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO project_members(project_id, user_id, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT DO NOTHING`, project["id"], currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create project membership")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "project.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit project")
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project"}, "read") {
		return
	}
	user := currentUser(r)
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT p.*
		FROM projects p
		WHERE $1 OR EXISTS (
			SELECT 1 FROM project_members pm
			WHERE pm.project_id=p.id AND pm.user_id=$2
		)
		ORDER BY p.created_at DESC`, userCanReadAllProjects(user), userIDOrNil(user))
	writeQueryResult(w, items, err)
}

func (s *Server) listProjectTemplates(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "read") {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT *
		FROM project_templates
		WHERE ($1='' OR status=$1)
		ORDER BY updated_at DESC, name
		LIMIT 100`, status)
	writeQueryResult(w, items, err)
}

func (s *Server) getProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "read") {
		return
	}
	template, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM project_templates WHERE id=$1", chi.URLParam(r, "id"))
	writeQueryOne(w, template, err)
}

func (s *Server) previewProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "read") {
		return
	}
	template, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM project_templates WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	var req struct {
		Name        string         `json:"name"`
		Slug        string         `json:"slug"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	req.Slug = slugify(req.Slug)
	writeJSON(w, http.StatusOK, projectTemplatePreview(template, req.Name, req.Slug, req.Description, req.Parameters))
}

func (s *Server) createProjectFromTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "create") {
		return
	}
	templateID := chi.URLParam(r, "id")
	template, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM project_templates WHERE id=$1 AND status='active'", templateID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	var req struct {
		Name        string         `json:"name"`
		Slug        string         `json:"slug"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	req.Slug = slugify(req.Slug)
	if req.Parameters == nil {
		req.Parameters = map[string]any{}
	}
	input := map[string]any{
		"project_template_id": templateID,
		"template_slug":       template["slug"],
		"project_name":        req.Name,
		"project_slug":        req.Slug,
		"description":         req.Description,
		"parameters":          req.Parameters,
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start template operation")
		return
	}
	defer tx.Rollback()
	op, err := enqueueOperationTx(r.Context(), tx, "", "", "project.create_from_template", "create project "+req.Name+" from "+fmt.Sprint(template["name"]), input, []string{"template"}, "control-worker")
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not enqueue template operation")
		return
	}
	run, err := createProjectTemplateRunTx(r.Context(), tx, op, template, req.Name, req.Slug, req.Description, req.Parameters, currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create template run")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "project_template.create_operation") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit template operation")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operation": op, "run": run})
}

func createProjectTemplateRunTx(ctx context.Context, tx *sqlx.Tx, op, template map[string]any, projectName, projectSlug, description string, parameters map[string]any, actorID string) (map[string]any, error) {
	inputJSON, err := jsonParam(map[string]any{"description": description, "parameters": parameters})
	if err != nil {
		return nil, err
	}
	stepsJSON, err := jsonParam(templateRunSteps(template["steps"]))
	if err != nil {
		return nil, err
	}
	return queryOne(ctx, tx, `
		INSERT INTO project_template_runs(
			operation_run_id, project_template_id, requested_by, project_name, project_slug, input, steps
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)
		RETURNING *`,
		op["id"],
		template["id"],
		actorID,
		projectName,
		projectSlug,
		inputJSON,
		stepsJSON,
	)
}

func templateRunSteps(value any) []map[string]any {
	stepsAny, _ := value.([]any)
	if len(stepsAny) == 0 {
		return []map[string]any{
			{"key": "project", "title": "Create project asset", "status": "queued"},
			{"key": "repository", "title": "Create repository metadata", "status": "queued"},
			{"key": "remotes", "title": "Bind repository remotes", "status": "queued"},
			{"key": "repo_sync", "title": "Configure repo sync asset", "status": "queued"},
			{"key": "files", "title": "Plan starter repository files", "status": "queued"},
		}
	}
	steps := make([]map[string]any, 0, len(stepsAny))
	for _, item := range stepsAny {
		step := mapFromAny(item)
		if stringFromMap(step, "status") == "" {
			step["status"] = "queued"
		}
		steps = append(steps, step)
	}
	return steps
}

func projectTemplatePreview(template map[string]any, projectName, projectSlug, description string, parameters map[string]any) map[string]any {
	if parameters == nil {
		parameters = map[string]any{}
	}
	defaults := mapFromAny(template["defaults"])
	repoDefaults := mapFromAny(defaults["repository"])
	repoParams := mapFromAny(parameters["repository"])
	syncDefaults := mapFromAny(defaults["repo_sync"])
	syncParams := mapFromAny(parameters["repo_sync"])
	remotes := previewTemplateRemotes(defaults, parameters)
	repository := map[string]any{
		"name":           firstNonEmptyString(stringFromMap(repoParams, "name"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "name_suffix"), "service")),
		"repo_key":       firstNonEmptyString(stringFromMap(repoParams, "repo_key"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "repo_key_suffix"), "service")),
		"display_name":   firstNonEmptyString(stringFromMap(repoParams, "display_name"), templateDisplayName(projectName, stringFromMap(repoDefaults, "display_name_suffix"), "Service")),
		"repo_role":      firstNonEmptyString(stringFromMap(repoParams, "repo_role"), stringFromMap(defaults, "repo_role"), "code"),
		"default_branch": firstNonEmptyString(stringFromMap(repoParams, "default_branch"), stringFromMap(defaults, "default_branch"), "main"),
		"status":         "planned",
	}
	sourceRemoteKey := firstNonEmptyString(stringFromMap(syncParams, "source_remote_key"), stringFromMap(syncDefaults, "source_remote_key"))
	targetRemoteKey := firstNonEmptyString(stringFromMap(syncParams, "target_remote_key"), stringFromMap(syncDefaults, "target_remote_key"))
	sourceRemoteID := firstNonEmptyString(stringFromMap(syncParams, "source_remote_id"), previewRemoteIDByKey(remotes, sourceRemoteKey))
	targetRemoteID := firstNonEmptyString(stringFromMap(syncParams, "target_remote_id"), previewRemoteIDByKey(remotes, targetRemoteKey))
	repoSyncStatus := "planned"
	repoSyncReason := "source_remote_id and target_remote_id are required after remotes are attached"
	if sourceRemoteID != "" && targetRemoteID != "" {
		if sourceRemoteID == targetRemoteID {
			repoSyncReason = "source_remote_id and target_remote_id must be different"
		} else {
			repoSyncStatus = "ready_for_remote_validation"
			repoSyncReason = "remote ownership will be validated when the template operation completes"
		}
	}
	repoSync := map[string]any{
		"name":              firstNonEmptyString(stringFromMap(syncParams, "name"), stringFromMap(syncDefaults, "name"), "default mirror"),
		"trigger_mode":      firstNonEmptyString(stringFromMap(syncParams, "trigger_mode"), stringFromMap(syncDefaults, "trigger_mode"), "manual"),
		"sync_mode":         firstNonEmptyString(stringFromMap(syncParams, "sync_mode"), stringFromMap(syncDefaults, "sync_mode"), "selected_refs"),
		"transport":         firstNonEmptyString(stringFromMap(syncParams, "transport"), stringFromMap(syncDefaults, "transport"), "ssh"),
		"driver":            firstNonEmptyString(stringFromMap(syncParams, "driver"), stringFromMap(syncDefaults, "driver"), "projectops_worker_git_ssh"),
		"enabled":           syncParams["enabled"],
		"source_remote_id":  sourceRemoteID,
		"target_remote_id":  targetRemoteID,
		"source_remote_key": sourceRemoteKey,
		"target_remote_key": targetRemoteKey,
		"status":            repoSyncStatus,
		"reason":            repoSyncReason,
	}
	if repoSync["enabled"] == nil {
		if enabled, ok := syncDefaults["enabled"].(bool); ok {
			repoSync["enabled"] = enabled
		} else {
			repoSync["enabled"] = false
		}
	}
	return map[string]any{
		"template": map[string]any{
			"id":      template["id"],
			"slug":    template["slug"],
			"name":    template["name"],
			"version": template["version"],
			"status":  template["status"],
		},
		"project": map[string]any{
			"name":        projectName,
			"slug":        projectSlug,
			"description": description,
		},
		"repository": repository,
		"remotes":    remotes,
		"repo_sync":  repoSync,
		"files":      previewTemplateFiles(template, projectName, projectSlug, repository, defaults, parameters),
		"steps":      templateRunSteps(template["steps"]),
		"defaults":   defaults,
		"parameters": parameters,
	}
}

func previewTemplateFiles(template map[string]any, projectName, projectSlug string, repository, defaults, parameters map[string]any) []map[string]any {
	items := templateFileItems(defaults, parameters)
	run := map[string]any{"template_slug": template["slug"]}
	project := map[string]any{"name": projectName, "slug": projectSlug}
	files := make([]map[string]any, 0, len(items))
	for _, item := range items {
		path := safeTemplateFilePath(stringFromMap(item, "path"))
		if path == "" {
			continue
		}
		files = append(files, map[string]any{
			"path":    path,
			"kind":    firstNonEmptyString(stringFromMap(item, "kind"), "text"),
			"content": renderTemplateFileContent(stringFromMap(item, "content"), run, project, repository),
			"status":  "planned",
		})
	}
	return files
}

func previewTemplateRemotes(defaults, parameters map[string]any) []map[string]any {
	items := templateRemoteItems(defaults, parameters)
	remotes := make([]map[string]any, 0, len(items))
	for _, item := range items {
		remoteKey := firstNonEmptyString(stringFromMap(item, "remote_key"), stringFromMap(item, "name"))
		name := firstNonEmptyString(stringFromMap(item, "name"), remoteKey)
		kind := firstNonEmptyString(stringFromMap(item, "kind"), stringFromMap(item, "provider_type"), "git")
		urls := stringSliceFromAny(item["urls"])
		remoteURL := stringFromMap(item, "remote_url")
		if remoteURL == "" && len(urls) > 0 {
			remoteURL = urls[0]
		}
		remotes = append(remotes, map[string]any{
			"id":                    "remote_key:" + remoteKey,
			"name":                  name,
			"remote_key":            remoteKey,
			"kind":                  kind,
			"provider_type":         firstNonEmptyString(stringFromMap(item, "provider_type"), kind),
			"provider_account_id":   stringFromMap(item, "provider_account_id"),
			"provider_account_name": stringFromMap(item, "provider_account_name"),
			"remote_url":            remoteURL,
			"web_url":               stringFromMap(item, "web_url"),
			"remote_role":           firstNonEmptyString(stringFromMap(item, "remote_role"), stringFromMap(item, "role"), "mirror"),
			"default_branch":        firstNonEmptyString(stringFromMap(item, "default_branch"), "main"),
			"sync_enabled":          boolDefaultFromMap(item, "sync_enabled", true),
			"is_primary":            boolFromMap(item, "is_primary"),
			"protected":             boolFromMap(item, "protected"),
			"status":                "planned",
		})
	}
	return remotes
}

func previewRemoteIDByKey(remotes []map[string]any, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, remote := range remotes {
		if stringFromMap(remote, "remote_key") == key || stringFromMap(remote, "name") == key {
			return stringFromMap(remote, "id")
		}
	}
	return ""
}

func (s *Server) listProjectTemplateRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template_run"}, "read") {
		return
	}
	user := currentUser(r)
	var items []map[string]any
	var err error
	if userCanReadAllProjects(user) {
		items, err = queryMaps(r.Context(), s.store.DB, `
			SELECT ptr.*, pt.name AS template_name, op.status AS operation_status
			FROM project_template_runs ptr
			LEFT JOIN project_templates pt ON pt.id=ptr.project_template_id
			LEFT JOIN operation_runs op ON op.id=ptr.operation_run_id
			ORDER BY ptr.created_at DESC
			LIMIT 100`)
	} else {
		items, err = queryMaps(r.Context(), s.store.DB, `
			SELECT ptr.*, pt.name AS template_name, op.status AS operation_status
			FROM project_template_runs ptr
			LEFT JOIN project_templates pt ON pt.id=ptr.project_template_id
			LEFT JOIN operation_runs op ON op.id=ptr.operation_run_id
			WHERE ptr.requested_by=$1
				OR EXISTS (
					SELECT 1 FROM project_members pm
					WHERE pm.project_id=ptr.project_id AND pm.user_id=$1
				)
			ORDER BY ptr.created_at DESC
			LIMIT 100`, userIDOrNil(user))
	}
	writeQueryResult(w, items, err)
}

func (s *Server) retryProjectTemplateProvision(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "create") {
		return
	}
	user := currentUser(r)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start template provision retry")
		return
	}
	defer tx.Rollback()
	run, err := queryOne(r.Context(), tx, `
		SELECT ptr.*, pt.name AS template_name
		FROM project_template_runs ptr
		LEFT JOIN project_templates pt ON pt.id=ptr.project_template_id
		WHERE ptr.id=$1
			AND ($2 OR ptr.requested_by=$3 OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id=ptr.project_id AND pm.user_id=$3
			))
		FOR UPDATE OF ptr`,
		chi.URLParam(r, "id"),
		userCanReadAllProjects(user),
		userIDOrNil(user),
	)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !canRetryTemplateProvision(run) {
		writeError(w, http.StatusConflict, "template run is not eligible for repository provisioning retry")
		return
	}
	input := map[string]any{
		"project_template_run_id":   run["id"],
		"previous_operation_run_id": cleanOptionalID(fmt.Sprint(run["operation_run_id"])),
	}
	op, err := enqueueOperationTx(
		r.Context(),
		tx,
		cleanOptionalID(fmt.Sprint(run["project_id"])),
		"",
		"project.template_provision_retry",
		"retry template provisioning "+fmt.Sprint(run["project_name"]),
		input,
		[]string{"template"},
		"control-worker",
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not enqueue template provision retry")
		return
	}
	retryRequested, err := jsonParam(map[string]any{
		"retry_requested": map[string]any{
			"operation_run_id": op["id"],
			"requested_by":     userIDOrNil(user),
			"requested_at":     time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record template provision retry")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE project_template_runs
		SET status='queued',
			result=result || $2::jsonb,
			updated_at=now()
		WHERE id=$1`, run["id"], retryRequested); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record template provision retry")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "project_template.retry_operation") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit template provision retry")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operation": op, "run": run})
}

func canRetryTemplateProvision(run map[string]any) bool {
	if run == nil {
		return false
	}
	result := mapFromAny(run["result"])
	if provisioned, _ := result["repository_provisioned"].(bool); provisioned {
		return false
	}
	if cleanOptionalID(fmt.Sprint(run["project_id"])) == "" {
		return false
	}
	status := strings.TrimSpace(fmt.Sprint(run["status"]))
	return status == "failed" || status == "completed"
}

func (s *Server) listProviderAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account"}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT * FROM provider_accounts
		ORDER BY provider_type, name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":                  sanitizeProviderAccounts(items),
		"token_rotation_summary": providerAccountTokenRotationPlanSummary(items, time.Now().UTC()),
	})
}

func (s *Server) getProviderAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account", ID: chi.URLParam(r, "id")}, "read") {
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM provider_accounts WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, sanitizeProviderAccount(item))
}

func (s *Server) createProviderAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account"}, "create") {
		return
	}
	var req struct {
		Name         string         `json:"name"`
		ProviderType string         `json:"provider_type"`
		APIBaseURL   string         `json:"api_base_url"`
		WebBaseURL   string         `json:"web_base_url"`
		TokenEnv     string         `json:"token_env"`
		DefaultOwner string         `json:"default_owner"`
		Visibility   string         `json:"visibility"`
		Enabled      *bool          `json:"enabled"`
		Metadata     map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	input, err := validateProviderAccountInput(r.Context(), req.Name, req.ProviderType, req.APIBaseURL, req.WebBaseURL, req.TokenEnv, req.DefaultOwner, req.Visibility, req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	metadata, err := jsonParam(input.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start provider account transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO provider_accounts(name, provider_type, api_base_url, web_base_url, token_env, default_owner, visibility, enabled, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
		RETURNING *`,
		input.Name,
		input.ProviderType,
		input.APIBaseURL,
		input.WebBaseURL,
		input.TokenEnv,
		input.DefaultOwner,
		input.Visibility,
		enabled,
		metadata,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create provider account")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit provider account")
		return
	}
	writeJSON(w, http.StatusCreated, sanitizeProviderAccount(item))
}

func (s *Server) updateProviderAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account", ID: chi.URLParam(r, "id")}, "update") {
		return
	}
	current, err := loadProviderAccountConfigByID(r.Context(), s.store.DB, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	var req struct {
		Name         *string         `json:"name"`
		ProviderType *string         `json:"provider_type"`
		APIBaseURL   *string         `json:"api_base_url"`
		WebBaseURL   *string         `json:"web_base_url"`
		TokenEnv     *string         `json:"token_env"`
		DefaultOwner *string         `json:"default_owner"`
		Visibility   *string         `json:"visibility"`
		Enabled      *bool           `json:"enabled"`
		Metadata     *map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := firstNonEmptyString(stringPtrValue(req.Name), current.Name)
	providerType := firstNonEmptyString(stringPtrValue(req.ProviderType), current.ProviderType)
	apiBaseURL := firstNonEmptyString(stringPtrValue(req.APIBaseURL), current.APIBaseURL)
	webBaseURL := firstNonEmptyString(stringPtrValue(req.WebBaseURL), current.WebBaseURL)
	tokenEnv := firstNonEmptyString(stringPtrValue(req.TokenEnv), current.TokenEnv)
	defaultOwner := firstNonEmptyString(stringPtrValue(req.DefaultOwner), current.DefaultOwner)
	visibility := firstNonEmptyString(stringPtrValue(req.Visibility), current.Visibility)
	metadata := cloneMap(current.Metadata)
	if req.Metadata != nil {
		metadata = mergeMaps(metadata, *req.Metadata)
	}
	input, err := validateProviderAccountInput(r.Context(), name, providerType, apiBaseURL, webBaseURL, tokenEnv, defaultOwner, visibility, metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := current.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	metadataJSON, err := jsonParam(input.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start provider account transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE provider_accounts
		SET name=$2,
			provider_type=$3,
			api_base_url=$4,
			web_base_url=$5,
			token_env=$6,
			default_owner=$7,
			visibility=$8,
			enabled=$9,
			metadata=$10::jsonb,
			updated_at=now()
		WHERE id=$1 AND token_env=$11
		RETURNING *`,
		chi.URLParam(r, "id"),
		input.Name,
		input.ProviderType,
		input.APIBaseURL,
		input.WebBaseURL,
		input.TokenEnv,
		input.DefaultOwner,
		input.Visibility,
		enabled,
		metadataJSON,
		current.TokenEnv,
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusConflict, "provider account changed during update; retry")
			return
		}
		writeError(w, http.StatusBadRequest, "could not update provider account")
		return
	}
	if err := refreshGitRemotesForProviderAccount(r.Context(), tx, input, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "could not refresh provider account remotes")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.update") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit provider account")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeProviderAccount(item))
}

func (s *Server) checkProviderAccount(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "id")
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account", ID: accountID}, "update") {
		return
	}
	account, err := loadProviderAccountConfigByID(r.Context(), s.store.DB, accountID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	check := runProviderAccountCheck(r.Context(), account, newTemplateProviderHTTPClient())
	checkJSON, err := jsonParam(check)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider account check")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start provider account check transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE provider_accounts
		SET metadata=metadata || jsonb_build_object('provider_check', $2::jsonb),
			updated_at=now()
		WHERE id=$1 AND token_env=$3
		RETURNING *`,
		accountID,
		checkJSON,
		account.TokenEnv,
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusConflict, "provider account changed during check; retry")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not store provider account check")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.check") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit provider account check")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"check": check, "account": sanitizeProviderAccount(item)})
}

func (s *Server) rotateProviderAccountTokenEnv(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "id")
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account", ID: accountID}, "update") {
		return
	}
	account, err := loadProviderAccountConfigByID(r.Context(), s.store.DB, accountID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	var req struct {
		TokenEnv string `json:"token_env"`
		Reason   string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tokenEnv := strings.TrimSpace(req.TokenEnv)
	if tokenEnv == "" {
		writeError(w, http.StatusBadRequest, "token_env is required")
		return
	}
	if !safeTemplateProviderTokenEnv(account.ProviderType, tokenEnv) {
		writeError(w, http.StatusBadRequest, "token_env is not allowed for provider_type")
		return
	}
	next := providerAccountInput{
		Name:         account.Name,
		ProviderType: account.ProviderType,
		APIBaseURL:   account.APIBaseURL,
		WebBaseURL:   account.WebBaseURL,
		TokenEnv:     tokenEnv,
		DefaultOwner: account.DefaultOwner,
		Visibility:   account.Visibility,
		Metadata:     cloneMap(account.Metadata),
	}
	next.Metadata = providerAccountRotationMetadata(next.Metadata, account.TokenEnv, tokenEnv, strings.TrimSpace(req.Reason), currentUser(r))
	metadataJSON, err := jsonParam(next.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start provider token rotation transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE provider_accounts
		SET token_env=$2,
			metadata=$3::jsonb,
			updated_at=now()
		WHERE id=$1 AND token_env=$4
		RETURNING *`,
		accountID,
		tokenEnv,
		metadataJSON,
		account.TokenEnv,
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusConflict, "provider account changed during token rotation; retry")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not rotate provider token env")
		return
	}
	if err := refreshGitRemotesForProviderAccount(r.Context(), tx, next, accountID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not refresh provider account remotes")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.rotate_token_env") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit provider token rotation")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeProviderAccount(item))
}

type providerAccountInput struct {
	Name         string
	ProviderType string
	APIBaseURL   string
	WebBaseURL   string
	TokenEnv     string
	DefaultOwner string
	Visibility   string
	Metadata     map[string]any
}

func validateProviderAccountInput(ctx context.Context, name, providerType, apiBaseURL, webBaseURL, tokenEnv, defaultOwner, visibility string, metadata map[string]any) (providerAccountInput, error) {
	input := providerAccountInput{
		Name:         strings.TrimSpace(name),
		ProviderType: strings.ToLower(strings.TrimSpace(providerType)),
		APIBaseURL:   strings.TrimRight(strings.TrimSpace(apiBaseURL), "/"),
		WebBaseURL:   strings.TrimRight(strings.TrimSpace(webBaseURL), "/"),
		TokenEnv:     strings.TrimSpace(tokenEnv),
		DefaultOwner: strings.TrimSpace(defaultOwner),
		Visibility:   strings.ToLower(strings.TrimSpace(visibility)),
		Metadata:     metadata,
	}
	if input.Name == "" {
		return input, fmt.Errorf("name is required")
	}
	if input.ProviderType != "github" && input.ProviderType != "gitea" {
		return input, fmt.Errorf("provider_type must be github or gitea")
	}
	if input.APIBaseURL == "" {
		input.APIBaseURL = defaultProviderAccountAPIBase(input.ProviderType)
	}
	if err := validateTemplateProviderURL(ctx, input.APIBaseURL); err != nil {
		return input, fmt.Errorf("api_base_url is unsafe: %w", err)
	}
	if input.WebBaseURL != "" {
		if err := validateTemplateProviderURL(ctx, input.WebBaseURL); err != nil {
			return input, fmt.Errorf("web_base_url is unsafe: %w", err)
		}
	}
	if input.TokenEnv == "" {
		input.TokenEnv = defaultTemplateProviderTokenEnv(input.ProviderType)
	}
	if !safeTemplateProviderTokenEnv(input.ProviderType, input.TokenEnv) {
		return input, fmt.Errorf("token_env is not allowed for provider_type")
	}
	if input.Visibility == "" {
		input.Visibility = "private"
	}
	if input.Visibility != "public" && input.Visibility != "private" && input.Visibility != "internal" {
		return input, fmt.Errorf("visibility must be public, private, or internal")
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	delete(input.Metadata, "token")
	delete(input.Metadata, "token_env")
	delete(input.Metadata, "secret")
	return input, nil
}

func providerAccountRotationMetadata(metadata map[string]any, oldEnv, newEnv, reason string, user *User) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	event := map[string]any{
		"rotated_at":             time.Now().UTC().Format(time.RFC3339),
		"previous_token_present": strings.TrimSpace(oldEnv) != "",
		"new_token_present":      strings.TrimSpace(newEnv) != "",
	}
	if reason != "" {
		event["reason"] = reason
	}
	if user != nil {
		event["rotated_by"] = user.ID
	}
	metadata["token_rotation"] = event
	delete(metadata, "token")
	delete(metadata, "token_env")
	delete(metadata, "secret")
	return metadata
}

func defaultProviderAccountAPIBase(provider string) string {
	if provider == "github" {
		return "https://api.github.com"
	}
	return ""
}

func refreshGitRemotesForProviderAccount(ctx context.Context, db sqlx.ExtContext, input providerAccountInput, accountID string) error {
	metadataPatch := map[string]any{
		"provider_account_id": accountID,
		"api_base_url":        input.APIBaseURL,
		"token_env":           input.TokenEnv,
		"visibility":          input.Visibility,
	}
	if input.DefaultOwner != "" {
		metadataPatch["owner"] = input.DefaultOwner
	}
	patchJSON, err := jsonParam(metadataPatch)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		UPDATE git_remotes
		SET metadata = metadata || $2::jsonb,
			updated_at = now()
		WHERE source_account_id=$1`,
		accountID,
		patchJSON,
	)
	return err
}

func runProviderAccountCheck(ctx context.Context, account providerAccountConfig, client *http.Client) map[string]any {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	check := map[string]any{
		"checked_at":        checkedAt,
		"provider_type":     account.ProviderType,
		"token_env_present": false,
		"status":            "error",
	}
	token := strings.TrimSpace(os.Getenv(account.TokenEnv))
	if token == "" {
		check["message"] = "provider token environment variable is not set"
		return check
	}
	check["token_env_present"] = true
	checkURL, ok := providerAccountCheckURL(account)
	if !ok {
		check["message"] = "provider account check endpoint is not configured"
		return check
	}
	if err := validateTemplateProviderURL(ctx, checkURL); err != nil {
		check["message"] = "provider check URL is unsafe: " + truncateProviderError(err.Error(), 240)
		return check
	}
	if client == nil {
		client = newTemplateProviderHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		check["message"] = truncateProviderError(err.Error(), 240)
		return check
	}
	switch account.ProviderType {
	case "github":
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	case "gitea":
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/json")
	default:
		check["message"] = "unsupported provider type"
		return check
	}
	res, err := client.Do(req)
	if err != nil {
		check["message"] = truncateProviderError(err.Error(), 240)
		return check
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	check["http_status"] = res.StatusCode
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message := providerErrorMessage(body)
		if message == "" {
			message = res.Status
		}
		check["message"] = truncateProviderError(message, 240)
		return check
	}
	payload := map[string]any{}
	_ = json.Unmarshal(body, &payload)
	check["status"] = "ok"
	check["message"] = "provider token verified"
	if actor := firstNonEmptyString(stringFromMap(payload, "login"), stringFromMap(payload, "username"), stringFromMap(payload, "user_name"), stringFromMap(payload, "name")); actor != "" {
		check["actor"] = actor
	}
	return check
}

func providerAccountCheckURL(account providerAccountConfig) (string, bool) {
	apiBase := strings.TrimRight(strings.TrimSpace(account.APIBaseURL), "/")
	if apiBase == "" {
		return "", false
	}
	switch account.ProviderType {
	case "github", "gitea":
		return apiBase + "/user", true
	default:
		return "", false
	}
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mergeMaps(base, overlay map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range overlay {
		base[key] = value
	}
	return base
}

type providerAccountConfig struct {
	ID           string
	Name         string
	ProviderType string
	APIBaseURL   string
	WebBaseURL   string
	TokenEnv     string
	DefaultOwner string
	Visibility   string
	Enabled      bool
	Metadata     map[string]any
}

func loadProviderAccountConfigByID(ctx context.Context, db sqlx.ExtContext, id string) (providerAccountConfig, error) {
	return loadProviderAccountConfig(ctx, db, "id=$1", id)
}

func loadProviderAccountConfigByName(ctx context.Context, db sqlx.ExtContext, name string) (providerAccountConfig, error) {
	return loadProviderAccountConfig(ctx, db, "name=$1", name)
}

func loadProviderAccountConfig(ctx context.Context, db sqlx.ExtContext, where string, arg any) (providerAccountConfig, error) {
	var cfg providerAccountConfig
	var metadataBytes []byte
	query := `
		SELECT id::text, name, provider_type, api_base_url, web_base_url, token_env, default_owner, visibility, enabled, metadata
		FROM provider_accounts
		WHERE ` + where
	err := db.QueryRowxContext(ctx, query, arg).Scan(
		&cfg.ID,
		&cfg.Name,
		&cfg.ProviderType,
		&cfg.APIBaseURL,
		&cfg.WebBaseURL,
		&cfg.TokenEnv,
		&cfg.DefaultOwner,
		&cfg.Visibility,
		&cfg.Enabled,
		&metadataBytes,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, ErrNotFound
	}
	if err != nil {
		return cfg, err
	}
	if len(metadataBytes) > 0 {
		_ = json.Unmarshal(metadataBytes, &cfg.Metadata)
	}
	if cfg.Metadata == nil {
		cfg.Metadata = map[string]any{}
	}
	return cfg, nil
}

func sanitizeProviderAccounts(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, sanitizeProviderAccount(item))
	}
	return out
}

func sanitizeProviderAccount(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	out := make(map[string]any, len(item)+2)
	for key, value := range item {
		if key == "token_env" {
			continue
		}
		out[key] = value
	}
	tokenEnv := rawStringFromMap(item, "token_env")
	out["token_configured"] = tokenEnv != ""
	out["masked_token_env"] = maskProviderTokenEnv(tokenEnv)
	out["token_rotation_status"] = providerAccountTokenRotationStatus(item, time.Now().UTC())
	return out
}

func providerAccountTokenRotationPlanSummary(items []map[string]any, now time.Time) map[string]any {
	counts := map[string]int{
		"fresh":   0,
		"soon":    0,
		"due":     0,
		"missing": 0,
		"unknown": 0,
	}
	nextAction := "No provider accounts configured."
	for _, item := range items {
		status := providerAccountTokenRotationStatus(item, now)
		key := strings.TrimSpace(fmt.Sprint(status["status"]))
		if _, ok := counts[key]; !ok {
			key = "unknown"
		}
		counts[key]++
	}
	actionRequired := counts["due"] + counts["missing"]
	if len(items) > 0 {
		switch {
		case counts["due"] > 0 && counts["missing"] > 0:
			nextAction = "Rotate due or missing provider token env values before external template provisioning."
		case counts["missing"] > 0:
			nextAction = "Configure missing provider token env values before external template provisioning."
		case counts["due"] > 0:
			nextAction = "Rotate due provider token env values before external template provisioning."
		case counts["soon"] > 0:
			nextAction = "Schedule provider token env rotation before the next due window."
		case counts["unknown"] > 0:
			nextAction = "Run a provider account check or rotate token env to establish rotation evidence."
		default:
			nextAction = "Provider token rotation evidence is fresh."
		}
	}
	return map[string]any{
		"total":           len(items),
		"fresh":           counts["fresh"],
		"soon":            counts["soon"],
		"due":             counts["due"],
		"missing":         counts["missing"],
		"unknown":         counts["unknown"],
		"action_required": actionRequired,
		"next_action":     nextAction,
	}
}

const (
	providerTokenRotationDueDays  = 90
	providerTokenRotationSoonDays = 75
)

func providerAccountTokenRotationStatus(item map[string]any, now time.Time) map[string]any {
	status := map[string]any{
		"status": "unknown",
		"source": "unknown",
	}
	if rawStringFromMap(item, "token_env") == "" {
		status["status"] = "missing"
		return status
	}
	metadata := mapFromAny(item["metadata"])
	rotation := mapFromAny(metadata["token_rotation"])
	lastRotatedAt, source := providerAccountRotationTime(item, rotation)
	if lastRotatedAt.IsZero() {
		return status
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lastRotatedAt = lastRotatedAt.UTC()
	daysSince := int(now.Sub(lastRotatedAt).Hours() / 24)
	if daysSince < 0 {
		daysSince = 0
	}
	dueAt := lastRotatedAt.Add(providerTokenRotationDueDays * 24 * time.Hour)
	daysUntilDue := int(math.Ceil(dueAt.Sub(now).Hours() / 24))
	tokenStatus := "fresh"
	if !now.Before(dueAt) {
		tokenStatus = "due"
		daysUntilDue = 0
	} else if daysSince >= providerTokenRotationSoonDays {
		tokenStatus = "soon"
	}
	status["status"] = tokenStatus
	status["source"] = source
	status["last_rotated_at"] = lastRotatedAt.Format(time.RFC3339)
	status["next_rotation_due_at"] = dueAt.Format(time.RFC3339)
	status["days_since_rotation"] = daysSince
	status["days_until_due"] = daysUntilDue
	return status
}

func providerAccountRotationTime(item, rotation map[string]any) (time.Time, string) {
	if rotatedAt := parseProviderAccountTime(rotation["rotated_at"]); !rotatedAt.IsZero() {
		return rotatedAt, "token_rotation"
	}
	if createdAt := parseProviderAccountTime(item["created_at"]); !createdAt.IsZero() {
		return createdAt, "created_at"
	}
	return time.Time{}, "unknown"
}

func parseProviderAccountTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case string:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	case []byte:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(string(typed)))
		if err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func maskProviderTokenEnv(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:8] + strings.Repeat("*", len(value)-12) + value[len(value)-4:]
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func rawStringFromMap(input map[string]any, key string) string {
	value, ok := input[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "read") {
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM projects WHERE id=$1", projectID)
	writeQueryOne(w, item, err)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Description *string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start project transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE projects
		SET name=COALESCE(NULLIF($2,''), name),
			slug=COALESCE(NULLIF($3,''), slug),
			description=COALESCE($4, description),
			updated_at=now()
		WHERE id=$1
		RETURNING *`,
		projectID,
		nullableString(req.Name),
		nullableString(req.Slug),
		nullableString(req.Description),
	)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "project.update") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit project")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	s.writeAssets(w, r, r.URL.Query().Get("project_id"))
}

func (s *Server) listProjectAssets(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ProjectID: projectID}, "read") {
		return
	}
	s.writeAssets(w, r, projectID)
}

func (s *Server) writeAssets(w http.ResponseWriter, r *http.Request, projectID string) {
	user := currentUser(r)
	if projectID != "" && !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, assetInventorySQL()+`
		SELECT *
		FROM asset_inventory
		WHERE ($1='' OR project_id=$1)
			AND ($2='' OR asset_type=$2)
			AND ($5='' OR name ILIKE $5 ESCAPE '\' OR display_name ILIKE $5 ESCAPE '\' OR external_id ILIKE $5 ESCAPE '\' OR source_table ILIKE $5 ESCAPE '\')
			AND (
				$3 OR project_id='' OR EXISTS (
					SELECT 1 FROM project_members pm
					WHERE pm.project_id::text=asset_inventory.project_id AND pm.user_id=$4
				)
			)
		ORDER BY updated_at DESC, name
		LIMIT 500`,
		projectID,
		r.URL.Query().Get("asset_type"),
		userCanReadAllProjects(user),
		userIDOrNil(user),
		likeContains(r.URL.Query().Get("q")),
	)
	writeQueryResult(w, items, err)
}

func (s *Server) listAssetGraph(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" && !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ProjectID: projectID}, "read") {
		return
	}
	user := currentUser(r)
	limit := assetGraphLimit(r.URL.Query().Get("limit"))
	nodes, err := queryMaps(r.Context(), s.store.DB, assetGraphNodesSQL(), projectID, r.URL.Query().Get("asset_type"), userCanReadAllProjects(user), userIDOrNil(user), likeContains(r.URL.Query().Get("q")), limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load asset graph nodes")
		return
	}
	truncated := len(nodes) > limit
	if truncated {
		nodes = nodes[:limit]
	}
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, fmt.Sprint(node["id"]))
	}
	edges := []map[string]any{}
	if len(ids) > 0 {
		edges, err = queryMaps(r.Context(), s.store.DB, assetRelationInventorySQL()+`
			SELECT *
			FROM asset_relation_inventory
			WHERE from_asset_id = ANY($1)
				AND to_asset_id = ANY($1)
			ORDER BY relation_type, from_asset_id, to_asset_id
			LIMIT 500`, pq.Array(ids))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load asset graph edges")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges, "truncated": truncated})
}

func assetGraphNodesSQL() string {
	return combinedAssetInventoryRelationSQL() + `,
		relation_degree_endpoints AS (
			SELECT from_asset_id AS asset_id, count(*)::int AS outgoing_relation_count, 0::int AS incoming_relation_count
			FROM asset_relation_inventory
			GROUP BY from_asset_id
			UNION ALL
			SELECT to_asset_id AS asset_id, 0::int AS outgoing_relation_count, count(*)::int AS incoming_relation_count
			FROM asset_relation_inventory
			GROUP BY to_asset_id
		),
		relation_degrees AS (
			SELECT
				asset_id,
				sum(outgoing_relation_count)::int AS outgoing_relation_count,
				sum(incoming_relation_count)::int AS incoming_relation_count,
				(sum(outgoing_relation_count) + sum(incoming_relation_count))::int AS relation_count
			FROM relation_degree_endpoints
			GROUP BY asset_id
		),
		ranked_asset_inventory AS (
			SELECT
				ai.*,
				COALESCE(rd.outgoing_relation_count, 0)::int AS outgoing_relation_count,
				COALESCE(rd.incoming_relation_count, 0)::int AS incoming_relation_count,
				COALESCE(rd.relation_count, 0)::int AS relation_count,
				CASE
					WHEN ai.risk_level='high' THEN 300
					WHEN ai.risk_level='warning' THEN 200
					WHEN ai.risk_level='normal' THEN 100
					ELSE 0
				END + LEAST(COALESCE(rd.relation_count, 0), 99)::int AS graph_rank
			FROM asset_inventory ai
			LEFT JOIN relation_degrees rd ON rd.asset_id=ai.id
		)
		SELECT *
		FROM ranked_asset_inventory
		WHERE ($1='' OR project_id=$1)
			AND ($2='' OR asset_type=$2)
			AND ($5='' OR name ILIKE $5 ESCAPE '\' OR display_name ILIKE $5 ESCAPE '\' OR external_id ILIKE $5 ESCAPE '\' OR source_table ILIKE $5 ESCAPE '\')
			AND (
				$3 OR project_id='' OR EXISTS (
					SELECT 1 FROM project_members pm
					WHERE pm.project_id::text=ranked_asset_inventory.project_id AND pm.user_id=$4
				)
			)
		ORDER BY graph_rank DESC, relation_count DESC, updated_at DESC, project_id NULLS FIRST, asset_type, name
		LIMIT $6`
}

func assetGraphLimit(raw string) int {
	limit := 80
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		limit = parsed
	}
	if limit < 1 {
		return 1
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func (s *Server) listAssetGraphViews(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, user_id, name, filters, created_at, updated_at
		FROM asset_graph_views
		WHERE user_id=$1
		ORDER BY updated_at DESC, name
		LIMIT 200`, userIDOrNil(currentUser(r)))
	writeQueryResult(w, items, err)
}

func (s *Server) createAssetGraphView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	var req struct {
		Name    string         `json:"name"`
		Filters map[string]any `json:"filters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 80 {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	filters, err := sanitizeAssetGraphViewFilters(req.Filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload, err := jsonParam(filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid filters")
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO asset_graph_views(user_id, name, filters)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, user_id, name, filters, created_at, updated_at`,
		userIDOrNil(currentUser(r)),
		name,
		payload,
	)
	if err != nil {
		if isUniqueViolation(err, "asset_graph_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an asset graph view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not create asset graph view")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateAssetGraphView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	var req struct {
		Name    string          `json:"name"`
		Filters json.RawMessage `json:"filters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if req.Name != "" && name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 80 {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	var payload any
	if len(req.Filters) > 0 && string(req.Filters) != "null" {
		var raw map[string]any
		if err := json.Unmarshal(req.Filters, &raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
		filters, err := sanitizeAssetGraphViewFilters(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		encoded, err := jsonParam(filters)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
		payload = encoded
	}
	item, err := queryOne(r.Context(), s.store.DB, `
		UPDATE asset_graph_views
		SET name=COALESCE(NULLIF($3, ''), name),
			filters=COALESCE($4::jsonb, filters),
			updated_at=now()
		WHERE id=$1 AND user_id=$2
		RETURNING id, user_id, name, filters, created_at, updated_at`,
		chi.URLParam(r, "id"),
		userIDOrNil(currentUser(r)),
		name,
		payload,
	)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		if isUniqueViolation(err, "asset_graph_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an asset graph view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not update asset graph view")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteAssetGraphView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	result, err := s.store.DB.ExecContext(r.Context(), `
		DELETE FROM asset_graph_views
		WHERE id=$1 AND user_id=$2`,
		chi.URLParam(r, "id"),
		userIDOrNil(currentUser(r)),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete asset graph view")
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete asset graph view")
		return
	}
	if count == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAssetRelations(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	user := currentUser(r)
	items, err := queryMaps(r.Context(), s.store.DB, assetRelationInventorySQL()+`
		SELECT *
		FROM asset_relation_inventory
		WHERE ($1='' OR from_asset_id=$1 OR to_asset_id=$1)
			AND ($2='' OR project_id=$2)
			AND (
				$3 OR project_id='' OR EXISTS (
					SELECT 1 FROM project_members pm
					WHERE pm.project_id::text=asset_relation_inventory.project_id AND pm.user_id=$4
				)
			)
		ORDER BY relation_type, from_asset_id, to_asset_id
		LIMIT 500`,
		chi.URLParam(r, "id"),
		r.URL.Query().Get("project_id"),
		userCanReadAllProjects(user),
		userIDOrNil(user),
	)
	writeQueryResult(w, items, err)
}

func (s *Server) listAssetStatusSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	assetID := chi.URLParam(r, "id")
	asset, err := queryOne(r.Context(), s.store.DB, `
		SELECT id, project_id
		FROM assets
		WHERE id=$1`, assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(asset["project_id"]))
	if projectID != "" && projectID != "<nil>" && !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ID: assetID, ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, asset_id, status, health, summary, raw, collected_at
		FROM asset_status_snapshots
		WHERE asset_id=$1
		ORDER BY collected_at DESC, id DESC
		LIMIT 50`, assetID)
	writeQueryResult(w, items, err)
}

func (s *Server) createAssetRelation(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "update") {
		return
	}
	var req struct {
		FromAssetID  string         `json:"from_asset_id"`
		ToAssetID    string         `json:"to_asset_id"`
		RelationType string         `json:"relation_type"`
		Metadata     map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.FromAssetID = strings.TrimSpace(req.FromAssetID)
	req.ToAssetID = strings.TrimSpace(req.ToAssetID)
	req.RelationType = cleanAssetRelationType(req.RelationType)
	if req.FromAssetID == "" || req.ToAssetID == "" || req.RelationType == "" {
		writeError(w, http.StatusBadRequest, "from_asset_id, to_asset_id, and relation_type are required")
		return
	}
	if req.FromAssetID == req.ToAssetID {
		writeError(w, http.StatusBadRequest, "from_asset_id and to_asset_id must differ")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create asset relation")
		return
	}
	defer tx.Rollback()
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "asset_relation.create") {
		return
	}
	fromAsset, err := canonicalAssetForRelation(r.Context(), tx, req.FromAssetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	toAsset, err := canonicalAssetForRelation(r.Context(), tx, req.ToAssetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := relationProjectID(fromAsset, toAsset)
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "at least one asset must belong to a project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset_relation", ProjectID: projectID}, "update") {
		return
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["source"] = "manual"
	metadata["created_by"] = userIDOrNil(currentUser(r))
	payload, err := jsonParam(metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO asset_relations(project_id, from_asset_id, to_asset_id, relation_type, metadata)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		ON CONFLICT (from_asset_id, to_asset_id, relation_type)
		DO UPDATE SET metadata=asset_relations.metadata || EXCLUDED.metadata
		RETURNING *`,
		projectID,
		fromAsset["id"],
		toAsset["id"],
		req.RelationType,
		payload,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create asset relation")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit asset relation")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func canonicalAssetForRelation(ctx context.Context, db sqlx.ExtContext, assetID string) (map[string]any, error) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return nil, ErrNotFound
	}
	if item, err := queryOne(ctx, db, "SELECT * FROM assets WHERE id::text=$1", assetID); err == nil {
		return item, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return queryOne(ctx, db, assetInventorySQL()+`
		SELECT a.*
		FROM asset_inventory ai
		JOIN assets a ON a.source_table=ai.source_table
			AND a.source_id::text=ai.source_id
			AND a.asset_type=ai.asset_type
		WHERE ai.id=$1
		LIMIT 1`, assetID)
}

func (s *Server) deleteAssetRelation(w http.ResponseWriter, r *http.Request) {
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete asset relation")
		return
	}
	defer tx.Rollback()
	relation, err := queryOne(r.Context(), tx, "SELECT * FROM asset_relations WHERE id=$1 FOR UPDATE", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanOptionalID(fmt.Sprint(relation["project_id"]))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "asset relation has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset_relation", ID: chi.URLParam(r, "id"), ProjectID: projectID}, "update") {
		return
	}
	metadata := mapFromAny(relation["metadata"])
	if stringFromMap(metadata, "source") != "manual" {
		writeError(w, http.StatusConflict, "only manual asset relations can be deleted")
		return
	}
	result, err := tx.ExecContext(r.Context(), "DELETE FROM asset_relations WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete asset relation")
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete asset relation")
		return
	}
	if count == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "asset_relation.delete") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit asset relation delete")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func cleanAssetRelationType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	if len(value) > 80 {
		value = value[:80]
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "_.-")
}

func relationProjectID(fromAsset, toAsset map[string]any) string {
	fromProjectID := cleanOptionalID(fmt.Sprint(fromAsset["project_id"]))
	toProjectID := cleanOptionalID(fmt.Sprint(toAsset["project_id"]))
	if fromProjectID != "" && toProjectID != "" && fromProjectID != toProjectID {
		return ""
	}
	if fromProjectID != "" {
		return fromProjectID
	}
	return toProjectID
}

func (s *Server) listAssetDependencies(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	user := currentUser(r)
	direction := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction")))
	if direction == "" {
		direction = "downstream"
	}
	if direction != "downstream" && direction != "upstream" {
		writeError(w, http.StatusBadRequest, "direction must be downstream or upstream")
		return
	}
	depth := intFromAny(r.URL.Query().Get("depth"), 4)
	if depth < 1 || depth > 8 {
		writeError(w, http.StatusBadRequest, "depth must be between 1 and 8")
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, assetDependencySQL(direction)+`
		SELECT *
		FROM asset_dependency_paths
		WHERE (
			$4 OR project_id='' OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id::text=asset_dependency_paths.project_id AND pm.user_id=$5
			)
		)
		ORDER BY depth, path_text
		LIMIT 501`,
		chi.URLParam(r, "id"),
		r.URL.Query().Get("project_id"),
		depth,
		userCanReadAllProjects(user),
		userIDOrNil(user),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	truncated := len(items) > 500
	if truncated {
		items = items[:500]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "truncated": truncated})
}

func (s *Server) createGitRepository(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name          string `json:"name"`
		RepoKey       string `json:"repo_key"`
		DisplayName   string `json:"display_name"`
		RepoRole      string `json:"repo_role"`
		Status        string `json:"status"`
		Description   string `json:"description"`
		DefaultBranch string `json:"default_branch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RepoKey == "" {
		req.RepoKey = slugify(req.Name)
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.RepoRole == "" {
		req.RepoRole = "code"
	}
	if req.Status == "" {
		req.Status = "active"
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start git repository transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO project_git_repositories(project_id, name, repo_key, display_name, repo_role, status, description, default_branch)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING *`,
		projectID,
		req.Name,
		req.RepoKey,
		req.DisplayName,
		req.RepoRole,
		req.Status,
		req.Description,
		req.DefaultBranch,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "git_repository.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit git repository")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listGitRepositories(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT * FROM project_git_repositories WHERE project_id=$1 ORDER BY created_at DESC`, projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) getGitRepository(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "read") {
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	writeQueryOne(w, item, err)
}

func (s *Server) updateGitRepository(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		Name          *string `json:"name"`
		RepoKey       *string `json:"repo_key"`
		DisplayName   *string `json:"display_name"`
		RepoRole      *string `json:"repo_role"`
		Status        *string `json:"status"`
		Description   *string `json:"description"`
		DefaultBranch *string `json:"default_branch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start git repository transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE project_git_repositories
		SET name=COALESCE(NULLIF($2,''), name),
			repo_key=COALESCE(NULLIF($3,''), repo_key),
			display_name=COALESCE($4, display_name),
			repo_role=COALESCE(NULLIF($5,''), repo_role),
			status=COALESCE(NULLIF($6,''), status),
			description=COALESCE($7, description),
			default_branch=COALESCE(NULLIF($8,''), default_branch),
			updated_at=now()
		WHERE id=$1
		RETURNING *`,
		repoID,
		nullableString(req.Name),
		nullableString(req.RepoKey),
		nullableString(req.DisplayName),
		nullableString(req.RepoRole),
		nullableString(req.Status),
		nullableString(req.Description),
		nullableString(req.DefaultBranch),
	)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "git_repository.update") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit git repository")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createRepositorySync(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "repo.sync") {
		return
	}
	var req struct {
		SourceRemoteID  string         `json:"source_remote_id"`
		TargetRemoteIDs []string       `json:"target_remote_ids"`
		Refs            map[string]any `json:"refs"`
		AllowForce      bool           `json:"allow_force"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SourceRemoteID == "" {
		writeError(w, http.StatusBadRequest, "source_remote_id is required")
		return
	}
	repo, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	source, err := remoteForRepository(r.Context(), s.store.DB, repoID, req.SourceRemoteID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	targetIDs := req.TargetRemoteIDs
	if len(targetIDs) == 0 {
		targetIDs, err = defaultTargetRemoteIDs(r.Context(), s.store.DB, repoID, req.SourceRemoteID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not select target remotes")
			return
		}
	}
	if len(targetIDs) == 0 {
		writeError(w, http.StatusBadRequest, "target_remote_ids is required")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start sync transaction")
		return
	}
	defer tx.Rollback()
	var runs []map[string]any
	for _, targetID := range targetIDs {
		if targetID == req.SourceRemoteID {
			writeError(w, http.StatusBadRequest, "target_remote_ids cannot include source_remote_id")
			return
		}
		target, err := remoteForRepository(r.Context(), tx, repoID, targetID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "target remote not found in repository")
			return
		}
		run, err := s.enqueueRepoSyncRun(r.Context(), tx, repo, source, target, req.Refs, req.AllowForce, currentUser(r).ID, "")
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not enqueue sync")
			return
		}
		runs = append(runs, run)
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repository_sync.enqueue") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit sync runs")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": runs})
}

func (s *Server) createRepositoryTag(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	var req repositoryTagRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TagName == "" {
		writeError(w, http.StatusBadRequest, "tag_name is required")
		return
	}
	payload := map[string]any{"kind": "repository_tag", "repo_id": repoID, "request": req}
	if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "repo.tag", "tag "+req.TagName, payload) {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start tag transaction")
		return
	}
	defer tx.Rollback()
	runs, err := s.enqueueRepositoryTagRuns(r.Context(), tx, repoID, req, currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repository_tag.enqueue") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit tag runs")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": runs})
}

func (s *Server) listRepoSyncAssets(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	includeArchived := boolQuery(r, "include_archived")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT rsa.*,
			source.name AS source_remote_name,
			target.name AS target_remote_name,
			analytics.total_runs,
			analytics.completed_runs,
			analytics.failed_runs,
			analytics.running_runs,
			analytics.success_rate,
			analytics.last_run_at,
			analytics.last_success_at,
			analytics.last_failure_at,
			analytics.last_failure_message,
			analytics.avg_duration_seconds
		FROM repo_sync_assets rsa
		JOIN git_remotes source ON source.id=rsa.source_remote_id
		JOIN git_remotes target ON target.id=rsa.target_remote_id
		LEFT JOIN LATERAL (`+repoSyncAssetAnalyticsSQL("rsa")+`) analytics ON true
		WHERE rsa.project_git_repository_id=$1
			AND ($2 OR rsa.archived_at IS NULL)
		ORDER BY rsa.created_at DESC`, repoID, includeArchived)
	annotateRepoSyncAssetRisks(items)
	writeQueryResult(w, items, err)
}

func annotateRepoSyncAssetRisks(items []map[string]any) {
	for _, item := range items {
		severity, summary := repoSyncAssetRisk(item)
		item["risk_severity"] = severity
		item["risk_summary"] = summary
	}
}

func repoSyncAssetRisk(asset map[string]any) (string, string) {
	if repoSyncAssetArchived(asset) {
		return "warning", "archived; restore before running"
	}
	if enabled, ok := asset["enabled"].(bool); ok && !enabled {
		return "warning", "disabled; manual and webhook runs are paused"
	}
	if signalSeverityFromSync(asset["last_sync_status"]) == "danger" {
		return "danger", "last sync failed"
	}
	runningRuns := intFromAny(asset["running_runs"], 0)
	if runningRuns >= 3 {
		return "danger", fmt.Sprintf("%d active runs", runningRuns)
	}
	if runningRuns > 0 {
		return "warning", fmt.Sprintf("%d active runs", runningRuns)
	}
	failedRuns := intFromAny(asset["failed_runs"], 0)
	if failedRuns >= 5 {
		return "danger", fmt.Sprintf("%d failed runs", failedRuns)
	}
	totalRuns := intFromAny(asset["total_runs"], 0)
	successRate := floatFromAny(asset["success_rate"], 100)
	if totalRuns >= 5 && successRate < 50 {
		return "danger", fmt.Sprintf("%.0f%% success rate", successRate)
	}
	if failedRuns > 0 {
		return "warning", fmt.Sprintf("%d failed runs", failedRuns)
	}
	if totalRuns >= 5 && successRate < 80 {
		return "warning", fmt.Sprintf("%.0f%% success rate", successRate)
	}
	return "ok", "healthy"
}

func repoSyncAssetAnalyticsSQL(assetAlias string) string {
	alias := strings.TrimSpace(assetAlias)
	if alias == "" {
		alias = "rsa"
	}
	return fmt.Sprintf(`
		SELECT
			count(rsr.id)::int AS total_runs,
			count(*) FILTER (WHERE rsr.status='completed')::int AS completed_runs,
			count(*) FILTER (WHERE rsr.status='failed')::int AS failed_runs,
			count(*) FILTER (WHERE rsr.status IN ('queued', 'running', 'provisioning'))::int AS running_runs,
			CASE
				WHEN count(rsr.id)=0 THEN 0
				ELSE round((count(*) FILTER (WHERE rsr.status='completed')::numeric / count(rsr.id)::numeric) * 100, 2)
			END AS success_rate,
			max(rsr.created_at) AS last_run_at,
			max(rsr.finished_at) FILTER (WHERE rsr.status='completed') AS last_success_at,
			max(rsr.finished_at) FILTER (WHERE rsr.status='failed') AS last_failure_at,
			(
				SELECT recent.error_message
				FROM repo_sync_runs recent
				WHERE recent.repo_sync_asset_id=%[1]s.id
					AND recent.status='failed'
					AND recent.error_message <> ''
				ORDER BY recent.created_at DESC
				LIMIT 1
			) AS last_failure_message,
			round(avg(EXTRACT(EPOCH FROM (rsr.finished_at - rsr.started_at))) FILTER (WHERE rsr.started_at IS NOT NULL AND rsr.finished_at IS NOT NULL), 2) AS avg_duration_seconds
		FROM repo_sync_runs rsr
		WHERE rsr.repo_sync_asset_id=%[1]s.id`, alias)
}

func (s *Server) createRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name           string         `json:"name"`
		SourceRemoteID string         `json:"source_remote_id"`
		TargetRemoteID string         `json:"target_remote_id"`
		TriggerMode    string         `json:"trigger_mode"`
		SyncMode       string         `json:"sync_mode"`
		Transport      string         `json:"transport"`
		Driver         string         `json:"driver"`
		Refs           map[string]any `json:"refs"`
		Enabled        *bool          `json:"enabled"`
		Metadata       map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SourceRemoteID == "" || req.TargetRemoteID == "" {
		writeError(w, http.StatusBadRequest, "source_remote_id and target_remote_id are required")
		return
	}
	if req.SourceRemoteID == req.TargetRemoteID {
		writeError(w, http.StatusBadRequest, "source and target remotes must differ")
		return
	}
	source, err := remoteForRepository(r.Context(), s.store.DB, repoID, req.SourceRemoteID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "source remote not found in repository")
		return
	}
	target, err := remoteForRepository(r.Context(), s.store.DB, repoID, req.TargetRemoteID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "target remote not found in repository")
		return
	}
	if req.Name == "" {
		req.Name = fmt.Sprint(source["name"]) + " to " + fmt.Sprint(target["name"])
	}
	if req.TriggerMode == "" {
		req.TriggerMode = "manual"
	}
	if req.SyncMode == "" {
		req.SyncMode = "selected_refs"
	}
	if req.Transport == "" {
		req.Transport = "ssh"
	}
	if req.Driver == "" {
		req.Driver = "projectops_worker_git_ssh"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	refs, err := jsonParam(req.Refs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid refs")
		return
	}
	metadata, err := jsonParam(req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start repo sync asset transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO repo_sync_assets(
			project_id, project_git_repository_id, name, source_remote_id, target_remote_id,
			trigger_mode, sync_mode, transport, driver, refs, enabled, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12::jsonb)
		RETURNING *`,
		projectID,
		repoID,
		req.Name,
		req.SourceRemoteID,
		req.TargetRemoteID,
		req.TriggerMode,
		req.SyncMode,
		req.Transport,
		req.Driver,
		refs,
		enabled,
		metadata,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit repo sync asset")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	user := currentUser(r)
	asset, err := queryOne(r.Context(), s.store.DB, `
		SELECT rsa.*,
			pgr.name AS repository_name,
			source.name AS source_remote_name,
			target.name AS target_remote_name,
			analytics.total_runs,
			analytics.completed_runs,
			analytics.failed_runs,
			analytics.running_runs,
			analytics.success_rate,
			analytics.last_run_at,
			analytics.last_success_at,
			analytics.last_failure_at,
			analytics.last_failure_message,
			analytics.avg_duration_seconds
		FROM repo_sync_assets rsa
		JOIN project_git_repositories pgr ON pgr.id=rsa.project_git_repository_id
		JOIN git_remotes source ON source.id=rsa.source_remote_id
		JOIN git_remotes target ON target.id=rsa.target_remote_id
		LEFT JOIN LATERAL (`+repoSyncAssetAnalyticsSQL("rsa")+`) analytics ON true
		WHERE rsa.id=$1
			AND (
				$2 OR EXISTS (
					SELECT 1 FROM project_members pm
					WHERE pm.project_id=rsa.project_id AND pm.user_id=$3
				)
			)`, assetID, userCanReadAllProjects(user), userIDOrNil(user))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(asset["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "read") {
		return
	}
	runs, err := queryMaps(r.Context(), s.store.DB, `
		SELECT rsr.id,
			rsr.operation_run_id,
			rsr.project_id,
			rsr.project_git_repository_id,
			rsr.repo_sync_asset_id,
			rsr.source_remote_id,
			rsr.target_remote_id,
			rsr.ref,
			rsr.before_sha,
			rsr.after_sha,
			rsr.status,
			rsr.error_message,
			rsr.started_at,
			rsr.finished_at,
			rsr.created_at,
			op.operation_type,
			op.title AS operation_title,
			op.status AS operation_status,
			op.error AS operation_error
		FROM repo_sync_runs rsr
		LEFT JOIN operation_runs op ON op.id=rsr.operation_run_id
		WHERE rsr.repo_sync_asset_id=$1
		ORDER BY rsr.created_at DESC
		LIMIT 50`, assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list repo sync runs")
		return
	}
	events, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id,
			webhook_connection_id,
			project_id,
			provider,
			event_type,
			delivery_id,
			signature_valid,
			matched_repo_sync_asset_id,
			operation_run_id,
			status,
			error_message,
			received_at,
			processed_at
		FROM webhook_events
		WHERE matched_repo_sync_asset_id=$1
		ORDER BY received_at DESC
		LIMIT 50`, assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list webhook events")
		return
	}
	logs, err := queryMaps(r.Context(), s.store.DB, `
		SELECT ol.id,
			ol.operation_run_id,
			ol.worker_job_id,
			ol.level,
			ol.message,
			ol.created_at
		FROM operation_logs ol
		JOIN repo_sync_runs rsr ON rsr.operation_run_id=ol.operation_run_id
		WHERE rsr.repo_sync_asset_id=$1
		ORDER BY ol.created_at DESC
		LIMIT 100`, assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list operation logs")
		return
	}
	trend, err := queryMaps(r.Context(), s.store.DB, repoSyncAssetTrendSQL(), assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load repo sync trend")
		return
	}
	capacity, err := s.repoSyncAssetCapacitySignals(r.Context(), asset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load repo sync capacity signals")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"asset":            asset,
		"runs":             runs,
		"webhook_events":   events,
		"operation_logs":   logs,
		"trend":            trend,
		"capacity_signals": capacity,
	})
}

func repoSyncAssetTrendSQL() string {
	return `
		SELECT
			to_char(day_bucket, 'YYYY-MM-DD') AS day,
			count(*)::int AS total_runs,
			count(*) FILTER (WHERE status='completed')::int AS completed_runs,
			count(*) FILTER (WHERE status='failed')::int AS failed_runs,
			count(*) FILTER (WHERE status IN ('queued', 'running', 'provisioning'))::int AS active_runs,
			round(avg(EXTRACT(EPOCH FROM (finished_at - started_at))) FILTER (WHERE started_at IS NOT NULL AND finished_at IS NOT NULL), 2) AS avg_duration_seconds
		FROM (
			SELECT date_trunc('day', created_at) AS day_bucket, status, started_at, finished_at
			FROM repo_sync_runs
			WHERE repo_sync_asset_id=$1
				AND created_at >= now() - interval '14 days'
		) daily
		GROUP BY day_bucket
		ORDER BY day_bucket DESC
		LIMIT 14`
}

func (s *Server) repoSyncAssetCapacitySignals(ctx context.Context, asset map[string]any) ([]map[string]any, error) {
	assetID := strings.TrimSpace(fmt.Sprint(asset["id"]))
	sourceID := strings.TrimSpace(fmt.Sprint(asset["source_remote_id"]))
	targetID := strings.TrimSpace(fmt.Sprint(asset["target_remote_id"]))
	raw, err := queryOne(ctx, s.store.DB, repoSyncAssetCapacitySQL(),
		assetID,
	)
	if err != nil {
		return nil, err
	}
	return repoSyncCapacitySignals(asset, raw, sourceID, targetID), nil
}

func repoSyncAssetCapacitySQL() string {
	return `
		SELECT
			source.provider_type AS source_provider,
			target.provider_type AS target_provider,
			source.last_sync_status AS source_last_sync_status,
			target.last_sync_status AS target_last_sync_status,
			count(DISTINCT rsr.id) FILTER (WHERE rsr.status IN ('queued', 'running', 'provisioning'))::int AS active_runs,
			count(DISTINCT rsr.id) FILTER (WHERE rsr.status='failed' AND rsr.created_at >= now() - interval '7 days')::int AS failed_runs_7d,
			count(DISTINCT we.id) FILTER (WHERE we.status IN ('failed', 'rejected') AND we.received_at >= now() - interval '7 days')::int AS webhook_failures_7d,
			count(DISTINCT gar.id) FILTER (WHERE gar.created_at >= now() - interval '24 hours')::int AS github_runs_24h,
			COALESCE(pair_pressure.active_runs, 0)::int AS provider_pair_active_runs,
			COALESCE(pair_pressure.runs_24h, 0)::int AS provider_pair_runs_24h,
			COALESCE(pair_pressure.failed_runs_24h, 0)::int AS provider_pair_failed_runs_24h,
			max(we.error_message) FILTER (WHERE we.status IN ('failed', 'rejected') AND we.error_message <> '') AS last_webhook_error
		FROM repo_sync_assets rsa
		JOIN git_remotes source ON source.id=rsa.source_remote_id
		JOIN git_remotes target ON target.id=rsa.target_remote_id
		LEFT JOIN repo_sync_runs rsr ON rsr.repo_sync_asset_id=rsa.id
		LEFT JOIN webhook_events we ON we.matched_repo_sync_asset_id=rsa.id
		LEFT JOIN github_action_runs gar ON gar.git_remote_id IN (rsa.source_remote_id, rsa.target_remote_id)
		LEFT JOIN LATERAL (
			SELECT
				count(DISTINCT pair_runs.id) FILTER (WHERE pair_runs.status IN ('queued', 'running', 'provisioning'))::int AS active_runs,
				count(DISTINCT pair_runs.id) FILTER (WHERE pair_runs.created_at >= now() - interval '24 hours')::int AS runs_24h,
				count(DISTINCT pair_runs.id) FILTER (WHERE pair_runs.status='failed' AND pair_runs.created_at >= now() - interval '24 hours')::int AS failed_runs_24h
			FROM repo_sync_assets pair_asset
			JOIN git_remotes pair_source ON pair_source.id=pair_asset.source_remote_id
			JOIN git_remotes pair_target ON pair_target.id=pair_asset.target_remote_id
			LEFT JOIN repo_sync_runs pair_runs ON pair_runs.repo_sync_asset_id=pair_asset.id
			WHERE pair_source.provider_type=source.provider_type
				AND pair_target.provider_type=target.provider_type
		) pair_pressure ON true
		WHERE rsa.id=$1
		GROUP BY source.provider_type, target.provider_type, source.last_sync_status, target.last_sync_status,
			pair_pressure.active_runs, pair_pressure.runs_24h, pair_pressure.failed_runs_24h`
}

const (
	repoSyncCapacityActiveWarningThreshold       = 1
	repoSyncCapacityActiveDangerThreshold        = 3
	repoSyncCapacityFailure7dWarningThreshold    = 1
	repoSyncCapacityFailure7dDangerThreshold     = 5
	repoSyncCapacityWebhookWarningThreshold      = 1
	repoSyncCapacityWebhookDangerThreshold       = 3
	repoSyncCapacityGitHubVolumeWarningThreshold = 50
	repoSyncCapacityGitHubVolumeDangerThreshold  = 200
	repoSyncCapacityPairActiveWarningThreshold   = 3
	repoSyncCapacityPairActiveDangerThreshold    = 8
	repoSyncCapacityPairFailureWarningThreshold  = 1
	repoSyncCapacityPairFailureDangerThreshold   = 3
)

func repoSyncCapacitySignals(asset, raw map[string]any, sourceID, targetID string) []map[string]any {
	signals := []map[string]any{
		{
			"name":     "source provider",
			"status":   signalStatusFromSync(raw["source_last_sync_status"]),
			"severity": signalSeverityFromSync(raw["source_last_sync_status"]),
			"detail":   fmt.Sprintf("%s remote %s", firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["source_provider"])), "unknown"), sourceID),
		},
		{
			"name":     "target provider",
			"status":   signalStatusFromSync(raw["target_last_sync_status"]),
			"severity": signalSeverityFromSync(raw["target_last_sync_status"]),
			"detail":   fmt.Sprintf("%s remote %s", firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["target_provider"])), "unknown"), targetID),
		},
	}
	activeRuns := intFromAny(raw["active_runs"], 0)
	signals = append(signals, map[string]any{
		"name":     "sync capacity",
		"status":   activeRuns,
		"severity": severityForCount(activeRuns, repoSyncCapacityActiveWarningThreshold, repoSyncCapacityActiveDangerThreshold),
		"threshold": thresholdDetail(
			repoSyncCapacityActiveWarningThreshold,
			repoSyncCapacityActiveDangerThreshold,
			"active runs",
		),
		"detail": fmt.Sprintf("%d queued or running sync runs", activeRuns),
	})
	failedRuns := intFromAny(raw["failed_runs_7d"], 0)
	signals = append(signals, map[string]any{
		"name":     "7d sync failures",
		"status":   failedRuns,
		"severity": severityForCount(failedRuns, repoSyncCapacityFailure7dWarningThreshold, repoSyncCapacityFailure7dDangerThreshold),
		"threshold": thresholdDetail(
			repoSyncCapacityFailure7dWarningThreshold,
			repoSyncCapacityFailure7dDangerThreshold,
			"failures",
		),
		"detail": fmt.Sprintf("%d failed sync runs in the last 7 days", failedRuns),
	})
	webhookFailures := intFromAny(raw["webhook_failures_7d"], 0)
	lastWebhookError := strings.TrimSpace(fmt.Sprint(raw["last_webhook_error"]))
	if lastWebhookError == "<nil>" {
		lastWebhookError = ""
	}
	detail := fmt.Sprintf("%d failed or rejected webhook events in the last 7 days", webhookFailures)
	if lastWebhookError != "" {
		detail = detail + ": " + truncateText(lastWebhookError, 160)
	}
	signals = append(signals, map[string]any{
		"name":     "webhook delivery",
		"status":   webhookFailures,
		"severity": severityForCount(webhookFailures, repoSyncCapacityWebhookWarningThreshold, repoSyncCapacityWebhookDangerThreshold),
		"threshold": thresholdDetail(
			repoSyncCapacityWebhookWarningThreshold,
			repoSyncCapacityWebhookDangerThreshold,
			"failed events",
		),
		"detail": detail,
	})
	githubRuns := intFromAny(raw["github_runs_24h"], 0)
	signals = append(signals, map[string]any{
		"name":     "GitHub Actions volume",
		"status":   githubRuns,
		"severity": severityForCount(githubRuns, repoSyncCapacityGitHubVolumeWarningThreshold, repoSyncCapacityGitHubVolumeDangerThreshold),
		"threshold": thresholdDetail(
			repoSyncCapacityGitHubVolumeWarningThreshold,
			repoSyncCapacityGitHubVolumeDangerThreshold,
			"runs",
		),
		"detail": fmt.Sprintf("%d action runs observed on source/target remotes in the last 24 hours", githubRuns),
	})
	pairActive := intFromAny(raw["provider_pair_active_runs"], 0)
	pairRuns24h := intFromAny(raw["provider_pair_runs_24h"], 0)
	pairFailures24h := intFromAny(raw["provider_pair_failed_runs_24h"], 0)
	pairSeverity := severityForCount(pairActive, repoSyncCapacityPairActiveWarningThreshold, repoSyncCapacityPairActiveDangerThreshold)
	if failureSeverity := severityForCount(pairFailures24h, repoSyncCapacityPairFailureWarningThreshold, repoSyncCapacityPairFailureDangerThreshold); failureSeverity == "danger" || (failureSeverity == "warning" && pairSeverity == "ok") {
		pairSeverity = failureSeverity
	}
	signals = append(signals, map[string]any{
		"name":     "provider pair pressure",
		"status":   pairActive,
		"severity": pairSeverity,
		"threshold": fmt.Sprintf(
			"active warning >= %d / danger >= %d; failures warning >= %d / danger >= %d",
			repoSyncCapacityPairActiveWarningThreshold,
			repoSyncCapacityPairActiveDangerThreshold,
			repoSyncCapacityPairFailureWarningThreshold,
			repoSyncCapacityPairFailureDangerThreshold,
		),
		"detail": fmt.Sprintf(
			"%d active and %d total sync runs in 24h for %s -> %s providers (%d failed)",
			pairActive,
			pairRuns24h,
			firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["source_provider"])), "unknown"),
			firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["target_provider"])), "unknown"),
			pairFailures24h,
		),
	})
	if enabled, ok := asset["enabled"].(bool); ok && !enabled {
		signals = append(signals, map[string]any{"name": "asset state", "status": "disabled", "severity": "warning", "detail": "disabled sync assets do not enqueue manual or webhook runs"})
	}
	if repoSyncAssetArchived(asset) {
		signals = append(signals, map[string]any{"name": "asset state", "status": "archived", "severity": "warning", "detail": "archived sync assets are hidden and cannot run until restored"})
	}
	return signals
}

func thresholdDetail(warningAt, dangerAt int, unit string) string {
	return fmt.Sprintf("warning >= %d %s / danger >= %d %s", warningAt, unit, dangerAt, unit)
}

func signalStatusFromSync(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return "unknown"
	}
	return text
}

func signalSeverityFromSync(value any) string {
	switch signalStatusFromSync(value) {
	case "failed", "error", "rejected":
		return "danger"
	case "running", "queued", "provisioning":
		return "warning"
	default:
		return "ok"
	}
}

func severityForCount(count, warningAt, dangerAt int) string {
	switch {
	case dangerAt > 0 && count >= dangerAt:
		return "danger"
	case warningAt > 0 && count >= warningAt:
		return "warning"
	default:
		return "ok"
	}
}

func floatFromAny(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func (s *Server) updateRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	asset, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM repo_sync_assets WHERE id=$1", assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(asset["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "update") {
		return
	}
	if repoSyncAssetArchived(asset) {
		writeError(w, http.StatusConflict, "repo sync asset is archived")
		return
	}
	var req struct {
		Name        *string         `json:"name"`
		TriggerMode *string         `json:"trigger_mode"`
		SyncMode    *string         `json:"sync_mode"`
		Transport   *string         `json:"transport"`
		Driver      *string         `json:"driver"`
		Refs        *map[string]any `json:"refs"`
		Enabled     *bool           `json:"enabled"`
		Metadata    *map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	refs, err := jsonPatchParam(req.Refs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid refs")
		return
	}
	metadata, err := jsonPatchParam(req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start repo sync asset transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE repo_sync_assets
		SET name=COALESCE(NULLIF($2,''), name),
			trigger_mode=COALESCE(NULLIF($3,''), trigger_mode),
			sync_mode=COALESCE(NULLIF($4,''), sync_mode),
			transport=COALESCE(NULLIF($5,''), transport),
			driver=COALESCE(NULLIF($6,''), driver),
			refs=CASE WHEN $7='null' THEN refs ELSE $7::jsonb END,
			enabled=COALESCE($8, enabled),
			metadata=CASE WHEN $9='null' THEN metadata ELSE $9::jsonb END,
			updated_at=now()
		WHERE id=$1 AND archived_at IS NULL
		RETURNING *`,
		assetID,
		nullableString(req.Name),
		nullableString(req.TriggerMode),
		nullableString(req.SyncMode),
		nullableString(req.Transport),
		nullableString(req.Driver),
		refs,
		nullableBool(req.Enabled),
		metadata,
	)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.update") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit repo sync asset")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) archiveRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	asset, err := queryOne(r.Context(), s.store.DB, "SELECT project_id FROM repo_sync_assets WHERE id=$1", assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(asset["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "update") {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start repo sync asset transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE repo_sync_assets
		SET enabled=false,
			archived_at=COALESCE(archived_at, now()),
			updated_at=now()
		WHERE id=$1
		RETURNING *`, assetID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.archive") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit repo sync asset")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) restoreRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	asset, err := queryOne(r.Context(), s.store.DB, "SELECT project_id FROM repo_sync_assets WHERE id=$1", assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(asset["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "update") {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start repo sync asset transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE repo_sync_assets
		SET archived_at=NULL,
			enabled=true,
			updated_at=now()
		WHERE id=$1
		RETURNING *`, assetID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.restore") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit repo sync asset")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) runRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	asset, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM repo_sync_assets WHERE id=$1", assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(asset["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "repo.sync") {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start sync transaction")
		return
	}
	defer tx.Rollback()
	lockedAsset, err := queryOne(r.Context(), tx, "SELECT * FROM repo_sync_assets WHERE id=$1 FOR UPDATE", assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if enabled, ok := lockedAsset["enabled"].(bool); ok && !enabled {
		writeError(w, http.StatusConflict, "repo sync asset is disabled")
		return
	}
	if repoSyncAssetArchived(lockedAsset) {
		writeError(w, http.StatusConflict, "repo sync asset is archived")
		return
	}
	repoID := strings.TrimSpace(fmt.Sprint(lockedAsset["project_git_repository_id"]))
	repo, err := queryOne(r.Context(), tx, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	sourceID := strings.TrimSpace(fmt.Sprint(lockedAsset["source_remote_id"]))
	targetID := strings.TrimSpace(fmt.Sprint(lockedAsset["target_remote_id"]))
	source, err := remoteForRepository(r.Context(), tx, repoID, sourceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "source remote not found in repository")
		return
	}
	target, err := remoteForRepository(r.Context(), tx, repoID, targetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "target remote not found in repository")
		return
	}
	refs := mapFromAny(lockedAsset["refs"])
	run, err := s.enqueueRepoSyncRun(r.Context(), tx, repo, source, target, refs, false, currentUser(r).ID, assetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not enqueue repo sync asset")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE repo_sync_assets SET last_sync_status='queued', last_sync_run_id=$2, updated_at=now() WHERE id=$1", assetID, run["id"]); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update repo sync asset")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.run") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit repo sync asset run")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run": run})
}

func (s *Server) rerunRepoSyncRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	run, err := queryOne(r.Context(), s.store.DB, `
		SELECT rsr.id,
			rsr.project_id,
			rsr.project_git_repository_id,
			rsr.repo_sync_asset_id,
			rsr.source_remote_id,
			rsr.target_remote_id,
			rsr.ref,
			rsa.project_id AS asset_project_id
		FROM repo_sync_runs rsr
		JOIN repo_sync_assets rsa ON rsa.id=rsr.repo_sync_asset_id
		WHERE rsr.id=$1`, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	assetID := strings.TrimSpace(fmt.Sprint(run["repo_sync_asset_id"]))
	projectID := strings.TrimSpace(fmt.Sprint(run["asset_project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "repo.sync") {
		return
	}
	repoID := strings.TrimSpace(fmt.Sprint(run["project_git_repository_id"]))
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start rerun transaction")
		return
	}
	defer tx.Rollback()
	lockedAsset, err := queryOne(r.Context(), tx, "SELECT * FROM repo_sync_assets WHERE id=$1 FOR UPDATE", assetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if enabled, ok := lockedAsset["enabled"].(bool); ok && !enabled {
		writeError(w, http.StatusConflict, "repo sync asset is disabled")
		return
	}
	if repoSyncAssetArchived(lockedAsset) {
		writeError(w, http.StatusConflict, "repo sync asset is archived")
		return
	}
	repo, err := queryOne(r.Context(), tx, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	source, err := remoteForRepository(r.Context(), tx, repoID, strings.TrimSpace(fmt.Sprint(run["source_remote_id"])))
	if err != nil {
		writeError(w, http.StatusBadRequest, "source remote not found in repository")
		return
	}
	target, err := remoteForRepository(r.Context(), tx, repoID, strings.TrimSpace(fmt.Sprint(run["target_remote_id"])))
	if err != nil {
		writeError(w, http.StatusBadRequest, "target remote not found in repository")
		return
	}
	refs := refsFromRunRef(strings.TrimSpace(fmt.Sprint(run["ref"])), mapFromAny(lockedAsset["refs"]))
	newRun, err := s.enqueueRepoSyncRun(r.Context(), tx, repo, source, target, refs, false, currentUser(r).ID, assetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not enqueue repo sync rerun")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE repo_sync_assets SET last_sync_status='queued', last_sync_run_id=$2, updated_at=now() WHERE id=$1", assetID, newRun["id"]); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update repo sync asset")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.rerun") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit repo sync rerun")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run": newRun})
}

func (s *Server) enqueueRepoSyncRun(ctx context.Context, tx *sqlx.Tx, repo, source, target map[string]any, refs map[string]any, allowForce bool, actorID, repoSyncAssetID string) (map[string]any, error) {
	repoID := strings.TrimSpace(fmt.Sprint(repo["id"]))
	sourceID := strings.TrimSpace(fmt.Sprint(source["id"]))
	targetID := strings.TrimSpace(fmt.Sprint(target["id"]))
	if repoID == "" || sourceID == "" || targetID == "" {
		return nil, fmt.Errorf("repository, source, and target are required")
	}
	if sourceID == targetID {
		return nil, fmt.Errorf("source and target remotes must differ")
	}
	if strings.TrimSpace(fmt.Sprint(source["project_git_repository_id"])) != repoID || strings.TrimSpace(fmt.Sprint(target["project_git_repository_id"])) != repoID {
		return nil, fmt.Errorf("source and target remotes must belong to the repository")
	}
	input := map[string]any{
		"project_git_repository_id": repoID,
		"source_remote_id":          sourceID,
		"target_remote_id":          targetID,
		"refs":                      refs,
		"allow_force":               allowForce,
	}
	if repoSyncAssetID != "" {
		input["repo_sync_asset_id"] = repoSyncAssetID
	}
	op, err := enqueueOperationTx(
		ctx,
		tx,
		fmt.Sprint(repo["project_id"]),
		targetID,
		"repo.sync_remote",
		"sync "+fmt.Sprint(source["name"])+" -> "+fmt.Sprint(target["name"]),
		input,
		[]string{"git"},
		"",
	)
	if err != nil {
		return nil, err
	}
	var assetID any
	if repoSyncAssetID != "" {
		assetID = repoSyncAssetID
	}
	var actor any
	if strings.TrimSpace(actorID) != "" {
		actor = actorID
	}
	return queryOne(ctx, tx, `
		INSERT INTO repo_sync_runs(
			operation_run_id, git_remote_id, project_id, project_git_repository_id,
			repo_sync_asset_id, source_remote_id, target_remote_id, ref, actor_user_id, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued')
		RETURNING *`,
		op["id"],
		targetID,
		repo["project_id"],
		repoID,
		assetID,
		sourceID,
		targetID,
		refsSummary(refs),
		actor,
	)
}

func (s *Server) listWebhookConnections(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT wc.id,
			wc.project_id,
			wc.provider,
			wc.name,
			wc.source_remote_id,
			wc.enabled,
			wc.event_types,
			wc.last_delivery_status,
			wc.last_delivery_error,
			wc.metadata,
			wc.created_at,
			wc.updated_at,
			gr.name AS source_remote_name,
			COALESCE(stats.deliveries_7d, 0)::int AS deliveries_7d,
			COALESCE(stats.failures_7d, 0)::int AS failures_7d,
			stats.last_event_at,
			stats.last_error_message,
			('/api/webhooks/' || wc.provider || '/' || wc.id::text) AS webhook_path,
			$2 || ('/api/webhooks/' || wc.provider || '/' || wc.id::text) AS webhook_url
		FROM webhook_connections wc
		LEFT JOIN git_remotes gr ON gr.id=wc.source_remote_id
		LEFT JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE we.received_at >= now() - interval '7 days') AS deliveries_7d,
				count(*) FILTER (WHERE we.status IN ('failed', 'rejected') AND we.received_at >= now() - interval '7 days') AS failures_7d,
				max(we.received_at) AS last_event_at,
				(
					SELECT recent.error_message
					FROM webhook_events recent
					WHERE recent.webhook_connection_id=wc.id
						AND recent.error_message <> ''
					ORDER BY recent.received_at DESC
					LIMIT 1
				) AS last_error_message
			FROM webhook_events we
			WHERE we.webhook_connection_id=wc.id
		) stats ON true
		WHERE wc.project_id=$1
		ORDER BY wc.created_at DESC`, projectID, s.publicBaseURL())
	annotateWebhookConnectionHealth(items)
	writeQueryResult(w, items, err)
}

func annotateWebhookConnectionHealth(items []map[string]any) {
	for _, item := range items {
		health, summary := webhookConnectionHealth(item)
		item["webhook_health"] = health
		item["webhook_summary"] = summary
	}
}

func webhookConnectionHealth(row map[string]any) (string, string) {
	if enabled, ok := row["enabled"].(bool); ok && !enabled {
		return "warning", "disabled"
	}
	failures := intFromAny(row["failures_7d"], 0)
	if failures >= 3 {
		return "danger", fmt.Sprintf("%d failed or rejected deliveries in 7d", failures)
	}
	lastStatus := strings.TrimSpace(fmt.Sprint(row["last_delivery_status"]))
	switch lastStatus {
	case "failed", "rejected":
		lastError := strings.TrimSpace(fmt.Sprint(row["last_error_message"]))
		if lastError == "" || lastError == "<nil>" {
			lastError = strings.TrimSpace(fmt.Sprint(row["last_delivery_error"]))
		}
		if lastError != "" && lastError != "<nil>" {
			return "danger", truncateText(lastError, 80)
		}
		return "danger", "last delivery failed"
	}
	if failures > 0 {
		return "warning", fmt.Sprintf("%d failed or rejected deliveries in 7d", failures)
	}
	deliveries := intFromAny(row["deliveries_7d"], 0)
	if deliveries == 0 {
		return "unknown", "no deliveries in 7d"
	}
	return "ok", fmt.Sprintf("%d deliveries in 7d", deliveries)
}

func (s *Server) createWebhookConnection(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name           string         `json:"name"`
		Provider       string         `json:"provider"`
		SourceRemoteID string         `json:"source_remote_id"`
		SecretToken    string         `json:"secret_token"`
		Enabled        *bool          `json:"enabled"`
		EventTypes     []string       `json:"event_types"`
		Metadata       map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Provider == "" {
		req.Provider = "gitea"
	}
	if req.Provider != "gitea" && req.Provider != "github" {
		writeError(w, http.StatusBadRequest, "provider must be gitea or github")
		return
	}
	if req.SourceRemoteID == "" {
		writeError(w, http.StatusBadRequest, "source_remote_id is required")
		return
	}
	remote, err := queryOne(r.Context(), s.store.DB, `
		SELECT gr.*, pgr.project_id
		FROM git_remotes gr
		JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id
		WHERE gr.id=$1`, req.SourceRemoteID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if strings.TrimSpace(fmt.Sprint(remote["project_id"])) != projectID {
		writeError(w, http.StatusBadRequest, "source remote must belong to the project")
		return
	}
	if req.Name == "" {
		req.Name = req.Provider + " webhook for " + fmt.Sprint(remote["name"])
	}
	secret := strings.TrimSpace(req.SecretToken)
	generated := false
	if secret == "" {
		var err error
		secret, err = randomWebhookSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate webhook secret")
			return
		}
		generated = true
	} else if len(secret) < 16 {
		writeError(w, http.StatusBadRequest, "secret_token must be at least 16 characters")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if len(req.EventTypes) == 0 {
		req.EventTypes = []string{"push"}
		if req.Provider == "github" {
			req.EventTypes = []string{"workflow_run"}
		}
	}
	secretCiphertext, err := s.encryptWebhookSecret(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encrypt webhook secret")
		return
	}
	eventTypes, err := jsonParam(req.EventTypes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid event_types")
		return
	}
	metadata, err := jsonParam(req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start webhook connection transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO webhook_connections(project_id, provider, name, source_remote_id, secret_token, secret_ciphertext, enabled, event_types, metadata)
		VALUES ($1, $2, $3, $4, '', $5, $6, $7::jsonb, $8::jsonb)
		RETURNING id, project_id, provider, name, source_remote_id, enabled, event_types,
			last_delivery_status, last_delivery_error, metadata, created_at, updated_at,
			('/api/webhooks/' || provider || '/' || id::text) AS webhook_path,
			$9 || ('/api/webhooks/' || provider || '/' || id::text) AS webhook_url`,
		projectID,
		req.Provider,
		req.Name,
		req.SourceRemoteID,
		secretCiphertext,
		enabled,
		eventTypes,
		metadata,
		s.publicBaseURL(),
	)
	if err != nil {
		writeQueryOne(w, item, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "webhook_connection.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit webhook connection")
		return
	}
	if generated {
		item["secret_token_once"] = secret
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) rotateWebhookConnectionSecret(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	connection, err := queryOne(r.Context(), s.store.DB, `
		SELECT id, project_id, provider
		FROM webhook_connections
		WHERE id=$1`, connectionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(connection["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ID: connectionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		SecretToken string `json:"secret_token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	secret := strings.TrimSpace(req.SecretToken)
	generated := false
	if secret == "" {
		secret, err = randomWebhookSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate webhook secret")
			return
		}
		generated = true
	} else if len(secret) < 16 {
		writeError(w, http.StatusBadRequest, "secret_token must be at least 16 characters")
		return
	}
	secretCiphertext, err := s.encryptWebhookSecret(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encrypt webhook secret")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start webhook secret rotation transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE webhook_connections
		SET secret_token='',
			secret_ciphertext=$2,
			updated_at=now()
		WHERE id=$1 AND project_id=$4
		RETURNING id, project_id, provider, name, source_remote_id, enabled, event_types,
			last_delivery_status, last_delivery_error, metadata, created_at, updated_at,
			('/api/webhooks/' || provider || '/' || id::text) AS webhook_path,
			$3 || ('/api/webhooks/' || provider || '/' || id::text) AS webhook_url`,
		connectionID,
		secretCiphertext,
		s.publicBaseURL(),
		projectID,
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusConflict, "webhook connection changed during secret rotation; retry")
			return
		}
		writeQueryOne(w, item, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "webhook_connection.rotate_secret") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit webhook secret rotation")
		return
	}
	if generated {
		item["secret_token_once"] = secret
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listWebhookEvents(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_event", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT we.id,
			we.webhook_connection_id,
			we.project_id,
			we.provider,
			we.event_type,
			we.delivery_id,
			we.signature_valid,
			we.matched_repo_sync_asset_id,
			we.operation_run_id,
			we.status,
			we.error_message,
			we.received_at,
			we.processed_at,
			wc.name AS webhook_connection_name
		FROM webhook_events we
		LEFT JOIN webhook_connections wc ON wc.id=we.webhook_connection_id
		WHERE we.project_id=$1
		ORDER BY we.received_at DESC
		LIMIT 100`, projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) replayWebhookEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	event, err := queryOne(r.Context(), s.store.DB, `
		SELECT *
		FROM webhook_events
		WHERE id=$1`, eventID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(event["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_event", ID: eventID, ProjectID: projectID}, "repo.sync") {
		return
	}
	if signatureValid, ok := event["signature_valid"].(bool); !ok || !signatureValid {
		writeError(w, http.StatusConflict, "only verified webhook events can be replayed")
		return
	}
	connectionID := strings.TrimSpace(fmt.Sprint(event["webhook_connection_id"]))
	if connectionID == "" || connectionID == "<nil>" {
		writeError(w, http.StatusBadRequest, "webhook event has no connection")
		return
	}
	provider := fmt.Sprint(event["provider"])
	eventType := fmt.Sprint(event["event_type"])
	if (provider != "gitea" || eventType != "push") && (provider != "github" || eventType != "workflow_run") {
		writeError(w, http.StatusBadRequest, "only gitea push or github workflow_run webhook events can be replayed")
		return
	}
	connection, err := webhookConnectionForDelivery(r.Context(), s.store.DB, connectionID, provider)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if enabled, ok := connection["enabled"].(bool); ok && !enabled {
		writeError(w, http.StatusConflict, "webhook connection is disabled")
		return
	}
	payload := mapFromAny(event["payload"])
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start webhook replay transaction")
		return
	}
	defer tx.Rollback()
	var result map[string]any
	if provider == "github" {
		result, err = s.upsertGitHubWorkflowRunFromWebhook(r.Context(), tx, connection, payload)
	} else {
		push := parseGiteaPushPayload(payload)
		if push.Ref == "" {
			writeError(w, http.StatusBadRequest, "webhook event payload has no push ref")
			return
		}
		result, err = s.enqueueWebhookRepoSyncRuns(r.Context(), tx, connection, push)
	}
	status := stringFromMap(result, "status")
	if status == "" {
		status = "processed"
	}
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	if result == nil {
		result = map[string]any{}
	}
	result["replayed_from_event_id"] = eventID
	replayDeliveryID := replayWebhookDeliveryID(strings.TrimSpace(fmt.Sprint(event["delivery_id"])), eventID)
	if err != nil {
		_ = tx.Rollback()
		replayEvent, eventErr := s.recordWebhookEvent(r.Context(), connection, eventType, replayDeliveryID, false, status, errorMessage, payload, result)
		if eventErr != nil {
			writeError(w, http.StatusInternalServerError, "could not record webhook replay")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"event": replayEvent, "result": result, "error": errorMessage})
		return
	}
	replayEvent, eventErr := s.recordWebhookEventTx(r.Context(), tx, connection, eventType, replayDeliveryID, false, status, errorMessage, payload, result)
	if eventErr != nil {
		writeError(w, http.StatusInternalServerError, "could not record webhook replay")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "webhook_event.replay") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit webhook replay")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"event": replayEvent, "result": result, "error": errorMessage})
}

func (s *Server) receiveGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	if !s.webhookLimiter.allow(webhookRateLimitKey(r, connectionID), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "webhook rate limit exceeded")
		return
	}
	connection, err := webhookConnectionForDelivery(r.Context(), s.store.DB, connectionID, "github")
	if err != nil {
		writeError(w, http.StatusNotFound, "webhook connection not found")
		return
	}
	eventType := webhookEventType(r)
	deliveryID := webhookDeliveryID(r)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "webhook payload is too large")
		return
	}
	if enabled, ok := connection["enabled"].(bool); ok && !enabled {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, false, "disabled", "webhook connection is disabled", nil, nil)
		writeError(w, http.StatusGone, "webhook connection is disabled")
		return
	}
	secret, err := s.webhookSecretFromConnection(connection)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read webhook secret")
		return
	}
	if !verifyWebhookSignature(r.Header, secret, body) {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, false, "rejected", "invalid signature", nil, nil)
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "failed", "invalid JSON payload", nil, nil)
		writeError(w, http.StatusBadRequest, "invalid webhook JSON")
		return
	}
	if eventType != "workflow_run" {
		result := map[string]any{"event_type": eventType, "message": "ignored unsupported event"}
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "ignored", "unsupported event type", payload, result)
		writeJSON(w, http.StatusAccepted, result)
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start GitHub webhook transaction")
		return
	}
	defer tx.Rollback()
	if deliveryID != "" {
		if _, err := tx.ExecContext(r.Context(), "SELECT pg_advisory_xact_lock(hashtext($1))", "webhook:"+connectionID+":"+deliveryID); err != nil {
			writeError(w, http.StatusInternalServerError, "could not lock webhook delivery")
			return
		}
		existing, exists, err := findProcessedWebhookDelivery(r.Context(), tx, connectionID, deliveryID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not check webhook delivery")
			return
		}
		if exists {
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, "could not commit webhook delivery check")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"duplicate": true, "event": existing})
			return
		}
	}
	result, err := s.upsertGitHubWorkflowRunFromWebhook(r.Context(), tx, connection, payload)
	status := "processed"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	if result == nil {
		result = map[string]any{}
	}
	if err != nil {
		_ = tx.Rollback()
		event, eventErr := s.recordWebhookEvent(r.Context(), connection, "workflow_run", deliveryID, true, status, errorMessage, payload, result)
		if eventErr != nil {
			writeError(w, http.StatusInternalServerError, "could not record GitHub webhook event")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result, "error": errorMessage})
		return
	}
	event, eventErr := s.recordWebhookEventTx(r.Context(), tx, connection, "workflow_run", deliveryID, true, status, errorMessage, payload, result)
	if eventErr != nil {
		writeError(w, http.StatusInternalServerError, "could not record GitHub webhook event")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "webhook_event.github_workflow_run") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit GitHub webhook event")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result})
}

func (s *Server) receiveGiteaWebhook(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	if !s.webhookLimiter.allow(webhookRateLimitKey(r, connectionID), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "webhook rate limit exceeded")
		return
	}
	connection, err := webhookConnectionForDelivery(r.Context(), s.store.DB, connectionID, "gitea")
	if err != nil {
		writeError(w, http.StatusNotFound, "webhook connection not found")
		return
	}
	eventType := webhookEventType(r)
	deliveryID := webhookDeliveryID(r)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "webhook payload is too large")
		return
	}
	if enabled, ok := connection["enabled"].(bool); ok && !enabled {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, false, "disabled", "webhook connection is disabled", nil, nil)
		writeError(w, http.StatusGone, "webhook connection is disabled")
		return
	}
	secret, err := s.webhookSecretFromConnection(connection)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read webhook secret")
		return
	}
	if !verifyWebhookSignature(r.Header, secret, body) {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, false, "rejected", "invalid signature", nil, nil)
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	if eventType != "" && eventType != "push" {
		result := map[string]any{"event_type": eventType, "message": "ignored unsupported event"}
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "ignored", "unsupported event type", nil, result)
		writeJSON(w, http.StatusAccepted, result)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "failed", "invalid JSON payload", nil, nil)
		writeError(w, http.StatusBadRequest, "invalid webhook JSON")
		return
	}
	push := parseGiteaPushPayload(payload)
	if push.Ref == "" {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, "push", deliveryID, true, "failed", "push ref is required", payload, nil)
		writeError(w, http.StatusBadRequest, "push ref is required")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start webhook transaction")
		return
	}
	defer tx.Rollback()
	if deliveryID != "" {
		if _, err := tx.ExecContext(r.Context(), "SELECT pg_advisory_xact_lock(hashtext($1))", "webhook:"+connectionID+":"+deliveryID); err != nil {
			writeError(w, http.StatusInternalServerError, "could not lock webhook delivery")
			return
		}
		existing, exists, err := findProcessedWebhookDelivery(r.Context(), tx, connectionID, deliveryID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not check webhook delivery")
			return
		}
		if exists {
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, "could not commit webhook delivery check")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"duplicate": true, "event": existing})
			return
		}
	}
	result, err := s.enqueueWebhookRepoSyncRuns(r.Context(), tx, connection, push)
	status := stringFromMap(result, "status")
	if status == "" {
		status = "processed"
	}
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	if err != nil {
		_ = tx.Rollback()
		event, eventErr := s.recordWebhookEvent(r.Context(), connection, "push", deliveryID, true, status, errorMessage, payload, result)
		if eventErr != nil {
			writeError(w, http.StatusInternalServerError, "could not record webhook event")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result, "error": errorMessage})
		return
	}
	event, eventErr := s.recordWebhookEventTx(r.Context(), tx, connection, "push", deliveryID, true, status, errorMessage, payload, result)
	if eventErr != nil {
		writeError(w, http.StatusInternalServerError, "could not record webhook event")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "webhook_event.gitea_push") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit webhook event")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result})
}

func webhookConnectionForDelivery(ctx context.Context, db sqlx.ExtContext, connectionID, provider string) (map[string]any, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT id, project_id, provider, source_remote_id, secret_token, secret_ciphertext, enabled
		FROM webhook_connections
		WHERE id=$1 AND provider=$2`, connectionID, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	row := map[string]any{}
	if err := rows.MapScan(row); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for key, value := range row {
		if bytes, ok := value.([]byte); ok {
			row[key] = string(bytes)
		}
	}
	return row, nil
}

type giteaPushPayload struct {
	Ref       string
	BeforeSHA string
	AfterSHA  string
}

func parseGiteaPushPayload(payload map[string]any) giteaPushPayload {
	return giteaPushPayload{
		Ref:       strings.TrimSpace(fmt.Sprint(payload["ref"])),
		BeforeSHA: strings.TrimSpace(fmt.Sprint(payload["before"])),
		AfterSHA:  strings.TrimSpace(fmt.Sprint(payload["after"])),
	}
}

func (s *Server) enqueueWebhookRepoSyncRuns(ctx context.Context, tx *sqlx.Tx, connection map[string]any, push giteaPushPayload) (map[string]any, error) {
	sourceRemoteID := strings.TrimSpace(fmt.Sprint(connection["source_remote_id"]))
	if sourceRemoteID == "" || sourceRemoteID == "<nil>" {
		return nil, fmt.Errorf("webhook connection has no source remote")
	}
	assets, err := queryMaps(ctx, tx, `
		SELECT *
		FROM repo_sync_assets
		WHERE source_remote_id=$1
			AND project_id=$2
			AND enabled
			AND archived_at IS NULL
			AND trigger_mode IN ('webhook', 'push', 'manual_or_webhook')
		ORDER BY created_at`, sourceRemoteID, connection["project_id"])
	if err != nil {
		return nil, err
	}
	var runs []map[string]any
	var matchedAssetID string
	eventRefs := refsForWebhookRef(push.Ref)
	for _, asset := range assets {
		if !repoSyncAssetMatchesWebhookRef(mapFromAny(asset["refs"]), push.Ref) {
			continue
		}
		repoID := strings.TrimSpace(fmt.Sprint(asset["project_git_repository_id"]))
		repo, err := queryOne(ctx, tx, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
		if err != nil {
			return nil, err
		}
		source, err := remoteForRepository(ctx, tx, repoID, strings.TrimSpace(fmt.Sprint(asset["source_remote_id"])))
		if err != nil {
			return nil, err
		}
		target, err := remoteForRepository(ctx, tx, repoID, strings.TrimSpace(fmt.Sprint(asset["target_remote_id"])))
		if err != nil {
			return nil, err
		}
		run, err := s.enqueueRepoSyncRun(ctx, tx, repo, source, target, eventRefs, false, "", strings.TrimSpace(fmt.Sprint(asset["id"])))
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE repo_sync_assets SET last_sync_status='queued', last_sync_run_id=$2, updated_at=now() WHERE id=$1", asset["id"], run["id"]); err != nil {
			return nil, err
		}
		if matchedAssetID == "" {
			matchedAssetID = strings.TrimSpace(fmt.Sprint(asset["id"]))
		}
		runs = append(runs, run)
	}
	status := "queued"
	if len(runs) == 0 {
		status = "ignored"
	}
	result := map[string]any{
		"status":                  status,
		"ref":                     push.Ref,
		"before":                  push.BeforeSHA,
		"after":                   push.AfterSHA,
		"matched_repo_sync_asset": matchedAssetID,
		"queued_runs":             runs,
		"queued_count":            len(runs),
	}
	return result, nil
}

type githubWorkflowRunPayload struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	WorkflowRun struct {
		ID           int64      `json:"id"`
		Name         string     `json:"name"`
		DisplayTitle string     `json:"display_title"`
		RunNumber    int64      `json:"run_number"`
		HeadBranch   string     `json:"head_branch"`
		HeadSHA      string     `json:"head_sha"`
		Status       string     `json:"status"`
		Conclusion   string     `json:"conclusion"`
		HTMLURL      string     `json:"html_url"`
		RunStartedAt *time.Time `json:"run_started_at"`
		UpdatedAt    *time.Time `json:"updated_at"`
		Event        string     `json:"event"`
	} `json:"workflow_run"`
}

func (s *Server) upsertGitHubWorkflowRunFromWebhook(ctx context.Context, tx *sqlx.Tx, connection map[string]any, payload map[string]any) (map[string]any, error) {
	remoteID := strings.TrimSpace(fmt.Sprint(connection["source_remote_id"]))
	if remoteID == "" || remoteID == "<nil>" {
		return nil, fmt.Errorf("webhook connection has no GitHub remote")
	}
	remote, err := queryOne(ctx, tx, `
		SELECT gr.*, pgr.project_id
		FROM git_remotes gr
		JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id
		WHERE gr.id=$1`, remoteID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(fmt.Sprint(remote["project_id"])) != strings.TrimSpace(fmt.Sprint(connection["project_id"])) {
		return nil, fmt.Errorf("GitHub remote does not belong to webhook project")
	}
	var event githubWorkflowRunPayload
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("decoding GitHub workflow_run payload: %w", err)
	}
	if event.WorkflowRun.ID == 0 {
		return nil, fmt.Errorf("GitHub workflow_run payload is missing workflow_run.id")
	}
	owner, repo, err := gitHubRepositoryFromRemote(remote)
	if err != nil {
		return nil, err
	}
	if event.Repository.FullName != "" && !strings.EqualFold(event.Repository.FullName, owner+"/"+repo) {
		return nil, fmt.Errorf("GitHub workflow_run repository does not match remote")
	}
	name := event.WorkflowRun.Name
	if name == "" {
		name = event.WorkflowRun.DisplayTitle
	}
	runID := fmt.Sprint(event.WorkflowRun.ID)
	metadata, err := jsonParam(map[string]any{
		"action":     event.Action,
		"event":      event.WorkflowRun.Event,
		"repository": event.Repository.FullName,
		"run_number": event.WorkflowRun.RunNumber,
	})
	if err != nil {
		return nil, err
	}
	run, err := queryOne(ctx, tx, `
		INSERT INTO github_action_runs(
			git_remote_id, external_run_id, workflow_name, run_id,
			branch, commit_sha, status, conclusion, html_url, metadata, started_at, updated_at, synced_at
		)
		VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, $10::jsonb, $11, $12, now()
		)
		ON CONFLICT (git_remote_id, external_run_id) WHERE external_run_id <> ''
		DO UPDATE SET
			workflow_name=EXCLUDED.workflow_name,
			run_id=EXCLUDED.run_id,
			branch=EXCLUDED.branch,
			commit_sha=EXCLUDED.commit_sha,
			status=EXCLUDED.status,
			conclusion=EXCLUDED.conclusion,
			html_url=EXCLUDED.html_url,
			metadata=EXCLUDED.metadata,
			started_at=EXCLUDED.started_at,
			updated_at=EXCLUDED.updated_at,
			synced_at=now()
		RETURNING *`,
		remoteID,
		runID,
		name,
		runID,
		event.WorkflowRun.HeadBranch,
		event.WorkflowRun.HeadSHA,
		event.WorkflowRun.Status,
		event.WorkflowRun.Conclusion,
		event.WorkflowRun.HTMLURL,
		metadata,
		event.WorkflowRun.RunStartedAt,
		event.WorkflowRun.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":               "processed",
		"git_remote_id":        remoteID,
		"github_action_run_id": run["id"],
		"external_run_id":      runID,
		"workflow_name":        name,
		"branch":               event.WorkflowRun.HeadBranch,
		"conclusion":           event.WorkflowRun.Conclusion,
	}, nil
}

func repoSyncAssetMatchesWebhookRef(refs map[string]any, ref string) bool {
	kind, name := splitGitRef(ref)
	if kind == "" || name == "" {
		return false
	}
	switch kind {
	case "branch":
		return refListMatches(stringSliceFromAny(refs["branches"]), name)
	case "tag":
		return refListMatches(stringSliceFromAny(refs["tags"]), name)
	default:
		return false
	}
}

func refsForWebhookRef(ref string) map[string]any {
	kind, name := splitGitRef(ref)
	switch kind {
	case "branch":
		return map[string]any{"branches": []string{name}, "tags": []string{}}
	case "tag":
		return map[string]any{"branches": []string{}, "tags": []string{name}}
	default:
		return map[string]any{}
	}
}

func splitGitRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return "branch", strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return "tag", strings.TrimPrefix(ref, "refs/tags/")
	default:
		return "", ""
	}
}

func refListMatches(values []string, name string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "*" || value == name {
			return true
		}
	}
	return false
}

func (s *Server) recordWebhookEvent(ctx context.Context, connection map[string]any, eventType, deliveryID string, signatureValid bool, status, errorMessage string, payload, result map[string]any) (map[string]any, error) {
	tx, err := s.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	event, err := s.recordWebhookEventTx(ctx, tx, connection, eventType, deliveryID, signatureValid, status, errorMessage, payload, result)
	if err != nil {
		return nil, err
	}
	if _, err := SyncCanonicalAssetsWith(ctx, tx); err != nil {
		return nil, fmt.Errorf("syncing canonical assets for webhook event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *Server) recordWebhookDiagnosticEvent(ctx context.Context, connection map[string]any, eventType, deliveryID string, signatureValid bool, status, errorMessage string, payload, result map[string]any) {
	if _, err := s.recordWebhookEvent(ctx, connection, eventType, deliveryID, signatureValid, status, errorMessage, payload, result); err != nil && s.log != nil {
		s.log.Warn(
			"failed to record webhook diagnostic event",
			"webhook_connection_id", connection["id"],
			"provider", connection["provider"],
			"event_type", eventType,
			"delivery_id", deliveryID,
			"status", status,
			"error", err,
		)
	}
}

func (s *Server) recordWebhookEventTx(ctx context.Context, db sqlx.ExtContext, connection map[string]any, eventType, deliveryID string, signatureValid bool, status, errorMessage string, payload, result map[string]any) (map[string]any, error) {
	payloadJSON, err := jsonParam(payload)
	if err != nil {
		return nil, err
	}
	resultJSON, err := jsonParam(result)
	if err != nil {
		return nil, err
	}
	var matchedAssetID any
	var operationRunID any
	if result != nil {
		if value := strings.TrimSpace(fmt.Sprint(result["matched_repo_sync_asset"])); value != "" && value != "<nil>" {
			matchedAssetID = value
		}
		if runs, ok := result["queued_runs"].([]map[string]any); ok && len(runs) > 0 {
			operationRunID = runs[0]["operation_run_id"]
		}
	}
	event, err := queryOne(ctx, db, `
		INSERT INTO webhook_events(
			webhook_connection_id, project_id, provider, event_type, delivery_id, signature_valid,
			matched_repo_sync_asset_id, operation_run_id, status, error_message, payload, result, processed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::jsonb, now())
		RETURNING *`,
		connection["id"],
		connection["project_id"],
		connection["provider"],
		eventType,
		deliveryID,
		signatureValid,
		matchedAssetID,
		operationRunID,
		status,
		errorMessage,
		payloadJSON,
		resultJSON,
	)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE webhook_connections
		SET last_delivery_status=$2,
			last_delivery_error=$3,
			updated_at=now()
		WHERE id=$1`, connection["id"], status, errorMessage); err != nil {
		return nil, err
	}
	return event, nil
}

func findProcessedWebhookDelivery(ctx context.Context, db sqlx.ExtContext, connectionID, deliveryID string) (map[string]any, bool, error) {
	event, err := queryOne(ctx, db, `
		SELECT *
		FROM webhook_events
		WHERE webhook_connection_id=$1
			AND delivery_id=$2
			AND signature_valid
		ORDER BY received_at DESC
		LIMIT 1`, connectionID, deliveryID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return event, true, nil
}

func webhookEventType(r *http.Request) string {
	for _, header := range []string{"X-Gitea-Event", "X-GitHub-Event"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return "push"
}

func webhookDeliveryID(r *http.Request) string {
	for _, header := range []string{"X-Gitea-Delivery", "X-GitHub-Delivery"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
}

func verifyWebhookSignature(header http.Header, secret string, body []byte) bool {
	if secret == "" {
		return false
	}
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMAC.Write(body)
	expected := expectedMAC.Sum(nil)
	for _, candidate := range []string{
		stripSignaturePrefix(header.Get("X-Gitea-Signature")),
		stripSignaturePrefix(header.Get("X-Hub-Signature-256")),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		got, err := hex.DecodeString(candidate)
		if err == nil && hmac.Equal(got, expected) {
			return true
		}
	}
	return false
}

func (s *Server) encryptWebhookSecret(secret string) (string, error) {
	block, err := aes.NewCipher(s.webhookSecretKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(secret), nil)
	return "v1:" + hex.EncodeToString(nonce) + ":" + hex.EncodeToString(sealed), nil
}

func (s *Server) decryptWebhookSecret(ciphertext string) (string, error) {
	parts := strings.Split(strings.TrimSpace(ciphertext), ":")
	if len(parts) == 3 && parts[0] == "v1" {
		parts = parts[1:]
	} else if len(parts) != 2 {
		return "", fmt.Errorf("invalid webhook secret ciphertext")
	}
	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decoding webhook secret nonce: %w", err)
	}
	sealed, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding webhook secret ciphertext: %w", err)
	}
	block, err := aes.NewCipher(s.webhookSecretKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting webhook secret: %w", err)
	}
	return string(plain), nil
}

func (s *Server) webhookSecretFromConnection(connection map[string]any) (string, error) {
	ciphertext := strings.TrimSpace(fmt.Sprint(connection["secret_ciphertext"]))
	if ciphertext != "" && ciphertext != "<nil>" {
		return s.decryptWebhookSecret(ciphertext)
	}
	secret := strings.TrimSpace(fmt.Sprint(connection["secret_token"]))
	if secret == "<nil>" {
		return "", fmt.Errorf("webhook connection has no secret configured")
	}
	if secret == "" {
		return "", fmt.Errorf("webhook connection has no secret configured")
	}
	return secret, nil
}

func (s *Server) webhookSecretKey() []byte {
	material := strings.TrimSpace(s.cfg.WebhookSecretKey)
	if material == "" {
		material = s.cfg.JWTSecret
	}
	sum := sha256.Sum256([]byte("assops:webhook-secret-encryption:" + material))
	return sum[:]
}

func (s *Server) publicBaseURL() string {
	base := strings.TrimSpace(s.cfg.GatewayURL)
	if base == "" {
		base = "http://localhost:8080"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "http://localhost:8080"
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func replayWebhookDeliveryID(deliveryID, eventID string) string {
	if deliveryID == "" || deliveryID == "<nil>" {
		deliveryID = eventID
	}
	return deliveryID + ":replay:" + uuid.NewString()
}

func stripSignaturePrefix(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimPrefix(value, "sha256=")
}

func randomWebhookSecret() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func (s *Server) createGitRemote(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name           string         `json:"name"`
		Kind           string         `json:"kind"`
		RemoteKey      string         `json:"remote_key"`
		ProviderType   string         `json:"provider_type"`
		RemoteURL      string         `json:"remote_url"`
		WebURL         string         `json:"web_url"`
		RemoteRole     string         `json:"remote_role"`
		IsPrimary      bool           `json:"is_primary"`
		SyncEnabled    *bool          `json:"sync_enabled"`
		Protected      bool           `json:"protected"`
		LatestSHA      string         `json:"latest_sha"`
		LastSyncStatus string         `json:"last_sync_status"`
		URLs           []string       `json:"urls"`
		DefaultBranch  string         `json:"default_branch"`
		Metadata       map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = "github"
	}
	if req.ProviderType == "" {
		req.ProviderType = req.Kind
	}
	if req.RemoteKey == "" {
		req.RemoteKey = req.Name
	}
	if req.RemoteRole == "" {
		req.RemoteRole = "mirror"
	}
	syncEnabled := true
	if req.SyncEnabled != nil {
		syncEnabled = *req.SyncEnabled
	}
	if req.LastSyncStatus == "" {
		req.LastSyncStatus = "never"
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.RemoteURL == "" && len(req.URLs) > 0 {
		req.RemoteURL = req.URLs[0]
	}
	urls, err := jsonParam(req.URLs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid urls")
		return
	}
	metadata, err := jsonParam(req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start git remote transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO git_remotes(
			project_git_repository_id, name, kind, remote_key, provider_type, remote_url, web_url,
			remote_role, is_primary, sync_enabled, protected, latest_sha, last_sync_status,
			urls, default_branch, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15, $16::jsonb)
		RETURNING *`,
		chi.URLParam(r, "id"),
		req.Name,
		req.Kind,
		req.RemoteKey,
		req.ProviderType,
		req.RemoteURL,
		req.WebURL,
		req.RemoteRole,
		req.IsPrimary,
		syncEnabled,
		req.Protected,
		req.LatestSHA,
		req.LastSyncStatus,
		urls,
		req.DefaultBranch,
		metadata,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "git_remote.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit git remote")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listGitRemotes(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT * FROM git_remotes WHERE project_git_repository_id=$1 ORDER BY created_at DESC`, chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) getGitRemote(w http.ResponseWriter, r *http.Request) {
	item, err := queryOne(r.Context(), s.store.DB, `
		SELECT gr.*, pgr.project_id
		FROM git_remotes gr
		JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id
		WHERE gr.id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ID: chi.URLParam(r, "id"), ProjectID: fmt.Sprint(item["project_id"])}, "read") {
		return
	}
	writeQueryOne(w, item, err)
}

func (s *Server) updateGitRemote(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDForGitRemote(r.Context(), s.store.DB, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ID: chi.URLParam(r, "id"), ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		Name           *string         `json:"name"`
		Kind           *string         `json:"kind"`
		RemoteKey      *string         `json:"remote_key"`
		ProviderType   *string         `json:"provider_type"`
		RemoteURL      *string         `json:"remote_url"`
		WebURL         *string         `json:"web_url"`
		RemoteRole     *string         `json:"remote_role"`
		IsPrimary      *bool           `json:"is_primary"`
		SyncEnabled    *bool           `json:"sync_enabled"`
		Protected      *bool           `json:"protected"`
		LatestSHA      *string         `json:"latest_sha"`
		LastSyncStatus *string         `json:"last_sync_status"`
		URLs           *[]string       `json:"urls"`
		DefaultBranch  *string         `json:"default_branch"`
		Metadata       *map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	urls, err := jsonPatchParam(req.URLs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid urls")
		return
	}
	metadata, err := jsonPatchParam(req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start git remote transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE git_remotes
		SET name=COALESCE(NULLIF($2,''), name),
			kind=COALESCE(NULLIF($3,''), kind),
			remote_key=COALESCE(NULLIF($4,''), remote_key),
			provider_type=COALESCE(NULLIF($5,''), provider_type),
			remote_url=COALESCE($6, remote_url),
			web_url=COALESCE($7, web_url),
			remote_role=COALESCE(NULLIF($8,''), remote_role),
			is_primary=COALESCE($9, is_primary),
			sync_enabled=COALESCE($10, sync_enabled),
			protected=COALESCE($11, protected),
			latest_sha=COALESCE($12, latest_sha),
			last_sync_status=COALESCE(NULLIF($13,''), last_sync_status),
			urls=CASE WHEN $14='null' THEN urls ELSE $14::jsonb END,
			default_branch=COALESCE(NULLIF($15,''), default_branch),
			metadata=CASE WHEN $16='null' THEN metadata ELSE $16::jsonb END,
			updated_at=now()
		WHERE id=$1
		RETURNING *`,
		chi.URLParam(r, "id"),
		nullableString(req.Name),
		nullableString(req.Kind),
		nullableString(req.RemoteKey),
		nullableString(req.ProviderType),
		nullableString(req.RemoteURL),
		nullableString(req.WebURL),
		nullableString(req.RemoteRole),
		nullableBool(req.IsPrimary),
		nullableBool(req.SyncEnabled),
		nullableBool(req.Protected),
		nullableString(req.LatestSHA),
		nullableString(req.LastSyncStatus),
		urls,
		nullableString(req.DefaultBranch),
		metadata,
	)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "git_remote.update") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit git remote")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listGitHubActions(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDForGitRemote(r.Context(), s.store.DB, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ID: chi.URLParam(r, "id"), ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT * FROM github_action_runs
		WHERE git_remote_id=$1
		ORDER BY created_at DESC
		LIMIT 50`, chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) listRepoSyncRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "repo_sync_run"}, "read") {
		return
	}
	repoID := r.URL.Query().Get("repo_id")
	remoteID := r.URL.Query().Get("remote_id")
	filters, err := repoSyncRunFiltersFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := currentUser(r)
	switch {
	case repoID != "":
		projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT * FROM repo_sync_runs
			WHERE project_git_repository_id=$1
				AND ($2='' OR repo_sync_asset_id::text=$2)
				AND ($3='' OR status=$3)
				AND ($4='' OR ref=$4)
				AND (NULLIF($5, '') IS NULL OR created_at >= NULLIF($5, '')::timestamptz)
				AND (NULLIF($6, '') IS NULL OR created_at <= NULLIF($6, '')::timestamptz)
			ORDER BY created_at DESC
			LIMIT 100`, repoID, filters.AssetID, filters.Status, filters.Ref, filters.Since, filters.Until)
		writeQueryResult(w, items, err)
	case remoteID != "":
		projectID, err := projectIDForGitRemote(r.Context(), s.store.DB, remoteID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT * FROM repo_sync_runs
			WHERE (source_remote_id=$1 OR target_remote_id=$1 OR git_remote_id=$1)
				AND ($2='' OR repo_sync_asset_id::text=$2)
				AND ($3='' OR status=$3)
				AND ($4='' OR ref=$4)
				AND (NULLIF($5, '') IS NULL OR created_at >= NULLIF($5, '')::timestamptz)
				AND (NULLIF($6, '') IS NULL OR created_at <= NULLIF($6, '')::timestamptz)
			ORDER BY created_at DESC
			LIMIT 100`, remoteID, filters.AssetID, filters.Status, filters.Ref, filters.Since, filters.Until)
		writeQueryResult(w, items, err)
	default:
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT rsr.*
			FROM repo_sync_runs rsr
			LEFT JOIN project_git_repositories pgr ON pgr.id=rsr.project_git_repository_id
			WHERE $1 OR COALESCE(rsr.project_id::text, pgr.project_id::text, '')='' OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id::text=COALESCE(rsr.project_id::text, pgr.project_id::text, '') AND pm.user_id=$2
			)
			AND ($3='' OR rsr.repo_sync_asset_id::text=$3)
			AND ($4='' OR rsr.status=$4)
			AND ($5='' OR rsr.ref=$5)
			AND (NULLIF($6, '') IS NULL OR rsr.created_at >= NULLIF($6, '')::timestamptz)
			AND (NULLIF($7, '') IS NULL OR rsr.created_at <= NULLIF($7, '')::timestamptz)
			ORDER BY rsr.created_at DESC
			LIMIT 100`, userCanReadAllProjects(user), userIDOrNil(user), filters.AssetID, filters.Status, filters.Ref, filters.Since, filters.Until)
		writeQueryResult(w, items, err)
	}
}

type repoSyncRunFilters struct {
	AssetID string
	Status  string
	Ref     string
	Since   string
	Until   string
}

func repoSyncRunFiltersFromRequest(r *http.Request) (repoSyncRunFilters, error) {
	q := r.URL.Query()
	filters := repoSyncRunFilters{
		AssetID: strings.TrimSpace(q.Get("asset_id")),
		Status:  strings.TrimSpace(q.Get("status")),
		Ref:     strings.TrimSpace(q.Get("ref")),
		Since:   strings.TrimSpace(q.Get("since")),
		Until:   strings.TrimSpace(q.Get("until")),
	}
	if err := validateOptionalRFC3339("since", filters.Since); err != nil {
		return repoSyncRunFilters{}, err
	}
	if err := validateOptionalRFC3339("until", filters.Until); err != nil {
		return repoSyncRunFilters{}, err
	}
	return filters, nil
}

func validateOptionalRFC3339(name, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("%s must be RFC3339", name)
	}
	return nil
}

func boolQuery(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) listRepoTagRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "repo_tag_run"}, "read") {
		return
	}
	repoID := r.URL.Query().Get("repo_id")
	remoteID := r.URL.Query().Get("remote_id")
	user := currentUser(r)
	switch {
	case repoID != "":
		projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT * FROM repo_tag_runs
			WHERE project_git_repository_id=$1
			ORDER BY created_at DESC
			LIMIT 100`, repoID)
		writeQueryResult(w, items, err)
	case remoteID != "":
		projectID, err := projectIDForGitRemote(r.Context(), s.store.DB, remoteID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT * FROM repo_tag_runs
			WHERE target_remote_id=$1 OR git_remote_id=$1
			ORDER BY created_at DESC
			LIMIT 100`, remoteID)
		writeQueryResult(w, items, err)
	default:
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT rtr.*
			FROM repo_tag_runs rtr
			LEFT JOIN project_git_repositories pgr ON pgr.id=rtr.project_git_repository_id
			WHERE $1 OR COALESCE(rtr.project_id::text, pgr.project_id::text, '')='' OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id::text=COALESCE(rtr.project_id::text, pgr.project_id::text, '') AND pm.user_id=$2
			)
			ORDER BY rtr.created_at DESC
			LIMIT 100`, userCanReadAllProjects(user), userIDOrNil(user))
		writeQueryResult(w, items, err)
	}
}

func remoteForRepository(ctx context.Context, db sqlx.ExtContext, repoID, remoteID string) (map[string]any, error) {
	return queryOne(ctx, db, `
		SELECT * FROM git_remotes
		WHERE id=$1 AND project_git_repository_id=$2`, remoteID, repoID)
}

func projectIDForRepository(ctx context.Context, db sqlx.ExtContext, repoID string) (string, error) {
	repo, err := queryOne(ctx, db, "SELECT project_id FROM project_git_repositories WHERE id=$1", repoID)
	if err != nil {
		return "", err
	}
	projectID := strings.TrimSpace(fmt.Sprint(repo["project_id"]))
	if projectID == "" || projectID == "<nil>" {
		return "", ErrNotFound
	}
	return projectID, nil
}

func projectIDForGitRemote(ctx context.Context, db sqlx.ExtContext, remoteID string) (string, error) {
	remote, err := queryOne(ctx, db, `
		SELECT pgr.project_id
		FROM git_remotes gr
		JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id
		WHERE gr.id=$1`, remoteID)
	if err != nil {
		return "", err
	}
	projectID := strings.TrimSpace(fmt.Sprint(remote["project_id"]))
	if projectID == "" || projectID == "<nil>" {
		return "", ErrNotFound
	}
	return projectID, nil
}

func defaultTargetRemoteIDs(ctx context.Context, db sqlx.ExtContext, repoID, sourceRemoteID string) ([]string, error) {
	items, err := queryMaps(ctx, db, `
		SELECT id FROM git_remotes
		WHERE project_git_repository_id=$1 AND id<>$2 AND sync_enabled=true
		ORDER BY is_primary DESC, name`, repoID, sourceRemoteID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, fmt.Sprint(item["id"]))
	}
	return ids, nil
}

func defaultGitHubRemoteIDs(ctx context.Context, db sqlx.ExtContext, repoID string) ([]string, error) {
	items, err := queryMaps(ctx, db, `
		SELECT id FROM git_remotes
		WHERE project_git_repository_id=$1
		  AND (provider_type='github' OR kind='github')
		ORDER BY is_primary DESC, name`, repoID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, fmt.Sprint(item["id"]))
	}
	return ids, nil
}

func refsSummary(refs map[string]any) string {
	if len(refs) == 0 {
		return "default"
	}
	data, err := json.Marshal(refs)
	if err != nil {
		return "custom"
	}
	return string(data)
}

func refsFromRunRef(ref string, fallback map[string]any) map[string]any {
	if strings.TrimSpace(ref) == "" || ref == "default" || ref == "custom" {
		return fallback
	}
	var refs map[string]any
	if err := json.Unmarshal([]byte(ref), &refs); err != nil || len(refs) == 0 {
		return fallback
	}
	return refs
}

func repoSyncAssetArchived(asset map[string]any) bool {
	value := strings.TrimSpace(fmt.Sprint(asset["archived_at"]))
	return value != "" && value != "<nil>"
}

func refsSummaryFromInput(input map[string]any) string {
	if input == nil {
		return "default"
	}
	refs, ok := input["refs"].(map[string]any)
	if !ok {
		return stringFromMap(input, "ref", "branch", "tag")
	}
	return refsSummary(refs)
}

func stringFromMap(input map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := input[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			return text
		}
		return fmt.Sprint(value)
	}
	return ""
}

func userCanReadAllProjects(user *User) bool {
	return user != nil && (user.Role == "admin" || user.Role == "owner")
}

func userIDOrNil(user *User) any {
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return nil
	}
	return user.ID
}

func assetInventorySQL() string {
	return `
	WITH asset_inventory AS (
		SELECT
			'project:' || p.id::text AS id,
			p.id::text AS project_id,
			'project' AS asset_type,
			p.name AS name,
			p.name AS display_name,
			p.description AS description,
			'local' AS source,
			p.slug AS external_id,
			'active' AS status,
			'normal' AS risk_level,
			'projects' AS source_table,
			p.id::text AS source_id,
			jsonb_build_object('slug', p.slug) AS metadata,
			p.created_at AS created_at,
			p.updated_at AS updated_at
		FROM projects p
		UNION ALL
		SELECT
			'project_template:' || pt.id::text,
			'',
			'project_template',
			pt.name,
			pt.name,
			pt.description,
			'assops_builtin',
			pt.slug,
			pt.status,
			'normal',
			'project_templates',
			pt.id::text,
			jsonb_build_object('slug', pt.slug, 'version', pt.version, 'defaults', pt.defaults, 'steps', pt.steps),
			pt.created_at,
			pt.updated_at
		FROM project_templates pt
		UNION ALL
		SELECT
			'project_template_run:' || ptr.id::text,
			COALESCE(ptr.project_id::text, ''),
			'project_template_run',
			ptr.project_name,
			ptr.project_name,
			ptr.project_slug,
			'project_template',
			ptr.id::text,
			ptr.status,
			CASE
				WHEN ptr.status='failed' THEN 'high'
				WHEN ptr.status IN ('queued', 'running') THEN 'warning'
				ELSE 'normal'
			END,
			'project_template_runs',
			ptr.id::text,
			jsonb_build_object(
				'project_template_id', ptr.project_template_id,
				'operation_run_id', ptr.operation_run_id,
				'project_id', ptr.project_id,
				'started_at', ptr.started_at,
				'finished_at', ptr.finished_at,
				'step_count', jsonb_array_length(ptr.steps),
				'has_error', ptr.error_message <> ''
			),
			ptr.created_at,
			ptr.updated_at
		FROM project_template_runs ptr
		UNION ALL
		SELECT
			'provider_account:' || pa.id::text,
			'',
			'provider_account',
			pa.name,
			pa.name,
			pa.api_base_url,
			pa.provider_type,
			pa.default_owner,
			CASE WHEN pa.enabled THEN 'active' ELSE 'disabled' END,
			'normal',
			'provider_accounts',
			pa.id::text,
			jsonb_build_object(
				'provider_type', pa.provider_type,
				'api_base_url', pa.api_base_url,
				'web_base_url', pa.web_base_url,
				'default_owner', pa.default_owner,
				'visibility', pa.visibility,
				'enabled', pa.enabled,
				'token_configured', pa.token_env <> ''
			),
			pa.created_at,
			pa.updated_at
		FROM provider_accounts pa
		UNION ALL
		SELECT
			'repository:' || r.id::text,
			r.project_id::text,
			'repository',
			r.name,
			COALESCE(NULLIF(r.display_name, ''), r.name),
			r.description,
			'local',
			r.repo_key,
			r.status,
			'normal',
			'project_git_repositories',
			r.id::text,
			jsonb_build_object('repo_key', r.repo_key, 'repo_role', r.repo_role, 'default_branch', r.default_branch),
			r.created_at,
			r.updated_at
		FROM project_git_repositories r
		UNION ALL
		SELECT
			'template_file:' || ptf.id::text,
			ptf.project_id::text,
			'template_file',
			ptf.path,
			ptf.path,
			'Planned starter file from project template',
			'project_template',
			ptf.path,
			ptf.status,
			'normal',
			'project_template_files',
			ptf.id::text,
			jsonb_build_object('kind', ptf.kind, 'project_template_run_id', ptf.project_template_run_id, 'project_git_repository_id', ptf.project_git_repository_id),
			ptf.created_at,
			ptf.updated_at
		FROM project_template_files ptf
		UNION ALL
		SELECT
			'git_remote:' || gr.id::text,
			r.project_id::text,
			'git_remote',
			gr.name,
			COALESCE(NULLIF(gr.remote_key, ''), gr.name),
			gr.web_url,
			COALESCE(NULLIF(gr.provider_type, ''), gr.kind, 'git'),
			COALESCE(NULLIF(gr.remote_url, ''), gr.urls->>0, ''),
			gr.last_sync_status,
			CASE WHEN gr.protected THEN 'high' ELSE 'normal' END,
			'git_remotes',
			gr.id::text,
			jsonb_build_object('remote_role', gr.remote_role, 'default_branch', gr.default_branch, 'latest_sha', gr.latest_sha, 'sync_enabled', gr.sync_enabled),
			gr.created_at,
			gr.updated_at
		FROM git_remotes gr
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text,
			COALESCE(op.project_id::text, ''),
			'operation_run',
			op.title,
			op.title,
			op.operation_type,
			'assops_operation',
			op.id::text,
			op.status,
			CASE WHEN op.status='failed' THEN 'high' ELSE 'normal' END,
			'operation_runs',
			op.id::text,
			jsonb_build_object(
				'operation_type', op.operation_type,
				'git_remote_id', op.git_remote_id,
				'started_at', op.started_at,
				'finished_at', op.finished_at,
				'has_error', op.error <> ''
			),
			op.created_at,
			op.updated_at
		FROM operation_runs op
		UNION ALL
		SELECT
			'operation_approval:' || oa.id::text,
			COALESCE(oa.project_id::text, ''),
			'operation_approval',
			oa.title,
			oa.title,
			oa.action,
			'assops_approval',
			oa.id::text,
			oa.status,
			CASE
				WHEN oa.status IN ('rejected', 'expired') THEN 'high'
				WHEN oa.status='pending' THEN 'warning'
				ELSE 'normal'
			END,
			'operation_approvals',
			oa.id::text,
			jsonb_build_object(
				'action', oa.action,
				'resource_type', oa.resource_type,
				'resource_id', oa.resource_id,
				'operation_run_id', oa.operation_run_id,
				'required_approval_count', oa.required_approval_count,
				'approved_count', COALESCE(decision_counts.approved_count, 0),
				'rejected_count', COALESCE(decision_counts.rejected_count, 0),
				'notification_status', oa.notification_status,
				'escalation_count', oa.escalation_count
			),
			oa.created_at,
			oa.updated_at
		FROM operation_approvals oa
		LEFT JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE decision='approved')::int AS approved_count,
				count(*) FILTER (WHERE decision='rejected')::int AS rejected_count
			FROM operation_approval_decisions oad
			WHERE oad.operation_approval_id=oa.id
		) decision_counts ON true
		UNION ALL
		SELECT
			'operation_approval_rule:' || oar.id::text,
			'',
			'operation_approval_rule',
			COALESCE(NULLIF(oar.resource_type, ''), '*') || ':' || oar.action,
			COALESCE(NULLIF(oar.resource_type, ''), '*') || ':' || oar.action,
			'Approval policy rule',
			'assops_policy',
			oar.id::text,
			CASE WHEN oar.enabled THEN 'active' ELSE 'disabled' END,
			CASE WHEN oar.enabled THEN 'normal' ELSE 'warning' END,
			'operation_approval_rules',
			oar.id::text,
			jsonb_build_object(
				'resource_type', oar.resource_type,
				'action', oar.action,
				'required_approver_roles', oar.required_approver_roles,
				'required_approval_count', oar.required_approval_count,
				'expires_after_minutes', oar.expires_after_minutes,
				'notification_channels', oar.notification_channels,
				'escalation_after_minutes', oar.escalation_after_minutes,
				'escalation_channels', oar.escalation_channels,
				'priority', oar.priority,
				'enabled', oar.enabled
			),
			oar.created_at,
			oar.updated_at
		FROM operation_approval_rules oar
		UNION ALL
		SELECT
			'repo_sync:' || rsa.id::text,
			rsa.project_id::text,
			'repo_sync',
			rsa.name,
			rsa.name,
			rsa.driver,
			'repo_sync',
			rsa.id::text,
			rsa.last_sync_status,
			'normal',
			'repo_sync_assets',
			rsa.id::text,
			jsonb_build_object(
				'trigger_mode', rsa.trigger_mode,
				'sync_mode', rsa.sync_mode,
				'transport', rsa.transport,
				'driver', rsa.driver,
				'source_remote_id', rsa.source_remote_id,
				'target_remote_id', rsa.target_remote_id,
				'enabled', rsa.enabled
			),
			rsa.created_at,
			rsa.updated_at
		FROM repo_sync_assets rsa
		UNION ALL
		SELECT
			'webhook_connection:' || wc.id::text,
			wc.project_id::text,
			'webhook_connection',
			wc.name,
			wc.name,
			wc.provider,
			'webhook',
			wc.id::text,
			wc.last_delivery_status,
			CASE
				WHEN wc.last_delivery_status IN ('failed', 'rejected') THEN 'high'
				WHEN NOT wc.enabled THEN 'warning'
				ELSE 'normal'
			END,
			'webhook_connections',
			wc.id::text,
			jsonb_build_object(
				'provider', wc.provider,
				'source_remote_id', wc.source_remote_id,
				'enabled', wc.enabled,
				'has_last_delivery_error', wc.last_delivery_error <> ''
			),
			wc.created_at,
			wc.updated_at
		FROM webhook_connections wc
		UNION ALL
		SELECT
			'webhook_event:' || we.id::text,
			COALESCE(we.project_id::text, wc.project_id::text, ''),
			'webhook_event',
			COALESCE(NULLIF(we.event_type, ''), we.provider || ' webhook'),
			we.id::text,
			we.provider,
			'webhook',
			we.id::text,
			we.status,
			CASE
				WHEN NOT we.signature_valid THEN 'high'
				WHEN we.status IN ('failed', 'rejected') THEN 'high'
				WHEN we.status IN ('received', 'processing') THEN 'warning'
				ELSE 'normal'
			END,
			'webhook_events',
			we.id::text,
			jsonb_build_object(
				'webhook_connection_id', we.webhook_connection_id,
				'provider', we.provider,
				'event_type', we.event_type,
				'delivery_id', we.delivery_id,
				'signature_valid', we.signature_valid,
				'matched_repo_sync_asset_id', we.matched_repo_sync_asset_id,
				'operation_run_id', we.operation_run_id,
				'processed_at', we.processed_at,
				'has_payload', we.payload <> '{}'::jsonb,
				'has_result', we.result <> '{}'::jsonb,
				'has_error', we.error_message <> ''
			),
			we.received_at,
			COALESCE(we.processed_at, we.received_at)
		FROM webhook_events we
		LEFT JOIN webhook_connections wc ON wc.id=we.webhook_connection_id
		UNION ALL
		SELECT
			'github_action_run:' || gh.id::text,
			r.project_id::text,
			'pipeline_run',
			COALESCE(NULLIF(gh.workflow_name, ''), 'GitHub Actions'),
			COALESCE(NULLIF(gh.workflow_name, ''), 'GitHub Actions'),
			gh.html_url,
			'github_actions',
			gh.run_id,
			COALESCE(NULLIF(gh.conclusion, ''), gh.status),
			'normal',
			'github_action_runs',
			gh.id::text,
			jsonb_build_object('branch', gh.branch, 'commit_sha', gh.commit_sha, 'html_url', gh.html_url),
			gh.created_at,
			COALESCE(gh.updated_at, gh.created_at)
		FROM github_action_runs gh
		JOIN git_remotes gr ON gr.id=gh.git_remote_id
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		UNION ALL
		SELECT
			'ssh_machine:' || sm.id::text,
			sm.project_id::text,
			'host',
			sm.name,
			sm.name,
			sm.host,
			'ssh',
			sm.host,
			'active',
			'normal',
			'ssh_machines',
			sm.id::text,
			jsonb_build_object('host', sm.host, 'port', sm.port, 'username', sm.username, 'auth_type', sm.auth_type),
			sm.created_at,
			sm.updated_at
		FROM ssh_machines sm
		UNION ALL
		SELECT
			'ssh_command_run:' || scr.id::text,
			COALESCE(scr.project_id::text, sm.project_id::text, op.project_id::text, ''),
			'ssh_command_run',
			'SSH command run',
			'SSH command run',
			COALESCE(sm.name, 'SSH command run'),
			'ssh',
			scr.id::text,
			scr.status,
			CASE
				WHEN scr.status='failed' THEN 'high'
				WHEN scr.status IN ('queued', 'running') THEN 'warning'
				ELSE 'normal'
			END,
			'ssh_command_runs',
			scr.id::text,
			jsonb_build_object(
				'operation_run_id', scr.operation_run_id,
				'ssh_machine_id', scr.ssh_machine_id,
				'actor_user_id', scr.actor_user_id,
				'exit_code', scr.exit_code,
				'started_at', scr.started_at,
				'finished_at', scr.finished_at,
				'has_command', scr.command <> '',
				'has_stdout', scr.stdout <> '',
				'has_stderr', scr.stderr <> '',
				'has_error', scr.error_message <> ''
			),
			scr.created_at,
			COALESCE(scr.finished_at, scr.started_at, scr.created_at)
		FROM ssh_command_runs scr
		LEFT JOIN ssh_machines sm ON sm.id=scr.ssh_machine_id
		LEFT JOIN operation_runs op ON op.id=scr.operation_run_id
		UNION ALL
		SELECT
			'argo_connection:' || ac.id::text,
			ac.project_id::text,
			'argo_connection',
			ac.name,
			ac.name,
			ac.server_url,
			'argocd',
			ac.server_url,
			ac.last_sync_status,
			'normal',
			'argo_connections',
			ac.id::text,
			jsonb_build_object('auth_type', ac.auth_type, 'last_sync_error', ac.last_sync_error),
			ac.created_at,
			ac.updated_at
		FROM argo_connections ac
		UNION ALL
		SELECT
			'deployment_target:' || dt.id::text,
			dt.project_id::text,
			'deployment_target',
			dt.name,
			dt.name,
			dt.namespace,
			dt.source,
			dt.environment,
			dt.status,
			'normal',
			'deployment_targets',
			dt.id::text,
			jsonb_build_object(
				'environment', dt.environment,
				'cluster_name', dt.cluster_name,
				'namespace', dt.namespace,
				'argo_connection_id', dt.argo_connection_id
			),
			dt.created_at,
			dt.updated_at
		FROM deployment_targets dt
		UNION ALL
		SELECT
			'deployment_record:' || dr.id::text,
			dr.project_id::text,
			'deployment_record',
			dr.name,
			dr.name,
			dr.environment || '/' || dr.namespace,
			dr.source,
			dr.revision,
			dr.status,
			'normal',
			'deployment_records',
			dr.id::text,
			jsonb_build_object('environment', dr.environment, 'namespace', dr.namespace, 'cluster_name', dr.cluster_name, 'deployment_target_id', dr.deployment_target_id, 'image_refs', dr.image_refs),
			dr.created_at,
			dr.updated_at
		FROM deployment_records dr
		UNION ALL
		SELECT
			'rollback_point:' || rp.id::text,
			rp.project_id::text,
			'rollback_point',
			rp.name,
			rp.name,
			rp.environment,
			rp.source,
			rp.revision,
			rp.status,
			'normal',
			'rollback_points',
			rp.id::text,
			jsonb_build_object('environment', rp.environment, 'deployment_record_id', rp.deployment_record_id, 'deployment_target_id', rp.deployment_target_id, 'image_refs', rp.image_refs),
			rp.created_at,
			rp.captured_at
		FROM rollback_points rp
		UNION ALL
		SELECT
			'argo_app:' || aa.id::text,
			aa.project_id::text,
			'argo_app',
			aa.name,
			aa.name,
			aa.namespace,
			'argocd',
			aa.name,
			aa.status,
			'normal',
			'argo_apps',
			aa.id::text,
			jsonb_build_object('namespace', aa.namespace, 'argo_connection_id', aa.argo_connection_id, 'deployment_target_id', aa.deployment_target_id),
			aa.created_at,
			aa.updated_at
		FROM argo_apps aa
		UNION ALL
		SELECT
			'ai_runtime:' || ar.id::text,
			COALESCE(ar.project_id::text, ''),
			'ai_runtime',
			ar.name,
			ar.name,
			ar.runtime_type,
			'local',
			ar.runtime_type,
			ar.status,
			'normal',
			'ai_runtimes',
			ar.id::text,
			jsonb_build_object('runtime_type', ar.runtime_type, 'codex_binary', ar.codex_binary, 'model', ar.model),
			ar.created_at,
			ar.updated_at
		FROM ai_runtimes ar
		UNION ALL
		SELECT
			'agent_task:' || at.id::text,
			at.project_id::text,
			'agent_task',
			at.title,
			at.title,
			'AI agent task',
			'assops_agent',
			at.id::text,
			at.status,
			'normal',
			'agent_tasks',
			at.id::text,
			jsonb_build_object(
				'created_by', at.created_by,
				'latest_plan_id', latest_plan.id,
				'latest_plan_status', latest_plan.status,
				'latest_plan_approved_at', latest_plan.approved_at
			),
			at.created_at,
			at.updated_at
		FROM agent_tasks at
		LEFT JOIN LATERAL (
			SELECT id, status, approved_at
			FROM agent_plans ap
			WHERE ap.agent_task_id=at.id
			ORDER BY created_at DESC
			LIMIT 1
		) latest_plan ON true
		UNION ALL
		SELECT
			'agent_tool_call:' || atc.id::text,
			COALESCE(atc.project_id::text, at.project_id::text, ''),
			'agent_tool_call',
			atc.tool_name,
			atc.tool_name,
			'AI tool call audit',
			'assops_agent',
			atc.id::text,
			atc.status,
			CASE WHEN atc.status='failed' THEN 'high' ELSE 'normal' END,
			'agent_tool_calls',
			atc.id::text,
			jsonb_build_object(
				'agent_task_id', atc.agent_task_id,
				'operation_run_id', atc.operation_run_id,
				'project_id', COALESCE(atc.project_id, at.project_id),
				'tool_name', atc.tool_name,
				'started_at', atc.started_at,
				'finished_at', atc.finished_at,
				'has_input', atc.input <> '{}'::jsonb,
				'has_output', atc.output <> '{}'::jsonb,
				'has_error', atc.error_message <> ''
			),
			atc.created_at,
			atc.updated_at
		FROM agent_tool_calls atc
		JOIN agent_tasks at ON at.id=atc.agent_task_id
		UNION ALL
		SELECT
			'worker_job:' || wj.id::text,
			COALESCE(op.project_id::text, ''),
			'worker_job',
			wj.tool_name,
			wj.tool_name,
			'Worker job',
			'assops_worker',
			wj.id::text,
			wj.status,
			CASE
				WHEN wj.status='failed' THEN 'high'
				WHEN wj.status IN ('queued', 'running') THEN 'warning'
				ELSE 'normal'
			END,
			'worker_jobs',
			wj.id::text,
			jsonb_build_object(
				'operation_run_id', wj.operation_run_id,
				'tool_name', wj.tool_name,
				'required_capabilities', wj.required_capabilities,
				'preferred_node_kind', wj.preferred_node_kind,
				'assigned_worker_node_id', wj.assigned_worker_node_id,
				'claimed_at', wj.claimed_at,
				'started_at', wj.started_at,
				'finished_at', wj.finished_at,
				'has_payload', wj.payload <> '{}'::jsonb,
				'has_result', wj.result <> '{}'::jsonb,
				'has_error', wj.error <> ''
			),
			wj.created_at,
			wj.updated_at
		FROM worker_jobs wj
		LEFT JOIN operation_runs op ON op.id=wj.operation_run_id
		UNION ALL
		SELECT
			'worker_node:' || wn.id::text,
			'',
			'node_agent',
			wn.name,
			wn.name,
			wn.kind,
			'local',
			wn.name,
			wn.status,
			'normal',
			'worker_nodes',
			wn.id::text,
			jsonb_build_object('kind', wn.kind, 'capabilities', wn.capabilities, 'last_heartbeat_at', wn.last_heartbeat_at),
			wn.created_at,
			wn.updated_at
		FROM worker_nodes wn
	)
`
}

func assetRelationInventorySQL() string {
	return `
	WITH asset_relation_inventory AS (
		SELECT
			'project:' || p.id::text || ':owns:repository:' || r.id::text AS id,
			p.id::text AS project_id,
			'project:' || p.id::text AS from_asset_id,
			'repository:' || r.id::text AS to_asset_id,
			'owns' AS relation_type,
			'{}'::jsonb AS metadata,
			r.created_at AS created_at
		FROM projects p
		JOIN project_git_repositories r ON r.project_id=p.id
		UNION ALL
		SELECT
			'repository:' || r.id::text || ':has_remote:git_remote:' || gr.id::text,
			r.project_id::text,
			'repository:' || r.id::text,
			'git_remote:' || gr.id::text,
			'has_remote',
			jsonb_build_object('remote_role', gr.remote_role),
			gr.created_at
		FROM project_git_repositories r
		JOIN git_remotes gr ON gr.project_git_repository_id=r.id
		UNION ALL
		SELECT
			'provider_account:' || pa.id::text || ':manages:git_remote:' || gr.id::text,
			r.project_id::text,
			'provider_account:' || pa.id::text,
			'git_remote:' || gr.id::text,
			'manages',
			jsonb_build_object('provider_type', pa.provider_type),
			gr.created_at
		FROM provider_accounts pa
		JOIN git_remotes gr ON gr.source_account_id=pa.id
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:operation_run:' || op.id::text,
			p.id::text,
			'project:' || p.id::text,
			'operation_run:' || op.id::text,
			'owns_operation',
			jsonb_build_object('operation_type', op.operation_type, 'status', op.status),
			op.created_at
		FROM projects p
		JOIN operation_runs op ON op.project_id=p.id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':dispatched_worker_job:worker_job:' || wj.id::text,
			COALESCE(op.project_id::text, ''),
			'operation_run:' || op.id::text,
			'worker_job:' || wj.id::text,
			'dispatched_worker_job',
			jsonb_build_object('tool_name', wj.tool_name, 'status', wj.status),
			wj.created_at
		FROM worker_jobs wj
		JOIN operation_runs op ON op.id=wj.operation_run_id
		UNION ALL
		-- Queued jobs have not been claimed yet, so they intentionally do not have an
		-- assigned node edge until assigned_worker_node_id is set by claim.
		SELECT
			'worker_job:' || wj.id::text || ':assigned_to:worker_node:' || wn.id::text,
			COALESCE(op.project_id::text, ''),
			'worker_job:' || wj.id::text,
			'worker_node:' || wn.id::text,
			'assigned_to_worker_node',
			jsonb_build_object('tool_name', wj.tool_name, 'status', wj.status, 'node_kind', wn.kind),
			wj.created_at
		FROM worker_jobs wj
		JOIN worker_nodes wn ON wn.id=wj.assigned_worker_node_id
		LEFT JOIN operation_runs op ON op.id=wj.operation_run_id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:operation_approval:' || oa.id::text,
			p.id::text,
			'project:' || p.id::text,
			'operation_approval:' || oa.id::text,
			'owns_approval',
			jsonb_build_object('action', oa.action, 'status', oa.status),
			oa.created_at
		FROM projects p
		JOIN operation_approvals oa ON oa.project_id=p.id
		UNION ALL
		SELECT
			'operation_approval:' || oa.id::text || ':gates_operation:operation_run:' || op.id::text,
			COALESCE(oa.project_id::text, op.project_id::text, ''),
			'operation_approval:' || oa.id::text,
			'operation_run:' || op.id::text,
			'gates_operation',
			jsonb_build_object('action', oa.action, 'status', oa.status),
			oa.created_at
		FROM operation_approvals oa
		JOIN operation_runs op ON op.id=oa.operation_run_id
		UNION ALL
		SELECT
			'operation_approval_rule:' || oar.id::text || ':governs:operation_approval:' || oa.id::text,
			COALESCE(oa.project_id::text, ''),
			'operation_approval_rule:' || oar.id::text,
			'operation_approval:' || oa.id::text,
			'governs',
			jsonb_build_object('action', oa.action, 'status', oa.status),
			oa.created_at
		FROM operation_approval_rules oar
		JOIN operation_approvals oa ON oa.approval_rule_id=oar.id
		UNION ALL
		SELECT
			'operation_approval:' || oa.id::text || ':targets:' || approval_resource.asset_id,
			COALESCE(oa.project_id::text, ''),
			'operation_approval:' || oa.id::text,
			approval_resource.asset_id,
			'targets',
			jsonb_build_object('action', oa.action, 'status', oa.status),
			oa.created_at
		FROM operation_approvals oa
		JOIN LATERAL (
			-- Current approval policy resources use UUID primary keys. If a future resource
			-- type uses slugs or external IDs, add an explicit mapping here instead of
			-- letting the UUID filter below silently drop the target relation.
			SELECT CASE oa.resource_type
				WHEN 'project' THEN 'project:' || oa.resource_id
				WHEN 'repository' THEN 'repository:' || oa.resource_id
				WHEN 'git_remote' THEN 'git_remote:' || oa.resource_id
				WHEN 'repo_sync' THEN 'repo_sync:' || oa.resource_id
				WHEN 'webhook_connection' THEN 'webhook_connection:' || oa.resource_id
				WHEN 'ssh_machine' THEN 'ssh_machine:' || oa.resource_id
				-- Compatibility alias for older callers that described SSH machines as hosts.
				WHEN 'host' THEN 'ssh_machine:' || oa.resource_id
				WHEN 'agent_task' THEN 'agent_task:' || oa.resource_id
				WHEN 'argo_connection' THEN 'argo_connection:' || oa.resource_id
				ELSE ''
			END AS asset_id
		) approval_resource ON approval_resource.asset_id <> ''
		WHERE oa.resource_type IN ('project', 'repository', 'git_remote', 'repo_sync', 'webhook_connection', 'host', 'ssh_machine', 'agent_task', 'argo_connection')
			AND oa.resource_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
		UNION ALL
		SELECT
			'git_remote:' || gr.id::text || ':triggered:operation_run:' || op.id::text,
			r.project_id::text,
			'git_remote:' || gr.id::text,
			'operation_run:' || op.id::text,
			'triggered',
			jsonb_build_object('operation_type', op.operation_type, 'status', op.status),
			op.created_at
		FROM operation_runs op
		JOIN git_remotes gr ON gr.id=op.git_remote_id
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':ran_repo_sync:repo_sync:' || rsa.id::text,
			rsa.project_id::text,
			'operation_run:' || op.id::text,
			'repo_sync:' || rsa.id::text,
			'ran_repo_sync',
			jsonb_build_object('status', rsr.status),
			rsr.created_at
		FROM repo_sync_runs rsr
		JOIN operation_runs op ON op.id=rsr.operation_run_id
		JOIN repo_sync_assets rsa ON rsa.id=rsr.repo_sync_asset_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':used_source_remote:git_remote:' || source.id::text,
			r.project_id::text,
			'operation_run:' || op.id::text,
			'git_remote:' || source.id::text,
			'used_source_remote',
			jsonb_build_object('status', rsr.status),
			rsr.created_at
		FROM repo_sync_runs rsr
		JOIN operation_runs op ON op.id=rsr.operation_run_id
		JOIN git_remotes source ON source.id=rsr.source_remote_id
		JOIN project_git_repositories r ON r.id=source.project_git_repository_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':used_target_remote:git_remote:' || target.id::text,
			r.project_id::text,
			'operation_run:' || op.id::text,
			'git_remote:' || target.id::text,
			'used_target_remote',
			jsonb_build_object('status', rsr.status),
			rsr.created_at
		FROM repo_sync_runs rsr
		JOIN operation_runs op ON op.id=rsr.operation_run_id
		JOIN git_remotes target ON target.id=rsr.target_remote_id
		JOIN project_git_repositories r ON r.id=target.project_git_repository_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':tagged_remote:git_remote:' || target.id::text,
			r.project_id::text,
			'operation_run:' || op.id::text,
			'git_remote:' || target.id::text,
			'tagged_remote',
			jsonb_build_object('status', rtr.status, 'tag_name', rtr.tag_name),
			rtr.created_at
		FROM repo_tag_runs rtr
		JOIN operation_runs op ON op.id=rtr.operation_run_id
		JOIN git_remotes target ON target.id=rtr.target_remote_id
		JOIN project_git_repositories r ON r.id=target.project_git_repository_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':executed_on:ssh_machine:' || sm.id::text,
			sm.project_id::text,
			'operation_run:' || op.id::text,
			'ssh_machine:' || sm.id::text,
			'executed_on',
			jsonb_build_object('status', scr.status, 'exit_code', scr.exit_code),
			scr.created_at
		FROM ssh_command_runs scr
		JOIN operation_runs op ON op.id=scr.operation_run_id
		JOIN ssh_machines sm ON sm.id=scr.ssh_machine_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':ran_ssh_command:ssh_command_run:' || scr.id::text,
			COALESCE(scr.project_id::text, op.project_id::text, sm.project_id::text, ''),
			'operation_run:' || op.id::text,
			'ssh_command_run:' || scr.id::text,
			'ran_ssh_command',
			jsonb_build_object('status', scr.status, 'exit_code', scr.exit_code),
			scr.created_at
		FROM ssh_command_runs scr
		JOIN operation_runs op ON op.id=scr.operation_run_id
		LEFT JOIN ssh_machines sm ON sm.id=scr.ssh_machine_id
		UNION ALL
		SELECT
			'ssh_command_run:' || scr.id::text || ':executed_on:ssh_machine:' || sm.id::text,
			COALESCE(scr.project_id::text, sm.project_id::text, ''),
			'ssh_command_run:' || scr.id::text,
			'ssh_machine:' || sm.id::text,
			'executed_on',
			jsonb_build_object('status', scr.status, 'exit_code', scr.exit_code),
			scr.created_at
		FROM ssh_command_runs scr
		JOIN ssh_machines sm ON sm.id=scr.ssh_machine_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':executed_agent_task:agent_task:' || at.id::text,
			at.project_id::text,
			'operation_run:' || op.id::text,
			'agent_task:' || at.id::text,
			'executed_agent_task',
			jsonb_build_object('status', at.status),
			op.created_at
		FROM operation_runs op
		JOIN agent_tasks at ON at.id=CASE
			WHEN (op.input->>'agent_task_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (op.input->>'agent_task_id')::uuid
			ELSE NULL
		END
		WHERE op.operation_type='agent.execute'
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':synced_argo_connection:argo_connection:' || ac.id::text,
			ac.project_id::text,
			'operation_run:' || op.id::text,
			'argo_connection:' || ac.id::text,
			'synced_argo_connection',
			jsonb_build_object('status', op.status),
			op.created_at
		FROM operation_runs op
		JOIN argo_connections ac ON ac.id=CASE
			WHEN (op.input->>'argo_connection_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (op.input->>'argo_connection_id')::uuid
			ELSE NULL
		END
		WHERE op.operation_type='argo.apps.sync'
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':created_template_run:project_template_run:' || ptr.id::text,
			COALESCE(ptr.project_id::text, op.project_id::text, ''),
			'operation_run:' || op.id::text,
			'project_template_run:' || ptr.id::text,
			'created_template_run',
			jsonb_build_object('status', ptr.status),
			ptr.created_at
		FROM project_template_runs ptr
		JOIN operation_runs op ON op.id=ptr.operation_run_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':created_from_template:project_template:' || pt.id::text,
			COALESCE(ptr.project_id::text, ''),
			'operation_run:' || op.id::text,
			'project_template:' || pt.id::text,
			'created_from_template',
			jsonb_build_object('status', ptr.status),
			ptr.created_at
		FROM project_template_runs ptr
		JOIN operation_runs op ON op.id=ptr.operation_run_id
		JOIN project_templates pt ON pt.id=ptr.project_template_id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:project_template_run:' || ptr.id::text,
			p.id::text,
			'project:' || p.id::text,
			'project_template_run:' || ptr.id::text,
			'owns_template_run',
			jsonb_build_object('status', ptr.status),
			ptr.created_at
		FROM project_template_runs ptr
		JOIN projects p ON p.id=ptr.project_id
		UNION ALL
		SELECT
			'project_template_run:' || ptr.id::text || ':instantiates:project_template:' || pt.id::text,
			COALESCE(ptr.project_id::text, ''),
			'project_template_run:' || ptr.id::text,
			'project_template:' || pt.id::text,
			'instantiates_template',
			jsonb_build_object('status', ptr.status),
			ptr.created_at
		FROM project_template_runs ptr
		JOIN project_templates pt ON pt.id=ptr.project_template_id
		UNION ALL
		SELECT
			'project_template_run:' || ptr.id::text || ':produced_file:template_file:' || ptf.id::text,
			COALESCE(ptr.project_id::text, ptf.project_id::text, ''),
			'project_template_run:' || ptr.id::text,
			'template_file:' || ptf.id::text,
			'produced_template_file',
			jsonb_build_object('path', ptf.path, 'status', ptf.status),
			ptf.created_at
		FROM project_template_files ptf
		JOIN project_template_runs ptr ON ptr.id=ptf.project_template_run_id
		UNION ALL
		-- Connection deletion nulls webhook_events.webhook_connection_id, so historical
		-- webhook events remain as assets while this connection edge naturally disappears.
		SELECT
			'webhook_connection:' || wc.id::text || ':received:webhook_event:' || we.id::text,
			COALESCE(we.project_id::text, wc.project_id::text, ''),
			'webhook_connection:' || wc.id::text,
			'webhook_event:' || we.id::text,
			'received_webhook_event',
			jsonb_build_object('provider', we.provider, 'event_type', we.event_type, 'status', we.status),
			we.received_at
		FROM webhook_events we
		JOIN webhook_connections wc ON wc.id=we.webhook_connection_id
		UNION ALL
		SELECT
			'webhook_event:' || we.id::text || ':matched_repo_sync:repo_sync:' || rsa.id::text,
			COALESCE(we.project_id::text, rsa.project_id::text, ''),
			'webhook_event:' || we.id::text,
			'repo_sync:' || rsa.id::text,
			'matched_repo_sync',
			jsonb_build_object('provider', we.provider, 'event_type', we.event_type, 'status', we.status),
			we.received_at
		FROM webhook_events we
		JOIN repo_sync_assets rsa ON rsa.id=we.matched_repo_sync_asset_id
		UNION ALL
		SELECT
			'webhook_event:' || we.id::text || ':triggered_operation:operation_run:' || op.id::text,
			COALESCE(we.project_id::text, op.project_id::text, ''),
			'webhook_event:' || we.id::text,
			'operation_run:' || op.id::text,
			'triggered_operation',
			jsonb_build_object('provider', we.provider, 'event_type', we.event_type, 'status', we.status),
			we.received_at
		FROM webhook_events we
		JOIN operation_runs op ON op.id=we.operation_run_id
		UNION ALL
		-- Compatibility edge for older graph consumers that linked connections straight
		-- to operations before webhook_event became a first-class asset node.
		SELECT
			'webhook_connection:' || wc.id::text || ':triggered_operation:operation_run:' || op.id::text,
			wc.project_id::text,
			'webhook_connection:' || wc.id::text,
			'operation_run:' || op.id::text,
			'triggered_operation',
			jsonb_build_object('provider', we.provider, 'event_type', we.event_type),
			we.received_at
		FROM webhook_events we
		JOIN operation_runs op ON op.id=we.operation_run_id
		JOIN webhook_connections wc ON wc.id=we.webhook_connection_id
		UNION ALL
		SELECT
			'repository:' || r.id::text || ':has_sync:repo_sync:' || rsa.id::text,
			r.project_id::text,
			'repository:' || r.id::text,
			'repo_sync:' || rsa.id::text,
			'has_sync',
			jsonb_build_object('trigger_mode', rsa.trigger_mode, 'sync_mode', rsa.sync_mode),
			rsa.created_at
		FROM project_git_repositories r
		JOIN repo_sync_assets rsa ON rsa.project_git_repository_id=r.id
		UNION ALL
		SELECT
			'repo_sync:' || rsa.id::text || ':synced_from:git_remote:' || source.id::text,
			rsa.project_id::text,
			'repo_sync:' || rsa.id::text,
			'git_remote:' || source.id::text,
			'synced_from',
			jsonb_build_object('remote_role', source.remote_role),
			rsa.created_at
		FROM repo_sync_assets rsa
		JOIN git_remotes source ON source.id=rsa.source_remote_id
		UNION ALL
		SELECT
			'repo_sync:' || rsa.id::text || ':mirrors_to:git_remote:' || target.id::text,
			rsa.project_id::text,
			'repo_sync:' || rsa.id::text,
			'git_remote:' || target.id::text,
			'mirrors_to',
			jsonb_build_object('remote_role', target.remote_role),
			rsa.created_at
		FROM repo_sync_assets rsa
		JOIN git_remotes target ON target.id=rsa.target_remote_id
		UNION ALL
		SELECT
			'git_remote:' || gr.id::text || ':receives:webhook_connection:' || wc.id::text,
			wc.project_id::text,
			'git_remote:' || gr.id::text,
			'webhook_connection:' || wc.id::text,
			'receives',
			jsonb_build_object('provider', wc.provider),
			wc.created_at
		FROM webhook_connections wc
		JOIN git_remotes gr ON gr.id=wc.source_remote_id
		UNION ALL
		SELECT
			'git_remote:' || gr.id::text || ':triggered_by:github_action_run:' || gh.id::text,
			r.project_id::text,
			'git_remote:' || gr.id::text,
			'github_action_run:' || gh.id::text,
			'triggered_by',
			jsonb_build_object('workflow_name', gh.workflow_name, 'run_id', gh.run_id),
			gh.created_at
		FROM github_action_runs gh
		JOIN git_remotes gr ON gr.id=gh.git_remote_id
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:ssh_machine:' || sm.id::text,
			p.id::text,
			'project:' || p.id::text,
			'ssh_machine:' || sm.id::text,
			'owns',
			'{}'::jsonb,
			sm.created_at
		FROM projects p
		JOIN ssh_machines sm ON sm.project_id=p.id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:argo_connection:' || ac.id::text,
			p.id::text,
			'project:' || p.id::text,
			'argo_connection:' || ac.id::text,
			'owns',
			'{}'::jsonb,
			ac.created_at
		FROM projects p
		JOIN argo_connections ac ON ac.project_id=p.id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:deployment_target:' || dt.id::text,
			p.id::text,
			'project:' || p.id::text,
			'deployment_target:' || dt.id::text,
			'owns',
			jsonb_build_object('environment', dt.environment, 'namespace', dt.namespace),
			dt.created_at
		FROM projects p
		JOIN deployment_targets dt ON dt.project_id=p.id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:deployment_record:' || dr.id::text,
			p.id::text,
			'project:' || p.id::text,
			'deployment_record:' || dr.id::text,
			'owns',
			jsonb_build_object('environment', dr.environment, 'namespace', dr.namespace),
			dr.created_at
		FROM projects p
		JOIN deployment_records dr ON dr.project_id=p.id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:rollback_point:' || rp.id::text,
			p.id::text,
			'project:' || p.id::text,
			'rollback_point:' || rp.id::text,
			'owns',
			jsonb_build_object('environment', rp.environment, 'revision', rp.revision),
			rp.created_at
		FROM projects p
		JOIN rollback_points rp ON rp.project_id=p.id
		UNION ALL
		SELECT
			'argo_connection:' || ac.id::text || ':manages:argo_app:' || aa.id::text,
			aa.project_id::text,
			'argo_connection:' || ac.id::text,
			'argo_app:' || aa.id::text,
			'manages',
			jsonb_build_object('namespace', aa.namespace),
			aa.created_at
		FROM argo_apps aa
		JOIN argo_connections ac ON ac.id=aa.argo_connection_id
		UNION ALL
		SELECT
			'argo_app:' || aa.id::text || ':deployed_to:deployment_target:' || dt.id::text,
			aa.project_id::text,
			'argo_app:' || aa.id::text,
			'deployment_target:' || dt.id::text,
			'deployed_to',
			jsonb_build_object('environment', dt.environment, 'namespace', dt.namespace, 'cluster_name', dt.cluster_name),
			aa.created_at
		FROM argo_apps aa
		JOIN deployment_targets dt ON dt.id=aa.deployment_target_id
		UNION ALL
		SELECT
			'deployment_target:' || dt.id::text || ':hosts:argo_app:' || aa.id::text,
			aa.project_id::text,
			'deployment_target:' || dt.id::text,
			'argo_app:' || aa.id::text,
			'hosts',
			jsonb_build_object('environment', dt.environment, 'namespace', dt.namespace, 'cluster_name', dt.cluster_name),
			aa.created_at
		FROM argo_apps aa
		JOIN deployment_targets dt ON dt.id=aa.deployment_target_id
		UNION ALL
		SELECT
			'deployment_record:' || dr.id::text || ':deployed_to:deployment_target:' || dt.id::text,
			dr.project_id::text,
			'deployment_record:' || dr.id::text,
			'deployment_target:' || dt.id::text,
			'deployed_to',
			jsonb_build_object('environment', dr.environment, 'namespace', dr.namespace, 'revision', dr.revision),
			dr.created_at
		FROM deployment_records dr
		JOIN deployment_targets dt ON dt.id=dr.deployment_target_id
		UNION ALL
		SELECT
			'deployment_record:' || dr.id::text || ':has_rollback:rollback_point:' || rp.id::text,
			dr.project_id::text,
			'deployment_record:' || dr.id::text,
			'rollback_point:' || rp.id::text,
			'has_rollback',
			jsonb_build_object('revision', rp.revision, 'captured_at', rp.captured_at),
			rp.created_at
		FROM rollback_points rp
		JOIN deployment_records dr ON dr.id=rp.deployment_record_id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:ai_runtime:' || ar.id::text,
			p.id::text,
			'project:' || p.id::text,
			'ai_runtime:' || ar.id::text,
			'owns',
			jsonb_build_object('runtime_type', ar.runtime_type),
			ar.created_at
		FROM projects p
		JOIN ai_runtimes ar ON ar.project_id=p.id
		UNION ALL
		SELECT
			'project:' || p.id::text || ':owns:agent_task:' || at.id::text,
			p.id::text,
			'project:' || p.id::text,
			'agent_task:' || at.id::text,
			'owns',
			jsonb_build_object('status', at.status),
			at.created_at
		FROM projects p
		JOIN agent_tasks at ON at.project_id=p.id
		UNION ALL
		SELECT
			'agent_task:' || at.id::text || ':uses_runtime:ai_runtime:' || runtime.id::text,
			at.project_id::text,
			'agent_task:' || at.id::text,
			'ai_runtime:' || runtime.id::text,
			'uses_runtime',
			jsonb_build_object('runtime_type', runtime.runtime_type),
			at.updated_at
		FROM agent_tasks at
		JOIN LATERAL (
			SELECT ar.id, ar.runtime_type
			FROM ai_runtimes ar
			WHERE ar.project_id=at.project_id OR ar.project_id IS NULL
			ORDER BY
				CASE WHEN ar.project_id=at.project_id THEN 0 ELSE 1 END,
				CASE WHEN ar.status='verified' THEN 0 ELSE 1 END,
				ar.updated_at DESC
			LIMIT 1
		) runtime ON true
		UNION ALL
		SELECT
			'agent_task:' || at.id::text || ':records_tool_call:agent_tool_call:' || atc.id::text,
			COALESCE(atc.project_id::text, at.project_id::text, ''),
			'agent_task:' || at.id::text,
			'agent_tool_call:' || atc.id::text,
			'records_tool_call',
			jsonb_build_object('tool_name', atc.tool_name, 'status', atc.status),
			atc.created_at
		FROM agent_tool_calls atc
		JOIN agent_tasks at ON at.id=atc.agent_task_id
		UNION ALL
		SELECT
			'operation_run:' || op.id::text || ':ran_tool_call:agent_tool_call:' || atc.id::text,
			COALESCE(atc.project_id::text, at.project_id::text, ''),
			'operation_run:' || op.id::text,
			'agent_tool_call:' || atc.id::text,
			'ran_tool_call',
			jsonb_build_object('tool_name', atc.tool_name, 'status', atc.status),
			atc.created_at
		FROM agent_tool_calls atc
		JOIN agent_tasks at ON at.id=atc.agent_task_id
		JOIN operation_runs op ON op.id=atc.operation_run_id
		UNION ALL
		SELECT
			ar.id::text,
			ar.project_id::text,
			from_asset.asset_type || ':' || from_asset.source_id::text,
			to_asset.asset_type || ':' || to_asset.source_id::text,
			ar.relation_type,
			ar.metadata,
			ar.created_at
		FROM asset_relations ar
		JOIN assets from_asset ON from_asset.id=ar.from_asset_id
		JOIN assets to_asset ON to_asset.id=ar.to_asset_id
		WHERE from_asset.source_id IS NOT NULL
			AND to_asset.source_id IS NOT NULL
			AND ar.metadata->>'source'='manual'
	)
`
}

func assetDependencySQL(direction string) string {
	startColumn := "from_asset_id"
	nextColumn := "to_asset_id"
	if direction == "upstream" {
		startColumn = "to_asset_id"
		nextColumn = "from_asset_id"
	}
	return fmt.Sprintf(`
	%s,
	asset_dependency_walk AS (
		SELECT
			ari.id,
			ari.project_id,
			ari.from_asset_id,
			ari.to_asset_id,
			ari.relation_type,
			1 AS depth,
			ARRAY[ari.from_asset_id, ari.to_asset_id]::text[] AS path_assets,
			ari.%[3]s AS current_asset_id,
			(ari.from_asset_id || ' --' || ari.relation_type || '--> ' || ari.to_asset_id) AS path_text,
			ari.created_at
		FROM asset_relation_inventory ari
		WHERE ari.%[2]s=$1
			AND ($2='' OR ari.project_id=$2)
		UNION ALL
		SELECT
			next.id,
			next.project_id,
			next.from_asset_id,
			next.to_asset_id,
			next.relation_type,
			walk.depth + 1,
			walk.path_assets || next.%[3]s,
			next.%[3]s,
			walk.path_text || ' | ' || next.from_asset_id || ' --' || next.relation_type || '--> ' || next.to_asset_id,
			next.created_at
		FROM asset_dependency_walk walk
		JOIN asset_relation_inventory next ON next.%[2]s=walk.current_asset_id
		WHERE walk.depth < $3
			AND ($2='' OR next.project_id=$2)
			AND NOT next.%[3]s = ANY(walk.path_assets)
	),
	asset_dependency_paths AS (
		SELECT
			id,
			project_id,
			from_asset_id,
			to_asset_id,
			relation_type,
			depth,
			path_assets,
			current_asset_id,
			path_text,
			created_at
		FROM asset_dependency_walk
		LIMIT 501
	)
`, assetRelationInventorySQL(), startColumn, nextColumn)
}

func (s *Server) createRemoteOperation(tool string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&input)
		}
		remote, err := queryOne(r.Context(), s.store.DB, "SELECT gr.*, pgr.project_id FROM git_remotes gr JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id WHERE gr.id=$1", chi.URLParam(r, "id"))
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		remoteID := chi.URLParam(r, "id")
		payload := map[string]any{"kind": "remote_operation", "tool": tool, "remote_id": remoteID, "input": input}
		if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "git_remote", ID: remoteID, ProjectID: fmt.Sprint(remote["project_id"])}, tool, tool+" "+fmt.Sprint(remote["name"]), payload) {
			return
		}
		tx, err := s.store.DB.BeginTxx(r.Context(), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not start operation transaction")
			return
		}
		defer tx.Rollback()
		op, err := s.enqueueRemoteOperationRun(r.Context(), tx, remoteID, tool, input, currentUser(r).ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !s.syncCanonicalAssetsInTransaction(w, r, tx, "remote_operation.enqueue") {
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "could not commit operation")
			return
		}
		writeCreatedOne(w, op, err)
	}
}

func createRemoteOperationRun(ctx context.Context, tx *sqlx.Tx, op, remote, input map[string]any, actorID, tool string) error {
	switch tool {
	case "repo.sync":
		sourceID := stringFromMap(input, "source_remote_id")
		if sourceID == "" {
			sourceID = fmt.Sprint(remote["id"])
		}
		repoID := fmt.Sprint(remote["project_git_repository_id"])
		targetID := stringFromMap(input, "target_remote_id")
		if targetID == "" {
			targetID = stringFromMap(input, "target_id")
		}
		if targetID == "" {
			targetIDs, err := defaultTargetRemoteIDs(ctx, tx, repoID, sourceID)
			if err != nil {
				return err
			}
			if len(targetIDs) > 0 {
				targetID = targetIDs[0]
			}
		}
		if targetID == "" {
			return fmt.Errorf("target_remote_id is required")
		}
		if targetID == sourceID {
			return fmt.Errorf("target_remote_id must be different from source_remote_id")
		}
		if _, err := remoteForRepository(ctx, tx, repoID, sourceID); err != nil {
			return fmt.Errorf("source remote not found in repository")
		}
		if _, err := remoteForRepository(ctx, tx, repoID, targetID); err != nil {
			return fmt.Errorf("target remote not found in repository")
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO repo_sync_runs(
				operation_run_id, git_remote_id, project_id, project_git_repository_id,
				source_remote_id, target_remote_id, ref, actor_user_id, status
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'queued')`,
			op["id"],
			targetID,
			remote["project_id"],
			remote["project_git_repository_id"],
			sourceID,
			targetID,
			refsSummaryFromInput(input),
			actorID,
		)
		return err
	case "repo.tag":
		_, err := tx.ExecContext(ctx, `
			INSERT INTO repo_tag_runs(
				operation_run_id, git_remote_id, project_id, project_git_repository_id,
				target_remote_id, tag_name, target_sha, tag_message, actor_user_id, status
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued')`,
			op["id"],
			remote["id"],
			remote["project_id"],
			remote["project_git_repository_id"],
			remote["id"],
			stringFromMap(input, "tag_name", "tag"),
			stringFromMap(input, "target_sha", "sha"),
			stringFromMap(input, "tag_message", "message"),
			actorID,
		)
		return err
	default:
		return nil
	}
}

type repositoryTagRequest struct {
	TargetRemoteIDs []string `json:"target_remote_ids"`
	TagName         string   `json:"tag_name"`
	TargetSHA       string   `json:"target_sha"`
	Branch          string   `json:"branch"`
	TagMessage      string   `json:"tag_message"`
}

func (s *Server) enqueueRepositoryTagRuns(ctx context.Context, tx *sqlx.Tx, repoID string, req repositoryTagRequest, actorID string) ([]map[string]any, error) {
	if strings.TrimSpace(req.TagName) == "" {
		return nil, fmt.Errorf("tag_name is required")
	}
	repo, err := queryOne(ctx, tx, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	if err != nil {
		return nil, err
	}
	targetIDs := req.TargetRemoteIDs
	if len(targetIDs) == 0 {
		targetIDs, err = defaultGitHubRemoteIDs(ctx, tx, repoID)
		if err != nil {
			return nil, fmt.Errorf("could not select GitHub remotes")
		}
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("target_remote_ids is required")
	}
	var runs []map[string]any
	for _, targetID := range targetIDs {
		target, err := remoteForRepository(ctx, tx, repoID, targetID)
		if err != nil {
			return nil, fmt.Errorf("target remote not found in repository")
		}
		input := map[string]any{
			"project_git_repository_id": repoID,
			"target_remote_id":          targetID,
			"tag_name":                  req.TagName,
			"target_sha":                req.TargetSHA,
			"branch":                    req.Branch,
			"tag_message":               req.TagMessage,
		}
		op, err := enqueueOperationTx(ctx, tx, fmt.Sprint(repo["project_id"]), targetID, "repo.create_tag", "tag "+req.TagName+" on "+fmt.Sprint(target["name"]), input, []string{"git"}, "")
		if err != nil {
			return nil, fmt.Errorf("could not enqueue tag")
		}
		run, err := queryOne(ctx, tx, `
			INSERT INTO repo_tag_runs(
				operation_run_id, git_remote_id, project_id, project_git_repository_id,
				target_remote_id, tag_name, target_sha, tag_message, actor_user_id, status
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued')
			RETURNING *`,
			op["id"],
			targetID,
			repo["project_id"],
			repoID,
			targetID,
			req.TagName,
			req.TargetSHA,
			req.TagMessage,
			actorID,
		)
		if err != nil {
			return nil, fmt.Errorf("could not create tag run")
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Server) enqueueRemoteOperationRun(ctx context.Context, tx *sqlx.Tx, remoteID, tool string, input map[string]any, actorID string) (map[string]any, error) {
	remote, err := queryOne(ctx, tx, "SELECT gr.*, pgr.project_id FROM git_remotes gr JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id WHERE gr.id=$1 FOR SHARE", remoteID)
	if err != nil {
		return nil, err
	}
	op, err := enqueueOperationTx(ctx, tx, fmt.Sprint(remote["project_id"]), remoteID, tool, tool+" "+fmt.Sprint(remote["name"]), input, []string{"git"}, "")
	if err != nil {
		return nil, fmt.Errorf("could not enqueue operation")
	}
	if err := createRemoteOperationRun(ctx, tx, op, remote, input, actorID, tool); err != nil {
		return nil, fmt.Errorf("could not create operation run")
	}
	return op, nil
}

func (s *Server) enqueueOperation(ctx context.Context, projectID, remoteID, tool, title string, input map[string]any, capabilities []string, preferredKind string) (map[string]any, error) {
	tx, err := s.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	op, err := enqueueOperationTx(ctx, tx, projectID, remoteID, tool, title, input, capabilities, preferredKind)
	if err != nil {
		return nil, err
	}
	if _, err := SyncCanonicalAssetsWith(ctx, tx); err != nil {
		return nil, fmt.Errorf("syncing canonical assets for operation enqueue: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return op, nil
}

func enqueueOperationTx(ctx context.Context, tx *sqlx.Tx, projectID, remoteID, tool, title string, input map[string]any, capabilities []string, preferredKind string) (map[string]any, error) {
	payload, err := jsonParam(input)
	if err != nil {
		return nil, err
	}
	op, err := queryOne(ctx, tx, `
		INSERT INTO operation_runs(project_id, git_remote_id, operation_type, title, input)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, $3, $4, $5::jsonb)
		RETURNING *`, projectID, remoteID, tool, title, payload)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO worker_jobs(operation_run_id, tool_name, payload, required_capabilities, preferred_node_kind)
		VALUES ($1, $2, $3::jsonb, $4, $5)`, op["id"], tool, payload, pq.Array(capabilities), preferredKind)
	if err != nil {
		return nil, err
	}
	return op, nil
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation"}, "read") {
		return
	}
	user := currentUser(r)
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT *
		FROM operation_runs op
		WHERE $1 OR op.project_id IS NULL OR EXISTS (
			SELECT 1 FROM project_members pm
			WHERE pm.project_id=op.project_id AND pm.user_id=$2
		)
		ORDER BY created_at DESC
		LIMIT 100`, userCanReadAllProjects(user), userIDOrNil(user))
	writeQueryResult(w, items, err)
}

func (s *Server) getWorkerQueueSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation"}, "read") {
		return
	}
	if !s.requirePolicy(w, r, PolicyResource{Type: "worker_node"}, "read") {
		return
	}
	user := currentUser(r)
	summary, err := queryOne(r.Context(), s.store.DB, workerQueueSummarySQL(), userCanReadAllProjects(user), userIDOrNil(user))
	writeQueryOne(w, summary, err)
}

func workerQueueSummarySQL() string {
	return `
		WITH visible_jobs AS (
			SELECT wj.*
			FROM worker_jobs wj
			LEFT JOIN operation_runs op ON op.id=wj.operation_run_id
			WHERE $1 OR op.project_id IS NULL OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id=op.project_id AND pm.user_id=$2
			)
		),
		job_status_counts AS (
			SELECT status, count(*)::int AS count
			FROM visible_jobs
			GROUP BY status
		),
		node_kind_counts AS (
			SELECT kind, count(*)::int AS count
			FROM worker_nodes
			GROUP BY kind
			ORDER BY count DESC, kind
		),
		queue_by_tool AS (
			SELECT tool_name, count(*)::int AS queued
			FROM visible_jobs
			WHERE status='queued'
			GROUP BY tool_name
			ORDER BY queued DESC, tool_name
			LIMIT 8
		),
		recent_failures AS (
			SELECT id, tool_name, error, updated_at
			FROM visible_jobs
			WHERE status='failed'
			ORDER BY updated_at DESC
			LIMIT 5
		)
		SELECT
			(SELECT count(*)::int FROM worker_nodes) AS total_nodes,
			(SELECT count(*)::int FROM worker_nodes WHERE status='online' AND last_heartbeat_at >= now() - interval '2 minutes') AS online_nodes,
			(SELECT count(*)::int FROM worker_nodes WHERE last_heartbeat_at < now() - interval '2 minutes') AS stale_nodes,
			(SELECT count(*)::int FROM visible_jobs) AS total_jobs,
			(SELECT count(*)::int FROM visible_jobs WHERE status='queued') AS queued_jobs,
			(SELECT count(*)::int FROM visible_jobs WHERE status='running') AS running_jobs,
			(SELECT count(*)::int FROM visible_jobs WHERE status='failed') AS failed_jobs,
			(SELECT count(*)::int FROM visible_jobs WHERE status='completed' AND updated_at >= now() - interval '24 hours') AS completed_24h,
			(SELECT count(*)::int FROM visible_jobs WHERE status='failed' AND updated_at >= now() - interval '24 hours') AS failed_24h,
			(SELECT count(*)::int FROM visible_jobs WHERE status='queued' AND created_at < now() - interval '15 minutes') AS aged_queued_jobs,
			(SELECT count(*)::int FROM visible_jobs WHERE status='running' AND started_at < now() - interval '15 minutes') AS stale_running_jobs,
			COALESCE((SELECT jsonb_object_agg(status, count) FROM job_status_counts), '{}'::jsonb) AS jobs_by_status,
			COALESCE((SELECT jsonb_agg(jsonb_build_object('kind', kind, 'count', count)) FROM node_kind_counts), '[]'::jsonb) AS nodes_by_kind,
			COALESCE((SELECT jsonb_agg(jsonb_build_object('tool_name', tool_name, 'queued', queued)) FROM queue_by_tool), '[]'::jsonb) AS queue_by_tool,
			COALESCE((SELECT jsonb_agg(jsonb_build_object('id', id, 'tool_name', tool_name, 'error', error, 'updated_at', updated_at)) FROM recent_failures), '[]'::jsonb) AS recent_failures`
}

func (s *Server) listOperationApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if err := s.expirePendingOperationApprovals(r.Context(), s.store.DB); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approvals")
		return
	}
	user := currentUser(r)
	filters, err := operationApprovalFiltersFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT
			oa.id,
			oa.project_id,
			oa.operation_run_id,
			oa.approval_rule_id,
			oa.resource_type,
			oa.resource_id,
			oa.action,
			oa.title,
			oa.status,
			oa.required_approver_roles,
			oa.required_approval_count,
			oa.notification_channels,
			oa.escalation_after_minutes,
			oa.escalation_channels,
			oa.last_escalated_at,
			oa.escalation_count,
			oa.notification_status,
			oa.requested_by,
			oa.decided_by,
			oa.decision_reason,
			oa.decided_at,
			oa.expires_at,
			oa.expired_at,
			oa.created_at,
			oa.updated_at,
			requester.email AS requested_by_email,
			decider.email AS decided_by_email,
			p.name AS project_name,
			COALESCE(decision_counts.approved_count, 0) AS approved_count,
			COALESCE(decision_counts.rejected_count, 0) AS rejected_count,
			EXISTS (
				SELECT 1 FROM operation_approval_delegations oadel
				WHERE oadel.operation_approval_id=oa.id
					AND oadel.to_user_id=$10
					AND oadel.revoked_at IS NULL
			) AS can_current_user_decide
		FROM operation_approvals oa
		LEFT JOIN users requester ON requester.id=oa.requested_by
		LEFT JOIN users decider ON decider.id=oa.decided_by
		LEFT JOIN projects p ON p.id=oa.project_id
		LEFT JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE decision='approved')::int AS approved_count,
				count(*) FILTER (WHERE decision='rejected')::int AS rejected_count
			FROM operation_approval_decisions oad
			WHERE oad.operation_approval_id=oa.id
		) decision_counts ON true
		WHERE ($3='' OR oa.status=$3)
			AND ($4='' OR oa.action=$4)
			AND ($5='' OR oa.resource_type=$5)
			AND ($6='' OR requester.email ILIKE $6 ESCAPE '\' OR oa.title ILIKE $6 ESCAPE '\' OR oa.resource_id ILIKE $6 ESCAPE '\')
			AND ($7='' OR requester.email ILIKE $7 ESCAPE '\')
			AND (NULLIF($8, '') IS NULL OR oa.created_at >= NULLIF($8, '')::timestamptz)
			AND (NULLIF($9, '') IS NULL OR oa.created_at <= NULLIF($9, '')::timestamptz)
			AND ($1 OR oa.project_id IS NULL OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id=oa.project_id AND pm.user_id=$2
			))
		ORDER BY oa.created_at DESC
		LIMIT 100`,
		userCanReadAllProjects(user),
		userIDOrNil(user),
		filters.Status,
		filters.Action,
		filters.ResourceType,
		likeContains(filters.Query),
		likeContains(filters.RequestedBy),
		filters.Since,
		filters.Until,
		userIDOrNil(user),
	)
	writeQueryResult(w, items, err)
}

func (s *Server) getOperationApprovalSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if err := s.expirePendingOperationApprovals(r.Context(), s.store.DB); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approvals")
		return
	}
	user := currentUser(r)
	canReadAll := userCanReadAllProjects(user)
	userID := userIDOrNil(user)
	summary, err := queryOne(r.Context(), s.store.DB, operationApprovalSummarySQL(), canReadAll, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval summary")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) listOperationApprovalReminderCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if err := s.expirePendingOperationApprovals(r.Context(), s.store.DB); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approvals")
		return
	}
	user := currentUser(r)
	items, err := queryMaps(r.Context(), s.store.DB, operationApprovalReminderCandidatesSQL(), userCanReadAllProjects(user), userIDOrNil(user))
	writeQueryResult(w, items, err)
}

func operationApprovalReminderCandidatesSQL() string {
	return `
		WITH visible AS (
			SELECT
				oa.id,
				oa.project_id,
				oa.action,
				oa.resource_type,
				oa.title,
				oa.status,
				oa.required_approval_count,
				oa.notification_status,
				oa.required_approver_roles,
				oa.expires_at,
				oa.last_reminded_at,
				oa.reminder_count,
				oa.escalation_after_minutes,
				oa.escalation_channels,
				oa.last_escalated_at,
				oa.escalation_count,
				oa.created_at,
				p.name AS project_name,
				requester.email AS requested_by_email,
				COALESCE(decision_counts.approved_count, 0)::int AS approved_count,
				EXISTS (
					SELECT 1 FROM operation_approval_delegations oadel
					WHERE oadel.operation_approval_id=oa.id
						AND oadel.to_user_id=$2
						AND oadel.revoked_at IS NULL
				) AS can_current_user_decide
			FROM operation_approvals oa
			LEFT JOIN projects p ON p.id=oa.project_id
			LEFT JOIN users requester ON requester.id=oa.requested_by
			LEFT JOIN LATERAL (
				SELECT count(*) FILTER (WHERE decision='approved')::int AS approved_count
				FROM operation_approval_decisions oad
				WHERE oad.operation_approval_id=oa.id
			) decision_counts ON true
			WHERE oa.status='pending'
				AND ($1 OR oa.project_id IS NULL OR EXISTS (
					SELECT 1 FROM project_members pm
					WHERE pm.project_id=oa.project_id AND pm.user_id=$2
				))
		)
		SELECT
			id,
			project_id,
			project_name,
			action,
			resource_type,
			title,
			status,
			required_approval_count,
			approved_count,
			notification_status,
			required_approver_roles,
			can_current_user_decide,
			requested_by_email,
			expires_at,
			last_reminded_at,
			reminder_count,
			escalation_after_minutes,
			escalation_channels,
			last_escalated_at,
			escalation_count,
			created_at,
			GREATEST(0, floor(EXTRACT(EPOCH FROM (now() - created_at)) / 60))::int AS age_minutes,
			CASE
				WHEN expires_at IS NULL THEN NULL
				ELSE floor(EXTRACT(EPOCH FROM (expires_at - now())) / 60)::int
			END AS minutes_until_expiry,
			CASE
				WHEN notification_status='failed' THEN 'notification_failed'
				WHEN escalation_after_minutes > 0 AND created_at <= now() - (escalation_after_minutes * interval '1 minute') AND approved_count < required_approval_count THEN 'escalation_due'
				WHEN expires_at IS NOT NULL AND expires_at <= now() + interval '15 minutes' THEN 'expires_soon'
				WHEN created_at <= now() - interval '30 minutes' AND approved_count < required_approval_count THEN 'waiting_for_approvers'
				WHEN expires_at IS NOT NULL AND expires_at <= now() + interval '1 hour' THEN 'approaching_expiry'
				ELSE 'watch'
			END AS reminder_reason,
			CASE
				WHEN notification_status='failed'
					OR (escalation_after_minutes > 0 AND created_at <= now() - (escalation_after_minutes * interval '1 minute') AND approved_count < required_approval_count)
					OR (expires_at IS NOT NULL AND expires_at <= now() + interval '15 minutes') THEN 'danger'
				WHEN created_at <= now() - interval '30 minutes' AND approved_count < required_approval_count THEN 'warning'
				WHEN expires_at IS NOT NULL AND expires_at <= now() + interval '1 hour' THEN 'warning'
				ELSE 'info'
			END AS escalation_level
		FROM visible
		WHERE notification_status='failed'
			OR (escalation_after_minutes > 0 AND created_at <= now() - (escalation_after_minutes * interval '1 minute') AND approved_count < required_approval_count)
			OR (expires_at IS NOT NULL AND expires_at <= now() + interval '1 hour')
			OR (created_at <= now() - interval '30 minutes' AND approved_count < required_approval_count)
		ORDER BY
			CASE
				WHEN notification_status='failed' THEN 0
				WHEN escalation_after_minutes > 0 AND created_at <= now() - (escalation_after_minutes * interval '1 minute') AND approved_count < required_approval_count THEN 1
				WHEN expires_at IS NOT NULL AND expires_at <= now() + interval '15 minutes' THEN 2
				WHEN created_at <= now() - interval '30 minutes' AND approved_count < required_approval_count THEN 3
				ELSE 4
			END,
			expires_at NULLS LAST,
			created_at ASC
		LIMIT 50`
}

func dueOperationApprovalRemindersSQL() string {
	return `
		WITH due AS (
			SELECT
				oa.id,
				COALESCE(decision_counts.approved_count, 0)::int AS approved_count
			FROM operation_approvals oa
			LEFT JOIN LATERAL (
				SELECT count(*) FILTER (WHERE decision='approved')::int AS approved_count
				FROM operation_approval_decisions oad
				WHERE oad.operation_approval_id=oa.id
			) decision_counts ON true
			WHERE oa.status='pending'
				AND (oa.last_reminded_at IS NULL OR oa.last_reminded_at <= now() - interval '60 minutes')
				AND (
					oa.notification_status='failed'
					OR (oa.expires_at IS NOT NULL AND oa.expires_at <= now() + interval '1 hour')
					OR (oa.created_at <= now() - interval '30 minutes' AND COALESCE(decision_counts.approved_count, 0) < oa.required_approval_count)
				)
			ORDER BY
				CASE
					WHEN oa.notification_status='failed' THEN 0
					WHEN oa.expires_at IS NOT NULL AND oa.expires_at <= now() + interval '15 minutes' THEN 1
					WHEN oa.created_at <= now() - interval '30 minutes' AND COALESCE(decision_counts.approved_count, 0) < oa.required_approval_count THEN 2
					ELSE 3
				END,
				oa.expires_at NULLS LAST,
				oa.created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 20
		)
		UPDATE operation_approvals oa
		SET last_reminded_at=now(),
			reminder_count=reminder_count + 1,
			updated_at=now()
		FROM due
		WHERE oa.id=due.id
		RETURNING oa.*, due.approved_count`
}

func (s *Server) dispatchDueOperationApprovalReminders(ctx context.Context) error {
	tx, err := s.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	items, err := queryMaps(ctx, tx, dueOperationApprovalRemindersSQL())
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, item := range items {
		item["required_approval_count"] = requiredApprovalCount(item["required_approval_count"])
		delete(item, "request_payload")
		s.dispatchApprovalNotification(ctx, item, "reminder")
	}
	return nil
}

func dueOperationApprovalEscalationsSQL() string {
	return `
		WITH due AS (
			SELECT
				oa.id,
				COALESCE(decision_counts.approved_count, 0)::int AS approved_count
			FROM operation_approvals oa
			LEFT JOIN LATERAL (
				SELECT count(*) FILTER (WHERE decision='approved')::int AS approved_count
				FROM operation_approval_decisions oad
				WHERE oad.operation_approval_id=oa.id
			) decision_counts ON true
			WHERE oa.status='pending'
				AND oa.escalation_after_minutes > 0
				AND oa.created_at <= now() - (oa.escalation_after_minutes * interval '1 minute')
				AND COALESCE(decision_counts.approved_count, 0) < oa.required_approval_count
				AND (oa.last_escalated_at IS NULL OR oa.last_escalated_at <= now() - interval '120 minutes')
			ORDER BY oa.created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 20
		)
		UPDATE operation_approvals oa
		SET last_escalated_at=now(),
			escalation_count=escalation_count + 1,
			updated_at=now()
		FROM due
		WHERE oa.id=due.id
		RETURNING oa.*, due.approved_count`
}

func (s *Server) dispatchDueOperationApprovalEscalations(ctx context.Context) error {
	tx, err := s.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	items, err := queryMaps(ctx, tx, dueOperationApprovalEscalationsSQL())
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, item := range items {
		item["required_approval_count"] = requiredApprovalCount(item["required_approval_count"])
		delete(item, "request_payload")
		s.dispatchApprovalNotification(ctx, item, "escalation")
	}
	return nil
}

func (s *Server) listOperationApprovalRules(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule"}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, operationApprovalRulesSQL())
	writeQueryResult(w, items, err)
}

func operationApprovalRulesSQL() string {
	return `
		SELECT id,
			resource_type,
			action,
			required_approver_roles,
			required_approval_count,
			expires_after_minutes,
			notification_channels,
			escalation_after_minutes,
			escalation_channels,
			priority,
			enabled,
			metadata,
			created_at,
			updated_at
		FROM operation_approval_rules
		ORDER BY enabled DESC, priority ASC, resource_type, action`
}

type operationApprovalRuleRequest struct {
	ResourceType           string         `json:"resource_type"`
	Action                 string         `json:"action"`
	RequiredApproverRoles  []string       `json:"required_approver_roles"`
	RequiredApprovalCount  int            `json:"required_approval_count"`
	ExpiresAfterMinutes    int            `json:"expires_after_minutes"`
	NotificationChannels   []string       `json:"notification_channels"`
	EscalationAfterMinutes int            `json:"escalation_after_minutes"`
	EscalationChannels     []string       `json:"escalation_channels"`
	Priority               int            `json:"priority"`
	Enabled                *bool          `json:"enabled"`
	Metadata               map[string]any `json:"metadata"`
}

func (s *Server) createOperationApprovalRule(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule"}, "create") {
		return
	}
	req, ok := decodeOperationApprovalRuleRequest(w, r, true)
	if !ok {
		return
	}
	metadata, _ := jsonParam(req.Metadata)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start approval rule transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO operation_approval_rules(
			resource_type,
			action,
			required_approver_roles,
			required_approval_count,
			expires_after_minutes,
			notification_channels,
			escalation_after_minutes,
			escalation_channels,
			priority,
			enabled,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
		RETURNING id, resource_type, action, required_approver_roles, required_approval_count, expires_after_minutes, notification_channels, escalation_after_minutes, escalation_channels, priority, enabled, metadata, created_at, updated_at`,
		req.ResourceType,
		req.Action,
		pq.Array(req.RequiredApproverRoles),
		req.RequiredApprovalCount,
		req.ExpiresAfterMinutes,
		pq.Array(req.NotificationChannels),
		req.EscalationAfterMinutes,
		pq.Array(req.EscalationChannels),
		req.Priority,
		*req.Enabled,
		metadata,
	)
	if err != nil {
		writeCreatedOne(w, item, err)
		return
	}
	if err := s.recordOperationApprovalRuleAudit(r.Context(), tx, item["id"], currentUser(r), "create", nil, item); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record approval rule audit")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval_rule.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval rule")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateOperationApprovalRule(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule", ID: chi.URLParam(r, "id")}, "update") {
		return
	}
	req, ok := decodeOperationApprovalRuleRequest(w, r, false)
	if !ok {
		return
	}
	metadata, _ := jsonParam(req.Metadata)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start approval rule transaction")
		return
	}
	defer tx.Rollback()
	before, err := queryOne(r.Context(), tx, `
		SELECT id, resource_type, action, required_approver_roles, required_approval_count, expires_after_minutes, notification_channels, escalation_after_minutes, escalation_channels, priority, enabled, metadata, created_at, updated_at
		FROM operation_approval_rules
		WHERE id=$1
		FOR UPDATE`, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	item, err := queryOne(r.Context(), tx, `
		UPDATE operation_approval_rules
		SET resource_type=$2,
			action=$3,
			required_approver_roles=$4,
			required_approval_count=$5,
			expires_after_minutes=$6,
			notification_channels=$7,
			escalation_after_minutes=$8,
			escalation_channels=$9,
			priority=$10,
			enabled=$11,
			metadata=$12::jsonb,
			updated_at=now()
		WHERE id=$1
		RETURNING id, resource_type, action, required_approver_roles, required_approval_count, expires_after_minutes, notification_channels, escalation_after_minutes, escalation_channels, priority, enabled, metadata, created_at, updated_at`,
		chi.URLParam(r, "id"),
		req.ResourceType,
		req.Action,
		pq.Array(req.RequiredApproverRoles),
		req.RequiredApprovalCount,
		req.ExpiresAfterMinutes,
		pq.Array(req.NotificationChannels),
		req.EscalationAfterMinutes,
		pq.Array(req.EscalationChannels),
		req.Priority,
		*req.Enabled,
		metadata,
	)
	if err != nil {
		writeQueryOne(w, item, err)
		return
	}
	if err := s.recordOperationApprovalRuleAudit(r.Context(), tx, item["id"], currentUser(r), "update", before, item); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record approval rule audit")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval_rule.update") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval rule")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listOperationApprovalRuleAudits(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule", ID: chi.URLParam(r, "id")}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT
			a.id,
			a.operation_approval_rule_id,
			a.actor_user_id,
			u.email AS actor_email,
			a.action,
			a.before_state,
			a.after_state,
			a.created_at
		FROM operation_approval_rule_audits a
		LEFT JOIN users u ON u.id=a.actor_user_id
		WHERE a.operation_approval_rule_id=$1
		ORDER BY a.created_at DESC
		LIMIT 100`, chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) recordOperationApprovalRuleAudit(ctx context.Context, db sqlx.ExtContext, ruleID any, actor *User, action string, before, after map[string]any) error {
	beforeJSON, err := jsonParam(nonNilMap(before))
	if err != nil {
		return err
	}
	afterJSON, err := jsonParam(nonNilMap(after))
	if err != nil {
		return err
	}
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO operation_approval_rule_audits(operation_approval_rule_id, actor_user_id, action, before_state, after_state)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4::jsonb, $5::jsonb)`,
		ruleID,
		actorID,
		action,
		beforeJSON,
		afterJSON,
	)
	return err
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func decodeOperationApprovalRuleRequest(w http.ResponseWriter, r *http.Request, create bool) (operationApprovalRuleRequest, bool) {
	var req operationApprovalRuleRequest
	if !decodeJSON(w, r, &req) {
		return req, false
	}
	req.ResourceType = strings.TrimSpace(req.ResourceType)
	req.Action = strings.TrimSpace(req.Action)
	if req.Action == "" {
		writeError(w, http.StatusBadRequest, "action is required")
		return req, false
	}
	if len(req.Action) > 80 || len(req.ResourceType) > 80 {
		writeError(w, http.StatusBadRequest, "rule key is too long")
		return req, false
	}
	req.RequiredApproverRoles = normalizeRuleStringList(req.RequiredApproverRoles, []string{"admin", "owner"})
	req.NotificationChannels = normalizeRuleStringList(req.NotificationChannels, []string{"ui"})
	req.EscalationChannels = normalizeRuleStringList(req.EscalationChannels, nil)
	if req.RequiredApprovalCount < 1 {
		req.RequiredApprovalCount = 1
	}
	if req.RequiredApprovalCount > len(req.RequiredApproverRoles) {
		writeError(w, http.StatusBadRequest, "required_approval_count cannot exceed approver role count")
		return req, false
	}
	if req.ExpiresAfterMinutes <= 0 {
		req.ExpiresAfterMinutes = 1440
	}
	if req.ExpiresAfterMinutes > 43200 {
		writeError(w, http.StatusBadRequest, "expires_after_minutes must be <= 43200")
		return req, false
	}
	if req.EscalationAfterMinutes < 0 {
		writeError(w, http.StatusBadRequest, "escalation_after_minutes must be >= 0")
		return req, false
	}
	if req.EscalationAfterMinutes > 43200 {
		writeError(w, http.StatusBadRequest, "escalation_after_minutes must be <= 43200")
		return req, false
	}
	if req.Priority == 0 && create {
		req.Priority = 100
	}
	if req.Enabled == nil {
		enabled := true
		req.Enabled = &enabled
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}
	return req, true
}

func normalizeRuleStringList(values []string, fallback []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.ToLower(strings.TrimSpace(value))
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		result = append(result, clean)
	}
	if len(result) == 0 {
		return fallback
	}
	return result
}

func (s *Server) listOperationApprovalViews(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	user := currentUser(r)
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, user_id, name, filters, created_at, updated_at
		FROM operation_approval_views
		WHERE user_id=$1
		ORDER BY updated_at DESC, name
		LIMIT 200`, userIDOrNil(user))
	writeQueryResult(w, items, err)
}

func (s *Server) createOperationApprovalView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	var req struct {
		Name    string         `json:"name"`
		Filters map[string]any `json:"filters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 80 {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	filters, err := sanitizeOperationApprovalViewFilters(req.Filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload, err := jsonParam(filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid filters")
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO operation_approval_views(user_id, name, filters)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, user_id, name, filters, created_at, updated_at`,
		userIDOrNil(currentUser(r)),
		name,
		payload,
	)
	if err != nil {
		if isUniqueViolation(err, "operation_approval_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an approval view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not create approval view")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateOperationApprovalView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	var req struct {
		Name    string          `json:"name"`
		Filters json.RawMessage `json:"filters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if req.Name != "" && name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 80 {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	var payload any
	if len(req.Filters) > 0 && string(req.Filters) != "null" {
		var raw map[string]any
		if err := json.Unmarshal(req.Filters, &raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
		filters, err := sanitizeOperationApprovalViewFilters(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		encoded, err := jsonParam(filters)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
		payload = encoded
	}
	item, err := queryOne(r.Context(), s.store.DB, `
		UPDATE operation_approval_views
		SET name=COALESCE(NULLIF($3, ''), name),
			filters=COALESCE($4::jsonb, filters),
			updated_at=now()
		WHERE id=$1 AND user_id=$2
		RETURNING id, user_id, name, filters, created_at, updated_at`,
		chi.URLParam(r, "id"),
		userIDOrNil(currentUser(r)),
		name,
		payload,
	)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		if isUniqueViolation(err, "operation_approval_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an approval view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not update approval view")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteOperationApprovalView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	result, err := s.store.DB.ExecContext(r.Context(), `
		DELETE FROM operation_approval_views
		WHERE id=$1 AND user_id=$2`,
		chi.URLParam(r, "id"),
		userIDOrNil(currentUser(r)),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete approval view")
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete approval view")
		return
	}
	if count == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func operationApprovalSummarySQL() string {
	return `
		WITH visible AS (
			SELECT oa.*
			FROM operation_approvals oa
			WHERE ($1 OR oa.project_id IS NULL OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id=oa.project_id AND pm.user_id=$2
			))
		),
		status_counts AS (
			SELECT status, count(*)::int AS count
			FROM visible
			GROUP BY status
		),
		action_counts AS (
			SELECT action, count(*)::int AS count
			FROM visible
			GROUP BY action
			ORDER BY count DESC, action
			LIMIT 8
		)
		SELECT
			(SELECT count(*)::int FROM visible) AS total,
			(SELECT count(*)::int FROM visible WHERE status='pending') AS pending,
			(SELECT count(*)::int FROM visible WHERE status='approved') AS approved,
			(SELECT count(*)::int FROM visible WHERE status='rejected') AS rejected,
			(SELECT count(*)::int FROM visible WHERE status='expired') AS expired,
			(SELECT count(*)::int FROM visible WHERE status='pending' AND expires_at IS NOT NULL AND expires_at <= now() + interval '1 hour') AS expiring_soon,
			(SELECT count(*)::int FROM visible WHERE notification_status='failed') AS notification_failed,
			COALESCE((SELECT jsonb_object_agg(status, count) FROM status_counts), '{}'::jsonb) AS by_status,
			COALESCE((SELECT jsonb_agg(jsonb_build_object('action', action, 'count', count)) FROM action_counts), '[]'::jsonb) AS by_action`
}

type operationApprovalFilters struct {
	Status       string
	Action       string
	ResourceType string
	Query        string
	RequestedBy  string
	Since        string
	Until        string
}

func operationApprovalFiltersFromRequest(r *http.Request) (operationApprovalFilters, error) {
	q := r.URL.Query()
	filters := operationApprovalFilters{
		Status:       strings.TrimSpace(q.Get("status")),
		Action:       strings.TrimSpace(q.Get("action")),
		ResourceType: strings.TrimSpace(q.Get("resource_type")),
		Query:        strings.TrimSpace(q.Get("q")),
		RequestedBy:  strings.TrimSpace(q.Get("requested_by")),
		Since:        strings.TrimSpace(q.Get("since")),
		Until:        strings.TrimSpace(q.Get("until")),
	}
	if err := validateOptionalRFC3339("since", filters.Since); err != nil {
		return operationApprovalFilters{}, err
	}
	if err := validateOptionalRFC3339("until", filters.Until); err != nil {
		return operationApprovalFilters{}, err
	}
	return filters, nil
}

func sanitizeOperationApprovalViewFilters(input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	status := approvalViewFilterString(input, "status", 40)
	if status != "" {
		switch status {
		case "pending", "approved", "rejected", "expired":
			out["status"] = status
		default:
			return nil, fmt.Errorf("status is invalid")
		}
	}
	for _, item := range []struct {
		key   string
		limit int
	}{
		{key: "action", limit: 120},
		{key: "resource_type", limit: 80},
		{key: "q", limit: 160},
		{key: "requested_by", limit: 160},
	} {
		if value := approvalViewFilterString(input, item.key, item.limit); value != "" {
			out[item.key] = value
		}
	}
	for _, key := range []string{"since", "until"} {
		value := approvalViewFilterString(input, key, 80)
		if value == "" {
			continue
		}
		if err := validateOptionalRFC3339(key, value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func sanitizeAssetGraphViewFilters(input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, item := range []struct {
		key   string
		limit int
	}{
		{key: "project_id", limit: 80},
		{key: "asset_type", limit: 80},
		{key: "q", limit: 160},
		{key: "selected_asset_id", limit: 180},
	} {
		if value := approvalViewFilterString(input, item.key, item.limit); value != "" {
			out[item.key] = value
		}
	}
	return out, nil
}

func approvalViewFilterString(input map[string]any, key string, limit int) string {
	if input == nil {
		return ""
	}
	value, ok := input[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) > limit {
		text = text[:limit]
	}
	return text
}

func likeContains(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range value {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}

func (s *Server) getOperationApproval(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if expired, err := s.expireOperationApprovalByID(r.Context(), s.store.DB, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	} else if expired != nil {
		s.dispatchApprovalNotification(r.Context(), expired, "expired")
	}
	approval, err := queryOne(r.Context(), s.store.DB, `
		SELECT
			oa.id,
			oa.project_id,
			oa.operation_run_id,
			oa.approval_rule_id,
			oa.resource_type,
			oa.resource_id,
			oa.action,
			oa.title,
			oa.status,
			oa.required_approver_roles,
			oa.required_approval_count,
			oa.notification_channels,
			oa.escalation_after_minutes,
			oa.escalation_channels,
			oa.last_escalated_at,
			oa.escalation_count,
			oa.notification_status,
			oa.notification_last_error,
			oa.requested_by,
			oa.decided_by,
			oa.decision_reason,
			oa.decided_at,
			oa.expires_at,
			oa.expired_at,
			oa.created_at,
			oa.updated_at,
			requester.email AS requested_by_email,
			decider.email AS decided_by_email,
			p.name AS project_name,
			COALESCE(decision_counts.approved_count, 0) AS approved_count,
			COALESCE(decision_counts.rejected_count, 0) AS rejected_count,
			EXISTS (
				SELECT 1 FROM operation_approval_delegations oadel
				WHERE oadel.operation_approval_id=oa.id
					AND oadel.to_user_id=$2
					AND oadel.revoked_at IS NULL
			) AS can_current_user_decide
		FROM operation_approvals oa
		LEFT JOIN users requester ON requester.id=oa.requested_by
		LEFT JOIN users decider ON decider.id=oa.decided_by
		LEFT JOIN projects p ON p.id=oa.project_id
		LEFT JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE decision='approved')::int AS approved_count,
				count(*) FILTER (WHERE decision='rejected')::int AS rejected_count
			FROM operation_approval_decisions oad
			WHERE oad.operation_approval_id=oa.id
		) decision_counts ON true
		WHERE oa.id=$1`, chi.URLParam(r, "id"), userIDOrNil(currentUser(r)))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	opID := cleanOptionalID(fmt.Sprint(approval["operation_run_id"]))
	response := map[string]any{"approval": approval}
	decisions, err := s.operationApprovalDecisions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval decisions")
		return
	}
	response["decisions"] = decisions
	delegations, err := s.operationApprovalDelegations(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval delegations")
		return
	}
	response["delegations"] = delegations
	if opID == "" {
		response["operation"] = nil
		response["operation_logs"] = []map[string]any{}
		response["worker_jobs"] = []map[string]any{}
		response["run_records"] = map[string]any{}
		writeJSON(w, http.StatusOK, response)
		return
	}
	operation, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM operation_runs WHERE id=$1", opID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "could not load approval operation")
		return
	}
	if operation != nil && !s.requireOperationRead(w, r, operation) {
		return
	}
	operation = safeOperationForAudit(operation)
	logs, err := queryMaps(r.Context(), s.store.DB, "SELECT id, operation_run_id, worker_job_id, level, message, fields, created_at FROM operation_logs WHERE operation_run_id=$1 ORDER BY created_at", opID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval operation logs")
		return
	}
	jobs, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, operation_run_id, tool_name, status, error, required_capabilities, preferred_node_kind, assigned_worker_node_id, claimed_at, started_at, finished_at, created_at, updated_at
		FROM worker_jobs
		WHERE operation_run_id=$1
		ORDER BY created_at`, opID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval worker jobs")
		return
	}
	canReadSSHOutput := NewPolicyChecker().Check(currentUser(r), PolicyResource{Type: "ssh_command_run"}, "read").Effect == PolicyAllow
	runRecords, err := s.operationRunRecords(r.Context(), opID, canReadSSHOutput)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval run records")
		return
	}
	response["operation"] = operation
	response["operation_logs"] = logs
	response["worker_jobs"] = jobs
	response["run_records"] = runRecords
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) operationApprovalDecisions(ctx context.Context, approvalID string) ([]map[string]any, error) {
	return queryMaps(ctx, s.store.DB, `
		SELECT
			oad.id,
			oad.operation_approval_id,
			oad.user_id,
			u.email AS user_email,
			oad.decision,
			oad.reason,
			oad.decided_at,
			oad.created_at,
			oad.updated_at
		FROM operation_approval_decisions oad
		LEFT JOIN users u ON u.id=oad.user_id
		WHERE oad.operation_approval_id=$1
		ORDER BY oad.decided_at DESC`, approvalID)
}

func (s *Server) operationApprovalDelegations(ctx context.Context, approvalID string) ([]map[string]any, error) {
	return queryMaps(ctx, s.store.DB, `
		SELECT
			oadel.id,
			oadel.operation_approval_id,
			oadel.from_user_id,
			from_user.email AS from_user_email,
			oadel.to_user_id,
			to_user.email AS to_user_email,
			oadel.reason,
			oadel.revoked_at,
			oadel.created_at,
			oadel.updated_at
		FROM operation_approval_delegations oadel
		LEFT JOIN users from_user ON from_user.id=oadel.from_user_id
		LEFT JOIN users to_user ON to_user.id=oadel.to_user_id
		WHERE oadel.operation_approval_id=$1
		ORDER BY oadel.created_at DESC`, approvalID)
}

func (s *Server) requireApprovalRead(w http.ResponseWriter, r *http.Request, approval map[string]any) bool {
	projectID := cleanOptionalID(fmt.Sprint(approval["project_id"]))
	if projectID == "" {
		return s.requirePolicy(w, r, PolicyResource{Type: "operation_approval", ID: fmt.Sprint(approval["id"])}, "read")
	}
	return s.requireProjectPolicy(w, r, PolicyResource{Type: "operation_approval", ID: fmt.Sprint(approval["id"]), ProjectID: projectID}, "read")
}

func safeOperationForAudit(operation map[string]any) map[string]any {
	if operation == nil {
		return nil
	}
	return map[string]any{
		"id":             operation["id"],
		"project_id":     operation["project_id"],
		"git_remote_id":  operation["git_remote_id"],
		"operation_type": operation["operation_type"],
		"status":         operation["status"],
		"title":          operation["title"],
		"error":          operation["error"],
		"started_at":     operation["started_at"],
		"finished_at":    operation["finished_at"],
		"created_at":     operation["created_at"],
		"updated_at":     operation["updated_at"],
	}
}

func (s *Server) operationRunRecords(ctx context.Context, opID string, canReadSSHOutput bool) (map[string]any, error) {
	records := map[string]any{}
	queries := map[string]string{
		"repo_sync_runs": `
			SELECT id, operation_run_id, project_id, project_git_repository_id, repo_sync_asset_id, source_remote_id, target_remote_id, ref, before_sha, after_sha, status, error_message, started_at, finished_at, created_at
			FROM repo_sync_runs
			WHERE operation_run_id=$1
			ORDER BY created_at`,
		"repo_tag_runs": `
			SELECT id, operation_run_id, project_id, project_git_repository_id, target_remote_id, tag_name, target_sha, status, error_message, started_at, finished_at, created_at
			FROM repo_tag_runs
			WHERE operation_run_id=$1
			ORDER BY created_at`,
		"project_template_runs": `
			SELECT id, operation_run_id, project_template_id, requested_by, project_id, project_name, project_slug, status, steps, error_message, started_at, finished_at, created_at, updated_at
			FROM project_template_runs
			WHERE operation_run_id=$1
			ORDER BY created_at`,
		"webhook_events": `
			SELECT id, webhook_connection_id, project_id, provider, event_type, delivery_id, signature_valid, matched_repo_sync_asset_id, operation_run_id, status, error_message, processed_at, received_at
			FROM webhook_events
			WHERE operation_run_id=$1
			ORDER BY received_at`,
		"agent_tool_calls": `
			SELECT id, agent_task_id, operation_run_id, project_id, tool_name, input, output, status, error_message, started_at, finished_at, created_at, updated_at
			FROM agent_tool_calls
			WHERE operation_run_id=$1
			ORDER BY created_at`,
	}
	for key, query := range queries {
		items, err := queryMaps(ctx, s.store.DB, query, opID)
		if err != nil {
			return nil, err
		}
		records[key] = items
	}
	sshQuery := `
		SELECT id, operation_run_id, ssh_machine_id, project_id, actor_user_id, status, exit_code, error_message, started_at, finished_at, created_at
		FROM ssh_command_runs
		WHERE operation_run_id=$1
		ORDER BY created_at`
	if canReadSSHOutput {
		sshQuery = `
			SELECT id, operation_run_id, ssh_machine_id, project_id, command, actor_user_id, status, exit_code, stdout, stderr, error_message, started_at, finished_at, created_at
			FROM ssh_command_runs
			WHERE operation_run_id=$1
			ORDER BY created_at`
	}
	sshItems, err := queryMaps(ctx, s.store.DB, sshQuery, opID)
	if err != nil {
		return nil, err
	}
	records["ssh_command_runs"] = sshItems
	return records, nil
}

func (s *Server) approveOperationApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start approval transaction")
		return
	}
	defer tx.Rollback()
	expired, err := s.expireOperationApprovalByID(r.Context(), tx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	}
	approval, err := queryOne(r.Context(), tx, "SELECT * FROM operation_approvals WHERE id=$1 FOR UPDATE", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if fmt.Sprint(approval["status"]) != "pending" {
		if expired != nil {
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, "could not commit expired approval")
				return
			}
			s.dispatchApprovalNotification(r.Context(), expired, "expired")
		}
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	if !s.canDecideOperationApproval(r.Context(), currentUser(r), approval) {
		writeError(w, http.StatusForbidden, "approval decision requires one of the configured approver roles")
		return
	}
	if err := upsertOperationApprovalDecision(r.Context(), tx, chi.URLParam(r, "id"), currentUser(r).ID, "approved", strings.TrimSpace(req.Reason)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record approval decision")
		return
	}
	approvedCount, err := operationApprovalApprovedCount(r.Context(), tx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not count approval decisions")
		return
	}
	requiredCount := requiredApprovalCount(approval["required_approval_count"])
	if approvedCount < requiredCount {
		item, err := queryOne(r.Context(), tx, `
			UPDATE operation_approvals
			SET decided_by=$2,
				decision_reason=$3,
				updated_at=now()
			WHERE id=$1 AND status='pending'
			RETURNING *`, chi.URLParam(r, "id"), currentUser(r).ID, strings.TrimSpace(req.Reason))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not update approval progress")
			return
		}
		if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.progress") {
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "could not commit approval progress")
			return
		}
		delete(item, "request_payload")
		item["approved_count"] = approvedCount
		item["required_approval_count"] = requiredCount
		writeJSON(w, http.StatusOK, item)
		return
	}
	result, operationRunID, err := s.executeApprovedOperation(r.Context(), tx, approval)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resultJSON, err := jsonParam(result)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid approval result")
		return
	}
	item, err := queryOne(r.Context(), tx, `
		UPDATE operation_approvals
		SET status='approved',
			operation_run_id=NULLIF($2,'')::uuid,
			decided_by=$3,
			decision_reason=$4,
			decided_at=now(),
			updated_at=now(),
			request_payload=request_payload || jsonb_build_object('approval_result', $5::jsonb)
		WHERE id=$1 AND status='pending'
		RETURNING *`, chi.URLParam(r, "id"), operationRunID, currentUser(r).ID, strings.TrimSpace(req.Reason), resultJSON)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update approval")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.execute") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval")
		return
	}
	delete(item, "request_payload")
	item["approved_count"] = approvedCount
	item["required_approval_count"] = requiredCount
	item = s.dispatchApprovalNotification(r.Context(), item, "approved")
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) rejectOperationApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start approval transaction")
		return
	}
	defer tx.Rollback()
	expired, err := s.expireOperationApprovalByID(r.Context(), tx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	}
	approval, err := queryOne(r.Context(), tx, "SELECT * FROM operation_approvals WHERE id=$1 FOR UPDATE", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if fmt.Sprint(approval["status"]) != "pending" {
		if expired != nil {
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, "could not commit expired approval")
				return
			}
			s.dispatchApprovalNotification(r.Context(), expired, "expired")
		}
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	if !s.canDecideOperationApproval(r.Context(), currentUser(r), approval) {
		writeError(w, http.StatusForbidden, "approval decision requires one of the configured approver roles")
		return
	}
	if err := upsertOperationApprovalDecision(r.Context(), tx, chi.URLParam(r, "id"), currentUser(r).ID, "rejected", strings.TrimSpace(req.Reason)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record approval decision")
		return
	}
	item, err := queryOne(r.Context(), tx, `
		UPDATE operation_approvals
		SET status='rejected', decided_by=$2, decision_reason=$3, decided_at=now(), updated_at=now()
		WHERE id=$1 AND status='pending'
		RETURNING *`, chi.URLParam(r, "id"), currentUser(r).ID, strings.TrimSpace(req.Reason))
	if err != nil {
		writeQueryOne(w, item, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.reject") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval")
		return
	}
	delete(item, "request_payload")
	item = s.dispatchApprovalNotification(r.Context(), item, "rejected")
	writeQueryOne(w, item, err)
}

func (s *Server) remindOperationApproval(w http.ResponseWriter, r *http.Request) {
	expired, err := s.expireOperationApprovalByID(r.Context(), s.store.DB, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	}
	if expired != nil {
		s.dispatchApprovalNotification(r.Context(), expired, "expired")
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	approval, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM operation_approvals WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	if fmt.Sprint(approval["status"]) != "pending" {
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	if !s.canDecideOperationApproval(r.Context(), currentUser(r), approval) {
		writeError(w, http.StatusForbidden, "approval reminder requires one of the configured approver roles")
		return
	}
	approvedCount, err := operationApprovalApprovedCount(r.Context(), s.store.DB, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not count approval decisions")
		return
	}
	approval["approved_count"] = approvedCount
	approval["required_approval_count"] = requiredApprovalCount(approval["required_approval_count"])
	delete(approval, "request_payload")
	approval, err = queryOne(r.Context(), s.store.DB, `
		UPDATE operation_approvals
		SET last_reminded_at=now(),
			reminder_count=reminder_count + 1,
			updated_at=now()
		WHERE id=$1 AND status='pending'
		RETURNING *`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record approval reminder")
		return
	}
	approval["approved_count"] = approvedCount
	approval["required_approval_count"] = requiredApprovalCount(approval["required_approval_count"])
	delete(approval, "request_payload")
	item := s.dispatchApprovalNotification(r.Context(), approval, "reminder")
	delete(item, "request_payload")
	item["approved_count"] = approvedCount
	item["required_approval_count"] = approval["required_approval_count"]
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createOperationApprovalDelegation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToEmail string `json:"to_email"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ToEmail = strings.ToLower(strings.TrimSpace(req.ToEmail))
	if req.ToEmail == "" {
		writeError(w, http.StatusBadRequest, "to_email is required")
		return
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		writeError(w, http.StatusInternalServerError, "approval store is not configured")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start delegation transaction")
		return
	}
	defer tx.Rollback()
	expired, err := s.expireOperationApprovalByID(r.Context(), tx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	}
	if expired != nil {
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "could not commit expired approval")
			return
		}
		s.dispatchApprovalNotification(r.Context(), expired, "expired")
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	approval, err := queryOne(r.Context(), tx, "SELECT * FROM operation_approvals WHERE id=$1 FOR UPDATE", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	if fmt.Sprint(approval["status"]) != "pending" {
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	if !s.canDecideOperationApproval(r.Context(), currentUser(r), approval) {
		writeError(w, http.StatusForbidden, "approval delegation requires one of the configured approver roles")
		return
	}
	target, err := queryOne(r.Context(), tx, "SELECT id, email, role FROM users WHERE lower(email)=lower($1)", req.ToEmail)
	if err != nil {
		writeError(w, http.StatusNotFound, "delegate user not found")
		return
	}
	if fmt.Sprint(target["id"]) == currentUser(r).ID {
		writeError(w, http.StatusBadRequest, "cannot delegate approval to yourself")
		return
	}
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO operation_approval_delegations(operation_approval_id, from_user_id, to_user_id, reason)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (operation_approval_id, to_user_id) DO UPDATE
		SET from_user_id=EXCLUDED.from_user_id,
			reason=EXCLUDED.reason,
			revoked_at=NULL,
			updated_at=now()
		RETURNING *`, chi.URLParam(r, "id"), currentUser(r).ID, target["id"], strings.TrimSpace(req.Reason))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create approval delegation")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval delegation")
		return
	}
	item["to_user_email"] = target["email"]
	item["from_user_email"] = currentUser(r).Email
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) revokeOperationApprovalDelegation(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.store == nil || s.store.DB == nil {
		writeError(w, http.StatusInternalServerError, "approval store is not configured")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start delegation transaction")
		return
	}
	defer tx.Rollback()
	approval, err := queryOne(r.Context(), tx, "SELECT * FROM operation_approvals WHERE id=$1 FOR UPDATE", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	delegation, err := queryOne(r.Context(), tx, `
		SELECT *
		FROM operation_approval_delegations
		WHERE id=$1 AND operation_approval_id=$2
		FOR UPDATE`, chi.URLParam(r, "delegationID"), chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if cleanOptionalText(fmt.Sprint(delegation["revoked_at"])) != "" {
		writeError(w, http.StatusConflict, "approval delegation is already revoked")
		return
	}
	if !s.canRevokeOperationApprovalDelegation(r.Context(), currentUser(r), approval, delegation) {
		writeError(w, http.StatusForbidden, "approval delegation revoke requires the delegator, an active approver, or an operator role")
		return
	}
	item, err := queryOne(r.Context(), tx, `
		UPDATE operation_approval_delegations
		SET revoked_at=COALESCE(revoked_at, now()),
			updated_at=now()
		WHERE id=$1 AND operation_approval_id=$2
		RETURNING *`, chi.URLParam(r, "delegationID"), chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, item, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval delegation revoke")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) canRevokeOperationApprovalDelegation(ctx context.Context, user *User, approval, delegation map[string]any) bool {
	if user == nil {
		return false
	}
	role := strings.ToLower(strings.TrimSpace(user.Role))
	if role == "admin" || role == "owner" {
		return true
	}
	if cleanOptionalID(fmt.Sprint(delegation["from_user_id"])) == user.ID {
		return true
	}
	return canDecideOperationApproval(user, approval)
}

func (s *Server) canDecideOperationApproval(ctx context.Context, user *User, approval map[string]any) bool {
	if canDecideOperationApproval(user, approval) {
		return true
	}
	if user == nil || s == nil || s.store == nil || s.store.DB == nil {
		return false
	}
	var exists bool
	err := s.store.DB.GetContext(ctx, &exists, `
		SELECT EXISTS(
			SELECT 1 FROM operation_approval_delegations
			WHERE operation_approval_id=$1
				AND to_user_id=$2
				AND revoked_at IS NULL
		)`, approval["id"], user.ID)
	return err == nil && exists
}

func canDecideOperationApproval(user *User, approval map[string]any) bool {
	if user == nil {
		return false
	}
	userRole := strings.ToLower(strings.TrimSpace(user.Role))
	roles := approvalRolesFromAny(approval["required_approver_roles"])
	if len(roles) == 0 {
		roles = []string{"admin", "owner"}
	}
	for _, role := range roles {
		if userRole == strings.ToLower(strings.TrimSpace(role)) {
			return true
		}
	}
	return false
}

func upsertOperationApprovalDecision(ctx context.Context, tx *sqlx.Tx, approvalID, userID, decision, reason string) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("approval decision requires a user")
	}
	if decision != "approved" && decision != "rejected" {
		return fmt.Errorf("approval decision is invalid")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO operation_approval_decisions(operation_approval_id, user_id, decision, reason)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (operation_approval_id, user_id) DO UPDATE
		SET decision=EXCLUDED.decision,
			reason=EXCLUDED.reason,
			decided_at=now(),
			updated_at=now()`,
		approvalID,
		userID,
		decision,
		reason,
	)
	return err
}

func operationApprovalApprovedCount(ctx context.Context, db sqlx.ExtContext, approvalID string) (int, error) {
	item, err := queryOne(ctx, db, `
		SELECT count(*)::int AS approved_count
		FROM operation_approval_decisions
		WHERE operation_approval_id=$1 AND decision='approved'`, approvalID)
	if err != nil {
		return 0, err
	}
	return intFromAny(item["approved_count"], 0), nil
}

func requiredApprovalCount(value any) int {
	count := intFromAny(value, 1)
	if count < 1 {
		return 1
	}
	return count
}

func (s *Server) requireProjectPolicyOrApproval(w http.ResponseWriter, r *http.Request, resource PolicyResource, action, title string, payload map[string]any) bool {
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return false
	}
	return s.requirePolicyOrApproval(w, r, resource, action, title, payload)
}

func (s *Server) requirePolicyOrApproval(w http.ResponseWriter, r *http.Request, resource PolicyResource, action, title string, payload map[string]any) bool {
	decision := NewPolicyChecker().Check(currentUser(r), resource, action)
	switch decision.Effect {
	case PolicyAllow:
		return true
	case PolicyRequireConfirm:
		approval, err := s.createOperationApproval(r.Context(), resource, action, title, payload, currentUser(r).ID)
		if err != nil {
			if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
				writeError(w, http.StatusConflict, "approval request is already pending")
				return false
			}
			writeError(w, http.StatusInternalServerError, "could not create approval request")
			return false
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"approval": approval, "decision": decision})
		return false
	default:
		writeJSON(w, http.StatusForbidden, decision)
		return false
	}
}

func (s *Server) requireProjectMembershipForPolicy(w http.ResponseWriter, r *http.Request, resource PolicyResource) bool {
	user := currentUser(r)
	if user != nil && resource.ProjectID != "" && resource.ProjectID != "<nil>" && user.Role != "admin" && user.Role != "owner" {
		var exists bool
		err := s.store.DB.GetContext(r.Context(), &exists, `
			SELECT EXISTS(
				SELECT 1 FROM project_members
				WHERE project_id=$1 AND user_id=$2
			)`, resource.ProjectID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not check project membership")
			return false
		}
		if !exists {
			writeJSON(w, http.StatusForbidden, PolicyDecision{Effect: PolicyDeny, Reason: "user is not a member of this project"})
			return false
		}
	}
	return true
}

func (s *Server) createOperationApproval(ctx context.Context, resource PolicyResource, action, title string, payload map[string]any, requestedBy string) (map[string]any, error) {
	rule, err := s.operationApprovalRule(ctx, s.store.DB, resource, action)
	if err != nil {
		return nil, err
	}
	payloadJSON, err := jsonParam(payload)
	if err != nil {
		return nil, err
	}
	expiresAfter := rule.ExpiresAfterMinutes
	if expiresAfter <= 0 {
		expiresAfter = 1440
	}
	tx, err := s.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	approval, err := queryOne(ctx, tx, `
		INSERT INTO operation_approvals(
			project_id,
			approval_rule_id,
			resource_type,
			resource_id,
			action,
			title,
			request_payload,
			required_approver_roles,
			required_approval_count,
			notification_channels,
			escalation_after_minutes,
			escalation_channels,
			expires_at,
			requested_by
		)
		VALUES (
			NULLIF($1,'')::uuid,
			NULLIF($2,'')::uuid,
			$3,
			$4,
			$5,
			$6,
			$7::jsonb,
			$8,
			$9,
			$10,
			$11,
			$12,
			now() + ($13::int * interval '1 minute'),
			$14
		)
		RETURNING *`, cleanOptionalID(resource.ProjectID), rule.ID, resource.Type, resource.ID, action, title, payloadJSON, pq.Array(rule.RequiredApproverRoles), rule.RequiredApprovalCount, pq.Array(rule.NotificationChannels), rule.EscalationAfterMinutes, pq.Array(rule.EscalationChannels), expiresAfter, requestedBy)
	if err != nil {
		return nil, err
	}
	if _, err := SyncCanonicalAssetsWith(ctx, tx); err != nil {
		return nil, fmt.Errorf("syncing canonical assets for operation approval create: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.dispatchApprovalNotification(ctx, approval, "pending"), nil
}

type operationApprovalRule struct {
	ID                     string
	RequiredApproverRoles  []string
	RequiredApprovalCount  int
	ExpiresAfterMinutes    int
	NotificationChannels   []string
	EscalationAfterMinutes int
	EscalationChannels     []string
}

func defaultOperationApprovalRule() operationApprovalRule {
	return operationApprovalRule{
		RequiredApproverRoles:  []string{"admin", "owner"},
		RequiredApprovalCount:  1,
		ExpiresAfterMinutes:    1440,
		NotificationChannels:   []string{"ui"},
		EscalationAfterMinutes: 0,
		EscalationChannels:     []string{},
	}
}

func (s *Server) operationApprovalRule(ctx context.Context, db sqlx.ExtContext, resource PolicyResource, action string) (operationApprovalRule, error) {
	rule := defaultOperationApprovalRule()
	row, err := queryOne(ctx, db, `
		SELECT id, required_approver_roles, required_approval_count, expires_after_minutes, notification_channels, escalation_after_minutes, escalation_channels
		FROM operation_approval_rules
		WHERE enabled
			AND action IN ($2, '*')
			AND resource_type IN ($1, '')
		ORDER BY
			priority ASC,
			(CASE WHEN resource_type=$1 THEN 1 ELSE 0 END + CASE WHEN action=$2 THEN 1 ELSE 0 END) DESC,
			CASE WHEN resource_type=$1 THEN 0 ELSE 1 END,
			CASE WHEN action=$2 THEN 0 ELSE 1 END,
			updated_at DESC
		LIMIT 1`, resource.Type, action)
	if errors.Is(err, ErrNotFound) {
		return rule, nil
	}
	if err != nil {
		return rule, err
	}
	rule.ID = cleanOptionalID(fmt.Sprint(row["id"]))
	if roles := approvalRolesFromAny(row["required_approver_roles"]); len(roles) > 0 {
		rule.RequiredApproverRoles = roles
	}
	rule.RequiredApprovalCount = requiredApprovalCount(row["required_approval_count"])
	rule.ExpiresAfterMinutes = intFromAny(row["expires_after_minutes"], rule.ExpiresAfterMinutes)
	if channels := approvalRolesFromAny(row["notification_channels"]); len(channels) > 0 {
		rule.NotificationChannels = channels
	}
	rule.EscalationAfterMinutes = intFromAny(row["escalation_after_minutes"], 0)
	if rule.EscalationAfterMinutes < 0 {
		rule.EscalationAfterMinutes = 0
	}
	rule.EscalationChannels = approvalRolesFromAny(row["escalation_channels"])
	return rule, nil
}

func (s *Server) expirePendingOperationApprovals(ctx context.Context, db sqlx.ExtContext) error {
	items, err := queryMaps(ctx, db, approvalExpirySQL())
	if err != nil {
		return err
	}
	if len(items) > 0 {
		if _, err := SyncCanonicalAssetsWith(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for expired operation approvals: %w", err)
		}
	}
	for _, item := range items {
		s.dispatchApprovalNotification(ctx, item, "expired")
	}
	return nil
}

func approvalExpirySQL() string {
	return `
		UPDATE operation_approvals
		SET status='expired',
			expired_at=now(),
			decision_reason=COALESCE(NULLIF(decision_reason, ''), 'approval expired'),
			updated_at=now()
		WHERE status='pending'
			AND expires_at IS NOT NULL
			AND expires_at <= now()
		RETURNING *`
}

func (s *Server) expireOperationApprovalByID(ctx context.Context, db sqlx.ExtContext, approvalID string) (map[string]any, error) {
	item, err := queryOne(ctx, db, `
		UPDATE operation_approvals
		SET status='expired',
			expired_at=now(),
			decision_reason=COALESCE(NULLIF(decision_reason, ''), 'approval expired'),
			updated_at=now()
		WHERE id=$1
			AND status='pending'
			AND expires_at IS NOT NULL
			AND expires_at <= now()
		RETURNING *`, approvalID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return item, err
}

func (s *Server) dispatchApprovalNotification(ctx context.Context, approval map[string]any, event string) map[string]any {
	if approval == nil {
		return nil
	}
	status, lastError := s.approvalNotificationStatus(ctx, approval, event)
	updated, err := queryOne(ctx, s.store.DB, `
		UPDATE operation_approvals
		SET notification_status=$2,
			notification_last_error=$3,
			updated_at=now()
		WHERE id=$1
		RETURNING *`, approval["id"], status, lastError)
	if err != nil {
		return approval
	}
	if s.store != nil && s.store.DB != nil {
		// Notification dispatch happens after the approval transaction commits, so this
		// refresh is intentionally best-effort; the next canonical sync will repair it.
		if _, syncErr := SyncCanonicalAssetsWith(ctx, s.store.DB); syncErr != nil {
			s.log.Debug("could not sync canonical assets after approval notification", "error", syncErr)
		}
	}
	return updated
}

func (s *Server) approvalNotificationStatus(ctx context.Context, approval map[string]any, event string) (string, string) {
	if strings.TrimSpace(s.cfg.ApprovalWebhookURL) == "" {
		return "delivered", ""
	}
	if err := s.postApprovalWebhook(ctx, approval, event); err != nil {
		return "failed", truncateText(err.Error(), 500)
	}
	return "delivered", ""
}

func (s *Server) postApprovalWebhook(ctx context.Context, approval map[string]any, event string) error {
	endpoint := strings.TrimSpace(s.cfg.ApprovalWebhookURL)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parsing approval webhook url: %w", err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("approval webhook url must include a host")
	}
	if !validPublicHTTPURL(ctx, endpoint) {
		return fmt.Errorf("approval webhook url must be a public http or https URL")
	}
	payload := map[string]any{
		"event": event,
		"approval": map[string]any{
			"id":                       approval["id"],
			"project_id":               approval["project_id"],
			"operation_run_id":         approval["operation_run_id"],
			"resource_type":            approval["resource_type"],
			"resource_id":              approval["resource_id"],
			"action":                   approval["action"],
			"title":                    approval["title"],
			"status":                   approval["status"],
			"required_approver_roles":  approval["required_approver_roles"],
			"required_approval_count":  approval["required_approval_count"],
			"approved_count":           approval["approved_count"],
			"rejected_count":           approval["rejected_count"],
			"escalation_after_minutes": approval["escalation_after_minutes"],
			"escalation_channels":      approval["escalation_channels"],
			"last_escalated_at":        approval["last_escalated_at"],
			"escalation_count":         approval["escalation_count"],
			"requested_by":             approval["requested_by"],
			"decided_by":               approval["decided_by"],
			"expires_at":               approval["expires_at"],
			"expired_at":               approval["expired_at"],
			"created_at":               approval["created_at"],
			"updated_at":               approval["updated_at"],
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling approval webhook payload: %w", err)
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(notifyCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating approval webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(s.cfg.ApprovalWebhookToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := approvalWebhookHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting approval webhook: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("approval webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func approvalRolesFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return cleanStringList(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return cleanStringList(out)
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || text == "<nil>" {
			return nil
		}
		if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
			return parsePostgresTextArray(text)
		}
		var parsed []string
		if json.Unmarshal([]byte(text), &parsed) == nil {
			return cleanStringList(parsed)
		}
		return cleanStringList([]string{text})
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" || text == "<nil>" {
			return nil
		}
		return cleanStringList([]string{text})
	}
}

func parsePostgresTextArray(value string) []string {
	text := strings.TrimSpace(value)
	if !strings.HasPrefix(text, "{") || !strings.HasSuffix(text, "}") {
		return cleanStringList([]string{text})
	}
	text = strings.TrimSuffix(strings.TrimPrefix(text, "{"), "}")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.TrimLeadingSpace = true
	reader.LazyQuotes = true
	items, err := reader.Read()
	if err != nil {
		return cleanStringList(strings.Split(text, ","))
	}
	for i, item := range items {
		items[i] = strings.ReplaceAll(item, `\"`, `"`)
	}
	return cleanStringList(items)
}

func cleanStringList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.ToLower(strings.Trim(item, ` "'`))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func isUniqueViolation(err error, constraint string) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return string(pqErr.Code) == "23505" && (constraint == "" || pqErr.Constraint == constraint)
}

func cleanOptionalID(value string) string {
	value = strings.TrimSpace(value)
	if value == "<nil>" {
		return ""
	}
	return value
}

func cleanOptionalText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return ""
	}
	return value
}

func (s *Server) executeApprovedOperation(ctx context.Context, tx *sqlx.Tx, approval map[string]any) (map[string]any, string, error) {
	payload := mapFromAny(approval["request_payload"])
	actorID := cleanOptionalID(fmt.Sprint(approval["requested_by"]))
	if actorID == "" {
		return nil, "", fmt.Errorf("approval has no requester")
	}
	switch stringFromMap(payload, "kind") {
	case "repository_tag":
		var req repositoryTagRequest
		if err := decodePayloadField(payload, "request", &req); err != nil {
			return nil, "", fmt.Errorf("invalid repository tag approval payload")
		}
		runs, err := s.enqueueRepositoryTagRuns(ctx, tx, stringFromMap(payload, "repo_id"), req, actorID)
		if err != nil {
			return nil, "", err
		}
		operationRunID := ""
		if len(runs) > 0 {
			operationRunID = cleanOptionalID(fmt.Sprint(runs[0]["operation_run_id"]))
		}
		return map[string]any{"items": runs}, operationRunID, nil
	case "remote_operation":
		op, err := s.enqueueRemoteOperationRun(ctx, tx, stringFromMap(payload, "remote_id"), stringFromMap(payload, "tool"), mapFromAny(payload["input"]), actorID)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "ssh_command":
		op, run, err := s.enqueueSSHCommandRun(ctx, tx, stringFromMap(payload, "machine_id"), mapFromAny(payload["input"]), actorID)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op, "run": run}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "agent_execute":
		op, err := s.enqueueAgentTaskExecutionTx(ctx, tx, stringFromMap(payload, "agent_task_id"))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "operation_cancel":
		op, err := s.cancelOperationRun(ctx, tx, stringFromMap(payload, "operation_id"))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	default:
		return nil, "", fmt.Errorf("unsupported approval payload")
	}
}

func decodePayloadField(payload map[string]any, key string, target any) error {
	data, err := json.Marshal(payload[key])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	item, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM operation_runs WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireOperationRead(w, r, item) {
		return
	}
	writeQueryOne(w, item, err)
}

func (s *Server) getOperationLogs(w http.ResponseWriter, r *http.Request) {
	op, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM operation_runs WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireOperationRead(w, r, op) {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM operation_logs WHERE operation_run_id=$1 ORDER BY created_at", chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) streamOperationLogs(w http.ResponseWriter, r *http.Request) {
	opID := chi.URLParam(r, "id")
	op, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM operation_runs WHERE id=$1", opID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireOperationRead(w, r, op) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	cursor := operationLogCursorFromRequest(r)
	streamCtx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		done, err := s.writeOperationLogStreamTick(streamCtx, w, opID, &cursor)
		if err != nil {
			s.log.Warn("operation log stream failed", "operation_id", opID, "error", err)
			if !errors.Is(err, errSSEWrite) {
				_ = writeSSE(w, "stream_error", map[string]any{"message": err.Error()})
			}
			flusher.Flush()
			return
		}
		flusher.Flush()
		if done {
			return
		}
		select {
		case <-streamCtx.Done():
			return
		case <-ticker.C:
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

var errSSEWrite = errors.New("sse write failed")

const operationLogStreamBatchLimit = 200

type operationLogCursor struct {
	CreatedAt string
	ID        string
}

func (s *Server) writeOperationLogStreamTick(ctx context.Context, w io.Writer, opID string, cursor *operationLogCursor) (bool, error) {
	items, err := operationLogStreamBatch(ctx, s.store.DB, opID, *cursor)
	if err != nil {
		return false, fmt.Errorf("loading operation logs: %w", err)
	}
	for _, item := range items {
		cursorID := operationLogCursorID(item)
		if err := writeSSEWithID(w, "log", cursorID, item); err != nil {
			return false, fmt.Errorf("%w: %v", errSSEWrite, err)
		}
		cursor.CreatedAt = operationLogCursorTime(item["created_at"])
		cursor.ID = strings.TrimSpace(fmt.Sprint(item["id"]))
	}
	status, err := operationStatus(ctx, s.store.DB, opID)
	if err != nil {
		return false, fmt.Errorf("loading operation status: %w", err)
	}
	if operationLogStreamShouldClose(status, len(items), operationLogStreamBatchLimit) {
		if err := writeSSE(w, "operation_status", map[string]any{"status": status}); err != nil {
			return false, fmt.Errorf("%w: %v", errSSEWrite, err)
		}
		return true, nil
	}
	return false, nil
}

func operationLogCursorFromRequest(r *http.Request) operationLogCursor {
	for _, value := range []string{
		r.Header.Get("Last-Event-ID"),
		r.URL.Query().Get("cursor"),
		r.URL.Query().Get("last_event_id"),
	} {
		if cursor, ok := parseOperationLogCursorID(value); ok {
			return cursor
		}
	}
	return operationLogCursor{}
}

func operationLogCursorID(item map[string]any) string {
	createdAt := operationLogCursorTime(item["created_at"])
	id := strings.TrimSpace(fmt.Sprint(item["id"]))
	if createdAt == "" || createdAt == "<nil>" || id == "" || id == "<nil>" {
		return ""
	}
	return createdAt + "|" + id
}

func parseOperationLogCursorID(value string) (operationLogCursor, bool) {
	value = strings.TrimSpace(value)
	createdAt, id, ok := strings.Cut(value, "|")
	if !ok {
		return operationLogCursor{}, false
	}
	createdAt = strings.TrimSpace(createdAt)
	id = strings.TrimSpace(id)
	if createdAt == "" || createdAt == "<nil>" || id == "" || id == "<nil>" {
		return operationLogCursor{}, false
	}
	return operationLogCursor{CreatedAt: createdAt, ID: id}, true
}

func operationLogCursorTime(value any) string {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case *time.Time:
		if typed != nil {
			return typed.UTC().Format(time.RFC3339Nano)
		}
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func operationLogStreamShouldClose(status string, batchSize, limit int) bool {
	return operationStreamTerminal(status) && batchSize < limit
}

func operationLogStreamBatch(ctx context.Context, db sqlx.ExtContext, opID string, cursor operationLogCursor) ([]map[string]any, error) {
	if cursor.CreatedAt == "" || cursor.ID == "" {
		return queryMaps(ctx, db, `
			SELECT id, operation_run_id, worker_job_id, level, message, fields, created_at
			FROM operation_logs
			WHERE operation_run_id=$1
			ORDER BY created_at, id
			LIMIT $2`, opID, operationLogStreamBatchLimit)
	}
	return queryMaps(ctx, db, `
		SELECT id, operation_run_id, worker_job_id, level, message, fields, created_at
		FROM operation_logs
		WHERE operation_run_id=$1
			AND (created_at > $2::timestamptz OR (created_at = $2::timestamptz AND id::text > $3))
		ORDER BY created_at, id
		LIMIT $4`, opID, cursor.CreatedAt, cursor.ID, operationLogStreamBatchLimit)
}

func operationStatus(ctx context.Context, db sqlx.ExtContext, opID string) (string, error) {
	var status string
	if err := sqlx.GetContext(ctx, db, &status, "SELECT status FROM operation_runs WHERE id=$1", opID); err != nil {
		return "", err
	}
	return status, nil
}

func operationStreamTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func writeSSE(w io.Writer, event string, data any) error {
	return writeSSEWithID(w, event, "", data)
}

func writeSSEWithID(w io.Writer, event, id string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", payload)
	return err
}

func (s *Server) requireOperationRead(w http.ResponseWriter, r *http.Request, op map[string]any) bool {
	projectID := strings.TrimSpace(fmt.Sprint(op["project_id"]))
	if projectID == "" || projectID == "<nil>" {
		return s.requirePolicy(w, r, PolicyResource{Type: "operation", ID: fmt.Sprint(op["id"])}, "read")
	}
	return s.requireProjectPolicy(w, r, PolicyResource{Type: "operation", ID: fmt.Sprint(op["id"]), ProjectID: projectID}, "read")
}

func (s *Server) cancelOperation(w http.ResponseWriter, r *http.Request) {
	opID := chi.URLParam(r, "id")
	op, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM operation_runs WHERE id=$1", opID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(op["project_id"]))
	resource := PolicyResource{Type: "operation", ID: opID, ProjectID: projectID}
	payload := map[string]any{"kind": "operation_cancel", "operation_id": opID}
	if projectID != "" && projectID != "<nil>" {
		if !s.requireProjectPolicyOrApproval(w, r, resource, "operation.cancel", "cancel "+fmt.Sprint(op["title"]), payload) {
			return
		}
	} else if !s.requirePolicyOrApproval(w, r, resource, "operation.cancel", "cancel "+fmt.Sprint(op["title"]), payload) {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start operation cancel transaction")
		return
	}
	defer tx.Rollback()
	item, err := s.cancelOperationRun(r.Context(), tx, opID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation.cancel") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit operation cancel")
		return
	}
	writeQueryOne(w, item, err)
}

func (s *Server) cancelOperationRun(ctx context.Context, db sqlx.ExtContext, operationID string) (map[string]any, error) {
	item, err := queryOne(ctx, db, `
		UPDATE operation_runs SET status='canceled', finished_at=now(), updated_at=now()
		WHERE id=$1
			AND status NOT IN ('completed', 'failed', 'canceled', 'cancelled')
		RETURNING *`, operationID)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE worker_jobs
		SET status='canceled',
			finished_at=now(),
			updated_at=now()
		WHERE operation_run_id=$1
			AND status='queued'`, operationID); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Server) createNodeTestJob(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "worker_node"}, "node.echo") {
		return
	}
	var input map[string]any
	_ = json.NewDecoder(r.Body).Decode(&input)
	if input == nil {
		input = map[string]any{"message": "hello from ASSOPS"}
	}
	op, err := s.enqueueOperation(r.Context(), "", "", "node.echo", "node-worker echo test", input, []string{"echo"}, "local")
	writeCreatedOne(w, op, err)
}

func (s *Server) listAIRuntimes(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM ai_runtimes ORDER BY created_at DESC")
	writeQueryResult(w, items, err)
}

func (s *Server) createAIRuntime(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "ai_runtime"}, "create") {
		return
	}
	var req struct {
		ProjectID   string         `json:"project_id"`
		Name        string         `json:"name"`
		RuntimeType string         `json:"runtime_type"`
		CodexBinary string         `json:"codex_binary"`
		Model       string         `json:"model"`
		Config      map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RuntimeType == "" {
		req.RuntimeType = "codex-cli"
	}
	if req.CodexBinary == "" {
		req.CodexBinary = "codex"
	}
	config, _ := jsonParam(req.Config)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start AI runtime transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO ai_runtimes(project_id, name, runtime_type, codex_binary, model, config)
		VALUES (NULLIF($1,'')::uuid, $2, $3, $4, $5, $6::jsonb)
		RETURNING *`, req.ProjectID, req.Name, req.RuntimeType, req.CodexBinary, req.Model, config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create AI runtime")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "ai_runtime.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit AI runtime")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) verifyAIRuntime(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "ai_runtime", ID: chi.URLParam(r, "id")}, "update") {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start AI runtime verification transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, "UPDATE ai_runtimes SET status='verified', updated_at=now() WHERE id=$1 RETURNING *", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, item, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "ai_runtime.verify") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit AI runtime verification")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createAgentTask(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start agent task transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO agent_tasks(project_id, title, prompt, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING *`, projectID, req.Title, req.Prompt, currentUser(r).ID)
	if err != nil {
		writeCreatedOne(w, item, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit agent task")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listAgentTasks(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT at.*,
			latest_plan.id AS latest_plan_id,
			latest_plan.status AS latest_plan_status,
			latest_plan.created_at AS latest_plan_created_at
		FROM agent_tasks at
		LEFT JOIN LATERAL (
			SELECT id, status, created_at
			FROM agent_plans ap
			WHERE ap.agent_task_id=at.id
			ORDER BY created_at DESC
			LIMIT 1
		) latest_plan ON true
		WHERE at.project_id=$1
		ORDER BY at.created_at DESC
		LIMIT 100`, projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) getAgentTask(w http.ResponseWriter, r *http.Request) {
	task, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM agent_tasks WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: chi.URLParam(r, "id"), ProjectID: projectID}, "read") {
		return
	}
	plans, _ := queryMaps(r.Context(), s.store.DB, "SELECT * FROM agent_plans WHERE agent_task_id=$1 ORDER BY created_at DESC", chi.URLParam(r, "id"))
	task["plans"] = plans
	toolCalls, _ := queryMaps(r.Context(), s.store.DB, `
		SELECT *
		FROM agent_tool_calls
		WHERE agent_task_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT 100`, chi.URLParam(r, "id"))
	task["tool_calls"] = toolCalls
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) listAgentTaskToolCalls(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	task, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM agent_tasks WHERE id=$1", taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT *
		FROM agent_tool_calls
		WHERE agent_task_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT 100`, taskID)
	writeQueryResult(w, items, err)
}

func (s *Server) generatePlan(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	task, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM agent_tasks WHERE id=$1", taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "agent.generate_plan") {
		return
	}
	_, snapshot, err := s.BuildContextFiles(r.Context(), fmt.Sprint(task["project_id"]))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not build agent context")
		return
	}
	content := agentPlanContent(task, snapshot)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start agent plan transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO agent_plans(agent_task_id, content)
		VALUES ($1, $2)
		RETURNING *`, taskID, content)
	if err != nil {
		writeCreatedOne(w, item, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.generate_plan") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit agent plan")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func agentPlanContent(task, snapshot map[string]any) string {
	contextJSON := mapFromAny(snapshot["context_json"])
	project := mapFromAny(contextJSON["project"])
	repos := mapSliceFromAny(contextJSON["repositories"])
	remotes := mapSliceFromAny(contextJSON["remotes"])
	operations := mapSliceFromAny(contextJSON["operations"])
	approvals := mapSliceFromAny(contextJSON["approvals"])
	deploymentTargets := mapSliceFromAny(contextJSON["deployment_targets"])
	rollbackPoints := mapSliceFromAny(contextJSON["rollback_points"])
	sshMachines := mapSliceFromAny(contextJSON["ssh_machines"])
	githubRuns := mapSliceFromAny(contextJSON["github_action_runs"])
	assetGraph := mapFromAny(contextJSON["asset_graph"])
	assets := mapSliceFromAny(assetGraph["assets"])
	assetRelations := mapSliceFromAny(assetGraph["relations"])
	assetStatusSnapshots := mapSliceFromAny(assetGraph["status_snapshots"])
	assetTypeSummary := formatCountMap(countByStringField(assets, "asset_type"))
	if assetTypeSummary == "" {
		assetTypeSummary = "none"
	}
	assetHealthSummary := formatCountMap(countByStringField(assetStatusSnapshots, "health"))
	if assetHealthSummary == "" {
		assetHealthSummary = "none"
	}
	rollbackReadinessSummary := formatCountMap(countByStringField(rollbackPoints, "rollback_readiness"))
	if rollbackReadinessSummary == "" {
		rollbackReadinessSummary = "none"
	}
	rollbackGuardrail := mapFromAny(contextJSON["rollback_guardrail"])
	if len(rollbackGuardrail) == 0 {
		rollbackGuardrail = rollbackGuardrailSummary(rollbackPoints)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent Read-Only Plan\n\n")
	fmt.Fprintf(&b, "Task: %s\n\n", strings.TrimSpace(fmt.Sprint(task["title"])))
	if prompt := strings.TrimSpace(fmt.Sprint(task["prompt"])); prompt != "" && prompt != "<nil>" {
		fmt.Fprintf(&b, "Prompt: %s\n\n", prompt)
	}
	fmt.Fprintf(&b, "## Context Snapshot\n\n")
	fmt.Fprintf(&b, "- Project: %s (`%s`)\n", strings.TrimSpace(fmt.Sprint(project["name"])), strings.TrimSpace(fmt.Sprint(project["slug"])))
	fmt.Fprintf(&b, "- Repositories: %d\n", len(repos))
	fmt.Fprintf(&b, "- Git remotes: %d\n", len(remotes))
	fmt.Fprintf(&b, "- Recent operations: %d\n", len(operations))
	fmt.Fprintf(&b, "- Pending/Recent approvals: %d\n", len(approvals))
	fmt.Fprintf(&b, "- Deployment targets: %d\n", len(deploymentTargets))
	fmt.Fprintf(&b, "- Rollback points: %d\n", len(rollbackPoints))
	fmt.Fprintf(&b, "- Rollback readiness: %s\n", rollbackReadinessSummary)
	fmt.Fprintf(&b, "- Rollback execution: %s (%d previewable, %d executable)\n",
		strings.TrimSpace(fmt.Sprint(rollbackGuardrail["execution_mode"])),
		intFromAny(rollbackGuardrail["previewable_count"], 0),
		intFromAny(rollbackGuardrail["executable_count"], 0),
	)
	fmt.Fprintf(&b, "- SSH machines: %d\n", len(sshMachines))
	fmt.Fprintf(&b, "- GitHub Actions runs: %d\n", len(githubRuns))
	fmt.Fprintf(&b, "- Asset graph assets: %d\n", len(assets))
	fmt.Fprintf(&b, "- Asset graph relations: %d\n", len(assetRelations))
	fmt.Fprintf(&b, "- Asset status snapshots: %d\n", len(assetStatusSnapshots))
	fmt.Fprintf(&b, "- Asset types: %s\n", assetTypeSummary)
	fmt.Fprintf(&b, "- Asset health: %s\n", assetHealthSummary)
	fmt.Fprintf(&b, "- Snapshot: %s\n\n", strings.TrimSpace(fmt.Sprint(snapshot["created_at"])))
	fmt.Fprintf(&b, "## Read-Only Checks\n\n")
	fmt.Fprintf(&b, "1. Review canonical asset graph entries, status snapshots, repositories, remotes, recent operations, deployment records, SSH runs, and approval state.\n")
	fmt.Fprintf(&b, "2. Summarize risks and missing operational evidence without mutating repositories, infrastructure, or databases.\n")
	fmt.Fprintf(&b, "3. If a mutation is needed, create a follow-up operation that goes through approval instead of executing directly.\n\n")
	fmt.Fprintf(&b, "## Allowed Tools\n\n")
	fmt.Fprintf(&b, "- context.generate\n- repo.sync status review\n- github.actions.sync status review\n- argo.apps.sync status review\n- ssh command audit review\n\n")
	fmt.Fprintf(&b, "## Guardrails\n\n")
	fmt.Fprintf(&b, "- No code changes, deployments, SSH execution, repository tags, or rollback actions in this plan.\n")
	if msg := strings.TrimSpace(fmt.Sprint(rollbackGuardrail["message"])); msg != "" && msg != "<nil>" {
		fmt.Fprintf(&b, "- %s\n", msg)
	}
	patchGuardrail := agentPatchWorkflowGuardrail()
	if msg := strings.TrimSpace(fmt.Sprint(patchGuardrail["message"])); msg != "" && msg != "<nil>" {
		fmt.Fprintf(&b, "- %s\n", msg)
	}
	fmt.Fprintf(&b, "- High-risk follow-up actions must use operation approvals.\n")
	return b.String()
}

func rollbackGuardrailSummary(rollbackPoints []map[string]any) map[string]any {
	previewable := 0
	executable := 0
	mode := ""
	for _, row := range rollbackPoints {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["rollback_readiness"])), "previewable") {
			previewable++
		}
		if value, ok := row["rollback_executable"].(bool); ok && value {
			executable++
		}
		if rowMode := strings.TrimSpace(fmt.Sprint(row["rollback_execution_mode"])); rowMode != "" && rowMode != "<nil>" {
			if mode == "" {
				mode = rowMode
			} else if mode != rowMode {
				mode = "mixed"
			}
		}
	}
	if mode == "" {
		mode = "read_only_preview"
	}
	executionEnabled := executable > 0
	message := "Rollback execution is disabled in this first version; rollback points are preview-only evidence."
	if executionEnabled {
		message = "Rollback execution appears enabled for at least one rollback point; require explicit approval and environment review before action."
	}
	return map[string]any{
		"total":             len(rollbackPoints),
		"previewable_count": previewable,
		"executable_count":  executable,
		"execution_enabled": executionEnabled,
		"execution_mode":    mode,
		"message":           message,
	}
}

func countByStringField(rows []map[string]any, field string) map[string]int {
	counts := make(map[string]int)
	for _, row := range rows {
		key := strings.TrimSpace(fmt.Sprint(row[field]))
		if key == "" || key == "<nil>" {
			continue
		}
		counts[key]++
	}
	return counts
}

func sanitizeContextRowsMetadata(rows []map[string]any) {
	for _, row := range rows {
		metadata, ok := row["metadata"].(map[string]any)
		if !ok {
			continue
		}
		// Keep AI/context snapshots explicitly sanitized even if callers change query normalization.
		row["metadata"] = sanitizeMetadata(metadata)
	}
}

func formatCountMap(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func (s *Server) approvePlan(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	task, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM agent_tasks WHERE id=$1", taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "agent.approve_plan") {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start agent plan approval transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE agent_plans SET status='approved', approved_at=now()
		WHERE agent_task_id=$1 AND id=(SELECT id FROM agent_plans WHERE agent_task_id=$1 ORDER BY created_at DESC LIMIT 1)
		RETURNING *`, taskID)
	if err != nil {
		writeQueryOne(w, item, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.approve_plan") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit agent plan approval")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) executePlan(w http.ResponseWriter, r *http.Request) {
	task, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM agent_tasks WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	payload := map[string]any{"kind": "agent_execute", "agent_task_id": chi.URLParam(r, "id")}
	if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "agent_task", ID: chi.URLParam(r, "id"), ProjectID: fmt.Sprint(task["project_id"])}, "agent.execute", "execute agent task "+fmt.Sprint(task["title"]), payload) {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start agent execution transaction")
		return
	}
	defer tx.Rollback()
	op, err := s.enqueueAgentTaskExecutionTx(r.Context(), tx, chi.URLParam(r, "id"))
	if errors.Is(err, errAgentPlanNotApproved) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeCreatedOne(w, op, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.execute") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit agent execution")
		return
	}
	writeJSON(w, http.StatusCreated, op)
}

func (s *Server) enqueueAgentTaskExecutionTx(ctx context.Context, tx *sqlx.Tx, taskID string) (map[string]any, error) {
	task, err := queryOne(ctx, tx, "SELECT * FROM agent_tasks WHERE id=$1 FOR UPDATE", taskID)
	if err != nil {
		return nil, err
	}
	plan, err := latestApprovedAgentPlan(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	op, err := enqueueOperationTx(ctx, tx, fmt.Sprint(task["project_id"]), "", "agent.execute", "execute agent task "+fmt.Sprint(task["title"]), map[string]any{"agent_task_id": task["id"]}, []string{"ai"}, "")
	if err == nil {
		if err = enqueueAgentToolCallAuditTx(ctx, tx, task, plan, op); err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE agent_tasks SET status='queued', updated_at=now() WHERE id=$1", taskID)
	}
	return op, err
}

func requireLatestAgentPlanApproved(ctx context.Context, db sqlx.ExtContext, taskID string) error {
	_, err := latestApprovedAgentPlan(ctx, db, taskID)
	return err
}

func latestApprovedAgentPlan(ctx context.Context, db sqlx.ExtContext, taskID string) (map[string]any, error) {
	plan, err := queryOne(ctx, db, `
		SELECT *
		FROM agent_plans
		WHERE agent_task_id=$1
		ORDER BY created_at DESC
		LIMIT 1`, taskID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errAgentPlanNotApproved
		}
		return nil, err
	}
	if !agentPlanStatusApproved(plan["status"]) {
		return nil, errAgentPlanNotApproved
	}
	return plan, nil
}

func agentPlanStatusApproved(status any) bool {
	return fmt.Sprint(status) == "approved"
}

func enqueueAgentToolCallAuditTx(ctx context.Context, tx *sqlx.Tx, task, plan, op map[string]any) error {
	runtime, err := latestProjectAIRuntime(ctx, tx, strings.TrimSpace(fmt.Sprint(task["project_id"])))
	if err != nil {
		return err
	}
	for _, call := range agentExecutionAuditSteps(task, plan, op, runtime) {
		input, err := jsonParam(call["input"])
		if err != nil {
			return fmt.Errorf("marshaling agent tool call input: %w", err)
		}
		output, err := jsonParam(call["output"])
		if err != nil {
			return fmt.Errorf("marshaling agent tool call output: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agent_tool_calls(agent_task_id, operation_run_id, project_id, tool_name, input, output, status)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, 'queued')`,
			task["id"], op["id"], task["project_id"], call["tool_name"], input, output)
		if err != nil {
			return fmt.Errorf("inserting agent tool call audit: %w", err)
		}
	}
	return nil
}

func latestProjectAIRuntime(ctx context.Context, db sqlx.ExtContext, projectID string) (map[string]any, error) {
	runtime, err := queryOne(ctx, db, `
		SELECT id, project_id, name, runtime_type, codex_binary, model, status, updated_at
		FROM ai_runtimes
		WHERE project_id=$1 OR project_id IS NULL
		ORDER BY
			CASE WHEN project_id=$1 THEN 0 ELSE 1 END,
			CASE WHEN status='verified' THEN 0 ELSE 1 END,
			updated_at DESC
		LIMIT 1`, projectID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading AI runtime for agent execution audit: %w", err)
	}
	return runtime, nil
}

func agentExecutionAuditSteps(task, plan, op, runtime map[string]any) []map[string]any {
	taskID := strings.TrimSpace(fmt.Sprint(task["id"]))
	planID := strings.TrimSpace(fmt.Sprint(plan["id"]))
	opID := strings.TrimSpace(fmt.Sprint(op["id"]))
	planContent := strings.TrimSpace(fmt.Sprint(plan["content"]))
	if planContent == "<nil>" {
		planContent = ""
	}
	return []map[string]any{
		{
			"tool_name": "context.generate",
			"input": map[string]any{
				"agent_task_id":    taskID,
				"operation_run_id": opID,
				"mode":             "read_only_snapshot",
			},
			"output": map[string]any{
				"message": "context snapshot is read by the approved plan; no repository mutation is performed",
			},
		},
		{
			"tool_name": "plan.review",
			"input": map[string]any{
				"agent_task_id": taskID,
				"agent_plan_id": planID,
				"plan_bytes":    len(planContent),
			},
			"output": map[string]any{
				"message": "approved plan accepted for execution audit",
			},
		},
		agentRuntimeCheckStep(taskID, opID, runtime),
		{
			"tool_name": "patch.prepare",
			"input": map[string]any{
				"agent_task_id": taskID,
				"agent_plan_id": planID,
				"mode":          "simulation_only",
			},
			"output": map[string]any{
				"message":                  "first-version agent execution records intent only; code mutation remains disabled",
				"patch_workflow_guardrail": agentPatchWorkflowGuardrail(),
			},
		},
	}
}

func agentPatchWorkflowGuardrail() map[string]any {
	return map[string]any{
		"execution_mode":              "simulation_only",
		"mutation_enabled":            false,
		"repository_mutation_allowed": false,
		"codex_cli_invocation":        "disabled",
		"pull_request_creation":       "disabled",
		"required_approvals": []string{
			"agent.execute",
			"future.patch.apply",
		},
		"blocked_reasons": []string{
			"codex CLI process execution is not enabled in the first version",
			"repository mutation requires a future approval-gated patch apply operation",
			"pull request creation is not wired to a provider account workflow yet",
		},
		"next_step": "Keep execution audit-only until Codex CLI runs, patch application, and PR creation are individually approval-gated.",
		"message":   "Agent patch workflow is audit-only: Codex CLI, repository mutation, and pull request creation are disabled.",
	}
}

func agentRuntimeCheckStep(taskID, opID string, runtime map[string]any) map[string]any {
	input := map[string]any{
		"agent_task_id":    taskID,
		"operation_run_id": opID,
		"mode":             "read_only_runtime_check",
	}
	output := map[string]any{
		"mutation_enabled": false,
	}
	if len(runtime) == 0 {
		output["readiness"] = "missing"
		output["message"] = "no AI runtime is configured for this project or globally; execution remains audit-only"
	} else {
		status := strings.TrimSpace(fmt.Sprint(runtime["status"]))
		if status == "" || status == "<nil>" {
			status = "unknown"
		}
		input["runtime_id"] = strings.TrimSpace(fmt.Sprint(runtime["id"]))
		input["runtime_name"] = strings.TrimSpace(fmt.Sprint(runtime["name"]))
		input["runtime_type"] = strings.TrimSpace(fmt.Sprint(runtime["runtime_type"]))
		input["codex_binary"] = strings.TrimSpace(fmt.Sprint(runtime["codex_binary"]))
		input["model"] = strings.TrimSpace(fmt.Sprint(runtime["model"]))
		input["status"] = status
		output["readiness"] = status
		output["message"] = "AI runtime metadata checked for audit; repository mutation remains disabled"
	}
	return map[string]any{
		"tool_name": "runtime.check",
		"input":     input,
		"output":    output,
	}
}

func (s *Server) createArgoConnection(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name      string         `json:"name"`
		ServerURL string         `json:"server_url"`
		AuthType  string         `json:"auth_type"`
		Config    map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validPublicHTTPURL(r.Context(), req.ServerURL) {
		writeError(w, http.StatusBadRequest, "server_url must be a public http or https URL")
		return
	}
	if (boolConfig(req.Config, "insecure_skip_verify") || boolConfig(req.Config, "use_env_token")) && !canUseSensitiveArgoConfig(currentUser(r)) {
		writeError(w, http.StatusForbidden, "sensitive Argo connection config requires an owner role")
		return
	}
	if req.AuthType == "" {
		req.AuthType = "token"
	}
	config, err := jsonParam(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "config must be valid JSON")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start argo connection transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO argo_connections(project_id, name, server_url, auth_type, config)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		RETURNING *`, projectID, req.Name, req.ServerURL, req.AuthType, config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "argo_connection.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit argo connection")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listArgoConnections(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM argo_connections WHERE project_id=$1 ORDER BY created_at DESC", projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) syncArgoApps(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start Argo sync transaction")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "SELECT pg_advisory_xact_lock(hashtext($1))", "argo.apps.sync:"+connectionID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not lock Argo sync")
		return
	}
	connection, err := queryOne(r.Context(), tx, "SELECT * FROM argo_connections WHERE id=$1 FOR SHARE", connectionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := fmt.Sprint(connection["project_id"])
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ID: connectionID, ProjectID: projectID}, "argo.apps.sync") {
		return
	}
	existing, err := queryMaps(r.Context(), tx, `
		SELECT id FROM operation_runs
		WHERE operation_type='argo.apps.sync'
			AND status IN ('queued', 'running')
			AND input->>'argo_connection_id'=$1
		LIMIT 1`, connectionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not check existing Argo sync")
		return
	}
	if len(existing) > 0 {
		writeError(w, http.StatusConflict, "Argo app sync is already queued or running")
		return
	}
	title := "sync Argo apps"
	if name := strings.TrimSpace(fmt.Sprint(connection["name"])); name != "" && name != "<nil>" {
		title += " " + name
	}
	op, err := enqueueOperationTx(
		r.Context(),
		tx,
		projectID,
		"",
		"argo.apps.sync",
		title,
		map[string]any{"argo_connection_id": connectionID},
		[]string{"argo"},
		"control-worker",
	)
	if err != nil {
		writeCreatedOne(w, op, err)
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "argo_apps_sync.enqueue") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit Argo sync")
		return
	}
	writeJSON(w, http.StatusCreated, op)
}

func (s *Server) listArgoApps(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_app", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT aa.*, dt.name AS deployment_target_name, dt.environment, dt.cluster_name
		FROM argo_apps aa
		LEFT JOIN deployment_targets dt ON dt.id=aa.deployment_target_id
		WHERE aa.project_id=$1
		ORDER BY aa.created_at DESC
		LIMIT 500`, projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) listDeploymentTargets(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_target", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT dt.*,
			ac.name AS argo_connection_name,
			COUNT(aa.id) AS argo_app_count
		FROM deployment_targets dt
		LEFT JOIN argo_connections ac ON ac.id=dt.argo_connection_id
		LEFT JOIN argo_apps aa ON aa.deployment_target_id=dt.id
		WHERE dt.project_id=$1
		GROUP BY dt.id, ac.name
		ORDER BY dt.environment, dt.namespace, dt.created_at DESC
		LIMIT 500`, projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) listDeploymentRecords(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_record", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT dr.*, dt.name AS deployment_target_name, aa.name AS argo_app_name
		FROM deployment_records dr
		LEFT JOIN deployment_targets dt ON dt.id=dr.deployment_target_id
		LEFT JOIN argo_apps aa ON aa.id=dr.argo_app_id
		WHERE dr.project_id=$1
		ORDER BY dr.observed_at DESC
		LIMIT 500`, projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) listRollbackPoints(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "rollback_point", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, rollbackPointReadinessSQL(500), projectID)
	writeQueryResult(w, items, err)
}

func rollbackPointReadinessSQL(limit int) string {
	if limit <= 0 {
		limit = 20
	}
	return fmt.Sprintf(`
		SELECT rp.*,
			dt.name AS deployment_target_name,
			dt.namespace AS deployment_namespace,
			dt.cluster_name AS deployment_cluster_name,
			dr.status AS deployment_status,
			false AS rollback_executable,
			'read_only_preview' AS rollback_execution_mode,
			CASE
				WHEN COALESCE(rp.status, '')='expired' THEN 'blocked'
				WHEN COALESCE(rp.revision, '')='' THEN 'incomplete'
				WHEN COALESCE(rp.status, '')='available' THEN 'previewable'
				ELSE 'blocked'
			END AS rollback_readiness,
			CASE
				WHEN COALESCE(rp.status, '')='expired' THEN 'rollback point is expired'
				WHEN COALESCE(rp.revision, '')='' THEN 'rollback point has no captured revision'
				WHEN COALESCE(rp.status, '')='available' THEN 'rollback point has revision metadata; execution remains disabled in this first version'
				ELSE 'rollback point is not available'
			END AS rollback_readiness_reason
		FROM rollback_points rp
		LEFT JOIN deployment_targets dt ON dt.id=rp.deployment_target_id
		LEFT JOIN deployment_records dr ON dr.id=rp.deployment_record_id
		WHERE rp.project_id=$1
		ORDER BY rp.captured_at DESC
		LIMIT %d`, limit)
}

func validPublicHTTPURL(ctx context.Context, value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.User != nil {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPublicIP(ip)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return false
		}
	}
	return true
}

func boolConfig(config map[string]any, key string) bool {
	value, ok := config[key].(bool)
	return ok && value
}

func canUseSensitiveArgoConfig(user *User) bool {
	return user != nil && (user.Role == "admin" || user.Role == "owner")
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsUnspecified()
}

func (s *Server) requireProjectPolicy(w http.ResponseWriter, r *http.Request, resource PolicyResource, action string) bool {
	user := currentUser(r)
	if user != nil && resource.ProjectID != "" && user.Role != "admin" && user.Role != "owner" {
		var exists bool
		err := s.store.DB.GetContext(r.Context(), &exists, `
			SELECT EXISTS(
				SELECT 1 FROM project_members
				WHERE project_id=$1 AND user_id=$2
			)`, resource.ProjectID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not check project membership")
			return false
		}
		if !exists {
			writeJSON(w, http.StatusForbidden, PolicyDecision{Effect: PolicyDeny, Reason: "user is not a member of this project"})
			return false
		}
	}
	if !s.requirePolicy(w, r, resource, action) {
		return false
	}
	return true
}

func (s *Server) createSSHMachine(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name     string         `json:"name"`
		Host     string         `json:"host"`
		Port     int            `json:"port"`
		Username string         `json:"username"`
		AuthType string         `json:"auth_type"`
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.AuthType == "" {
		req.AuthType = "key"
	}
	metadata, _ := jsonParam(req.Metadata)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start ssh machine transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		INSERT INTO ssh_machines(project_id, name, host, port, username, auth_type, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		RETURNING *`, projectID, req.Name, req.Host, req.Port, req.Username, req.AuthType, metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "ssh_machine.create") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit ssh machine")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listSSHMachines(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM ssh_machines WHERE project_id=$1 ORDER BY created_at DESC", projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) createSSHCommand(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	machine, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM ssh_machines WHERE id=$1", machineID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	var req struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	if len(req.Command) > 4096 {
		writeError(w, http.StatusBadRequest, "command is too long")
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 60
	}
	if req.TimeoutSeconds > 300 {
		writeError(w, http.StatusBadRequest, "timeout_seconds must be <= 300")
		return
	}
	input := map[string]any{
		"ssh_machine_id":  machineID,
		"command":         req.Command,
		"timeout_seconds": req.TimeoutSeconds,
	}
	payload := map[string]any{"kind": "ssh_command", "machine_id": machineID, "input": input}
	if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: fmt.Sprint(machine["project_id"])}, "ssh.exec", "ssh "+fmt.Sprint(machine["name"]), payload) {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start SSH command transaction")
		return
	}
	defer tx.Rollback()
	op, run, err := s.enqueueSSHCommandRun(r.Context(), tx, machineID, input, currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "ssh_command.enqueue") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit SSH command")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operation": op, "run": run})
}

func (s *Server) enqueueSSHCommandRun(ctx context.Context, tx *sqlx.Tx, machineID string, input map[string]any, actorID string) (map[string]any, map[string]any, error) {
	machine, err := queryOne(ctx, tx, "SELECT * FROM ssh_machines WHERE id=$1 FOR SHARE", machineID)
	if err != nil {
		return nil, nil, err
	}
	command := strings.TrimSpace(stringFromMap(input, "command"))
	if command == "" {
		return nil, nil, fmt.Errorf("command is required")
	}
	op, err := enqueueOperationTx(
		ctx,
		tx,
		fmt.Sprint(machine["project_id"]),
		"",
		"ssh.exec",
		"ssh "+fmt.Sprint(machine["name"]),
		input,
		[]string{"ssh"},
		"control-worker",
	)
	if err != nil {
		return nil, nil, fmt.Errorf("could not enqueue SSH command")
	}
	run, err := queryOne(ctx, tx, `
		INSERT INTO ssh_command_runs(
			operation_run_id, ssh_machine_id, project_id, command, actor_user_id, status
		)
		VALUES ($1, $2, $3, $4, $5, 'queued')
		RETURNING *`,
		op["id"],
		machineID,
		machine["project_id"],
		command,
		actorID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("could not create SSH command run")
	}
	return op, run, nil
}

func (s *Server) listSSHCommandRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "ssh_command_run"}, "read") {
		return
	}
	projectID := r.URL.Query().Get("project_id")
	machineID := r.URL.Query().Get("machine_id")
	switch {
	case machineID != "":
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT * FROM ssh_command_runs
			WHERE ssh_machine_id=$1
			ORDER BY created_at DESC
			LIMIT 100`, machineID)
		writeQueryResult(w, items, err)
	case projectID != "":
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT * FROM ssh_command_runs
			WHERE project_id=$1
			ORDER BY created_at DESC
			LIMIT 100`, projectID)
		writeQueryResult(w, items, err)
	default:
		writeError(w, http.StatusBadRequest, "project_id or machine_id is required")
	}
}

func (s *Server) registerNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string         `json:"name"`
		Kind         string         `json:"kind"`
		Capabilities []string       `json:"capabilities"`
		Metadata     map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "node-" + uuid.NewString()[:8]
	}
	if req.Kind == "" {
		req.Kind = "local"
	}
	if req.Kind == "control-worker" {
		writeError(w, http.StatusBadRequest, "reserved worker node kind")
		return
	}
	metadata, _ := jsonParam(req.Metadata)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start worker node registration transaction")
		return
	}
	defer tx.Rollback()
	node, err := queryOne(r.Context(), tx, `
		INSERT INTO worker_nodes(name, kind, capabilities, metadata)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT(name) DO UPDATE SET kind=EXCLUDED.kind, capabilities=EXCLUDED.capabilities, metadata=EXCLUDED.metadata, status='online', last_heartbeat_at=now(), updated_at=now()
		RETURNING *`, req.Name, req.Kind, pq.Array(req.Capabilities), metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not register node")
		return
	}
	token := newToken()
	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO worker_node_tokens(worker_node_id, token_hash)
		VALUES ($1, $2)`, node["id"], tokenHash(token))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create node token")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "worker_node.register") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit worker node registration")
		return
	}
	node["token"] = token
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) nodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start worker node heartbeat transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE worker_nodes SET status='online', last_heartbeat_at=now(), updated_at=now()
		WHERE id=$1 RETURNING *`, node["id"])
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if _, err := SyncWorkerNodeCanonicalAssetWith(r.Context(), tx, node["id"]); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync worker node canonical asset")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit worker node heartbeat")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) claimJob(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start claim transaction")
		return
	}
	defer tx.Rollback()
	item, err := queryOne(r.Context(), tx, `
		UPDATE worker_jobs
		SET status='running', assigned_worker_node_id=$1, claimed_at=now(), started_at=now(), updated_at=now()
		WHERE id = (
			SELECT id FROM worker_jobs
			WHERE status='queued'
			  AND (preferred_node_kind='' OR preferred_node_kind=(SELECT kind FROM worker_nodes WHERE id=$1))
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING *`, node["id"])
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"job": nil})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not claim job")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE operation_runs SET status='running', started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=$1", item["operation_run_id"]); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update operation status")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "worker_job.claim") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit claimed job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": item})
}

func (s *Server) nodeJobLog(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	var req struct {
		Level   string         `json:"level"`
		Message string         `json:"message"`
		Fields  map[string]any `json:"fields"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Level == "" {
		req.Level = "info"
	}
	fields, _ := jsonParam(req.Fields)
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO operation_logs(operation_run_id, worker_job_id, level, message, fields)
		SELECT operation_run_id, id, $3, $4, $5::jsonb FROM worker_jobs
		WHERE id=$1 AND assigned_worker_node_id=$2
		RETURNING *`, chi.URLParam(r, "id"), node["id"], req.Level, req.Message, fields)
	writeCreatedOne(w, item, err)
}

func (s *Server) nodeJobComplete(w http.ResponseWriter, r *http.Request) {
	s.finishNodeJob(w, r, "completed")
}

func (s *Server) nodeJobFail(w http.ResponseWriter, r *http.Request) {
	s.finishNodeJob(w, r, "failed")
}

func (s *Server) finishNodeJob(w http.ResponseWriter, r *http.Request, status string) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	var req struct {
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	result, _ := jsonParam(req.Result)
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start job finish transaction")
		return
	}
	defer tx.Rollback()
	job, err := queryOne(r.Context(), tx, `
		UPDATE worker_jobs SET status=$3, result=$4::jsonb, error=$5, finished_at=now(), updated_at=now()
		WHERE id=$1 AND assigned_worker_node_id=$2
		RETURNING *`, chi.URLParam(r, "id"), node["id"], status, result, req.Error)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	opStatus := "completed"
	if status == "failed" {
		opStatus = "failed"
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE operation_runs SET status=$2, result=$3::jsonb, error=$4, finished_at=now(), updated_at=now()
		WHERE id=$1`, job["operation_run_id"], opStatus, result, req.Error); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update operation status")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "worker_job.finish") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit job finish")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) authenticateNode(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		var req struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		token = req.Token
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing node token")
		return nil, false
	}
	node, err := queryOne(r.Context(), s.store.DB, `
		SELECT wn.* FROM worker_nodes wn
		JOIN worker_node_tokens wnt ON wnt.worker_node_id=wn.id
		WHERE wnt.token_hash=$1`, tokenHash(token))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid node token")
		return nil, false
	}
	_, _ = s.store.DB.ExecContext(r.Context(), "UPDATE worker_node_tokens SET last_used_at=now() WHERE token_hash=$1", tokenHash(token))
	return node, true
}

func (s *Server) generateContext(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "context.generate") {
		return
	}
	files, snapshot, err := s.BuildContextFiles(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "snapshot": snapshot})
}

func (s *Server) BuildContextFiles(ctx context.Context, projectID string) (map[string]string, map[string]any, error) {
	project, err := queryOne(ctx, s.store.DB, "SELECT * FROM projects WHERE id=$1", projectID)
	if err != nil {
		return nil, nil, err
	}
	repos, err := queryMaps(ctx, s.store.DB, "SELECT * FROM project_git_repositories WHERE project_id=$1 ORDER BY name", projectID)
	if err != nil {
		return nil, nil, err
	}
	remotes, err := queryMaps(ctx, s.store.DB, `
		SELECT gr.* FROM git_remotes gr
		JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id
		WHERE pgr.project_id=$1 ORDER BY gr.name`, projectID)
	if err != nil {
		return nil, nil, err
	}
	operations, err := queryMaps(ctx, s.store.DB, `
		SELECT id, operation_type, status, title, error, created_at, updated_at
		FROM operation_runs
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT 20`, projectID)
	if err != nil {
		return nil, nil, err
	}
	approvals, err := queryMaps(ctx, s.store.DB, `
		SELECT id, resource_type, resource_id, action, title, status, expires_at, created_at, updated_at
		FROM operation_approvals
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT 20`, projectID)
	if err != nil {
		return nil, nil, err
	}
	deploymentTargets, err := queryMaps(ctx, s.store.DB, `
		SELECT id, name, environment, cluster_name, namespace, source, status, updated_at
		FROM deployment_targets
		WHERE project_id=$1
		ORDER BY updated_at DESC
		LIMIT 20`, projectID)
	if err != nil {
		return nil, nil, err
	}
	deploymentRecords, err := queryMaps(ctx, s.store.DB, `
		SELECT id, deployment_target_id, name, environment, namespace, cluster_name, source, status, revision, observed_at
		FROM deployment_records
		WHERE project_id=$1
		ORDER BY observed_at DESC
		LIMIT 20`, projectID)
	if err != nil {
		return nil, nil, err
	}
	rollbackPoints, err := queryMaps(ctx, s.store.DB, rollbackPointReadinessSQL(20), projectID)
	if err != nil {
		return nil, nil, err
	}
	sanitizeContextRowsMetadata(rollbackPoints)
	sshMachines, err := queryMaps(ctx, s.store.DB, `
		SELECT id, name, host, port, username, auth_type, created_at, updated_at
		FROM ssh_machines
		WHERE project_id=$1
		ORDER BY updated_at DESC
		LIMIT 20`, projectID)
	if err != nil {
		return nil, nil, err
	}
	githubRuns, err := queryMaps(ctx, s.store.DB, `
		SELECT gar.id, gar.git_remote_id, gar.workflow_name, gar.run_id, gar.branch, gar.commit_sha, gar.status, gar.conclusion, gar.html_url, gar.started_at, gar.updated_at, gar.synced_at
		FROM github_action_runs gar
		JOIN git_remotes gr ON gr.id=gar.git_remote_id
		JOIN project_git_repositories pgr ON pgr.id=gr.project_git_repository_id
		WHERE pgr.project_id=$1
		ORDER BY gar.created_at DESC
		LIMIT 20`, projectID)
	if err != nil {
		return nil, nil, err
	}
	assets, err := queryMaps(ctx, s.store.DB, `
		SELECT id, project_id, asset_type, source_table, source_id, name, display_name, source, external_id, status, risk_level, metadata, updated_at
		FROM assets
		WHERE project_id=$1
		ORDER BY asset_type, name
		LIMIT 200`, projectID)
	if err != nil {
		return nil, nil, err
	}
	assetRelations, err := queryMaps(ctx, s.store.DB, `
		SELECT
			ar.id,
			ar.project_id,
			ar.relation_type,
			ar.metadata,
			ar.created_at,
			from_asset.id AS from_asset_id,
			from_asset.asset_type AS from_asset_type,
			from_asset.name AS from_asset_name,
			from_asset.source_table AS from_source_table,
			from_asset.source_id AS from_source_id,
			to_asset.id AS to_asset_id,
			to_asset.asset_type AS to_asset_type,
			to_asset.name AS to_asset_name,
			to_asset.source_table AS to_source_table,
			to_asset.source_id AS to_source_id
		FROM asset_relations ar
		JOIN assets from_asset ON from_asset.id=ar.from_asset_id
		JOIN assets to_asset ON to_asset.id=ar.to_asset_id
		WHERE ar.project_id=$1
			AND from_asset.project_id=$1
			AND to_asset.project_id=$1
		ORDER BY ar.relation_type, from_asset.name, to_asset.name
		LIMIT 300`, projectID)
	if err != nil {
		return nil, nil, err
	}
	assetStatusSnapshots, err := queryMaps(ctx, s.store.DB, `
		SELECT
			ass.id,
			ass.asset_id,
			a.asset_type,
			a.name AS asset_name,
			ass.status,
			ass.health,
			ass.summary,
			ass.collected_at
		FROM asset_status_snapshots ass
		JOIN assets a ON a.id=ass.asset_id
		WHERE a.project_id=$1
		ORDER BY ass.collected_at DESC, ass.id DESC
		LIMIT 100`, projectID)
	if err != nil {
		return nil, nil, err
	}
	tools := []map[string]any{
		{"name": "repo.sync", "description": "sync repository adapter"},
		{"name": "repo.tag", "description": "tag repository adapter"},
		{"name": "github.actions.sync", "description": "GitHub Actions query adapter"},
		{"name": "ssh.exec", "description": "SSH command adapter"},
	}
	rollbackGuardrail := rollbackGuardrailSummary(rollbackPoints)
	contextJSON := map[string]any{
		"project":            project,
		"repositories":       repos,
		"remotes":            remotes,
		"operations":         operations,
		"approvals":          approvals,
		"deployment_targets": deploymentTargets,
		"deployment_records": deploymentRecords,
		"rollback_points":    rollbackPoints,
		"rollback_guardrail": rollbackGuardrail,
		"ssh_machines":       sshMachines,
		"github_action_runs": githubRuns,
		"asset_graph": map[string]any{
			"assets":               assets,
			"relations":            assetRelations,
			"status_snapshots":     assetStatusSnapshots,
			"asset_type_counts":    countByStringField(assets, "asset_type"),
			"relation_type_counts": countByStringField(assetRelations, "relation_type"),
			"health_counts":        countByStringField(assetStatusSnapshots, "health"),
		},
	}
	manifest := map[string]any{"tools": tools}
	brief := fmt.Sprintf("# ASSOPS Context\n\nProject: %s\n\nRepositories: %d\nRemotes: %d\nRecent operations: %d\nApprovals: %d\nDeployment targets: %d\nRollback points: %d\nRollback execution: %s\nSSH machines: %d\nGitHub Actions runs: %d\nAsset graph assets: %d\nAsset graph relations: %d\nAsset status snapshots: %d\n", project["name"], len(repos), len(remotes), len(operations), len(approvals), len(deploymentTargets), len(rollbackPoints), rollbackGuardrail["execution_mode"], len(sshMachines), len(githubRuns), len(assets), len(assetRelations), len(assetStatusSnapshots))
	base := filepath.Join(s.cfg.ContextDir, projectID)
	if err := os.MkdirAll(base, contextDirMode); err != nil {
		return nil, nil, err
	}
	files := map[string]string{
		"ASSOPS_CONTEXT.md":   filepath.Join(base, "ASSOPS_CONTEXT.md"),
		"assops-context.json": filepath.Join(base, "assops-context.json"),
		"tool-manifest.json":  filepath.Join(base, "tool-manifest.json"),
	}
	if err := os.WriteFile(files["ASSOPS_CONTEXT.md"], []byte(brief), contextFileMode); err != nil {
		return nil, nil, err
	}
	if err := writeJSONFile(files["assops-context.json"], contextJSON); err != nil {
		return nil, nil, err
	}
	if err := writeJSONFile(files["tool-manifest.json"], manifest); err != nil {
		return nil, nil, err
	}
	contextBytes, _ := json.Marshal(contextJSON)
	manifestBytes, _ := json.Marshal(manifest)
	snapshot, err := queryOne(ctx, s.store.DB, `
		INSERT INTO agent_context_snapshots(project_id, summary_markdown, context_json, tool_manifest)
		VALUES ($1, $2, $3::jsonb, $4::jsonb)
		RETURNING *`, projectID, brief, string(contextBytes), string(manifestBytes))
	return files, snapshot, err
}

func writeJSONFile(path string, value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, contextFileMode)
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return false
	}
	return true
}

func writeQueryResult(w http.ResponseWriter, items []map[string]any, err error) {
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func writeQueryOne(w http.ResponseWriter, item map[string]any, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writeCreatedOne(w http.ResponseWriter, item map[string]any, err error) {
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) syncCanonicalAssetsInTransaction(w http.ResponseWriter, r *http.Request, tx *sqlx.Tx, reason string) bool {
	result, err := SyncCanonicalAssetsWith(r.Context(), tx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync canonical assets")
		return false
	}
	if s.log != nil {
		s.log.Debug("canonical assets synced in transaction", "reason", reason, "synced_assets", result.SyncedAssets, "inserted_relations", result.InsertedRelations, "pruned_relations", result.PrunedRelations, "inserted_status_snapshots", result.InsertedStatusSnapshots)
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
