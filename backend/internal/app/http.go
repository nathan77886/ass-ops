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
var errProjectVersionRefreshAlreadyQueued = errors.New("project version refresh is already queued or running")

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
		r.Post("/api/demo-readiness-data", s.ensureDemoReadinessData)
		r.Post("/api/demo-readiness-snapshot", s.recordDemoReadinessSnapshot)
		r.Get("/api/project-templates", s.listProjectTemplates)
		r.Get("/api/project-templates/{id}", s.getProjectTemplate)
		r.Post("/api/project-templates/{id}/preview", s.previewProjectTemplate)
		r.Post("/api/project-templates/{id}/create-project", s.createProjectFromTemplate)
		r.Get("/api/project-template-runs", s.listProjectTemplateRuns)
		r.Post("/api/project-template-runs/{id}/retry-provision", s.retryProjectTemplateProvision)
		r.Post("/api/project-template-runs/{id}/request-provider-review-execution", s.requestProjectTemplateProviderReviewExecution)
		r.Get("/api/provider-accounts", s.listProviderAccounts)
		r.Post("/api/provider-accounts", s.createProviderAccount)
		r.Post("/api/provider-accounts/execute-token-rotation-plan", s.executeProviderAccountTokenRotationPlan)
		r.Get("/api/provider-accounts/{id}", s.getProviderAccount)
		r.Patch("/api/provider-accounts/{id}", s.updateProviderAccount)
		r.Post("/api/provider-accounts/{id}/check", s.checkProviderAccount)
		r.Post("/api/provider-accounts/{id}/rotate-token-env", s.rotateProviderAccountTokenEnv)
		r.Get("/api/projects/{id}", s.getProject)
		r.Patch("/api/projects/{id}", s.updateProject)
		r.Get("/api/projects/{id}/versions", s.listProjectVersions)
		r.Post("/api/projects/{id}/versions", s.createProjectVersion)
		r.Get("/api/project-versions/{id}", s.getProjectVersion)
		r.Get("/api/project-versions/{id}/validation", s.getProjectVersionValidation)
		r.Post("/api/project-versions/{id}/refresh", s.refreshProjectVersionProviders)
		r.Post("/api/project-versions/{id}/validation-rerun", s.requestProjectVersionValidationRerun)
		r.Post("/api/project-versions/{id}/validation-snapshot", s.recordProjectVersionValidationSnapshot)
		r.Post("/api/project-versions/{id}/validation-rerun-snapshot", s.recordProjectVersionValidationRerunSnapshot)
		r.Post("/api/project-versions/{id}/pin-config-commit", s.pinProjectVersionConfigCommit)
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
		r.Get("/api/git-repositories/{id}/config-scaffold", s.getConfigRepositoryScaffold)
		r.Post("/api/git-repositories/{id}/config-scaffold/request-git-workflow", s.requestConfigRepositoryGitWorkflow)
		r.Post("/api/git-repositories/{id}/config-scaffold/promotion-snapshot", s.recordConfigRepositoryGitWorkflowPromotionSnapshot)
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
		r.Post("/api/repo-tag-runs/{id}/result-snapshot", s.recordRepoTagRunResultSnapshot)
		r.Post("/api/repo-tag-runs/{id}/actions-refresh-snapshot", s.recordRepoTagRunActionsRefreshSnapshot)
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
		r.Post("/api/provider-review-attempts/{id}/claim", s.claimProviderReviewAttempt)
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
		r.Post("/api/agent/tasks/{id}/tool-audit-snapshot", s.recordAgentToolAuditSnapshot)
		r.Post("/api/agent/tasks/{id}/tool-arming-snapshot", s.recordAgentToolArmingSnapshot)
		r.Post("/api/agent/tasks/{id}/code-audit-snapshot", s.recordAgentCodeAuditSnapshot)
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
		r.Post("/api/projects/{id}/argo/pod-log-query-preview", s.previewArgoPodLogQuery)
		r.Post("/api/projects/{id}/argo/pod-logs", s.requestArgoPodLogRetrieval)
		r.Post("/api/projects/{id}/argo/pod-log-audit-snapshot", s.recordArgoPodLogAuditSnapshot)
		r.Post("/api/projects/{id}/webhook-connections", s.createWebhookConnection)
		r.Get("/api/projects/{id}/webhook-connections", s.listWebhookConnections)
		r.Get("/api/projects/{id}/webhook-events", s.listWebhookEvents)
		r.Post("/api/webhook-connections/{id}/threshold-decision-audit", s.recordWebhookThresholdDecisionAudit)
		r.Post("/api/webhook-connections/{id}/threshold-configuration", s.applyWebhookThresholdConfiguration)
		r.Post("/api/webhook-connections/{id}/rotate-secret", s.rotateWebhookConnectionSecret)
		r.Post("/api/webhook-events/{id}/replay", s.replayWebhookEvent)
		r.Post("/api/projects/{id}/ssh-machines", s.createSSHMachine)
		r.Get("/api/projects/{id}/ssh-machines", s.listSSHMachines)
		r.Get("/api/ssh-machines/{id}/rehearsal", s.getSSHMachineRehearsal)
		r.Post("/api/ssh-machines/{id}/target-environment-proof", s.recordSSHMachineTargetEnvironmentProof)
		r.Post("/api/ssh-machines/{id}/rehearsal-snapshot", s.recordSSHMachineRehearsalSnapshot)
		r.Post("/api/ssh-machines/{id}/verify", s.verifySSHMachine)
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

func (s *Server) recordDemoReadinessSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectSlug string `json:"project_slug"`
		DryRun      bool   `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	opts := DemoReadinessSnapshotOptions{ProjectID: req.ProjectID, ProjectSlug: req.ProjectSlug, DryRun: req.DryRun}
	projectID, err := ResolveDemoReadinessSnapshotProjectID(r.Context(), s.store, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "resolve demo readiness project failed")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "update") {
		return
	}
	opts.ProjectID = projectID
	opts.ProjectSlug = ""
	result, err := RecordDemoReadinessSnapshot(r.Context(), s.store, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "record demo readiness snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) ensureDemoReadinessData(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project"}, "create") {
		return
	}
	var req struct {
		ProjectName    string `json:"project_name"`
		ProjectSlug    string `json:"project_slug"`
		RepositoryName string `json:"repository_name"`
		RepositoryKey  string `json:"repository_key"`
		DryRun         bool   `json:"dry_run"`
		RecordSnapshot *bool  `json:"record_snapshot"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	recordSnapshot := true
	if req.RecordSnapshot != nil {
		recordSnapshot = *req.RecordSnapshot
	}
	actorUserID := ""
	if user := currentUser(r); user != nil {
		actorUserID = user.ID
	}
	result, err := EnsureDemoReadinessData(r.Context(), s.store, DemoReadinessDataOptions{
		ProjectName:    req.ProjectName,
		ProjectSlug:    req.ProjectSlug,
		RepositoryName: req.RepositoryName,
		RepositoryKey:  req.RepositoryKey,
		ActorUserID:    actorUserID,
		DryRun:         req.DryRun,
		RecordSnapshot: recordSnapshot,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not ensure demo readiness data")
		return
	}
	writeJSON(w, http.StatusOK, result)
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

func (s *Server) requestProjectTemplateProviderReviewExecution(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "create") {
		return
	}
	user := currentUser(r)
	run, err := queryOne(r.Context(), s.store.DB, `
		SELECT ptr.*, pt.name AS template_name
		FROM project_template_runs ptr
		LEFT JOIN project_templates pt ON pt.id=ptr.project_template_id
		WHERE ptr.id=$1
			AND ($2 OR ptr.requested_by=$3 OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id=ptr.project_id AND pm.user_id=$3
			))`,
		chi.URLParam(r, "id"),
		userCanReadAllProjects(user),
		userIDOrNil(user),
	)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	payload, err := projectTemplateProviderReviewApprovalPayloadForConfig(run, s.cfg.ProviderReviewExecutionEnabled, s.cfg.ProviderReviewMutationArmed)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	requestedBy := ""
	if user != nil {
		requestedBy = user.ID
	}
	if requestedBy == "" {
		writeError(w, http.StatusForbidden, "provider review execution approval requires a user")
		return
	}
	title := "execute provider review for template run " + fmt.Sprint(run["template_name"])
	approval, err := s.createOperationApproval(
		r.Context(),
		PolicyResource{
			Type:      "project_template_run",
			ID:        cleanOptionalID(fmt.Sprint(run["id"])),
			ProjectID: cleanOptionalID(fmt.Sprint(run["project_id"])),
		},
		templateProviderReviewExecuteApprovalAction,
		title,
		payload,
		requestedBy,
	)
	if err != nil {
		if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
			writeError(w, http.StatusConflict, "provider review execution approval is already pending")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not request provider review execution approval")
		return
	}
	delete(approval, "request_payload")
	writeJSON(w, http.StatusAccepted, map[string]any{"approval": approval})
}

func projectTemplateProviderReviewApprovalPayload(run map[string]any) (map[string]any, error) {
	return projectTemplateProviderReviewApprovalPayloadForConfig(run, false, false)
}

func projectTemplateProviderReviewApprovalPayloadForConfig(run map[string]any, providerReviewExecutionEnabled, providerReviewMutationArmed bool) (map[string]any, error) {
	result := mapFromAny(run["result"])
	details := mapFromAny(result["details"])
	repositoryReconciliation := mapFromAny(details["repository_reconciliation"])
	readiness := mapFromAny(repositoryReconciliation["provider_review_readiness"])
	executionPlan := mapFromAny(readiness["execution_plan"])
	executionRequest := mapFromAny(executionPlan["execution_request"])
	if executionRequest["status"] != "approval_ready" {
		return nil, fmt.Errorf("provider review execution request is not approval ready")
	}
	starterFilePayload := projectTemplateStarterFilePayloadSummary(run)
	executionGuardrail := templateProviderReviewExecutionGuardrailWithStaging(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		stringFromMap(executionRequest, "source_branch"),
		stringFromMap(executionRequest, "target_branch"),
		providerReviewExecutionEnabled,
		providerReviewMutationArmed,
		starterFilePayloadReady(starterFilePayload),
	)
	providerAPIRequestPlan := templateProviderReviewAPIRequestPlan(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		stringFromMap(executionRequest, "source_branch"),
		stringFromMap(executionRequest, "target_branch"),
		starterFilePayload,
	)
	credentialStrategy := sanitizedProviderReviewCredentialStrategy(mapFromAny(repositoryReconciliation["credential_strategy"]))
	if len(mapFromAny(repositoryReconciliation["credential_strategy"])) == 0 {
		credentialStrategy = sanitizedProviderReviewCredentialStrategy(mapFromAny(executionPlan["credential_strategy"]))
	}
	reconciliation := templateProviderReviewExecutionReconciliation(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		starterFilePayload,
		executionGuardrail,
		providerAPIRequestPlan,
		credentialStrategy,
	)
	targetSummary := providerReviewExecutionTargetSummary(
		stringFromMap(executionRequest, "provider_type"),
		stringFromMap(executionRequest, "review_kind"),
		providerAPIRequestPlan,
		starterFilePayload,
		reconciliation,
	)
	projectTemplateRunID := cleanOptionalID(fmt.Sprint(run["id"]))
	if projectTemplateRunID == "" {
		return nil, fmt.Errorf("template run id is required")
	}
	request := map[string]any{
		"status":                   executionRequest["status"],
		"approval_action":          executionRequest["approval_action"],
		"resource_type":            executionRequest["resource_type"],
		"provider_type":            executionRequest["provider_type"],
		"review_kind":              executionRequest["review_kind"],
		"source_branch":            executionRequest["source_branch"],
		"target_branch":            executionRequest["target_branch"],
		"payload_redacted":         true,
		"contains_token":           false,
		"provider_api_mutation":    "disabled",
		"requires_operator_review": true,
	}
	return map[string]any{
		"kind":                           "project_template_provider_review_execute",
		"project_template_run_id":        projectTemplateRunID,
		"project_id":                     cleanOptionalID(fmt.Sprint(run["project_id"])),
		"execution_request":              request,
		"execution_guardrail":            executionGuardrail,
		"credential_strategy":            credentialStrategy,
		"starter_file_payload":           starterFilePayload,
		"provider_api_request_plan":      providerAPIRequestPlan,
		"provider_review_reconciliation": reconciliation,
		"provider_review_target_summary": targetSummary,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"message":                        "Provider review execution is approval-gated; provider API mutation remains disabled in the first version.",
	}, nil
}

func projectTemplateStarterFilePayloadSummary(run map[string]any) map[string]any {
	result := mapFromAny(run["result"])
	return starterFilePayloadSummaryFromFiles(mapSliceFromAny(result["template_files"]))
}

func starterFilePayloadSummaryFromFiles(files []map[string]any) map[string]any {
	if len(files) == 0 {
		return map[string]any{
			"status":           "blocked",
			"file_count":       0,
			"files":            []map[string]any{},
			"payload_redacted": true,
			"content_included": false,
			"blocked_reason":   "template run result does not include starter file summaries",
		}
	}
	summaries := make([]map[string]any, 0, len(files))
	for _, file := range files {
		path := safeTemplateFilePath(stringFromMap(file, "path"))
		if path == "" {
			continue
		}
		summaries = append(summaries, map[string]any{
			"id":     cleanOptionalID(fmt.Sprint(file["id"])),
			"path":   path,
			"kind":   cleanOptionalText(firstNonEmptyString(stringFromMap(file, "kind"), "text")),
			"status": cleanOptionalText(firstNonEmptyString(stringFromMap(file, "status"), "planned")),
		})
	}
	if len(summaries) == 0 {
		return map[string]any{
			"status":           "blocked",
			"file_count":       0,
			"files":            []map[string]any{},
			"payload_redacted": true,
			"content_included": false,
			"blocked_reason":   "template run result does not include safe starter file paths",
		}
	}
	return map[string]any{
		"status":           "ready",
		"file_count":       len(summaries),
		"files":            summaries,
		"payload_redacted": true,
		"content_included": false,
	}
}

func sanitizedStarterFilePayloadSummary(payload map[string]any) map[string]any {
	return starterFilePayloadSummaryFromFiles(mapSliceFromAny(payload["files"]))
}

func starterFilePayloadReady(payload map[string]any) bool {
	return payload["status"] == "ready" && intFromAny(payload["file_count"], 0) > 0 && payload["content_included"] == false
}

func providerReviewStarterFilePayloadForExecution(ctx context.Context, tx *sqlx.Tx, payload map[string]any) map[string]any {
	runID := cleanOptionalID(stringFromMap(payload, "project_template_run_id"))
	if tx != nil && runID != "" {
		run, err := queryOne(ctx, tx, "SELECT result FROM project_template_runs WHERE id=$1", runID)
		if err == nil {
			return projectTemplateStarterFilePayloadSummary(run)
		}
	}
	return sanitizedStarterFilePayloadSummary(mapFromAny(payload["starter_file_payload"]))
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
		"token_rotation_plan":    providerAccountAutomatedRotationPlan(items, time.Now().UTC()),
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

func (s *Server) executeProviderAccountTokenRotationPlan(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account"}, "update") {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "automated rotation plan execution"
	}
	now := time.Now().UTC()
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start provider token rotation execution transaction")
		return
	}
	defer tx.Rollback()
	items, err := queryMaps(r.Context(), tx, `
		SELECT *
		FROM provider_accounts
		ORDER BY provider_type, name
		FOR UPDATE`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider token rotation plan")
		return
	}
	plan := providerAccountAutomatedRotationPlan(items, now)
	candidates := providerAccountAutomatedRotationExecutionCandidates(items, now)
	if len(candidates) == 0 {
		writeError(w, http.StatusConflict, "no provider token rotation candidates are ready")
		return
	}

	rotated := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		account := candidate.account
		accountID := rawStringFromMap(account, "id")
		currentTokenEnv := rawStringFromMap(account, "token_env")
		next := providerAccountInput{
			Name:         rawStringFromMap(account, "name"),
			ProviderType: rawStringFromMap(account, "provider_type"),
			APIBaseURL:   rawStringFromMap(account, "api_base_url"),
			WebBaseURL:   rawStringFromMap(account, "web_base_url"),
			TokenEnv:     candidate.tokenEnv,
			DefaultOwner: rawStringFromMap(account, "default_owner"),
			Visibility:   rawStringFromMap(account, "visibility"),
			Metadata:     cloneMap(mapFromAny(account["metadata"])),
		}
		next.Metadata = providerAccountRotationMetadata(next.Metadata, currentTokenEnv, candidate.tokenEnv, reason, currentUser(r))
		metadataJSON, err := jsonParam(next.Metadata)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid provider token rotation metadata")
			return
		}
		item, err := queryOne(r.Context(), tx, `
			UPDATE provider_accounts
			SET token_env=$2,
				metadata=$3::jsonb,
				updated_at=now()
			WHERE id=$1 AND token_env=$4
			RETURNING *`,
			accountID,
			candidate.tokenEnv,
			metadataJSON,
			currentTokenEnv,
		)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusConflict, "provider account changed during token rotation execution; retry")
				return
			}
			writeError(w, http.StatusInternalServerError, "could not execute provider token rotation")
			return
		}
		if err := refreshGitRemotesForProviderAccount(r.Context(), tx, next, accountID); err != nil {
			writeError(w, http.StatusInternalServerError, "could not refresh provider account remotes")
			return
		}
		rotated = append(rotated, item)
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.execute_token_rotation_plan") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit provider token rotation execution")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":                   "executed",
		"automation_enabled":     true,
		"provider_api_call_made": false,
		"rotated_count":          len(rotated),
		"skipped_count":          len(items) - len(rotated),
		"plan_before":            plan,
		"items":                  sanitizeProviderAccounts(rotated),
	})
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
		if key == "metadata" {
			out[key] = sanitizeProviderAccountMetadata(mapFromAny(value))
			continue
		}
		out[key] = value
	}
	tokenEnv := rawStringFromMap(item, "token_env")
	out["token_configured"] = tokenEnv != ""
	out["masked_token_env"] = maskProviderTokenEnv(tokenEnv)
	out["token_rotation_status"] = providerAccountTokenRotationStatus(item, time.Now().UTC())
	out["token_rotation_candidate"] = providerAccountRotationCandidate(item)
	return out
}

func sanitizeProviderAccountMetadata(metadata map[string]any) map[string]any {
	out := cloneMap(metadata)
	for _, key := range providerTokenRotationCandidateKeys {
		delete(out, key)
	}
	delete(out, "token")
	delete(out, "token_env")
	delete(out, "secret")
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

func providerAccountAutomatedRotationPlan(items []map[string]any, now time.Time) map[string]any {
	planItems := make([]map[string]any, 0, len(items))
	counts := map[string]int{
		"ready":      0,
		"blocked":    0,
		"not_needed": 0,
	}
	for _, item := range items {
		entry := providerAccountAutomatedRotationPlanItem(item, now)
		status := strings.TrimSpace(fmt.Sprint(entry["status"]))
		if _, ok := counts[status]; !ok {
			status = "blocked"
		}
		counts[status]++
		planItems = append(planItems, entry)
	}
	nextAction := "No provider accounts configured."
	if len(items) > 0 {
		switch {
		case counts["ready"] > 0:
			nextAction = "Review ready provider token rotation candidates, then execute the ready rotation plan or rotate manually."
		case counts["blocked"] > 0:
			nextAction = "Add safe rotation candidate token env metadata before automated rotation can be enabled."
		default:
			nextAction = "No provider token rotation is currently due."
		}
	}
	return map[string]any{
		"mode":                "dry_run",
		"automation_enabled":  false,
		"execution_available": counts["ready"] > 0,
		"external_call_made":  false,
		"total":               len(items),
		"ready":               counts["ready"],
		"blocked":             counts["blocked"],
		"not_needed":          counts["not_needed"],
		"next_action":         nextAction,
		"items":               planItems,
	}
}

func providerAccountAutomatedRotationPlanItem(item map[string]any, now time.Time) map[string]any {
	status := providerAccountTokenRotationStatus(item, now)
	accountID := rawStringFromMap(item, "id")
	tokenStatus := strings.TrimSpace(fmt.Sprint(status["status"]))
	entry := map[string]any{
		"provider_account_id": accountID,
		"name":                rawStringFromMap(item, "name"),
		"provider_type":       rawStringFromMap(item, "provider_type"),
		"rotation_status":     tokenStatus,
		"status":              "blocked",
		"automation_enabled":  false,
		"external_call_made":  false,
		"masked_current_env":  maskProviderTokenEnv(rawStringFromMap(item, "token_env")),
	}
	for _, key := range []string{"last_rotated_at", "next_rotation_due_at", "days_since_rotation", "days_until_due"} {
		if value, ok := status[key]; ok {
			entry[key] = value
		}
	}
	if rawStringFromMap(item, "token_env") == "" {
		entry["blocked_reason"] = "current token env is missing"
		entry["next_action"] = "configure a current provider token env before planning automated rotation"
		return entry
	}
	if tokenStatus != "due" && tokenStatus != "soon" {
		entry["status"] = "not_needed"
		entry["next_action"] = "no rotation required in the current window"
		return entry
	}
	candidate := providerAccountRotationCandidate(item)
	entry["candidate_present"] = candidate["present"]
	entry["masked_candidate_env"] = candidate["masked_token_env"]
	if candidate["safe"] != true {
		if candidate["present"] == true {
			entry["blocked_reason"] = "rotation candidate token env is not allowed for this provider type"
			entry["next_action"] = "set provider account metadata rotation_candidate_token_env to an allowed provider-scoped env name"
		} else {
			entry["blocked_reason"] = "safe rotation candidate token env metadata is missing"
			entry["next_action"] = "set provider account metadata rotation_candidate_token_env to an allowed env name"
		}
		return entry
	}
	if candidate["same_as_current"] == true {
		entry["blocked_reason"] = "candidate token env matches the current token env"
		entry["next_action"] = "provide a different allowed candidate token env"
		return entry
	}
	entry["status"] = "ready"
	entry["next_action"] = "ready for operator-triggered token-env rotation execution"
	return entry
}

type providerAccountRotationExecutionCandidate struct {
	account  map[string]any
	tokenEnv string
}

func providerAccountAutomatedRotationExecutionCandidates(items []map[string]any, now time.Time) []providerAccountRotationExecutionCandidate {
	candidates := make([]providerAccountRotationExecutionCandidate, 0)
	for _, item := range items {
		planItem := providerAccountAutomatedRotationPlanItem(item, now)
		if planItem["status"] != "ready" {
			continue
		}
		tokenEnv := providerAccountRotationCandidateEnv(item)
		if tokenEnv == "" ||
			!safeTemplateProviderTokenEnv(rawStringFromMap(item, "provider_type"), tokenEnv) ||
			tokenEnv == rawStringFromMap(item, "token_env") {
			continue
		}
		candidates = append(candidates, providerAccountRotationExecutionCandidate{
			account:  item,
			tokenEnv: tokenEnv,
		})
	}
	return candidates
}

var providerTokenRotationCandidateKeys = []string{
	"rotation_candidate_token_env",
	"next_token_env",
	"candidate_token_env",
	"automated_rotation_token_env",
}

func providerAccountRotationCandidateEnv(item map[string]any) string {
	metadata := mapFromAny(item["metadata"])
	for _, key := range providerTokenRotationCandidateKeys {
		if value := strings.TrimSpace(fmt.Sprint(metadata[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func providerAccountRotationCandidate(item map[string]any) map[string]any {
	providerType := rawStringFromMap(item, "provider_type")
	current := rawStringFromMap(item, "token_env")
	candidate := providerAccountRotationCandidateEnv(item)
	out := map[string]any{
		"present":          candidate != "",
		"safe":             false,
		"same_as_current":  false,
		"masked_token_env": maskProviderTokenEnv(candidate),
	}
	if candidate == "" {
		return out
	}
	out["safe"] = safeTemplateProviderTokenEnv(providerType, candidate)
	out["same_as_current"] = current != "" && candidate == current
	return out
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

func (s *Server) listProjectVersions(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ProjectID: projectID}, "read") {
		return
	}
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, project_id, version, source, metadata, created_at
		FROM project_versions
		WHERE project_id=$1
		ORDER BY created_at DESC`, projectID)
	writeQueryResult(w, items, err)
}

func (s *Server) createProjectVersion(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Version  string         `json:"version"`
		Source   string         `json:"source"`
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Version = strings.TrimSpace(req.Version)
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	if len(req.Version) > 200 {
		writeError(w, http.StatusBadRequest, "version must be 200 characters or fewer")
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		req.Source = "manual"
	}
	metadata, err := jsonParam(req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "metadata must be valid json")
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO project_versions(project_id, version, source, metadata)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (project_id, version) DO UPDATE
		SET source=EXCLUDED.source,
			metadata=EXCLUDED.metadata
		RETURNING id, project_id, version, source, metadata, created_at`,
		projectID,
		req.Version,
		req.Source,
		metadata,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create project version")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getProjectVersion(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	projectID, err := projectIDForProjectVersion(r.Context(), s.store.DB, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "read") {
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, `
		SELECT id, project_id, version, source, metadata, created_at
		FROM project_versions
		WHERE id=$1`, versionID)
	writeQueryOne(w, item, err)
}

func (s *Server) getProjectVersionValidation(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	projectID, err := projectIDForProjectVersion(r.Context(), s.store.DB, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "read") {
		return
	}
	version, err := queryOne(r.Context(), s.store.DB, `
		SELECT id, project_id, version, source, metadata, created_at
		FROM project_versions
		WHERE id=$1`, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	remotes, err := queryMaps(r.Context(), s.store.DB, `
		SELECT gr.id, gr.remote_key, gr.provider_type, gr.latest_sha, gr.default_branch, r.repo_key, r.repo_role, r.name AS repository_name
		FROM git_remotes gr
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		WHERE r.project_id=$1`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load version remotes")
		return
	}
	tagRuns, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, project_git_repository_id, target_remote_id, git_remote_id, tag_name, target_sha, status, created_at, finished_at
		FROM repo_tag_runs
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT 500`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load tag runs")
		return
	}
	actionRuns, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, git_remote_id, run_id, workflow_name, branch, commit_sha, status, conclusion, started_at, updated_at
		FROM github_action_runs
		WHERE git_remote_id IN (
			SELECT gr.id
			FROM git_remotes gr
			JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
			WHERE r.project_id=$1
		)
		ORDER BY updated_at DESC
		LIMIT 500`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load action runs")
		return
	}
	argoApps, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, name, namespace, status, metadata, synced_at, updated_at
		FROM argo_apps
		WHERE project_id=$1
		ORDER BY updated_at DESC
		LIMIT 500`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load Argo apps")
		return
	}
	argoConnections, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, name, last_sync_status
		FROM argo_connections
		WHERE project_id=$1
		ORDER BY updated_at DESC
		LIMIT 100`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load Argo connections")
		return
	}
	refreshOperations, err := queryProjectVersionRefreshOperations(r.Context(), s.store.DB, fmt.Sprint(version["id"]))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project version refresh operations")
		return
	}
	backgroundOperations, err := queryProjectVersionValidationRerunOperations(r.Context(), s.store.DB, fmt.Sprint(version["id"]))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project version validation rerun operations")
		return
	}
	writeJSON(w, http.StatusOK, projectVersionValidationPreview(version, remotes, tagRuns, actionRuns, argoApps, argoConnections, refreshOperations, backgroundOperations))
}

func (s *Server) refreshProjectVersionProviders(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	projectID, err := projectIDForProjectVersion(r.Context(), s.store.DB, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "project_version.refresh") {
		return
	}
	version, remotes, argoConnections, err := s.projectVersionRefreshInputs(r.Context(), versionID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project version refresh inputs")
		return
	}
	metadata := mapFromAny(version["metadata"])
	repositories := mapSliceFromAny(metadata["repositories"])
	refreshPlan := projectVersionProviderRefreshPlan(repositories, remotes, argoConnections)
	steps := mapSliceFromAny(refreshPlan["steps"])
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start project version refresh transaction")
		return
	}
	defer tx.Rollback()
	result, err := s.enqueueProjectVersionRefreshOperationsTx(r.Context(), tx, version, steps, argoConnections, currentUser(r).ID)
	if errors.Is(err, errProjectVersionRefreshAlreadyQueued) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "project_version.refresh") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit project version refresh")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) requestProjectVersionValidationRerun(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	projectID, err := projectIDForProjectVersion(r.Context(), s.store.DB, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "project_version.refresh") {
		return
	}
	version, err := queryOne(r.Context(), s.store.DB, `
		SELECT id, project_id, version
		FROM project_versions
		WHERE id=$1`, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start validation rerun transaction")
		return
	}
	defer tx.Rollback()
	result, err := s.enqueueProjectVersionValidationRerunTx(r.Context(), tx, version, currentUser(r).ID)
	if errors.Is(err, errProjectVersionRefreshAlreadyQueued) {
		writeError(w, http.StatusConflict, "project version validation rerun is already queued or running")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not enqueue validation rerun")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "project_version.validation_rerun") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit validation rerun request")
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) pinProjectVersionConfigCommit(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	projectID, err := projectIDForProjectVersion(r.Context(), s.store.DB, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		RepositoryID string `json:"repository_id"`
		RemoteID     string `json:"remote_id"`
		DryRun       bool   `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RepositoryID) == "" {
		writeError(w, http.StatusBadRequest, "repository_id is required")
		return
	}
	result, err := PinConfigCommit(r.Context(), s.store, ConfigCommitPinOptions{
		ProjectVersionID: versionID,
		RepositoryID:     req.RepositoryID,
		RemoteID:         req.RemoteID,
		DryRun:           req.DryRun,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("config commit pin failed", "project_version_id", versionID, "repository_id", strings.TrimSpace(req.RepositoryID), "error", err)
		}
		writeError(w, http.StatusBadRequest, "pin config commit failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProjectVersionValidationSnapshot(w http.ResponseWriter, r *http.Request) {
	s.recordProjectVersionValidationSnapshotWithOptions(w, r, false)
}

func (s *Server) recordProjectVersionValidationRerunSnapshot(w http.ResponseWriter, r *http.Request) {
	s.recordProjectVersionValidationSnapshotWithOptions(w, r, true)
}

func (s *Server) recordProjectVersionValidationSnapshotWithOptions(w http.ResponseWriter, r *http.Request, requireRecordedRefresh bool) {
	versionID := chi.URLParam(r, "id")
	projectID, err := projectIDForProjectVersion(r.Context(), s.store.DB, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	recordingTrigger := "operator_request"
	if requireRecordedRefresh {
		recordingTrigger = "validation_auto_reload"
	}
	result, err := RecordProjectVersionValidationSnapshot(r.Context(), s.store, ProjectVersionValidationSnapshotOptions{
		ProjectVersionID:       versionID,
		DryRun:                 req.DryRun,
		RequireRecordedRefresh: requireRecordedRefresh,
		RecordingTrigger:       recordingTrigger,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("project version validation snapshot failed", "project_version_id", versionID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record validation snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) projectVersionRefreshInputs(ctx context.Context, versionID, projectID string) (map[string]any, []map[string]any, []map[string]any, error) {
	version, err := queryOne(ctx, s.store.DB, `
		SELECT id, project_id, version, source, metadata, created_at
		FROM project_versions
		WHERE id=$1`, versionID)
	if err != nil {
		return nil, nil, nil, err
	}
	remotes, err := queryMaps(ctx, s.store.DB, `
		SELECT gr.id, gr.remote_key, gr.provider_type, gr.latest_sha, gr.default_branch, r.repo_key, r.repo_role, r.name AS repository_name
		FROM git_remotes gr
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		WHERE r.project_id=$1`, projectID)
	if err != nil {
		return nil, nil, nil, err
	}
	argoConnections, err := queryMaps(ctx, s.store.DB, `
		SELECT id, name, last_sync_status
		FROM argo_connections
		WHERE project_id=$1
		ORDER BY updated_at DESC
		LIMIT 100`, projectID)
	if err != nil {
		return nil, nil, nil, err
	}
	return version, remotes, argoConnections, nil
}

func (s *Server) enqueueProjectVersionRefreshOperationsTx(ctx context.Context, tx *sqlx.Tx, version map[string]any, steps, argoConnections []map[string]any, actorID string) (map[string]any, error) {
	projectID := strings.TrimSpace(fmt.Sprint(version["project_id"]))
	versionID := strings.TrimSpace(fmt.Sprint(version["id"]))
	if projectID == "" || projectID == "<nil>" || versionID == "" || versionID == "<nil>" {
		return nil, fmt.Errorf("project version metadata is incomplete")
	}
	existing, err := queryMaps(ctx, tx, `
		SELECT id
		FROM operation_runs
		WHERE status IN ('queued', 'running')
			AND operation_type IN ('git.refs.refresh', 'github.actions.sync', 'argo.apps.sync')
			AND input->>'project_version_id'=$1
		LIMIT 1`, versionID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, errProjectVersionRefreshAlreadyQueued
	}
	operations := []map[string]any{}
	blockedSteps := []map[string]any{}
	enqueuedKeys := map[string]bool{}
	enqueueSummary := func(kind string, op map[string]any, remoteID, connectionID string) {
		operations = append(operations, map[string]any{
			"operation_run_id":   op["id"],
			"operation_type":     op["operation_type"],
			"kind":               kind,
			"refresh_kind":       kind,
			"remote_id":          remoteID,
			"argo_connection_id": connectionID,
			"status":             op["status"],
		})
	}
	for _, step := range steps {
		kind := strings.TrimSpace(fmt.Sprint(step["kind"]))
		status := strings.TrimSpace(fmt.Sprint(step["status"]))
		if status != "planned" {
			blockedSteps = append(blockedSteps, sanitizedProjectVersionRefreshStep(step))
			continue
		}
		remoteID := strings.TrimSpace(fmt.Sprint(step["remote_id"]))
		switch kind {
		case "git_ref_fetch":
			key := kind + ":" + remoteID
			if remoteID == "" || remoteID == "<nil>" || enqueuedKeys[key] {
				continue
			}
			input := map[string]any{
				"project_version_id": versionID,
				"refresh_kind":       kind,
				"remote_id":          remoteID,
				"repo_key":           step["repo_key"],
				"tag":                step["tag_name"],
			}
			op, err := enqueueOperationTx(ctx, tx, projectID, remoteID, "git.refs.refresh", "refresh Git refs for project version "+fmt.Sprint(version["version"]), input, []string{"git"}, "")
			if err != nil {
				return nil, err
			}
			enqueuedKeys[key] = true
			enqueueSummary(kind, op, remoteID, "")
		case "github_actions_api_refresh":
			key := kind + ":" + remoteID
			if remoteID == "" || remoteID == "<nil>" || enqueuedKeys[key] {
				continue
			}
			op, err := s.enqueueRemoteOperationRun(ctx, tx, remoteID, "github.actions.sync", map[string]any{
				"project_version_id": versionID,
				"refresh_kind":       kind,
			}, actorID)
			if err != nil {
				return nil, err
			}
			enqueuedKeys[key] = true
			enqueueSummary(kind, op, remoteID, "")
		case "argocd_app_refresh":
			for _, connection := range argoConnections {
				connectionID := strings.TrimSpace(fmt.Sprint(connection["id"]))
				key := kind + ":" + connectionID
				if connectionID == "" || connectionID == "<nil>" || enqueuedKeys[key] {
					continue
				}
				op, err := enqueueOperationTx(ctx, tx, projectID, "", "argo.apps.sync", "refresh Argo apps for project version "+fmt.Sprint(version["version"]), map[string]any{
					"project_version_id": versionID,
					"refresh_kind":       kind,
					"argo_connection_id": connectionID,
				}, []string{"argo"}, "control-worker")
				if err != nil {
					return nil, err
				}
				enqueuedKeys[key] = true
				enqueueSummary(kind, op, "", connectionID)
			}
		default:
			blockedSteps = append(blockedSteps, sanitizedProjectVersionRefreshStep(step))
		}
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("no planned provider refresh operations are available")
	}
	return map[string]any{
		"mode":                             "project_version_provider_refresh_execution",
		"project_version_id":               versionID,
		"version":                          version["version"],
		"operation_enqueued":               true,
		"worker_job_created":               true,
		"external_call_made":               false,
		"secret_included":                  false,
		"raw_provider_response":            false,
		"operation_count":                  len(operations),
		"blocked_step_count":               len(blockedSteps),
		"operations":                       operations,
		"blocked_steps":                    blockedSteps,
		"validation_rerun_required":        true,
		"validation_auto_reload_supported": true,
		"server_side_validation_rerun":     false,
		"result_recording_scope":           "operation_ids_and_sanitized_refresh_kinds",
		"required_operator_action":         "Keep the validation panel open; the UI can reload ProjectVersion validation until queued refresh operations finish.",
	}, nil
}

func (s *Server) enqueueProjectVersionValidationRerunTx(ctx context.Context, tx *sqlx.Tx, version map[string]any, actorID string) (map[string]any, error) {
	projectID := strings.TrimSpace(fmt.Sprint(version["project_id"]))
	versionID := strings.TrimSpace(fmt.Sprint(version["id"]))
	if projectID == "" || projectID == "<nil>" || versionID == "" || versionID == "<nil>" {
		return nil, fmt.Errorf("project version metadata is incomplete")
	}
	existing, err := queryMaps(ctx, tx, `
		SELECT id
		FROM operation_runs
		WHERE status IN ('queued', 'running')
			AND operation_type='project_version.validation_rerun'
			AND input->>'project_version_id'=$1
		LIMIT 1`, versionID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, errProjectVersionRefreshAlreadyQueued
	}
	input := map[string]any{
		"project_version_id":             versionID,
		"validation_source":              "local_synced_database_state",
		"recording_trigger":              "standalone_background_validation_rerun",
		"require_recorded_refresh":       true,
		"external_call_made":             false,
		"provider_api_called":            false,
		"git_fetch_performed":            false,
		"argocd_api_called":              false,
		"raw_provider_response_recorded": false,
		"actor_user_id":                  actorID,
	}
	op, err := enqueueOperationTx(ctx, tx, projectID, "", "project_version.validation_rerun", "rerun validation for project version "+fmt.Sprint(version["version"]), input, []string{"validation"}, "control-worker")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"mode":                                   "project_version_background_validation_rerun_request",
		"project_version_id":                     versionID,
		"version":                                version["version"],
		"operation":                              op,
		"operation_run_id":                       op["id"],
		"operation_enqueued":                     true,
		"worker_job_created":                     true,
		"background_worker_enqueued":             true,
		"automatic_background_rerun":             true,
		"validation_snapshot_write_requested":    true,
		"validation_source":                      "local_synced_database_state",
		"requires_recorded_refresh":              true,
		"external_call_made":                     false,
		"provider_api_called":                    false,
		"git_fetch_performed":                    false,
		"argocd_api_called":                      false,
		"raw_provider_response_recorded":         false,
		"secret_included":                        false,
		"result_recording_scope":                 "sanitized_validation_snapshot_metadata",
		"suppressed_fields":                      []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"required_operator_action":               "Wait for the control worker to rerun local validation and record a sanitized ProjectVersion validation snapshot.",
		"provider_refresh_operation_performed":   false,
		"standalone_background_worker_enabled":   true,
		"control_worker_auto_snapshot_supported": true,
	}, nil
}

func sanitizedProjectVersionRefreshStep(step map[string]any) map[string]any {
	return map[string]any{
		"kind":       step["kind"],
		"status":     step["status"],
		"repo_key":   step["repo_key"],
		"repo_role":  step["repo_role"],
		"remote_id":  step["remote_id"],
		"remote_key": step["remote_key"],
		"reason":     step["reason"],
	}
}

func queryProjectVersionRefreshOperations(ctx context.Context, db sqlx.ExtContext, versionID string) ([]map[string]any, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" || versionID == "<nil>" {
		return nil, nil
	}
	return queryMaps(ctx, db, `
		SELECT id, operation_type, status, error, input, started_at, finished_at, created_at, updated_at
		FROM operation_runs
		WHERE input->>'project_version_id'=$1
			AND operation_type IN ('git.refs.refresh', 'github.actions.sync', 'argo.apps.sync')
		ORDER BY created_at DESC
		LIMIT 50`, versionID)
}

func queryProjectVersionValidationRerunOperations(ctx context.Context, db sqlx.ExtContext, versionID string) ([]map[string]any, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" || versionID == "<nil>" {
		return nil, nil
	}
	return queryMaps(ctx, db, `
		SELECT id, operation_type, status, error, input, result, started_at, finished_at, created_at, updated_at
		FROM operation_runs
		WHERE input->>'project_version_id'=$1
			AND operation_type='project_version.validation_rerun'
		ORDER BY created_at DESC
		LIMIT 20`, versionID)
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

func (s *Server) getConfigRepositoryScaffold(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "read") {
		return
	}
	repo, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	remotes, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, name, remote_key, provider_type, remote_role, default_branch, latest_sha, last_sync_status
		FROM git_remotes
		WHERE project_git_repository_id=$1
		ORDER BY created_at DESC`, repoID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load git remotes")
		return
	}
	versions, err := queryMaps(r.Context(), s.store.DB, `
		SELECT id, version, metadata, created_at
		FROM project_versions
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT 100`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project versions")
		return
	}
	workflowOperations, err := queryConfigRepositoryGitWorkflowOperations(r.Context(), s.store.DB, projectID, repoID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config git workflow operations")
		return
	}
	writeJSON(w, http.StatusOK, configRepositoryScaffoldPreview(repo, remotes, versions, workflowOperations))
}

func (s *Server) requestConfigRepositoryGitWorkflow(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	resource := PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return
	}
	decision := NewPolicyChecker().Check(currentUser(r), resource, "config.git_commit")
	if decision.Effect == PolicyDeny {
		writeJSON(w, http.StatusForbidden, decision)
		return
	}
	repo, remotes, versions, preview, err := s.configRepositoryScaffoldPreviewForRequest(r.Context(), repoID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config scaffold preview")
		return
	}
	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["plan_state"] != "planned" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":               "config git workflow is not ready",
			"blocked_reasons":     commitPlan["blocked_reasons"],
			"git_commit_plan":     commitPlan,
			"scaffold_preview":    preview,
			"external_call_made":  false,
			"git_write_performed": false,
		})
		return
	}
	input := configRepositoryGitWorkflowInput(repo, remotes, preview)
	payload := map[string]any{
		"kind":                  "config_git_commit",
		"project_id":            projectID,
		"repo_id":               repoID,
		"input":                 input,
		"scaffold_file_count":   preview["file_count"],
		"project_version_count": len(versions),
		"file_content_included": false,
		"secret_included":       false,
		"external_call_made":    false,
		"git_write_performed":   false,
	}
	if decision.Effect == PolicyRequireConfirm {
		approval, err := s.createOperationApproval(r.Context(), resource, "config.git_commit", "config git workflow "+fmt.Sprint(repo["name"]), payload, currentUser(r).ID)
		if err != nil {
			if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
				writeError(w, http.StatusConflict, "approval request is already pending")
				return
			}
			writeError(w, http.StatusInternalServerError, "could not create approval request")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"approval": approval, "decision": decision})
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start config git workflow transaction")
		return
	}
	defer tx.Rollback()
	op, err := enqueueConfigRepositoryGitWorkflowTx(r.Context(), tx, projectID, repo, remotes, preview, currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not enqueue config git workflow")
		return
	}
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "config_git_commit.enqueue") {
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit config git workflow request")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"operation":                op,
		"git_commit_plan":          commitPlan,
		"scaffold_preview":         preview,
		"operation_request_state":  "queued",
		"operation_request_result": configRepositoryGitWorkflowRequestResult(op),
	})
}

func (s *Server) configRepositoryScaffoldPreviewForRequest(ctx context.Context, repoID, projectID string) (map[string]any, []map[string]any, []map[string]any, map[string]any, error) {
	repo, err := queryOne(ctx, s.store.DB, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	remotes, err := queryMaps(ctx, s.store.DB, `
		SELECT id, name, remote_key, provider_type, remote_role, default_branch, latest_sha, last_sync_status
		FROM git_remotes
		WHERE project_git_repository_id=$1
		ORDER BY created_at DESC`, repoID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	versions, err := queryMaps(ctx, s.store.DB, `
		SELECT id, version, metadata, created_at
		FROM project_versions
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT 100`, projectID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	workflowOperations, err := queryConfigRepositoryGitWorkflowOperations(ctx, s.store.DB, projectID, repoID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return repo, remotes, versions, configRepositoryScaffoldPreview(repo, remotes, versions, workflowOperations), nil
}

func queryConfigRepositoryGitWorkflowOperations(ctx context.Context, db sqlx.ExtContext, projectID, repoID string) ([]map[string]any, error) {
	return queryMaps(ctx, db, `
		SELECT op.id, op.status, op.created_at, op.updated_at, op.started_at, op.finished_at,
			COUNT(ol.id)::int AS operation_log_count
		FROM operation_runs op
		LEFT JOIN operation_logs ol ON ol.operation_run_id=op.id
		WHERE op.project_id=$1
			AND op.operation_type='config.git_commit'
			AND op.input->>'project_git_repository_id'=$2
		GROUP BY op.id, op.status, op.created_at, op.updated_at, op.started_at, op.finished_at
		ORDER BY op.created_at DESC, op.id DESC
		LIMIT 20`, projectID, repoID)
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

func configRepositoryScaffoldPreview(repo map[string]any, remotes []map[string]any, optionalRows ...[]map[string]any) map[string]any {
	repoRole := strings.ToLower(strings.TrimSpace(stringFromMap(repo, "repo_role")))
	environments := []string{"dev", "test", "prod"}
	files := make([]map[string]any, 0, len(environments)*3+1)
	for _, env := range environments {
		files = append(files,
			map[string]any{
				"path":        "envs/" + env + "/values.yaml",
				"environment": env,
				"purpose":     "environment values entrypoint",
				"required":    true,
			},
			map[string]any{
				"path":        "envs/" + env + "/secrets.example.yaml",
				"environment": env,
				"purpose":     "redacted secret shape only; real secrets stay outside Git",
				"required":    true,
			},
			map[string]any{
				"path":        "envs/" + env + "/README.md",
				"environment": env,
				"purpose":     "operator notes, owners, rollout checks, and rollback hints",
				"required":    true,
			},
		)
	}
	files = append(files, map[string]any{
		"path":        "README.md",
		"environment": "all",
		"purpose":     "config repository overview and branch/review policy",
		"required":    true,
	})
	remoteSummaries := make([]map[string]any, 0, len(remotes))
	for _, remote := range remotes {
		remoteSummaries = append(remoteSummaries, map[string]any{
			"id":               remote["id"],
			"name":             remote["name"],
			"remote_key":       remote["remote_key"],
			"provider_type":    remote["provider_type"],
			"remote_role":      remote["remote_role"],
			"default_branch":   remote["default_branch"],
			"latest_sha":       remote["latest_sha"],
			"last_sync_status": remote["last_sync_status"],
		})
	}
	blockedReasons := []string{}
	if repoRole != "config" {
		blockedReasons = append(blockedReasons, "repository_role_is_not_config")
	}
	if len(remotes) == 0 {
		blockedReasons = append(blockedReasons, "config_remote_missing")
	}
	scaffoldState := "ready"
	if len(blockedReasons) > 0 {
		scaffoldState = "blocked"
	}
	var versions []map[string]any
	if len(optionalRows) > 0 {
		versions = optionalRows[0]
	}
	var workflowOperations []map[string]any
	if len(optionalRows) > 1 {
		workflowOperations = optionalRows[1]
	}
	pinEvidence := configRepositoryProjectVersionPinEvidence(repo, remoteSummaries, versions)
	workflowEvidence := configRepositoryGitWorkflowAuditEvidence(workflowOperations)
	commitPlan := configRepositoryGitCommitPlan(repo, files, remoteSummaries, blockedReasons, pinEvidence, workflowEvidence)
	return map[string]any{
		"mode":                         "config_repository_scaffold_preview",
		"scaffold_state":               scaffoldState,
		"repository_id":                repo["id"],
		"repository_name":              stringFromMap(repo, "name"),
		"repo_key":                     stringFromMap(repo, "repo_key"),
		"repo_role":                    stringFromMap(repo, "repo_role"),
		"default_branch":               stringFromMap(repo, "default_branch"),
		"environments":                 environments,
		"files":                        files,
		"file_count":                   len(files),
		"remote_count":                 len(remotes),
		"remotes":                      remoteSummaries,
		"required_controls":            []string{"config_remote_review", "branch_policy_review", "human_file_review", "project_version_config_commit_pin"},
		"blocked_reasons":              blockedReasons,
		"git_write_performed":          false,
		"external_call_made":           false,
		"file_content_included":        false,
		"secret_included":              false,
		"project_version_pin_evidence": pinEvidence,
		"git_workflow_audit_evidence":  workflowEvidence,
		"live_commit_validation":       "not_performed",
		"live_commit_validation_state": pinEvidence["live_validation_state"],
		"git_commit_plan":              commitPlan,
		"next_step":                    "Create or sync the config remote, commit the scaffold files through a reviewed Git workflow, then pin config_commit_sha in ProjectVersion.",
		"suppressed_fields":            []string{"file_content", "secret_values", "git_credentials", "provider_token", "author_email"},
	}
}

func configRepositoryGitWorkflowAuditEvidence(operations []map[string]any) map[string]any {
	items := make([]map[string]any, 0, len(operations))
	statusCounts := map[string]int{}
	logCount := 0
	for _, operation := range operations {
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(operation["status"])))
		if status == "" || status == "<nil>" {
			status = "unknown"
		}
		statusCounts[status]++
		rowLogCount := intFromAny(operation["operation_log_count"], 0)
		logCount += rowLogCount
		items = append(items, map[string]any{
			"operation_run_id":                  cleanOptionalID(fmt.Sprint(operation["id"])),
			"status":                            status,
			"created_at":                        operation["created_at"],
			"updated_at":                        operation["updated_at"],
			"started_at":                        operation["started_at"],
			"finished_at":                       operation["finished_at"],
			"operation_log_count":               rowLogCount,
			"result_scope":                      "sanitized_config_git_workflow_intent",
			"git_write_performed":               false,
			"git_commit_created":                false,
			"git_push_performed":                false,
			"external_call_made":                false,
			"file_content_included":             false,
			"secret_included":                   false,
			"raw_git_output_recorded":           false,
			"raw_provider_response_recorded":    false,
			"project_version_pin_written":       false,
			"live_commit_validation_performed":  false,
			"project_version_pin_write_allowed": false,
		})
	}
	queued := statusCounts["queued"] + statusCounts["pending"]
	running := statusCounts["running"]
	completed := statusCounts["completed"] + statusCounts["succeeded"] + statusCounts["success"]
	failed := statusCounts["failed"] + statusCounts["error"]
	canceled := statusCounts["canceled"] + statusCounts["cancelled"]
	active := queued + running
	known := queued + running + completed + failed + canceled
	unknown := len(operations) - known
	if unknown < 0 {
		unknown = 0
	}
	state := "not_requested"
	switch {
	case len(operations) == 0:
		state = "not_requested"
	case active > 0:
		state = "waiting_for_worker"
	case failed > 0 && canceled > 0:
		state = "mixed_failed"
	case failed > 0:
		state = "failed"
	case canceled > 0:
		state = "canceled"
	case unknown > 0:
		state = "unknown"
	default:
		state = "recorded"
	}
	sanitizedRecorded := len(operations) > 0 && active == 0
	return map[string]any{
		"mode":                                      "config_repository_git_workflow_audit_evidence",
		"evidence_state":                            state,
		"has_audit_operations":                      len(operations) > 0,
		"operation_count":                           len(operations),
		"active_count":                              active,
		"queued_count":                              queued,
		"running_count":                             running,
		"completed_count":                           completed,
		"failed_count":                              failed,
		"canceled_count":                            canceled,
		"unknown_count":                             unknown,
		"operation_log_count":                       logCount,
		"sanitized_result_recorded":                 sanitizedRecorded,
		"has_failures":                              failed > 0,
		"has_cancellations":                         canceled > 0,
		"has_unknown_status":                        unknown > 0,
		"items":                                     items,
		"git_write_performed":                       false,
		"git_commit_created":                        false,
		"git_push_performed":                        false,
		"external_call_made":                        false,
		"file_content_included":                     false,
		"secret_included":                           false,
		"raw_git_output_recorded":                   false,
		"raw_provider_response_recorded":            false,
		"project_version_pin_written":               false,
		"live_commit_validation_performed":          false,
		"live_remote_commit_validation_performed":   false,
		"operation_result_contains_raw_git_output":  false,
		"operation_result_contains_provider_body":   false,
		"operation_result_contains_file_content":    false,
		"operation_result_contains_secret_material": false,
		"suppressed_fields": []string{
			"file_content",
			"secret_values",
			"git_credentials",
			"provider_token",
			"remote_url",
			"branch_name",
			"commit_message",
			"commit_sha",
			"git_output",
			"provider_response_body",
			"provider_response_headers",
		},
	}
}

func configRepositoryGitCommitPlan(repo map[string]any, files, remotes []map[string]any, scaffoldBlockedReasons []string, pinEvidence, workflowEvidence map[string]any) map[string]any {
	planState := "planned"
	blockedReasons := append([]string{}, scaffoldBlockedReasons...)
	defaultBranch := strings.TrimSpace(stringFromMap(repo, "default_branch"))
	if defaultBranch == "" {
		blockedReasons = append(blockedReasons, "default_branch_missing")
	}
	if len(files) == 0 {
		blockedReasons = append(blockedReasons, "scaffold_files_missing")
	}
	if len(blockedReasons) > 0 {
		planState = "blocked"
	}
	approvalPlan := configRepositoryGitCommitApprovalPlan(planState, blockedReasons)
	workspacePlan := configRepositoryGitCommitWorkspacePlan(len(files), len(remotes), defaultBranch != "")
	remoteReviewPlan := configRepositoryRemoteReviewPlan(planState, len(remotes), defaultBranch != "")
	pinValidationPlan := configRepositoryProjectVersionPinValidationPlan(defaultBranch != "", len(remotes) > 0, pinEvidence)
	promotionReadinessPlan := configRepositoryGitCommitPromotionReadinessPlan(pinEvidence, workflowEvidence)
	pinObserved := boolOnlyFromAny(pinEvidence["config_commit_sha_recorded"])
	liveValidationObserved := boolOnlyFromAny(pinEvidence["live_validation_recorded"])
	steps := []map[string]any{
		{
			"kind":   "scaffold_review",
			"status": statusWhen(len(files) > 0 && !stringListContains(blockedReasons, "repository_role_is_not_config")),
			"checks": []string{"repository_role", "scaffold_paths", "human_file_review"},
			"reason": reasonWhen(len(files) > 0 && !stringListContains(blockedReasons, "repository_role_is_not_config"), "config scaffold paths are ready for human review", "config repository scaffold is not ready"),
		},
		{
			"kind":   "remote_binding",
			"status": statusWhen(len(remotes) > 0),
			"checks": []string{"git_remote", "provider_type", "branch_policy"},
			"reason": reasonWhen(len(remotes) > 0, "at least one config remote is available for future Git workflow", "config remote is required before commit rehearsal"),
		},
		{
			"kind":   "workspace_checkout",
			"status": "blocked",
			"checks": []string{"clone_or_fetch", "clean_worktree", "credential_binding"},
			"reason": "Git checkout/fetch is not performed by this preview",
		},
		{
			"kind":   "review_branch",
			"status": statusWhen(defaultBranch != ""),
			"checks": []string{"default_branch", "review_branch_policy", "protected_branch_avoidance"},
			"reason": reasonWhen(defaultBranch != "", "review branch can be derived after branch policy review", "default branch metadata is required"),
		},
		{
			"kind":   "scaffold_commit",
			"status": "blocked",
			"checks": []string{"file_materialization", "secret_scan", "commit_author_policy"},
			"reason": "File content materialization and git commit are disabled in this preview",
		},
		{
			"kind":   "remote_push",
			"status": "blocked",
			"checks": []string{"git_push", "provider_protection", "review_request"},
			"reason": "Git push and PR/MR creation require a future approval-gated provider workflow",
		},
		{
			"kind":   "project_version_pin",
			"status": statusWhen(pinObserved),
			"checks": []string{"config_commit_sha", "ProjectVersion.metadata.repositories[].config_commit_sha"},
			"reason": reasonWhen(pinObserved, "ProjectVersion config_commit_sha metadata is already recorded for this config repository", "ProjectVersion config commit pin is not written by this preview"),
		},
		{
			"kind":   "live_commit_validation",
			"status": statusWhen(liveValidationObserved),
			"checks": []string{"git_fetch", "remote_commit_lookup", "synced_state_validation"},
			"reason": reasonWhen(liveValidationObserved, "config_commit_sha matches synced remote latest_sha without performing Git fetch", "Live commit validation is not performed by this preview"),
		},
	}
	return map[string]any{
		"mode":                              "config_repository_git_commit_plan_preview",
		"plan_state":                        planState,
		"execution_enabled":                 planState == "planned",
		"execution_mode":                    "approval_gated_audit_only",
		"operation_request_enabled":         planState == "planned",
		"external_call_made":                false,
		"git_clone_performed":               false,
		"git_fetch_performed":               false,
		"git_commit_created":                false,
		"git_push_performed":                false,
		"pull_request_created":              false,
		"project_version_pin_written":       false,
		"live_commit_validation_performed":  false,
		"project_version_pin_observed":      pinObserved,
		"live_commit_validation_observed":   liveValidationObserved,
		"audit_operation_observed":          boolOnlyFromAny(workflowEvidence["has_audit_operations"]),
		"sanitized_result_observed":         boolOnlyFromAny(workflowEvidence["sanitized_result_recorded"]),
		"file_content_materialized":         false,
		"secret_scan_performed":             false,
		"credential_bound":                  false,
		"scaffold_file_count":               len(files),
		"remote_count":                      len(remotes),
		"default_branch_configured":         defaultBranch != "",
		"required_controls":                 []string{"config_remote_review", "branch_policy_review", "human_file_review", "secret_scan", "commit_author_policy", "provider_review_workflow", "project_version_config_commit_pin", "live_remote_commit_validation"},
		"disabled_backends":                 []string{"git_clone", "git_fetch", "file_write", "git_commit", "git_push", "pull_request_create", "project_version_update", "live_commit_validation"},
		"enabled_backends":                  []string{"operation_run_enqueue", "worker_job_enqueue", "sanitized_audit_result_recording"},
		"blocked_reasons":                   blockedReasons,
		"suppressed_fields":                 []string{"file_content", "secret_values", "git_credentials", "provider_token", "author_email", "remote_url", "branch_name", "commit_message"},
		"steps":                             steps,
		"execution_sequence":                []string{"review_scaffold", "bind_config_remote", "checkout_clean_workspace", "create_review_branch", "materialize_files", "run_secret_scan", "commit_scaffold", "push_review_branch", "open_review_request", "pin_config_commit_sha", "validate_remote_commit"},
		"required_project_version_metadata": []string{"repositories[].repo_key", "repositories[].remote_id", "repositories[].config_commit_sha"},
		"approval_request_plan":             approvalPlan,
		"workspace_execution_plan":          workspacePlan,
		"remote_review_plan":                remoteReviewPlan,
		"project_version_pin_plan":          pinValidationPlan,
		"git_workflow_audit_evidence":       workflowEvidence,
		"promotion_readiness_plan":          promotionReadinessPlan,
		"result_recording_plan":             configRepositoryGitCommitResultRecordingPlan(pinEvidence, workflowEvidence),
		"message":                           "Config repository Git workflow can now enqueue an approval-gated audit job; file materialization, Git commit/push, provider requests, ProjectVersion pin writes, and live validation remain disabled.",
	}
}

func configRepositoryGitCommitApprovalPlan(planState string, blockedReasons []string) map[string]any {
	metadataReady := planState == "planned"
	metadataBlockedReasons := append([]string{}, blockedReasons...)
	requestReadyReason := "config_git_commit_metadata_ready"
	if !metadataReady {
		requestReadyReason = "config_git_commit_metadata_blocked"
	}
	return map[string]any{
		"mode":                     "config_repository_git_commit_approval_plan",
		"request_state":            planState,
		"request_ready":            metadataReady,
		"request_ready_reason":     requestReadyReason,
		"metadata_ready":           metadataReady,
		"operation_created":        false,
		"approval_request_created": false,
		"worker_job_created":       false,
		"external_call_made":       false,
		"required_approval_fields": []string{"operation_run_id", "repository_id", "remote_id", "default_branch", "scaffold_file_count", "requested_by", "reason"},
		"suppressed_fields":        []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "author_email"},
		"blocked_reasons":          metadataBlockedReasons,
		"execution_blockers":       []string{"git_workspace_backend_disabled", "git_commit_not_created", "provider_review_workflow_not_wired", "project_version_pin_write_disabled"},
		"required_operator_action": "Request approval for a config Git workflow audit job before any future checkout, file materialization, commit, push, ProjectVersion pin, or live validation backend is armed.",
	}
}

func configRepositoryGitWorkflowInput(repo map[string]any, remotes []map[string]any, preview map[string]any) map[string]any {
	defaultBranch := cleanOptionalText(stringFromMap(repo, "default_branch"))
	remoteID := ""
	remoteProvider := ""
	if len(remotes) > 0 {
		remoteID = cleanOptionalID(fmt.Sprint(remotes[0]["id"]))
		remoteProvider = cleanOptionalText(stringFromMap(remotes[0], "provider_type"))
		if defaultBranch == "" {
			defaultBranch = cleanOptionalText(stringFromMap(remotes[0], "default_branch"))
		}
	}
	return map[string]any{
		"project_git_repository_id": cleanOptionalID(fmt.Sprint(repo["id"])),
		"config_remote_id":          remoteID,
		"provider_type":             remoteProvider,
		"default_branch_configured": defaultBranch != "",
		"scaffold_file_count":       preview["file_count"],
		"remote_count":              preview["remote_count"],
		"mode":                      "approval_gated_audit_only",
		"file_content_included":     false,
		"secret_included":           false,
		"external_call_made":        false,
		"git_write_performed":       false,
	}
}

func enqueueConfigRepositoryGitWorkflowTx(ctx context.Context, tx *sqlx.Tx, projectID string, repo map[string]any, remotes []map[string]any, preview map[string]any, actorID string) (map[string]any, error) {
	input := configRepositoryGitWorkflowInput(repo, remotes, preview)
	input["actor_user_id"] = cleanOptionalID(actorID)
	title := "config git workflow " + cleanOptionalText(stringFromMap(repo, "name"))
	if strings.TrimSpace(title) == "config git workflow" {
		title = "config git workflow"
	}
	op, err := enqueueOperationTx(ctx, tx, projectID, cleanOptionalID(stringFromMap(input, "config_remote_id")), "config.git_commit", title, input, []string{"git", "config"}, "control-worker")
	if err != nil {
		return nil, err
	}
	return op, nil
}

func configRepositoryGitWorkflowRequestResult(op map[string]any) map[string]any {
	return map[string]any{
		"mode":                         "config_repository_git_workflow_request_result",
		"operation_run_id":             op["id"],
		"operation_type":               "config.git_commit",
		"operation_created":            true,
		"worker_job_created":           true,
		"approval_gated":               true,
		"git_write_performed":          false,
		"external_call_made":           false,
		"file_content_included":        false,
		"secret_included":              false,
		"project_version_pin_written":  false,
		"live_commit_validation":       "disabled",
		"sanitized_result_expected":    true,
		"required_worker_capabilities": []string{"git", "config"},
		"suppressed_fields":            []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha"},
	}
}

func configRepositoryGitCommitWorkspacePlan(fileCount, remoteCount int, defaultBranchConfigured bool) map[string]any {
	metadataReady := fileCount > 0 && remoteCount > 0 && defaultBranchConfigured
	blockedReasons := []string{"git_workspace_backend_disabled", "secret_scan_not_performed", "git_commit_not_created", "provider_review_not_created"}
	if fileCount == 0 {
		blockedReasons = append(blockedReasons, "scaffold_files_missing")
	}
	if remoteCount == 0 {
		blockedReasons = append(blockedReasons, "config_remote_missing")
	}
	if !defaultBranchConfigured {
		blockedReasons = append(blockedReasons, "default_branch_missing")
	}
	return map[string]any{
		"mode":                      "config_repository_git_workspace_plan",
		"workspace_state":           "blocked",
		"workspace_ready":           false,
		"workspace_ready_reason":    "config_git_workspace_backend_disabled",
		"metadata_ready":            metadataReady,
		"workspace_bound":           false,
		"git_clone_performed":       false,
		"file_content_materialized": false,
		"secret_scan_performed":     false,
		"git_commit_created":        false,
		"git_push_performed":        false,
		"provider_review_created":   false,
		"external_call_made":        false,
		"contains_file_content":     false,
		"contains_secret_values":    false,
		"required_workspace_fields": []string{"operation_run_id", "repository_id", "remote_id", "workspace_id", "scaffold_file_count", "secret_scan_status", "commit_author"},
		"suppressed_fields":         []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "author_email"},
		"blocked_reasons":           blockedReasons,
		"execution_sequence":        []string{"bind_clean_workspace", "materialize_scaffold_files", "run_secret_scan", "create_review_branch", "commit_scaffold", "push_review_branch", "open_provider_review"},
		"message":                   "Config Git workspace execution is preview-only; no workspace, file content, Git ref, commit, push, or provider review is created.",
	}
}

func configRepositoryRemoteReviewPlan(planState string, remoteCount int, defaultBranchConfigured bool) map[string]any {
	metadataReady := planState == "planned" && remoteCount > 0 && defaultBranchConfigured
	reviewState := "blocked"
	if metadataReady {
		reviewState = "planned"
	}
	blockedReasons := []string{"git_push_not_performed", "provider_review_workflow_not_wired", "provider_review_not_created"}
	if remoteCount == 0 {
		blockedReasons = append(blockedReasons, "config_remote_missing")
	}
	if !defaultBranchConfigured {
		blockedReasons = append(blockedReasons, "default_branch_missing")
	}
	return map[string]any{
		"mode":                             "config_repository_remote_review_plan",
		"review_state":                     reviewState,
		"metadata_ready":                   metadataReady,
		"review_branch_ready":              metadataReady,
		"protected_default_branch_avoided": true,
		"git_push_performed":               false,
		"review_branch_pushed":             false,
		"provider_review_created":          false,
		"provider_review_link_recorded":    false,
		"external_call_made":               false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_branch_name":             false,
		"contains_commit_message":          false,
		"contains_provider_response":       false,
		"required_review_fields":           []string{"operation_run_id", "repository_id", "remote_id", "review_branch_key", "base_branch_key", "commit_sha_status", "provider_review_status"},
		"required_controls":                []string{"branch_policy_review", "protected_branch_avoidance", "provider_review_workflow", "provider_response_redaction", "operator_review_before_merge"},
		"execution_sequence":               []string{"derive_review_branch", "push_review_branch", "open_provider_review_request", "record_review_request_summary", "wait_for_operator_merge"},
		"disabled_backends":                []string{"git_push", "pull_request_create", "merge_request_create", "provider_review_link_write", "provider_response_recording"},
		"suppressed_fields":                []string{"remote_url", "branch_name", "commit_message", "commit_sha", "git_credentials", "provider_token", "authorization_header", "provider_response_body", "provider_response_headers"},
		"blocked_reasons":                  blockedReasons,
		"execution_blockers":               []string{"git_push_not_performed", "provider_review_workflow_not_wired"},
		"message":                          "Config remote push and provider review creation are planned only; no review branch, provider request, URL, response, or branch name is persisted.",
	}
}

func configRepositoryProjectVersionPinEvidence(repo map[string]any, remotes, versions []map[string]any) map[string]any {
	// This function receives raw ProjectVersion metadata. Return only redacted
	// evidence; never include the original metadata map or raw config_commit_sha.
	repoKey := strings.TrimSpace(stringFromMap(repo, "repo_key"))
	repoID := strings.TrimSpace(fmt.Sprint(repo["id"]))
	remoteByID := map[string]map[string]any{}
	for _, remote := range remotes {
		remoteID := strings.TrimSpace(fmt.Sprint(remote["id"]))
		if remoteID != "" && remoteID != "<nil>" {
			remoteByID[remoteID] = remote
		}
	}
	pinned, validated, mismatched := 0, 0, 0
	items := []map[string]any{}
	for _, version := range versions {
		metadata := mapFromAny(version["metadata"])
		for _, manifest := range mapSliceFromAny(metadata["repositories"]) {
			configSHA := strings.TrimSpace(stringFromMap(manifest, "config_commit_sha"))
			if configSHA == "" {
				continue
			}
			manifestRepoKey := strings.TrimSpace(stringFromMap(manifest, "repo_key"))
			manifestRepoID := strings.TrimSpace(stringFromMap(manifest, "repository_id"))
			manifestRole := strings.TrimSpace(stringFromMap(manifest, "repo_role"))
			_ = manifestRole
			repositoryMatches := (manifestRepoKey != "" && manifestRepoKey == repoKey) || (manifestRepoID != "" && manifestRepoID == repoID)
			if !repositoryMatches {
				continue
			}
			remoteID := strings.TrimSpace(stringFromMap(manifest, "remote_id"))
			remote := remoteByID[remoteID]
			latestSHA := ""
			if remote != nil {
				latestSHA = strings.TrimSpace(stringFromMap(remote, "latest_sha"))
			}
			validationStatus := "not_observed"
			if latestSHA != "" && strings.EqualFold(latestSHA, configSHA) {
				validationStatus = "validated"
				validated++
			} else if latestSHA != "" {
				validationStatus = "mismatched"
				mismatched++
			}
			pinned++
			items = append(items, map[string]any{
				"project_version_id":        version["id"],
				"version":                   version["version"],
				"repo_key":                  manifestRepoKey,
				"repo_role":                 manifestRole,
				"remote_id":                 remoteID,
				"config_commit_sha_present": true,
				"remote_latest_sha_present": latestSHA != "",
				"validation_status":         validationStatus,
				"commit_sha_included":       false,
				"remote_url_included":       false,
				"secret_included":           false,
			})
		}
	}
	pinState := "not_recorded"
	if pinned > 0 {
		pinState = "recorded"
	}
	liveState := "not_recorded"
	if validated > 0 {
		liveState = "recorded"
	} else if mismatched > 0 {
		liveState = "mismatched"
	} else if pinned > 0 {
		liveState = "waiting_for_synced_remote"
	}
	return map[string]any{
		"mode":                       "config_repository_project_version_pin_evidence",
		"project_version_count":      len(versions),
		"pinned_version_count":       pinned,
		"validated_version_count":    validated,
		"mismatched_version_count":   mismatched,
		"config_commit_sha_recorded": pinned > 0,
		"live_validation_recorded":   validated > 0,
		"pin_state":                  pinState,
		"live_validation_state":      liveState,
		"items":                      items,
		"external_call_made":         false,
		"git_fetch_performed":        false,
		"commit_sha_included":        false,
		"remote_url_included":        false,
		"secret_included":            false,
		"suppressed_fields":          []string{"config_commit_sha", "remote_url", "git_credentials", "provider_token", "authorization_header", "provider_response_body"},
	}
}

func configRepositoryProjectVersionPinValidationPlan(defaultBranchConfigured, remoteConfigured bool, evidence map[string]any) map[string]any {
	metadataReady := defaultBranchConfigured && remoteConfigured
	blockedReasons := []string{"project_version_pin_write_disabled", "live_remote_commit_validation_not_performed"}
	if !remoteConfigured {
		blockedReasons = append(blockedReasons, "config_remote_missing")
	}
	if !defaultBranchConfigured {
		blockedReasons = append(blockedReasons, "default_branch_missing")
	}
	pinObserved := boolOnlyFromAny(evidence["config_commit_sha_recorded"])
	liveObserved := boolOnlyFromAny(evidence["live_validation_recorded"])
	pinState := "blocked"
	if pinObserved {
		pinState = "observed"
	}
	pinReadyReason := "config_commit_sha_pin_write_disabled"
	if pinObserved {
		pinReadyReason = "config_commit_sha_observed_in_project_version_metadata"
	}
	pinWritePreflightPlan := configRepositoryProjectVersionPinWritePreflightPlan(metadataReady, pinObserved, liveObserved, evidence, blockedReasons)
	return map[string]any{
		"mode":                            "config_repository_project_version_pin_validation_plan",
		"pin_state":                       pinState,
		"pin_ready":                       pinObserved,
		"pin_ready_reason":                pinReadyReason,
		"metadata_ready":                  metadataReady,
		"project_version_pin_written":     false,
		"project_version_pin_observed":    pinObserved,
		"config_commit_sha_recorded":      pinObserved,
		"live_commit_validation_started":  false,
		"live_commit_validation_recorded": liveObserved,
		"git_fetch_performed":             false,
		"external_call_made":              false,
		"contains_commit_sha":             false,
		"contains_remote_url":             false,
		"pin_evidence":                    evidence,
		"pin_write_preflight_plan":        pinWritePreflightPlan,
		"required_pin_fields":             []string{"project_version_id", "repository_id", "remote_id", "repo_key", "config_commit_sha", "validation_status"},
		"suppressed_fields":               []string{"remote_url", "branch_name", "commit_message", "commit_sha", "git_credentials", "provider_token", "provider_response_body"},
		"blocked_reasons":                 blockedReasons,
		"message":                         "ProjectVersion config_commit_sha pinning and live remote validation are not performed by this preview.",
	}
}

func configRepositoryProjectVersionPinWritePreflightPlan(metadataReady, pinObserved, liveObserved bool, evidence map[string]any, parentBlockedReasons []string) map[string]any {
	preflightState := "blocked"
	if metadataReady && !pinObserved {
		preflightState = "metadata_review_ready"
	}
	if pinObserved {
		preflightState = "observed"
	}
	blockedReasons := []string{"project_version_pin_write_disabled", "live_remote_commit_validation_not_performed"}
	if !metadataReady {
		blockedReasons = append(blockedReasons, parentBlockedReasons...)
	}
	if pinObserved {
		blockedReasons = []string{"project_version_pin_write_disabled"}
	}
	return map[string]any{
		"mode":                             "config_repository_project_version_pin_write_preflight_plan",
		"preflight_state":                  preflightState,
		"pin_write_ready_for_review":       metadataReady && !pinObserved,
		"metadata_ready":                   metadataReady,
		"project_version_pin_observed":     pinObserved,
		"live_commit_validation_observed":  liveObserved,
		"project_version_pin_written":      false,
		"project_version_update_enabled":   false,
		"project_version_metadata_written": false,
		"live_commit_validation_started":   false,
		"live_remote_validation_performed": false,
		"git_fetch_performed":              false,
		"external_call_made":               false,
		"contains_commit_sha":              false,
		"contains_remote_url":              false,
		"contains_git_credentials":         false,
		"contains_provider_token":          false,
		"pinned_version_count":             intFromAny(evidence["pinned_version_count"], 0),
		"validated_version_count":          intFromAny(evidence["validated_version_count"], 0),
		"mismatched_version_count":         intFromAny(evidence["mismatched_version_count"], 0),
		"required_write_fields":            []string{"project_version_id", "repository_id", "remote_id", "repo_key", "config_commit_sha", "pin_source_operation_run_id", "validation_status", "reviewed_by"},
		"required_controls":                []string{"operator_review", "config_commit_sha_source_review", "project_version_metadata_schema_review", "live_remote_validation_review", "redacted_pin_result_recording"},
		"disabled_backends":                []string{"project_version_update", "live_commit_validation", "git_fetch", "remote_commit_lookup", "operation_log_write"},
		"suppressed_fields":                []string{"config_commit_sha", "remote_url", "branch_name", "commit_message", "git_credentials", "provider_token", "authorization_header", "provider_response_body", "provider_response_headers", "operator_identity"},
		"blocked_reasons":                  blockedReasons,
		"message":                          "ProjectVersion config_commit_sha pin write preflight is metadata-only; no ProjectVersion metadata, operation log, Git fetch, remote validation, URL, credential, or commit SHA is written.",
	}
}

func configRepositoryGitCommitResultRecordingPlan(evidence map[string]any, workflowEvidence map[string]any) map[string]any {
	pinObserved := boolOnlyFromAny(evidence["config_commit_sha_recorded"])
	liveObserved := boolOnlyFromAny(evidence["live_validation_recorded"])
	workflowObserved := boolOnlyFromAny(workflowEvidence["has_audit_operations"])
	workflowRecorded := boolOnlyFromAny(workflowEvidence["sanitized_result_recorded"])
	workflowState := strings.TrimSpace(fmt.Sprint(workflowEvidence["evidence_state"]))
	recordingState := "blocked"
	recordingReason := "config_git_commit_execution_not_performed"
	if workflowState == "waiting_for_worker" {
		recordingState = "waiting_for_worker"
		recordingReason = "config_git_commit_audit_operation_waiting_for_worker"
	} else if workflowState == "failed" || workflowState == "mixed_failed" {
		recordingState = "failed"
		recordingReason = "config_git_commit_audit_operation_failed"
	} else if workflowState == "canceled" {
		recordingState = "canceled"
		recordingReason = "config_git_commit_audit_operation_canceled"
	} else if pinObserved && liveObserved && workflowRecorded {
		recordingState = "recorded"
		recordingReason = "audit_result_pin_and_live_validation_observed"
	} else if pinObserved && liveObserved {
		recordingState = "recorded"
		recordingReason = "project_version_pin_and_live_validation_observed"
	} else if workflowRecorded {
		recordingState = "audit_recorded"
		recordingReason = "sanitized_config_git_workflow_audit_result_observed"
	} else if pinObserved {
		recordingState = "partial"
		recordingReason = "project_version_config_commit_pin_observed"
	}
	resultWritten := pinObserved || workflowRecorded
	operationLogWritten := intFromAny(workflowEvidence["operation_log_count"], 0) > 0
	return map[string]any{
		"mode":                             "config_repository_git_commit_result_recording_plan",
		"result_recording_state":           recordingState,
		"result_recording_ready":           resultWritten,
		"result_recording_ready_reason":    recordingReason,
		"recording_enabled":                resultWritten,
		"result_written":                   resultWritten,
		"operation_log_written":            operationLogWritten,
		"scaffold_artifact_recorded":       false,
		"commit_record_written":            false,
		"push_record_written":              false,
		"review_request_recorded":          false,
		"remote_review_subplan_recorded":   false,
		"project_version_pin_written":      false,
		"project_version_pin_observed":     pinObserved,
		"config_commit_sha_recorded":       pinObserved,
		"live_validation_recorded":         liveObserved,
		"audit_operation_observed":         workflowObserved,
		"sanitized_audit_result_recorded":  workflowRecorded,
		"pin_evidence":                     evidence,
		"git_workflow_audit_evidence":      workflowEvidence,
		"promotion_readiness_plan":         configRepositoryGitCommitPromotionReadinessPlan(evidence, workflowEvidence),
		"raw_file_content_recorded":        false,
		"raw_secret_value_recorded":        false,
		"raw_git_output_recorded":          false,
		"raw_provider_response_recorded":   false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_branch_name":             false,
		"contains_commit_message":          false,
		"requires_secret_scan_result":      true,
		"requires_human_result_review":     true,
		"requires_project_version_context": true,
		"result_recording_sequence": []string{
			"classify_git_workflow_result",
			"record_sanitized_scaffold_summary",
			"record_commit_push_review_summary",
			"stage_project_version_config_commit_pin",
			"record_live_validation_summary",
			"persist_redacted_operation_result",
		},
		"result_diagnostic_fields": []string{
			"scaffold_file_count",
			"secret_scan_status",
			"commit_created",
			"push_performed",
			"review_request_created",
			"remote_review_state",
			"config_commit_sha_present",
			"live_validation_status",
			"git_workflow_audit_status",
			"operation_log_count",
		},
		"result_persisted_fields": []string{
			"operation_status",
			"scaffold_file_count",
			"secret_scan_status",
			"review_request_status",
			"project_version_pin_status",
			"live_validation_status",
			"sanitized_audit_result_status",
		},
		"suppressed_fields": []string{
			"file_content",
			"secret_values",
			"git_credentials",
			"provider_token",
			"remote_url",
			"branch_name",
			"commit_message",
			"commit_sha",
			"provider_response_body",
			"provider_response_headers",
		},
		"blocked_reasons": []string{
			"config_git_commit_execution_not_performed",
			"project_version_config_commit_pin_not_written",
			"live_remote_commit_validation_not_performed",
		},
		"message": "Config Git workflow result recording only reconciles sanitized audit operation metadata; no scaffold artifact, Git result, provider review, ProjectVersion pin write, or live validation record is persisted.",
	}
}

func configRepositoryGitCommitPromotionReadinessPlan(evidence map[string]any, workflowEvidence map[string]any) map[string]any {
	pinObserved := boolOnlyFromAny(evidence["config_commit_sha_recorded"])
	liveObserved := boolOnlyFromAny(evidence["live_validation_recorded"])
	workflowObserved := boolOnlyFromAny(workflowEvidence["has_audit_operations"])
	workflowRecorded := boolOnlyFromAny(workflowEvidence["sanitized_result_recorded"])
	workflowState := strings.TrimSpace(fmt.Sprint(workflowEvidence["evidence_state"]))
	promotionState := "blocked"
	promotionReason := "config_git_commit_audit_result_not_recorded"
	switch {
	case workflowState == "waiting_for_worker":
		promotionState = "waiting_for_worker"
		promotionReason = "config_git_commit_audit_operation_waiting_for_worker"
	case workflowState == "failed" || workflowState == "mixed_failed":
		promotionState = "failed"
		promotionReason = "config_git_commit_audit_operation_failed"
	case workflowState == "canceled":
		promotionState = "canceled"
		promotionReason = "config_git_commit_audit_operation_canceled"
	case workflowRecorded && pinObserved && liveObserved:
		promotionState = "ready_for_live_workflow_review"
		promotionReason = "audit_result_pin_and_live_validation_ready_for_operator_review"
	case workflowRecorded:
		promotionState = "audit_result_ready_for_review"
		promotionReason = "sanitized_audit_result_ready_for_operator_review"
	case pinObserved || liveObserved:
		promotionState = "partial_evidence"
		promotionReason = "project_version_pin_or_live_validation_observed_without_audit_result"
	}
	promotionReady := promotionState == "ready_for_live_workflow_review" || promotionState == "audit_result_ready_for_review"
	return map[string]any{
		"mode":                             "config_repository_audit_to_live_promotion_readiness_plan",
		"promotion_state":                  promotionState,
		"promotion_ready":                  promotionReady,
		"promotion_ready_reason":           promotionReason,
		"audit_operation_observed":         workflowObserved,
		"sanitized_audit_result_recorded":  workflowRecorded,
		"project_version_pin_observed":     pinObserved,
		"live_commit_validation_observed":  liveObserved,
		"live_git_workflow_enabled":        false,
		"live_git_commit_enabled":          false,
		"git_workspace_mutation_enabled":   false,
		"git_commit_created":               false,
		"git_push_performed":               false,
		"provider_review_created":          false,
		"project_version_pin_written":      false,
		"live_remote_validation_performed": false,
		"external_call_made":               false,
		"contains_file_content":            false,
		"contains_remote_url":              false,
		"contains_credentials":             false,
		"contains_commit_sha":              false,
		"contains_branch_name":             false,
		"contains_git_output":              false,
		"contains_provider_response":       false,
		"required_controls":                []string{"operator_review", "git_workspace_backend", "secret_scan_backend", "git_commit_backend", "git_push_backend", "provider_review_backend", "project_version_pin_write_backend", "live_remote_validation_backend"},
		"disabled_backends":                []string{"git_workspace_mutation", "secret_scan", "git_commit", "git_push", "provider_review", "project_version_pin_write", "live_remote_validation"},
		"promotion_blockers":               []string{"git_workspace_backend_disabled", "secret_scan_not_performed", "git_commit_not_created", "git_push_not_performed", "provider_review_workflow_not_wired", "project_version_pin_write_disabled", "live_remote_commit_validation_not_performed"},
		"promotion_sequence":               []string{"operator_review_sanitized_audit_result", "materialize_scaffold_in_clean_workspace", "run_secret_scan", "commit_config_scaffold", "push_review_branch", "open_provider_review", "pin_project_version_config_commit_sha", "validate_live_remote_commit", "record_redacted_live_result"},
		"suppressed_fields":                []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "git_output", "provider_response_body", "provider_response_headers"},
		"message":                          "Sanitized audit evidence can only be reviewed for future promotion; this preview still performs no Git mutation, provider request, ProjectVersion pin write, or live validation.",
	}
}

func stringListContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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
	runs = repoTagRunsWithRemoteRehearsal(runs)
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
	thresholds, err := queryWebhookThresholdConfigurationOverrides(ctx, s.store.DB, sourceID, "7d")
	if err != nil {
		return nil, err
	}
	return repoSyncCapacitySignalsWithThresholds(asset, raw, sourceID, targetID, thresholds), nil
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
	return repoSyncCapacitySignalsWithThresholds(asset, raw, sourceID, targetID, nil)
}

func repoSyncCapacitySignalsWithThresholds(asset, raw map[string]any, sourceID, targetID string, thresholds map[string]webhookThresholdConfiguration) []map[string]any {
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
	activeThreshold := thresholdForKey(thresholds, "sync_capacity_active", repoSyncCapacityActiveWarningThreshold, repoSyncCapacityActiveDangerThreshold, "active_runs")
	signals = append(signals, map[string]any{
		"name":                            "sync capacity",
		"status":                          activeRuns,
		"severity":                        severityForCount(activeRuns, activeThreshold.WarningAt, activeThreshold.DangerAt),
		"threshold":                       thresholdDetail(activeThreshold.WarningAt, activeThreshold.DangerAt, humanThresholdUnit(activeThreshold.Unit)),
		"threshold_key":                   "sync_capacity_active",
		"threshold_source":                activeThreshold.Source,
		"threshold_configuration_applied": activeThreshold.Applied,
		"capacity_signals_recomputed":     activeThreshold.Applied,
		"detail":                          fmt.Sprintf("%d queued or running sync runs", activeRuns),
	})
	failedRuns := intFromAny(raw["failed_runs_7d"], 0)
	failureThreshold := thresholdForKey(thresholds, "sync_failure_7d", repoSyncCapacityFailure7dWarningThreshold, repoSyncCapacityFailure7dDangerThreshold, "failures")
	signals = append(signals, map[string]any{
		"name":                            "7d sync failures",
		"status":                          failedRuns,
		"severity":                        severityForCount(failedRuns, failureThreshold.WarningAt, failureThreshold.DangerAt),
		"threshold":                       thresholdDetail(failureThreshold.WarningAt, failureThreshold.DangerAt, humanThresholdUnit(failureThreshold.Unit)),
		"threshold_key":                   "sync_failure_7d",
		"threshold_source":                failureThreshold.Source,
		"threshold_configuration_applied": failureThreshold.Applied,
		"capacity_signals_recomputed":     failureThreshold.Applied,
		"detail":                          fmt.Sprintf("%d failed sync runs in the last 7 days", failedRuns),
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
	webhookThreshold := thresholdForKey(thresholds, "webhook_delivery_failure_7d", repoSyncCapacityWebhookWarningThreshold, repoSyncCapacityWebhookDangerThreshold, "failed_events")
	signals = append(signals, map[string]any{
		"name":                            "webhook delivery",
		"status":                          webhookFailures,
		"severity":                        severityForCount(webhookFailures, webhookThreshold.WarningAt, webhookThreshold.DangerAt),
		"threshold":                       thresholdDetail(webhookThreshold.WarningAt, webhookThreshold.DangerAt, humanThresholdUnit(webhookThreshold.Unit)),
		"threshold_key":                   "webhook_delivery_failure_7d",
		"threshold_source":                webhookThreshold.Source,
		"threshold_configuration_applied": webhookThreshold.Applied,
		"capacity_signals_recomputed":     webhookThreshold.Applied,
		"detail":                          detail,
	})
	githubRuns := intFromAny(raw["github_runs_24h"], 0)
	githubThreshold := thresholdForKey(thresholds, "github_actions_volume_24h", repoSyncCapacityGitHubVolumeWarningThreshold, repoSyncCapacityGitHubVolumeDangerThreshold, "runs")
	signals = append(signals, map[string]any{
		"name":                            "GitHub Actions volume",
		"status":                          githubRuns,
		"severity":                        severityForCount(githubRuns, githubThreshold.WarningAt, githubThreshold.DangerAt),
		"threshold":                       thresholdDetail(githubThreshold.WarningAt, githubThreshold.DangerAt, humanThresholdUnit(githubThreshold.Unit)),
		"threshold_key":                   "github_actions_volume_24h",
		"threshold_source":                githubThreshold.Source,
		"threshold_configuration_applied": githubThreshold.Applied,
		"capacity_signals_recomputed":     githubThreshold.Applied,
		"detail":                          fmt.Sprintf("%d action runs observed on source/target remotes in the last 24 hours", githubRuns),
	})
	pairActive := intFromAny(raw["provider_pair_active_runs"], 0)
	pairRuns24h := intFromAny(raw["provider_pair_runs_24h"], 0)
	pairFailures24h := intFromAny(raw["provider_pair_failed_runs_24h"], 0)
	pairActiveThreshold := thresholdForKey(thresholds, "provider_pair_active_24h", repoSyncCapacityPairActiveWarningThreshold, repoSyncCapacityPairActiveDangerThreshold, "active_runs")
	pairFailureThreshold := thresholdForKey(thresholds, "provider_pair_failure_24h", repoSyncCapacityPairFailureWarningThreshold, repoSyncCapacityPairFailureDangerThreshold, "failures")
	pairSeverity := severityForCount(pairActive, pairActiveThreshold.WarningAt, pairActiveThreshold.DangerAt)
	if failureSeverity := severityForCount(pairFailures24h, pairFailureThreshold.WarningAt, pairFailureThreshold.DangerAt); failureSeverity == "danger" || (failureSeverity == "warning" && pairSeverity == "ok") {
		pairSeverity = failureSeverity
	}
	pairThresholdApplied := pairActiveThreshold.Applied || pairFailureThreshold.Applied
	signals = append(signals, map[string]any{
		"name":     "provider pair pressure",
		"status":   pairActive,
		"severity": pairSeverity,
		"threshold": fmt.Sprintf(
			"active warning >= %d / danger >= %d; failures warning >= %d / danger >= %d",
			pairActiveThreshold.WarningAt,
			pairActiveThreshold.DangerAt,
			pairFailureThreshold.WarningAt,
			pairFailureThreshold.DangerAt,
		),
		"threshold_key":                   "provider_pair_active_24h,provider_pair_failure_24h",
		"threshold_source":                thresholdSourceForPair(pairActiveThreshold, pairFailureThreshold),
		"threshold_configuration_applied": pairThresholdApplied,
		"capacity_signals_recomputed":     pairThresholdApplied,
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

type webhookThresholdConfiguration struct {
	WarningAt      int
	DangerAt       int
	Unit           string
	Source         string
	EvidenceWindow string
	Applied        bool
}

func queryWebhookThresholdConfigurationOverrides(ctx context.Context, db sqlx.ExtContext, sourceRemoteID, evidenceWindow string) (map[string]webhookThresholdConfiguration, error) {
	sourceRemoteID = strings.TrimSpace(sourceRemoteID)
	if sourceRemoteID == "" || sourceRemoteID == "<nil>" {
		return nil, nil
	}
	evidenceWindow = strings.TrimSpace(evidenceWindow)
	if evidenceWindow == "" {
		evidenceWindow = "7d"
	}
	rows, err := queryMaps(ctx, db, `
		SELECT DISTINCT ON (wtc.threshold_key)
			wtc.threshold_key,
			wtc.warning_at,
			wtc.danger_at,
			wtc.unit,
			wtc.evidence_window,
			wtc.applied_at
		FROM webhook_threshold_configurations wtc
		JOIN webhook_connections wc ON wc.id=wtc.webhook_connection_id
		WHERE wc.source_remote_id=$1
			AND wtc.evidence_window=$2
		ORDER BY wtc.threshold_key, wtc.applied_at DESC`, sourceRemoteID, evidenceWindow)
	if err != nil {
		return nil, err
	}
	thresholds := make(map[string]webhookThresholdConfiguration, len(rows))
	for _, row := range rows {
		key := cleanPreviewString(row["threshold_key"])
		if key == "" {
			continue
		}
		thresholds[key] = webhookThresholdConfiguration{
			WarningAt:      intFromAny(row["warning_at"], 0),
			DangerAt:       intFromAny(row["danger_at"], 0),
			Unit:           cleanPreviewString(row["unit"]),
			Source:         "webhook_threshold_configuration",
			EvidenceWindow: cleanPreviewString(row["evidence_window"]),
			Applied:        true,
		}
	}
	return thresholds, nil
}

func thresholdForKey(thresholds map[string]webhookThresholdConfiguration, key string, defaultWarning, defaultDanger int, defaultUnit string) webhookThresholdConfiguration {
	if thresholds != nil {
		if threshold, ok := thresholds[key]; ok {
			if threshold.WarningAt < 0 {
				threshold.WarningAt = 0
			}
			if threshold.DangerAt < threshold.WarningAt {
				threshold.DangerAt = threshold.WarningAt
			}
			if threshold.Unit == "" {
				threshold.Unit = defaultUnit
			}
			if expected := expectedWebhookThresholdUnit(key); expected != "" && threshold.Unit != expected {
				threshold.Unit = defaultUnit
			}
			if threshold.Source == "" {
				threshold.Source = "webhook_threshold_configuration"
			}
			threshold.Applied = true
			return threshold
		}
	}
	return webhookThresholdConfiguration{
		WarningAt: defaultWarning,
		DangerAt:  defaultDanger,
		Unit:      defaultUnit,
		Source:    "default_static_threshold",
	}
}

func expectedWebhookThresholdUnit(key string) string {
	switch key {
	case "sync_capacity_active", "provider_pair_active_24h":
		return "active_runs"
	case "sync_failure_7d", "provider_pair_failure_24h":
		return "failures"
	case "webhook_delivery_failure_7d":
		return "failed_events"
	case "github_actions_volume_24h":
		return "runs"
	default:
		return ""
	}
}

func humanThresholdUnit(unit string) string {
	switch strings.TrimSpace(unit) {
	case "active_runs":
		return "active runs"
	case "failed_events":
		return "failed events"
	default:
		return strings.ReplaceAll(strings.TrimSpace(unit), "_", " ")
	}
}

func thresholdSourceForPair(activeThreshold, failureThreshold webhookThresholdConfiguration) string {
	if activeThreshold.Applied && failureThreshold.Applied {
		return "webhook_threshold_configuration"
	}
	if activeThreshold.Applied {
		return "webhook_threshold_configuration_active_only"
	}
	if failureThreshold.Applied {
		return "webhook_threshold_configuration_failure_only"
	}
	return "default_static_threshold"
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
	baseURL := s.publicBaseURL()
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT wc.id,
			wc.project_id,
			wc.provider,
			wc.name,
			wc.source_remote_id,
			wc.enabled,
			wc.event_types,
			wc.last_delivery_status,
			wc.metadata,
			wc.created_at,
			wc.updated_at,
			gr.name AS source_remote_name,
			COALESCE(stats.deliveries_7d, 0)::int AS deliveries_7d,
			COALESCE(stats.failures_7d, 0)::int AS failures_7d,
			COALESCE(stats.processed_7d, 0)::int AS processed_7d,
			COALESCE(stats.ignored_7d, 0)::int AS ignored_7d,
			COALESCE(stats.replayed_7d, 0)::int AS replayed_7d,
			COALESCE(stats.signature_valid_7d, 0)::int AS signature_valid_7d,
			COALESCE(stats.matched_repo_sync_asset_7d, 0)::int AS matched_repo_sync_asset_7d,
			COALESCE(stats.operation_run_7d, 0)::int AS operation_run_7d,
			stats.last_event_at,
			stats.last_event_status,
			stats.last_event_type,
			stats.last_event_signature_valid,
			COALESCE(audit_stats.threshold_decision_audit_count, 0)::int AS threshold_decision_audit_count,
			audit_stats.last_threshold_decision_audit_at,
			COALESCE(config_stats.threshold_configuration_count, 0)::int AS threshold_configuration_count,
			config_stats.last_threshold_configuration_at,
			('/api/webhooks/' || wc.provider || '/' || wc.id::text) AS webhook_path,
			$2 || ('/api/webhooks/' || wc.provider || '/' || wc.id::text) AS webhook_url
		FROM webhook_connections wc
		LEFT JOIN git_remotes gr ON gr.id=wc.source_remote_id
		LEFT JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE we.received_at >= now() - interval '7 days') AS deliveries_7d,
				count(*) FILTER (WHERE we.status IN ('failed', 'rejected') AND we.received_at >= now() - interval '7 days') AS failures_7d,
				count(*) FILTER (WHERE we.status='processed' AND we.received_at >= now() - interval '7 days') AS processed_7d,
				count(*) FILTER (WHERE we.status='ignored' AND we.received_at >= now() - interval '7 days') AS ignored_7d,
				count(*) FILTER (WHERE we.delivery_id ILIKE '%:replay:%' AND we.received_at >= now() - interval '7 days') AS replayed_7d,
				count(*) FILTER (WHERE we.signature_valid AND we.received_at >= now() - interval '7 days') AS signature_valid_7d,
				count(*) FILTER (WHERE we.matched_repo_sync_asset_id IS NOT NULL AND we.received_at >= now() - interval '7 days') AS matched_repo_sync_asset_7d,
				count(*) FILTER (WHERE we.operation_run_id IS NOT NULL AND we.received_at >= now() - interval '7 days') AS operation_run_7d,
				max(we.received_at) AS last_event_at,
				(
					SELECT recent.status
					FROM webhook_events recent
					WHERE recent.webhook_connection_id=wc.id
					ORDER BY recent.received_at DESC
					LIMIT 1
				) AS last_event_status,
				(
					SELECT recent.event_type
					FROM webhook_events recent
					WHERE recent.webhook_connection_id=wc.id
					ORDER BY recent.received_at DESC
					LIMIT 1
				) AS last_event_type,
				(
					SELECT recent.signature_valid
					FROM webhook_events recent
					WHERE recent.webhook_connection_id=wc.id
					ORDER BY recent.received_at DESC
					LIMIT 1
				) AS last_event_signature_valid
			FROM webhook_events we
			WHERE we.webhook_connection_id=wc.id
		) stats ON true
		LEFT JOIN LATERAL (
			SELECT count(*) AS threshold_decision_audit_count,
				max(wtda.created_at) AS last_threshold_decision_audit_at
			FROM webhook_threshold_decision_audits wtda
			WHERE wtda.webhook_connection_id=wc.id
		) audit_stats ON true
		LEFT JOIN LATERAL (
			SELECT count(*) AS threshold_configuration_count,
				max(wtc.applied_at) AS last_threshold_configuration_at
			FROM webhook_threshold_configurations wtc
			WHERE wtc.webhook_connection_id=wc.id
		) config_stats ON true
		WHERE wc.project_id=$1
		ORDER BY wc.created_at DESC`, projectID, baseURL)
	annotateWebhookConnectionHealth(items)
	annotateWebhookCallbackReadiness(items, baseURL)
	annotateWebhookThresholdDecisionAuditEvidence(items)
	annotateWebhookThresholdConfigurationEvidence(items)
	writeQueryResult(w, items, err)
}

func webhookConnectionWithCallbackReadiness(ctx context.Context, db sqlx.ExtContext, connectionID, baseURL string) (map[string]any, error) {
	item, err := queryOne(ctx, db, `
		SELECT wc.id,
			wc.project_id,
			wc.provider,
			wc.name,
			wc.source_remote_id,
			wc.enabled,
			wc.event_types,
			wc.last_delivery_status,
			wc.metadata,
			wc.created_at,
			wc.updated_at,
			gr.name AS source_remote_name,
			COALESCE(stats.deliveries_7d, 0)::int AS deliveries_7d,
			COALESCE(stats.failures_7d, 0)::int AS failures_7d,
			COALESCE(stats.processed_7d, 0)::int AS processed_7d,
			COALESCE(stats.ignored_7d, 0)::int AS ignored_7d,
			COALESCE(stats.replayed_7d, 0)::int AS replayed_7d,
			COALESCE(stats.signature_valid_7d, 0)::int AS signature_valid_7d,
			COALESCE(stats.matched_repo_sync_asset_7d, 0)::int AS matched_repo_sync_asset_7d,
			COALESCE(stats.operation_run_7d, 0)::int AS operation_run_7d,
			stats.last_event_at,
			stats.last_event_status,
			stats.last_event_type,
			stats.last_event_signature_valid,
			COALESCE(audit_stats.threshold_decision_audit_count, 0)::int AS threshold_decision_audit_count,
			audit_stats.last_threshold_decision_audit_at,
			COALESCE(config_stats.threshold_configuration_count, 0)::int AS threshold_configuration_count,
			config_stats.last_threshold_configuration_at,
			('/api/webhooks/' || wc.provider || '/' || wc.id::text) AS webhook_path,
			$2 || ('/api/webhooks/' || wc.provider || '/' || wc.id::text) AS webhook_url
		FROM webhook_connections wc
		LEFT JOIN git_remotes gr ON gr.id=wc.source_remote_id
		LEFT JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE we.received_at >= now() - interval '7 days') AS deliveries_7d,
				count(*) FILTER (WHERE we.status IN ('failed', 'rejected') AND we.received_at >= now() - interval '7 days') AS failures_7d,
				count(*) FILTER (WHERE we.status='processed' AND we.received_at >= now() - interval '7 days') AS processed_7d,
				count(*) FILTER (WHERE we.status='ignored' AND we.received_at >= now() - interval '7 days') AS ignored_7d,
				count(*) FILTER (WHERE we.delivery_id ILIKE '%:replay:%' AND we.received_at >= now() - interval '7 days') AS replayed_7d,
				count(*) FILTER (WHERE we.signature_valid AND we.received_at >= now() - interval '7 days') AS signature_valid_7d,
				count(*) FILTER (WHERE we.matched_repo_sync_asset_id IS NOT NULL AND we.received_at >= now() - interval '7 days') AS matched_repo_sync_asset_7d,
				count(*) FILTER (WHERE we.operation_run_id IS NOT NULL AND we.received_at >= now() - interval '7 days') AS operation_run_7d,
				max(we.received_at) AS last_event_at,
				(
					SELECT recent.status
					FROM webhook_events recent
					WHERE recent.webhook_connection_id=wc.id
					ORDER BY recent.received_at DESC
					LIMIT 1
				) AS last_event_status,
				(
					SELECT recent.event_type
					FROM webhook_events recent
					WHERE recent.webhook_connection_id=wc.id
					ORDER BY recent.received_at DESC
					LIMIT 1
				) AS last_event_type,
				(
					SELECT recent.signature_valid
					FROM webhook_events recent
					WHERE recent.webhook_connection_id=wc.id
					ORDER BY recent.received_at DESC
					LIMIT 1
				) AS last_event_signature_valid
			FROM webhook_events we
			WHERE we.webhook_connection_id=wc.id
		) stats ON true
		LEFT JOIN LATERAL (
			SELECT count(*) AS threshold_decision_audit_count,
				max(wtda.created_at) AS last_threshold_decision_audit_at
			FROM webhook_threshold_decision_audits wtda
			WHERE wtda.webhook_connection_id=wc.id
		) audit_stats ON true
		LEFT JOIN LATERAL (
			SELECT count(*) AS threshold_configuration_count,
				max(wtc.applied_at) AS last_threshold_configuration_at
			FROM webhook_threshold_configurations wtc
			WHERE wtc.webhook_connection_id=wc.id
		) config_stats ON true
		WHERE wc.id=$1`, connectionID, baseURL)
	if err != nil {
		return nil, err
	}
	annotateWebhookConnectionHealth([]map[string]any{item})
	annotateWebhookCallbackReadiness([]map[string]any{item}, baseURL)
	annotateWebhookThresholdDecisionAuditEvidence([]map[string]any{item})
	annotateWebhookThresholdConfigurationEvidence([]map[string]any{item})
	return item, nil
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
		return "danger", "last delivery " + lastStatus
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

func annotateWebhookCallbackReadiness(items []map[string]any, baseURL string) {
	for _, item := range items {
		item["callback_rehearsal"] = webhookCallbackRehearsalReadiness(item, baseURL)
	}
}

func annotateWebhookThresholdDecisionAuditEvidence(items []map[string]any) {
	for _, item := range items {
		count := intFromAny(item["threshold_decision_audit_count"], 0)
		if count <= 0 {
			continue
		}
		readiness := mapFromAny(item["callback_rehearsal"])
		providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
		thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
		configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
		decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
		decisionAuditPlan["threshold_configuration_audit_inserted"] = true
		decisionAuditPlan["operator_threshold_review_recorded"] = true
		decisionAuditPlan["threshold_decision_audit_count"] = count
		decisionAuditPlan["last_threshold_decision_audit_at"] = item["last_threshold_decision_audit_at"]
		configurationPlan["operator_threshold_review_recorded"] = true
		configurationPlan["threshold_decision_audit_plan"] = decisionAuditPlan
		thresholdPlan["threshold_configuration_plan"] = configurationPlan
		providerPlan["threshold_tuning_plan"] = thresholdPlan
		readiness["provider_rehearsal_plan"] = providerPlan
		item["callback_rehearsal"] = readiness
	}
}

func annotateWebhookThresholdConfigurationEvidence(items []map[string]any) {
	for _, item := range items {
		count := intFromAny(item["threshold_configuration_count"], 0)
		readiness := mapFromAny(item["callback_rehearsal"])
		providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
		thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
		configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
		decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
		auditRecorded := boolOnlyFromAny(decisionAuditPlan["operator_threshold_review_recorded"]) ||
			intFromAny(decisionAuditPlan["threshold_decision_audit_count"], 0) > 0
		if auditRecorded {
			configurationPlan["configuration_write_enabled"] = true
			configurationPlan["blocked_reasons"] = removeStringFromSlice(stringSliceFromAny(configurationPlan["blocked_reasons"]), "operator_threshold_review_not_recorded")
			decisionAuditPlan["configuration_write_enabled"] = true
			decisionAuditPlan["blocked_reasons"] = removeStringFromSlice(stringSliceFromAny(decisionAuditPlan["blocked_reasons"]), "operator_threshold_review_not_recorded")
		}
		if count > 0 {
			recomputeEvidence := webhookThresholdCapacityRecomputeEvidence(item, count)
			configurationPlan["threshold_configuration_written"] = true
			configurationPlan["configuration_state"] = "recorded"
			configurationPlan["threshold_configuration_count"] = count
			configurationPlan["last_threshold_configuration_at"] = item["last_threshold_configuration_at"]
			configurationPlan["capacity_signals_recomputed"] = true
			configurationPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
			configurationPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
			configurationPlan["provider_metrics_fetched"] = false
			configurationPlan["external_call_made"] = false
			decisionAuditPlan["threshold_configuration_written"] = true
			decisionAuditPlan["threshold_configuration_count"] = count
			decisionAuditPlan["last_threshold_configuration_at"] = item["last_threshold_configuration_at"]
			decisionAuditPlan["capacity_signals_recomputed"] = true
			decisionAuditPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
			decisionAuditPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
			thresholdPlan["threshold_configuration_written"] = true
			thresholdPlan["provider_pair_thresholds_tuned"] = true
			thresholdPlan["capacity_signals_recomputed"] = true
			thresholdPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
		}
		configurationPlan["threshold_decision_audit_plan"] = decisionAuditPlan
		thresholdPlan["threshold_configuration_plan"] = configurationPlan
		providerPlan["threshold_tuning_plan"] = thresholdPlan
		readiness["provider_rehearsal_plan"] = providerPlan
		item["callback_rehearsal"] = readiness
	}
}

func removeStringFromSlice(values []string, remove string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == remove {
			continue
		}
		result = append(result, value)
	}
	return result
}

func webhookThresholdCapacityRecomputeEvidence(item map[string]any, configurationCount int) map[string]any {
	return map[string]any{
		"mode":                             "webhook_threshold_capacity_signal_recompute_evidence",
		"capacity_signals_recomputed":      configurationCount > 0,
		"recompute_mode":                   "read_time_repo_sync_asset_detail",
		"threshold_configuration_count":    configurationCount,
		"threshold_source":                 "webhook_threshold_configuration",
		"source_remote_id":                 cleanOptionalID(fmt.Sprint(item["source_remote_id"])),
		"delivery_count_7d":                intFromAny(item["deliveries_7d"], 0),
		"failed_count_7d":                  intFromAny(item["failures_7d"], 0),
		"processed_count_7d":               intFromAny(item["processed_7d"], 0),
		"operation_run_count_7d":           intFromAny(item["operation_run_7d"], 0),
		"matched_repo_sync_asset_count_7d": intFromAny(item["matched_repo_sync_asset_7d"], 0),
		"provider_metrics_fetched":         false,
		"provider_pair_limits_compared":    false,
		"external_call_made":               false,
		"raw_provider_response_recorded":   false,
		"raw_request_or_payload_recorded":  false,
		"contains_token":                   false,
		"contains_secret":                  false,
		"contains_payload":                 false,
		"contains_provider_url":            false,
		"suppressed_fields":                []string{"provider_token", "provider_url", "authorization_header", "request_headers", "provider_response_body", "provider_response_headers", "delivery_id", "payload"},
		"message":                          "Repo sync capacity signals are recomputed on read from local webhook threshold configuration rows and local counters only.",
	}
}

func webhookCallbackRehearsalReadiness(row map[string]any, baseURL string) map[string]any {
	reasons := make([]string, 0)
	origin := strings.TrimSpace(baseURL)
	if !isPlausiblePublicWebhookOrigin(origin) {
		reasons = append(reasons, "set ASSOPS_GATEWAY_URL to a public HTTP(S) origin before provider callback rehearsal")
	}
	if enabled, ok := row["enabled"].(bool); ok && !enabled {
		reasons = append(reasons, "webhook connection is disabled")
	}
	if !hasNonZeroValue(row["source_remote_id"]) {
		reasons = append(reasons, "source remote is missing")
	}
	if len(stringSliceFromAny(row["event_types"])) == 0 {
		reasons = append(reasons, "event types are missing")
	}
	if failures := intFromAny(row["failures_7d"], 0); failures > 0 {
		reasons = append(reasons, fmt.Sprintf("%d failed or rejected deliveries in 7d should be reviewed before rehearsal", failures))
	}
	switch strings.TrimSpace(fmt.Sprint(row["last_delivery_status"])) {
	case "failed", "rejected":
		reasons = append(reasons, "last delivery was "+fmt.Sprint(row["last_delivery_status"]))
	}
	status := "ready"
	message := "local prerequisites are ready; complete provider callback rehearsal in Gitea/GitHub"
	if len(reasons) > 0 {
		status = "blocked"
		message = strings.Join(reasons, "; ")
	}
	evidence := webhookProviderCallbackRehearsalEvidence(row)
	return map[string]any{
		"status":                  status,
		"public_origin":           origin,
		"provider":                strings.TrimSpace(fmt.Sprint(row["provider"])),
		"webhook_url":             strings.TrimSpace(fmt.Sprint(row["webhook_url"])),
		"required_provider":       "gitea_or_github_webhook_settings",
		"external_call_made":      false,
		"reasons":                 reasons,
		"callback_evidence":       evidence,
		"provider_rehearsal_plan": webhookProviderCallbackRehearsalPlan(status, reasons, evidence),
		"message":                 message,
	}
}

func webhookProviderCallbackRehearsalEvidence(row map[string]any) map[string]any {
	deliveries := intFromAny(row["deliveries_7d"], 0)
	failures := intFromAny(row["failures_7d"], 0)
	processed := intFromAny(row["processed_7d"], 0)
	ignored := intFromAny(row["ignored_7d"], 0)
	replayed := intFromAny(row["replayed_7d"], 0)
	signatureValid := intFromAny(row["signature_valid_7d"], 0)
	matchedRepoSyncAsset := intFromAny(row["matched_repo_sync_asset_7d"], 0)
	operationRuns := intFromAny(row["operation_run_7d"], 0)
	lastStatus := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["last_event_status"])))
	if lastStatus == "" || lastStatus == "<nil>" {
		lastStatus = strings.ToLower(strings.TrimSpace(fmt.Sprint(row["last_delivery_status"])))
	}
	lastEventType := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["last_event_type"])))
	if lastEventType == "<nil>" {
		lastEventType = ""
	}
	state := "not_observed"
	switch {
	case deliveries == 0:
		state = "not_observed"
	case failures > 0:
		state = "failed"
	case processed > 0:
		state = "recorded"
	case ignored > 0:
		state = "ignored"
	default:
		state = "observed"
	}
	return map[string]any{
		"mode":                             "provider_callback_rehearsal_evidence",
		"evidence_state":                   state,
		"delivery_count_7d":                deliveries,
		"processed_count_7d":               processed,
		"failed_count_7d":                  failures,
		"ignored_count_7d":                 ignored,
		"replayed_count_7d":                replayed,
		"signature_valid_count_7d":         signatureValid,
		"matched_repo_sync_asset_count_7d": matchedRepoSyncAsset,
		"operation_run_count_7d":           operationRuns,
		"last_event_at":                    row["last_event_at"],
		"last_event_status":                lastStatus,
		"last_event_type":                  lastEventType,
		"last_event_signature_valid":       boolOnlyFromAny(row["last_event_signature_valid"]),
		"provider":                         strings.TrimSpace(fmt.Sprint(row["provider"])),
		"webhook_event_recorded":           deliveries > 0,
		"provider_delivery_observed":       deliveries > 0,
		"signature_validation_observed":    signatureValid > 0,
		"webhook_event_replay_observed":    replayed > 0,
		"repo_sync_enqueue_observed":       operationRuns > 0 || matchedRepoSyncAsset > 0,
		"github_actions_refresh_observed":  strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["provider"])), "github") && processed > 0 && operationRuns > 0,
		"sanitized_result_recorded":        deliveries > 0,
		"operator_replay_proof":            webhookProviderCallbackOperatorReplayProof(deliveries, failures, processed, replayed, signatureValid, matchedRepoSyncAsset, operationRuns),
		"external_call_made_by_assops":     false,
		"provider_settings_written":        false,
		"provider_test_delivery_sent":      false,
		"raw_request_headers_recorded":     false,
		"raw_request_body_recorded":        false,
		"raw_provider_response_recorded":   false,
		"contains_token":                   false,
		"contains_secret":                  false,
		"contains_payload":                 false,
		"contains_provider_url":            false,
		"suppressed_fields":                []string{"secret_token", "shared_secret", "signature_header", "provider_token", "provider_url", "request_headers", "request_body", "delivery_payload", "delivery_response", "provider_response_body", "provider_response_headers", "delivery_id", "payload", "result", "error_message"},
	}
}

func webhookProviderCallbackOperatorReplayProof(deliveries, failures, processed, replayed, signatureValid, matchedRepoSyncAsset, operationRuns int) map[string]any {
	proofState := "waiting_for_operator_replay"
	switch {
	case replayed > 0 && failures > 0 && processed == 0:
		proofState = "failed"
	case replayed > 0 && (operationRuns > 0 || matchedRepoSyncAsset > 0 || processed > 0):
		proofState = "recorded"
	case replayed > 0:
		proofState = "observed"
	}
	return map[string]any{
		"mode":                               "operator_guided_webhook_replay_proof",
		"proof_state":                        proofState,
		"proof_source":                       "webhook_events_aggregate",
		"manual_replay_required":             replayed == 0,
		"operator_replay_observed":           replayed > 0,
		"sanitized_replay_result_recorded":   replayed > 0,
		"delivery_evidence_count_7d":         deliveries,
		"replayed_event_count_7d":            replayed,
		"processed_delivery_count_7d":        processed,
		"failed_delivery_count_7d":           failures,
		"signature_valid_delivery_count_7d":  signatureValid,
		"repo_sync_binding_count_7d":         matchedRepoSyncAsset,
		"operation_binding_count_7d":         operationRuns,
		"signature_validation_observed":      replayed > 0 && signatureValid > 0,
		"repo_sync_binding_observed":         replayed > 0 && matchedRepoSyncAsset > 0,
		"operation_binding_observed":         replayed > 0 && operationRuns > 0,
		"external_call_made_by_assops":       false,
		"provider_api_called":                false,
		"provider_test_delivery_sent":        false,
		"raw_request_headers_recorded":       false,
		"raw_request_body_recorded":          false,
		"raw_provider_response_recorded":     false,
		"source_delivery_id_recorded":        false,
		"replay_source_delivery_id_recorded": false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"suppressed_fields":                  []string{"delivery_id", "source_delivery_id", "replay_source_delivery_id", "secret_token", "shared_secret", "signature_header", "authorization_header", "provider_token", "provider_url", "request_headers", "request_body", "payload", "delivery_payload", "result", "error_message", "provider_response_body", "provider_response_headers"},
	}
}

func webhookProviderCallbackRehearsalPlan(readinessStatus string, readinessReasons []string, evidence map[string]any) map[string]any {
	planState := "blocked"
	if readinessStatus == "ready" {
		planState = "planned"
	}
	blockedReasons := append([]string{}, readinessReasons...)
	executionBlockers := []string{"provider_callback_rehearsal_not_performed"}
	if planState == "planned" {
		blockedReasons = []string{}
	}
	deliveryObserved := boolOnlyFromAny(evidence["provider_delivery_observed"])
	resultWritten := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	replayProof := mapFromAny(evidence["operator_replay_proof"])
	replayProofState := cleanPreviewString(replayProof["proof_state"])
	return map[string]any{
		"mode":                           "provider_callback_rehearsal_plan",
		"plan_state":                     planState,
		"execution_enabled":              false,
		"external_call_made":             false,
		"provider_settings_written":      false,
		"provider_test_delivery_sent":    false,
		"provider_delivery_received":     deliveryObserved,
		"webhook_event_created":          deliveryObserved,
		"webhook_event_replayed":         boolOnlyFromAny(evidence["webhook_event_replay_observed"]),
		"operator_replay_proof_recorded": replayProofState == "recorded",
		"repo_sync_enqueued":             boolOnlyFromAny(evidence["repo_sync_enqueue_observed"]),
		"github_actions_refresh_started": boolOnlyFromAny(evidence["github_actions_refresh_observed"]),
		"result_written":                 resultWritten,
		"contains_token":                 false,
		"contains_secret":                false,
		"contains_payload":               false,
		"contains_provider_url":          false,
		"contains_delivery_body":         false,
		"required_controls": []string{
			"public_gateway_origin",
			"provider_webhook_settings_review",
			"webhook_secret_rotation_review",
			"delivery_id_deduplication",
			"provider_test_delivery",
			"sanitized_result_recording",
		},
		"callback_execution_sequence": []string{
			"verify_public_staging_origin",
			"review_provider_webhook_settings",
			"send_provider_test_delivery",
			"observe_sanitized_callback_event",
			"replay_event_to_repo_sync",
			"refresh_provider_actions_state",
			"record_redacted_rehearsal_result",
			"review_provider_pair_thresholds",
		},
		"disabled_backends": []string{
			"provider_webhook_settings_write",
			"provider_test_delivery",
			"external_callback_wait",
			"webhook_event_insert",
			"webhook_event_replay",
			"repo_sync_enqueue",
			"github_actions_api_sync",
		},
		"suppressed_fields": []string{
			"secret_token",
			"shared_secret",
			"signature_header",
			"provider_token",
			"provider_url",
			"request_headers",
			"request_body",
			"delivery_payload",
			"delivery_response",
		},
		"blocked_reasons":        blockedReasons,
		"execution_blockers":     executionBlockers,
		"public_endpoint_plan":   webhookProviderCallbackPublicEndpointPlan(planState, readinessReasons),
		"provider_delivery_plan": webhookProviderCallbackDeliveryPlan(planState),
		"operator_replay_proof":  replayProof,
		"threshold_tuning_plan":  webhookProviderCallbackThresholdTuningPlan(planState, evidence),
		"result_recording_plan":  webhookProviderCallbackRehearsalResultRecordingPlan(evidence),
		"message":                "Provider callback rehearsal is audit-only; no provider settings write or provider test delivery is performed, while existing webhook event evidence is reconciled as sanitized metadata.",
	}
}

func webhookProviderCallbackPublicEndpointPlan(planState string, readinessReasons []string) map[string]any {
	publicOriginReady := true
	blockedReasons := make([]string, 0)
	for _, reason := range readinessReasons {
		if strings.Contains(reason, "ASSOPS_GATEWAY_URL") || strings.Contains(reason, "public HTTP(S) origin") {
			publicOriginReady = false
			blockedReasons = append(blockedReasons, reason)
		}
	}
	endpointState := "planned"
	if planState != "planned" || !publicOriginReady {
		endpointState = "blocked"
	}
	return map[string]any{
		"mode":                    "provider_callback_public_endpoint_plan",
		"endpoint_state":          endpointState,
		"public_origin_ready":     publicOriginReady,
		"public_staging_required": true,
		"dns_probe_performed":     false,
		"tls_probe_performed":     false,
		"provider_ping_performed": false,
		"external_call_made":      false,
		"contains_provider_url":   false,
		"contains_token":          false,
		"required_controls":       []string{"public_https_origin", "dns_review", "tls_review", "provider_callback_path_review"},
		"disabled_backends":       []string{"dns_probe", "tls_probe", "provider_callback_ping"},
		"suppressed_fields":       []string{"provider_url", "request_headers", "provider_token", "shared_secret"},
		"blocked_reasons":         blockedReasons,
		"execution_blockers":      []string{"public_staging_hostname_not_verified"},
		"message":                 "Public endpoint verification is planned only; no DNS, TLS, or provider callback probe is performed.",
	}
}

func webhookProviderCallbackDeliveryPlan(planState string) map[string]any {
	deliveryState := "blocked"
	if planState == "planned" {
		deliveryState = "planned"
	}
	return map[string]any{
		"mode":                         "provider_callback_delivery_plan",
		"delivery_state":               deliveryState,
		"provider_settings_written":    false,
		"provider_test_delivery_sent":  false,
		"provider_delivery_received":   false,
		"delivery_signature_validated": false,
		"delivery_deduplicated":        false,
		"webhook_event_created":        false,
		"external_call_made":           false,
		"contains_token":               false,
		"contains_secret":              false,
		"contains_payload":             false,
		"required_controls": []string{
			"provider_settings_operator_review",
			"webhook_secret_rotation_review",
			"test_delivery_id_capture",
			"signature_validation",
			"delivery_id_deduplication",
		},
		"disabled_backends": []string{
			"provider_webhook_settings_write",
			"provider_test_delivery",
			"external_callback_wait",
			"webhook_event_insert",
		},
		"suppressed_fields":  []string{"secret_token", "shared_secret", "signature_header", "provider_token", "request_headers", "request_body", "delivery_payload", "delivery_response"},
		"blocked_reasons":    []string{"real_provider_test_delivery_not_performed"},
		"execution_blockers": []string{"provider_callback_rehearsal_not_performed"},
		"message":            "Provider delivery rehearsal is planned only; no provider settings are written and no test delivery is sent.",
	}
}

func webhookProviderCallbackThresholdTuningPlan(planState string, evidence map[string]any) map[string]any {
	thresholdState := "blocked"
	if planState == "planned" {
		thresholdState = "planned"
	}
	volumeEvidence := webhookProviderCallbackThresholdVolumeEvidence(evidence)
	metricsComparisonPlan := webhookProviderCallbackProviderMetricsComparisonPlan(volumeEvidence)
	thresholdReviewReady := boolOnlyFromAny(volumeEvidence["threshold_review_ready"])
	executionBlockers := []string{"provider_pair_thresholds_need_live_volume_tuning"}
	switch cleanPreviewString(volumeEvidence["threshold_review_state"]) {
	case "ready_for_review":
		executionBlockers = []string{"operator_threshold_review_not_recorded"}
	case "review_failed_volume":
		executionBlockers = []string{"webhook_failures_need_operator_threshold_review"}
	case "volume_observed":
		executionBlockers = []string{"processed_or_repo_sync_volume_not_observed"}
	}
	return map[string]any{
		"mode":                              "provider_callback_threshold_tuning_plan",
		"threshold_state":                   thresholdState,
		"live_volume_observed":              boolOnlyFromAny(volumeEvidence["local_volume_observed"]),
		"threshold_review_state":            volumeEvidence["threshold_review_state"],
		"threshold_review_ready":            thresholdReviewReady,
		"provider_pair_thresholds_tuned":    false,
		"sync_capacity_thresholds_tuned":    false,
		"webhook_delivery_thresholds_tuned": false,
		"github_actions_thresholds_tuned":   false,
		"threshold_configuration_written":   false,
		"external_call_made":                false,
		"volume_evidence":                   volumeEvidence,
		"provider_metrics_comparison_plan":  metricsComparisonPlan,
		"threshold_configuration_plan":      webhookProviderCallbackThresholdConfigurationPlan(volumeEvidence, metricsComparisonPlan),
		"required_observations":             []string{"provider_pair_active_runs", "provider_pair_recent_failures", "webhook_delivery_failures", "github_actions_run_volume"},
		"threshold_review_sequence":         []string{"collect_live_sync_volume", "compare_provider_limits", "adjust_warning_thresholds", "adjust_danger_thresholds", "record_threshold_review"},
		"disabled_backends":                 []string{"provider_metrics_fetch", "threshold_configuration_write", "sync_capacity_backfill"},
		"suppressed_fields":                 []string{"provider_token", "provider_url", "request_headers", "provider_response_body"},
		"blocked_reasons":                   stringSliceFromAny(volumeEvidence["blocked_reasons"]),
		"execution_blockers":                executionBlockers,
		"message":                           "Provider-pair threshold tuning is planned only; local webhook volume evidence is redacted and current thresholds stay unchanged until an operator reviews real rehearsal volume.",
	}
}

func webhookProviderCallbackThresholdConfigurationPlan(volumeEvidence, metricsComparisonPlan map[string]any) map[string]any {
	reviewState := cleanPreviewString(volumeEvidence["threshold_review_state"])
	reviewReady := boolOnlyFromAny(volumeEvidence["threshold_review_ready"])
	configurationState := "blocked"
	blockedReasons := append([]string{}, stringSliceFromAny(volumeEvidence["blocked_reasons"])...)
	switch {
	case reviewState == "waiting_for_volume":
		configurationState = "waiting_for_volume"
	case reviewState == "review_failed_volume":
		configurationState = "needs_failure_review"
	case reviewReady:
		configurationState = "ready_for_operator_review"
		blockedReasons = []string{"operator_threshold_review_not_recorded", "threshold_configuration_write_disabled"}
	}
	decisionAuditPlan := webhookProviderCallbackThresholdDecisionAuditPlan(volumeEvidence, metricsComparisonPlan, configurationState)
	return map[string]any{
		"mode":                               "provider_callback_threshold_configuration_plan",
		"configuration_state":                configurationState,
		"configuration_review_ready":         reviewReady,
		"threshold_review_state":             reviewState,
		"threshold_configuration_written":    false,
		"configuration_write_enabled":        false,
		"operator_threshold_review_recorded": false,
		"threshold_decision_audit_plan":      decisionAuditPlan,
		"provider_metrics_fetched":           false,
		"provider_pair_limits_compared":      false,
		"provider_metrics_comparison_plan":   metricsComparisonPlan,
		"external_call_made":                 false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"current_thresholds":                 webhookProviderCallbackCurrentThresholds(),
		"required_persisted_fields": []string{
			"provider_pair",
			"threshold_key",
			"warning_at",
			"danger_at",
			"unit",
			"reviewed_by",
			"reviewed_at",
			"evidence_window",
		},
		"configuration_sequence": []string{
			"collect_live_volume_evidence",
			"compare_current_thresholds",
			"record_operator_threshold_review",
			"persist_threshold_configuration",
			"recompute_repo_sync_capacity_signals",
		},
		"disabled_backends": []string{
			"provider_metrics_fetch",
			"threshold_configuration_write",
			"threshold_configuration_audit_insert",
		},
		"capacity_signal_recompute_mode": "read_time_repo_sync_asset_detail",
		"capacity_signals_recomputed":    false,
		"suppressed_fields": []string{
			"provider_token",
			"provider_url",
			"request_headers",
			"provider_response_body",
			"operator_identity",
			"operator_notes",
		},
		"blocked_reasons": blockedReasons,
		"message":         "Threshold configuration persistence is review-only until an operator audit is recorded; after configuration write, repo sync capacity signals recompute from local rows without provider metrics.",
	}
}

func webhookProviderCallbackCurrentThresholds() []map[string]any {
	return []map[string]any{
		{"key": "sync_capacity_active", "warning_at": repoSyncCapacityActiveWarningThreshold, "danger_at": repoSyncCapacityActiveDangerThreshold, "unit": "active_runs"},
		{"key": "sync_failure_7d", "warning_at": repoSyncCapacityFailure7dWarningThreshold, "danger_at": repoSyncCapacityFailure7dDangerThreshold, "unit": "failures"},
		{"key": "webhook_delivery_failure_7d", "warning_at": repoSyncCapacityWebhookWarningThreshold, "danger_at": repoSyncCapacityWebhookDangerThreshold, "unit": "failed_events"},
		{"key": "github_actions_volume_24h", "warning_at": repoSyncCapacityGitHubVolumeWarningThreshold, "danger_at": repoSyncCapacityGitHubVolumeDangerThreshold, "unit": "runs"},
		{"key": "provider_pair_active_24h", "warning_at": repoSyncCapacityPairActiveWarningThreshold, "danger_at": repoSyncCapacityPairActiveDangerThreshold, "unit": "active_runs"},
		{"key": "provider_pair_failure_24h", "warning_at": repoSyncCapacityPairFailureWarningThreshold, "danger_at": repoSyncCapacityPairFailureDangerThreshold, "unit": "failures"},
	}
}

func webhookProviderCallbackThresholdDecisionAuditPlan(volumeEvidence, metricsComparisonPlan map[string]any, configurationState string) map[string]any {
	reviewState := cleanPreviewString(volumeEvidence["threshold_review_state"])
	if reviewState == "" {
		reviewState = "waiting_for_volume"
	}
	decisionState := "blocked"
	switch {
	case reviewState == "waiting_for_volume":
		decisionState = "waiting_for_volume"
	case reviewState == "review_failed_volume":
		decisionState = "needs_failure_review"
	case configurationState == "ready_for_operator_review" && boolOnlyFromAny(volumeEvidence["threshold_review_ready"]):
		decisionState = "metadata_review_ready"
	}
	decisionReady := decisionState == "metadata_review_ready" &&
		boolOnlyFromAny(metricsComparisonPlan["comparison_ready_for_review"])
	blockedReasons := []string{"threshold_configuration_write_disabled"}
	if !decisionReady {
		blockedReasons = append(blockedReasons, "threshold_configuration_audit_insert_disabled")
	}
	if reviewState == "waiting_for_volume" {
		blockedReasons = append(blockedReasons, "real_provider_volume_not_observed")
	}
	if reviewState == "review_failed_volume" {
		blockedReasons = append(blockedReasons, "webhook_failures_need_operator_threshold_review")
	}
	if decisionState == "blocked" {
		blockedReasons = append(blockedReasons, "threshold_review_metadata_not_ready")
	} else if !decisionReady {
		blockedReasons = append(blockedReasons, "operator_threshold_review_not_recorded")
	}
	if !boolOnlyFromAny(metricsComparisonPlan["comparison_ready_for_review"]) {
		blockedReasons = append(blockedReasons, "provider_metrics_comparison_not_review_ready")
	}
	disabledBackends := []string{"threshold_configuration_write", "threshold_delta_persist", "provider_metrics_fetch"}
	if !decisionReady {
		disabledBackends = append([]string{"threshold_configuration_audit_insert"}, disabledBackends...)
	}
	return map[string]any{
		"mode":                                   "provider_callback_threshold_decision_audit_plan",
		"decision_state":                         decisionState,
		"decision_ready_for_review":              decisionReady,
		"threshold_review_state":                 reviewState,
		"configuration_state":                    configurationState,
		"threshold_configuration_written":        false,
		"threshold_configuration_audit_inserted": false,
		"configuration_write_enabled":            false,
		"audit_insert_enabled":                   decisionReady,
		"capacity_signals_recomputed":            false,
		"provider_metrics_fetched":               false,
		"provider_pair_limits_compared":          false,
		"proposed_threshold_delta_persisted":     false,
		"operator_threshold_review_recorded":     false,
		"external_call_made":                     false,
		"contains_token":                         false,
		"contains_secret":                        false,
		"contains_payload":                       false,
		"contains_provider_url":                  false,
		"delivery_count_7d":                      intFromAny(volumeEvidence["delivery_count_7d"], 0),
		"failed_count_7d":                        intFromAny(volumeEvidence["failed_count_7d"], 0),
		"operation_run_count_7d":                 intFromAny(volumeEvidence["operation_run_count_7d"], 0),
		"matched_repo_sync_asset_count_7d":       intFromAny(volumeEvidence["matched_repo_sync_asset_count_7d"], 0),
		"required_decision_fields":               []string{"provider_pair", "threshold_key", "current_warning_at", "current_danger_at", "proposed_warning_at", "proposed_danger_at", "evidence_window", "operator_decision", "reviewed_at"},
		"required_controls":                      []string{"operator_threshold_review", "provider_metrics_comparison_review", "threshold_delta_schema_review", "audit_row_redaction_review", "capacity_signal_recompute_review"},
		"disabled_backends":                      disabledBackends,
		"suppressed_fields":                      []string{"operator_identity", "operator_notes", "provider_token", "provider_url", "authorization_header", "request_headers", "provider_response_body", "provider_response_headers", "delivery_id", "payload"},
		"blocked_reasons":                        blockedReasons,
		"message":                                "Threshold decision audit can record sanitized local callback-volume metadata when review-ready; no threshold configuration, provider metric, URL, token, payload, raw provider response, or operator note is written.",
	}
}

func webhookProviderCallbackProviderMetricsComparisonPlan(volumeEvidence map[string]any) map[string]any {
	reviewState := cleanPreviewString(volumeEvidence["threshold_review_state"])
	if reviewState == "" {
		reviewState = "waiting_for_volume"
	}
	volumeObserved := boolOnlyFromAny(volumeEvidence["local_volume_observed"])
	reviewReady := boolOnlyFromAny(volumeEvidence["threshold_review_ready"])
	comparisonState := "waiting_for_volume"
	switch {
	case reviewState == "review_failed_volume":
		comparisonState = "needs_failure_review"
	case reviewReady:
		comparisonState = "ready_for_operator_review"
	case volumeObserved:
		comparisonState = "local_volume_observed"
	}
	blockedReasons := []string{"provider_metrics_fetch_disabled", "provider_pair_limits_compare_disabled"}
	if !volumeObserved {
		blockedReasons = append(blockedReasons, "real_provider_volume_not_observed")
	}
	if reviewState == "review_failed_volume" {
		blockedReasons = append(blockedReasons, "webhook_failures_need_operator_threshold_review")
	} else if volumeObserved && !reviewReady {
		blockedReasons = append(blockedReasons, "processed_or_repo_sync_volume_not_observed")
	}
	return map[string]any{
		"mode":                               "provider_callback_provider_metrics_comparison_plan",
		"comparison_state":                   comparisonState,
		"threshold_review_state":             reviewState,
		"comparison_ready_for_review":        reviewReady,
		"local_volume_observed":              volumeObserved,
		"provider_volume_observed":           false,
		"provider_metrics_fetched":           false,
		"provider_pair_limits_compared":      false,
		"external_call_made":                 false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"delivery_count_7d":                  intFromAny(volumeEvidence["delivery_count_7d"], 0),
		"processed_count_7d":                 intFromAny(volumeEvidence["processed_count_7d"], 0),
		"failed_count_7d":                    intFromAny(volumeEvidence["failed_count_7d"], 0),
		"operation_run_count_7d":             intFromAny(volumeEvidence["operation_run_count_7d"], 0),
		"matched_repo_sync_asset_count_7d":   intFromAny(volumeEvidence["matched_repo_sync_asset_count_7d"], 0),
		"repo_sync_volume_observed":          boolOnlyFromAny(volumeEvidence["repo_sync_volume_observed"]),
		"processed_or_bound_volume_observed": boolOnlyFromAny(volumeEvidence["processed_or_bound_volume_observed"]),
		"current_thresholds": []map[string]any{
			{"key": "webhook_delivery_failure_7d", "warning_at": repoSyncCapacityWebhookWarningThreshold, "danger_at": repoSyncCapacityWebhookDangerThreshold, "unit": "failed_events"},
			{"key": "provider_pair_active_24h", "warning_at": repoSyncCapacityPairActiveWarningThreshold, "danger_at": repoSyncCapacityPairActiveDangerThreshold, "unit": "active_runs"},
			{"key": "provider_pair_failure_24h", "warning_at": repoSyncCapacityPairFailureWarningThreshold, "danger_at": repoSyncCapacityPairFailureDangerThreshold, "unit": "failures"},
			{"key": "github_actions_volume_24h", "warning_at": repoSyncCapacityGitHubVolumeWarningThreshold, "danger_at": repoSyncCapacityGitHubVolumeDangerThreshold, "unit": "runs"},
		},
		"required_provider_metrics": []string{
			"provider_delivery_attempts",
			"provider_delivery_failures",
			"provider_rate_limit_remaining",
			"provider_actions_run_volume",
		},
		"comparison_sequence": []string{
			"collect_local_webhook_volume",
			"fetch_provider_delivery_metrics",
			"compare_provider_pair_limits",
			"review_threshold_delta",
			"record_operator_threshold_decision",
		},
		"disabled_backends": []string{
			"provider_metrics_fetch",
			"provider_pair_limits_compare",
			"threshold_delta_persist",
			"operator_review_audit_insert",
		},
		"suppressed_fields": []string{
			"provider_token",
			"provider_url",
			"authorization_header",
			"request_headers",
			"provider_response_body",
			"provider_response_headers",
			"delivery_id",
			"payload",
		},
		"blocked_reasons": blockedReasons,
		"message":         "Provider metrics comparison is review-only; ASSOPS uses local webhook counters here and does not fetch provider metrics, compare live provider limits, or persist threshold deltas.",
	}
}

func webhookProviderCallbackThresholdVolumeEvidence(evidence map[string]any) map[string]any {
	if evidence == nil {
		evidence = map[string]any{}
	}
	deliveries := intFromAny(evidence["delivery_count_7d"], 0)
	failures := intFromAny(evidence["failed_count_7d"], 0)
	processed := intFromAny(evidence["processed_count_7d"], 0)
	replayed := intFromAny(evidence["replayed_count_7d"], 0)
	operationRuns := intFromAny(evidence["operation_run_count_7d"], 0)
	matchedRepoSyncAsset := intFromAny(evidence["matched_repo_sync_asset_count_7d"], 0)
	localVolumeObserved := deliveries > 0 || replayed > 0 || operationRuns > 0 || matchedRepoSyncAsset > 0
	processedOrBoundVolume := operationRuns > 0 || matchedRepoSyncAsset > 0 || processed > 0
	reviewState := "waiting_for_volume"
	var blockedReasons []string
	switch {
	case !localVolumeObserved:
		reviewState = "waiting_for_volume"
		blockedReasons = []string{"real_provider_volume_not_observed"}
	case failures > 0:
		reviewState = "review_failed_volume"
		blockedReasons = []string{"webhook_failures_need_operator_threshold_review"}
	case processedOrBoundVolume:
		reviewState = "ready_for_review"
		blockedReasons = []string{"operator_threshold_review_not_recorded"}
	default:
		reviewState = "volume_observed"
		blockedReasons = []string{"processed_or_repo_sync_volume_not_observed"}
	}
	return map[string]any{
		"mode":                               "provider_callback_threshold_volume_evidence",
		"threshold_review_state":             reviewState,
		"threshold_review_ready":             localVolumeObserved && failures == 0 && processedOrBoundVolume,
		"local_volume_observed":              localVolumeObserved,
		"provider_volume_observed":           false,
		"provider_metrics_fetched":           false,
		"provider_pair_limits_compared":      false,
		"threshold_configuration_written":    false,
		"delivery_count_7d":                  deliveries,
		"processed_count_7d":                 processed,
		"failed_count_7d":                    failures,
		"replayed_count_7d":                  replayed,
		"operation_run_count_7d":             operationRuns,
		"matched_repo_sync_asset_count_7d":   matchedRepoSyncAsset,
		"repo_sync_volume_observed":          operationRuns > 0 || matchedRepoSyncAsset > 0,
		"processed_or_bound_volume_observed": processedOrBoundVolume,
		"webhook_failure_volume_observed":    failures > 0,
		"external_call_made":                 false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"suppressed_fields":                  []string{"delivery_id", "source_delivery_id", "provider_token", "provider_url", "request_headers", "request_body", "payload", "provider_response_body", "provider_response_headers", "repo_url", "branch_name"},
		"blocked_reasons":                    blockedReasons,
	}
}

func webhookProviderCallbackRehearsalResultRecordingPlan(evidence map[string]any) map[string]any {
	evidenceState := strings.TrimSpace(fmt.Sprint(evidence["evidence_state"]))
	evidenceObserved := boolOnlyFromAny(evidence["webhook_event_recorded"])
	resultReady := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	replayProof := mapFromAny(evidence["operator_replay_proof"])
	replayProofState := cleanPreviewString(replayProof["proof_state"])
	if replayProofState == "" {
		replayProofState = "waiting_for_operator_replay"
	}
	recordingState := "blocked"
	recordingReason := "provider_callback_rehearsal_execution_not_performed"
	switch evidenceState {
	case "recorded", "observed":
		recordingState = "recorded"
		recordingReason = "sanitized_provider_callback_event_observed"
	case "failed":
		recordingState = "failed"
		recordingReason = "provider_callback_delivery_failed"
	case "ignored":
		recordingState = "ignored"
		recordingReason = "provider_callback_delivery_ignored"
	}
	return map[string]any{
		"mode":                             "provider_callback_rehearsal_result_recording_plan",
		"result_recording_state":           recordingState,
		"result_recording_ready":           resultReady,
		"result_recording_ready_reason":    recordingReason,
		"recording_enabled":                resultReady,
		"result_written":                   resultReady,
		"webhook_connection_updated":       false,
		"webhook_event_recorded":           evidenceObserved,
		"operator_replay_proof_state":      replayProofState,
		"operator_replay_proof_recorded":   replayProofState == "recorded",
		"operation_log_written":            false,
		"repo_sync_result_recorded":        boolOnlyFromAny(evidence["repo_sync_enqueue_observed"]),
		"github_actions_result_recorded":   boolOnlyFromAny(evidence["github_actions_refresh_observed"]),
		"threshold_tuning_result_recorded": false,
		"raw_request_headers_recorded":     false,
		"raw_request_body_recorded":        false,
		"raw_provider_response_recorded":   false,
		"contains_token":                   false,
		"contains_secret":                  false,
		"contains_payload":                 false,
		"contains_provider_url":            false,
		"result_recording_sequence": []string{
			"classify_provider_delivery",
			"record_sanitized_delivery_summary",
			"record_webhook_event_status",
			"record_repo_sync_rehearsal_summary",
			"record_github_actions_refresh_summary",
			"persist_redacted_rehearsal_result",
		},
		"result_diagnostic_fields": []string{
			"provider",
			"public_origin_valid",
			"delivery_status",
			"signature_valid",
			"event_type",
			"repo_sync_enqueued",
			"github_actions_refresh_status",
			"provider_pair_threshold_state",
		},
		"suppressed_fields": []string{
			"secret_token",
			"shared_secret",
			"signature_header",
			"provider_token",
			"provider_url",
			"request_headers",
			"request_body",
			"delivery_payload",
			"delivery_response",
			"provider_response_body",
			"provider_response_headers",
		},
		"blocked_reasons": []string{
			"provider_callback_rehearsal_execution_not_performed",
			"webhook_event_result_update_not_wired",
			"repo_sync_rehearsal_result_not_recorded",
			"github_actions_refresh_not_performed",
		},
		"message": "Provider callback rehearsal result recording is planned only; raw request, provider response, secret, and payload material are never persisted.",
	}
}

func isPlausiblePublicWebhookOrigin(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPublicIP(ip)
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, suffix := range []string{".local", ".internal", ".cluster.local", ".svc", ".svc.cluster.local"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

func hasNonZeroValue(value any) bool {
	text := strings.TrimSpace(fmt.Sprint(value))
	return text != "" && text != "<nil>" && text != "0"
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

func (s *Server) recordWebhookThresholdDecisionAudit(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	connection, err := webhookConnectionWithCallbackReadiness(r.Context(), s.store.DB, connectionID, s.publicBaseURL())
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(connection["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ID: connectionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		OperatorDecision string `json:"operator_decision"`
		EvidenceWindow   string `json:"evidence_window"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	operatorDecision := strings.TrimSpace(req.OperatorDecision)
	if operatorDecision == "" {
		operatorDecision = "record_metadata_review"
	}
	evidenceWindow := strings.TrimSpace(req.EvidenceWindow)
	if evidenceWindow == "" {
		evidenceWindow = "7d"
	}
	if !validWebhookEvidenceWindow(evidenceWindow) {
		writeError(w, http.StatusBadRequest, "invalid evidence window")
		return
	}
	readiness := mapFromAny(connection["callback_rehearsal"])
	providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
	thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
	volumeEvidence := mapFromAny(thresholdPlan["volume_evidence"])
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if !boolOnlyFromAny(decisionAuditPlan["decision_ready_for_review"]) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "threshold decision audit requires review-ready callback volume evidence",
			"readiness": readiness,
		})
		return
	}
	metricsComparisonPlan := mapFromAny(thresholdPlan["provider_metrics_comparison_plan"])
	evidence := map[string]any{
		"mode":                                     "provider_callback_threshold_decision_audit_evidence",
		"webhook_connection_id":                    connectionID,
		"provider":                                 strings.TrimSpace(fmt.Sprint(connection["provider"])),
		"operator_decision":                        operatorDecision,
		"evidence_window":                          evidenceWindow,
		"threshold_review_state":                   cleanPreviewString(decisionAuditPlan["threshold_review_state"]),
		"configuration_state":                      cleanPreviewString(decisionAuditPlan["configuration_state"]),
		"decision_state":                           cleanPreviewString(decisionAuditPlan["decision_state"]),
		"comparison_state":                         cleanPreviewString(metricsComparisonPlan["comparison_state"]),
		"delivery_count_7d":                        intFromAny(decisionAuditPlan["delivery_count_7d"], 0),
		"processed_count_7d":                       intFromAny(volumeEvidence["processed_count_7d"], 0),
		"failed_count_7d":                          intFromAny(decisionAuditPlan["failed_count_7d"], 0),
		"operation_run_count_7d":                   intFromAny(decisionAuditPlan["operation_run_count_7d"], 0),
		"matched_repo_sync_asset_count_7d":         intFromAny(decisionAuditPlan["matched_repo_sync_asset_count_7d"], 0),
		"local_volume_observed":                    boolOnlyFromAny(volumeEvidence["local_volume_observed"]),
		"repo_sync_volume_observed":                boolOnlyFromAny(volumeEvidence["repo_sync_volume_observed"]),
		"processed_or_bound_volume_observed":       boolOnlyFromAny(volumeEvidence["processed_or_bound_volume_observed"]),
		"provider_metrics_comparison_review_ready": boolOnlyFromAny(metricsComparisonPlan["comparison_ready_for_review"]),
		"threshold_configuration_written":          false,
		"provider_metrics_fetched":                 false,
		"provider_pair_limits_compared":            false,
		"external_call_made":                       false,
		"contains_token":                           false,
		"contains_secret":                          false,
		"contains_payload":                         false,
		"contains_provider_url":                    false,
		"raw_request_headers_recorded":             false,
		"raw_request_body_recorded":                false,
		"raw_provider_response_recorded":           false,
		"suppressed_fields":                        decisionAuditPlan["suppressed_fields"],
	}
	evidenceJSON, err := jsonParam(evidence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not serialize threshold decision audit evidence")
		return
	}
	actorID := ""
	if user := currentUser(r); user != nil {
		actorID = strings.TrimSpace(user.ID)
	}
	audit, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO webhook_threshold_decision_audits(
			project_id,
			webhook_connection_id,
			provider,
			threshold_review_state,
			decision_state,
			operator_decision,
			evidence_window,
			evidence,
			created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, NULLIF($9, '')::uuid)
		ON CONFLICT (webhook_connection_id, decision_state, evidence_window)
		DO UPDATE SET
			provider=EXCLUDED.provider,
			threshold_review_state=EXCLUDED.threshold_review_state,
			operator_decision=EXCLUDED.operator_decision,
			evidence=EXCLUDED.evidence,
			created_by=COALESCE(EXCLUDED.created_by, webhook_threshold_decision_audits.created_by)
		RETURNING id, project_id, webhook_connection_id, provider, threshold_review_state,
			decision_state, operator_decision, evidence_window, evidence, created_by, created_at`,
		projectID,
		connectionID,
		strings.TrimSpace(fmt.Sprint(connection["provider"])),
		cleanPreviewString(decisionAuditPlan["threshold_review_state"]),
		cleanPreviewString(decisionAuditPlan["decision_state"]),
		operatorDecision,
		evidenceWindow,
		evidenceJSON,
		actorID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record threshold decision audit")
		return
	}
	decisionAuditPlan["threshold_configuration_audit_inserted"] = true
	decisionAuditPlan["audit_insert_enabled"] = true
	decisionAuditPlan["operator_threshold_review_recorded"] = true
	writeJSON(w, http.StatusCreated, map[string]any{
		"audit":                         audit,
		"readiness":                     readiness,
		"threshold_decision_audit_plan": decisionAuditPlan,
	})
}

func (s *Server) applyWebhookThresholdConfiguration(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	connection, err := webhookConnectionWithCallbackReadiness(r.Context(), s.store.DB, connectionID, s.publicBaseURL())
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(connection["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ID: connectionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		EvidenceWindow string `json:"evidence_window"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	evidenceWindow := strings.TrimSpace(req.EvidenceWindow)
	if evidenceWindow == "" {
		evidenceWindow = "7d"
	}
	if !validWebhookEvidenceWindow(evidenceWindow) {
		writeError(w, http.StatusBadRequest, "invalid evidence window")
		return
	}
	readiness := mapFromAny(connection["callback_rehearsal"])
	providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
	thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if boolOnlyFromAny(configurationPlan["threshold_configuration_written"]) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "threshold configuration is already applied",
			"readiness": readiness,
		})
		return
	}
	if !boolOnlyFromAny(configurationPlan["configuration_write_enabled"]) ||
		!boolOnlyFromAny(decisionAuditPlan["operator_threshold_review_recorded"]) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "threshold configuration requires a recorded threshold decision audit",
			"readiness": readiness,
		})
		return
	}
	thresholds := webhookProviderCallbackCurrentThresholds()
	thresholdsJSON, err := jsonParam(thresholds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not serialize threshold configuration")
		return
	}
	evidence := map[string]any{
		"mode":                                     "provider_callback_threshold_configuration_evidence",
		"webhook_connection_id":                    connectionID,
		"provider":                                 strings.TrimSpace(fmt.Sprint(connection["provider"])),
		"evidence_window":                          evidenceWindow,
		"threshold_review_state":                   cleanPreviewString(decisionAuditPlan["threshold_review_state"]),
		"configuration_state":                      cleanPreviewString(decisionAuditPlan["configuration_state"]),
		"decision_state":                           cleanPreviewString(decisionAuditPlan["decision_state"]),
		"threshold_configuration_written":          true,
		"threshold_configuration_count":            len(thresholds),
		"operator_threshold_review_recorded":       true,
		"provider_metrics_fetched":                 false,
		"provider_pair_limits_compared":            false,
		"capacity_signals_recomputed":              true,
		"capacity_signal_recompute_mode":           "read_time_repo_sync_asset_detail",
		"external_call_made":                       false,
		"contains_token":                           false,
		"contains_secret":                          false,
		"contains_payload":                         false,
		"contains_provider_url":                    false,
		"raw_request_headers_recorded":             false,
		"raw_request_body_recorded":                false,
		"raw_provider_response_recorded":           false,
		"provider_metrics_comparison_review_ready": boolOnlyFromAny(decisionAuditPlan["decision_ready_for_review"]),
		"suppressed_fields":                        decisionAuditPlan["suppressed_fields"],
	}
	evidenceJSON, err := jsonParam(evidence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not serialize threshold configuration evidence")
		return
	}
	actorID := ""
	if user := currentUser(r); user != nil {
		actorID = strings.TrimSpace(user.ID)
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start threshold configuration transaction")
		return
	}
	defer tx.Rollback()
	configurations, err := queryMaps(r.Context(), tx, `
		WITH latest_audit AS (
			SELECT id
			FROM webhook_threshold_decision_audits
			WHERE webhook_connection_id=$2 AND evidence_window=$4
			ORDER BY created_at DESC
			LIMIT 1
		),
		thresholds AS (
			SELECT *
			FROM jsonb_to_recordset($5::jsonb) AS t(key text, warning_at integer, danger_at integer, unit text)
		)
		INSERT INTO webhook_threshold_configurations(
			project_id,
			webhook_connection_id,
			provider,
			threshold_key,
			warning_at,
			danger_at,
			unit,
			evidence_window,
			source_audit_id,
			evidence,
			applied_by
		)
		SELECT $1, $2, $3, thresholds.key, thresholds.warning_at, thresholds.danger_at,
			thresholds.unit, $4, latest_audit.id, $6::jsonb, NULLIF($7, '')::uuid
		FROM thresholds
		CROSS JOIN latest_audit
		ON CONFLICT (webhook_connection_id, threshold_key, evidence_window)
		DO UPDATE SET
			provider=EXCLUDED.provider,
			warning_at=EXCLUDED.warning_at,
			danger_at=EXCLUDED.danger_at,
			unit=EXCLUDED.unit,
			source_audit_id=EXCLUDED.source_audit_id,
			evidence=EXCLUDED.evidence,
			applied_by=COALESCE(EXCLUDED.applied_by, webhook_threshold_configurations.applied_by),
			applied_at=now()
		RETURNING id, project_id, webhook_connection_id, provider, threshold_key,
			warning_at, danger_at, unit, evidence_window, source_audit_id, evidence,
			applied_by, applied_at`,
		projectID,
		connectionID,
		strings.TrimSpace(fmt.Sprint(connection["provider"])),
		evidenceWindow,
		thresholdsJSON,
		evidenceJSON,
		actorID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not apply threshold configuration")
		return
	}
	if len(configurations) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           "threshold configuration requires a matching threshold decision audit",
			"evidence_window": evidenceWindow,
		})
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit threshold configuration")
		return
	}
	configurationPlan["threshold_configuration_written"] = true
	configurationPlan["configuration_state"] = "recorded"
	configurationPlan["threshold_configuration_count"] = len(configurations)
	configurationPlan["provider_metrics_fetched"] = false
	recomputeEvidence := webhookThresholdCapacityRecomputeEvidence(connection, len(configurations))
	configurationPlan["capacity_signals_recomputed"] = true
	configurationPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
	configurationPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
	configurationPlan["external_call_made"] = false
	decisionAuditPlan["threshold_configuration_written"] = true
	decisionAuditPlan["capacity_signals_recomputed"] = true
	decisionAuditPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
	decisionAuditPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
	configurationPlan["threshold_decision_audit_plan"] = decisionAuditPlan
	writeJSON(w, http.StatusCreated, map[string]any{
		"configurations":                       configurations,
		"readiness":                            readiness,
		"threshold_configuration_plan":         configurationPlan,
		"threshold_configuration_written":      true,
		"threshold_configuration_count":        len(configurations),
		"provider_metrics_fetched":             false,
		"provider_pair_limits_compared":        false,
		"capacity_signals_recomputed":          true,
		"capacity_signal_recompute_mode":       recomputeEvidence["recompute_mode"],
		"capacity_signal_recompute_evidence":   recomputeEvidence,
		"external_call_made":                   false,
		"raw_provider_response_recorded":       false,
		"raw_request_or_payload_body_recorded": false,
	})
}

func validWebhookEvidenceWindow(value string) bool {
	if len(value) < 2 {
		return false
	}
	unit := value[len(value)-1]
	if unit != 'h' && unit != 'd' && unit != 'w' && unit != 'm' {
		return false
	}
	amount, err := strconv.Atoi(value[:len(value)-1])
	return err == nil && amount > 0
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
		items = repoTagRunsWithRemoteRehearsal(items)
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
		items = repoTagRunsWithRemoteRehearsal(items)
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
		items = repoTagRunsWithRemoteRehearsal(items)
		writeQueryResult(w, items, err)
	}
}

func repoTagRunsWithRemoteRehearsal(items []map[string]any) []map[string]any {
	for _, item := range items {
		item["remote_rehearsal_plan"] = repoTagRemoteRehearsalPlan(item)
	}
	return items
}

func (s *Server) recordRepoTagRunResultSnapshot(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	projectID, err := repoTagRunProjectID(r.Context(), s.store.DB, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ID: runID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := RecordRepoTagRunResultSnapshot(r.Context(), s.store, RepoTagRunResultSnapshotOptions{RepoTagRunID: runID, DryRun: req.DryRun})
	if err != nil {
		writeError(w, http.StatusBadRequest, "record repo tag result snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordRepoTagRunActionsRefreshSnapshot(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	projectID, err := repoTagRunProjectID(r.Context(), s.store.DB, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ID: runID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := RecordRepoTagRunActionsRefreshSnapshot(r.Context(), s.store, RepoTagRunActionsRefreshSnapshotOptions{RepoTagRunID: runID, DryRun: req.DryRun})
	if err != nil {
		writeError(w, http.StatusBadRequest, "record repo tag actions refresh snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func repoTagRemoteRehearsalPlan(run map[string]any) map[string]any {
	status := strings.TrimSpace(stringFromMap(run, "status"))
	if status == "" {
		status = "unknown"
	}
	tagNameConfigured := strings.TrimSpace(stringFromMap(run, "tag_name")) != ""
	targetSHAConfigured := strings.TrimSpace(stringFromMap(run, "target_sha")) != ""
	targetRemoteBound := strings.TrimSpace(firstNonEmptyString(stringFromMap(run, "target_remote_id"), stringFromMap(run, "git_remote_id"))) != ""
	tagObserved := status == "completed" || status == "succeeded" || status == "success"
	tagFailed := status == "failed" || status == "error" || status == "canceled" || status == "cancelled"
	rehearsalState := "planned"
	if !tagNameConfigured || !targetRemoteBound {
		rehearsalState = "blocked"
	}
	if tagFailed {
		rehearsalState = "failed"
	}
	if tagObserved {
		rehearsalState = "observed"
	}
	blockedReasons := []string{}
	if !tagNameConfigured {
		blockedReasons = append(blockedReasons, "tag_name_missing")
	}
	if !targetRemoteBound {
		blockedReasons = append(blockedReasons, "target_remote_missing")
	}
	if !targetSHAConfigured {
		blockedReasons = append(blockedReasons, "target_sha_missing")
	}
	if !tagObserved {
		blockedReasons = append(blockedReasons, "live_remote_tag_success_not_observed")
	}
	if tagFailed {
		blockedReasons = append(blockedReasons, "live_remote_tag_failed_observed")
	}
	resultEvidence := repoTagRemoteResultEvidence(run, rehearsalState, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound)
	lookupPreflight := repoTagLiveRemoteLookupPreflight(rehearsalState, status, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound)
	return map[string]any{
		"mode":                             "repo_tag_remote_rehearsal_plan",
		"rehearsal_state":                  rehearsalState,
		"tag_run_status":                   status,
		"tag_name_configured":              tagNameConfigured,
		"target_sha_configured":            targetSHAConfigured,
		"target_remote_bound":              targetRemoteBound,
		"live_remote_tag_success_observed": tagObserved,
		"live_remote_tag_failed_observed":  tagFailed,
		"tag_result_evidence":              resultEvidence,
		"execution_enabled":                false,
		"external_call_made":               false,
		"git_tag_created":                  false,
		"git_push_performed":               false,
		"github_actions_refresh_performed": false,
		"remote_tag_lookup_performed":      false,
		"result_written":                   boolOnlyFromAny(resultEvidence["sanitized_result_recorded"]),
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_tag_message":             false,
		"required_controls": []string{
			"operation_approval",
			"target_remote_review",
			"git_credential_review",
			"tag_protection_review",
			"github_actions_refresh",
			"remote_tag_success_recording",
		},
		"live_rehearsal_sequence": []string{
			"approve_remote_tag_operation",
			"create_or_verify_remote_tag",
			"lookup_remote_tag_result",
			"classify_live_remote_tag_result",
			"persist_sanitized_tag_run_result",
			"refresh_github_actions_after_tag",
			"record_redacted_actions_refresh_result",
		},
		"disabled_backends": []string{
			"git_tag",
			"git_push",
			"remote_tag_lookup",
			"github_actions_api_sync",
			"repo_tag_run_update",
		},
		"suppressed_fields": []string{
			"tag_name",
			"target_sha",
			"remote_url",
			"git_credentials",
			"provider_token",
			"authorization_header",
			"tag_message",
			"git_output",
			"github_actions_response",
		},
		"blocked_reasons":              blockedReasons,
		"live_remote_lookup_preflight": lookupPreflight,
		"live_result_plan":             repoTagLiveResultPlan(rehearsalState, tagObserved, tagFailed, lookupPreflight),
		"actions_refresh_plan":         repoTagActionsRefreshPlan(rehearsalState, tagObserved, tagFailed, lookupPreflight),
		"result_recording_plan":        repoTagRemoteRehearsalResultRecordingPlan(resultEvidence, lookupPreflight),
		"message":                      "Remote tag success rehearsal is audit-only; no Git tag, push, provider refresh, or remote lookup is performed; existing repo tag run state is reconciled as sanitized result evidence.",
	}
}

func repoTagLiveRemoteLookupPreflight(rehearsalState, status string, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound bool) map[string]any {
	lookupState := "blocked"
	if tagNameConfigured && targetSHAConfigured && targetRemoteBound {
		lookupState = "planned"
	}
	if tagObserved {
		lookupState = "observed"
	}
	if tagFailed {
		lookupState = "failed"
	}
	// Keep this blocker even for observed local tag-run evidence: the plan proves
	// that no live remote lookup backend ran while reconciling sanitized rows.
	blockedReasons := []string{"remote_tag_lookup_backend_disabled"}
	if !tagNameConfigured {
		blockedReasons = append(blockedReasons, "tag_name_missing")
	}
	if !targetSHAConfigured {
		blockedReasons = append(blockedReasons, "target_sha_missing")
	}
	if !targetRemoteBound {
		blockedReasons = append(blockedReasons, "target_remote_missing")
	}
	if !tagObserved {
		blockedReasons = append(blockedReasons, "live_remote_tag_success_not_observed")
	}
	if tagFailed {
		blockedReasons = append(blockedReasons, "live_remote_tag_failed_observed")
	}
	return map[string]any{
		"mode":                             "repo_tag_live_remote_lookup_preflight",
		"lookup_state":                     lookupState,
		"tag_run_status":                   status,
		"rehearsal_state":                  rehearsalState,
		"lookup_ready_for_review":          tagNameConfigured && targetSHAConfigured && targetRemoteBound && !tagFailed,
		"tag_name_configured":              tagNameConfigured,
		"target_sha_configured":            targetSHAConfigured,
		"target_remote_bound":              targetRemoteBound,
		"live_remote_tag_success_observed": tagObserved,
		"live_remote_tag_failed_observed":  tagFailed,
		"remote_tag_lookup_performed":      false,
		"git_ls_remote_performed":          false,
		"provider_api_called":              false,
		"github_actions_refresh_performed": false,
		"repo_tag_run_update_performed":    false,
		"operation_log_written":            false,
		"external_call_made":               false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_target_sha":              false,
		"contains_tag_message":             false,
		"required_lookup_fields":           []string{"target_remote_id", "tag_name", "target_sha", "tag_run_status", "repository_binding", "provider_type"},
		"required_review_controls":         []string{"target_remote_review", "git_credential_review", "tag_ref_policy_review", "actions_refresh_scope_review", "sanitized_result_recording_review"},
		"disabled_backends":                []string{"remote_tag_lookup", "git_ls_remote", "provider_tag_lookup", "github_actions_api_sync", "repo_tag_run_update", "operation_log_write"},
		"suppressed_fields":                []string{"tag_name", "target_sha", "tag_message", "remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"},
		"blocked_reasons":                  blockedReasons,
		"message":                          "Live remote tag lookup preflight is review metadata only; no remote lookup, provider API call, Actions refresh, repo_tag_run update, or operation log write is performed.",
	}
}

func repoTagRemoteResultEvidence(run map[string]any, rehearsalState string, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound bool) map[string]any {
	status := strings.ToLower(strings.TrimSpace(stringFromMap(run, "status")))
	if status == "" {
		status = "unknown"
	}
	waiting := status == "queued" || status == "running" || status == "pending" || status == "unknown"
	state := "blocked"
	switch {
	case tagObserved:
		state = "recorded"
	case tagFailed:
		state = "failed"
	case waiting && tagNameConfigured && targetSHAConfigured && targetRemoteBound:
		state = "waiting_for_worker"
	}
	return map[string]any{
		"mode":                             "repo_tag_remote_result_evidence",
		"evidence_state":                   state,
		"tag_run_status":                   status,
		"tag_name_configured":              tagNameConfigured,
		"target_sha_configured":            targetSHAConfigured,
		"target_remote_bound":              targetRemoteBound,
		"operation_run_bound":              strings.TrimSpace(stringFromMap(run, "operation_run_id")) != "",
		"finished_at":                      run["finished_at"],
		"created_at":                       run["created_at"],
		"live_remote_tag_success_observed": tagObserved,
		"live_remote_tag_failed_observed":  tagFailed,
		"sanitized_result_recorded":        tagObserved || tagFailed,
		"waiting_for_worker":               state == "waiting_for_worker",
		"rehearsal_state":                  rehearsalState,
		"external_call_made":               false,
		"git_tag_created":                  false,
		"git_push_performed":               false,
		"remote_tag_lookup_performed":      false,
		"github_actions_refresh_performed": false,
		"raw_git_output_recorded":          false,
		"raw_provider_response_recorded":   false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_tag_message":             false,
		"suppressed_fields":                []string{"tag_name", "target_sha", "tag_message", "remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"},
	}
}

func repoTagLiveResultPlan(rehearsalState string, tagObserved, tagFailed bool, lookupPreflight map[string]any) map[string]any {
	resultState := "blocked"
	if rehearsalState == "observed" {
		resultState = "planned"
	}
	if tagFailed {
		resultState = "failed"
	}
	return map[string]any{
		"mode":                              "repo_tag_live_result_plan",
		"live_result_state":                 resultState,
		"live_remote_tag_success_observed":  tagObserved,
		"live_remote_tag_failed_observed":   tagFailed,
		"remote_tag_lookup_performed":       false,
		"repo_tag_run_result_write_planned": tagObserved,
		"repo_tag_run_result_written":       false,
		"operation_log_written":             false,
		"external_call_made":                false,
		"contains_token":                    false,
		"contains_remote_url":               false,
		"contains_ref_name":                 false,
		"contains_tag_message":              false,
		"required_controls": []string{
			"remote_tag_lookup",
			"tag_result_classification",
			"repo_tag_run_update",
			"operation_log_summary",
			"sanitized_result_recording",
		},
		"disabled_backends": []string{
			"remote_tag_lookup",
			"repo_tag_run_update",
			"operation_log_write",
		},
		"suppressed_fields": []string{
			"remote_url",
			"git_credentials",
			"provider_token",
			"authorization_header",
			"tag_message",
			"git_output",
		},
		"blocked_reasons":              repoTagLiveResultBlockedReasons(tagObserved, tagFailed),
		"execution_blockers":           []string{"live_remote_tag_result_write_not_performed"},
		"live_remote_lookup_preflight": lookupPreflight,
		"message":                      "Live remote tag result persistence is planned only; no remote lookup, repo_tag_run update, or operation log write is performed.",
	}
}

func repoTagLiveResultBlockedReasons(tagObserved, tagFailed bool) []string {
	if tagObserved {
		return []string{"repo_tag_run_result_update_not_wired"}
	}
	if tagFailed {
		return []string{"live_remote_tag_failed_observed", "repo_tag_run_result_update_not_wired"}
	}
	return []string{"live_remote_tag_success_not_observed", "repo_tag_run_result_update_not_wired"}
}

func repoTagActionsRefreshPlan(rehearsalState string, tagObserved, tagFailed bool, lookupPreflight map[string]any) map[string]any {
	refreshState := "blocked"
	if rehearsalState == "observed" {
		refreshState = "planned"
	}
	if tagFailed {
		refreshState = "failed"
	}
	return map[string]any{
		"mode":                               "repo_tag_github_actions_refresh_plan",
		"refresh_state":                      refreshState,
		"refresh_after_tag_success_required": true,
		"live_remote_tag_success_observed":   tagObserved,
		"live_remote_tag_failed_observed":    tagFailed,
		"github_actions_refresh_performed":   false,
		"github_action_runs_synced":          false,
		"repo_tag_run_link_written":          false,
		"external_call_made":                 false,
		"contains_token":                     false,
		"contains_remote_url":                false,
		"contains_provider_response":         false,
		"required_controls": []string{
			"github_actions_remote_review",
			"github_actions_api_sync",
			"action_run_linking",
			"sanitized_refresh_result_recording",
		},
		"disabled_backends": []string{
			"github_actions_api_sync",
			"github_action_run_link_write",
			"provider_response_recording",
		},
		"suppressed_fields": []string{
			"provider_token",
			"authorization_header",
			"remote_url",
			"github_actions_response",
			"provider_response_body",
			"provider_response_headers",
		},
		"blocked_reasons":              repoTagActionsRefreshBlockedReasons(tagObserved, tagFailed),
		"execution_blockers":           []string{"github_actions_refresh_not_performed"},
		"live_remote_lookup_preflight": lookupPreflight,
		"message":                      "GitHub Actions refresh after live tag success is planned only; no provider API call or action-run link write is performed.",
	}
}

func repoTagActionsRefreshBlockedReasons(tagObserved, tagFailed bool) []string {
	if tagObserved {
		return []string{"github_actions_refresh_not_performed"}
	}
	if tagFailed {
		return []string{"live_remote_tag_failed_observed", "github_actions_refresh_not_performed"}
	}
	return []string{"live_remote_tag_success_not_observed", "github_actions_refresh_not_performed"}
}

func repoTagRemoteRehearsalResultRecordingPlan(evidence map[string]any, lookupPreflight map[string]any) map[string]any {
	evidenceState := strings.TrimSpace(fmt.Sprint(evidence["evidence_state"]))
	resultRecorded := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	recordingState := "blocked"
	recordingReason := "remote_tag_rehearsal_execution_not_performed"
	switch evidenceState {
	case "recorded":
		recordingState = "recorded"
		recordingReason = "sanitized_remote_tag_success_observed"
	case "failed":
		recordingState = "failed"
		recordingReason = "sanitized_remote_tag_failure_observed"
	case "waiting_for_worker":
		recordingState = "waiting_for_worker"
		recordingReason = "remote_tag_run_waiting_for_worker"
	}
	return map[string]any{
		"mode":                            "repo_tag_remote_rehearsal_result_recording_plan",
		"result_recording_state":          recordingState,
		"result_recording_ready":          resultRecorded,
		"result_recording_ready_reason":   recordingReason,
		"recording_enabled":               resultRecorded,
		"result_written":                  resultRecorded,
		"repo_tag_run_updated":            false,
		"github_action_runs_synced":       false,
		"remote_tag_success_recorded":     evidenceState == "recorded",
		"live_result_subplan_recorded":    resultRecorded,
		"actions_refresh_result_recorded": false,
		"raw_git_output_recorded":         false,
		"raw_provider_response_recorded":  false,
		"contains_token":                  false,
		"contains_remote_url":             false,
		"contains_ref_name":               false,
		"contains_tag_message":            false,
		"tag_result_evidence":             evidence,
		"live_remote_lookup_preflight":    lookupPreflight,
		"result_recording_sequence": []string{
			"classify_remote_tag_result",
			"record_sanitized_tag_summary",
			"record_github_actions_refresh_summary",
			"persist_repo_tag_run_result",
		},
		"result_diagnostic_fields": []string{
			"tag_run_status",
			"tag_name_configured",
			"target_sha_configured",
			"target_remote_bound",
			"live_remote_tag_success_observed",
			"live_remote_tag_failed_observed",
			"live_result_state",
			"github_actions_refresh_status",
			"github_actions_refresh_state",
		},
		"suppressed_fields": []string{
			"remote_url",
			"git_credentials",
			"provider_token",
			"authorization_header",
			"tag_message",
			"git_output",
			"github_actions_response",
			"provider_response_body",
			"provider_response_headers",
		},
		"blocked_reasons": []string{
			"remote_tag_rehearsal_execution_not_performed",
			"repo_tag_run_result_update_not_wired",
			"github_actions_refresh_not_performed",
		},
		"message": "Remote tag rehearsal result recording reconciles sanitized repo_tag_run state only; no repo_tag_run update, GitHub Actions sync result, Git output, or provider response is persisted.",
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

func projectIDForProjectVersion(ctx context.Context, db sqlx.ExtContext, versionID string) (string, error) {
	version, err := queryOne(ctx, db, "SELECT project_id FROM project_versions WHERE id=$1", versionID)
	if err != nil {
		return "", err
	}
	projectID := strings.TrimSpace(fmt.Sprint(version["project_id"]))
	if projectID == "" || projectID == "<nil>" {
		return "", ErrNotFound
	}
	return projectID, nil
}

func projectVersionValidationPreview(version map[string]any, remotes, tagRuns, actionRuns, argoApps []map[string]any, argoConnections ...[]map[string]any) map[string]any {
	metadata := mapFromAny(version["metadata"])
	repositories := mapSliceFromAny(metadata["repositories"])
	items := make([]map[string]any, 0, len(repositories))
	ready, partial, blocked := 0, 0, 0
	for index, repoItem := range repositories {
		item := projectVersionValidationItem(index, repoItem, remotes, tagRuns, actionRuns, argoApps)
		switch item["status"] {
		case "ready":
			ready++
		case "partial":
			partial++
		default:
			blocked++
		}
		items = append(items, item)
	}
	var argoConnectionRows []map[string]any
	if len(argoConnections) > 0 {
		argoConnectionRows = argoConnections[0]
	}
	var refreshOperationRows []map[string]any
	if len(argoConnections) > 1 {
		refreshOperationRows = argoConnections[1]
	}
	var backgroundOperationRows []map[string]any
	if len(argoConnections) > 2 {
		backgroundOperationRows = argoConnections[2]
	}
	refreshSummary := projectVersionRefreshResultSummary(refreshOperationRows)
	backgroundSummary := projectVersionValidationRerunOperationSummary(backgroundOperationRows)
	refreshPlan := projectVersionProviderRefreshPlan(repositories, remotes, argoConnectionRows)
	overall := "blocked"
	switch {
	case len(items) > 0 && blocked == 0 && partial == 0:
		overall = "ready"
	case ready > 0 || partial > 0:
		overall = "partial"
	}
	rerunEvidence := projectVersionValidationRerunEvidence(refreshSummary, overall, len(items), ready, partial, blocked)
	backgroundRerunPlan := projectVersionBackgroundValidationRerunPlan(refreshSummary, rerunEvidence, backgroundSummary)
	attachProjectVersionRefreshResultSummary(refreshPlan, refreshSummary, rerunEvidence)
	attachProjectVersionBackgroundValidationRerunPlan(refreshPlan, backgroundRerunPlan)
	return map[string]any{
		"version_id":                          version["id"],
		"version":                             version["version"],
		"mode":                                "synced_state_validation_preview",
		"validation_state":                    overall,
		"external_call_made":                  false,
		"provider_api_called":                 false,
		"git_fetch_performed":                 false,
		"argocd_api_called":                   false,
		"validation_source":                   "local_synced_database_state",
		"repository_count":                    len(items),
		"ready_count":                         ready,
		"partial_count":                       partial,
		"blocked_count":                       blocked,
		"items":                               items,
		"provider_refresh_plan":               refreshPlan,
		"provider_refresh_result_summary":     refreshSummary,
		"background_validation_rerun_summary": backgroundSummary,
		"validation_rerun_evidence":           rerunEvidence,
		"background_validation_rerun_plan":    backgroundRerunPlan,
		"required_live_rehearsal":             stringSliceFromAny(refreshPlan["required_live_rehearsal"]),
	}
}

func projectVersionRefreshResultSummary(operations []map[string]any) map[string]any {
	statusCounts := map[string]any{}
	kindCounts := map[string]any{}
	kinds := []string{}
	items := make([]map[string]any, 0, len(operations))
	queued, running, completed, failed, canceled := 0, 0, 0, 0, 0
	for _, operation := range operations {
		status := strings.TrimSpace(fmt.Sprint(operation["status"]))
		if status == "" || status == "<nil>" {
			status = "unknown"
		}
		input := mapFromAny(operation["input"])
		refreshKind := strings.TrimSpace(fmt.Sprint(input["refresh_kind"]))
		if refreshKind == "" || refreshKind == "<nil>" {
			refreshKind = strings.TrimSpace(fmt.Sprint(operation["operation_type"]))
		}
		if refreshKind == "" || refreshKind == "<nil>" {
			refreshKind = "unknown"
		}
		if !stringInSlice(kinds, refreshKind) {
			kinds = append(kinds, refreshKind)
		}
		perKindCounts := mapFromAny(kindCounts[refreshKind])
		if len(perKindCounts) == 0 {
			perKindCounts = map[string]any{}
			kindCounts[refreshKind] = perKindCounts
		}
		statusCounts[status] = intFromAny(statusCounts[status], 0) + 1
		perKindCounts[status] = intFromAny(perKindCounts[status], 0) + 1
		switch status {
		case "queued":
			queued++
		case "running":
			running++
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled":
			canceled++
		}
		item := map[string]any{
			"operation_run_id":      operation["id"],
			"operation_type":        operation["operation_type"],
			"refresh_kind":          refreshKind,
			"status":                status,
			"started_at":            operation["started_at"],
			"finished_at":           operation["finished_at"],
			"created_at":            operation["created_at"],
			"updated_at":            operation["updated_at"],
			"raw_response_included": false,
			"secret_included":       false,
		}
		if status == "failed" {
			item["error_recorded"] = cleanOptionalText(fmt.Sprint(operation["error"])) != ""
		}
		items = append(items, item)
	}
	operationCount := len(operations)
	terminalCount := completed + failed + canceled
	activeCount := queued + running
	rerunStatus := "not_requested"
	if operationCount > 0 {
		rerunStatus = "waiting_for_workers"
		if activeCount == 0 {
			if failed > 0 {
				rerunStatus = "refresh_failed"
			} else if canceled > 0 {
				rerunStatus = "refresh_canceled"
			} else {
				rerunStatus = "recorded"
			}
		}
	}
	return map[string]any{
		"mode":                      "project_version_refresh_result_summary",
		"operation_count":           operationCount,
		"queued_count":              queued,
		"running_count":             running,
		"completed_count":           completed,
		"failed_count":              failed,
		"canceled_count":            canceled,
		"active_count":              activeCount,
		"terminal_count":            terminalCount,
		"validation_rerun_status":   rerunStatus,
		"validation_rerun_recorded": rerunStatus == "recorded",
		"has_refresh_failures":      failed > 0,
		"has_refresh_cancellations": canceled > 0,
		"refresh_kinds":             kinds,
		"status_counts":             statusCounts,
		"status_counts_by_kind":     kindCounts,
		"items":                     items,
		"raw_response_included":     false,
		"secret_included":           false,
		"suppressed_fields":         []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
	}
}

func projectVersionValidationRerunEvidence(summary map[string]any, validationState string, repositoryCount, readyCount, partialCount, blockedCount int) map[string]any {
	rerunStatus := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	operationCount := intFromAny(summary["operation_count"], 0)
	activeCount := intFromAny(summary["active_count"], 0)
	serverSideRecheck := operationCount > 0
	if rerunStatus == "" || rerunStatus == "<nil>" {
		rerunStatus = "not_requested"
	}
	return map[string]any{
		"mode":                                   "project_version_validation_rerun_evidence",
		"rerun_state":                            rerunStatus,
		"rerun_source":                           "validation_preview_request",
		"server_side_validation_recheck":         serverSideRecheck,
		"server_side_validation_recheck_ready":   operationCount > 0 && activeCount == 0,
		"automatic_background_rerun":             false,
		"control_worker_auto_snapshot_supported": true,
		"validation_rerun_recorded":              rerunStatus == "recorded",
		"provider_refresh_operation_observed":    operationCount > 0,
		"provider_refresh_active":                activeCount > 0,
		"provider_refresh_terminal":              operationCount > 0 && activeCount == 0,
		"provider_refresh_status":                rerunStatus,
		"operation_count":                        operationCount,
		"active_count":                           activeCount,
		"validation_state":                       validationState,
		"repository_count":                       repositoryCount,
		"ready_count":                            readyCount,
		"partial_count":                          partialCount,
		"blocked_count":                          blockedCount,
		"validation_source":                      "local_synced_database_state",
		"external_call_made":                     false,
		"provider_api_called":                    false,
		"git_fetch_performed":                    false,
		"argocd_api_called":                      false,
		"raw_response_included":                  false,
		"secret_included":                        false,
		"suppressed_fields":                      []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"message":                                "This validation response is a server-side recheck of local synced database state; the control worker can auto-record a sanitized snapshot after terminal refresh workers, and standalone background rerun can enqueue the same local-only snapshot recorder without raw provider response binding.",
	}
}

func projectVersionValidationRerunOperationSummary(operations []map[string]any) map[string]any {
	queued, running, completed, failed, canceled := 0, 0, 0, 0, 0
	items := make([]map[string]any, 0, len(operations))
	latestState := "not_requested"
	snapshotWritten := false
	for _, operation := range operations {
		status := cleanPreviewString(operation["status"])
		if status == "" {
			status = "unknown"
		}
		switch status {
		case "queued":
			queued++
		case "running":
			running++
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled":
			canceled++
		}
		result := mapFromAny(operation["result"])
		operationResult := mapFromAny(result["operation_result"])
		validationSnapshotWritten := boolOnlyFromAny(result["validation_snapshot_written"]) ||
			boolOnlyFromAny(operationResult["validation_snapshot_written"])
		if validationSnapshotWritten {
			snapshotWritten = true
		}
		item := map[string]any{
			"operation_run_id":               operation["id"],
			"status":                         status,
			"created_at":                     operation["created_at"],
			"updated_at":                     operation["updated_at"],
			"started_at":                     operation["started_at"],
			"finished_at":                    operation["finished_at"],
			"validation_snapshot_written":    validationSnapshotWritten,
			"recording_state":                firstNonEmptyString(cleanPreviewString(result["recording_state"]), cleanPreviewString(operationResult["recording_state"])),
			"raw_response_included":          false,
			"secret_included":                false,
			"external_call_made":             false,
			"provider_api_called":            false,
			"raw_provider_response_recorded": false,
		}
		if status == "failed" {
			item["error_recorded"] = cleanOptionalText(fmt.Sprint(operation["error"])) != ""
		}
		items = append(items, item)
	}
	activeCount := queued + running
	operationCount := len(operations)
	switch {
	case operationCount == 0:
		latestState = "not_requested"
	case activeCount > 0:
		latestState = "waiting_for_worker"
	case failed > 0:
		latestState = "failed"
	case canceled > 0:
		latestState = "canceled"
	default:
		latestState = "recorded"
	}
	return map[string]any{
		"mode":                        "project_version_background_validation_rerun_summary",
		"operation_count":             operationCount,
		"queued_count":                queued,
		"running_count":               running,
		"completed_count":             completed,
		"failed_count":                failed,
		"canceled_count":              canceled,
		"active_count":                activeCount,
		"terminal_count":              completed + failed + canceled,
		"background_rerun_state":      latestState,
		"background_worker_enqueued":  operationCount > 0,
		"automatic_background_rerun":  operationCount > 0,
		"validation_snapshot_written": snapshotWritten,
		"raw_response_included":       false,
		"secret_included":             false,
		"external_call_made":          false,
		"provider_api_called":         false,
		"suppressed_fields":           []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"items":                       items,
	}
}

func projectVersionBackgroundValidationRerunPlan(summary map[string]any, rerunEvidence map[string]any, backgroundSummaries ...map[string]any) map[string]any {
	rerunStatus := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	if rerunStatus == "" || rerunStatus == "<nil>" {
		rerunStatus = "not_requested"
	}
	backgroundSummary := map[string]any{}
	if len(backgroundSummaries) > 0 {
		backgroundSummary = backgroundSummaries[0]
	}
	operationCount := intFromAny(summary["operation_count"], 0)
	activeCount := intFromAny(summary["active_count"], 0)
	terminal := operationCount > 0 && activeCount == 0
	backgroundOperationCount := intFromAny(backgroundSummary["operation_count"], 0)
	backgroundActiveCount := intFromAny(backgroundSummary["active_count"], 0)
	backgroundState := cleanPreviewString(backgroundSummary["background_rerun_state"])
	backgroundSnapshotWritten := boolOnlyFromAny(backgroundSummary["validation_snapshot_written"])
	snapshotWritePlan := projectVersionValidationSnapshotWritePlan(summary, rerunEvidence, terminal)
	planState := "blocked"
	blockedReasons := []string{"provider_refresh_execution_not_performed", "background_validation_rerun_disabled"}
	switch rerunStatus {
	case "waiting_for_workers":
		planState = "waiting_for_workers"
		blockedReasons = []string{"refresh_workers_still_running", "background_validation_rerun_disabled"}
	case "recorded":
		planState = "ready_for_operator_review"
		blockedReasons = []string{"background_validation_rerun_disabled", "validation_snapshot_write_disabled"}
	case "refresh_failed":
		planState = "blocked"
		blockedReasons = []string{"refresh_worker_failed", "background_validation_rerun_disabled"}
	case "refresh_canceled":
		planState = "blocked"
		blockedReasons = []string{"refresh_worker_canceled", "background_validation_rerun_disabled"}
	}
	if backgroundOperationCount > 0 {
		switch backgroundState {
		case "waiting_for_worker":
			planState = "waiting_for_worker"
			blockedReasons = []string{"background_validation_worker_running"}
		case "recorded":
			planState = "recorded"
			blockedReasons = []string{}
		case "failed":
			planState = "failed"
			blockedReasons = []string{"background_validation_worker_failed"}
		case "canceled":
			planState = "canceled"
			blockedReasons = []string{"background_validation_worker_canceled"}
		}
	}
	return map[string]any{
		"mode":                                    "project_version_background_validation_rerun_plan",
		"plan_state":                              planState,
		"rerun_status":                            rerunStatus,
		"background_rerun_ready_for_review":       rerunStatus == "recorded" && terminal,
		"automatic_background_rerun":              backgroundOperationCount > 0,
		"background_worker_enqueued":              backgroundOperationCount > 0,
		"standalone_background_worker_enabled":    true,
		"background_worker_active":                backgroundActiveCount > 0,
		"background_rerun_state":                  backgroundState,
		"background_validation_rerun_summary":     backgroundSummary,
		"control_worker_auto_snapshot_supported":  true,
		"control_worker_auto_snapshot_ready":      rerunStatus == "recorded" && terminal,
		"control_worker_auto_snapshot_trigger":    "refresh_worker_completion",
		"validation_snapshot_written":             backgroundSnapshotWritten,
		"validation_snapshot_write_plan":          snapshotWritePlan,
		"validation_rerun_recorded":               boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"server_side_validation_recheck_observed": boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"]),
		"server_side_validation_recheck_ready":    boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]),
		"provider_refresh_operation_observed":     operationCount > 0,
		"provider_refresh_terminal":               terminal,
		"operation_count":                         operationCount,
		"active_count":                            activeCount,
		"external_call_made":                      false,
		"provider_api_called":                     false,
		"git_fetch_performed":                     false,
		"argocd_api_called":                       false,
		"raw_response_included":                   false,
		"secret_included":                         false,
		"required_controls": []string{
			"terminal_refresh_workers",
			"server_side_validation_recheck",
			"validation_snapshot_write_audit",
			"control_worker_auto_snapshot_review",
			"standalone_background_worker_policy_review",
		},
		"rerun_sequence": []string{
			"observe_terminal_refresh_workers",
			"rerun_validation_against_synced_state",
			"record_validation_snapshot",
			"control_worker_auto_record_validation_snapshot",
			"publish_background_rerun_result",
		},
		"disabled_backends": []string{"raw_provider_response_recording"},
		"suppressed_fields": []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"blocked_reasons":   blockedReasons,
		"message":           "Standalone background validation rerun can now enqueue a control-worker job that rereads local synced state and records a sanitized ProjectVersion validation snapshot without provider calls.",
	}
}

func projectVersionValidationSnapshotWritePlan(summary map[string]any, rerunEvidence map[string]any, terminal bool) map[string]any {
	rerunStatus := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	if rerunStatus == "" || rerunStatus == "<nil>" {
		rerunStatus = "not_requested"
	}
	snapshotState := "blocked"
	if rerunStatus == "waiting_for_workers" {
		snapshotState = "waiting_for_workers"
	} else if rerunStatus == "recorded" && terminal {
		snapshotState = "metadata_review_ready"
	}
	reviewReady := snapshotState == "metadata_review_ready" &&
		boolOnlyFromAny(summary["validation_rerun_recorded"]) &&
		boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"])
	blockedReasons := []string{"validation_snapshot_write_disabled"}
	if rerunStatus == "not_requested" {
		blockedReasons = append(blockedReasons, "provider_refresh_execution_not_performed")
	}
	if rerunStatus == "waiting_for_workers" {
		blockedReasons = append(blockedReasons, "refresh_workers_still_running")
	}
	if rerunStatus == "refresh_failed" {
		blockedReasons = append(blockedReasons, "refresh_worker_failed")
	}
	if rerunStatus == "refresh_canceled" {
		blockedReasons = append(blockedReasons, "refresh_worker_canceled")
	}
	if !boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) {
		blockedReasons = append(blockedReasons, "server_side_validation_recheck_not_terminal")
	}
	if !boolOnlyFromAny(summary["validation_rerun_recorded"]) {
		blockedReasons = append(blockedReasons, "validation_rerun_not_recorded")
	}
	return map[string]any{
		"mode":                                    "project_version_validation_snapshot_write_plan",
		"snapshot_state":                          snapshotState,
		"snapshot_ready_for_review":               reviewReady,
		"snapshot_write_enabled":                  false,
		"validation_snapshot_written":             false,
		"asset_status_snapshot_written":           false,
		"operation_log_written":                   false,
		"background_worker_enqueued":              false,
		"automatic_background_rerun":              false,
		"standalone_background_worker_enabled":    true,
		"control_worker_auto_snapshot_supported":  true,
		"control_worker_auto_snapshot_ready":      reviewReady,
		"server_side_validation_recheck_observed": boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"]),
		"server_side_validation_recheck_ready":    boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]),
		"validation_rerun_recorded":               boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"provider_refresh_terminal":               terminal,
		"provider_refresh_status":                 rerunStatus,
		"operation_count":                         intFromAny(summary["operation_count"], 0),
		"active_count":                            intFromAny(summary["active_count"], 0),
		"repository_count":                        intFromAny(rerunEvidence["repository_count"], 0),
		"ready_count":                             intFromAny(rerunEvidence["ready_count"], 0),
		"partial_count":                           intFromAny(rerunEvidence["partial_count"], 0),
		"blocked_count":                           intFromAny(rerunEvidence["blocked_count"], 0),
		"validation_state":                        rerunEvidence["validation_state"],
		"validation_source":                       "local_synced_database_state",
		"external_call_made":                      false,
		"provider_api_called":                     false,
		"git_fetch_performed":                     false,
		"argocd_api_called":                       false,
		"raw_response_included":                   false,
		"secret_included":                         false,
		"required_snapshot_fields":                []string{"project_version_id", "validation_state", "repository_count", "ready_count", "partial_count", "blocked_count", "provider_refresh_status", "operation_count", "server_side_validation_recheck_status"},
		"required_controls":                       []string{"terminal_refresh_workers", "server_side_validation_recheck", "snapshot_schema_review", "snapshot_operator_review", "asset_status_snapshot_audit", "operation_log_redaction_review"},
		"disabled_backends":                       []string{"operation_log_write", "raw_provider_response_recording"},
		"suppressed_fields":                       []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body", "repository_ref"},
		"blocked_reasons":                         blockedReasons,
		"message":                                 "Metadata-only validation snapshot write preflight; standalone background workers may record asset status snapshots, but no operation log raw output, raw provider response, Git output, Argo response, URL, credential, or workflow log is written.",
	}
}

func attachProjectVersionRefreshResultSummary(refreshPlan map[string]any, summary map[string]any, rerunEvidence map[string]any) {
	if refreshPlan == nil || summary == nil {
		return
	}
	refreshPlan["result_summary"] = summary
	refreshPlan["validation_rerun_evidence"] = rerunEvidence
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if len(executionPlan) == 0 {
		return
	}
	workerBindingEvidence := projectVersionRefreshWorkerResultBindingEvidence(summary, stringSliceFromAny(executionPlan["planned_refresh_kinds"]))
	refreshPlan["worker_result_binding_evidence"] = workerBindingEvidence
	operationCount := intFromAny(summary["operation_count"], 0)
	validationRecorded := summary["validation_rerun_recorded"] == true
	executionPlan["operation_enqueued"] = operationCount > 0
	executionPlan["worker_job_created"] = operationCount > 0
	executionPlan["validation_reopened"] = validationRecorded
	executionPlan["refresh_result_summary"] = summary
	executionPlan["worker_result_binding_evidence"] = workerBindingEvidence
	executionPlan["worker_result_binding_state"] = workerBindingEvidence["binding_state"]
	executionPlan["server_side_validation_recheck_observed"] = boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"])
	executionPlan["automatic_background_rerun"] = boolOnlyFromAny(rerunEvidence["automatic_background_rerun"])
	executionPlan["validation_rerun_evidence"] = rerunEvidence
	if resultPlan := mapFromAny(executionPlan["result_recording_plan"]); len(resultPlan) > 0 {
		resultPlan["result_recording_state"] = projectVersionRefreshResultRecordingState(summary)
		resultPlan["result_recording_ready"] = operationCount > 0
		resultPlan["result_recording_ready_reason"] = projectVersionRefreshResultRecordingReason(summary)
		resultPlan["recording_enabled"] = operationCount > 0
		resultPlan["result_written"] = operationCount > 0
		resultPlan["operation_log_written"] = operationCount > 0
		resultPlan["canonical_asset_sync_queued"] = operationCount > 0
		resultPlan["status_snapshot_written"] = operationCount > 0
		resultPlan["validation_rerun_recorded"] = validationRecorded
		resultPlan["git_ref_fetch_result_recorded"] = projectVersionRefreshKindObserved(summary, "git_ref_fetch")
		resultPlan["github_actions_result_recorded"] = projectVersionRefreshKindObserved(summary, "github_actions_api_refresh")
		resultPlan["argo_revision_result_recorded"] = projectVersionRefreshKindObserved(summary, "argocd_app_refresh")
		resultPlan["refresh_result_summary"] = summary
		resultPlan["worker_result_binding_evidence"] = workerBindingEvidence
		resultPlan["worker_result_binding_state"] = workerBindingEvidence["binding_state"]
		resultPlan["server_side_validation_recheck_observed"] = boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"])
		resultPlan["automatic_background_rerun"] = boolOnlyFromAny(rerunEvidence["automatic_background_rerun"])
		resultPlan["validation_rerun_evidence"] = rerunEvidence
		resultPlan["blocked_reasons"] = projectVersionRefreshResultBlockedReasons(summary)
	}
}

func attachProjectVersionBackgroundValidationRerunPlan(refreshPlan map[string]any, backgroundPlan map[string]any) {
	if refreshPlan == nil || backgroundPlan == nil {
		return
	}
	refreshPlan["background_validation_rerun_plan"] = backgroundPlan
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if len(executionPlan) == 0 {
		return
	}
	executionPlan["background_validation_rerun_plan"] = backgroundPlan
	executionPlan["background_validation_rerun_state"] = backgroundPlan["plan_state"]
	if resultPlan := mapFromAny(executionPlan["result_recording_plan"]); len(resultPlan) > 0 {
		resultPlan["background_validation_rerun_plan"] = backgroundPlan
		resultPlan["background_validation_rerun_state"] = backgroundPlan["plan_state"]
		resultPlan["background_validation_rerun_ready_for_review"] = backgroundPlan["background_rerun_ready_for_review"]
	}
}

func projectVersionRefreshWorkerResultBindingEvidence(summary map[string]any, plannedKinds []string) map[string]any {
	operationCount := intFromAny(summary["operation_count"], 0)
	activeCount := intFromAny(summary["active_count"], 0)
	failedCount := intFromAny(summary["failed_count"], 0)
	canceledCount := intFromAny(summary["canceled_count"], 0)
	observedKinds := stringSliceFromAny(summary["refresh_kinds"])
	missingKinds := []string{}
	for _, kind := range plannedKinds {
		if strings.TrimSpace(kind) == "" {
			continue
		}
		if !stringInSlice(observedKinds, kind) {
			missingKinds = append(missingKinds, kind)
		}
	}
	allPlannedObserved := len(plannedKinds) > 0 && len(missingKinds) == 0
	bindingState := "not_recorded"
	switch {
	case operationCount == 0:
		bindingState = "not_recorded"
	case activeCount > 0:
		bindingState = "waiting_for_workers"
	case failedCount > 0:
		bindingState = "failed"
	case canceledCount > 0:
		bindingState = "canceled"
	case allPlannedObserved:
		bindingState = "recorded"
	default:
		bindingState = "partial_recorded"
	}
	return map[string]any{
		"mode":                            "project_version_refresh_worker_result_binding_evidence",
		"binding_state":                   bindingState,
		"project_version_scope_bound":     operationCount > 0,
		"operation_result_bound":          operationCount > 0,
		"worker_result_observed":          operationCount > 0,
		"terminal_worker_result_observed": operationCount > 0 && activeCount == 0,
		"all_planned_results_observed":    allPlannedObserved,
		"planned_refresh_kinds":           plannedKinds,
		"observed_refresh_kinds":          observedKinds,
		"missing_planned_result_kinds":    missingKinds,
		"operation_count":                 operationCount,
		"active_count":                    activeCount,
		"terminal_count":                  intFromAny(summary["terminal_count"], 0),
		"failed_count":                    failedCount,
		"canceled_count":                  canceledCount,
		"validation_rerun_status":         summary["validation_rerun_status"],
		"validation_rerun_recorded":       boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"external_call_made":              false,
		"provider_api_called":             false,
		"git_fetch_performed":             false,
		"argocd_api_called":               false,
		"raw_response_included":           false,
		"raw_git_output_included":         false,
		"raw_argo_response_included":      false,
		"secret_included":                 false,
		"contains_remote_url":             false,
		"contains_provider_token":         false,
		"contains_provider_response":      false,
		"suppressed_fields":               []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body", "operation_error_detail"},
		"message":                         "ProjectVersion refresh worker result binding is reconciled from operation kind/status metadata only; provider responses, Git output, Argo responses, URLs, credentials, and workflow logs remain suppressed.",
	}
}

func projectVersionRefreshResultRecordingState(summary map[string]any) string {
	switch strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"])) {
	case "recorded":
		return "recorded"
	case "waiting_for_workers":
		return "waiting"
	case "refresh_failed":
		return "failed"
	case "refresh_canceled":
		return "canceled"
	default:
		return "blocked"
	}
}

func projectVersionRefreshResultRecordingReason(summary map[string]any) string {
	switch strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"])) {
	case "recorded":
		return "validation_rerun_recorded"
	case "waiting_for_workers":
		return "refresh_workers_still_running"
	case "refresh_failed":
		return "refresh_worker_failed"
	case "refresh_canceled":
		return "refresh_worker_canceled"
	default:
		return "provider_refresh_execution_not_performed"
	}
}

func projectVersionRefreshKindObserved(summary map[string]any, kind string) bool {
	counts := mapFromAny(mapFromAny(summary["status_counts_by_kind"])[kind])
	total := 0
	for _, value := range counts {
		total += intFromAny(value, 0)
	}
	return total > 0
}

func projectVersionRefreshResultBlockedReasons(summary map[string]any) []string {
	switch strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"])) {
	case "recorded":
		return []string{}
	case "waiting_for_workers":
		return []string{"refresh_workers_still_running"}
	case "refresh_failed":
		return []string{"refresh_worker_failed"}
	case "refresh_canceled":
		return []string{"refresh_worker_canceled"}
	default:
		return []string{"provider_refresh_execution_not_performed", "synced_state_write_not_performed", "validation_auto_reload_not_observed"}
	}
}

func projectVersionProviderRefreshPlan(repositories, remotes, argoConnections []map[string]any) map[string]any {
	steps := []map[string]any{}
	addStep := func(step map[string]any) {
		step["external_call_made"] = false
		step["secret_included"] = false
		steps = append(steps, step)
	}
	for index, manifest := range repositories {
		remoteID := strings.TrimSpace(stringFromMap(manifest, "remote_id"))
		remote := findRowByID(remotes, remoteID)
		stepBase := map[string]any{
			"index":      index,
			"repo_key":   manifest["repo_key"],
			"repo_role":  manifest["repo_role"],
			"remote_id":  remoteID,
			"remote_key": manifest["remote_key"],
		}
		if remote == nil {
			if remoteID != "" {
				step := cloneMap(stepBase)
				step["kind"] = "remote_missing"
				step["status"] = "blocked"
				step["reason"] = "manifest remote must exist before provider refresh can be planned"
				addStep(step)
			}
			continue
		}
		if strings.TrimSpace(fmt.Sprint(stepBase["remote_key"])) == "" {
			stepBase["remote_key"] = stringFromMap(remote, "remote_key")
		}
		commitSHA := strings.TrimSpace(firstNonEmptyString(stringFromMap(manifest, "commit_sha"), stringFromMap(manifest, "config_commit_sha")))
		tagName := strings.TrimSpace(stringFromMap(manifest, "tag"))
		actionRunID := strings.TrimSpace(stringFromMap(manifest, "github_action_run_id"))
		argoRevision := strings.TrimSpace(stringFromMap(manifest, "argo_revision"))
		if commitSHA != "" || tagName != "" {
			step := cloneMap(stepBase)
			step["kind"] = "git_ref_fetch"
			step["status"] = "planned"
			step["reason"] = "refresh remote refs before comparing manifest commit or tag"
			step["refresh_endpoint"] = "/api/project-versions/{id}/refresh"
			step["commit_sha"] = commitSHA
			step["tag_name"] = tagName
			step["commit_sha_configured"] = commitSHA != ""
			step["tag_configured"] = tagName != ""
			addStep(step)
		}
		if actionRunID != "" {
			step := cloneMap(stepBase)
			step["kind"] = "github_actions_api_refresh"
			if strings.EqualFold(strings.TrimSpace(stringFromMap(remote, "provider_type")), "github") {
				step["status"] = "planned"
				step["refresh_endpoint"] = "/api/git-remotes/" + remoteID + "/github-actions/sync"
				step["reason"] = "refresh GitHub Actions runs before validating the manifest run id"
			} else {
				step["status"] = "blocked"
				step["reason"] = "GitHub Actions refresh requires a GitHub remote"
			}
			addStep(step)
		}
		if argoRevision != "" {
			step := cloneMap(stepBase)
			step["kind"] = "argocd_app_refresh"
			step["candidate_connection_count"] = len(argoConnections)
			if len(argoConnections) > 0 {
				step["status"] = "planned"
				step["reason"] = "refresh Argo apps before validating the manifest revision"
			} else {
				step["status"] = "blocked"
				step["reason"] = "Argo revision validation requires at least one project Argo connection"
			}
			addStep(step)
		}
	}
	required := []string{}
	planned, blocked := 0, 0
	for _, step := range steps {
		kind := strings.TrimSpace(fmt.Sprint(step["kind"]))
		if kind != "" && kind != "remote_missing" && !stringInSlice(required, kind) {
			required = append(required, kind)
		}
		if step["status"] == "planned" {
			planned++
		} else {
			blocked++
		}
	}
	state := "blocked"
	switch {
	case len(steps) > 0 && blocked == 0:
		state = "planned"
	case planned > 0:
		state = "partial"
	}
	executionPlan := projectVersionProviderRefreshExecutionPlan(steps, state)
	return map[string]any{
		"mode":                     "provider_refresh_plan_preview",
		"plan_state":               state,
		"external_call_made":       false,
		"provider_api_called":      false,
		"git_fetch_performed":      false,
		"argocd_api_called":        false,
		"planned_count":            planned,
		"blocked_count":            blocked,
		"step_count":               len(steps),
		"steps":                    steps,
		"required_live_rehearsal":  required,
		"execution_plan":           executionPlan,
		"required_operator_action": "Run the planned refresh operations and keep this validation preview open so the UI can auto-reload observed refresh results.",
	}
}

func projectVersionProviderRefreshExecutionPlan(steps []map[string]any, refreshPlanState string) map[string]any {
	plannedKinds := []string{}
	blockedKinds := []string{}
	plannedTotal, blockedTotal := 0, 0
	for _, step := range steps {
		kind := strings.TrimSpace(fmt.Sprint(step["kind"]))
		if kind == "" {
			continue
		}
		switch strings.TrimSpace(fmt.Sprint(step["status"])) {
		case "planned":
			plannedTotal++
			if !stringInSlice(plannedKinds, kind) {
				plannedKinds = append(plannedKinds, kind)
			}
		default:
			blockedTotal++
			if kind == "remote_missing" {
				continue
			}
			if !stringInSlice(blockedKinds, kind) {
				blockedKinds = append(blockedKinds, kind)
			}
		}
	}
	executionState := "blocked"
	if refreshPlanState == "planned" {
		executionState = "ready_for_approval"
	} else if refreshPlanState == "partial" {
		executionState = "partial"
	}
	workerBindingEvidence := projectVersionRefreshWorkerResultBindingEvidence(projectVersionRefreshResultSummary(nil), plannedKinds)
	return map[string]any{
		"mode":                             "provider_refresh_execution_plan_preview",
		"execution_state":                  executionState,
		"refresh_plan_state":               refreshPlanState,
		"execution_enabled":                plannedTotal > 0,
		"external_call_made":               false,
		"operation_enqueued":               false,
		"worker_job_created":               false,
		"validation_auto_reload_supported": true,
		"server_side_validation_rerun":     false,
		"git_fetch_performed":              false,
		"provider_api_called":              false,
		"argocd_api_called":                false,
		"synced_state_written":             false,
		"validation_reopened":              false,
		"secret_included":                  false,
		"planned_step_count":               plannedTotal,
		"blocked_step_count":               blockedTotal,
		"unique_planned_kind_count":        len(plannedKinds),
		"unique_blocked_kind_count":        len(blockedKinds),
		"planned_refresh_kinds":            plannedKinds,
		"blocked_refresh_kinds":            blockedKinds,
		"worker_result_binding_evidence":   workerBindingEvidence,
		"worker_result_binding_state":      workerBindingEvidence["binding_state"],
		"required_controls":                []string{"operation_approval", "provider_account_binding", "git_remote_credential_review", "github_actions_scope_review", "argo_connection_review", "result_recording_audit", "ui_auto_validation_reload"},
		"disabled_backends":                []string{"provider_mutation", "raw_provider_response_recording", "server_side_automatic_validation_rerun"},
		"suppressed_fields":                []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "github_actions_response", "argo_response", "commit_body", "workflow_logs"},
		"blocked_reasons":                  []string{"refresh_operations_not_enqueued", "validation_auto_reload_not_observed"},
		"execution_sequence":               []string{"request_project_version_refresh", "enqueue_git_ref_refresh", "enqueue_github_actions_refresh", "enqueue_argocd_app_refresh", "worker_records_synced_state", "rerun_validation_preview"},
		"git_ref_fetch_plan":               providerRefreshKindExecutionPlan("git_ref_fetch", plannedKinds, blockedKinds),
		"github_actions_refresh_plan":      providerRefreshKindExecutionPlan("github_actions_api_refresh", plannedKinds, blockedKinds),
		"argo_revision_refresh_plan":       providerRefreshKindExecutionPlan("argocd_app_refresh", plannedKinds, blockedKinds),
		"result_recording_plan":            projectVersionProviderRefreshResultRecordingPlan(plannedKinds),
		"message":                          "Provider refresh execution can enqueue fetch-only Git ref refresh, GitHub Actions sync, and Argo app sync worker jobs; this preview performs no external call and the UI can automatically reload validation after workers finish.",
		"required_operator_action":         "Run ProjectVersion provider refresh and keep this validation panel open so the UI can reload validation until refresh operations finish.",
		"requires_project_visibility":      true,
		"requires_manifest_consistency":    true,
	}
}

func providerRefreshKindExecutionPlan(kind string, plannedKinds, blockedKinds []string) map[string]any {
	state := "not_required"
	if stringInSlice(plannedKinds, kind) {
		state = "planned"
	} else if stringInSlice(blockedKinds, kind) {
		state = "blocked"
	}
	switch kind {
	case "git_ref_fetch":
		return map[string]any{
			"mode":                       "provider_refresh_git_ref_fetch_plan",
			"kind":                       kind,
			"refresh_state":              state,
			"operation_enqueued":         false,
			"worker_job_created":         false,
			"fetch_only_backend_enabled": state == "planned",
			"git_fetch_performed":        false,
			"git_remote_sync_performed":  false,
			"remote_ref_verified":        false,
			"synced_state_write_planned": state == "planned",
			"synced_state_written":       false,
			"external_call_made":         false,
			"contains_remote_url":        false,
			"contains_git_credentials":   false,
			"contains_commit_body":       false,
			"required_controls":          []string{"git_remote_credential_review", "ref_validation_policy", "synced_state_write_audit"},
			"disabled_backends":          []string{"git_push", "remote_mutation", "raw_git_output_recording", "server_side_automatic_validation_rerun"},
			"suppressed_fields":          []string{"remote_url", "git_credentials", "authorization_header", "commit_body", "raw_git_output"},
			"blocked_reasons":            providerRefreshKindBlockedReasons(state, "git_ref_fetch_not_enqueued"),
			"execution_blockers":         []string{"git_ref_fetch_not_enqueued", "server_side_validation_rerun_not_performed"},
			"message":                    "Git ref refresh can enqueue a fetch-only worker job; this preview does not fetch, expose remote URL, push refs, or rerun validation server-side.",
		}
	case "github_actions_api_refresh":
		return map[string]any{
			"mode":                          "provider_refresh_github_actions_plan",
			"kind":                          kind,
			"refresh_state":                 state,
			"operation_enqueued":            false,
			"worker_job_created":            false,
			"github_actions_sync_enabled":   state == "planned",
			"github_actions_api_called":     false,
			"github_actions_runs_synced":    false,
			"github_actions_scope_verified": false,
			"synced_state_write_planned":    state == "planned",
			"synced_state_written":          false,
			"external_call_made":            false,
			"contains_provider_token":       false,
			"contains_remote_url":           false,
			"contains_provider_response":    false,
			"required_controls":             []string{"github_actions_scope_review", "provider_account_binding", "synced_state_write_audit"},
			"disabled_backends":             []string{"provider_mutation", "raw_provider_response_recording", "server_side_automatic_validation_rerun"},
			"suppressed_fields":             []string{"provider_token", "authorization_header", "remote_url", "github_actions_response", "workflow_logs", "provider_response_body", "provider_response_headers"},
			"blocked_reasons":               providerRefreshKindBlockedReasons(state, "github_actions_api_refresh_not_enqueued"),
			"execution_blockers":            []string{"github_actions_api_refresh_not_enqueued", "server_side_validation_rerun_not_performed"},
			"message":                       "GitHub Actions refresh can enqueue the existing sync worker job; this preview does not call the provider, record raw responses, or rerun validation server-side.",
		}
	case "argocd_app_refresh":
		return map[string]any{
			"mode":                         "provider_refresh_argo_revision_plan",
			"kind":                         kind,
			"refresh_state":                state,
			"operation_enqueued":           false,
			"worker_job_created":           false,
			"argocd_app_sync_enabled":      state == "planned",
			"argocd_api_called":            false,
			"argocd_app_refresh_performed": false,
			"argo_revision_bound":          false,
			"synced_state_write_planned":   state == "planned",
			"synced_state_written":         false,
			"external_call_made":           false,
			"contains_provider_token":      false,
			"contains_argo_response":       false,
			"required_controls":            []string{"argo_connection_review", "argo_revision_binding", "synced_state_write_audit"},
			"disabled_backends":            []string{"provider_mutation", "raw_argo_response_recording", "server_side_automatic_validation_rerun"},
			"suppressed_fields":            []string{"provider_token", "authorization_header", "argo_response", "raw_argo_response", "provider_response_body", "provider_response_headers"},
			"blocked_reasons":              providerRefreshKindBlockedReasons(state, "argocd_app_refresh_not_enqueued"),
			"execution_blockers":           []string{"argocd_app_refresh_not_enqueued", "server_side_validation_rerun_not_performed"},
			"message":                      "Argo revision refresh can enqueue the existing Argo app sync worker job; this preview does not call Argo, record raw responses, or rerun validation server-side.",
		}
	default:
		return map[string]any{
			"mode":               "provider_refresh_unknown_kind_plan",
			"kind":               kind,
			"refresh_state":      state,
			"external_call_made": false,
			"blocked_reasons":    providerRefreshKindBlockedReasons(state, "provider_refresh_kind_not_performed"),
		}
	}
}

func providerRefreshKindBlockedReasons(state, executionReason string) []string {
	if state == "not_required" {
		return []string{"refresh_kind_not_required"}
	}
	if state == "blocked" {
		return []string{"refresh_kind_blocked", executionReason}
	}
	return []string{executionReason}
}

func projectVersionProviderRefreshResultRecordingPlan(plannedKinds []string) map[string]any {
	return map[string]any{
		"mode":                           "provider_refresh_result_recording_plan",
		"result_recording_state":         "blocked",
		"result_recording_ready":         false,
		"result_recording_ready_reason":  "provider_refresh_execution_not_performed",
		"recording_enabled":              false,
		"result_written":                 false,
		"operation_log_written":          false,
		"canonical_asset_sync_queued":    false,
		"status_snapshot_written":        false,
		"validation_rerun_recorded":      false,
		"git_ref_fetch_result_recorded":  false,
		"github_actions_result_recorded": false,
		"argo_revision_result_recorded":  false,
		"planned_refresh_kinds":          plannedKinds,
		"required_result_fields":         []string{"operation_run_id", "refresh_kind", "status", "started_at", "finished_at", "synced_entity_count", "git_ref_fetch_status", "github_actions_refresh_status", "argo_revision_refresh_status", "validation_rerun_status"},
		"suppressed_fields":              []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"blocked_reasons":                []string{"provider_refresh_execution_not_performed", "synced_state_write_not_performed", "validation_auto_reload_not_observed"},
		"message":                        "Refresh results are not recorded by this preview; future execution must write sanitized status, counts, and validation rerun state only.",
		"raw_response_included":          false,
		"raw_git_output_included":        false,
		"raw_argo_response_included":     false,
		"provider_request_id_included":   false,
	}
}

func stringInSlice(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func projectVersionValidationItem(index int, manifest map[string]any, remotes, tagRuns, actionRuns, argoApps []map[string]any) map[string]any {
	remoteID := strings.TrimSpace(stringFromMap(manifest, "remote_id"))
	commitSHA := strings.TrimSpace(firstNonEmptyString(stringFromMap(manifest, "commit_sha"), stringFromMap(manifest, "config_commit_sha")))
	tagName := strings.TrimSpace(stringFromMap(manifest, "tag"))
	actionRunID := strings.TrimSpace(stringFromMap(manifest, "github_action_run_id"))
	argoRevision := strings.TrimSpace(stringFromMap(manifest, "argo_revision"))
	checks := make([]map[string]any, 0, 5)
	remote := findRowByID(remotes, remoteID)
	checks = append(checks, validationCheck("remote_present", remote != nil, false, "manifest remote is available in synced database state"))
	refChecksConfigured := commitSHA != "" || tagName != "" || actionRunID != "" || argoRevision != ""
	if commitSHA != "" && remote != nil {
		latestSHA := strings.TrimSpace(fmt.Sprint(remote["latest_sha"]))
		checks = append(checks, validationCheck("commit_matches_remote_latest", latestSHA != "" && strings.EqualFold(latestSHA, commitSHA), latestSHA != "", "manifest commit matches synced remote latest_sha"))
	}
	if tagName != "" {
		tagRun := findProjectVersionTagRun(tagRuns, remoteID, tagName, commitSHA)
		checks = append(checks, validationCheck("tag_run_observed", tagRun != nil, len(tagRuns) > 0, "tag has a local repo_tag_run observation"))
	}
	if actionRunID != "" {
		actionRun := findProjectVersionActionRun(actionRuns, remoteID, actionRunID, commitSHA)
		checks = append(checks, validationCheck("github_action_run_observed", actionRun != nil, len(actionRuns) > 0, "GitHub Actions run has a local synced observation"))
	}
	if argoRevision != "" {
		argoApp := findProjectVersionArgoRevision(argoApps, argoRevision)
		checks = append(checks, validationCheck("argo_revision_observed", argoApp != nil, len(argoApps) > 0, "Argo revision has a local synced app observation"))
	}
	if remote != nil && !refChecksConfigured {
		checks = append(checks, validationCheck("version_refs_configured", false, true, "manifest item has a remote but no commit, tag, action, or Argo revision to validate"))
	}
	status := validationStatus(checks)
	return map[string]any{
		"index":               index,
		"repo_key":            manifest["repo_key"],
		"repo_role":           manifest["repo_role"],
		"remote_id":           remoteID,
		"remote_key":          manifest["remote_key"],
		"status":              status,
		"checks":              checks,
		"external_call_made":  false,
		"secret_included":     false,
		"credential_included": false,
	}
}

func validationCheck(name string, ready, observed bool, message string) map[string]any {
	status := "blocked"
	if ready {
		status = "ready"
	} else if observed {
		status = "partial"
	}
	return map[string]any{"name": name, "status": status, "message": message}
}

func validationStatus(checks []map[string]any) string {
	if len(checks) == 0 {
		return "blocked"
	}
	hasReady := false
	hasPartial := false
	for _, check := range checks {
		switch check["status"] {
		case "ready":
			hasReady = true
		case "partial":
			hasPartial = true
		default:
			return "blocked"
		}
	}
	if hasPartial {
		return "partial"
	}
	if hasReady {
		return "ready"
	}
	return "blocked"
}

func findRowByID(rows []map[string]any, id string) map[string]any {
	for _, row := range rows {
		if strings.TrimSpace(fmt.Sprint(row["id"])) == id {
			return row
		}
	}
	return nil
}

func findProjectVersionTagRun(rows []map[string]any, remoteID, tagName, commitSHA string) map[string]any {
	for _, row := range rows {
		if remoteID != "" && strings.TrimSpace(fmt.Sprint(row["target_remote_id"])) != remoteID && strings.TrimSpace(fmt.Sprint(row["git_remote_id"])) != remoteID {
			continue
		}
		if tagName != "" && strings.TrimSpace(fmt.Sprint(row["tag_name"])) != tagName {
			continue
		}
		if commitSHA != "" && !strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["target_sha"])), commitSHA) {
			continue
		}
		return row
	}
	return nil
}

func findProjectVersionActionRun(rows []map[string]any, remoteID, actionRunID, commitSHA string) map[string]any {
	for _, row := range rows {
		idMatches := strings.TrimSpace(fmt.Sprint(row["id"])) == actionRunID || strings.TrimSpace(fmt.Sprint(row["run_id"])) == actionRunID
		if !idMatches {
			continue
		}
		if remoteID != "" && strings.TrimSpace(fmt.Sprint(row["git_remote_id"])) != remoteID {
			continue
		}
		if commitSHA != "" && !strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["commit_sha"])), commitSHA) {
			continue
		}
		return row
	}
	return nil
}

func findProjectVersionArgoRevision(rows []map[string]any, argoRevision string) map[string]any {
	needle := strings.TrimSpace(argoRevision)
	for _, row := range rows {
		metadata := mapFromAny(row["metadata"])
		revision := firstNonEmptyString(stringFromMap(metadata, "revision"), stringFromMap(metadata, "target_revision"))
		if strings.EqualFold(strings.TrimSpace(revision), needle) {
			return row
		}
	}
	return nil
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
			'project_version:' || pv.id::text,
			pv.project_id::text,
			'project_version',
			pv.version,
			pv.version,
			COALESCE(NULLIF(pv.source, ''), 'manual'),
			'project_version',
			pv.version,
			'active',
			'normal',
			'project_versions',
			pv.id::text,
			jsonb_build_object(
				'source', pv.source,
				'repository_count', COALESCE(version_manifest.repository_count, 0),
				'has_repository_manifest', jsonb_typeof(pv.metadata->'repositories')='array',
				'has_config_commit', COALESCE(version_manifest.has_config_commit, false),
				'has_action_link', COALESCE(version_manifest.has_action_link, false),
				'has_argo_revision', COALESCE(version_manifest.has_argo_revision, false)
			),
			pv.created_at,
			pv.created_at
		FROM project_versions pv
		LEFT JOIN LATERAL (
			SELECT
				count(*)::int AS repository_count,
				bool_or(manifest_repo.item->>'repo_role'='config' OR COALESCE(NULLIF(manifest_repo.item->>'config_commit_sha', ''), '') <> '') AS has_config_commit,
				bool_or(COALESCE(NULLIF(manifest_repo.item->>'github_action_run_id', ''), '') <> '') AS has_action_link,
				bool_or(COALESCE(NULLIF(manifest_repo.item->>'argo_revision', ''), '') <> '') AS has_argo_revision
			FROM jsonb_array_elements(
				CASE
					WHEN jsonb_typeof(pv.metadata->'repositories')='array'
					THEN pv.metadata->'repositories'
					ELSE '[]'::jsonb
				END
			) AS manifest_repo(item)
		) version_manifest ON true
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
				'active_delegation_count', COALESCE(delegation_counts.active_delegation_count, 0),
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
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE revoked_at IS NULL)::int AS active_delegation_count
			FROM operation_approval_delegations oadel
			WHERE oadel.operation_approval_id=oa.id
		) delegation_counts ON true
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
			'repo_tag_run:' || rtr.id::text,
			COALESCE(rtr.project_id::text, pgr.project_id::text, ''),
			'repo_tag_run',
			COALESCE(NULLIF(gr.name, ''), 'target remote') || ' tag run',
			COALESCE(NULLIF(gr.name, ''), 'target remote') || ' tag run',
			COALESCE(gr.name, 'target remote'),
			'repo_tag',
			rtr.id::text,
			rtr.status,
			CASE
				WHEN rtr.status IN ('failed', 'error', 'canceled', 'cancelled') THEN 'high'
				WHEN rtr.status IN ('queued', 'running', 'pending') THEN 'warning'
				ELSE 'normal'
			END,
			'repo_tag_runs',
			rtr.id::text,
			jsonb_build_object(
				'operation_run_id', rtr.operation_run_id,
				'project_git_repository_id', rtr.project_git_repository_id,
				'target_remote_id', COALESCE(rtr.target_remote_id, rtr.git_remote_id),
				'status', rtr.status,
				'tag_name_configured', rtr.tag_name <> '',
				'target_sha_configured', rtr.target_sha <> '',
				'started_at', rtr.started_at,
				'finished_at', rtr.finished_at,
				'has_error', rtr.error_message <> ''
			),
			rtr.created_at,
			COALESCE(rtr.finished_at, rtr.started_at, rtr.created_at)
		FROM repo_tag_runs rtr
		LEFT JOIN project_git_repositories pgr ON pgr.id=rtr.project_git_repository_id
		LEFT JOIN git_remotes gr ON gr.id=COALESCE(rtr.target_remote_id, rtr.git_remote_id)
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
			'project:' || p.id::text || ':owns:project_version:' || pv.id::text,
			p.id::text,
			'project:' || p.id::text,
			'project_version:' || pv.id::text,
			'owns_version',
			jsonb_build_object('version', pv.version, 'source', pv.source),
			pv.created_at
		FROM projects p
		JOIN project_versions pv ON pv.project_id=p.id
		UNION ALL
		SELECT
			'project_version:' || pv.id::text || ':includes_repository:repository:' || r.id::text,
			pv.project_id::text,
			'project_version:' || pv.id::text,
			'repository:' || r.id::text,
			'includes_repository',
			jsonb_build_object(
				'version', pv.version,
				'repository_id', r.id,
				'has_tag', COALESCE(NULLIF(manifest_repo.item->>'tag', ''), '') <> '',
				'has_commit_sha', COALESCE(NULLIF(manifest_repo.item->>'commit_sha', ''), '') <> ''
			),
			pv.created_at
		FROM project_versions pv
		JOIN LATERAL jsonb_array_elements(
			CASE
				WHEN jsonb_typeof(pv.metadata->'repositories')='array'
				THEN pv.metadata->'repositories'
				ELSE '[]'::jsonb
			END
		) AS manifest_repo(item) ON true
		JOIN project_git_repositories r ON r.id=CASE
			WHEN (manifest_repo.item->>'repository_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (manifest_repo.item->>'repository_id')::uuid
			ELSE NULL
		END
			AND r.project_id=pv.project_id
		UNION ALL
		SELECT
			'project_version:' || pv.id::text || ':pins_remote:git_remote:' || gr.id::text,
			pv.project_id::text,
			'project_version:' || pv.id::text,
			'git_remote:' || gr.id::text,
			'pins_remote',
			jsonb_build_object(
				'version', pv.version,
				'remote_id', gr.id,
				'has_tag', COALESCE(NULLIF(manifest_repo.item->>'tag', ''), '') <> '',
				'has_commit_sha', COALESCE(NULLIF(manifest_repo.item->>'commit_sha', ''), '') <> '',
				'has_github_action_run', COALESCE(NULLIF(manifest_repo.item->>'github_action_run_id', ''), '') <> '',
				'has_argo_revision', COALESCE(NULLIF(manifest_repo.item->>'argo_revision', ''), '') <> ''
			),
			pv.created_at
		FROM project_versions pv
		JOIN LATERAL jsonb_array_elements(
			CASE
				WHEN jsonb_typeof(pv.metadata->'repositories')='array'
				THEN pv.metadata->'repositories'
				ELSE '[]'::jsonb
			END
		) AS manifest_repo(item) ON true
		JOIN git_remotes gr ON gr.id=CASE
			WHEN (manifest_repo.item->>'remote_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (manifest_repo.item->>'remote_id')::uuid
			ELSE NULL
		END
		-- Git remotes inherit project ownership through their logical repository.
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
			AND r.project_id=pv.project_id
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
	items, err := queryMaps(r.Context(), s.store.DB, operationListSQL(), userCanReadAllProjects(user), userIDOrNil(user))
	writeQueryResult(w, items, err)
}

func operationListSQL() string {
	return `
		SELECT op.*,
			COALESCE(log_counts.log_count, 0) AS log_count
		FROM (
			-- Keep visibility filtering and LIMIT inside this subquery so the
			-- lateral log count only touches the currently visible page.
			SELECT *
			FROM operation_runs op
			WHERE $1 OR op.project_id IS NULL OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id=op.project_id AND pm.user_id=$2
			)
			ORDER BY op.created_at DESC
			LIMIT 100
		) op
		LEFT JOIN LATERAL (
			SELECT count(*)::int AS log_count
			FROM operation_logs
			WHERE operation_run_id=op.id
		) log_counts ON true
		ORDER BY op.created_at DESC
		LIMIT 100`
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
	if err == nil {
		summary["backend_summary"] = workerQueueBackendSummary()
	}
	writeQueryOne(w, summary, err)
}

func workerQueueBackendSummary() map[string]any {
	return map[string]any{
		"backend":           "postgres",
		"claiming":          "select_for_update_skip_locked",
		"redis_locking":     "disabled",
		"redis_enabled":     false,
		"pubsub":            "disabled",
		"pubsub_enabled":    false,
		"log_fanout":        "sse_polling",
		"websocket_fanout":  "deferred",
		"active_components": []string{"postgres_polling", "row_lock_claiming", "sse_polling_log_fanout"},
		"deferred_backends": []string{"redis_locking", "redis_pubsub", "websocket_fanout"},
		"message":           "Worker jobs use PostgreSQL polling and row locks; Redis-backed locking and pub/sub fanout are deferred.",
	}
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
	if err != nil {
		writeQueryResult(w, items, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": enrichOperationApprovalRules(items)})
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
	writeJSON(w, http.StatusCreated, enrichOperationApprovalRule(item))
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
	writeJSON(w, http.StatusOK, enrichOperationApprovalRule(item))
}

func enrichOperationApprovalRules(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, enrichOperationApprovalRule(item))
	}
	return out
}

func enrichOperationApprovalRule(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	out := make(map[string]any, len(item)+2)
	for key, value := range item {
		out[key] = value
	}
	out["notification_destinations"] = approvalChannelDestinations(approvalRolesFromAny(item["notification_channels"]))
	out["escalation_destinations"] = approvalChannelDestinations(approvalRolesFromAny(item["escalation_channels"]))
	return out
}

func approvalChannelDestinations(channels []string) []map[string]any {
	destinations := make([]map[string]any, 0, len(channels))
	for _, channel := range channels {
		raw := strings.ToLower(strings.TrimSpace(channel))
		if raw == "" {
			continue
		}
		kind, target, _ := strings.Cut(raw, ":")
		if target == "" {
			kind = raw
		}
		known := approvalDestinationKnownKind(kind)
		exposedTarget := target
		if !known {
			exposedTarget = ""
		}
		readiness := approvalDestinationAdapterReadiness(kind, target)
		destination := map[string]any{
			"channel":      raw,
			"kind":         kind,
			"target":       exposedTarget,
			"label":        approvalDestinationLabel(kind, exposedTarget),
			"needs_config": approvalDestinationNeedsConfig(kind, target),
		}
		for key, value := range readiness {
			destination[key] = value
		}
		destinations = append(destinations, destination)
	}
	return destinations
}

func approvalDestinationKnownKind(kind string) bool {
	switch kind {
	case "ui", "webhook", "email", "slack", "pagerduty":
		return true
	default:
		return false
	}
}

func approvalDestinationLabel(kind, target string) string {
	switch kind {
	case "ui":
		return "Operations UI"
	case "webhook":
		if target != "" {
			return "Approval webhook: " + target
		}
		return "Approval webhook"
	case "email":
		if target != "" {
			return "Email: " + target
		}
		return "Email"
	case "slack":
		if target != "" {
			return "Slack: " + target
		}
		return "Slack"
	case "pagerduty":
		if target != "" {
			return "PagerDuty: " + target
		}
		return "PagerDuty"
	default:
		return "Unknown channel: " + kind
	}
}

func approvalDestinationAdapterReadiness(kind, target string) map[string]any {
	switch kind {
	case "ui":
		return map[string]any{
			"adapter":                "operations_ui",
			"adapter_status":         "enabled",
			"delivery_mode":          "in_app",
			"requires_external_call": false,
			"blocked_reason":         "",
			"configuration_scope":    "built_in",
		}
	case "webhook":
		return map[string]any{
			"adapter":                "approval_webhook",
			"adapter_status":         "environment_backed",
			"delivery_mode":          "http_post",
			"requires_external_call": true,
			"blocked_reason":         "",
			"configuration_scope":    "ASSOPS_APPROVAL_WEBHOOK_URL",
		}
	case "email", "slack", "pagerduty":
		return map[string]any{
			"adapter":                kind,
			"adapter_status":         "planned",
			"delivery_mode":          "preview_only",
			"requires_external_call": true,
			"blocked_reason":         "adapter delivery is not implemented yet",
			"configuration_scope":    "future_connector",
		}
	default:
		return map[string]any{
			"adapter":                "custom",
			"adapter_status":         "unknown",
			"delivery_mode":          "preview_only",
			"requires_external_call": target != "",
			"blocked_reason":         "unknown approval destination adapter",
			"configuration_scope":    "custom",
			"redacted_target":        target != "",
		}
	}
}

func approvalDestinationNeedsConfig(kind, target string) bool {
	switch kind {
	case "ui":
		return false
	case "webhook":
		return false
	case "email", "slack", "pagerduty":
		return true
	default:
		return true
	}
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
	payloadAudit := operationApprovalPayloadAudit(approval)
	delete(approval, "request_payload")
	opID := cleanOptionalID(fmt.Sprint(approval["operation_run_id"]))
	response := map[string]any{"approval": approval, "approval_payload_audit": payloadAudit}
	if stringFromMap(approval, "action") == templateProviderReviewExecuteApprovalAction {
		attemptLedger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
			return
		}
		response["provider_review_attempt_ledger"] = attemptLedger
	}
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

func operationApprovalPayloadAudit(approval map[string]any) map[string]any {
	payload := mapFromAny(approval["request_payload"])
	switch stringFromMap(payload, "kind") {
	case "config_git_commit":
		out := map[string]any{
			"kind":                  "config_git_commit",
			"project_id":            cleanOptionalID(stringFromMap(payload, "project_id")),
			"repo_id":               cleanOptionalID(stringFromMap(payload, "repo_id")),
			"scaffold_file_count":   intFromAny(payload["scaffold_file_count"], 0),
			"project_version_count": intFromAny(payload["project_version_count"], 0),
			"payload_redacted":      true,
			"external_call_made":    false,
			"git_write_performed":   false,
			"file_content_included": false,
			"secret_included":       false,
		}
		input := mapFromAny(payload["input"])
		if len(input) > 0 {
			out["input"] = map[string]any{
				"project_git_repository_id": cleanOptionalID(fmt.Sprint(input["project_git_repository_id"])),
				"config_remote_id":          cleanOptionalID(fmt.Sprint(input["config_remote_id"])),
				"provider_type":             cleanOptionalText(fmt.Sprint(input["provider_type"])),
				"default_branch_configured": boolOnlyFromAny(input["default_branch_configured"]),
				"scaffold_file_count":       intFromAny(input["scaffold_file_count"], 0),
				"remote_count":              intFromAny(input["remote_count"], 0),
				"mode":                      cleanOptionalText(fmt.Sprint(input["mode"])),
				"file_content_included":     false,
				"secret_included":           false,
				"external_call_made":        false,
				"git_write_performed":       false,
			}
		}
		if result := mapFromAny(payload["approval_result"]); len(result) > 0 {
			out["approval_result"] = map[string]any{
				"operation_request_result": mapFromAny(result["operation_request_result"]),
				"external_call_made":       false,
				"git_write_performed":      false,
				"file_content_included":    false,
				"secret_included":          false,
			}
		}
		return out
	case "project_template_provider_review_execute":
		out := map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_template_run_id": cleanOptionalID(stringFromMap(payload, "project_template_run_id")),
			"project_id":              cleanOptionalID(stringFromMap(payload, "project_id")),
			"provider_api_call_made":  false,
			"provider_api_mutation":   "disabled",
			"payload_redacted":        true,
			"contains_token":          false,
			"contains_file_content":   false,
		}
		request := mapFromAny(payload["execution_request"])
		if len(request) > 0 {
			out["execution_request"] = map[string]any{
				"status":                   request["status"],
				"approval_action":          request["approval_action"],
				"resource_type":            request["resource_type"],
				"provider_type":            request["provider_type"],
				"review_kind":              request["review_kind"],
				"source_branch":            request["source_branch"],
				"target_branch":            request["target_branch"],
				"payload_redacted":         true,
				"contains_token":           false,
				"provider_api_mutation":    "disabled",
				"requires_operator_review": true,
			}
		}
		out["execution_guardrail"] = sanitizedProviderReviewExecutionGuardrail(mapFromAny(payload["execution_guardrail"]))
		out["credential_strategy"] = sanitizedProviderReviewCredentialStrategy(mapFromAny(payload["credential_strategy"]))
		out["starter_file_payload"] = sanitizedStarterFilePayloadSummary(mapFromAny(payload["starter_file_payload"]))
		out["provider_api_request_plan"] = sanitizedProviderAPIRequestPlan(mapFromAny(payload["provider_api_request_plan"]))
		out["provider_review_reconciliation"] = sanitizedProviderReviewReconciliation(mapFromAny(payload["provider_review_reconciliation"]))
		out["provider_review_target_summary"] = sanitizedProviderReviewTargetSummary(mapFromAny(payload["provider_review_target_summary"]))
		if result := mapFromAny(payload["approval_result"]); len(result) > 0 {
			out["approval_result"] = map[string]any{
				"project_template_run_id":        cleanOptionalID(stringFromMap(result, "project_template_run_id")),
				"execution_request":              out["execution_request"],
				"execution_guardrail":            sanitizedProviderReviewExecutionGuardrail(mapFromAny(result["execution_guardrail"])),
				"credential_strategy":            sanitizedProviderReviewCredentialStrategy(mapFromAny(result["credential_strategy"])),
				"starter_file_payload":           sanitizedStarterFilePayloadSummary(mapFromAny(result["starter_file_payload"])),
				"provider_api_request_plan":      sanitizedProviderAPIRequestPlan(mapFromAny(result["provider_api_request_plan"])),
				"provider_review_reconciliation": sanitizedProviderReviewReconciliation(mapFromAny(result["provider_review_reconciliation"])),
				"provider_review_target_summary": sanitizedProviderReviewTargetSummary(mapFromAny(result["provider_review_target_summary"])),
				"provider_review_attempt_ledger": sanitizedProviderReviewAttemptLedger(mapFromAny(result["provider_review_attempt_ledger"])),
				"provider_api_call_made":         false,
				"provider_api_mutation":          "disabled",
				"execution_enabled":              false,
			}
		}
		return out
	default:
		return map[string]any{}
	}
}

func (s *Server) providerReviewAttemptLedgerForApproval(ctx context.Context, approvalID string) (map[string]any, error) {
	approvalID = cleanOptionalID(approvalID)
	if approvalID == "" {
		return providerReviewAttemptLedgerSummary(nil), nil
	}
	return s.providerReviewAttemptLedgerForApprovalDB(ctx, s.store.DB, approvalID)
}

func (s *Server) providerReviewAttemptLedgerForApprovalDB(ctx context.Context, db sqlx.ExtContext, approvalID string) (map[string]any, error) {
	approvalID = cleanOptionalID(approvalID)
	if approvalID == "" {
		return providerReviewAttemptLedgerSummary(nil), nil
	}
	attempts, err := queryMaps(ctx, db, `
		SELECT
			id,
			operation_name,
			endpoint_key,
			status,
			replay_check,
			conflict_policy,
			retry_policy,
			operation_order,
			depends_on_operation,
			dependency_status,
			request_summary,
			response_diagnostics,
			provider_api_call_made,
			provider_api_mutation,
			external_call_made,
			claimed_at,
			claimed_by_user_id
		FROM provider_review_attempts
		WHERE operation_approval_id=$1
		ORDER BY operation_order ASC, created_at ASC, operation_name ASC`, approvalID)
	if err != nil {
		return nil, err
	}
	return providerReviewAttemptLedgerSummary(attempts), nil
}

func (s *Server) claimProviderReviewAttempt(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start provider review attempt claim")
		return
	}
	defer tx.Rollback()
	attempt, err := queryOne(r.Context(), tx, `
		SELECT
			pra.id,
			pra.operation_approval_id,
			pra.project_template_run_id,
			pra.provider_type,
			pra.review_kind,
			pra.operation_name,
			pra.endpoint_key,
			pra.status,
			pra.replay_check,
			pra.conflict_policy,
			pra.retry_policy,
			pra.operation_order,
			pra.depends_on_operation,
			pra.dependency_status,
			pra.request_summary,
			pra.response_diagnostics,
			pra.provider_api_call_made,
			pra.provider_api_mutation,
			pra.external_call_made,
			pra.claimed_at,
			pra.claimed_by_user_id,
			oa.id AS approval_id,
			oa.project_id AS approval_project_id,
			oa.action AS approval_action,
			oa.status AS approval_status
		FROM provider_review_attempts pra
		JOIN operation_approvals oa ON oa.id=pra.operation_approval_id
		WHERE pra.id=$1
		FOR UPDATE OF pra`, attemptID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	approval := map[string]any{
		"id":         attempt["approval_id"],
		"project_id": attempt["approval_project_id"],
	}
	projectID := cleanOptionalID(fmt.Sprint(approval["project_id"]))
	if projectID == "" {
		if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval", ID: fmt.Sprint(approval["id"])}, "update") {
			return
		}
	} else if !s.requireProjectPolicy(w, r, PolicyResource{Type: "operation_approval", ID: fmt.Sprint(approval["id"]), ProjectID: projectID}, "update") {
		return
	}
	if stringFromMap(attempt, "approval_action") != templateProviderReviewExecuteApprovalAction {
		writeError(w, http.StatusConflict, "provider review attempt is not tied to provider review execution approval")
		return
	}
	if stringFromMap(attempt, "approval_status") != "approved" {
		writeJSON(w, http.StatusOK, providerReviewAttemptClaimBlockedResponse(attempt, "operation_approval_not_approved", nil))
		return
	}
	claimPlan := providerReviewAttemptClaimPlanFromAttempt(attempt)
	if claimPlan["claim_metadata_ready"] != true {
		writeJSON(w, http.StatusOK, providerReviewAttemptClaimBlockedResponse(attempt, "provider_review_attempt_claim_metadata_not_ready", claimPlan))
		return
	}
	claimed, err := queryOne(r.Context(), tx, `
		UPDATE provider_review_attempts
		SET status='running',
			claimed_at=COALESCE(claimed_at, now()),
			claimed_by_user_id=COALESCE(claimed_by_user_id, NULLIF($2,'')::uuid),
			updated_at=now()
		WHERE id=$1
			AND status='planned'
			AND dependency_status IN ('independent', 'dependency_satisfied')
			AND provider_api_call_made=false
			AND external_call_made=false
			AND provider_api_mutation='disabled'
		RETURNING
			id,
			operation_approval_id,
			project_template_run_id,
			provider_type,
			review_kind,
			operation_name,
			endpoint_key,
			status,
			replay_check,
			conflict_policy,
			retry_policy,
			operation_order,
			depends_on_operation,
			dependency_status,
			request_summary,
			response_diagnostics,
			provider_api_call_made,
			provider_api_mutation,
			external_call_made,
			claimed_at,
			claimed_by_user_id`, attemptID, userIDOrNil(currentUser(r)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusOK, providerReviewAttemptClaimBlockedResponse(attempt, "provider_review_attempt_claim_conflict", claimPlan))
			return
		}
		writeError(w, http.StatusInternalServerError, "could not claim provider review attempt")
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApprovalDB(r.Context(), tx, cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not reload provider review attempt ledger")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit provider review attempt claim")
		return
	}
	writeJSON(w, http.StatusOK, providerReviewAttemptClaimResponse(claimed, ledger, true, "claimed"))
}

func providerReviewAttemptClaimBlockedResponse(attempt map[string]any, reason string, claimPlan map[string]any) map[string]any {
	if len(claimPlan) == 0 {
		claimPlan = providerReviewAttemptClaimPlanFromAttempt(attempt)
	}
	return providerReviewAttemptClaimResponse(attempt, providerReviewAttemptLedgerSummary([]map[string]any{attempt}), false, reason, claimPlan)
}

func providerReviewAttemptClaimResponse(attempt map[string]any, ledger map[string]any, claimed bool, state string, claimPlanOverride ...map[string]any) map[string]any {
	claimPlan := providerReviewAttemptClaimPlanFromAttempt(attempt)
	if len(claimPlanOverride) > 0 && len(claimPlanOverride[0]) > 0 {
		claimPlan = claimPlanOverride[0]
	}
	return map[string]any{
		"claim_state":                cleanOptionalText(state),
		"claimed":                    claimed,
		"attempt":                    providerReviewAttemptLedgerSummary([]map[string]any{attempt})["operations"].([]map[string]any)[0],
		"provider_review_attempt_id": cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"operation_name":             safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"endpoint_key":               safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")),
		"claim_plan":                 claimPlan,
		"ledger":                     ledger,
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"idempotency_key_included":   false,
		"contains_token":             false,
		"contains_provider_url":      false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
	}
}

func providerReviewAttemptClaimPlanFromAttempt(attempt map[string]any) map[string]any {
	operation := map[string]any{
		"name":                  stringFromMap(attempt, "operation_name"),
		"endpoint_key":          stringFromMap(attempt, "endpoint_key"),
		"status":                stringFromMap(attempt, "status"),
		"dependency_status":     stringFromMap(attempt, "dependency_status"),
		"replay_check":          stringFromMap(attempt, "replay_check"),
		"conflict_policy":       stringFromMap(attempt, "conflict_policy"),
		"retry_policy":          stringFromMap(attempt, "retry_policy"),
		"operation_order":       attempt["operation_order"],
		"request_summary":       attempt["request_summary"],
		"response_diagnostics":  attempt["response_diagnostics"],
		"claimed_at":            attempt["claimed_at"],
		"claimed_by_user_id":    attempt["claimed_by_user_id"],
		"provider_api_mutation": "disabled",
	}
	requestSummary := mapFromAny(attempt["request_summary"])
	responseDiagnostics := mapFromAny(attempt["response_diagnostics"])
	idempotencyReady := boolOnlyFromAny(requestSummary["requires_idempotency_ledger"])
	responseReady := responseDiagnostics["mode"] == "redacted_attempt_response_diagnostics"
	return providerReviewAttemptExecutionClaimPlan(operation, idempotencyReady, responseReady)
}

func sanitizedProviderReviewExecutionGuardrail(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"execution_mode":           cleanOptionalText(stringFromMap(value, "execution_mode")),
		"execution_enabled":        false,
		"execution_enabled_config": boolValueFromAny(value["execution_enabled_config"]),
		"mutation_armed_config":    boolOnlyFromAny(value["mutation_armed_config"]),
		"provider_type":            cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":              cleanOptionalText(stringFromMap(value, "review_kind")),
		"source_branch":            cleanOptionalText(stringFromMap(value, "source_branch")),
		"target_branch":            cleanOptionalText(stringFromMap(value, "target_branch")),
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"branch_creation_allowed":  false,
		"review_request_allowed":   false,
		"blocked_reasons":          stringSliceFromAny(value["blocked_reasons"]),
		"gates":                    sanitizedProviderReviewGates(mapSliceFromAny(value["gates"])),
		"next_step":                cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewTargetSummary(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	sourceBranch := cleanOptionalText(stringFromMap(value, "source_branch"))
	if !isSafeGitRefPart(sourceBranch) {
		sourceBranch = ""
	}
	targetBranch := cleanOptionalText(stringFromMap(value, "target_branch"))
	if !isSafeGitRefPart(targetBranch) {
		targetBranch = ""
	}
	operations := make([]map[string]any, 0, len(mapSliceFromAny(value["operations"])))
	for _, operation := range mapSliceFromAny(value["operations"]) {
		operations = append(operations, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"payload_shape":         cleanOptionalText(stringFromMap(operation, "payload_shape")),
			"status":                cleanOptionalText(stringFromMap(operation, "status")),
			"api_call":              false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		})
	}
	return map[string]any{
		"status":                          cleanOptionalText(stringFromMap(value, "status")),
		"mode":                            "redacted_execution_target_summary",
		"provider_type":                   cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                     cleanOptionalText(stringFromMap(value, "review_kind")),
		"source_branch":                   sourceBranch,
		"target_branch":                   targetBranch,
		"branch_refs_ready":               boolOnlyFromAny(value["branch_refs_ready"]),
		"starter_file_payload_ready":      boolOnlyFromAny(value["starter_file_payload_ready"]),
		"provider_api_request_ready":      boolOnlyFromAny(value["provider_api_request_ready"]),
		"file_count":                      intFromAny(value["file_count"], 0),
		"operation_count":                 len(operations),
		"operations":                      operations,
		"adapter_status":                  safeProviderReviewAdapterStatus(stringFromMap(value, "adapter_status")),
		"blocked_reasons":                 safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"payload_redacted":                true,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_file_content":           false,
		"idempotency_key_included":        false,
		"requires_persisted_attempt":      true,
		"requires_response_diagnostics":   true,
		"requires_provider_api_adapter":   true,
		"requires_adapter_mutation_armed": true,
		"requires_operator_review":        true,
		"future_adapter_input_boundary":   "branch_ref_commit_review_request",
		"adapter_mutation_currently_off":  true,
	}
}

func safeProviderReviewAdapterStatus(value string) string {
	switch cleanOptionalText(value) {
	case "missing", "planned", "ready", "blocked", "unsupported":
		return cleanOptionalText(value)
	default:
		return "missing"
	}
}

func safeProviderReviewBlockedReasons(items []string) []string {
	allowed := map[string]bool{
		"provider_supported":                  true,
		"starter_file_payload_staged":         true,
		"provider_api_request_plan_ready":     true,
		"provider_review_execution_enabled":   true,
		"provider_credential_configured":      true,
		"provider_token_env_present":          true,
		"provider_review_api_adapter":         true,
		"provider_review_adapter_rehearsal":   true,
		"provider_review_mutation_armed":      true,
		"review_branches_valid":               true,
		"review_target_summary_ready":         true,
		"provider_review_target_summary_safe": true,
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = cleanOptionalText(item)
		if item == "" || len(item) > 128 || !allowed[item] || seen[item] {
			continue
		}
		out = append(out, item)
		seen[item] = true
	}
	return out
}

func sanitizedProviderReviewAttemptLedger(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	operations := make([]map[string]any, 0, len(mapSliceFromAny(value["operations"])))
	for _, operation := range mapSliceFromAny(value["operations"]) {
		operations = append(operations, map[string]any{
			"id":                       cleanOptionalID(fmt.Sprint(operation["id"])),
			"name":                     cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":             cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"status":                   cleanOptionalText(stringFromMap(operation, "status")),
			"replay_check":             cleanOptionalText(stringFromMap(operation, "replay_check")),
			"conflict_policy":          cleanOptionalText(stringFromMap(operation, "conflict_policy")),
			"retry_policy":             cleanOptionalText(stringFromMap(operation, "retry_policy")),
			"operation_order":          intFromAny(operation["operation_order"], 0),
			"depends_on_operation":     safeProviderReviewAttemptDependencyName(stringFromMap(operation, "depends_on_operation")),
			"dependency_status":        safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(operation, "dependency_status")),
			"request_summary":          sanitizedProviderReviewAttemptRequestSummary(mapFromAny(operation["request_summary"])),
			"response_diagnostics":     sanitizedProviderReviewAttemptResponseDiagnostics(mapFromAny(operation["response_diagnostics"])),
			"external_call_made":       false,
			"provider_api_call_made":   false,
			"provider_api_mutation":    "disabled",
			"idempotency_key_included": false,
		})
	}
	return map[string]any{
		"status":                   cleanOptionalText(stringFromMap(value, "status")),
		"mode":                     "redacted_attempt_ledger",
		"attempt_count":            len(operations),
		"operations":               operations,
		"orchestration":            sanitizedProviderReviewAttemptOrchestration(mapFromAny(value["orchestration"]), operations),
		"external_call_made":       false,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"idempotency_key_included": false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"contains_repository_ref":  false,
		"contains_branch_name":     false,
		"contains_file_content":    false,
	}
}

func boolValueFromAny(value any) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	return text == "true" || text == "1" || text == "yes" || text == "on"
}

func sanitizedProviderReviewGates(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"gate":              cleanOptionalText(stringFromMap(item, "gate")),
			"status":            cleanOptionalText(stringFromMap(item, "status")),
			"required_config":   cleanOptionalText(stringFromMap(item, "required_config")),
			"provider_type":     cleanOptionalText(stringFromMap(item, "provider_type")),
			"review_kind":       cleanOptionalText(stringFromMap(item, "review_kind")),
			"adapter_status":    safeProviderReviewAdapterStatus(stringFromMap(item, "adapter_status")),
			"source_branch":     cleanOptionalText(stringFromMap(item, "source_branch")),
			"target_branch":     cleanOptionalText(stringFromMap(item, "target_branch")),
			"message":           cleanOptionalText(stringFromMap(item, "message")),
			"sensitive_payload": false,
		})
	}
	return out
}

func sanitizedProviderAPIRequestPlan(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                 cleanOptionalText(stringFromMap(value, "status")),
		"mode":                   "redacted_request_plan",
		"provider_type":          cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":            cleanOptionalText(stringFromMap(value, "review_kind")),
		"source_branch":          cleanOptionalText(stringFromMap(value, "source_branch")),
		"target_branch":          cleanOptionalText(stringFromMap(value, "target_branch")),
		"file_count":             intFromAny(value["file_count"], 0),
		"payload_redacted":       true,
		"contains_token":         false,
		"contains_file_content":  false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        stringSliceFromAny(value["blocked_reasons"]),
		"operations":             sanitizedProviderAPIRequestOperations(mapSliceFromAny(value["operations"])),
	}
}

func sanitizedProviderAPIRequestOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(item, "name")),
			"method":                cleanOptionalText(stringFromMap(item, "method")),
			"endpoint_key":          cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"payload_shape":         cleanOptionalText(stringFromMap(item, "payload_shape")),
			"file_count":            intFromAny(item["file_count"], 0),
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
			"api_call":              false,
		})
	}
	return out
}

func sanitizedProviderReviewReconciliation(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                 cleanOptionalText(stringFromMap(value, "status")),
		"mode":                   "preflight_reconciliation",
		"provider_type":          cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":            cleanOptionalText(stringFromMap(value, "review_kind")),
		"credential_strategy":    sanitizedProviderReviewCredentialStrategy(mapFromAny(value["credential_strategy"])),
		"adapter_contract":       sanitizedProviderReviewAdapterContract(mapFromAny(value["adapter_contract"])),
		"request_envelopes":      sanitizedProviderReviewAdapterRequestEnvelopes(mapSliceFromAny(value["request_envelopes"])),
		"adapter_rehearsal":      sanitizedProviderReviewAdapterRehearsal(mapFromAny(value["adapter_rehearsal"])),
		"mutation_arming_plan":   sanitizedProviderReviewMutationArmingPlan(mapFromAny(value["mutation_arming_plan"])),
		"execution_blueprint":    sanitizedProviderReviewAdapterExecutionBlueprint(mapFromAny(value["execution_blueprint"])),
		"response_diagnostics":   sanitizedProviderReviewAdapterResponseDiagnostics(mapFromAny(value["response_diagnostics"])),
		"idempotency_plan":       sanitizedProviderReviewAdapterIdempotencyPlan(mapFromAny(value["idempotency_plan"])),
		"adapter_status":         cleanOptionalText(stringFromMap(value, "adapter_status")),
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        stringSliceFromAny(value["blocked_reasons"]),
		"gates":                  sanitizedProviderReviewGates(mapSliceFromAny(value["gates"])),
		"operations":             sanitizedProviderReviewReconciliationOperations(mapSliceFromAny(value["operations"])),
		"next_step":              cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewAdapterExecutionBlueprint(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	operations := sanitizedProviderReviewAdapterExecutionBlueprintOperations(mapSliceFromAny(value["operations"]))
	status := safeProviderReviewAdapterExecutionBlueprintStatus(stringFromMap(value, "status"))
	if len(operations) == 0 {
		status = "not_recorded"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_adapter_execution_blueprint",
		"provider_type":                  cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":                 safeProviderReviewAdapterStatus(stringFromMap(value, "adapter_status")),
		"operation_count":                len(operations),
		"operations":                     operations,
		"execution_stage":                "adapter_implementation_required",
		"live_adapter_implemented":       false,
		"requires_provider_client":       true,
		"requires_request_builder":       true,
		"requires_response_handler":      true,
		"requires_idempotency_ledger":    true,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_branch_name":           false,
		"contains_file_content":          false,
		"adapter_mutation_currently_off": true,
		"next_step":                      cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewAdapterExecutionBlueprintOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                        safeProviderReviewAttemptOperationName(stringFromMap(item, "name")),
			"endpoint_key":                cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"method":                      cleanOptionalText(stringFromMap(item, "method")),
			"payload_shape":               cleanOptionalText(stringFromMap(item, "payload_shape")),
			"execution_status":            safeProviderReviewAdapterExecutionStatus(stringFromMap(item, "execution_status")),
			"payload_builder":             safeProviderReviewPayloadBuilderName(stringFromMap(item, "payload_builder")),
			"response_handler":            safeProviderReviewResponseHandlerName(stringFromMap(item, "response_handler")),
			"idempotency_scope":           "operation_scope_hash",
			"request_body_included":       false,
			"response_body_included":      false,
			"headers_included":            false,
			"payload_redacted":            true,
			"contains_token":              false,
			"contains_provider_url":       false,
			"contains_repository_ref":     false,
			"contains_branch_name":        false,
			"contains_file_content":       false,
			"api_call":                    false,
			"external_call_made":          false,
			"provider_api_call_made":      false,
			"provider_api_mutation":       "disabled",
			"requires_provider_client":    true,
			"requires_request_builder":    true,
			"requires_response_handler":   true,
			"requires_idempotency_ledger": true,
		})
	}
	return out
}

func safeProviderReviewAdapterExecutionBlueprintStatus(value string) string {
	switch cleanOptionalText(value) {
	case "blocked", "ready_for_adapter_implementation":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewAdapterExecutionStatus(value string) string {
	switch cleanOptionalText(value) {
	case "blocked", "ready_for_adapter_implementation":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewPayloadBuilderName(value string) string {
	switch cleanOptionalText(value) {
	case "build_redacted_branch_ref_request", "build_redacted_file_batch_request", "build_redacted_review_request", "build_redacted_provider_request":
		return cleanOptionalText(value)
	default:
		return "build_redacted_provider_request"
	}
}

func safeProviderReviewResponseHandlerName(value string) string {
	switch cleanOptionalText(value) {
	case "handle_branch_ref_response", "handle_commit_files_response", "handle_review_request_response", "handle_provider_response":
		return cleanOptionalText(value)
	default:
		return "handle_provider_response"
	}
}

func sanitizedProviderReviewMutationArmingPlan(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	executionEnabled := boolOnlyFromAny(value["execution_enabled_config"])
	rehearsalReady := boolOnlyFromAny(value["adapter_rehearsal_ready"])
	status := safeProviderReviewMutationArmingStatus(stringFromMap(value, "status"))
	if status == "armed" {
		status = "ready_to_arm"
	}
	if !executionEnabled || !rehearsalReady {
		status = "blocked"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_mutation_arming_plan",
		"provider_type":                  cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(value, "review_kind")),
		"required_config":                "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
		"execution_enabled_config":       executionEnabled,
		"adapter_rehearsal_ready":        rehearsalReady,
		"mutation_armed_config":          boolOnlyFromAny(value["mutation_armed_config"]),
		"mutation_armed":                 false,
		"blocked_reasons":                safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_adapter_rehearsal":     true,
		"adapter_mutation_currently_off": true,
		"next_step":                      cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func safeProviderReviewMutationArmingStatus(value string) string {
	switch cleanOptionalText(value) {
	case "blocked", "ready_to_arm", "armed":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func sanitizedProviderReviewAdapterRehearsal(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	operations := sanitizedProviderReviewAdapterRehearsalOperations(mapSliceFromAny(value["operations"]))
	readyCount := 0
	blockedCount := 0
	for _, operation := range operations {
		if operation["status"] == "ready" {
			readyCount++
		} else {
			blockedCount++
		}
	}
	status := "not_recorded"
	if len(operations) > 0 {
		status = "ready"
	}
	if blockedCount > 0 {
		status = "blocked"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_adapter_rehearsal",
		"provider_type":                  cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":                 safeProviderReviewAdapterStatus(stringFromMap(value, "adapter_status")),
		"operation_count":                len(operations),
		"ready_operation_count":          readyCount,
		"blocked_operation_count":        blockedCount,
		"blocked_reasons":                safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
		"operations":                     operations,
		"mutation_arming_candidate":      status == "ready" && blockedCount == 0 && len(operations) > 0,
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"adapter_mutation_currently_off": true,
	}
}

func sanitizedProviderReviewAdapterRehearsalOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                   cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":           cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                 safeProviderReviewRehearsalStatus(stringFromMap(item, "status")),
			"blocked_reasons":        safeProviderReviewBlockedReasons(stringSliceFromAny(item["blocked_reasons"])),
			"external_call_made":     false,
			"provider_api_call_made": false,
			"provider_api_mutation":  "disabled",
		})
	}
	return out
}

func safeProviderReviewRehearsalStatus(value string) string {
	switch cleanOptionalText(value) {
	case "ready", "blocked", "not_recorded":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func sanitizedProviderReviewAdapterContract(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                cleanOptionalText(stringFromMap(value, "status")),
		"adapter_status":        cleanOptionalText(stringFromMap(value, "adapter_status")),
		"contract_version":      cleanOptionalText(stringFromMap(value, "contract_version")),
		"provider_type":         cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":           cleanOptionalText(stringFromMap(value, "review_kind")),
		"external_call_made":    false,
		"provider_api_mutation": "disabled",
		"contains_token":        false,
		"contains_file_content": false,
		"operations":            sanitizedProviderReviewAdapterContractOperations(mapSliceFromAny(value["operations"])),
		"request_envelopes":     sanitizedProviderReviewAdapterRequestEnvelopes(mapSliceFromAny(value["request_envelopes"])),
		"response_diagnostics":  sanitizedProviderReviewAdapterResponseDiagnostics(mapFromAny(value["response_diagnostics"])),
		"idempotency_plan":      sanitizedProviderReviewAdapterIdempotencyPlan(mapFromAny(value["idempotency_plan"])),
		"next_step":             cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewAdapterIdempotencyPlan(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                     cleanOptionalText(stringFromMap(value, "status")),
		"mode":                       "redacted_idempotency_plan",
		"provider_type":              cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":             cleanOptionalText(stringFromMap(value, "adapter_status")),
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"contains_token":             false,
		"contains_provider_url":      false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
		"idempotency_key_included":   false,
		"idempotency_key_material":   "redacted_required_material_only",
		"requires_persisted_attempt": boolOnlyFromAny(value["requires_persisted_attempt"]),
		"retry_after_diagnostics":    boolOnlyFromAny(value["retry_after_diagnostics"]),
		"operations":                 sanitizedProviderReviewAdapterIdempotencyOperations(mapSliceFromAny(value["operations"])),
	}
}

func sanitizedProviderReviewAdapterIdempotencyOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                          cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":                  cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                        cleanOptionalText(stringFromMap(item, "status")),
			"idempotency_key_kind":          "operation_scope_hash",
			"idempotency_key_included":      false,
			"idempotency_key_material":      "redacted_required_material_only",
			"replay_check":                  cleanOptionalText(stringFromMap(item, "replay_check")),
			"conflict_policy":               cleanOptionalText(stringFromMap(item, "conflict_policy")),
			"retry_policy":                  cleanOptionalText(stringFromMap(item, "retry_policy")),
			"requires_persisted_attempt":    boolOnlyFromAny(item["requires_persisted_attempt"]),
			"contains_token":                false,
			"contains_provider_url":         false,
			"contains_repository_ref":       false,
			"contains_branch_name":          false,
			"contains_file_content":         false,
			"external_call_made":            false,
			"provider_api_call_made":        false,
			"provider_api_mutation":         "disabled",
			"response_diagnostics_required": boolOnlyFromAny(item["response_diagnostics_required"]),
		})
	}
	return out
}

func sanitizedProviderReviewAdapterResponseDiagnostics(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                 cleanOptionalText(stringFromMap(value, "status")),
		"mode":                   "redacted_response_diagnostics",
		"provider_type":          cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":            cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":         cleanOptionalText(stringFromMap(value, "adapter_status")),
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"response_body_included": false,
		"headers_included":       false,
		"contains_token":         false,
		"contains_provider_url":  false,
		"diagnostic_fields":      stringSliceFromAny(value["diagnostic_fields"]),
		"operations":             sanitizedProviderReviewAdapterResponseDiagnosticOperations(mapSliceFromAny(value["operations"])),
	}
}

func sanitizedProviderReviewAdapterResponseDiagnosticOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                     cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":             cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                   cleanOptionalText(stringFromMap(item, "status")),
			"success_status_class":     cleanOptionalText(stringFromMap(item, "success_status_class")),
			"retryable_status_classes": stringSliceFromAny(item["retryable_status_classes"]),
			"response_body_included":   false,
			"headers_included":         false,
			"contains_token":           false,
			"contains_provider_url":    false,
			"external_call_made":       false,
			"provider_api_mutation":    "disabled",
		})
	}
	return out
}

func sanitizedProviderReviewAdapterRequestEnvelopes(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                    cleanOptionalText(stringFromMap(item, "name")),
			"method":                  cleanOptionalText(stringFromMap(item, "method")),
			"endpoint_key":            cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"payload_shape":           cleanOptionalText(stringFromMap(item, "payload_shape")),
			"file_count":              intFromAny(item["file_count"], 0),
			"payload_redacted":        true,
			"contains_token":          false,
			"contains_file_content":   false,
			"contains_provider_url":   false,
			"contains_repository_ref": false,
			"api_call":                false,
			"provider_api_mutation":   "disabled",
			"execution_status":        cleanOptionalText(stringFromMap(item, "execution_status")),
			"blocked_reason":          cleanOptionalText(stringFromMap(item, "blocked_reason")),
			"readiness":               sanitizedProviderReviewAdapterRequestReadiness(mapSliceFromAny(item["readiness"])),
		})
	}
	return out
}

func sanitizedProviderReviewAdapterRequestReadiness(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"evidence": cleanOptionalText(stringFromMap(item, "evidence")),
			"status":   cleanOptionalText(stringFromMap(item, "status")),
		})
	}
	return out
}

func sanitizedProviderReviewAdapterContractOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"required_capability":   cleanOptionalText(stringFromMap(item, "required_capability")),
			"required_scope":        cleanOptionalText(stringFromMap(item, "required_scope")),
			"payload_shape":         cleanOptionalText(stringFromMap(item, "payload_shape")),
			"adapter_status":        cleanOptionalText(stringFromMap(item, "adapter_status")),
			"execution_status":      cleanOptionalText(stringFromMap(item, "execution_status")),
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		})
	}
	return out
}

func sanitizedProviderReviewReconciliationOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                cleanOptionalText(stringFromMap(item, "status")),
			"blocked_reason":        cleanOptionalText(stringFromMap(item, "blocked_reason")),
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
		})
	}
	return out
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
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.delegation.create") {
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
	if !s.syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.delegation.revoke") {
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
	payload := approvalWebhookPayload(approval, event)
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

func approvalWebhookPayload(approval map[string]any, event string) map[string]any {
	// Approval notifications intentionally use an allowlist so external
	// destinations never receive raw request payloads, secrets, or rule metadata.
	return map[string]any{
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
		op, run, err := s.enqueueSSHCommandRun(ctx, tx, stringFromMap(payload, "machine_id"), mapFromAny(payload["input"]), actorID, "ssh.exec", "")
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
	case "argo_pod_logs":
		op, err := s.enqueueArgoPodLogOperationTx(ctx, tx, mapFromAny(payload["input"]))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "config_git_commit":
		repoID := cleanOptionalID(stringFromMap(payload, "repo_id"))
		projectID := cleanOptionalID(stringFromMap(payload, "project_id"))
		if repoID == "" || projectID == "" {
			return nil, "", fmt.Errorf("config git workflow approval is missing repository metadata")
		}
		repo, remotes, _, preview, err := s.configRepositoryScaffoldPreviewForRequest(ctx, repoID, projectID)
		if err != nil {
			return nil, "", err
		}
		commitPlan := mapFromAny(preview["git_commit_plan"])
		if commitPlan["plan_state"] != "planned" {
			return nil, "", fmt.Errorf("config git workflow is not ready")
		}
		op, err := enqueueConfigRepositoryGitWorkflowTx(ctx, tx, projectID, repo, remotes, preview, actorID)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{
			"operation":                op,
			"operation_request_result": configRepositoryGitWorkflowRequestResult(op),
			"git_commit_plan":          commitPlan,
			"external_call_made":       false,
			"git_write_performed":      false,
			"file_content_included":    false,
			"secret_included":          false,
		}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "operation_cancel":
		op, err := s.cancelOperationRun(ctx, tx, stringFromMap(payload, "operation_id"))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"operation": op}, cleanOptionalID(fmt.Sprint(op["id"])), nil
	case "project_template_provider_review_execute":
		request := mapFromAny(payload["execution_request"])
		starterFilePayload := providerReviewStarterFilePayloadForExecution(ctx, tx, payload)
		providerAPIRequestPlan := templateProviderReviewAPIRequestPlan(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			stringFromMap(request, "source_branch"),
			stringFromMap(request, "target_branch"),
			starterFilePayload,
		)
		guardrail := templateProviderReviewExecutionGuardrailWithStaging(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			stringFromMap(request, "source_branch"),
			stringFromMap(request, "target_branch"),
			s.cfg.ProviderReviewExecutionEnabled,
			s.cfg.ProviderReviewMutationArmed,
			starterFilePayloadReady(starterFilePayload),
		)
		credentialStrategy := sanitizedProviderReviewCredentialStrategy(mapFromAny(payload["credential_strategy"]))
		if len(mapFromAny(payload["credential_strategy"])) == 0 {
			credentialStrategy = sanitizedProviderReviewCredentialStrategy(mapFromAny(mapFromAny(payload["provider_review_reconciliation"])["credential_strategy"]))
		}
		reconciliation := templateProviderReviewExecutionReconciliation(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			starterFilePayload,
			guardrail,
			providerAPIRequestPlan,
			credentialStrategy,
		)
		targetSummary := providerReviewExecutionTargetSummary(
			stringFromMap(request, "provider_type"),
			stringFromMap(request, "review_kind"),
			providerAPIRequestPlan,
			starterFilePayload,
			reconciliation,
		)
		attemptLedger, err := s.recordProviderReviewAttemptLedger(
			ctx,
			tx,
			cleanOptionalID(fmt.Sprint(approval["id"])),
			stringFromMap(payload, "project_template_run_id"),
			reconciliation,
		)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{
			"project_template_run_id":        stringFromMap(payload, "project_template_run_id"),
			"execution_request":              request,
			"execution_guardrail":            guardrail,
			"credential_strategy":            credentialStrategy,
			"starter_file_payload":           starterFilePayload,
			"provider_api_request_plan":      providerAPIRequestPlan,
			"provider_review_reconciliation": reconciliation,
			"provider_review_target_summary": targetSummary,
			"provider_review_attempt_ledger": attemptLedger,
			"provider_api_call_made":         false,
			"provider_api_mutation":          "disabled",
			"execution_enabled":              false,
			"message":                        "Provider review execution approval was recorded; provider API branch creation and PR/MR mutation remain disabled.",
		}, "", nil
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

func (s *Server) recordProviderReviewAttemptLedger(ctx context.Context, tx *sqlx.Tx, approvalID, runID string, reconciliation map[string]any) (map[string]any, error) {
	summary := providerReviewAttemptLedgerSummary(nil)
	if tx == nil || approvalID == "" {
		return summary, nil
	}
	idempotencyPlan := mapFromAny(reconciliation["idempotency_plan"])
	operations := mapSliceFromAny(idempotencyPlan["operations"])
	if len(operations) == 0 {
		return summary, nil
	}
	provider := cleanOptionalText(stringFromMap(reconciliation, "provider_type"))
	reviewKind := cleanOptionalText(stringFromMap(reconciliation, "review_kind"))
	projectTemplateRunID := cleanOptionalID(runID)
	attempts := make([]map[string]any, 0, len(operations))
	for _, operation := range operations {
		name := cleanOptionalText(stringFromMap(operation, "name"))
		if name == "" {
			continue
		}
		endpointKey := cleanOptionalText(stringFromMap(operation, "endpoint_key"))
		dependency := providerReviewAttemptDependency(name)
		requestSummary := providerReviewAttemptRequestSummary(operation, providerReviewExecutionBlueprintOperationForEndpoint(reconciliation, endpointKey))
		responseDiagnostics := providerReviewAttemptResponseDiagnostics(reconciliation, endpointKey)
		requestJSON, err := jsonParam(requestSummary)
		if err != nil {
			return summary, fmt.Errorf("encoding provider review attempt request summary: %w", err)
		}
		responseJSON, err := jsonParam(responseDiagnostics)
		if err != nil {
			return summary, fmt.Errorf("encoding provider review attempt response diagnostics: %w", err)
		}
		idempotencyJSON, err := jsonParam(map[string]any{"material": "redacted_required_material_only"})
		if err != nil {
			return summary, fmt.Errorf("encoding provider review attempt idempotency material: %w", err)
		}
		attempt, err := queryOne(ctx, tx, `
			INSERT INTO provider_review_attempts(
				operation_approval_id,
				project_template_run_id,
				provider_type,
				review_kind,
				operation_name,
				endpoint_key,
				status,
				replay_check,
				conflict_policy,
				retry_policy,
				operation_order,
				depends_on_operation,
				dependency_status,
				idempotency_key_kind,
				idempotency_key_hash,
				idempotency_key_material,
				request_summary,
				response_diagnostics,
				provider_api_call_made,
				provider_api_mutation,
				external_call_made
			)
			VALUES (
				$1,
				NULLIF($2,'')::uuid,
				$3,
				$4,
				$5,
				$6,
				'planned',
				$7,
				$8,
				$9,
				$10,
				$11,
				$12,
				'operation_scope_hash',
				'',
				$13::jsonb,
				$14::jsonb,
				$15::jsonb,
				false,
				'disabled',
				false
			)
			ON CONFLICT (operation_approval_id, operation_name) DO UPDATE
			SET endpoint_key=EXCLUDED.endpoint_key,
				status=EXCLUDED.status,
				replay_check=EXCLUDED.replay_check,
				conflict_policy=EXCLUDED.conflict_policy,
				retry_policy=EXCLUDED.retry_policy,
				operation_order=EXCLUDED.operation_order,
				depends_on_operation=EXCLUDED.depends_on_operation,
				dependency_status=EXCLUDED.dependency_status,
				request_summary=EXCLUDED.request_summary,
				response_diagnostics=EXCLUDED.response_diagnostics,
				updated_at=now()
			RETURNING id, operation_name, endpoint_key, status, replay_check, conflict_policy, retry_policy, operation_order, depends_on_operation, dependency_status, request_summary, response_diagnostics, provider_api_call_made, provider_api_mutation, external_call_made`,
			approvalID,
			projectTemplateRunID,
			provider,
			reviewKind,
			name,
			endpointKey,
			cleanOptionalText(stringFromMap(operation, "replay_check")),
			cleanOptionalText(stringFromMap(operation, "conflict_policy")),
			cleanOptionalText(stringFromMap(operation, "retry_policy")),
			dependency["operation_order"],
			dependency["depends_on_operation"],
			dependency["dependency_status"],
			idempotencyJSON,
			requestJSON,
			responseJSON,
		)
		if err != nil {
			return summary, fmt.Errorf("recording provider review attempt: %w", err)
		}
		attempts = append(attempts, attempt)
	}
	return providerReviewAttemptLedgerSummary(attempts), nil
}

func providerReviewExecutionBlueprintOperationForEndpoint(reconciliation map[string]any, endpointKey string) map[string]any {
	if endpointKey == "" {
		return map[string]any{}
	}
	blueprint := mapFromAny(reconciliation["execution_blueprint"])
	for _, operation := range mapSliceFromAny(blueprint["operations"]) {
		if cleanOptionalText(stringFromMap(operation, "endpoint_key")) == endpointKey {
			return operation
		}
	}
	return map[string]any{}
}

func providerReviewAttemptDependency(operationName string) map[string]any {
	switch cleanOptionalText(operationName) {
	case "create_branch_ref":
		return map[string]any{"operation_order": 10, "depends_on_operation": "", "dependency_status": "independent"}
	case "commit_starter_files":
		return map[string]any{"operation_order": 20, "depends_on_operation": "create_branch_ref", "dependency_status": "waiting_for_dependency"}
	case "open_review_request":
		return map[string]any{"operation_order": 30, "depends_on_operation": "commit_starter_files", "dependency_status": "waiting_for_dependency"}
	default:
		return map[string]any{"operation_order": 100, "depends_on_operation": "", "dependency_status": "independent"}
	}
}

func providerReviewAttemptRequestSummary(operation, executionBlueprintOperation map[string]any) map[string]any {
	return map[string]any{
		"mode":                        "redacted_attempt_request_summary",
		"operation_name":              safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")),
		"endpoint_key":                cleanOptionalText(stringFromMap(operation, "endpoint_key")),
		"payload_builder":             safeProviderReviewPayloadBuilderName(stringFromMap(executionBlueprintOperation, "payload_builder")),
		"response_handler":            safeProviderReviewResponseHandlerName(stringFromMap(executionBlueprintOperation, "response_handler")),
		"execution_status":            safeProviderReviewAdapterExecutionStatus(stringFromMap(executionBlueprintOperation, "execution_status")),
		"request_body_included":       false,
		"headers_included":            false,
		"idempotency_key_kind":        "operation_scope_hash",
		"idempotency_key_included":    false,
		"requires_provider_client":    true,
		"requires_request_builder":    true,
		"requires_response_handler":   true,
		"requires_idempotency_ledger": true,
		"provider_api_call_made":      false,
		"provider_api_mutation":       "disabled",
		"external_call_made":          false,
		"payload_redacted":            true,
		"contains_token":              false,
		"contains_provider_url":       false,
		"contains_repository_ref":     false,
		"contains_branch_name":        false,
		"contains_file_content":       false,
	}
}

func providerReviewAttemptResponseDiagnostics(reconciliation map[string]any, endpointKey string) map[string]any {
	responseDiagnostics := mapFromAny(reconciliation["response_diagnostics"])
	for _, operation := range mapSliceFromAny(responseDiagnostics["operations"]) {
		if cleanOptionalText(stringFromMap(operation, "endpoint_key")) == endpointKey {
			return map[string]any{
				"mode":                     "redacted_attempt_response_diagnostics",
				"endpoint_key":             safeProviderReviewEndpointKey(endpointKey),
				"status":                   safeProviderReviewAttemptResponseStatus(stringFromMap(operation, "status")),
				"success_status_class":     safeProviderReviewStatusClass(stringFromMap(operation, "success_status_class")),
				"retryable_status_classes": safeProviderReviewStatusClasses(stringSliceFromAny(operation["retryable_status_classes"])),
				"response_body_included":   false,
				"headers_included":         false,
				"contains_token":           false,
				"contains_provider_url":    false,
				"provider_api_call_made":   false,
				"provider_api_mutation":    "disabled",
				"external_call_made":       false,
			}
		}
	}
	return map[string]any{
		"mode":                     "redacted_attempt_response_diagnostics",
		"endpoint_key":             safeProviderReviewEndpointKey(endpointKey),
		"status":                   "pending",
		"success_status_class":     "",
		"retryable_status_classes": []string{},
		"response_body_included":   false,
		"headers_included":         false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"external_call_made":       false,
	}
}

func sanitizedProviderReviewAttemptOrchestration(value map[string]any, operations []map[string]any) map[string]any {
	summary := providerReviewAttemptOrchestrationSummary(operations)
	if len(value) > 0 {
		summary["status"] = safeProviderReviewAttemptOrchestrationStatus(stringFromMap(value, "status"))
	}
	return summary
}

func providerReviewAttemptLedgerSummary(attempts []map[string]any) map[string]any {
	operations := make([]map[string]any, 0, len(attempts))
	for _, attempt := range attempts {
		claimedAt := cleanOptionalText(fmt.Sprint(attempt["claimed_at"]))
		if claimedAt == "<nil>" {
			claimedAt = ""
		}
		operations = append(operations, map[string]any{
			"id":                       cleanOptionalID(fmt.Sprint(attempt["id"])),
			"name":                     cleanOptionalText(stringFromMap(attempt, "operation_name")),
			"endpoint_key":             cleanOptionalText(stringFromMap(attempt, "endpoint_key")),
			"status":                   safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
			"replay_check":             safeProviderReviewReplayCheck(stringFromMap(attempt, "replay_check")),
			"conflict_policy":          safeProviderReviewConflictPolicy(stringFromMap(attempt, "conflict_policy")),
			"retry_policy":             safeProviderReviewRetryPolicy(stringFromMap(attempt, "retry_policy")),
			"operation_order":          intFromAny(attempt["operation_order"], 0),
			"depends_on_operation":     safeProviderReviewAttemptDependencyName(stringFromMap(attempt, "depends_on_operation")),
			"dependency_status":        safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
			"request_summary":          sanitizedProviderReviewAttemptRequestSummary(mapFromAny(attempt["request_summary"])),
			"response_diagnostics":     sanitizedProviderReviewAttemptResponseDiagnostics(mapFromAny(attempt["response_diagnostics"])),
			"claim_recorded":           providerReviewAttemptClaimRecorded(attempt),
			"claimed_at":               claimedAt,
			"external_call_made":       false,
			"provider_api_call_made":   false,
			"provider_api_mutation":    "disabled",
			"idempotency_key_included": false,
		})
	}
	status := "not_recorded"
	if len(operations) > 0 {
		status = "recorded"
	}
	orchestration := providerReviewAttemptOrchestrationSummary(operations)
	return map[string]any{
		"status":                   status,
		"mode":                     "redacted_attempt_ledger",
		"attempt_count":            len(operations),
		"operations":               operations,
		"orchestration":            orchestration,
		"external_call_made":       false,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"idempotency_key_included": false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"contains_repository_ref":  false,
		"contains_branch_name":     false,
		"contains_file_content":    false,
	}
}

func sanitizedProviderReviewAttemptRequestSummary(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                        "redacted_attempt_request_summary",
		"operation_name":              safeProviderReviewAttemptOperationName(stringFromMap(value, "operation_name")),
		"endpoint_key":                cleanOptionalText(stringFromMap(value, "endpoint_key")),
		"payload_builder":             safeProviderReviewPayloadBuilderName(stringFromMap(value, "payload_builder")),
		"response_handler":            safeProviderReviewResponseHandlerName(stringFromMap(value, "response_handler")),
		"execution_status":            safeProviderReviewAdapterExecutionStatus(stringFromMap(value, "execution_status")),
		"request_body_included":       false,
		"headers_included":            false,
		"idempotency_key_kind":        "operation_scope_hash",
		"idempotency_key_included":    false,
		"requires_provider_client":    true,
		"requires_request_builder":    true,
		"requires_response_handler":   true,
		"requires_idempotency_ledger": true,
		"provider_api_call_made":      false,
		"provider_api_mutation":       "disabled",
		"external_call_made":          false,
		"payload_redacted":            true,
		"contains_token":              false,
		"contains_provider_url":       false,
		"contains_repository_ref":     false,
		"contains_branch_name":        false,
		"contains_file_content":       false,
	}
}

func sanitizedProviderReviewAttemptResponseDiagnostics(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                     "redacted_attempt_response_diagnostics",
		"endpoint_key":             safeProviderReviewEndpointKey(stringFromMap(value, "endpoint_key")),
		"status":                   safeProviderReviewAttemptResponseStatus(stringFromMap(value, "status")),
		"success_status_class":     safeProviderReviewStatusClass(stringFromMap(value, "success_status_class")),
		"retryable_status_classes": safeProviderReviewStatusClasses(stringSliceFromAny(value["retryable_status_classes"])),
		"response_body_included":   false,
		"headers_included":         false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"external_call_made":       false,
	}
}

func safeProviderReviewAttemptResponseStatus(value string) string {
	switch cleanOptionalText(value) {
	case "pending", "success", "retryable", "failed", "blocked":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewAttemptStatus(value string) string {
	switch cleanOptionalText(value) {
	case "planned", "running", "completed", "failed", "blocked":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewReplayCheck(value string) string {
	switch cleanOptionalText(value) {
	case "detect_existing_branch_ref", "detect_existing_commit_batch", "detect_existing_open_review":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewConflictPolicy(value string) string {
	switch cleanOptionalText(value) {
	case "treat_existing_matching_ref_as_success", "block_on_content_or_parent_conflict", "reuse_existing_review_request":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewRetryPolicy(value string) string {
	switch cleanOptionalText(value) {
	case "retry_only_after_response_diagnostics":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewStatusClass(value string) string {
	switch cleanOptionalText(value) {
	case "2xx", "4xx", "5xx":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewEndpointKey(value string) string {
	switch cleanOptionalText(value) {
	case "github.create_branch_ref", "github.commit_files", "github.open_review", "gitea.create_branch_ref", "gitea.commit_files", "gitea.open_review":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewProviderType(value string) string {
	switch cleanOptionalText(value) {
	case "github", "gitea":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewStatusClasses(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = safeProviderReviewStatusClass(item)
		if item == "" || seen[item] {
			continue
		}
		out = append(out, item)
		seen[item] = true
	}
	return out
}

func providerReviewAttemptOrchestrationSummary(operations []map[string]any) map[string]any {
	summary := map[string]any{
		"status":                     "not_recorded",
		"mode":                       "redacted_attempt_orchestration",
		"next_operation":             "",
		"ready_count":                0,
		"waiting_count":              0,
		"blocked_count":              0,
		"completed_count":            0,
		"dependency_chain_status":    "not_recorded",
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"idempotency_key_included":   false,
		"requires_operator_review":   true,
		"requires_adapter_execution": true,
		"dependency_chain_plan":      providerReviewAttemptDependencyChainPlan(nil, "not_recorded", "", 0, 0, 0, 0),
		"execution_candidate":        providerReviewAttemptExecutionCandidate(nil, ""),
	}
	if len(operations) == 0 {
		return summary
	}
	nextOperation := ""
	nextOperationSet := false
	readyCount := 0
	waitingCount := 0
	blockedCount := 0
	completedCount := 0
	failed := false
	for _, operation := range operations {
		name := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
		status := safeProviderReviewAttemptStatus(stringFromMap(operation, "status"))
		dependencyStatus := safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(operation, "dependency_status"))
		switch {
		case dependencyStatus == "dependency_failed" || status == "failed" || status == "blocked":
			blockedCount++
			failed = true
		case status == "completed":
			completedCount++
		case dependencyStatus == "waiting_for_dependency" || status == "running":
			waitingCount++
		case status == "planned" && (dependencyStatus == "independent" || dependencyStatus == "dependency_satisfied"):
			readyCount++
			if !nextOperationSet && name != "" {
				nextOperation = name
				nextOperationSet = true
			}
		default:
			blockedCount++
			failed = true
		}
	}
	chainStatus := "ready"
	if failed {
		chainStatus = "blocked"
	} else if waitingCount > 0 {
		chainStatus = "waiting_for_dependency"
	} else if completedCount == len(operations) {
		chainStatus = "completed"
	}
	summary["status"] = "planned"
	summary["next_operation"] = nextOperation
	summary["ready_count"] = readyCount
	summary["waiting_count"] = waitingCount
	summary["blocked_count"] = blockedCount
	summary["completed_count"] = completedCount
	summary["dependency_chain_status"] = chainStatus
	summary["dependency_chain_plan"] = providerReviewAttemptDependencyChainPlan(operations, chainStatus, nextOperation, readyCount, waitingCount, blockedCount, completedCount)
	summary["execution_candidate"] = providerReviewAttemptExecutionCandidate(operations, nextOperation)
	return summary
}

func providerReviewAttemptDependencyChainPlan(operations []map[string]any, chainStatus, nextOperation string, readyCount, waitingCount, blockedCount, completedCount int) map[string]any {
	orderedOperations := make([]map[string]any, 0, len(operations))
	for _, operation := range operations {
		name := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
		dependsOn := safeProviderReviewAttemptDependencyName(stringFromMap(operation, "depends_on_operation"))
		orderedOperations = append(orderedOperations, map[string]any{
			"name":                         name,
			"endpoint_key":                 safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key")),
			"operation_order":              intFromAny(operation["operation_order"], 0),
			"depends_on_operation":         dependsOn,
			"dependency_status":            safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(operation, "dependency_status")),
			"attempt_status":               safeProviderReviewAttemptStatus(stringFromMap(operation, "status")),
			"dependency_unlocks_operation": providerReviewAttemptDependencyUnlockOperation(name),
			"requires_dependency_update":   providerReviewAttemptDependencyUnlockOperation(name) != "",
			"external_call_made":           false,
			"provider_api_call_made":       false,
			"provider_api_mutation":        "disabled",
			"contains_token":               false,
			"contains_provider_url":        false,
			"contains_repository_ref":      false,
			"contains_branch_name":         false,
			"contains_file_content":        false,
		})
	}
	chainStatus = safeProviderReviewAttemptDependencyChainStatus(chainStatus)
	nextOperation = safeProviderReviewAttemptOperationName(nextOperation)
	return map[string]any{
		"mode":                          "redacted_attempt_dependency_chain_plan",
		"status":                        chainStatus,
		"next_operation":                nextOperation,
		"operation_count":               len(orderedOperations),
		"ready_count":                   readyCount,
		"waiting_count":                 waitingCount,
		"blocked_count":                 blockedCount,
		"completed_count":               completedCount,
		"ordered_operations":            orderedOperations,
		"chain_ready_for_next_attempt":  chainStatus == "ready" && nextOperation != "",
		"requires_ordered_claim":        true,
		"requires_dependency_update":    true,
		"requires_response_plan":        true,
		"requires_transaction_boundary": true,
		"requires_operator_review":      true,
		"requires_adapter_execution":    true,
		"dependency_updates_recorded":   false,
		"attempt_claim_recorded":        false,
		"provider_request_sent":         false,
		"provider_response_recorded":    false,
		"external_call_made":            false,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"idempotency_key_included":      false,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
		"suppressed_fields":             []string{"provider_url", "repository_ref", "branch_name", "file_content", "request_body", "response_body", "headers", "authorization_header", "idempotency_key", "token"},
		"disabled_backends":             []string{"provider_api_branch_create", "provider_api_file_commit", "provider_api_review_create", "provider_request_send", "provider_response_record"},
	}
}

func providerReviewAttemptExecutionCandidate(operations []map[string]any, nextOperation string) map[string]any {
	candidate := map[string]any{
		"mode":                          "redacted_attempt_execution_candidate",
		"status":                        "blocked",
		"next_operation":                "",
		"endpoint_key":                  "",
		"operation_order":               0,
		"requires_provider_client":      true,
		"requires_idempotency_ledger":   true,
		"requires_response_diagnostics": true,
		"requires_mutation_arming":      true,
		"adapter_contract":              map[string]any{},
		"claim_plan":                    map[string]any{},
		"dispatch_plan":                 map[string]any{},
		"adapter_implemented":           false,
		"mutation_armed":                false,
		"external_call_made":            false,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"idempotency_key_included":      false,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
		"blocked_reasons": []string{
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
		"gates": providerReviewAttemptExecutionCandidateGates(false, false, false),
	}
	nextOperation = safeProviderReviewAttemptOperationName(nextOperation)
	if nextOperation == "" {
		candidate["blocked_reasons"] = []string{"provider_review_attempt_not_ready"}
		return candidate
	}
	for _, operation := range operations {
		if safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")) != nextOperation {
			continue
		}
		requestSummary := mapFromAny(operation["request_summary"])
		responseDiagnostics := mapFromAny(operation["response_diagnostics"])
		endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
		idempotencyReady := boolOnlyFromAny(requestSummary["requires_idempotency_ledger"])
		responseReady := mapFromAny(responseDiagnostics)["mode"] == "redacted_attempt_response_diagnostics"
		adapterContract := providerReviewAttemptCandidateAdapterContract(operation, requestSummary, responseDiagnostics)
		claimPlan := providerReviewAttemptExecutionClaimPlan(operation, idempotencyReady, responseReady)
		candidate["next_operation"] = nextOperation
		candidate["endpoint_key"] = endpointKey
		candidate["operation_order"] = intFromAny(operation["operation_order"], 0)
		candidate["status"] = "blocked"
		candidate["adapter_contract"] = adapterContract
		candidate["claim_plan"] = claimPlan
		candidate["dispatch_plan"] = providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan)
		candidate["gates"] = providerReviewAttemptExecutionCandidateGates(true, idempotencyReady, responseReady)
		return candidate
	}
	candidate["blocked_reasons"] = []string{"provider_review_attempt_not_found"}
	return candidate
}

func providerReviewAttemptCandidateAdapterContract(operation, requestSummary, responseDiagnostics map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                            "redacted_attempt_adapter_contract",
		"operation_name":                  safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")),
		"endpoint_key":                    safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key")),
		"operation_order":                 intFromAny(operation["operation_order"], 0),
		"payload_builder":                 safeProviderReviewPayloadBuilderName(stringFromMap(requestSummary, "payload_builder")),
		"response_handler":                safeProviderReviewResponseHandlerName(stringFromMap(requestSummary, "response_handler")),
		"idempotency_key_kind":            "operation_scope_hash",
		"response_status":                 safeProviderReviewAttemptResponseStatus(stringFromMap(responseDiagnostics, "status")),
		"success_status_class":            safeProviderReviewStatusClass(stringFromMap(responseDiagnostics, "success_status_class")),
		"retryable_status_classes":        safeProviderReviewStatusClasses(stringSliceFromAny(responseDiagnostics["retryable_status_classes"])),
		"adapter_call_state":              "blocked",
		"requires_provider_client":        true,
		"requires_request_builder":        true,
		"requires_response_handler":       true,
		"requires_idempotency_ledger":     true,
		"requires_response_diagnostics":   true,
		"requires_mutation_arming":        true,
		"adapter_implemented":             false,
		"mutation_armed":                  false,
		"request_body_included":           false,
		"response_body_included":          false,
		"headers_included":                false,
		"idempotency_key_included":        false,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_branch_name":            false,
		"contains_file_content":           false,
		"activation_requirements":         []string{"provider_api_adapter_implemented", "provider_review_mutation_armed", "operator_approval_still_valid", "idempotency_ledger_claim"},
		"adapter_input_boundary_redacted": true,
	}
}

func providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	claimMetadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	idempotencyMetadataReady := providerReviewAttemptClaimPlanIdempotencyReadyForOperation(claimPlan, operationName, endpointKey)
	adapterContractReady := providerReviewAttemptPlanMatchesOperation(adapterContract, "redacted_attempt_adapter_contract", operationName, endpointKey)
	metadataReady := claimMetadataReady &&
		adapterContractReady &&
		operationName != "" &&
		endpointKey != "" &&
		providerType != ""
	blockedReasons := []string{
		"provider_review_attempt_claim_not_recorded",
		"provider_review_adapter_not_implemented",
		"provider_review_mutation_not_armed",
	}
	if !metadataReady {
		blockedReasons = append([]string{"provider_review_dispatch_metadata_not_ready"}, blockedReasons...)
	}
	if providerType == "" {
		blockedReasons = append([]string{"provider_review_dispatch_provider_unknown"}, blockedReasons...)
	}
	requestPlan := providerReviewAttemptAdapterRequestMaterializationPlan(operation, requestSummary, providerType)
	transportPlan := providerReviewAttemptAdapterTransportPlan(providerType, operationName)
	responsePlan := providerReviewAttemptAdapterResponsePlan(operation, requestSummary, responseDiagnostics)
	credentialPlan := providerReviewAttemptAdapterCredentialBindingPlan(providerType, operationName)
	runtimePlan := providerReviewAttemptAdapterRuntimePlan(providerType, operationName, endpointKey)
	transactionPlan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, responsePlan)
	branchPolicyPlan := providerReviewAttemptBranchPolicyPlan(operation, requestPlan)
	requestValidationPreflight := map[string]any{}
	if operationName != "" && endpointKey != "" && providerType != "" {
		requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
		branchPolicyReady := providerReviewAttemptBranchPolicyPlanReadyForOperation(branchPolicyPlan, operationName, endpointKey)
		credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
		transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
		responseReady := providerReviewAttemptResponseRecordingReadyForOperation(responsePlan, operationName, endpointKey)
		transactionReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
		requestValidationPreflight = map[string]any{
			"mode":                                "redacted_attempt_adapter_request_validation_preflight",
			"preflight_state":                     "blocked",
			"preflight_ready":                     false,
			"preflight_ready_reason":              "provider_review_request_validation_not_armed",
			"operation_name":                      operationName,
			"endpoint_key":                        endpointKey,
			"operation_order":                     intFromAny(operation["operation_order"], 0),
			"provider_type":                       providerType,
			"dispatch_metadata_ready":             metadataReady,
			"attempt_claim_metadata_ready":        claimMetadataReady,
			"idempotency_metadata_ready":          idempotencyMetadataReady,
			"request_materialization_ready":       requestReady,
			"branch_policy_metadata_ready":        branchPolicyReady,
			"credential_binding_ready":            credentialReady,
			"transport_metadata_ready":            transportReady,
			"response_recording_ready":            responseReady,
			"transaction_metadata_ready":          transactionReady,
			"protected_branch_policy_check":       false,
			"token_env_check":                     false,
			"request_validated":                   false,
			"request_body_included":               false,
			"headers_included":                    false,
			"authorization_header_included":       false,
			"provider_url_included":               false,
			"repository_ref_included":             false,
			"branch_name_included":                false,
			"file_content_included":               false,
			"external_call_made":                  false,
			"provider_api_call_made":              false,
			"provider_api_mutation":               "disabled",
			"contains_token":                      false,
			"contains_provider_url":               false,
			"contains_repository_ref":             false,
			"contains_branch_name":                false,
			"contains_file_content":               false,
			"preflight_boundary_redacted":         true,
			"requires_request_materialization":    true,
			"requires_branch_policy_verification": true,
			"requires_credential_binding":         true,
			"requires_transport_metadata":         true,
			"requires_response_recording":         true,
			"requires_transaction_boundary":       true,
			"requires_mutation_arming":            true,
			"blocked_reasons": []string{
				"provider_review_request_validation_not_armed",
				"provider_review_adapter_not_implemented",
				"provider_review_mutation_not_armed",
			},
		}
	}
	return map[string]any{
		"mode":                         "redacted_attempt_adapter_dispatch_plan",
		"dispatch_state":               "blocked",
		"dispatch_ready":               false,
		"dispatch_ready_reason":        "provider_api_adapter_dispatch_not_armed",
		"dispatch_metadata_ready":      metadataReady,
		"attempt_claim_metadata_ready": claimMetadataReady,
		"adapter_contract_ready":       adapterContractReady,
		"provider_type":                providerType,
		"adapter_kind":                 providerReviewAdapterKindForProvider(providerType),
		"operation_name":               operationName,
		"endpoint_key":                 endpointKey,
		"operation_order":              intFromAny(operation["operation_order"], 0),
		"method":                       providerReviewMethodForOperation(operationName),
		"payload_shape":                providerReviewPayloadShapeForOperation(operationName),
		"payload_builder":              safeProviderReviewPayloadBuilderName(stringFromMap(requestSummary, "payload_builder")),
		"response_handler":             safeProviderReviewResponseHandlerName(stringFromMap(requestSummary, "response_handler")),
		"request_materialization_plan": requestPlan,
		"transport_plan":               transportPlan,
		"response_plan":                responsePlan,
		"credential_binding_plan":      credentialPlan,
		"adapter_runtime_plan":         runtimePlan,
		"branch_policy_plan":           branchPolicyPlan,
		"transaction_plan":             transactionPlan,
		"request_validation_preflight": requestValidationPreflight,
		"invocation_plan":              providerReviewAttemptAdapterInvocationPlan(operation, claimPlan, requestPlan, credentialPlan, runtimePlan, branchPolicyPlan, transportPlan, responsePlan, transactionPlan),
		"idempotency_key_kind":         "operation_scope_hash",
		"requires_attempt_claim":       true,
		"requires_idempotency_claim":   true,
		"requires_provider_client":     true,
		"requires_request_builder":     true,
		"requires_response_handler":    true,
		"requires_mutation_arming":     true,
		"claim_recorded":               false,
		"idempotency_claim_recorded":   false,
		"adapter_implemented":          false,
		"mutation_armed":               false,
		"external_call_made":           false,
		"provider_api_call_made":       false,
		"provider_api_mutation":        "disabled",
		"request_body_included":        false,
		"response_body_included":       false,
		"headers_included":             false,
		"idempotency_key_included":     false,
		"contains_token":               false,
		"contains_provider_url":        false,
		"contains_repository_ref":      false,
		"contains_branch_name":         false,
		"contains_file_content":        false,
		"blocked_reasons":              blockedReasons,
		"dispatch_boundary_redacted":   true,
		"provider_request_id_included": false,
	}
}

func providerReviewAttemptAdapterInvocationPlan(operation, claimPlan, requestPlan, credentialPlan, runtimePlan, branchPolicyPlan, transportPlan, responsePlan, transactionPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	executionLockPlan := providerReviewAttemptAdapterExecutionLockPlan(operation, claimPlan, transactionPlan)
	providerSendPlan := providerReviewAttemptAdapterProviderSendPlan(operation, requestPlan, credentialPlan, runtimePlan, transportPlan)
	activationPlan := providerReviewAttemptAdapterActivationPlan(operation, claimPlan, executionLockPlan, credentialPlan, runtimePlan, requestPlan, transportPlan, providerSendPlan, responsePlan, transactionPlan)
	claimMetadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	executionLockReady := providerReviewAttemptExecutionLockPlanReadyForOperation(executionLockPlan, operationName, endpointKey)
	adapterActivationReady := providerReviewAttemptActivationPlanReadyForOperation(activationPlan, operationName, endpointKey)
	credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	runtimeReady := providerReviewAttemptRuntimePlanReadyForOperation(runtimePlan, operationName, endpointKey)
	branchPolicyReady := providerReviewAttemptBranchPolicyPlanReadyForOperation(branchPolicyPlan, operationName, endpointKey)
	requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
	transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	providerSendReady := providerReviewAttemptProviderSendPlanReadyForOperation(providerSendPlan, operationName, endpointKey)
	responseReady := providerReviewAttemptResponseRecordingReadyForOperation(responsePlan, operationName, endpointKey)
	transactionReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
	return map[string]any{
		"mode":                              "redacted_attempt_adapter_invocation_plan",
		"invocation_state":                  "blocked",
		"invocation_ready":                  false,
		"invocation_ready_reason":           "provider_api_invocation_not_armed",
		"operation_name":                    operationName,
		"endpoint_key":                      endpointKey,
		"operation_order":                   intFromAny(operation["operation_order"], 0),
		"invocation_sequence":               []string{"claim_attempt", "claim_idempotency", "claim_execution_lock", "evaluate_adapter_activation", "bind_credential", "select_adapter_runtime", "verify_branch_policy", "materialize_request", "send_provider_request", "record_response", "record_transaction_boundary", "unlock_dependency"},
		"required_subplans":                 []string{"claim_plan", "execution_lock_plan", "adapter_activation_plan", "credential_binding_plan", "adapter_runtime_plan", "branch_policy_plan", "request_materialization_plan", "transport_plan", "provider_send_plan", "response_plan", "transaction_plan"},
		"execution_lock_plan":               executionLockPlan,
		"adapter_activation_plan":           activationPlan,
		"provider_send_plan":                providerSendPlan,
		"claim_metadata_ready":              claimMetadataReady,
		"execution_lock_metadata_ready":     executionLockReady,
		"adapter_activation_metadata_ready": adapterActivationReady,
		"credential_binding_ready":          credentialReady,
		"adapter_runtime_ready":             runtimeReady,
		"branch_policy_metadata_ready":      branchPolicyReady,
		"request_materialization_ready":     requestReady,
		"transport_metadata_ready":          transportReady,
		"provider_send_metadata_ready":      providerSendReady,
		"response_recording_ready":          responseReady,
		"transaction_metadata_ready":        transactionReady,
		"claim_metadata_ready_reason":       providerReviewAttemptInvocationReadyReason(claimMetadataReady, "provider_review_claim_metadata_not_ready"),
		"execution_lock_ready_reason":       stringFromMap(executionLockPlan, "execution_lock_metadata_ready_reason"),
		"adapter_activation_ready_reason":   stringFromMap(activationPlan, "adapter_activation_metadata_ready_reason"),
		"adapter_runtime_ready_reason":      providerReviewAttemptInvocationReadyReason(runtimeReady, "provider_review_adapter_runtime_not_ready"),
		"branch_policy_ready_reason":        stringFromMap(branchPolicyPlan, "branch_policy_ready_reason"),
		"transport_metadata_ready_reason":   providerReviewAttemptInvocationReadyReason(transportReady, "provider_review_transport_metadata_not_ready"),
		"provider_send_ready_reason":        providerReviewAttemptInvocationReadyReason(providerSendReady, "provider_request_send_not_armed"),
		"transaction_metadata_ready_reason": providerReviewAttemptInvocationReadyReason(transactionReady, "provider_review_transaction_metadata_not_ready"),
		"requires_attempt_claim":            true,
		"requires_idempotency_claim":        true,
		"requires_execution_lock":           true,
		"requires_adapter_activation":       true,
		"requires_credential_binding":       true,
		"requires_adapter_runtime":          true,
		"requires_branch_policy":            true,
		"requires_request_materialization":  true,
		"requires_transport":                true,
		"requires_response_recording":       true,
		"requires_transaction_boundary":     true,
		"requires_mutation_arming":          true,
		"attempt_claim_recorded":            false,
		"idempotency_claim_recorded":        false,
		"execution_lock_acquired":           false,
		"adapter_activation_approved":       false,
		"duplicate_send_detected":           false,
		"credential_bound":                  false,
		"adapter_runtime_bound":             false,
		"branch_policy_verified":            false,
		"request_materialized":              false,
		"provider_request_sent":             false,
		"response_recorded":                 false,
		"transaction_recorded":              false,
		"dependency_update_recorded":        false,
		"adapter_implemented":               false,
		"mutation_armed":                    false,
		"external_call_made":                false,
		"provider_api_call_made":            false,
		"provider_api_mutation":             "disabled",
		"request_body_included":             false,
		"response_body_included":            false,
		"headers_included":                  false,
		"authorization_header_included":     false,
		"provider_url_included":             false,
		"idempotency_key_included":          false,
		"contains_token":                    false,
		"contains_provider_url":             false,
		"contains_repository_ref":           false,
		"contains_branch_name":              false,
		"contains_file_content":             false,
		"invocation_boundary_redacted":      true,
		"blocked_reasons": []string{
			"provider_review_attempt_claim_not_recorded",
			"provider_review_execution_lock_not_acquired",
			"provider_review_adapter_activation_not_armed",
			"provider_credential_runtime_binding_not_armed",
			"provider_review_adapter_runtime_not_bound",
			"provider_branch_policy_not_armed",
			"provider_request_not_materialized",
			"provider_api_call_not_made",
			"provider_review_transaction_not_recorded",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptBranchPolicyPlan(operation, requestPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	metadataReady := providerReviewAttemptPlanMatchesOperation(requestPlan, providerReviewAttemptAdapterRequestMaterializationPlanMode, operationName, endpointKey)
	return map[string]any{
		"mode":                                  "redacted_attempt_branch_policy_plan",
		"branch_policy_state":                   "blocked",
		"branch_policy_ready":                   false,
		"branch_policy_ready_reason":            "provider_branch_policy_not_armed",
		"branch_policy_metadata_ready":          metadataReady,
		"operation_name":                        operationName,
		"endpoint_key":                          endpointKey,
		"operation_order":                       intFromAny(operation["operation_order"], 0),
		"policy_scope":                          "provider_review_attempt_operation",
		"target_branch_policy":                  "protected_default_branch_no_direct_write",
		"review_branch_policy":                  "required_before_starter_file_commit",
		"requires_review_branch":                true,
		"requires_default_branch_protection":    true,
		"requires_review_request":               true,
		"requires_operator_policy_review":       true,
		"requires_mutation_arming":              true,
		"default_branch_direct_write_allowed":   false,
		"protected_branch_direct_write_allowed": false,
		"starter_file_commit_to_default":        false,
		"review_branch_materialized":            false,
		"default_branch_materialized":           false,
		"protected_branch_rules_materialized":   false,
		"branch_policy_verified":                false,
		"branch_ref_created":                    false,
		"review_request_created":                false,
		"external_call_made":                    false,
		"provider_api_call_made":                false,
		"provider_api_mutation":                 "disabled",
		"repository_ref_included":               false,
		"branch_name_included":                  false,
		"protected_branch_rules_included":       false,
		"contains_token":                        false,
		"contains_provider_url":                 false,
		"contains_repository_ref":               false,
		"contains_branch_name":                  false,
		"contains_file_content":                 false,
		"branch_policy_boundary_redacted":       true,
		"branch_policy_sequence":                []string{"verify_target_branch_policy", "require_review_branch_strategy", "block_default_branch_direct_write", "require_review_request", "handoff_to_provider_adapter"},
		"branch_policy_suppressed_fields":       []string{"default_branch", "target_branch", "review_branch", "branch_ref", "repository_ref", "protected_branch_rules", "provider_url", "authorization_header", "token", "file_content"},
		"blocked_reasons":                       []string{"provider_branch_policy_not_armed", "protected_default_branch_direct_write_disabled", "provider_review_adapter_not_implemented", "provider_review_mutation_not_armed"},
	}
}

func providerReviewAttemptAdapterActivationPlan(operation, claimPlan, executionLockPlan, credentialPlan, runtimePlan, requestPlan, transportPlan, providerSendPlan, responsePlan, transactionPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	if providerType == "" {
		return map[string]any{}
	}
	liveAdapterPlan := providerReviewAttemptLiveAdapterPlan(providerType, operationName, endpointKey)
	claimReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	executionLockReady := providerReviewAttemptExecutionLockPlanReadyForOperation(executionLockPlan, operationName, endpointKey)
	credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	runtimeReady := providerReviewAttemptRuntimePlanReadyForOperation(runtimePlan, operationName, endpointKey)
	requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
	transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	providerSendReady := providerReviewAttemptProviderSendPlanReadyForOperation(providerSendPlan, operationName, endpointKey)
	responseReady := providerReviewAttemptResponseRecordingReadyForOperation(responsePlan, operationName, endpointKey)
	transactionReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
	metadataReady := claimReady &&
		executionLockReady &&
		credentialReady &&
		runtimeReady &&
		requestReady &&
		transportReady &&
		providerSendReady &&
		responseReady &&
		transactionReady
	return map[string]any{
		"mode":                                      "redacted_attempt_adapter_activation_plan",
		"adapter_activation_state":                  "blocked",
		"adapter_activation_ready":                  false,
		"adapter_activation_ready_reason":           "provider_review_adapter_activation_not_armed",
		"adapter_activation_metadata_ready":         metadataReady,
		"adapter_activation_metadata_ready_reason":  providerReviewAttemptAdapterActivationMetadataReadyReason(claimReady, executionLockReady, credentialReady, runtimeReady, requestReady, transportReady, providerSendReady, responseReady, transactionReady),
		"operation_name":                            operationName,
		"endpoint_key":                              endpointKey,
		"operation_order":                           intFromAny(operation["operation_order"], 0),
		"live_adapter_plan":                         liveAdapterPlan,
		"activation_scope":                          "provider_review_attempt_operation",
		"activation_policy":                         "require_all_redacted_subplans_and_mutation_gate",
		"requires_live_adapter":                     true,
		"requires_attempt_claim":                    true,
		"requires_execution_lock":                   true,
		"requires_credential_binding":               true,
		"requires_adapter_runtime":                  true,
		"requires_request_materialization":          true,
		"requires_transport":                        true,
		"requires_provider_send_plan":               true,
		"requires_response_recording":               true,
		"requires_transaction_boundary":             true,
		"requires_mutation_arming":                  true,
		"claim_metadata_ready":                      claimReady,
		"execution_lock_metadata_ready":             executionLockReady,
		"credential_binding_ready":                  credentialReady,
		"adapter_runtime_ready":                     runtimeReady,
		"request_materialization_ready":             requestReady,
		"transport_metadata_ready":                  transportReady,
		"provider_send_metadata_ready":              providerSendReady,
		"response_recording_ready":                  responseReady,
		"transaction_metadata_ready":                transactionReady,
		"live_adapter_registered":                   boolOnlyFromAny(liveAdapterPlan["live_adapter_registered"]),
		"adapter_implemented":                       false,
		"live_adapter_implemented":                  boolOnlyFromAny(liveAdapterPlan["live_adapter_implemented"]),
		"adapter_activation_approved":               false,
		"mutation_gate_armed":                       false,
		"provider_request_sent":                     false,
		"external_call_made":                        false,
		"provider_api_call_made":                    false,
		"provider_api_mutation":                     "disabled",
		"request_body_included":                     false,
		"response_body_included":                    false,
		"headers_included":                          false,
		"authorization_header_included":             false,
		"provider_url_included":                     false,
		"idempotency_key_included":                  false,
		"provider_request_id_included":              false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"adapter_activation_boundary_redacted":      true,
		"adapter_activation_sequence":               []string{"verify_live_adapter_registry", "verify_claim_metadata", "verify_execution_lock_metadata", "verify_credential_binding", "verify_runtime_contract", "verify_request_materialization", "verify_transport_contract", "verify_provider_send_contract", "verify_response_recording", "verify_transaction_boundary", "verify_mutation_arming"},
		"adapter_activation_suppressed_fields":      []string{"provider_url", "authorization_header", "token", "request_body", "response_body", "repository_ref", "branch_name", "file_content", "idempotency_key", "lock_key"},
		"adapter_activation_required_config_gates":  []string{"ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION", "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION"},
		"adapter_activation_required_interfaces":    []string{"providerReviewAttemptLiveAdapter", "providerReviewAttemptAdapterRuntime", "providerReviewAttemptRequestBuilder", "providerReviewAttemptProviderClientFactory", "providerReviewAttemptExecuteMethod", "providerReviewAttemptResponseHandler"},
		"adapter_activation_required_capabilities":  providerReviewClientRequiredCapabilitiesForOperation(operationName),
		"adapter_activation_required_status_inputs": []string{"claim_metadata_ready", "execution_lock_metadata_ready", "credential_binding_ready", "runtime_ready", "request_materialization_ready", "transport_ready", "provider_send_metadata_ready", "response_recording_ready", "transaction_metadata_ready"},
		"blocked_reasons": []string{
			"provider_review_adapter_activation_not_armed",
			"provider_review_activation_metadata_not_ready",
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterActivationMetadataReadyReason(claimReady, executionLockReady, credentialReady, runtimeReady, requestReady, transportReady, providerSendReady, responseReady, transactionReady bool) string {
	switch {
	case !claimReady:
		return "provider_review_activation_claim_metadata_not_ready"
	case !executionLockReady:
		return "provider_review_activation_execution_lock_not_ready"
	case !credentialReady:
		return "provider_review_activation_credential_binding_not_ready"
	case !runtimeReady:
		return "provider_review_activation_adapter_runtime_not_ready"
	case !requestReady:
		return "provider_review_activation_request_materialization_not_ready"
	case !transportReady:
		return "provider_review_activation_transport_not_ready"
	case !providerSendReady:
		return "provider_review_activation_provider_send_not_ready"
	case !responseReady:
		return "provider_review_activation_response_recording_not_ready"
	case !transactionReady:
		return "provider_review_activation_transaction_not_ready"
	default:
		return "ready"
	}
}

func providerReviewAttemptAdapterExecutionLockPlan(operation, claimPlan, transactionPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	claimMetadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	transactionMetadataReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
	metadataReady := claimMetadataReady && transactionMetadataReady
	return map[string]any{
		"mode":                                  "redacted_attempt_adapter_execution_lock_plan",
		"execution_lock_state":                  "blocked",
		"execution_lock_ready":                  false,
		"execution_lock_ready_reason":           "provider_review_execution_lock_not_armed",
		"execution_lock_metadata_ready":         metadataReady,
		"execution_lock_metadata_ready_reason":  providerReviewAttemptExecutionLockMetadataReadyReason(claimMetadataReady, transactionMetadataReady),
		"operation_name":                        operationName,
		"endpoint_key":                          endpointKey,
		"operation_order":                       intFromAny(operation["operation_order"], 0),
		"claim_status_from":                     "planned",
		"claim_status_to":                       "running",
		"lock_scope":                            "provider_review_attempt_operation",
		"lock_key_kind":                         "attempt_operation_hash",
		"duplicate_send_policy":                 "block_duplicate_provider_send",
		"stale_running_policy":                  "manual_recovery_required",
		"requires_database_transaction":         true,
		"requires_attempt_claim":                true,
		"requires_attempt_status_planned":       true,
		"requires_dependency_ready":             true,
		"requires_optimistic_lock":              true,
		"requires_idempotency_claim":            true,
		"requires_mutation_arming":              true,
		"claim_metadata_ready":                  claimMetadataReady,
		"transaction_metadata_ready":            transactionMetadataReady,
		"attempt_claim_recorded":                false,
		"idempotency_claim_recorded":            false,
		"execution_lock_acquired":               false,
		"optimistic_lock_verified":              false,
		"duplicate_send_detected":               false,
		"stale_running_recovered":               false,
		"provider_request_sent":                 false,
		"external_call_made":                    false,
		"provider_api_call_made":                false,
		"provider_api_mutation":                 "disabled",
		"request_body_included":                 false,
		"response_body_included":                false,
		"headers_included":                      false,
		"authorization_header_included":         false,
		"provider_url_included":                 false,
		"idempotency_key_included":              false,
		"provider_request_id_included":          false,
		"contains_token":                        false,
		"contains_provider_url":                 false,
		"contains_repository_ref":               false,
		"contains_branch_name":                  false,
		"contains_file_content":                 false,
		"execution_lock_boundary_redacted":      true,
		"execution_lock_sequence":               []string{"verify_attempt_status_planned", "verify_dependency_ready", "claim_attempt_running", "claim_idempotency_scope", "mark_duplicate_send_guard", "release_lock_after_transaction"},
		"execution_lock_suppressed_fields":      []string{"lock_key", "idempotency_key", "provider_request_id", "provider_url", "authorization_header", "token", "repository_ref", "branch_name", "file_content"},
		"execution_lock_transaction_boundaries": []string{"claim_attempt_start", "duplicate_send_guard", "provider_call_boundary", "attempt_status_update"},
		"blocked_reasons": []string{
			"provider_review_execution_lock_not_armed",
			"provider_review_attempt_claim_not_recorded",
			"provider_idempotency_ledger_not_claimed",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptExecutionLockMetadataReadyReason(claimMetadataReady, transactionMetadataReady bool) string {
	if !claimMetadataReady {
		return "provider_review_execution_lock_claim_metadata_not_ready"
	}
	if !transactionMetadataReady {
		return "provider_review_execution_lock_transaction_metadata_not_ready"
	}
	return "ready"
}

func providerReviewAttemptAdapterProviderSendPlan(operation, requestPlan, credentialPlan, runtimePlan, transportPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	if operationName == "" || endpointKey == "" || providerType == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	retryBackoffPlan := providerReviewAttemptAdapterRetryBackoffPlan(operation, transportPlan)
	requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
	credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	runtimeReady := providerReviewAttemptRuntimePlanReadyForOperation(runtimePlan, operationName, endpointKey)
	transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	metadataReady := requestReady &&
		credentialReady &&
		runtimeReady &&
		transportReady
	return map[string]any{
		"mode":                              "redacted_attempt_adapter_provider_send_plan",
		"provider_send_state":               "blocked",
		"provider_send_ready":               false,
		"provider_send_ready_reason":        "provider_request_send_not_armed",
		"provider_send_metadata_ready":      metadataReady,
		"provider_type":                     providerType,
		"operation_name":                    operationName,
		"endpoint_key":                      endpointKey,
		"operation_order":                   intFromAny(operation["operation_order"], 0),
		"method":                            providerReviewMethodForOperation(operationName),
		"payload_shape":                     providerReviewPayloadShapeForOperation(operationName),
		"auth_scheme":                       providerReviewAuthSchemeForProvider(providerType),
		"content_type":                      "application/json",
		"timeout_seconds":                   intFromAny(transportPlan["timeout_seconds"], 15),
		"retry_backoff_plan":                retryBackoffPlan,
		"requires_request_materialization":  true,
		"requires_credential_binding":       true,
		"requires_adapter_runtime":          true,
		"requires_transport":                true,
		"requires_retry_backoff_plan":       true,
		"requires_mutation_arming":          true,
		"request_materialization_ready":     requestReady,
		"credential_binding_ready":          credentialReady,
		"adapter_runtime_ready":             runtimeReady,
		"transport_metadata_ready":          transportReady,
		"request_path_materialized":         false,
		"request_url_materialized":          false,
		"request_body_materialized":         false,
		"headers_materialized":              false,
		"authorization_header_materialized": false,
		"provider_client_bound":             false,
		"credential_bound":                  false,
		"runtime_bound":                     false,
		"mutation_armed":                    false,
		"send_attempted":                    false,
		"provider_request_sent":             false,
		"provider_response_received":        false,
		"external_call_made":                false,
		"provider_api_call_made":            false,
		"provider_api_mutation":             "disabled",
		"request_body_included":             false,
		"response_body_included":            false,
		"headers_included":                  false,
		"authorization_header_included":     false,
		"provider_url_included":             false,
		"idempotency_key_included":          false,
		"provider_request_id_included":      false,
		"contains_token":                    false,
		"contains_provider_url":             false,
		"contains_repository_ref":           false,
		"contains_branch_name":              false,
		"contains_file_content":             false,
		"provider_send_boundary_redacted":   true,
		"provider_send_sequence":            []string{"bind_provider_client", "apply_redacted_transport_metadata", "verify_mutation_arming", "stage_provider_request", "send_provider_request", "handoff_to_response_handler"},
		"provider_send_suppressed_fields":   []string{"request_url", "request_path", "request_body", "request_headers", "authorization_header", "token", "idempotency_key", "repository_ref", "branch_name", "file_content"},
		"blocked_reasons": []string{
			"provider_request_send_not_armed",
			"provider_request_not_materialized",
			"provider_credential_runtime_binding_not_armed",
			"provider_review_adapter_runtime_not_bound",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterRetryBackoffPlan(operation, transportPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	if operationName == "" || endpointKey == "" || providerType == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) || !providerReviewAttemptPlanMatchesOperation(transportPlan, "redacted_attempt_adapter_transport_plan", operationName, endpointKey) {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                               "redacted_attempt_adapter_retry_backoff_plan",
		"retry_backoff_state":                "blocked",
		"retry_backoff_ready":                false,
		"retry_backoff_ready_reason":         "provider_retry_backoff_not_armed",
		"retry_backoff_metadata_ready":       true,
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"operation_order":                    intFromAny(operation["operation_order"], 0),
		"retry_policy":                       "retry_only_after_response_diagnostics",
		"max_attempts":                       3,
		"initial_backoff_seconds":            30,
		"max_backoff_seconds":                300,
		"jitter":                             "full",
		"retryable_status_classes":           providerReviewExpectedRetryClassesForOperation(operationName),
		"transport_retryable_status_classes": safeProviderReviewStatusClasses(stringSliceFromAny(transportPlan["retryable_status_classes"])),
		"requires_response_diagnostics":      true,
		"requires_idempotency_ledger":        true,
		"requires_attempt_ledger":            true,
		"requires_mutation_arming":           true,
		"retry_scheduled":                    false,
		"retry_attempt_recorded":             false,
		"retry_after_value_recorded":         false,
		"retry_after_header_included":        false,
		"provider_rate_limit_value_included": false,
		"provider_error_code_included":       false,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"request_body_included":              false,
		"response_body_included":             false,
		"headers_included":                   false,
		"authorization_header_included":      false,
		"provider_url_included":              false,
		"idempotency_key_included":           false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"retry_backoff_boundary_redacted":    true,
		"retry_backoff_sequence":             []string{"classify_retryable_response", "verify_idempotency_ledger", "record_retry_decision", "schedule_backoff_retry"},
		"retry_backoff_suppressed_fields":    []string{"retry_after_value", "rate_limit_remaining", "provider_error_code", "response_headers", "response_body", "provider_url", "authorization_header", "token", "idempotency_key", "repository_ref", "branch_name", "file_content"},
		"blocked_reasons": []string{
			"provider_retry_backoff_not_armed",
			"provider_response_diagnostics_not_recorded",
			"provider_idempotency_ledger_not_claimed",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptInvocationReadyReason(ready bool, blockedReason string) string {
	if ready {
		return "ready"
	}
	return blockedReason
}

const (
	providerReviewAttemptAdapterResponsePlanMode = "redacted_attempt_adapter_response_plan"
)

func providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, responsePlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	providerCallBoundaryPlan := providerReviewAttemptAdapterProviderCallBoundaryPlan(operation, claimPlan, responsePlan)
	return map[string]any{
		"mode":                               "redacted_attempt_adapter_transaction_plan",
		"transaction_state":                  "blocked",
		"transaction_ready":                  false,
		"transaction_ready_reason":           "provider_review_transaction_not_armed",
		"transaction_metadata_ready":         providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey) && providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey),
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"operation_order":                    intFromAny(operation["operation_order"], 0),
		"transaction_sequence":               []string{"verify_attempt_claim", "verify_idempotency_claim", "record_provider_call_boundary", "classify_provider_response", "update_attempt_status", "update_dependency_status"},
		"claim_status_from":                  "planned",
		"claim_status_to":                    "running",
		"success_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "success_attempt_status")),
		"retry_attempt_status":               safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "retry_attempt_status")),
		"failure_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "failure_attempt_status")),
		"dependency_unlocks_operation":       safeProviderReviewAttemptOperationName(stringFromMap(responsePlan, "dependency_unlocks_operation")),
		"dependency_update_status":           safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(responsePlan, "dependency_update_status")),
		"requires_database_transaction":      true,
		"requires_attempt_status_planned":    true,
		"requires_attempt_status_running":    true,
		"requires_optimistic_lock":           true,
		"requires_idempotency_ledger":        true,
		"requires_provider_call_boundary":    true,
		"requires_response_diagnostics":      true,
		"requires_dependency_update":         boolOnlyFromAny(responsePlan["requires_dependency_update"]),
		"requires_mutation_arming":           true,
		"provider_call_boundary_plan":        providerCallBoundaryPlan,
		"transaction_opened":                 false,
		"attempt_claim_verified":             false,
		"idempotency_claim_verified":         false,
		"provider_call_boundary_recorded":    false,
		"provider_response_classified":       false,
		"attempt_status_updated":             false,
		"response_recorded":                  false,
		"dependency_update_recorded":         false,
		"provider_request_id_recorded":       false,
		"provider_response_body_recorded":    false,
		"provider_response_headers_recorded": false,
		"adapter_implemented":                false,
		"mutation_armed":                     false,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"request_body_included":              false,
		"response_body_included":             false,
		"headers_included":                   false,
		"authorization_header_included":      false,
		"provider_url_included":              false,
		"idempotency_key_included":           false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"transaction_boundary_redacted":      true,
		"blocked_reasons": []string{
			"provider_review_attempt_claim_not_recorded",
			"provider_review_transaction_not_armed",
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterProviderCallBoundaryPlan(operation, claimPlan, responsePlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	metadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey) &&
		providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey)
	return map[string]any{
		"mode":                                  "redacted_attempt_adapter_provider_call_boundary_plan",
		"provider_call_boundary_state":          "blocked",
		"provider_call_boundary_ready":          false,
		"provider_call_boundary_ready_reason":   "provider_review_provider_call_boundary_not_armed",
		"provider_call_boundary_metadata_ready": metadataReady,
		"operation_name":                        operationName,
		"endpoint_key":                          endpointKey,
		"operation_order":                       intFromAny(operation["operation_order"], 0),
		"idempotency_key_kind":                  "operation_scope_hash",
		"requires_database_transaction":         true,
		"requires_attempt_claim":                true,
		"requires_idempotency_claim":            true,
		"requires_response_diagnostics":         true,
		"requires_mutation_arming":              true,
		"transaction_opened":                    false,
		"attempt_claim_verified":                false,
		"idempotency_claim_verified":            false,
		"provider_call_boundary_opened":         false,
		"provider_call_boundary_recorded":       false,
		"provider_call_started_recorded":        false,
		"provider_call_finished_recorded":       false,
		"provider_request_sent":                 false,
		"provider_response_received":            false,
		"provider_request_id_recorded":          false,
		"provider_response_status_recorded":     false,
		"provider_response_body_recorded":       false,
		"provider_response_headers_recorded":    false,
		"provider_call_boundary_redacted":       true,
		"external_call_made":                    false,
		"provider_api_call_made":                false,
		"provider_api_mutation":                 "disabled",
		"request_body_included":                 false,
		"response_body_included":                false,
		"headers_included":                      false,
		"authorization_header_included":         false,
		"provider_url_included":                 false,
		"idempotency_key_included":              false,
		"provider_request_id_included":          false,
		"contains_token":                        false,
		"contains_provider_url":                 false,
		"contains_repository_ref":               false,
		"contains_branch_name":                  false,
		"contains_file_content":                 false,
		"boundary_sequence": []string{
			"verify_attempt_claim",
			"verify_idempotency_claim",
			"open_database_transaction",
			"record_provider_call_started",
			"stage_provider_request_send",
			"record_provider_call_finished",
			"commit_database_transaction",
		},
		"blocked_reasons": []string{
			"provider_review_provider_call_boundary_not_armed",
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

const providerReviewAttemptAdapterRequestMaterializationPlanMode = "redacted_attempt_adapter_request_materialization_plan"

func providerReviewAttemptAdapterRequestMaterializationPlan(operation, requestSummary map[string]any, providerType string) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	providerType = safeProviderReviewProviderType(providerType)
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	endpointTemplateKey := providerReviewEndpointPathTemplateKeyForOperation(providerType, operationName)
	payloadBuilder := safeProviderReviewPayloadBuilderName(stringFromMap(requestSummary, "payload_builder"))
	if providerType == "" || operationName == "" || endpointKey == "" || endpointTemplateKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) || !providerReviewAttemptPayloadBuilderMatchesOperation(operationName, payloadBuilder) {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                                      providerReviewAttemptAdapterRequestMaterializationPlanMode,
		"request_materialization_state":             "blocked",
		"request_materialization_ready":             false,
		"request_materialization_ready_reason":      "provider_request_materialization_not_armed",
		"provider_type":                             providerType,
		"operation_name":                            operationName,
		"endpoint_key":                              endpointKey,
		"operation_order":                           intFromAny(operation["operation_order"], 0),
		"method":                                    providerReviewMethodForOperation(operationName),
		"endpoint_path_template_key":                endpointTemplateKey,
		"payload_shape":                             providerReviewPayloadShapeForOperation(operationName),
		"payload_builder":                           payloadBuilder,
		"requires_request_builder":                  true,
		"requires_provider_repository_context":      true,
		"requires_redacted_payload_summary":         true,
		"requires_starter_file_manifest":            operationName == "commit_starter_files",
		"requires_mutation_arming":                  true,
		"request_builder_implemented":               false,
		"provider_repository_context_resolved":      false,
		"request_path_materialized":                 false,
		"request_url_materialized":                  false,
		"request_body_materialized":                 false,
		"payload_materialized":                      false,
		"headers_materialized":                      false,
		"starter_file_manifest_materialized":        false,
		"authorization_header_materialized":         false,
		"external_call_made":                        false,
		"provider_api_call_made":                    false,
		"provider_api_mutation":                     "disabled",
		"request_body_included":                     false,
		"headers_included":                          false,
		"provider_url_included":                     false,
		"repository_ref_included":                   false,
		"branch_name_included":                      false,
		"file_content_included":                     false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"request_materialization_boundary_redacted": true,
		"blocked_reasons": []string{
			"provider_request_not_materialized",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterCredentialBindingPlan(providerType, operationName string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	authScheme := providerReviewAuthSchemeForProvider(providerType)
	if providerType == "" || operationName == "" || authScheme == "" {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                              "redacted_attempt_adapter_credential_binding_plan",
		"credential_binding_state":          "blocked",
		"credential_binding_ready":          false,
		"credential_binding_ready_reason":   "provider_credential_runtime_binding_not_armed",
		"provider_type":                     providerType,
		"operation_name":                    operationName,
		"endpoint_key":                      providerReviewEndpointKey(providerType, providerReviewEndpointOperationForAttempt(operationName)),
		"auth_scheme":                       authScheme,
		"credential_source_kind":            "provider_account_token_env",
		"requires_provider_account":         true,
		"requires_allowed_token_env":        true,
		"requires_runtime_token_present":    true,
		"requires_mutation_arming":          true,
		"credential_bound":                  false,
		"authorization_header_materialized": false,
		"token_env_name_included":           false,
		"token_value_included":              false,
		"token_stored":                      false,
		"headers_included":                  false,
		"external_call_made":                false,
		"provider_api_call_made":            false,
		"provider_api_mutation":             "disabled",
		"contains_token":                    false,
		"contains_provider_url":             false,
		"contains_repository_ref":           false,
		"contains_branch_name":              false,
		"contains_file_content":             false,
		"blocked_reasons": []string{
			"provider_credential_runtime_binding_not_armed",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
		"credential_boundary_redacted": true,
	}
}

func providerReviewAttemptAdapterResponsePlan(operation, requestSummary, responseDiagnostics map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	responseHandler := safeProviderReviewResponseHandlerName(stringFromMap(requestSummary, "response_handler"))
	if operationName == "" || endpointKey == "" || providerType == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) || !providerReviewAttemptResponseHandlerMatchesOperation(operationName, responseHandler) {
		return map[string]any{}
	}
	unlockOperation := providerReviewAttemptDependencyUnlockOperation(operationName)
	plan := map[string]any{
		"mode":                            providerReviewAttemptAdapterResponsePlanMode,
		"response_recording_state":        "blocked",
		"response_recording_ready":        false,
		"response_recording_ready_reason": "provider_api_adapter_response_not_recorded",
		"operation_name":                  operationName,
		"endpoint_key":                    endpointKey,
		"operation_order":                 intFromAny(operation["operation_order"], 0),
		"response_handler":                responseHandler,
		"response_status":                 safeProviderReviewAttemptResponseStatus(stringFromMap(responseDiagnostics, "status")),
		"expected_success_classes":        providerReviewExpectedSuccessClassesForOperation(operationName),
		"retryable_status_classes":        providerReviewExpectedRetryClassesForOperation(operationName),
		"terminal_failure_status_classes": providerReviewTerminalFailureClassesForOperation(operationName),
		"success_attempt_status":          "completed",
		"retry_attempt_status":            "planned",
		"failure_attempt_status":          "failed",
		"dependency_unlocks_operation":    unlockOperation,
		"dependency_update_status":        providerReviewAttemptDependencyUnlockStatus(unlockOperation),
		"requires_response_handler":       true,
		"requires_response_diagnostics":   true,
		"requires_idempotency_ledger":     true,
		"requires_dependency_update":      unlockOperation != "",
		"adapter_implemented":             false,
		"mutation_armed":                  false,
		"response_recorded":               false,
		"dependency_update_recorded":      false,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"response_body_included":          false,
		"headers_included":                false,
		"provider_request_id_included":    false,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_branch_name":            false,
		"contains_file_content":           false,
		"blocked_reasons": []string{
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
		"response_boundary_redacted": true,
	}
	plan["result_recording_plan"] = providerReviewAttemptAdapterResultRecordingPlan(operation, plan)
	return plan
}

func providerReviewAttemptAdapterResultRecordingPlan(operation, responsePlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" || !providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey) {
		return map[string]any{}
	}
	dependencyUnlockOperation := safeProviderReviewAttemptOperationName(stringFromMap(responsePlan, "dependency_unlocks_operation"))
	dependencyUpdateStatus := ""
	if dependencyUnlockOperation != "" {
		dependencyUpdateStatus = safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(responsePlan, "dependency_update_status"))
	}
	return map[string]any{
		"mode":                               "redacted_attempt_adapter_result_recording_plan",
		"result_recording_state":             "blocked",
		"result_recording_ready":             false,
		"result_recording_ready_reason":      "provider_review_result_recording_not_armed",
		"result_recording_metadata_ready":    true,
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"operation_order":                    intFromAny(operation["operation_order"], 0),
		"response_status":                    safeProviderReviewAttemptResponseStatus(stringFromMap(responsePlan, "response_status")),
		"success_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "success_attempt_status")),
		"retry_attempt_status":               safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "retry_attempt_status")),
		"failure_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "failure_attempt_status")),
		"dependency_unlocks_operation":       dependencyUnlockOperation,
		"dependency_update_status":           dependencyUpdateStatus,
		"requires_response_handler":          true,
		"requires_response_diagnostics":      true,
		"requires_transaction_boundary":      true,
		"requires_dependency_update":         boolOnlyFromAny(responsePlan["requires_dependency_update"]),
		"requires_mutation_arming":           true,
		"result_recorded":                    false,
		"response_classified":                false,
		"attempt_status_mapped":              false,
		"attempt_result_persisted":           false,
		"dependency_update_staged":           false,
		"provider_request_id_recorded":       false,
		"provider_response_status_recorded":  false,
		"provider_response_body_recorded":    false,
		"provider_response_headers_recorded": false,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"response_body_included":             false,
		"headers_included":                   false,
		"provider_request_id_included":       false,
		"provider_response_status_included":  false,
		"provider_url_included":              false,
		"idempotency_key_included":           false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"result_recording_boundary_redacted": true,
		"result_recording_sequence":          []string{"classify_provider_response", "map_attempt_status", "stage_dependency_update", "record_redacted_result", "persist_attempt_result"},
		"result_recording_diagnostic_fields": []string{"status_class", "retry_class", "dependency_update_required", "provider_request_id_present"},
		"result_recording_persisted_fields":  []string{"attempt_status", "dependency_status", "response_status_class", "retry_class"},
		"result_recording_suppressed_fields": []string{"provider_request_id", "response_body", "response_headers", "provider_url", "authorization_header", "token", "repository_ref", "branch_name", "file_content"},
		"blocked_reasons": []string{
			"provider_review_result_recording_not_armed",
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterTransportPlan(providerType, operationName string) map[string]any {
	providerType = providerReviewProviderFromEndpointKey(providerReviewEndpointKey(providerType, providerReviewEndpointOperationForAttempt(operationName)))
	operationName = safeProviderReviewAttemptOperationName(operationName)
	if providerType == "" || operationName == "" {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                          "redacted_attempt_adapter_transport_plan",
		"transport_ready":               true,
		"transport_ready_reason":        "ready",
		"provider_type":                 providerType,
		"operation_name":                operationName,
		"method":                        providerReviewMethodForOperation(operationName),
		"endpoint_key":                  providerReviewEndpointKey(providerType, providerReviewEndpointOperationForAttempt(operationName)),
		"payload_shape":                 providerReviewPayloadShapeForOperation(operationName),
		"auth_scheme":                   providerReviewAuthSchemeForProvider(providerType),
		"accept_header":                 providerReviewAcceptHeaderForProvider(providerType),
		"content_type":                  "application/json",
		"timeout_seconds":               15,
		"expected_success_classes":      providerReviewExpectedSuccessClassesForOperation(operationName),
		"retryable_status_classes":      []string{"5xx"},
		"diagnostic_fields":             []string{"status_code_class", "provider_request_id_present", "rate_limit_remaining_present", "retry_after_present", "provider_error_code_present"},
		"request_body_included":         false,
		"response_body_included":        false,
		"headers_included":              false,
		"authorization_header_included": false,
		"auth_header_redacted":          true,
		"provider_url_included":         false,
		"external_call_made":            false,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
	}
}

func providerReviewAttemptExecutionClaimPlan(operation map[string]any, idempotencyReady, responseDiagnosticsReady bool) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	status := safeProviderReviewAttemptStatus(stringFromMap(operation, "status"))
	rawDependencyStatus := cleanOptionalText(stringFromMap(operation, "dependency_status"))
	dependencyStatus := safeProviderReviewAttemptClaimDependencyStatus(rawDependencyStatus)
	dependencyReady := providerReviewAttemptClaimDependencyReady(dependencyStatus)
	claimRecorded := providerReviewAttemptClaimRecorded(operation)
	claimMetadataReady := status == "planned" && dependencyReady && idempotencyReady && responseDiagnosticsReady
	claimState := "blocked"
	if claimRecorded {
		claimState = "claimed"
	}
	blockedReasons := []string{
		"provider_review_adapter_not_implemented",
		"provider_review_mutation_not_armed",
	}
	if status != "planned" && !claimRecorded {
		blockedReasons = append([]string{"provider_review_attempt_status_not_planned"}, blockedReasons...)
	}
	if !dependencyReady {
		blockedReasons = append([]string{"provider_review_dependency_not_ready"}, blockedReasons...)
	}
	if !idempotencyReady {
		blockedReasons = append([]string{"provider_review_idempotency_metadata_missing"}, blockedReasons...)
	}
	if !responseDiagnosticsReady {
		blockedReasons = append([]string{"provider_review_response_diagnostics_missing"}, blockedReasons...)
	}
	return map[string]any{
		"mode":                            "redacted_attempt_execution_claim_plan",
		"claim_state":                     claimState,
		"claim_ready":                     false,
		"claim_metadata_ready":            claimMetadataReady,
		"operation_name":                  safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")),
		"endpoint_key":                    safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key")),
		"operation_order":                 intFromAny(operation["operation_order"], 0),
		"attempt_status":                  status,
		"dependency_status":               dependencyStatus,
		"dependency_ready":                dependencyReady,
		"claim_status_from":               "planned",
		"claim_status_to":                 "running",
		"replay_check":                    safeProviderReviewReplayCheck(stringFromMap(operation, "replay_check")),
		"conflict_policy":                 safeProviderReviewConflictPolicy(stringFromMap(operation, "conflict_policy")),
		"retry_policy":                    safeProviderReviewRetryPolicy(stringFromMap(operation, "retry_policy")),
		"requires_attempt_status_planned": true,
		"requires_dependency_ready":       true,
		"requires_idempotency_ledger":     true,
		"requires_response_diagnostics":   true,
		"requires_optimistic_lock":        true,
		"requires_provider_adapter":       true,
		"requires_mutation_arming":        true,
		"idempotency_metadata_ready":      idempotencyReady,
		"response_diagnostics_ready":      responseDiagnosticsReady,
		"claim_recorded":                  claimRecorded,
		"claimed_at_recorded":             claimRecorded,
		"idempotency_claim_recorded":      false,
		"adapter_implemented":             false,
		"mutation_armed":                  false,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"idempotency_key_included":        false,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_branch_name":            false,
		"contains_file_content":           false,
		"blocked_reasons":                 blockedReasons,
	}
}

func providerReviewAttemptClaimRecorded(operation map[string]any) bool {
	value, ok := operation["claimed_at"]
	if !ok || value == nil {
		return false
	}
	claimedAt := cleanOptionalText(fmt.Sprint(value))
	return claimedAt != "" && claimedAt != "<nil>"
}

func safeProviderReviewAttemptClaimDependencyStatus(value string) string {
	switch cleanOptionalText(value) {
	case "independent", "waiting_for_dependency", "dependency_satisfied", "dependency_failed":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func providerReviewAttemptClaimDependencyReady(status string) bool {
	switch safeProviderReviewAttemptClaimDependencyStatus(status) {
	case "independent", "dependency_satisfied":
		return true
	default:
		return false
	}
}

func providerReviewProviderFromEndpointKey(endpointKey string) string {
	switch safeProviderReviewEndpointKey(endpointKey) {
	case "github.create_branch_ref", "github.commit_files", "github.open_review":
		return "github"
	case "gitea.create_branch_ref", "gitea.commit_files", "gitea.open_review":
		return "gitea"
	default:
		return ""
	}
}

func providerReviewAdapterKindForProvider(provider string) string {
	switch cleanOptionalText(provider) {
	case "github":
		return "github_provider_review_adapter"
	case "gitea":
		return "gitea_provider_review_adapter"
	default:
		return ""
	}
}

func providerReviewMethodForOperation(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "open_review_request":
		return "POST"
	case "commit_starter_files":
		return "PUT"
	default:
		return ""
	}
}

func providerReviewPayloadShapeForOperation(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "ref_from_target_branch"
	case "commit_starter_files":
		return "content_redacted_file_batch"
	case "open_review_request":
		return "review_request"
	default:
		return ""
	}
}

func providerReviewEndpointOperationForAttempt(operationName string) string {
	// Attempt operation names describe ledger steps; endpoint operation names describe provider API routes.
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "create_branch_ref"
	case "commit_starter_files":
		return "commit_files"
	case "open_review_request":
		return "open_review"
	default:
		return ""
	}
}

func providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey string) bool {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	endpointOperation := providerReviewEndpointOperationForAttempt(operationName)
	return providerType != "" &&
		operationName != "" &&
		endpointKey != "" &&
		endpointOperation != "" &&
		endpointKey == providerReviewEndpointKey(providerType, endpointOperation)
}

func providerReviewAttemptPlanMatchesOperation(plan map[string]any, mode, operationName, endpointKey string) bool {
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	return operationName != "" &&
		endpointKey != "" &&
		stringFromMap(plan, "mode") == mode &&
		safeProviderReviewAttemptOperationName(stringFromMap(plan, "operation_name")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(plan, "endpoint_key")) == endpointKey
}

func providerReviewAttemptClaimPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["claim_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_execution_claim_plan", operationName, endpointKey)
}

func providerReviewAttemptClaimPlanIdempotencyReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["idempotency_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_execution_claim_plan", operationName, endpointKey)
}

func providerReviewAttemptResponsePlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	if !providerReviewAttemptPlanMatchesOperation(plan, providerReviewAttemptAdapterResponsePlanMode, operationName, endpointKey) {
		return false
	}
	expectedUnlockOperation := providerReviewAttemptDependencyUnlockOperation(operationName)
	expectedDependencyStatus := providerReviewAttemptDependencyUnlockStatus(expectedUnlockOperation)
	dependencyUnlockReady := cleanOptionalText(stringFromMap(plan, "dependency_unlocks_operation")) == expectedUnlockOperation
	if expectedUnlockOperation != "" {
		dependencyUnlockReady = safeProviderReviewAttemptOperationName(stringFromMap(plan, "dependency_unlocks_operation")) == expectedUnlockOperation
	}
	dependencyUpdateStatusReady := safeProviderReviewAttemptResponseDependencyStatus(stringFromMap(plan, "dependency_update_status")) == expectedDependencyStatus
	requiresDependencyUpdate, hasRequiresDependencyUpdate := plan["requires_dependency_update"]
	return safeProviderReviewAttemptStatus(stringFromMap(plan, "success_attempt_status")) == "completed" &&
		safeProviderReviewAttemptStatus(stringFromMap(plan, "retry_attempt_status")) == "planned" &&
		safeProviderReviewAttemptStatus(stringFromMap(plan, "failure_attempt_status")) == "failed" &&
		dependencyUnlockReady &&
		dependencyUpdateStatusReady &&
		hasRequiresDependencyUpdate &&
		boolOnlyFromAny(requiresDependencyUpdate) == (expectedUnlockOperation != "")
}

func safeProviderReviewAttemptResponseDependencyStatus(value string) string {
	switch cleanOptionalText(value) {
	case "", "dependency_satisfied":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func providerReviewAttemptRequestPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["request_materialization_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, providerReviewAttemptAdapterRequestMaterializationPlanMode, operationName, endpointKey)
}

func providerReviewAttemptBranchPolicyPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["branch_policy_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_branch_policy_plan", operationName, endpointKey)
}

func providerReviewAttemptCredentialPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["credential_binding_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_credential_binding_plan", operationName, endpointKey)
}

func providerReviewAttemptRuntimePlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["runtime_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_runtime_plan", operationName, endpointKey)
}

func providerReviewAttemptTransportPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["transport_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_transport_plan", operationName, endpointKey)
}

func providerReviewAttemptProviderSendPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["provider_send_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_provider_send_plan", operationName, endpointKey)
}

func providerReviewAttemptResponseRecordingReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["response_recording_ready"]) &&
		providerReviewAttemptResponsePlanReadyForOperation(plan, operationName, endpointKey)
}

func providerReviewAttemptTransactionPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["transaction_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_transaction_plan", operationName, endpointKey)
}

func providerReviewAttemptExecutionLockPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["execution_lock_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_execution_lock_plan", operationName, endpointKey)
}

func providerReviewAttemptActivationPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["adapter_activation_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_activation_plan", operationName, endpointKey)
}

func providerReviewAttemptPayloadBuilderMatchesOperation(operationName, payloadBuilder string) bool {
	operationName = safeProviderReviewAttemptOperationName(operationName)
	payloadBuilder = safeProviderReviewPayloadBuilderName(payloadBuilder)
	expectedBuilder := providerReviewExpectedPayloadBuilderName(operationName)
	return operationName != "" && expectedBuilder != "" && payloadBuilder == expectedBuilder
}

func providerReviewAttemptResponseHandlerMatchesOperation(operationName, responseHandler string) bool {
	operationName = safeProviderReviewAttemptOperationName(operationName)
	responseHandler = safeProviderReviewResponseHandlerName(responseHandler)
	expectedHandler := providerReviewExpectedResponseHandlerName(operationName)
	return operationName != "" && expectedHandler != "" && responseHandler == expectedHandler
}

func providerReviewExpectedPayloadBuilderName(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "build_redacted_branch_ref_request"
	case "commit_starter_files":
		return "build_redacted_file_batch_request"
	case "open_review_request":
		return "build_redacted_review_request"
	default:
		return ""
	}
}

func providerReviewExpectedResponseHandlerName(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "handle_branch_ref_response"
	case "commit_starter_files":
		return "handle_commit_files_response"
	case "open_review_request":
		return "handle_review_request_response"
	default:
		return ""
	}
}

func providerReviewEndpointPathTemplateKeyForOperation(providerType, operationName string) string {
	providerType = safeProviderReviewProviderType(providerType)
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		switch providerType {
		case "github":
			return "github_git_refs_path_template"
		case "gitea":
			return "gitea_git_refs_path_template"
		}
	case "commit_starter_files":
		switch providerType {
		case "github":
			return "github_repository_contents_path_template"
		case "gitea":
			return "gitea_repository_contents_path_template"
		}
	case "open_review_request":
		switch providerType {
		case "github":
			return "github_pull_request_path_template"
		case "gitea":
			return "gitea_merge_request_path_template"
		}
	}
	return ""
}

func providerReviewAuthSchemeForProvider(provider string) string {
	switch strings.ToLower(cleanOptionalText(provider)) {
	case "github":
		return "bearer_token"
	case "gitea":
		return "token"
	default:
		return ""
	}
}

func providerReviewAcceptHeaderForProvider(provider string) string {
	switch strings.ToLower(cleanOptionalText(provider)) {
	case "github":
		return "application/vnd.github+json"
	case "gitea":
		return "application/json"
	default:
		return ""
	}
}

func providerReviewExpectedSuccessClassesForOperation(operationName string) []string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return []string{"2xx"}
	default:
		return []string{}
	}
}

func providerReviewExpectedRetryClassesForOperation(operationName string) []string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return []string{"5xx"}
	default:
		return []string{}
	}
}

func providerReviewTerminalFailureClassesForOperation(operationName string) []string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return []string{"4xx"}
	default:
		return []string{}
	}
}

func providerReviewAttemptDependencyUnlockOperation(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "commit_starter_files"
	case "commit_starter_files":
		return "open_review_request"
	default:
		return ""
	}
}

func providerReviewAttemptDependencyUnlockStatus(operationName string) string {
	if safeProviderReviewAttemptOperationName(operationName) == "" {
		return ""
	}
	return "dependency_satisfied"
}

func providerReviewAttemptExecutionCandidateGates(candidateReady, idempotencyReady, responseDiagnosticsReady bool) []map[string]any {
	return []map[string]any{
		{
			"gate":     "attempt_operation_ready",
			"category": "data_integrity",
			"status":   readinessStatus(candidateReady),
		},
		{
			"gate":     "idempotency_metadata",
			"category": "data_integrity",
			"status":   readinessStatus(idempotencyReady),
		},
		{
			"gate":     "response_diagnostics_metadata",
			"category": "data_integrity",
			"status":   readinessStatus(responseDiagnosticsReady),
		},
		{
			"gate":     "provider_api_adapter",
			"category": "execution_blocker",
			"status":   "blocked",
		},
		{
			"gate":     "provider_review_mutation_armed",
			"category": "execution_blocker",
			"status":   "blocked",
		},
	}
}

func safeProviderReviewAttemptOperationName(value string) string {
	switch cleanOptionalText(value) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewAttemptDependencyChainStatus(value string) string {
	switch cleanOptionalText(value) {
	case "not_recorded", "ready", "waiting_for_dependency", "blocked", "completed":
		return cleanOptionalText(value)
	default:
		return "not_recorded"
	}
}

func safeProviderReviewAttemptOrchestrationStatus(value string) string {
	switch cleanOptionalText(value) {
	case "not_recorded", "planned", "running", "completed", "blocked":
		return cleanOptionalText(value)
	default:
		return "not_recorded"
	}
}

func safeProviderReviewAttemptDependencyStatus(value string) string {
	switch cleanOptionalText(value) {
	case "independent", "waiting_for_dependency", "dependency_satisfied", "dependency_failed":
		return cleanOptionalText(value)
	default:
		return "independent"
	}
}

func safeProviderReviewAttemptDependencyName(value string) string {
	switch cleanOptionalText(value) {
	case "", "create_branch_ref", "commit_starter_files":
		return cleanOptionalText(value)
	default:
		return ""
	}
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
				_ = writeSSE(w, "stream_error", map[string]any{"message": operationLogStreamClientErrorMessage})
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

const operationLogStreamClientErrorMessage = "operation log stream failed"

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
	toolCallEvidence := agentToolCallAuditEvidence(toolCalls)
	task["tool_call_audit_evidence"] = toolCallEvidence
	task["code_modification_evidence"] = agentCodeModificationEvidence(toolCallEvidence)
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

func (s *Server) recordAgentToolAuditSnapshot(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	task, err := agentTaskForToolAuditSnapshot(r.Context(), s.store.DB, taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "agent task has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordAgentToolAuditSnapshot(r.Context(), s.store, AgentToolAuditSnapshotOptions{
		AgentTaskID: taskID,
		DryRun:      req.DryRun,
		Task:        task,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("agent tool-call audit snapshot failed", "agent_task_id", taskID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record agent tool-call audit snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordAgentToolArmingSnapshot(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	task, err := agentTaskForToolAuditSnapshot(r.Context(), s.store.DB, taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "agent task has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordAgentToolArmingSnapshot(r.Context(), s.store, AgentToolArmingSnapshotOptions{
		AgentTaskID: taskID,
		DryRun:      req.DryRun,
		Task:        task,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("agent tool arming snapshot failed", "agent_task_id", taskID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record agent tool arming snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordAgentCodeAuditSnapshot(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	task, err := agentTaskForToolAuditSnapshot(r.Context(), s.store.DB, taskID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "agent task has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordAgentCodeAuditSnapshot(r.Context(), s.store, AgentCodeAuditSnapshotOptions{
		AgentTaskID: taskID,
		DryRun:      req.DryRun,
		Task:        task,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("agent code audit snapshot failed", "agent_task_id", taskID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record agent code audit snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
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
	deploymentExecutionSummary := formatCountMap(countNestedStringField(deploymentTargets, "deployment_execution_readiness", "status"))
	if deploymentExecutionSummary == "" {
		deploymentExecutionSummary = "none"
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
	fmt.Fprintf(&b, "- Deployment execution readiness: %s\n", deploymentExecutionSummary)
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
	fmt.Fprintf(&b, "- Deployment execution readiness is dry-run only; Helm/k8s execution remains disabled.\n")
	if msg := strings.TrimSpace(fmt.Sprint(rollbackGuardrail["message"])); msg != "" && msg != "<nil>" {
		fmt.Fprintf(&b, "- %s\n", msg)
	}
	patchGuardrail := agentPatchWorkflowGuardrail()
	if msg := strings.TrimSpace(fmt.Sprint(patchGuardrail["message"])); msg != "" && msg != "<nil>" {
		fmt.Fprintf(&b, "- %s\n", msg)
	}
	codexExecutionPlan := agentCodexExecutionPlan(nil)
	if msg := strings.TrimSpace(fmt.Sprint(codexExecutionPlan["message"])); msg != "" && msg != "<nil>" {
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

// rollbackExecutionPlan mirrors rollbackPointReadinessSQL's redacted JSON contract for unit tests and fallback callers.
func rollbackExecutionPlan(readiness, mode string) map[string]any {
	prerequisiteState := "metadata_blocked"
	if strings.EqualFold(strings.TrimSpace(readiness), "previewable") {
		prerequisiteState = "metadata_available"
	}
	mode = strings.TrimSpace(mode)
	if mode == "" || mode == "<nil>" {
		mode = "read_only_preview"
	}
	return map[string]any{
		"mode":                           "redacted_rollback_execution_plan",
		"plan_state":                     "blocked",
		"prerequisite_state":             prerequisiteState,
		"plan_ready":                     false,
		"plan_ready_reason":              "rollback_execution_backend_disabled",
		"execution_enabled":              false,
		"execution_mode":                 mode,
		"requires_approval":              true,
		"approval_action":                "deployment.rollback",
		"requires_environment_review":    true,
		"requires_kubeconfig_binding":    true,
		"requires_revision_verification": true,
		"requires_manifest_diff":         true,
		"requires_dry_run_preflight":     true,
		"requires_operator_confirmation": true,
		"rollback_request_materialized":  false,
		"revision_verified":              false,
		"manifest_diff_rendered":         false,
		"dry_run_performed":              false,
		"kubernetes_client_constructed":  false,
		"helm_rollback_invoked":          false,
		"kubectl_rollout_invoked":        false,
		"argocd_rollback_invoked":        false,
		"rollback_started":               false,
		"external_call_made":             false,
		"kubernetes_api_call_made":       false,
		"helm_command_invoked":           false,
		"rollback_mutation":              "disabled",
		"kubeconfig_included":            false,
		"secret_included":                false,
		"manifest_body_included":         false,
		"helm_values_included":           false,
		"cluster_credential_included":    false,
		"revision_value_included":        false,
		"contains_token":                 false,
		"contains_kubeconfig":            false,
		"contains_secret":                false,
		"contains_manifest_body":         false,
		"rollback_boundary_redacted":     true,
		"blocked_reasons":                []string{"rollback_execution_backend_disabled", "rollback_mutation_not_armed"},
		"required_controls":              []string{"operation_approval", "environment_review", "kubeconfig_binding", "revision_verification", "manifest_diff", "server_side_dry_run", "operator_confirmation"},
		"disabled_backends":              []string{"helm_rollback", "kubectl_rollout_undo", "argocd_rollback", "rollback_execute"},
		"suppressed_fields":              []string{"kubeconfig", "cluster_token", "authorization_header", "secret_manifest", "rendered_manifest", "helm_values", "image_pull_secret", "environment_secret", "revision_value"},
		"execution_sequence":             []string{"request_approval", "bind_environment", "bind_kubeconfig", "verify_revision", "render_manifest_diff", "run_server_side_dry_run", "record_rollback_audit", "start_rollback"},
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

func countNestedStringField(rows []map[string]any, outer, inner string) map[string]int {
	counts := make(map[string]int)
	for _, row := range rows {
		nested := mapFromAny(row[outer])
		key := strings.TrimSpace(fmt.Sprint(nested[inner]))
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
			"tool_name": "worker.dispatch.plan",
			"input": map[string]any{
				"agent_task_id":    taskID,
				"agent_plan_id":    planID,
				"operation_run_id": opID,
				"mode":             "redacted_worker_dispatch_plan",
			},
			"output": map[string]any{
				"message":              "worker-backed agent execution dispatch is planned for audit only; no worker is claimed and no tool is invoked",
				"worker_dispatch_plan": agentWorkerDispatchPlan(runtime),
			},
		},
		{
			"tool_name": "codex.execution.plan",
			"input": map[string]any{
				"agent_task_id":    taskID,
				"agent_plan_id":    planID,
				"operation_run_id": opID,
				"mode":             "redacted_execution_plan",
			},
			"output": map[string]any{
				"message":              "Codex CLI execution plan is recorded for audit only; process spawning and repository mutation remain disabled",
				"codex_execution_plan": agentCodexExecutionPlan(runtime),
			},
		},
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
		"code_modification_plan": agentCodeModificationPlan(),
		"execution_readiness":    agentExecutionReadinessGates(),
		"next_step":              "Keep execution audit-only until Codex CLI runs, patch application, and PR creation are individually approval-gated.",
		"message":                "Agent patch workflow is audit-only: Codex CLI, repository mutation, and pull request creation are disabled.",
	}
}

func agentCodeModificationPlan(auditEvidenceRows ...map[string]any) map[string]any {
	auditEvidence := map[string]any{}
	if len(auditEvidenceRows) > 0 {
		auditEvidence = auditEvidenceRows[0]
	}
	codeEvidence := agentCodeModificationEvidence(auditEvidence)
	executionArmingPlan := agentCodeModificationExecutionArmingPlan(codeEvidence)
	sourceCheckoutBranchReviewPlan := agentCodeModificationSourceCheckoutBranchReviewPlan(codeEvidence, executionArmingPlan)
	return map[string]any{
		"mode":                                "redacted_agent_code_modification_plan",
		"plan_state":                          "blocked",
		"plan_ready":                          false,
		"plan_ready_reason":                   "agent_code_modification_backend_disabled",
		"execution_enabled":                   false,
		"mutation_enabled":                    false,
		"external_call_made":                  false,
		"repository_mutation_allowed":         false,
		"source_checkout_performed":           false,
		"workspace_bound":                     false,
		"branch_created":                      false,
		"patch_content_materialized":          false,
		"diff_materialized":                   false,
		"file_patch_applied":                  false,
		"tests_executed":                      false,
		"git_commit_created":                  false,
		"git_push_performed":                  false,
		"pull_request_created":                false,
		"commit_push_agent_invoked":           false,
		"requires_source_remote_review":       true,
		"requires_branch_policy_review":       true,
		"requires_patch_review":               true,
		"requires_test_plan_review":           true,
		"requires_commit_push_agent":          true,
		"contains_token":                      false,
		"contains_remote_url":                 false,
		"contains_branch_name":                false,
		"contains_workspace_path":             false,
		"contains_patch_content":              false,
		"contains_diff_content":               false,
		"contains_file_content":               false,
		"execution_boundary_redacted":         true,
		"code_modification_evidence":          codeEvidence,
		"execution_arming_plan":               executionArmingPlan,
		"source_checkout_branch_review_plan":  sourceCheckoutBranchReviewPlan,
		"source_checkout_branch_review_ready": sourceCheckoutBranchReviewPlan["review_ready"],
		"execution_arming_ready":              executionArmingPlan["arming_ready"],
		"audit_result_recorded":               boolOnlyFromAny(codeEvidence["sanitized_result_recorded"]),
		"blocked_reasons": []string{
			"agent_code_modification_backend_disabled",
			"source_checkout_not_armed",
			"branch_creation_not_armed",
			"patch_apply_not_armed",
			"commit_push_agent_not_invoked",
			"provider_pr_workflow_not_wired",
		},
		"required_controls": []string{
			"agent_execute_approval",
			"runtime_verification",
			"source_remote_review",
			"workspace_binding",
			"branch_policy_review",
			"structured_patch_review",
			"human_patch_approval",
			"test_plan_review",
			"commit_push_agent",
			"provider_review_reconciliation",
		},
		"disabled_backends": []string{
			"source_checkout",
			"branch_create",
			"file_patch_apply",
			"test_command_execute",
			"git_commit",
			"git_push",
			"pull_request_create",
			"commit_push_agent",
		},
		"suppressed_fields": []string{
			"runtime_config",
			"environment_variables",
			"authorization_header",
			"source_remote_url",
			"workspace_path",
			"branch_name",
			"prompt_body",
			"patch_content",
			"diff_content",
			"file_content",
			"test_output",
			"token",
		},
		"execution_sequence": []string{
			"request_agent_execute_approval",
			"verify_runtime_metadata",
			"review_source_remote",
			"bind_workspace",
			"create_review_branch",
			"capture_structured_patch",
			"review_diff",
			"run_test_plan",
			"request_patch_apply_approval",
			"apply_patch",
			"delegate_commit_push_agent",
			"open_provider_review",
		},
		"result_recording_plan": agentCodeModificationResultRecordingPlan(codeEvidence),
		"message":               "Agent code modification is represented as a redacted rehearsal plan only; source checkout, branch creation, patch application, tests, commit, push, and review creation remain disabled.",
	}
}

func agentCodeModificationSourceCheckoutBranchReviewPlan(codeEvidence, executionArmingPlan map[string]any) map[string]any {
	auditState := cleanPreviewString(codeEvidence["evidence_state"])
	hasAudit := boolOnlyFromAny(codeEvidence["has_code_modification_audit"])
	completeAudit := boolOnlyFromAny(executionArmingPlan["arming_ready"])
	workerDispatchRecorded := boolOnlyFromAny(codeEvidence["worker_dispatch_audit_recorded"])
	codexPlanRecorded := boolOnlyFromAny(codeEvidence["codex_execution_plan_recorded"])
	patchPrepareRecorded := boolOnlyFromAny(codeEvidence["patch_prepare_audit_recorded"])
	terminalAuditRecorded := boolOnlyFromAny(codeEvidence["sanitized_result_recorded"])

	reviewState := "blocked"
	reviewReason := "agent_code_source_checkout_branch_review_audit_not_recorded"
	switch {
	case completeAudit:
		reviewState = "ready_for_operator_review"
		reviewReason = "agent_code_source_checkout_branch_review_ready_for_operator_review"
	case auditState == "waiting_for_worker":
		reviewState = "waiting_for_worker"
		reviewReason = "agent_code_source_checkout_branch_review_waiting_for_worker"
	case hasAudit:
		reviewState = "partial_audit"
		reviewReason = "agent_code_source_checkout_branch_review_incomplete_audit"
	}

	missing := append([]string{}, stringSliceFromAny(codeEvidence["missing_audit_evidence"])...)
	if !completeAudit && !stringListContains(missing, "operator_source_checkout_branch_review") {
		missing = append(missing, "operator_source_checkout_branch_review")
	}

	return map[string]any{
		"mode":                                "redacted_agent_source_checkout_branch_review_plan",
		"review_state":                        reviewState,
		"review_ready":                        completeAudit,
		"review_ready_reason":                 reviewReason,
		"audit_evidence_state":                auditState,
		"worker_dispatch_audit_recorded":      workerDispatchRecorded,
		"codex_execution_plan_recorded":       codexPlanRecorded,
		"patch_prepare_audit_recorded":        patchPrepareRecorded,
		"terminal_audit_recorded":             terminalAuditRecorded,
		"review_evidence_scope":               "shared_code_modification_audit",
		"source_remote_review_ready":          completeAudit,
		"workspace_binding_review_ready":      completeAudit,
		"branch_policy_review_ready":          completeAudit,
		"source_remote_review_scope":          "shared_code_modification_audit",
		"workspace_binding_review_scope":      "shared_code_modification_audit",
		"branch_policy_review_scope":          "shared_code_modification_audit",
		"review_branch_required":              true,
		"default_branch_direct_write_blocked": true,
		"source_checkout_performed":           false,
		"workspace_bound":                     false,
		"branch_created":                      false,
		"default_branch_checked_out":          false,
		"repository_mutation_allowed":         false,
		"external_call_made":                  false,
		"git_fetch_performed":                 false,
		"git_checkout_performed":              false,
		"git_branch_created":                  false,
		"contains_source_remote_url":          false,
		"contains_workspace_path":             false,
		"contains_branch_name":                false,
		"contains_default_branch_name":        false,
		"contains_token":                      false,
		"required_review_fields":              []string{"operation_run_id", "agent_task_id", "source_remote_review", "workspace_binding_review", "branch_policy_review", "review_branch_policy", "operator_review_status"},
		"required_operator_controls":          []string{"source_remote_review", "workspace_binding_review", "branch_policy_review", "default_branch_protection_review", "operator_source_checkout_branch_review"},
		"missing_evidence":                    missing,
		"disabled_backends":                   []string{"source_checkout", "workspace_bind", "git_fetch", "git_checkout", "branch_create", "default_branch_write", "repository_mutation"},
		"suppressed_fields":                   []string{"source_remote_url", "repository_url", "workspace_path", "branch_name", "default_branch", "review_branch_name", "authorization_header", "runtime_config", "environment_variables", "token", "api_key"},
		"message":                             "Source checkout and branch creation are represented as a redacted operator-review preflight derived from the shared code-modification audit in this phase; no repository is cloned, no workspace is bound, no branch is created, and default-branch writes remain blocked.",
	}
}

func agentCodeModificationExecutionArmingPlan(codeEvidence map[string]any) map[string]any {
	evidenceState := cleanPreviewString(codeEvidence["evidence_state"])
	completeAudit := evidenceState == "recorded" &&
		boolOnlyFromAny(codeEvidence["worker_dispatch_audit_recorded"]) &&
		boolOnlyFromAny(codeEvidence["codex_execution_plan_recorded"]) &&
		boolOnlyFromAny(codeEvidence["patch_prepare_audit_recorded"]) &&
		boolOnlyFromAny(codeEvidence["sanitized_result_recorded"])
	armingState := "blocked"
	armingReason := "agent_code_modification_audit_not_recorded"
	switch {
	case completeAudit:
		armingState = "ready_for_operator_review"
		armingReason = "sanitized_agent_code_modification_audit_ready_for_future_execution_review"
	case evidenceState == "waiting_for_worker":
		armingState = "waiting_for_worker"
		armingReason = "agent_code_modification_audit_waiting_for_worker"
	case boolOnlyFromAny(codeEvidence["has_code_modification_audit"]):
		armingState = "partial_audit"
		armingReason = "agent_code_modification_audit_incomplete"
	}
	missing := append([]string{}, stringSliceFromAny(codeEvidence["missing_audit_evidence"])...)
	if !completeAudit && !stringListContains(missing, "future_operator_execution_review") {
		missing = append(missing, "future_operator_execution_review")
	}
	return map[string]any{
		"mode":                           "redacted_agent_code_modification_execution_arming_plan",
		"arming_state":                   armingState,
		"arming_ready":                   armingState == "ready_for_operator_review",
		"arming_ready_reason":            armingReason,
		"audit_evidence_state":           evidenceState,
		"worker_dispatch_audit_recorded": boolOnlyFromAny(codeEvidence["worker_dispatch_audit_recorded"]),
		"codex_execution_plan_recorded":  boolOnlyFromAny(codeEvidence["codex_execution_plan_recorded"]),
		"patch_prepare_audit_recorded":   boolOnlyFromAny(codeEvidence["patch_prepare_audit_recorded"]),
		"terminal_audit_recorded":        boolOnlyFromAny(codeEvidence["sanitized_result_recorded"]),
		"source_checkout_performed":      false,
		"workspace_bound":                false,
		"branch_created":                 false,
		"patch_content_materialized":     false,
		"diff_materialized":              false,
		"file_patch_applied":             false,
		"tests_executed":                 false,
		"git_commit_created":             false,
		"git_push_performed":             false,
		"provider_review_created":        false,
		"commit_push_agent_invoked":      false,
		"external_call_made":             false,
		"repository_mutation_allowed":    false,
		"contains_source_remote_url":     false,
		"contains_workspace_path":        false,
		"contains_branch_name":           false,
		"contains_patch_content":         false,
		"contains_diff_content":          false,
		"contains_file_content":          false,
		"contains_test_output":           false,
		"contains_token":                 false,
		"required_controls":              []string{"agent_execute_approval", "runtime_verification", "source_remote_review", "workspace_binding_review", "branch_policy_review", "structured_patch_review", "test_plan_review", "commit_push_agent_review", "provider_review_reconciliation"},
		"required_evidence":              []string{"worker_dispatch_plan_audit", "codex_execution_plan_audit", "patch_prepare_audit", "terminal_tool_call_audit", "future_operator_execution_review"},
		"missing_evidence":               missing,
		"disabled_backends":              []string{"source_checkout", "workspace_bind", "branch_create", "file_patch_apply", "test_command_execute", "git_commit", "git_push", "pull_request_create", "commit_push_agent"},
		"suppressed_fields":              []string{"runtime_config", "environment_variables", "authorization_header", "source_remote_url", "repository_url", "workspace_path", "branch_name", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "test_output", "command_output", "token", "api_key"},
		"message":                        "Agent code modification execution is only ready for future operator review; source checkout, branch creation, patch application, tests, commit, push, and provider review remain disabled.",
	}
}

func agentCodeModificationEvidence(auditEvidence map[string]any) map[string]any {
	toolCounts := mapFromAny(auditEvidence["tool_counts"])
	hasAudit := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	activeCount := intFromAny(auditEvidence["active_count"], 0)
	hasCodexPlan := intFromAny(toolCounts["codex.execution.plan"], 0) > 0
	hasPatchPrepare := intFromAny(toolCounts["patch.prepare"], 0) > 0
	hasWorkerDispatch := intFromAny(toolCounts["worker.dispatch.plan"], 0) > 0
	auditState := cleanPreviewString(auditEvidence["evidence_state"])
	evidenceState := "not_recorded"
	switch {
	case !hasAudit:
		evidenceState = "not_recorded"
	case activeCount > 0:
		evidenceState = "waiting_for_worker"
	case auditState == "failed" || auditState == "canceled" || auditState == "mixed_failed" || auditState == "unknown" || auditState == "absent":
		evidenceState = auditState
	case boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]) && hasWorkerDispatch && hasCodexPlan && hasPatchPrepare:
		evidenceState = "recorded"
	case boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]):
		evidenceState = "partial_recorded"
	default:
		evidenceState = "blocked"
	}
	missing := []string{}
	if !hasWorkerDispatch {
		missing = append(missing, "worker_dispatch_plan_audit")
	}
	if !hasCodexPlan {
		missing = append(missing, "codex_execution_plan_audit")
	}
	if !hasPatchPrepare {
		missing = append(missing, "patch_prepare_audit")
	}
	if !boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]) {
		missing = append(missing, "terminal_tool_call_audit")
	}
	return map[string]any{
		"mode":                              "redacted_agent_code_modification_evidence",
		"evidence_state":                    evidenceState,
		"tool_call_audit_state":             auditState,
		"has_code_modification_audit":       hasAudit,
		"sanitized_result_recorded":         hasAudit && activeCount == 0,
		"worker_dispatch_audit_recorded":    hasWorkerDispatch,
		"codex_execution_plan_recorded":     hasCodexPlan,
		"patch_prepare_audit_recorded":      hasPatchPrepare,
		"completed_tool_call_count":         intFromAny(auditEvidence["completed_count"], 0),
		"failed_tool_call_count":            intFromAny(auditEvidence["failed_count"], 0),
		"active_tool_call_count":            activeCount,
		"terminal_tool_call_count":          intFromAny(auditEvidence["terminal_count"], 0),
		"required_audit_evidence":           []string{"worker_dispatch_plan_audit", "codex_execution_plan_audit", "patch_prepare_audit", "terminal_tool_call_audit"},
		"missing_audit_evidence":            missing,
		"execution_enabled":                 false,
		"mutation_enabled":                  false,
		"external_call_made":                false,
		"repository_mutation_allowed":       false,
		"source_checkout_performed":         false,
		"workspace_bound":                   false,
		"branch_created":                    false,
		"patch_content_materialized":        false,
		"diff_materialized":                 false,
		"file_patch_applied":                false,
		"tests_executed":                    false,
		"git_commit_created":                false,
		"git_push_performed":                false,
		"pull_request_created":              false,
		"commit_push_agent_invoked":         false,
		"raw_patch_recorded":                false,
		"raw_diff_recorded":                 false,
		"raw_file_content_recorded":         false,
		"raw_command_output_recorded":       false,
		"raw_test_output_recorded":          false,
		"contains_token":                    false,
		"contains_remote_url":               false,
		"contains_branch_name":              false,
		"contains_workspace_path":           false,
		"contains_patch_content":            false,
		"contains_diff_content":             false,
		"contains_file_content":             false,
		"suppressed_fields":                 []string{"runtime_config", "environment_variables", "authorization_header", "source_remote_url", "repository_url", "workspace_path", "branch_name", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "test_output", "command_output", "token", "api_key"},
		"tool_call_audit_evidence_attached": hasAudit,
		"message":                           "Agent code modification evidence is reconciled from sanitized audit rows only; source checkout, patch content, diff, tests, commit, push, and provider review remain disabled.",
	}
}

func agentCodeModificationResultRecordingPlan(evidenceRows ...map[string]any) map[string]any {
	evidence := map[string]any{}
	if len(evidenceRows) > 0 {
		evidence = evidenceRows[0]
	}
	recordingState := "blocked"
	recordingEnabled := false
	resultWritten := false
	readyReason := "agent_code_modification_result_not_recorded"
	if boolOnlyFromAny(evidence["has_code_modification_audit"]) {
		recordingState = cleanPreviewString(evidence["evidence_state"])
		recordingEnabled = boolOnlyFromAny(evidence["sanitized_result_recorded"])
		resultWritten = boolOnlyFromAny(evidence["sanitized_result_recorded"])
		readyReason = "sanitized_agent_code_modification_audit_observed"
	}
	return map[string]any{
		"mode":                         "redacted_agent_code_modification_result_recording_plan",
		"recording_state":              recordingState,
		"recording_ready_reason":       readyReason,
		"recording_enabled":            recordingEnabled,
		"result_written":               resultWritten,
		"operation_log_written":        false,
		"patch_artifact_written":       false,
		"diff_artifact_written":        false,
		"test_result_written":          false,
		"commit_record_written":        false,
		"push_record_written":          false,
		"pr_record_written":            false,
		"raw_patch_recorded":           false,
		"raw_diff_recorded":            false,
		"raw_file_content_recorded":    false,
		"raw_command_output_recorded":  false,
		"raw_test_output_recorded":     false,
		"contains_token":               false,
		"contains_remote_url":          false,
		"contains_branch_name":         false,
		"contains_patch_content":       false,
		"contains_diff_content":        false,
		"contains_file_content":        false,
		"requires_sanitization":        true,
		"requires_human_result_review": true,
		"code_modification_evidence":   evidence,
		"suppressed_fields": []string{
			"source_remote_url",
			"workspace_path",
			"branch_name",
			"patch_content",
			"diff_content",
			"file_content",
			"test_output",
			"command_output",
			"token",
		},
		"message": "No code modification result is persisted until sanitized patch, diff, test, commit, push, and provider review records are wired.",
	}
}

func agentWorkerDispatchPlan(runtime map[string]any, auditEvidenceRows ...map[string]any) map[string]any {
	cliReadiness := agentCodexCLIReadiness(runtime)
	runtimeReady := strings.TrimSpace(fmt.Sprint(cliReadiness["readiness"])) == "metadata_ready"
	dispatchPrerequisite := "metadata_blocked"
	if runtimeReady {
		dispatchPrerequisite = "metadata_available"
	}
	claimPlan := agentWorkerClaimPlan(dispatchPrerequisite)
	allowedToolNames := agentWorkerAllowedToolNames()
	toolInvocationPlan := agentWorkerToolInvocationPlan(allowedToolNames)
	auditEvidence := map[string]any{}
	if len(auditEvidenceRows) > 0 {
		auditEvidence = auditEvidenceRows[0]
	}
	resultCallbackPlan := agentWorkerResultCallbackPlan(auditEvidence)
	toolExecutionArmingPlan := agentWorkerToolExecutionArmingPlan(dispatchPrerequisite, allowedToolNames, auditEvidence, resultCallbackPlan)
	toolInvocationReviewPlan := agentWorkerToolInvocationReviewPlan(allowedToolNames, auditEvidence, toolExecutionArmingPlan)
	resultObserved := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	return map[string]any{
		"mode":                           "redacted_agent_worker_dispatch_plan",
		"dispatch_state":                 "audit_queued",
		"dispatch_ready":                 true,
		"dispatch_ready_reason":          "agent_worker_audit_job_enqueued",
		"prerequisite_state":             dispatchPrerequisite,
		"execution_enabled":              false,
		"audit_worker_execution_enabled": true,
		"worker_claim_enabled":           true,
		"worker_job_created":             true,
		"worker_node_claimed":            false,
		"tool_invocation_enabled":        false,
		"tool_invoked":                   false,
		"external_call_made":             false,
		"repository_mutation_allowed":    false,
		"result_callback_enabled":        true,
		"result_written":                 resultObserved,
		"context_snapshot_materialized":  true,
		"tool_call_audit_evidence":       auditEvidence,
		"worker_claim_plan":              claimPlan,
		"tool_invocation_plan":           toolInvocationPlan,
		"tool_execution_arming_plan":     toolExecutionArmingPlan,
		"tool_invocation_review_plan":    toolInvocationReviewPlan,
		"result_callback_plan":           resultCallbackPlan,
		"requires_operation_run":         true,
		"requires_approved_plan":         true,
		"requires_worker_capability":     true,
		"requires_runtime_verification":  true,
		"requires_tool_allowlist":        true,
		"requires_result_callback":       true,
		"contains_token":                 false,
		"contains_runtime_config":        false,
		"contains_prompt_body":           false,
		"contains_tool_input":            false,
		"contains_tool_output":           false,
		"contains_workspace_path":        false,
		"dispatch_boundary_redacted":     true,
		"blocked_reasons": []string{
			"tool_invocation_not_armed",
			"codex_cli_execution_backend_disabled",
			"repository_mutation_not_armed",
		},
		"required_controls": []string{
			"agent_execute_approval",
			"worker_capability_ai",
			"runtime_verification",
			"tool_allowlist",
			"context_snapshot",
			"result_callback_audit",
			"human_result_review",
		},
		"required_worker_capabilities": []string{
			"ai",
			"context.read",
			"agent.audit",
		},
		"allowed_tool_names": allowedToolNames,
		"disabled_backends": []string{
			"worker_tool_invoke",
			"codex_cli_process",
			"file_patch_apply",
			"git_commit",
			"git_push",
			"pull_request_create",
		},
		"suppressed_fields": []string{
			"runtime_config",
			"environment_variables",
			"authorization_header",
			"workspace_path",
			"repository_url",
			"prompt_body",
			"tool_input",
			"tool_output",
			"patch_content",
			"diff_content",
			"token",
			"api_key",
			"bearer_token",
		},
		"dispatch_sequence": []string{
			"verify_operation_run",
			"verify_approved_plan",
			"select_ai_worker",
			"bind_runtime_metadata",
			"materialize_context_snapshot",
			"invoke_allowlisted_tools",
			"record_tool_results",
			"mark_operation_complete",
		},
		"runtime_readiness": cliReadiness,
		"message":           "Agent worker dispatch now enqueues a real audit worker job and sanitized result callback; allowlisted tool invocation, Codex CLI, patch, git, and pull request mutations remain disabled.",
	}
}

func agentWorkerToolInvocationReviewPlan(allowedToolNames []string, auditEvidence, armingPlan map[string]any) map[string]any {
	metadataReady := boolOnlyFromAny(armingPlan["metadata_ready"])
	allowlistReady := boolOnlyFromAny(armingPlan["allowlist_ready"])
	terminalAuditObserved := boolOnlyFromAny(armingPlan["terminal_audit_observed"])
	callbackWired := boolOnlyFromAny(armingPlan["result_callback_wired"])
	callbackObserved := boolOnlyFromAny(armingPlan["result_callback_observed"])
	armingReady := boolOnlyFromAny(armingPlan["arming_ready"])
	hasAudit := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	sanitizedResultRecorded := boolOnlyFromAny(auditEvidence["sanitized_result_recorded"])
	readyForOperatorReview := metadataReady && allowlistReady && hasAudit && callbackObserved && sanitizedResultRecorded && armingReady

	reviewState := "blocked"
	reviewReason := "agent_tool_invocation_review_metadata_not_ready"
	switch {
	case readyForOperatorReview:
		reviewState = "ready_for_operator_review"
		reviewReason = "allowlisted_tool_invocation_preflight_ready_for_operator_review"
	case metadataReady && allowlistReady && hasAudit && callbackWired && !terminalAuditObserved:
		reviewState = "waiting_for_terminal_audit"
		reviewReason = "agent_tool_call_audit_not_terminal"
	case metadataReady && allowlistReady && callbackWired:
		reviewState = "audit_ready"
		reviewReason = "allowlisted_tool_invocation_audit_boundary_ready"
	case metadataReady && allowlistReady:
		reviewState = "callback_blocked"
		reviewReason = "agent_result_callback_not_wired"
	case metadataReady:
		reviewState = "allowlist_blocked"
		reviewReason = "agent_tool_allowlist_missing"
	}

	missing := []string{}
	if !metadataReady {
		missing = append(missing, "runtime_metadata")
	}
	if !allowlistReady {
		missing = append(missing, "tool_allowlist")
	}
	if !callbackWired {
		missing = append(missing, "result_callback")
	}
	if !hasAudit {
		missing = append(missing, "tool_call_audit_evidence")
	}
	if hasAudit && !terminalAuditObserved {
		missing = append(missing, "terminal_tool_call_audit")
	}
	if hasAudit && !callbackObserved {
		missing = append(missing, "result_callback_observation")
	}
	if hasAudit && !sanitizedResultRecorded {
		missing = append(missing, "sanitized_result_recording")
	}

	return map[string]any{
		"mode":                             "redacted_agent_tool_invocation_review_plan",
		"review_state":                     reviewState,
		"review_ready":                     readyForOperatorReview,
		"review_ready_reason":              reviewReason,
		"metadata_ready":                   metadataReady,
		"allowlist_ready":                  allowlistReady,
		"allowed_tool_count":               len(allowedToolNames),
		"audit_evidence_observed":          hasAudit,
		"terminal_audit_observed":          terminalAuditObserved,
		"sanitized_result_recorded":        sanitizedResultRecorded,
		"result_callback_wired":            callbackWired,
		"result_callback_observed":         callbackObserved,
		"arming_ready_for_operator_review": armingReady,
		"live_tool_invocation_allowed":     false,
		"tool_invocation_enabled":          false,
		"allowlisted_tool_invoked":         false,
		"tool_input_materialized":          false,
		"tool_output_recorded":             false,
		"raw_tool_input_materialized":      false,
		"raw_tool_output_recorded":         false,
		"runtime_config_materialized":      false,
		"codex_cli_process_started":        false,
		"repository_mutation_allowed":      false,
		"external_call_made":               false,
		"operator_review_recorded":         false,
		"allowed_tool_names":               allowedToolNames,
		"required_review_fields":           []string{"operation_run_id", "agent_task_id", "tool_name", "tool_call_id", "allowlist_entry", "input_schema_key", "output_schema_key", "sanitization_status", "operator_review_status"},
		"required_operator_controls":       []string{"agent_execute_approval", "verified_runtime_metadata", "allowlisted_tool_review", "terminal_audit_review", "result_callback_review", "raw_io_redaction_review"},
		"missing_evidence":                 missing,
		"disabled_backends":                []string{"worker_tool_invoke", "tool_input_materialization", "tool_output_recording", "codex_cli_process", "patch_apply", "repository_mutation", "provider_call"},
		"suppressed_fields":                []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "raw_tool_input", "raw_tool_output", "patch_content", "diff_content", "file_content", "token", "api_key", "bearer_token"},
		"message":                          "Allowlisted tool invocation review is a redacted preflight only; live tool calls, raw tool I/O, Codex CLI, patch application, repository mutation, and provider calls remain disabled.",
	}
}

func agentWorkerToolExecutionArmingPlan(prerequisiteState string, allowedToolNames []string, auditEvidence, resultCallbackPlan map[string]any) map[string]any {
	metadataReady := prerequisiteState == "metadata_available"
	allowlistReady := len(allowedToolNames) > 0
	auditObserved := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	auditTerminal := boolOnlyFromAny(auditEvidence["sanitized_result_recorded"])
	callbackWired := boolOnlyFromAny(resultCallbackPlan["callback_enabled"])
	callbackObserved := boolOnlyFromAny(resultCallbackPlan["result_written"])
	armingState := "blocked"
	armingReason := "agent_tool_execution_metadata_not_ready"
	switch {
	case metadataReady && allowlistReady && auditObserved && auditTerminal && callbackWired:
		armingState = "ready_for_operator_review"
		armingReason = "sanitized_agent_tool_audit_ready_for_future_invocation_review"
	case metadataReady && allowlistReady && auditObserved && callbackWired:
		armingState = "waiting_for_terminal_audit"
		armingReason = "agent_tool_call_audit_not_terminal"
	case metadataReady && allowlistReady && callbackWired:
		armingState = "audit_ready"
		armingReason = "agent_tool_audit_boundary_ready"
	case metadataReady && allowlistReady:
		armingState = "callback_blocked"
		armingReason = "agent_result_callback_not_wired"
	case metadataReady:
		armingState = "allowlist_blocked"
		armingReason = "agent_tool_allowlist_missing"
	}
	missing := []string{}
	if !metadataReady {
		missing = append(missing, "runtime_metadata")
	}
	if !allowlistReady {
		missing = append(missing, "tool_allowlist")
	}
	if !callbackWired {
		missing = append(missing, "result_callback")
	}
	if !auditObserved {
		missing = append(missing, "tool_call_audit_evidence")
	}
	if auditObserved && !auditTerminal {
		missing = append(missing, "terminal_tool_call_audit")
	}
	return map[string]any{
		"mode":                        "redacted_agent_tool_execution_arming_plan",
		"arming_state":                armingState,
		"arming_ready":                armingState == "ready_for_operator_review",
		"arming_ready_reason":         armingReason,
		"metadata_ready":              metadataReady,
		"allowlist_ready":             allowlistReady,
		"allowed_tool_count":          len(allowedToolNames),
		"audit_evidence_observed":     auditObserved,
		"terminal_audit_observed":     auditTerminal,
		"result_callback_wired":       callbackWired,
		"result_callback_observed":    callbackObserved,
		"tool_invocation_enabled":     false,
		"tool_invoked":                false,
		"allowlisted_tool_invoked":    false,
		"codex_cli_process_started":   false,
		"patch_applied":               false,
		"repository_mutation_allowed": false,
		"external_call_made":          false,
		"raw_tool_input_materialized": false,
		"raw_tool_output_recorded":    false,
		"runtime_config_materialized": false,
		"contains_runtime_config":     false,
		"contains_prompt_body":        false,
		"contains_tool_input":         false,
		"contains_tool_output":        false,
		"contains_patch_content":      false,
		"contains_diff_content":       false,
		"contains_token":              false,
		"required_controls":           []string{"agent_execute_approval", "verified_runtime_metadata", "tool_allowlist_review", "result_callback_audit", "operator_execution_review", "raw_io_redaction_review"},
		"required_evidence":           []string{"runtime_metadata", "tool_allowlist", "tool_call_audit_evidence", "terminal_tool_call_audit", "result_callback"},
		"missing_evidence":            missing,
		"allowed_tool_names":          allowedToolNames,
		"disabled_backends":           []string{"worker_tool_invoke", "codex_cli_process", "tool_input_materialization", "tool_output_recording", "patch_apply", "repository_mutation"},
		"suppressed_fields":           []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "token", "api_key", "bearer_token"},
		"message":                     "Allowlisted tool execution is only ready for future operator review; live tool invocation, Codex CLI, patch application, repository mutation, and raw tool I/O remain disabled.",
	}
}

func agentToolCallAuditEvidence(toolCalls []map[string]any) map[string]any {
	statusCounts := map[string]any{}
	toolCounts := map[string]any{}
	toolNames := []string{}
	queued, running, completed, failed, canceled, unknown, absent := 0, 0, 0, 0, 0, 0, 0
	items := make([]map[string]any, 0, len(toolCalls))
	for _, call := range toolCalls {
		status := cleanPreviewString(call["status"])
		if status == "" {
			status = "absent"
		}
		toolName := cleanPreviewString(call["tool_name"])
		if toolName == "" {
			toolName = "unknown"
		}
		if !stringInSlice(toolNames, toolName) {
			toolNames = append(toolNames, toolName)
		}
		statusCounts[status] = intFromAny(statusCounts[status], 0) + 1
		toolCounts[toolName] = intFromAny(toolCounts[toolName], 0) + 1
		switch status {
		case "queued":
			queued++
		case "running":
			running++
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled":
			canceled++
		case "absent":
			absent++
		default:
			unknown++
		}
		items = append(items, map[string]any{
			"tool_call_id":                call["id"],
			"operation_run_id":            call["operation_run_id"],
			"tool_name":                   toolName,
			"status":                      status,
			"started_at":                  call["started_at"],
			"finished_at":                 call["finished_at"],
			"created_at":                  call["created_at"],
			"updated_at":                  call["updated_at"],
			"input_included":              false,
			"output_included":             false,
			"raw_tool_output_recorded":    false,
			"raw_runtime_output_recorded": false,
			"secret_included":             false,
		})
	}
	operationCount := len(toolCalls)
	activeCount := queued + running
	terminalCount := completed + failed + canceled + unknown + absent
	evidenceState := "not_recorded"
	if operationCount > 0 {
		evidenceState = "waiting_for_worker"
		if activeCount == 0 {
			if failed > 0 && canceled > 0 {
				evidenceState = "mixed_failed"
			} else if failed > 0 {
				evidenceState = "failed"
			} else if canceled > 0 {
				evidenceState = "canceled"
			} else if absent > 0 {
				evidenceState = "absent"
			} else if unknown > 0 {
				evidenceState = "unknown"
			} else {
				evidenceState = "recorded"
			}
		}
	}
	return map[string]any{
		"mode":                        "agent_tool_call_audit_evidence",
		"evidence_state":              evidenceState,
		"tool_call_count":             operationCount,
		"queued_count":                queued,
		"running_count":               running,
		"completed_count":             completed,
		"failed_count":                failed,
		"canceled_count":              canceled,
		"unknown_count":               unknown,
		"absent_count":                absent,
		"active_count":                activeCount,
		"terminal_count":              terminalCount,
		"has_tool_call_audit":         operationCount > 0,
		"sanitized_result_recorded":   operationCount > 0 && activeCount == 0,
		"has_failures":                failed > 0,
		"has_cancellations":           canceled > 0,
		"has_unknown_status":          unknown > 0,
		"has_absent_status":           absent > 0,
		"tool_names":                  toolNames,
		"status_counts":               statusCounts,
		"tool_counts":                 toolCounts,
		"items":                       items,
		"external_call_made":          false,
		"tool_invocation_enabled":     false,
		"tool_invoked":                false,
		"repository_mutation_allowed": false,
		"raw_tool_output_recorded":    false,
		"raw_runtime_output_recorded": false,
		"raw_patch_recorded":          false,
		"raw_diff_recorded":           false,
		"input_included":              false,
		"output_included":             false,
		"secret_included":             false,
		"suppressed_fields":           []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token", "api_key", "bearer_token"},
		"message":                     "Agent tool-call audit evidence records sanitized status metadata only; raw tool input, output, runtime config, patch, diff, and credentials remain suppressed.",
	}
}

func agentWorkerAllowedToolNames() []string {
	return []string{
		"context.generate",
		"runtime.check",
		"codex.execution.plan",
		"patch.prepare",
	}
}

func agentWorkerClaimPlan(prerequisiteState string) map[string]any {
	metadataReady := prerequisiteState == "metadata_available"
	blockedReasons := []string{"worker_node_not_claimed_yet", "idempotency_claim_pending"}
	if !metadataReady {
		blockedReasons = append(blockedReasons, "runtime_metadata_not_ready")
	}
	return map[string]any{
		"mode":                       "redacted_agent_worker_claim_plan",
		"claim_state":                "queued",
		"claim_ready":                true,
		"claim_ready_reason":         "worker_job_enqueued_for_audit_execution",
		"metadata_ready":             metadataReady,
		"worker_claim_enabled":       true,
		"worker_job_created":         true,
		"worker_node_claimed":        false,
		"operation_locked":           false,
		"idempotency_claimed":        false,
		"external_call_made":         false,
		"required_claim_fields":      []string{"operation_run_id", "agent_task_id", "agent_plan_id", "required_capability", "claim_attempt", "claimed_by", "claimed_at"},
		"suppressed_fields":          []string{"runtime_config", "environment_variables", "worker_secret", "authorization_header", "workspace_path", "prompt_body"},
		"blocked_reasons":            blockedReasons,
		"required_worker_capability": "ai",
		"message":                    "Agent execution creates a worker job for audit processing; runtime secrets and prompt bodies are still not materialized in the claim plan.",
	}
}

func agentWorkerToolInvocationPlan(allowedToolNames []string) map[string]any {
	return map[string]any{
		"mode":                        "redacted_agent_tool_invocation_plan",
		"invocation_state":            "blocked",
		"invocation_ready":            false,
		"invocation_ready_reason":     "agent_tool_invocation_backend_disabled",
		"tool_invocation_enabled":     false,
		"tool_invoked":                false,
		"external_call_made":          false,
		"repository_mutation_allowed": false,
		"contains_tool_input":         false,
		"contains_tool_output":        false,
		"allowed_tool_names":          allowedToolNames,
		"required_invocation_fields":  []string{"operation_run_id", "agent_task_id", "tool_name", "tool_call_id", "input_schema_key", "output_schema_key", "started_at", "finished_at"},
		"suppressed_fields":           []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token"},
		"blocked_reasons":             []string{"tool_invocation_not_armed", "tool_input_materialization_disabled", "tool_output_recording_disabled"},
		"message":                     "Agent tool invocation is audit-only; allowlisted tool names are recorded without materializing tool input or output.",
	}
}

func agentWorkerResultCallbackPlan(auditEvidence map[string]any) map[string]any {
	resultObserved := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	resultRecorded := boolOnlyFromAny(auditEvidence["sanitized_result_recorded"])
	callbackState := "planned"
	readyReason := "sanitized_agent_audit_result_callback_wired"
	blockedReasons := []string{"sanitized_tool_result_not_recorded_yet", "canonical_asset_sync_pending"}
	if resultObserved {
		callbackState = cleanPreviewString(auditEvidence["evidence_state"])
		readyReason = "sanitized_agent_audit_result_observed"
		blockedReasons = []string{"canonical_asset_sync_pending"}
		if !resultRecorded {
			blockedReasons = append(blockedReasons, "agent_tool_call_audit_not_terminal")
		}
	}
	return map[string]any{
		"mode":                         "redacted_agent_result_callback_plan",
		"callback_state":               callbackState,
		"callback_ready":               true,
		"callback_ready_reason":        readyReason,
		"callback_enabled":             true,
		"callback_scope":               "sanitized_audit_status_only",
		"result_written":               resultObserved,
		"operation_log_written":        resultObserved,
		"agent_task_status_written":    false,
		"tool_call_status_written":     resultObserved,
		"canonical_asset_sync_queued":  false,
		"status_snapshot_written":      false,
		"raw_tool_output_recorded":     false,
		"raw_runtime_output_recorded":  false,
		"raw_patch_recorded":           false,
		"raw_diff_recorded":            false,
		"contains_tool_output":         false,
		"contains_runtime_config":      false,
		"requires_human_result_review": true,
		"tool_call_audit_evidence":     auditEvidence,
		"required_result_fields":       []string{"operation_run_id", "agent_task_id", "tool_call_id", "tool_name", "status", "sanitization_status", "started_at", "finished_at"},
		"suppressed_fields":            []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token", "api_key", "bearer_token"},
		"blocked_reasons":              blockedReasons,
		"message":                      "Agent worker completion records sanitized audit status metadata; raw tool output, runtime output, patch, diff, and config material remain suppressed.",
	}
}

func agentCodexExecutionPlan(runtime map[string]any) map[string]any {
	cliReadiness := agentCodexCLIReadiness(runtime)
	prerequisiteState := "metadata_blocked"
	if strings.TrimSpace(fmt.Sprint(cliReadiness["readiness"])) == "metadata_ready" {
		prerequisiteState = "metadata_available"
	}
	return map[string]any{
		"mode":                          "redacted_codex_execution_plan",
		"plan_state":                    "blocked",
		"prerequisite_state":            prerequisiteState,
		"plan_ready":                    false,
		"plan_ready_reason":             "codex_cli_execution_backend_disabled",
		"execution_enabled":             false,
		"process_spawn_enabled":         false,
		"repository_mutation_allowed":   false,
		"pull_request_creation":         false,
		"external_call_made":            false,
		"codex_cli_process_started":     false,
		"command_invoked":               false,
		"workspace_bound":               false,
		"context_snapshot_materialized": true,
		"patch_content_materialized":    false,
		"diff_materialized":             false,
		"file_patch_applied":            false,
		"git_write_performed":           false,
		"approval_action":               "agent.execute",
		"requires_approval":             true,
		"requires_runtime_verification": true,
		"requires_workspace_binding":    true,
		"requires_patch_review":         true,
		"requires_human_approval":       true,
		"contains_token":                false,
		"contains_runtime_config":       false,
		"contains_prompt_body":          false,
		"contains_tool_input":           false,
		"contains_tool_output":          false,
		"contains_patch_content":        false,
		"contains_diff_content":         false,
		"execution_boundary_redacted":   true,
		"blocked_reasons": []string{
			"codex_cli_execution_backend_disabled",
			"process_spawn_disabled",
			"repository_mutation_not_armed",
			"pull_request_workflow_not_wired",
		},
		"required_controls": []string{
			"agent_execute_approval",
			"runtime_verification",
			"workspace_binding",
			"context_snapshot",
			"structured_patch_review",
			"human_patch_approval",
			"commit_push_agent",
			"provider_review_reconciliation",
		},
		"disabled_backends": agentDisabledMutationBackends(),
		"suppressed_fields": []string{
			"runtime_config",
			"environment_variables",
			"authorization_header",
			"workspace_path",
			"repository_url",
			"prompt_body",
			"tool_input",
			"tool_output",
			"patch_content",
			"diff_content",
			"token",
		},
		"execution_sequence": []string{
			"record_context_snapshot",
			"verify_runtime_metadata",
			"bind_workspace",
			"request_process_launch_approval",
			"start_codex_cli",
			"capture_structured_patch",
			"review_patch",
			"request_patch_apply_approval",
			"apply_patch",
			"delegate_commit_push",
		},
		"message": "Codex CLI execution is still a redacted audit plan; no process, patch, git, or pull request mutation is enabled.",
	}
}

func agentDisabledMutationBackends() []string {
	return []string{
		"codex_cli_process",
		"file_patch_apply",
		"git_commit",
		"git_push",
		"pull_request_create",
	}
}

func agentExecutionReadinessGates() []map[string]any {
	return []map[string]any{
		{
			"gate":    "agent_execute_approval",
			"status":  "audit_ready",
			"message": "agent.execute approval only permits audit rows; real Codex CLI execution remains blocked",
		},
		{
			"gate":    "runtime_metadata",
			"status":  "audit_checked",
			"message": "AI runtime metadata is reviewed for audit without exposing runtime secrets",
		},
		{
			"gate":    "codex_cli_process",
			"status":  "blocked",
			"message": "Codex CLI process execution is not enabled",
		},
		{
			"gate":    "repository_mutation",
			"status":  "blocked",
			"message": "repository mutation requires a future approval-gated patch apply operation",
		},
		{
			"gate":    "pull_request_workflow",
			"status":  "blocked",
			"message": "pull request creation is not wired to a provider account workflow",
		},
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
	output["codex_cli_readiness"] = agentCodexCLIReadiness(runtime)
	return map[string]any{
		"tool_name": "runtime.check",
		"input":     input,
		"output":    output,
	}
}

func agentCodexCLIReadiness(runtime map[string]any) map[string]any {
	status := strings.TrimSpace(fmt.Sprint(runtime["status"]))
	if status == "" || status == "<nil>" {
		status = "missing"
	}
	codexBinary := strings.TrimSpace(fmt.Sprint(runtime["codex_binary"]))
	if codexBinary == "<nil>" {
		codexBinary = ""
	}
	runtimeName := strings.TrimSpace(fmt.Sprint(runtime["name"]))
	if runtimeName == "<nil>" {
		runtimeName = ""
	}
	runtimeType := strings.TrimSpace(fmt.Sprint(runtime["runtime_type"]))
	if runtimeType == "<nil>" {
		runtimeType = ""
	}
	runtimeConfigured := runtimeName != "" && runtimeType != ""
	runtimeVerified := status == "verified"
	binaryConfigured := codexBinary != ""

	gates := []map[string]any{
		{
			"gate":    "runtime_configured",
			"status":  readinessStatus(runtimeConfigured),
			"message": "project or global AI runtime metadata must be selected before Codex CLI execution can be enabled",
		},
		{
			"gate":    "runtime_verified",
			"status":  readinessStatus(runtimeVerified),
			"message": "AI runtime must be verified before any future Codex CLI process launch",
		},
		{
			"gate":    "codex_binary_configured",
			"status":  readinessStatus(binaryConfigured),
			"message": "Codex CLI binary path/name must be configured in runtime metadata",
		},
		{
			"gate":    "codex_cli_process",
			"status":  "blocked",
			"message": "process spawning is disabled; this audit row is a dry-run readiness preview only",
		},
		{
			"gate":    "repository_mutation",
			"status":  "blocked",
			"message": "repository writes require a future approval-gated patch apply operation",
		},
		{
			"gate":    "pull_request_workflow",
			"status":  "blocked",
			"message": "pull request creation requires a future provider account workflow",
		},
	}

	readiness := "blocked"
	if runtimeConfigured && runtimeVerified && binaryConfigured {
		readiness = "metadata_ready"
	}
	return map[string]any{
		"readiness":                   readiness,
		"execution_enabled":           false,
		"process_spawn_enabled":       false,
		"external_call_made":          false,
		"repository_mutation_allowed": false,
		"pull_request_creation":       false,
		"runtime_status":              status,
		"gates":                       gates,
		"next_step":                   "Enable Codex CLI only after process launch, patch application, and PR creation each have approval gates and provider reconciliation.",
	}
}

func readinessStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "blocked"
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
	enrichDeploymentTargetsWithExecutionReadiness(items)
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

func (s *Server) previewArgoPodLogQuery(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_target", ProjectID: projectID}, "read") {
		return
	}
	var req argoPodLogRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cleaned, err := cleanArgoPodLogRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := loadArgoPodLogTarget(r.Context(), s.store.DB, projectID, cleaned.DeploymentTargetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	auditRows, err := queryArgoPodLogAuditOperations(r.Context(), s.store.DB, projectID, cleaned.DeploymentTargetID, cleaned.PodName, cleaned.ContainerName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load pod log audit evidence")
		return
	}
	writeJSON(w, http.StatusOK, argoPodLogQueryPreview(cleaned.PodName, cleaned.ContainerName, cleaned.TailLines, cleaned.SinceSeconds, target, auditRows))
}

type argoPodLogRequest struct {
	DeploymentTargetID string `json:"deployment_target_id"`
	PodName            string `json:"pod_name"`
	ContainerName      string `json:"container_name"`
	TailLines          int    `json:"tail_lines"`
	SinceSeconds       int    `json:"since_seconds"`
}

func cleanArgoPodLogRequest(req argoPodLogRequest) (argoPodLogRequest, error) {
	req.DeploymentTargetID = strings.TrimSpace(req.DeploymentTargetID)
	req.PodName = strings.TrimSpace(req.PodName)
	req.ContainerName = strings.TrimSpace(req.ContainerName)
	if req.TailLines <= 0 {
		req.TailLines = 200
	}
	if req.TailLines > 1000 {
		req.TailLines = 1000
	}
	if req.SinceSeconds < 0 {
		req.SinceSeconds = 0
	}
	if req.SinceSeconds > 86400 {
		req.SinceSeconds = 86400
	}
	if req.DeploymentTargetID == "" {
		return req, fmt.Errorf("deployment_target_id is required")
	}
	if req.PodName == "" {
		return req, fmt.Errorf("pod_name is required")
	}
	return req, nil
}

func loadArgoPodLogTarget(ctx context.Context, db sqlx.ExtContext, projectID, deploymentTargetID string) (map[string]any, error) {
	return queryOne(ctx, db, `
		SELECT id, project_id, name, environment, cluster_name, namespace, status
		FROM deployment_targets
		WHERE id=$1 AND project_id=$2`, deploymentTargetID, projectID)
}

func queryArgoPodLogAuditOperations(ctx context.Context, db sqlx.ExtContext, projectID, deploymentTargetID, podName, containerName string) ([]map[string]any, error) {
	return queryMaps(ctx, db, `
		SELECT op.id, op.status, op.created_at, op.updated_at, op.finished_at,
			COUNT(ol.id)::int AS operation_log_count
		FROM operation_runs op
		LEFT JOIN operation_logs ol ON ol.operation_run_id=op.id
		WHERE op.project_id=$1
			AND op.operation_type='argo.pod_logs'
			AND op.input->>'deployment_target_id'=$2
			AND op.input->>'pod_name'=$3
			AND COALESCE(op.input->>'container_name', '')=$4
		GROUP BY op.id
		ORDER BY op.created_at DESC
		LIMIT 20`, projectID, deploymentTargetID, podName, containerName)
}

func (s *Server) requestArgoPodLogRetrieval(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	var req argoPodLogRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cleaned, err := cleanArgoPodLogRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := loadArgoPodLogTarget(r.Context(), s.store.DB, projectID, cleaned.DeploymentTargetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	preview := argoPodLogQueryPreview(cleaned.PodName, cleaned.ContainerName, cleaned.TailLines, cleaned.SinceSeconds, target)
	retrievalPlan := mapFromAny(preview["retrieval_plan"])
	executionPlan := mapFromAny(retrievalPlan["execution_plan"])
	if executionPlan["prerequisite_state"] != "metadata_available" || executionPlan["audit_worker_job_enabled"] != true {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":          "pod log target metadata is incomplete",
			"execution_plan": executionPlan,
		})
		return
	}
	input := argoPodLogOperationInput(projectID, target, mapFromAny(preview["query"]), executionPlan)
	payload := map[string]any{
		"kind":                  "argo_pod_logs",
		"project_id":            projectID,
		"deployment_target_id":  cleaned.DeploymentTargetID,
		"input":                 input,
		"execution_plan_audit":  executionPlan,
		"live_log_body_enabled": false,
	}
	resource := PolicyResource{Type: "deployment_target", ID: cleaned.DeploymentTargetID, ProjectID: projectID}
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return
	}
	decision := NewPolicyChecker().Check(currentUser(r), resource, "argo.pod_logs")
	if decision.Effect == PolicyDeny {
		writeJSON(w, http.StatusForbidden, decision)
		return
	}
	approval, err := s.createOperationApproval(r.Context(), resource, "argo.pod_logs", "retrieve pod logs for "+cleaned.PodName, payload, currentUser(r).ID)
	if err != nil {
		if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
			writeError(w, http.StatusConflict, "approval request is already pending")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create pod log approval request")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"approval":           approval,
		"decision":           decision,
		"operation_type":     "argo.pod_logs",
		"worker_job_created": false,
		"log_body_included":  false,
		"message":            "Pod log approval requested; worker job will be created only after approval.",
	})
}

func (s *Server) recordArgoPodLogAuditSnapshot(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	var req struct {
		DeploymentTargetID string `json:"deployment_target_id"`
		PodName            string `json:"pod_name"`
		ContainerName      string `json:"container_name"`
		TailLines          int    `json:"tail_lines"`
		SinceSeconds       int    `json:"since_seconds"`
		DryRun             bool   `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cleaned, err := cleanArgoPodLogRequest(argoPodLogRequest{
		DeploymentTargetID: req.DeploymentTargetID,
		PodName:            req.PodName,
		ContainerName:      req.ContainerName,
		TailLines:          req.TailLines,
		SinceSeconds:       req.SinceSeconds,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_target", ID: cleaned.DeploymentTargetID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordArgoPodLogAuditSnapshot(r.Context(), s.store, ArgoPodLogAuditSnapshotOptions{
		ProjectID:          projectID,
		DeploymentTargetID: cleaned.DeploymentTargetID,
		PodName:            cleaned.PodName,
		ContainerName:      cleaned.ContainerName,
		TailLines:          cleaned.TailLines,
		SinceSeconds:       cleaned.SinceSeconds,
		DryRun:             req.DryRun,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("pod log audit snapshot failed", "project_id", projectID, "deployment_target_id", cleaned.DeploymentTargetID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record pod log audit snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func argoPodLogOperationInput(projectID string, target, query, executionPlan map[string]any) map[string]any {
	return map[string]any{
		"project_id":             projectID,
		"deployment_target_id":   cleanOptionalID(fmt.Sprint(target["id"])),
		"deployment_target_name": cleanOptionalText(fmt.Sprint(target["name"])),
		"environment":            cleanOptionalText(fmt.Sprint(target["environment"])),
		"cluster_name":           cleanOptionalText(fmt.Sprint(target["cluster_name"])),
		"namespace":              cleanOptionalText(fmt.Sprint(target["namespace"])),
		"pod_name":               cleanOptionalText(fmt.Sprint(query["pod_name"])),
		"container_name":         cleanOptionalText(fmt.Sprint(query["container_name"])),
		"tail_lines":             intFromAny(query["tail_lines"], 200),
		"since_seconds":          intFromAny(query["since_seconds"], 0),
		"result_scope":           "sanitized_metadata_only",
		"execution_mode":         "approval_gated_audit",
		"live_log_backend":       "disabled",
		"execution_plan":         executionPlan,
	}
}

func (s *Server) enqueueArgoPodLogOperationTx(ctx context.Context, tx *sqlx.Tx, input map[string]any) (map[string]any, error) {
	projectID := cleanOptionalID(fmt.Sprint(input["project_id"]))
	targetID := cleanOptionalID(fmt.Sprint(input["deployment_target_id"]))
	podName := cleanOptionalText(fmt.Sprint(input["pod_name"]))
	if projectID == "" || targetID == "" || podName == "" {
		return nil, fmt.Errorf("invalid pod log operation input")
	}
	title := "retrieve pod logs for " + podName
	op, err := enqueueOperationTx(ctx, tx, projectID, "", "argo.pod_logs", title, input, []string{"argo", "kubernetes"}, "control-worker")
	if err != nil {
		return nil, fmt.Errorf("could not enqueue pod log operation")
	}
	return op, nil
}

func argoPodLogQueryPreview(podName, containerName string, tailLines, sinceSeconds int, target map[string]any, auditRows ...[]map[string]any) map[string]any {
	if tailLines <= 0 {
		tailLines = 200
	}
	if tailLines > 1000 {
		tailLines = 1000
	}
	if sinceSeconds < 0 {
		sinceSeconds = 0
	}
	if sinceSeconds > 86400 {
		sinceSeconds = 86400
	}
	namespace := strings.TrimSpace(fmt.Sprint(target["namespace"]))
	clusterName := strings.TrimSpace(fmt.Sprint(target["cluster_name"]))
	status := strings.TrimSpace(fmt.Sprint(target["status"]))
	if namespace == "<nil>" {
		namespace = ""
	}
	if clusterName == "<nil>" {
		clusterName = ""
	}
	if status == "<nil>" {
		status = ""
	}
	blockedReasons := []string{"pod_log_backend_disabled", "kubernetes_client_not_bound"}
	if namespace == "" {
		blockedReasons = append(blockedReasons, "namespace_missing")
	}
	if clusterName == "" {
		blockedReasons = append(blockedReasons, "cluster_name_missing")
	}
	metadataReady := namespace != "" && clusterName != "" && strings.TrimSpace(podName) != ""
	queryState := "blocked"
	if metadataReady {
		queryState = "ready_for_approval"
	}
	query := map[string]any{
		"pod_name":       podName,
		"container_name": containerName,
		"namespace":      namespace,
		"tail_lines":     tailLines,
		"since_seconds":  sinceSeconds,
	}
	deploymentTarget := map[string]any{
		"id":           target["id"],
		"name":         target["name"],
		"environment":  target["environment"],
		"cluster_name": clusterName,
		"namespace":    namespace,
		"status":       status,
	}
	var auditEvidenceRows []map[string]any
	if len(auditRows) > 0 {
		auditEvidenceRows = auditRows[0]
	}
	auditEvidence := argoPodLogAuditEvidenceSummary(auditEvidenceRows)
	retrievalPlan := argoPodLogRetrievalPlan(query, deploymentTarget, blockedReasons, auditEvidence)
	return map[string]any{
		"mode":                      "read_only_preview",
		"query_state":               queryState,
		"execution_enabled":         false,
		"operation_request_enabled": metadataReady,
		"external_call_made":        false,
		"kubernetes_api_call":       false,
		"argocd_api_call":           false,
		"log_body_included":         false,
		"contains_secret":           false,
		"contains_token":            false,
		"deployment_target":         deploymentTarget,
		"query":                     query,
		"audit_evidence":            auditEvidence,
		"retrieval_plan":            retrievalPlan,
		"required_controls":         []string{"deployment_target_review", "kubeconfig_binding", "namespace_confirmation", "pod_name_confirmation", "operator_confirmation"},
		"disabled_backends":         []string{"kubectl_logs", "kubernetes_pod_log_api", "argocd_pod_logs"},
		"suppressed_fields":         []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "pod_env", "secret_env", "volume_secret"},
		"blocked_reasons":           blockedReasons,
		"next_step":                 "Request an approval-gated pod log audit job; kubeconfig binding and live log bodies remain disabled until a reviewed backend is added.",
	}
}

func argoPodLogRetrievalPlan(query, target map[string]any, blockedReasons []string, auditEvidence map[string]any) map[string]any {
	metadataReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != "" && strings.TrimSpace(fmt.Sprint(target["namespace"])) != "" && strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	approvalStatus := "blocked"
	if metadataReady {
		approvalStatus = "planned"
	}
	steps := []map[string]any{
		{
			"kind":    "operation_approval",
			"status":  approvalStatus,
			"message": "pod log retrieval requires an approval-gated audit operation before any live backend can be enabled",
		},
		{
			"kind":    "kubeconfig_binding",
			"status":  "blocked",
			"message": "bind a reviewed namespace-scoped kubeconfig outside the preview response",
		},
		{
			"kind":    "target_scope_check",
			"status":  podLogPlanStatus(strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != "" && strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""),
			"message": "deployment target must carry cluster and namespace metadata",
		},
		{
			"kind":    "pod_identity_confirmation",
			"status":  podLogPlanStatus(strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""),
			"message": "operator must provide an explicit pod name",
		},
		{
			"kind":    "container_scope_confirmation",
			"status":  "planned",
			"message": "empty container name means provider default; explicit container narrows scope",
		},
		{
			"kind":    "live_log_stream",
			"status":  "blocked",
			"message": "Kubernetes/Argo pod log backends are disabled in the first-version preview",
		},
	}
	planned, blocked := 0, 0
	for _, step := range steps {
		step["external_call_made"] = false
		step["secret_included"] = false
		if step["status"] == "planned" {
			planned++
		} else {
			blocked++
		}
	}
	executionPlan := argoPodLogExecutionPlan(query, target, steps, blockedReasons, auditEvidence)
	planState := "blocked"
	if metadataReady {
		planState = "ready_for_approval"
	}
	return map[string]any{
		"mode":                         "pod_log_retrieval_plan_preview",
		"plan_state":                   planState,
		"execution_enabled":            false,
		"operation_request_enabled":    metadataReady,
		"external_call_made":           false,
		"kubernetes_api_call":          false,
		"argocd_api_call":              false,
		"log_body_included":            false,
		"kubeconfig_included":          false,
		"contains_secret":              false,
		"planned_count":                planned,
		"blocked_count":                blocked,
		"step_count":                   len(steps),
		"steps":                        steps,
		"blocked_reasons":              blockedReasons,
		"audit_evidence":               auditEvidence,
		"execution_plan":               executionPlan,
		"required_live_controls":       []string{"operation_approval", "environment_review", "kubeconfig_binding", "namespace_confirmation", "pod_name_confirmation", "operator_confirmation"},
		"disabled_backends":            []string{"kubectl_logs", "kubernetes_pod_log_api", "argocd_pod_logs"},
		"suppressed_fields":            []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "pod_env", "secret_env", "volume_secret"},
		"required_operator_action":     "Review the target and pod identity, then request an approval-gated audit job; live log backend remains disabled.",
		"future_execution_result_type": "sanitized_metadata_only",
	}
}

func argoPodLogExecutionPlan(query, target map[string]any, steps []map[string]any, blockedReasons []string, auditEvidence map[string]any) map[string]any {
	planned, blocked := 0, 0
	for _, step := range steps {
		if step["status"] == "planned" {
			planned++
		} else {
			blocked++
		}
	}
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	prerequisiteState := "metadata_blocked"
	if namespaceReady && clusterReady && podReady {
		prerequisiteState = "metadata_available"
	}
	auditReady := prerequisiteState == "metadata_available"
	executionState := "blocked"
	if auditReady {
		executionState = "ready_for_approval"
	}
	kubeconfigBindingPlan := argoPodLogKubeconfigBindingPlan(prerequisiteState, namespaceReady, clusterReady)
	kubeconfigReadinessPlan := argoPodLogNamespaceKubeconfigReadinessPlan(query, target, prerequisiteState, auditEvidence)
	podScopePlan := argoPodLogPodScopePlan(query, target, prerequisiteState)
	logCapturePlan := argoPodLogCapturePlan(prerequisiteState)
	resultRecordingPlan := argoPodLogResultRecordingPlan(auditReady, auditEvidence, query, target, prerequisiteState)
	liveLogStreamPlan := argoPodLogLiveLogStreamReviewPlan(query, target, prerequisiteState, auditEvidence, kubeconfigReadinessPlan, podScopePlan, logCapturePlan, resultRecordingPlan)
	return map[string]any{
		"mode":                          "pod_log_execution_plan_preview",
		"execution_state":               executionState,
		"prerequisite_state":            prerequisiteState,
		"approval_request_plan":         argoPodLogApprovalRequestPlan(query, target, prerequisiteState),
		"execution_enabled":             false,
		"operation_request_enabled":     auditReady,
		"audit_worker_job_enabled":      auditReady,
		"external_call_made":            false,
		"operation_enqueued":            false,
		"worker_job_created":            false,
		"audit_operation_observed":      boolOnlyFromAny(auditEvidence["has_audit_operations"]),
		"sanitized_result_observed":     boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]),
		"kubeconfig_bound":              false,
		"kubernetes_client_created":     false,
		"kubernetes_api_call":           false,
		"argocd_api_call":               false,
		"kubectl_command_invoked":       false,
		"log_stream_opened":             false,
		"log_body_included":             false,
		"redacted_log_body_included":    false,
		"result_written":                false,
		"secret_included":               false,
		"kubeconfig_included":           false,
		"authorization_header_included": false,
		"planned_step_count":            planned,
		"blocked_step_count":            blocked,
		"blocked_reasons":               blockedReasons,
		"audit_evidence":                auditEvidence,
		"required_controls":             []string{"operation_approval", "environment_review", "kubeconfig_binding", "namespace_confirmation", "pod_name_confirmation", "container_scope_confirmation", "operator_confirmation", "result_redaction_review"},
		"disabled_backends":             []string{"kubeconfig_binding", "kubernetes_pod_log_api", "kubectl_logs", "argocd_pod_logs", "raw_log_body_recording"},
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "redacted_log_body", "pod_env", "secret_env", "volume_secret"},
		"execution_sequence":            []string{"request_operation_approval", "bind_namespace_scoped_kubeconfig", "verify_target_scope", "confirm_pod_identity", "open_pod_log_stream", "redact_log_body", "record_sanitized_result"},
		"kubeconfig_binding_plan":       kubeconfigBindingPlan,
		"kubeconfig_readiness_plan":     kubeconfigReadinessPlan,
		"pod_scope_plan":                podScopePlan,
		"log_capture_plan":              logCapturePlan,
		"live_log_stream_plan":          liveLogStreamPlan,
		"result_recording_plan":         resultRecordingPlan,
		"message":                       "Pod log live execution remains disabled; metadata-ready requests can create an approval-gated audit job with sanitized result only.",
	}
}

func argoPodLogLiveLogStreamReviewPlan(query, target map[string]any, prerequisiteState string, auditEvidence, kubeconfigReadinessPlan, podScopePlan, logCapturePlan, resultRecordingPlan map[string]any) map[string]any {
	evidenceState := cleanPreviewString(auditEvidence["evidence_state"])
	if evidenceState == "" {
		evidenceState = "not_requested"
	}
	streamState := "metadata_blocked"
	if prerequisiteState == "metadata_available" {
		streamState = "ready_for_approval"
	}
	switch evidenceState {
	case "waiting_for_worker", "failed", "canceled", "unknown":
		streamState = "audit_" + evidenceState
	case "recorded":
		if prerequisiteState == "metadata_available" {
			streamState = "ready_for_operator_review"
		}
	}
	streamReadyForReview := streamState == "ready_for_operator_review" &&
		boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]) &&
		kubeconfigReadinessPlan["readiness_state"] == "audit_result_ready_for_binding_review" &&
		podScopePlan["scope_state"] == "planned" &&
		logCapturePlan["capture_state"] == "planned" &&
		boolOnlyFromAny(resultRecordingPlan["result_written"])
	blockedReasons := []string{"live_log_backend_disabled", "namespace_scoped_kubeconfig_not_bound", "live_log_stream_not_opened"}
	if prerequisiteState != "metadata_available" {
		blockedReasons = append(blockedReasons, "metadata_incomplete")
	}
	if evidenceState == "not_requested" {
		blockedReasons = append(blockedReasons, "pod_log_audit_not_requested")
	}
	if evidenceState == "waiting_for_worker" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_still_running")
	}
	if evidenceState == "failed" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_failed")
	}
	if evidenceState == "canceled" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_canceled")
	}
	if evidenceState == "unknown" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_status_unknown")
	}
	if !boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]) {
		blockedReasons = append(blockedReasons, "sanitized_log_result_not_recorded")
	}
	return map[string]any{
		"mode":                              "pod_log_live_log_stream_review_plan",
		"stream_state":                      streamState,
		"stream_ready_for_review":           streamReadyForReview,
		"metadata_ready":                    prerequisiteState == "metadata_available",
		"audit_operation_observed":          boolOnlyFromAny(auditEvidence["has_audit_operations"]),
		"sanitized_result_observed":         boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]),
		"kubeconfig_binding_review_ready":   kubeconfigReadinessPlan["readiness_ready"] == true,
		"namespace_scope_ready":             kubeconfigReadinessPlan["namespace_scope_ready"] == true,
		"pod_identity_present":              kubeconfigReadinessPlan["pod_identity_present"] == true,
		"pod_scope_review_ready":            podScopePlan["scope_state"] == "planned",
		"log_capture_review_ready":          logCapturePlan["capture_state"] == "planned",
		"result_recording_observed":         boolOnlyFromAny(resultRecordingPlan["result_written"]),
		"namespace_scoped_kubeconfig_bound": false,
		"kubeconfig_secret_read":            false,
		"kubeconfig_bound":                  false,
		"kubernetes_client_created":         false,
		"token_subject_review_performed":    false,
		"rbac_read_logs_review_performed":   false,
		"kubernetes_api_call":               false,
		"argocd_api_call":                   false,
		"kubectl_command_invoked":           false,
		"live_log_stream_opened":            false,
		"log_stream_opened":                 false,
		"log_body_included":                 false,
		"redacted_log_body_included":        false,
		"raw_response_recorded":             false,
		"result_write_enabled":              false,
		"external_call_made":                false,
		"contains_kubeconfig":               false,
		"contains_cluster_token":            false,
		"contains_authorization_header":     false,
		"contains_log_body":                 false,
		"contains_redacted_log_body":        false,
		"contains_raw_kubernetes_response":  false,
		"required_review_fields":            []string{"operation_run_id", "approval_request_id", "deployment_target_id", "cluster_name", "namespace", "pod_name", "container_name", "tail_lines", "since_seconds", "kubeconfig_binding_status", "pod_scope_status", "log_redaction_status", "operator_review_status"},
		"required_controls":                 []string{"operation_approval", "namespace_scoped_kubeconfig_secret", "token_subject_review", "rbac_read_logs_review", "namespace_confirmation", "pod_identity_confirmation", "container_scope_confirmation", "log_line_redaction", "result_redaction_review"},
		"disabled_backends":                 []string{"kubeconfig_secret_binding", "kubernetes_client_create", "kubernetes_pod_log_api", "kubectl_logs", "argocd_pod_logs", "live_log_stream_open", "log_body_storage", "redacted_log_body_storage"},
		"suppressed_fields":                 []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret", "pod_annotations"},
		"blocked_reasons":                   blockedReasons,
		"execution_blockers":                []string{"live_log_backend_disabled", "namespace_scoped_kubeconfig_not_bound", "pod_scope_not_verified", "result_redaction_review_not_approved"},
		"target_ref":                        map[string]any{"deployment_target_id": target["id"], "cluster_name": target["cluster_name"], "namespace": target["namespace"], "pod_name": query["pod_name"], "container_name": query["container_name"], "tail_lines": query["tail_lines"], "since_seconds": query["since_seconds"]},
		"message":                           "Live pod-log stream review is metadata-only; no kubeconfig, Kubernetes client, log stream, raw body, redacted body, or result row is produced here.",
	}
}

func argoPodLogKubeconfigBindingPlan(prerequisiteState string, namespaceReady, clusterReady bool) map[string]any {
	bindingState := "blocked"
	if prerequisiteState == "metadata_available" {
		bindingState = "planned"
	}
	blockedReasons := []string{"kubeconfig_binding_not_performed"}
	if !clusterReady {
		blockedReasons = append(blockedReasons, "cluster_name_missing")
	}
	if !namespaceReady {
		blockedReasons = append(blockedReasons, "namespace_missing")
	}
	return map[string]any{
		"mode":                          "pod_log_kubeconfig_binding_plan",
		"binding_state":                 bindingState,
		"metadata_ready":                prerequisiteState == "metadata_available",
		"namespace_scoped_required":     true,
		"kubeconfig_bound":              false,
		"kubernetes_client_created":     false,
		"token_subject_reviewed":        false,
		"external_call_made":            false,
		"contains_kubeconfig":           false,
		"contains_cluster_token":        false,
		"contains_authorization_header": false,
		"required_controls":             []string{"environment_review", "namespace_scoped_kubeconfig", "token_subject_review", "rbac_read_logs_review"},
		"disabled_backends":             []string{"kubeconfig_binding", "kubernetes_client_create", "token_subject_review"},
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key"},
		"blocked_reasons":               blockedReasons,
		"execution_blockers":            []string{"kubeconfig_binding_not_approved", "kubeconfig_binding_not_performed"},
		"message":                       "Kubeconfig binding is planned only; no kubeconfig, cluster token, client certificate, or Kubernetes client is created.",
	}
}

func argoPodLogPodScopePlan(query, target map[string]any, prerequisiteState string) map[string]any {
	podName := strings.TrimSpace(fmt.Sprint(query["pod_name"]))
	containerName := strings.TrimSpace(fmt.Sprint(query["container_name"]))
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := podName != ""
	scopeState := "blocked"
	if prerequisiteState == "metadata_available" {
		scopeState = "planned"
	}
	blockedReasons := []string{"pod_scope_not_verified"}
	if !clusterReady {
		blockedReasons = append(blockedReasons, "cluster_name_missing")
	}
	if !namespaceReady {
		blockedReasons = append(blockedReasons, "namespace_missing")
	}
	if !podReady {
		blockedReasons = append(blockedReasons, "pod_name_missing")
	}
	return map[string]any{
		"mode":                      "pod_log_pod_scope_plan",
		"scope_state":               scopeState,
		"metadata_ready":            prerequisiteState == "metadata_available",
		"pod_name_present":          podReady,
		"container_name_present":    containerName != "",
		"default_container_allowed": containerName == "",
		"target_scope_verified":     false,
		"pod_identity_confirmed":    false,
		"container_scope_confirmed": false,
		"external_call_made":        false,
		"contains_pod_env":          false,
		"contains_secret_env":       false,
		"required_controls":         []string{"namespace_confirmation", "pod_name_confirmation", "container_scope_confirmation", "operator_confirmation"},
		"disabled_backends":         []string{"kubernetes_pod_lookup", "argocd_pod_lookup"},
		"suppressed_fields":         []string{"pod_env", "secret_env", "volume_secret", "owner_references", "pod_annotations"},
		"blocked_reasons":           blockedReasons,
		"execution_blockers":        []string{"pod_scope_not_verified", "pod_identity_not_confirmed"},
		"message":                   "Pod and container scope verification is planned only; no pod lookup, env, annotation, or secret material is read.",
	}
}

func argoPodLogCapturePlan(prerequisiteState string) map[string]any {
	captureState := "blocked"
	if prerequisiteState == "metadata_available" {
		captureState = "planned"
	}
	return map[string]any{
		"mode":                       "pod_log_capture_plan",
		"capture_state":              captureState,
		"metadata_ready":             prerequisiteState == "metadata_available",
		"kubernetes_api_call":        false,
		"argocd_api_call":            false,
		"kubectl_command_invoked":    false,
		"log_stream_opened":          false,
		"log_body_included":          false,
		"redacted_log_body_included": false,
		"redaction_performed":        false,
		"result_write_planned":       captureState == "planned",
		"external_call_made":         false,
		"contains_log_body":          false,
		"contains_redacted_log_body": false,
		"contains_raw_response":      false,
		"required_controls":          []string{"operation_approval", "log_line_redaction", "tail_limit_enforcement", "result_redaction_review"},
		"disabled_backends":          []string{"kubernetes_pod_log_api", "kubectl_logs", "argocd_pod_logs", "log_stream_result_write"},
		"suppressed_fields":          []string{"log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret"},
		"blocked_reasons":            []string{"pod_log_execution_not_performed", "log_stream_not_opened", "sanitized_log_result_not_recorded"},
		"execution_blockers":         []string{"pod_log_backend_disabled", "log_stream_result_write_disabled"},
		"message":                    "Pod log capture is planned only; no log stream, raw body, redacted body, Kubernetes response, or result row is produced.",
	}
}

func argoPodLogApprovalRequestPlan(query, target map[string]any, prerequisiteState string) map[string]any {
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	metadataReady := namespaceReady && clusterReady && podReady && prerequisiteState == "metadata_available"
	requestState := "blocked"
	if metadataReady {
		requestState = "planned"
	}
	requestReadyReason := "pod_log_metadata_incomplete"
	if metadataReady {
		requestReadyReason = "pod_log_audit_operation_ready"
	}
	metadataBlockedReasons := []string{}
	if !clusterReady {
		metadataBlockedReasons = append(metadataBlockedReasons, "cluster_name_missing")
	}
	if !namespaceReady {
		metadataBlockedReasons = append(metadataBlockedReasons, "namespace_missing")
	}
	if !podReady {
		metadataBlockedReasons = append(metadataBlockedReasons, "pod_name_missing")
	}
	return map[string]any{
		"mode":                         "pod_log_approval_request_plan",
		"request_state":                requestState,
		"request_ready":                metadataReady,
		"request_ready_reason":         requestReadyReason,
		"metadata_ready":               metadataReady,
		"operation_created":            false,
		"approval_request_created":     false,
		"worker_job_created":           false,
		"kubeconfig_binding_requested": false,
		"external_call_made":           false,
		"required_action":              "Create a high-risk operation approval request before any pod log audit worker can run.",
		"required_approval_fields":     []string{"operation_run_id", "deployment_target_id", "cluster_name", "namespace", "pod_name", "container_name", "tail_lines", "since_seconds", "requested_by", "reason"},
		"suppressed_fields":            []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "pod_env", "secret_env", "volume_secret", "approval_reason_detail"},
		"blocked_reasons":              metadataBlockedReasons,
		"execution_blockers":           []string{"pod_log_operation_not_created", "approval_policy_not_applied", "live_log_backend_disabled"},
	}
}

func argoPodLogAuditEvidenceSummary(rows []map[string]any) map[string]any {
	queued, running, completed, failed, canceled, unknown, logCount := 0, 0, 0, 0, 0, 0, 0
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		status := cleanPreviewString(row["status"])
		if status == "" {
			status = "unknown"
		}
		switch status {
		case "queued":
			queued++
		case "running":
			running++
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled":
			canceled++
		default:
			unknown++
		}
		rowLogCount := intFromAny(row["operation_log_count"], 0)
		logCount += rowLogCount
		items = append(items, map[string]any{
			"operation_run_id":      row["id"],
			"status":                status,
			"created_at":            row["created_at"],
			"updated_at":            row["updated_at"],
			"finished_at":           row["finished_at"],
			"operation_log_count":   rowLogCount,
			"raw_input_included":    false,
			"log_body_included":     false,
			"kubeconfig_included":   false,
			"raw_response_included": false,
			"secret_included":       false,
		})
	}
	operationCount := len(rows)
	activeCount := queued + running
	evidenceState := "not_requested"
	if operationCount > 0 {
		evidenceState = "waiting_for_worker"
		if activeCount == 0 {
			if failed > 0 {
				evidenceState = "failed"
			} else if canceled > 0 {
				evidenceState = "canceled"
			} else if completed > 0 {
				evidenceState = "recorded"
			} else if unknown > 0 {
				evidenceState = "unknown"
			}
		}
	}
	return map[string]any{
		"mode":                       "pod_log_audit_evidence_summary",
		"operation_count":            operationCount,
		"queued_count":               queued,
		"running_count":              running,
		"completed_count":            completed,
		"failed_count":               failed,
		"canceled_count":             canceled,
		"unknown_count":              unknown,
		"active_count":               activeCount,
		"operation_log_count":        logCount,
		"evidence_state":             evidenceState,
		"has_audit_operations":       operationCount > 0,
		"sanitized_result_recorded":  completed > 0,
		"has_failures":               failed > 0,
		"has_cancellations":          canceled > 0,
		"has_unknown_status":         unknown > 0,
		"items":                      items,
		"external_call_made":         false,
		"kubernetes_api_call":        false,
		"argocd_api_call":            false,
		"kubeconfig_included":        false,
		"log_body_included":          false,
		"redacted_log_body_included": false,
		"raw_response_included":      false,
		"secret_included":            false,
		"suppressed_fields":          []string{"operation_input", "kubeconfig", "cluster_token", "authorization_header", "log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret"},
	}
}

func argoPodLogResultRecordingPlan(auditReady bool, evidence, query, target map[string]any, prerequisiteState string) map[string]any {
	recordingState := "blocked"
	readyReason := "pod_log_metadata_incomplete"
	if auditReady {
		recordingState = "planned"
		readyReason = "sanitized_audit_result_ready_after_worker"
	}
	if auditReady && boolOnlyFromAny(evidence["has_audit_operations"]) {
		recordingState = cleanPreviewString(evidence["evidence_state"])
		if recordingState == "waiting_for_worker" {
			readyReason = "pod_log_audit_worker_still_running"
		} else if recordingState == "recorded" {
			readyReason = "sanitized_pod_log_audit_result_recorded"
		} else if recordingState == "failed" {
			readyReason = "pod_log_audit_worker_failed"
		} else if recordingState == "canceled" {
			readyReason = "pod_log_audit_worker_canceled"
		} else if recordingState == "unknown" {
			readyReason = "pod_log_audit_worker_status_unknown"
		}
	}
	resultObserved := auditReady && boolOnlyFromAny(evidence["sanitized_result_recorded"])
	blockedReasons := []string{"live_log_backend_disabled", "sanitized_log_result_not_recorded"}
	if resultObserved {
		blockedReasons = []string{"live_log_backend_disabled"}
	}
	return map[string]any{
		"mode":                          "pod_log_result_recording_plan",
		"recording_state":               recordingState,
		"recording_ready":               auditReady,
		"recording_ready_reason":        readyReason,
		"recording_enabled":             auditReady,
		"result_written":                resultObserved,
		"operation_log_written":         auditReady && intFromAny(evidence["operation_log_count"], 0) > 0,
		"canonical_asset_sync_queued":   auditReady,
		"status_snapshot_written":       auditReady,
		"audit_operation_observed":      boolOnlyFromAny(evidence["has_audit_operations"]),
		"sanitized_result_observed":     resultObserved,
		"kubeconfig_binding_recorded":   false,
		"pod_scope_recorded":            false,
		"log_capture_recorded":          false,
		"log_body_included":             false,
		"redacted_log_body_included":    false,
		"raw_response_included":         false,
		"kubeconfig_included":           false,
		"authorization_header_included": false,
		"audit_evidence":                evidence,
		"kubeconfig_readiness_plan":     argoPodLogNamespaceKubeconfigReadinessPlan(query, target, prerequisiteState, evidence),
		"required_result_fields":        []string{"operation_run_id", "approval_request_id", "deployment_target_id", "pod_name", "container_name", "status", "line_count", "truncated", "started_at", "finished_at", "kubeconfig_binding_status", "pod_scope_status", "log_capture_status", "redaction_status"},
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "redacted_log_body", "pod_env", "secret_env", "volume_secret", "raw_kubernetes_response"},
		"blocked_reasons":               blockedReasons,
		"message":                       "Preview does not write results; the audit worker records sanitized metadata only and never stores kubeconfig, raw response, or log bodies.",
	}
}

func argoPodLogNamespaceKubeconfigReadinessPlan(query, target map[string]any, prerequisiteState string, evidence map[string]any) map[string]any {
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	metadataReady := prerequisiteState == "metadata_available" || (namespaceReady && clusterReady && podReady)
	evidenceState := cleanPreviewString(evidence["evidence_state"])
	resultObserved := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	hasAudit := boolOnlyFromAny(evidence["has_audit_operations"])
	readinessState := "metadata_blocked"
	readinessReason := "pod_log_target_metadata_incomplete"
	if metadataReady {
		readinessState = "ready_for_approval"
		readinessReason = "namespace_scoped_kubeconfig_binding_ready_for_operator_approval"
	}
	if metadataReady && hasAudit {
		switch evidenceState {
		case "waiting_for_worker":
			readinessState = "waiting_for_worker"
			readinessReason = "pod_log_audit_worker_still_running"
		case "failed":
			readinessState = "audit_failed"
			readinessReason = "pod_log_audit_worker_failed"
		case "canceled":
			readinessState = "audit_canceled"
			readinessReason = "pod_log_audit_worker_canceled"
		case "recorded":
			readinessState = "audit_result_ready_for_binding_review"
			readinessReason = "sanitized_pod_log_audit_result_ready_for_kubeconfig_binding_review"
		case "unknown":
			readinessState = "audit_unknown"
			readinessReason = "pod_log_audit_worker_status_unknown"
		}
	}
	readinessReady := readinessState == "ready_for_approval" || readinessState == "audit_result_ready_for_binding_review"
	return map[string]any{
		"mode":                              "pod_log_namespace_kubeconfig_binding_readiness_plan",
		"readiness_state":                   readinessState,
		"readiness_ready":                   readinessReady,
		"readiness_ready_reason":            readinessReason,
		"metadata_ready":                    metadataReady,
		"namespace_scope_ready":             namespaceReady && clusterReady,
		"pod_identity_present":              podReady,
		"audit_operation_observed":          hasAudit,
		"sanitized_audit_result_observed":   resultObserved,
		"kubeconfig_binding_performed":      false,
		"namespace_scoped_kubeconfig_bound": false,
		"kubernetes_client_created":         false,
		"token_subject_review_performed":    false,
		"rbac_read_logs_review_performed":   false,
		"kubernetes_api_call":               false,
		"argocd_api_call":                   false,
		"kubectl_command_invoked":           false,
		"log_stream_opened":                 false,
		"log_body_included":                 false,
		"redacted_log_body_included":        false,
		"external_call_made":                false,
		"contains_kubeconfig":               false,
		"contains_cluster_token":            false,
		"contains_authorization_header":     false,
		"contains_log_body":                 false,
		"contains_raw_kubernetes_response":  false,
		"required_controls":                 []string{"operator_approval", "namespace_scoped_kubeconfig_secret", "token_subject_review", "rbac_read_logs_review", "namespace_confirmation", "pod_identity_confirmation", "result_redaction_review"},
		"disabled_backends":                 []string{"kubeconfig_secret_binding", "kubernetes_client_create", "token_subject_review", "rbac_review", "kubernetes_pod_log_api", "kubectl_logs", "argocd_pod_logs"},
		"binding_blockers":                  []string{"kubeconfig_secret_binding_not_configured", "namespace_scoped_kubeconfig_not_bound", "token_subject_review_not_performed", "rbac_read_logs_review_not_performed", "live_log_backend_disabled"},
		"readiness_sequence":                []string{"review_deployment_target_namespace", "approve_pod_log_audit_request", "bind_namespace_scoped_kubeconfig_secret", "review_token_subject", "review_rbac_logs_permission", "create_kubernetes_client", "open_live_log_stream", "record_redacted_log_result"},
		"suppressed_fields":                 []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret"},
		"message":                           "Namespace-scoped kubeconfig binding is readiness metadata only; no kubeconfig secret is read, no Kubernetes client is created, and no pod log stream is opened.",
	}
}

func podLogPlanStatus(ready bool) string {
	if ready {
		return "planned"
	}
	return "blocked"
}

func rollbackPointReadinessSQL(limit int) string {
	if limit <= 0 {
		limit = 20
	}
	// Keep rollback_execution_plan in sync with rollbackExecutionPlan; tests lock
	// the redacted preview contract because this SQL feeds API/context surfaces.
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
			END AS rollback_readiness_reason,
			jsonb_build_object(
				'mode', 'redacted_rollback_execution_plan',
				'plan_state', 'blocked',
				'prerequisite_state', CASE WHEN COALESCE(rp.status, '')='available' AND COALESCE(rp.revision, '')<>'' THEN 'metadata_available' ELSE 'metadata_blocked' END,
				'plan_ready', false,
				'plan_ready_reason', 'rollback_execution_backend_disabled',
				'execution_enabled', false,
				'execution_mode', 'read_only_preview',
				'requires_approval', true,
				'approval_action', 'deployment.rollback',
				'requires_environment_review', true,
				'requires_kubeconfig_binding', true,
				'requires_revision_verification', true,
				'requires_manifest_diff', true,
				'requires_dry_run_preflight', true,
				'requires_operator_confirmation', true,
				'rollback_request_materialized', false,
				'revision_verified', false,
				'manifest_diff_rendered', false,
				'dry_run_performed', false,
				'kubernetes_client_constructed', false,
				'helm_rollback_invoked', false,
				'kubectl_rollout_invoked', false,
				'argocd_rollback_invoked', false,
				'rollback_started', false,
				'external_call_made', false,
				'kubernetes_api_call_made', false,
				'helm_command_invoked', false,
				'rollback_mutation', 'disabled',
				'kubeconfig_included', false,
				'secret_included', false,
				'manifest_body_included', false,
				'helm_values_included', false,
				'cluster_credential_included', false,
				'revision_value_included', false,
				'contains_token', false,
				'contains_kubeconfig', false,
				'contains_secret', false,
				'contains_manifest_body', false,
				'rollback_boundary_redacted', true,
				'blocked_reasons', jsonb_build_array('rollback_execution_backend_disabled', 'rollback_mutation_not_armed'),
				'required_controls', jsonb_build_array('operation_approval', 'environment_review', 'kubeconfig_binding', 'revision_verification', 'manifest_diff', 'server_side_dry_run', 'operator_confirmation'),
				'disabled_backends', jsonb_build_array('helm_rollback', 'kubectl_rollout_undo', 'argocd_rollback', 'rollback_execute'),
				'suppressed_fields', jsonb_build_array('kubeconfig', 'cluster_token', 'authorization_header', 'secret_manifest', 'rendered_manifest', 'helm_values', 'image_pull_secret', 'environment_secret', 'revision_value'),
				'execution_sequence', jsonb_build_array('request_approval', 'bind_environment', 'bind_kubeconfig', 'verify_revision', 'render_manifest_diff', 'run_server_side_dry_run', 'record_rollback_audit', 'start_rollback')
			) AS rollback_execution_plan
		FROM rollback_points rp
		LEFT JOIN deployment_targets dt ON dt.id=rp.deployment_target_id
		LEFT JOIN deployment_records dr ON dr.id=rp.deployment_record_id
		WHERE rp.project_id=$1
		ORDER BY rp.captured_at DESC
		LIMIT %d`, limit)
}

func enrichDeploymentTargetsWithExecutionReadiness(rows []map[string]any) {
	for _, row := range rows {
		row["deployment_execution_readiness"] = deploymentExecutionReadiness(row)
	}
}

func deploymentExecutionReadiness(row map[string]any) map[string]any {
	healthStatus := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["status"])))
	cluster := strings.TrimSpace(fmt.Sprint(row["cluster_name"]))
	namespace := strings.TrimSpace(fmt.Sprint(row["namespace"]))
	appCount := intFromAny(row["argo_app_count"], -1)
	blockedReasons := make([]string, 0)
	if deploymentTargetStatusBlocksExecution(healthStatus) {
		blockedReasons = append(blockedReasons, "deployment target status needs review before execution")
	}
	if cluster == "" || cluster == "<nil>" {
		blockedReasons = append(blockedReasons, "cluster name is missing")
	}
	if namespace == "" || namespace == "<nil>" {
		blockedReasons = append(blockedReasons, "namespace is missing")
	}
	if appCount == 0 {
		blockedReasons = append(blockedReasons, "no Argo apps are linked to this deployment target")
	}
	readiness := "planned"
	message := "Deployment execution dry-run plan is ready; Helm/k8s execution remains disabled."
	if len(blockedReasons) > 0 {
		readiness = "blocked"
		message = "Deployment execution cannot be planned until target metadata and health are reviewed."
	}
	executionPlan := deploymentExecutionPlan(readiness, blockedReasons)
	return map[string]any{
		"status":             readiness,
		"mode":               "dry_run",
		"execution_enabled":  false,
		"external_call_made": false,
		"requires_approval":  true,
		"approval_action":    "deployment.execute",
		"execution_backend":  "disabled",
		"blocked_reasons":    blockedReasons,
		"execution_plan":     executionPlan,
		"steps": []map[string]any{
			{"name": "validate_target", "status": "planned", "execution": false},
			{"name": "render_manifest", "status": "planned", "execution": false},
			{"name": "helm_or_kubectl_preflight", "status": "planned", "execution": false},
			{"name": "rollout", "status": "planned", "execution": false},
		},
		"message": message,
	}
}

func deploymentExecutionPlan(readiness string, blockedReasons []string) map[string]any {
	prerequisiteState := strings.ToLower(strings.TrimSpace(readiness))
	if prerequisiteState != "planned" {
		prerequisiteState = "blocked"
	}
	return map[string]any{
		"mode":                            "redacted_deployment_execution_plan",
		"plan_state":                      "blocked",
		"prerequisite_state":              prerequisiteState,
		"plan_ready":                      false,
		"plan_ready_reason":               "deployment_execution_backend_disabled",
		"execution_enabled":               false,
		"execution_backend":               "disabled",
		"requires_approval":               true,
		"approval_action":                 "deployment.execute",
		"requires_environment_review":     true,
		"requires_kubeconfig_binding":     true,
		"requires_manifest_render":        true,
		"requires_dry_run_preflight":      true,
		"requires_rollback_plan":          true,
		"requires_operator_confirmation":  true,
		"target_metadata_ready":           prerequisiteState == "planned",
		"deployment_request_materialized": false,
		"manifest_rendered":               false,
		"dry_run_performed":               false,
		"helm_release_bound":              false,
		"kubernetes_client_constructed":   false,
		"rollout_started":                 false,
		"rollback_point_selected":         false,
		"external_call_made":              false,
		"kubernetes_api_call_made":        false,
		"helm_command_invoked":            false,
		"deployment_mutation":             "disabled",
		"kubeconfig_included":             false,
		"secret_included":                 false,
		"manifest_body_included":          false,
		"helm_values_included":            false,
		"cluster_credential_included":     false,
		"contains_token":                  false,
		"contains_kubeconfig":             false,
		"contains_secret":                 false,
		"contains_manifest_body":          false,
		"execution_boundary_redacted":     true,
		"blocked_reasons":                 append([]string{"deployment_execution_backend_disabled"}, blockedReasons...),
		"required_controls":               []string{"operation_approval", "environment_review", "kubeconfig_binding", "manifest_render", "server_side_dry_run", "rollback_plan", "operator_confirmation"},
		"disabled_backends":               []string{"helm_upgrade", "kubectl_apply", "kubectl_rollout", "argocd_sync", "rollback_execute"},
		"suppressed_fields":               []string{"kubeconfig", "cluster_token", "authorization_header", "secret_manifest", "rendered_manifest", "helm_values", "image_pull_secret", "environment_secret"},
		"execution_sequence":              []string{"request_approval", "bind_environment", "bind_kubeconfig", "render_manifest", "run_server_side_dry_run", "record_deployment_audit", "start_rollout"},
	}
}

func deploymentTargetStatusBlocksExecution(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "<nil>", "healthy", "synced", "running", "available", "active", "ok", "completed":
		return false
	case "failed", "error", "degraded", "outofsync", "missing", "unknown":
		return true
	default:
		return true
	}
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

func (s *Server) getSSHMachineRehearsal(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	machine, err := queryOne(r.Context(), s.store.DB, `
		SELECT id, project_id, name, host, port, username, auth_type, metadata, created_at, updated_at
		FROM ssh_machines
		WHERE id=$1`, machineID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanPreviewString(machine["project_id"])
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "SSH machine has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: projectID}, "read") {
		return
	}
	runs, err := queryMaps(r.Context(), s.store.DB, `
		SELECT scr.id, scr.status, scr.exit_code, scr.created_at, scr.finished_at, op.operation_type
		FROM ssh_command_runs scr
		LEFT JOIN operation_runs op ON op.id=scr.operation_run_id
		WHERE scr.ssh_machine_id=$1
		ORDER BY scr.created_at DESC
		LIMIT 50`, machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load SSH rehearsal evidence")
		return
	}
	proofEvidence, err := sshMachineTargetEnvironmentProofEvidence(r.Context(), s.store.DB, machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load SSH target environment proof evidence")
		return
	}
	writeJSON(w, http.StatusOK, buildSSHMachineRehearsalPreview(machine, runs, proofEvidence))
}

func (s *Server) recordSSHMachineTargetEnvironmentProof(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	machine, err := sshMachineForRehearsalSnapshot(r.Context(), s.store.DB, machineID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanPreviewString(machine["project_id"])
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "SSH machine has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordSSHMachineTargetEnvironmentProof(r.Context(), s.store, SSHMachineTargetEnvironmentProofOptions{
		MachineID: machineID,
		DryRun:    req.DryRun,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("ssh target environment proof failed", "ssh_machine_id", machineID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record SSH target environment proof failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordSSHMachineRehearsalSnapshot(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	machine, err := sshMachineForRehearsalSnapshot(r.Context(), s.store.DB, machineID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanPreviewString(machine["project_id"])
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "SSH machine has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordSSHMachineRehearsalSnapshot(r.Context(), s.store, SSHMachineRehearsalSnapshotOptions{
		MachineID: machineID,
		DryRun:    req.DryRun,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("ssh rehearsal snapshot failed", "ssh_machine_id", machineID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record SSH rehearsal snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func buildSSHMachineRehearsalPreview(machine map[string]any, runs []map[string]any, proofEvidenceOpt ...map[string]any) map[string]any {
	host := cleanPreviewString(machine["host"])
	username := cleanPreviewString(machine["username"])
	authType := cleanPreviewString(machine["auth_type"])
	portValue := intFromAny(machine["port"], 22)
	metadata := mapFromAny(machine["metadata"])
	hasKeyReference := cleanPreviewString(metadata["key_path"]) != ""
	hasKnownHostsReference := cleanPreviewString(metadata["known_hosts_path"]) != ""
	strictHostKeyChecking := cleanPreviewString(metadata["strict_host_key_checking"])
	if strictHostKeyChecking == "" {
		strictHostKeyChecking = "accept-new"
	}
	metadataReady := host != "" && username != "" && authType != "" && portValue > 0 && portValue <= 65535

	evidence := summarizeSSHRehearsalEvidence(runs)
	hasVerified := boolOnlyFromAny(evidence["completed_verify"])
	hasExecuted := boolOnlyFromAny(evidence["completed_exec"])
	state := "planned"
	if !metadataReady {
		state = "blocked"
	} else if hasVerified && hasExecuted {
		state = "ready"
	} else if intFromAny(evidence["total_runs"], 0) > 0 {
		state = "partial"
	}

	verifyStatus := "planned"
	if !metadataReady {
		verifyStatus = "blocked"
	} else if hasVerified {
		verifyStatus = "completed"
	}
	execStatus := "blocked"
	if hasExecuted {
		execStatus = "completed"
	} else if hasVerified {
		execStatus = "planned"
	}

	requiredLiveRehearsal := []string{}
	if !hasVerified {
		requiredLiveRehearsal = append(requiredLiveRehearsal, "ssh.verify")
	}
	if !hasExecuted {
		requiredLiveRehearsal = append(requiredLiveRehearsal, "ssh.exec")
	}
	approvalPlan := sshRehearsalApprovalRequestPlan(metadataReady, hasVerified, hasExecuted)
	resultRecordingPlan := sshRehearsalResultRecordingPlan(evidence)
	authBindingPlan := sshRehearsalAuthBindingPlan(metadataReady, authType, hasKeyReference, hasKnownHostsReference)
	verifyPlan := sshRehearsalVerifyExecutionPlan(metadataReady, hasVerified)
	execPlan := sshRehearsalExecExecutionPlan(metadataReady, hasVerified, hasExecuted)
	liveControlEvidence := sshRehearsalLiveControlEvidence(metadata, metadataReady, hasVerified, hasExecuted)
	environmentProofPlan := sshRehearsalEnvironmentProofPlan(metadata, metadataReady, hasVerified, hasExecuted, liveControlEvidence, evidence)
	targetEnvironmentAttestationPlan := sshTargetEnvironmentAttestationPlan(metadataReady, liveControlEvidence, environmentProofPlan, evidence)
	proofEvidence := map[string]any{
		"mode":                    "ssh_target_environment_proof_registration",
		"proof_state":             "not_recorded",
		"proof_registered":        false,
		"asset_status_observed":   false,
		"external_call_made":      false,
		"ssh_process_started":     false,
		"command_executed":        false,
		"raw_output_recorded":     false,
		"private_key_included":    false,
		"operator_identity_saved": false,
	}
	if len(proofEvidenceOpt) > 0 && proofEvidenceOpt[0] != nil {
		proofEvidence = proofEvidenceOpt[0]
	}

	steps := []map[string]any{
		{
			"kind":   "machine_metadata",
			"status": statusWhen(metadataReady),
			"checks": []string{"host", "port", "username", "auth_type"},
			"reason": reasonWhen(metadataReady, "machine metadata is complete", "host, username, auth_type, and valid port are required"),
		},
		{
			"kind":   "auth_material_reference",
			"status": statusWhen(authType != ""),
			"checks": []string{"auth_type", "runtime_secret_binding"},
			"reason": reasonWhen(authType != "", "auth material must be resolved by the runtime worker", "auth_type is required before a live SSH rehearsal"),
		},
		{
			"kind":   "known_hosts_policy",
			"status": "planned",
			"checks": []string{"known_hosts_reference", "strict_host_key_checking"},
			"reason": map[string]any{
				"known_hosts_reference_present": hasKnownHostsReference,
				"strict_host_key_checking":      strictHostKeyChecking,
			},
		},
		{
			"kind":   "verify_rehearsal",
			"status": verifyStatus,
			"checks": []string{"POST /api/ssh-machines/{id}/verify", "ssh.verify operation evidence"},
			"reason": reasonWhen(metadataReady, "verify can be queued after operator approval and runtime auth binding", "machine metadata is incomplete"),
		},
		{
			"kind":   "exec_rehearsal",
			"status": execStatus,
			"checks": []string{"POST /api/ssh-machines/{id}/commands", "ssh.exec operation evidence", "operator command review"},
			"reason": reasonWhen(hasVerified || hasExecuted, "exec rehearsal can follow a successful verify rehearsal", "complete ssh.verify evidence first"),
		},
		{
			"kind":   "live_rehearsal_controls",
			"status": liveControlEvidence["control_state"],
			"checks": []string{"authorized_machine_fixture", "live_rehearsal_runbook", "operator_approval_proof"},
			"reason": liveControlEvidence["control_ready_reason"],
		},
	}

	return map[string]any{
		"mode":                                   "ssh_rehearsal_plan_preview",
		"rehearsal_state":                        state,
		"execution_enabled":                      false,
		"external_call_made":                     false,
		"ssh_process_started":                    false,
		"command_executed":                       false,
		"stdout_included":                        false,
		"stderr_included":                        false,
		"private_key_included":                   false,
		"known_hosts_included":                   false,
		"secret_included":                        false,
		"live_evidence_recorded":                 boolOnlyFromAny(evidence["has_live_evidence"]),
		"sanitized_result_recorded":              cleanPreviewString(evidence["evidence_state"]) == "recorded",
		"result_recording_state":                 resultRecordingPlan["recording_state"],
		"auth_reference_present":                 hasKeyReference || authType != "",
		"known_hosts_configured":                 hasKnownHostsReference,
		"approval_request_plan":                  approvalPlan,
		"auth_binding_plan":                      authBindingPlan,
		"verify_execution_plan":                  verifyPlan,
		"exec_execution_plan":                    execPlan,
		"result_recording_plan":                  resultRecordingPlan,
		"live_rehearsal_control_evidence":        liveControlEvidence,
		"live_rehearsal_controls_ready":          liveControlEvidence["controls_ready"],
		"environment_proof_plan":                 environmentProofPlan,
		"environment_proof_ready":                environmentProofPlan["environment_proof_ready"],
		"target_environment_attestation_plan":    targetEnvironmentAttestationPlan,
		"target_environment_attestation_ready":   targetEnvironmentAttestationPlan["attestation_ready_for_review"],
		"target_environment_proof_registration":  proofEvidence,
		"target_environment_proof_registered":    boolOnlyFromAny(proofEvidence["proof_registered"]),
		"target_environment_proof_state":         cleanPreviewString(proofEvidence["proof_state"]),
		"target_environment_proof_registered_at": proofEvidence["proof_registered_at"],
		"operator_approved_proof_recorded":       liveControlEvidence["operator_approval_recorded"],
		"required_live_rehearsal":                requiredLiveRehearsal,
		"required_controls": []string{
			"machine_metadata_review",
			"ssh_auth_material_binding",
			"known_hosts_review",
			"operation_approval",
			"operator_command_review",
			"live_rehearsal_runbook",
			"authorized_machine_fixture",
		},
		"suppressed_fields": []string{
			"private_key",
			"passphrase",
			"known_hosts_body",
			"stdout",
			"stderr",
			"raw_error",
			"command_output",
			"runbook_url",
			"runbook_path",
			"fixture_id",
			"fixture_name",
			"operator_approved_by",
			"operator_approval_note",
		},
		"execution_blockers": approvalPlan["execution_blockers"],
		"machine": map[string]any{
			"id":         machine["id"],
			"project_id": machine["project_id"],
			"name":       machine["name"],
			"host":       host,
			"port":       portValue,
			"username":   username,
			"auth_type":  authType,
		},
		"steps":           steps,
		"recent_evidence": evidence,
	}
}

func sshTargetEnvironmentAttestationPlan(metadataReady bool, controlEvidence, environmentProofPlan, evidence map[string]any) map[string]any {
	runbookRecorded := boolOnlyFromAny(controlEvidence["runbook_reference_recorded"])
	fixtureRecorded := boolOnlyFromAny(controlEvidence["fixture_reference_recorded"])
	operatorApprovalRecorded := boolOnlyFromAny(controlEvidence["operator_approval_recorded"])
	targetEnvironmentProofObserved := boolOnlyFromAny(environmentProofPlan["target_environment_reference_recorded"]) &&
		boolOnlyFromAny(environmentProofPlan["operator_environment_proof_recorded"])
	verifyObserved := boolOnlyFromAny(environmentProofPlan["completed_verify_evidence"])
	execObserved := boolOnlyFromAny(environmentProofPlan["completed_exec_evidence"])
	sanitizedResultRecorded := boolOnlyFromAny(environmentProofPlan["sanitized_result_recorded"]) &&
		cleanPreviewString(evidence["evidence_state"]) == "recorded"
	readyForReview := metadataReady && runbookRecorded && fixtureRecorded && operatorApprovalRecorded &&
		targetEnvironmentProofObserved && verifyObserved && execObserved && sanitizedResultRecorded

	attestationState := "blocked"
	readyReason := "ssh_target_environment_attestation_machine_metadata_incomplete"
	switch {
	case readyForReview:
		attestationState = "ready_for_operator_review"
		readyReason = "ssh_target_environment_attestation_ready_for_operator_review"
	case metadataReady && (runbookRecorded || fixtureRecorded || operatorApprovalRecorded || targetEnvironmentProofObserved || verifyObserved || execObserved || sanitizedResultRecorded):
		attestationState = "partial"
		readyReason = "ssh_target_environment_attestation_incomplete"
	case metadataReady:
		attestationState = "planned"
		readyReason = "ssh_target_environment_attestation_not_recorded"
	}

	blockedReasons := []string{}
	if !metadataReady {
		blockedReasons = append(blockedReasons, "machine_metadata_incomplete")
	}
	if !runbookRecorded {
		blockedReasons = append(blockedReasons, "runbook_reference_not_recorded")
	}
	if !fixtureRecorded {
		blockedReasons = append(blockedReasons, "authorized_machine_fixture_not_recorded")
	}
	if !operatorApprovalRecorded {
		blockedReasons = append(blockedReasons, "operator_approval_proof_not_recorded")
	}
	if !targetEnvironmentProofObserved {
		blockedReasons = append(blockedReasons, "target_environment_proof_not_recorded")
	}
	if !verifyObserved {
		blockedReasons = append(blockedReasons, "completed_ssh_verify_not_recorded")
	}
	if !execObserved {
		blockedReasons = append(blockedReasons, "completed_ssh_exec_not_recorded")
	}
	if !sanitizedResultRecorded {
		blockedReasons = append(blockedReasons, "sanitized_ssh_result_not_recorded")
	}

	return map[string]any{
		"mode":                                "ssh_target_environment_attestation_plan",
		"attestation_state":                   attestationState,
		"attestation_ready_for_review":        readyForReview,
		"attestation_ready_reason":            readyReason,
		"machine_metadata_ready":              metadataReady,
		"runbook_reference_observed":          runbookRecorded,
		"authorized_machine_fixture_observed": fixtureRecorded,
		"operator_approval_proof_observed":    operatorApprovalRecorded,
		"target_environment_proof_observed":   targetEnvironmentProofObserved,
		"verify_result_observed":              verifyObserved,
		"exec_result_observed":                execObserved,
		"sanitized_result_recorded":           sanitizedResultRecorded,
		"environment_probe_performed":         false,
		"ssh_process_started":                 false,
		"ssh_verify_executed":                 false,
		"ssh_exec_executed":                   false,
		"raw_output_recorded":                 false,
		"operator_identity_recorded":          false,
		"key_material_included":               false,
		"external_call_made":                  false,
		"required_attestation_fields":         []string{"runbook_reference", "authorized_machine_fixture", "operator_approval_proof", "target_environment_proof", "completed_verify", "completed_exec", "sanitized_result_recording"},
		"disabled_backends":                   []string{"environment_probe", "ssh_process_start", "ssh_verify_execute", "ssh_exec_execute", "raw_output_recording", "operator_identity_recording"},
		"suppressed_fields":                   []string{"runbook_url", "runbook_path", "environment_identifier", "fixture_identifier", "operator_identity", "operator_notes", "ssh_host", "ssh_user", "ssh_key_material", "command", "stdout", "stderr", "raw_output", "known_hosts"},
		"blocked_reasons":                     blockedReasons,
		"message":                             "Target environment attestation is a redacted operator-review preflight only; it does not probe the environment, start SSH, execute verify/exec, record raw output, or include operator identity.",
	}
}

func sshRehearsalEnvironmentProofPlan(metadata map[string]any, metadataReady, hasVerified, hasExecuted bool, controlEvidence, evidence map[string]any) map[string]any {
	environmentReferenceRecorded := firstNonEmptyString(
		stringFromMap(metadata, "live_rehearsal_environment"),
		stringFromMap(metadata, "rehearsal_environment"),
		stringFromMap(metadata, "authorized_environment"),
		stringFromMap(metadata, "target_environment"),
		stringFromMap(metadata, "environment_id"),
	) != ""
	operatorEnvironmentProofRecorded := boolOnlyFromAny(metadata["operator_environment_approved"]) ||
		boolOnlyFromAny(metadata["environment_proof_recorded"]) ||
		firstNonEmptyString(
			stringFromMap(metadata, "operator_environment_approval_id"),
			stringFromMap(metadata, "operator_environment_approved_at"),
			stringFromMap(metadata, "operator_environment_proof_id"),
			stringFromMap(metadata, "environment_proof_id"),
			stringFromMap(metadata, "environment_proof_recorded_at"),
		) != ""
	controlsReady := boolOnlyFromAny(controlEvidence["controls_ready"])
	sanitizedResultRecorded := cleanPreviewString(evidence["evidence_state"]) == "recorded"
	environmentProofReady := metadataReady && controlsReady && environmentReferenceRecorded && operatorEnvironmentProofRecorded && hasVerified && hasExecuted && sanitizedResultRecorded
	proofState := "blocked"
	proofReason := "ssh_environment_proof_machine_metadata_incomplete"
	switch {
	case environmentProofReady:
		proofState = "ready"
		proofReason = "authorized_machine_environment_proof_ready"
	case metadataReady && (environmentReferenceRecorded || operatorEnvironmentProofRecorded || controlsReady || hasVerified || hasExecuted):
		proofState = "partial"
		proofReason = "authorized_machine_environment_proof_incomplete"
	case metadataReady:
		proofState = "planned"
		proofReason = "authorized_machine_environment_proof_not_recorded"
	}
	missing := []string{}
	if !metadataReady {
		missing = append(missing, "machine_metadata")
	}
	if !environmentReferenceRecorded {
		missing = append(missing, "target_environment_reference")
	}
	if !boolOnlyFromAny(controlEvidence["fixture_reference_recorded"]) {
		missing = append(missing, "authorized_machine_fixture")
	}
	if !boolOnlyFromAny(controlEvidence["operator_approval_recorded"]) {
		missing = append(missing, "operator_approval_proof")
	}
	if !operatorEnvironmentProofRecorded {
		missing = append(missing, "operator_environment_proof")
	}
	if !hasVerified {
		missing = append(missing, "completed_ssh_verify")
	}
	if !hasExecuted {
		missing = append(missing, "completed_ssh_exec")
	}
	if !sanitizedResultRecorded {
		missing = append(missing, "sanitized_ssh_result_recorded")
	}
	return map[string]any{
		"mode":                                  "ssh_rehearsal_environment_proof_plan",
		"environment_proof_state":               proofState,
		"environment_proof_ready":               environmentProofReady,
		"environment_proof_ready_reason":        proofReason,
		"machine_metadata_ready":                metadataReady,
		"target_environment_reference_recorded": environmentReferenceRecorded,
		"authorized_fixture_recorded":           boolOnlyFromAny(controlEvidence["fixture_reference_recorded"]),
		"operator_approval_recorded":            boolOnlyFromAny(controlEvidence["operator_approval_recorded"]),
		"operator_environment_proof_recorded":   operatorEnvironmentProofRecorded,
		"completed_verify_evidence":             hasVerified,
		"completed_exec_evidence":               hasExecuted,
		"sanitized_result_recorded":             sanitizedResultRecorded,
		"external_call_made":                    false,
		"ssh_process_started":                   false,
		"command_executed":                      false,
		"environment_probe_performed":           false,
		"operator_identity_included":            false,
		"operator_note_included":                false,
		"fixture_identifier_included":           false,
		"environment_identifier_included":       false,
		"stdout_included":                       false,
		"stderr_included":                       false,
		"private_key_included":                  false,
		"known_hosts_included":                  false,
		"runtime_secret_included":               false,
		"required_evidence":                     []string{"target_environment_reference", "authorized_machine_fixture", "operator_approval_proof", "operator_environment_proof", "completed_ssh_verify", "completed_ssh_exec", "sanitized_ssh_result_recorded"},
		"missing_evidence":                      missing,
		"disabled_backends":                     []string{"environment_probe", "ssh_process_start", "ssh_verify_execute", "ssh_exec_execute", "raw_output_recording", "operator_identity_recording"},
		"suppressed_fields":                     []string{"live_rehearsal_environment", "rehearsal_environment", "authorized_environment", "target_environment", "environment_id", "authorized_machine_fixture", "authorized_fixture_id", "fixture_id", "fixture_name", "operator_approved_by", "operator_approval_note", "operator_environment_approval_id", "operator_environment_approved_at", "operator_environment_proof_id", "environment_proof_id", "environment_proof_recorded_at", "private_key", "passphrase", "known_hosts_body", "stdout", "stderr", "raw_error", "runtime_secret"},
		"message":                               "SSH environment proof is reconciled as booleans only; target environment identifiers, fixture identifiers, operator identity, auth material, and command output remain suppressed.",
	}
}

func sshRehearsalLiveControlEvidence(metadata map[string]any, metadataReady, hasVerified, hasExecuted bool) map[string]any {
	runbookRecorded := firstNonEmptyString(
		stringFromMap(metadata, "live_rehearsal_runbook"),
		stringFromMap(metadata, "rehearsal_runbook"),
		stringFromMap(metadata, "runbook_url"),
		stringFromMap(metadata, "runbook_path"),
	) != ""
	fixtureRecorded := firstNonEmptyString(
		stringFromMap(metadata, "authorized_machine_fixture"),
		stringFromMap(metadata, "authorized_fixture_id"),
		stringFromMap(metadata, "fixture_id"),
		stringFromMap(metadata, "fixture_name"),
	) != ""
	operatorApprovalRecorded := boolOnlyFromAny(metadata["operator_approved"]) ||
		firstNonEmptyString(
			stringFromMap(metadata, "operator_approval_id"),
			stringFromMap(metadata, "operator_approved_at"),
			stringFromMap(metadata, "operator_approved_by"),
		) != ""
	controlsReady := metadataReady && hasVerified && hasExecuted && runbookRecorded && fixtureRecorded && operatorApprovalRecorded
	controlState := "blocked"
	controlReadyReason := "ssh_live_rehearsal_machine_metadata_incomplete"
	switch {
	case controlsReady:
		controlState = "ready"
		controlReadyReason = "authorized_machine_live_rehearsal_controls_recorded"
	case metadataReady && (runbookRecorded || fixtureRecorded || operatorApprovalRecorded || hasVerified || hasExecuted):
		controlState = "partial"
		controlReadyReason = "authorized_machine_live_rehearsal_controls_incomplete"
	case metadataReady:
		controlState = "planned"
		controlReadyReason = "authorized_machine_live_rehearsal_controls_not_recorded"
	}
	missing := []string{}
	if !metadataReady {
		missing = append(missing, "machine_metadata")
	}
	if !runbookRecorded {
		missing = append(missing, "live_rehearsal_runbook")
	}
	if !fixtureRecorded {
		missing = append(missing, "authorized_machine_fixture")
	}
	if !operatorApprovalRecorded {
		missing = append(missing, "operator_approval_proof")
	}
	if !hasVerified {
		missing = append(missing, "completed_ssh_verify")
	}
	if !hasExecuted {
		missing = append(missing, "completed_ssh_exec")
	}
	return map[string]any{
		"mode":                        "ssh_live_rehearsal_control_evidence",
		"control_state":               controlState,
		"controls_ready":              controlsReady,
		"control_ready_reason":        controlReadyReason,
		"machine_metadata_ready":      metadataReady,
		"runbook_reference_recorded":  runbookRecorded,
		"fixture_reference_recorded":  fixtureRecorded,
		"operator_approval_recorded":  operatorApprovalRecorded,
		"completed_verify_evidence":   hasVerified,
		"completed_exec_evidence":     hasExecuted,
		"external_call_made":          false,
		"ssh_process_started":         false,
		"command_executed":            false,
		"contains_runbook_body":       false,
		"contains_fixture_identifier": false,
		"contains_operator_identity":  false,
		"contains_operator_note":      false,
		"contains_private_key":        false,
		"contains_known_hosts_body":   false,
		"contains_stdout":             false,
		"contains_stderr":             false,
		"required_evidence":           []string{"live_rehearsal_runbook", "authorized_machine_fixture", "operator_approval_proof", "completed_ssh_verify", "completed_ssh_exec"},
		"missing_evidence":            missing,
		"suppressed_fields":           []string{"live_rehearsal_runbook", "rehearsal_runbook", "runbook_url", "runbook_path", "runbook_body", "authorized_machine_fixture", "authorized_fixture_id", "fixture_id", "fixture_name", "operator_approved", "operator_approval_id", "operator_approved_by", "operator_approved_at", "operator_approval_note", "private_key", "passphrase", "known_hosts_body", "stdout", "stderr", "raw_error", "runtime_secret"},
		"message":                     "SSH live rehearsal controls are reconciled from metadata as booleans only; runbook, fixture, operator identity, auth material, and command output remain suppressed.",
	}
}

func sshRehearsalAuthBindingPlan(metadataReady bool, authType string, hasKeyReference, hasKnownHostsReference bool) map[string]any {
	bindingState := "blocked"
	if metadataReady {
		bindingState = "planned"
	}
	blockedReasons := []string{"runtime_auth_binding_not_performed"}
	if !metadataReady {
		blockedReasons = append(blockedReasons, "machine_metadata_incomplete")
	}
	if strings.TrimSpace(authType) == "" {
		blockedReasons = append(blockedReasons, "auth_type_missing")
	}
	return map[string]any{
		"mode":                          "ssh_rehearsal_auth_binding_plan",
		"binding_state":                 bindingState,
		"metadata_ready":                metadataReady,
		"auth_type_configured":          strings.TrimSpace(authType) != "",
		"key_reference_present":         hasKeyReference,
		"known_hosts_reference_present": hasKnownHostsReference,
		"runtime_auth_bound":            false,
		"known_hosts_bound":             false,
		"ssh_client_configured":         false,
		"external_call_made":            false,
		"contains_private_key":          false,
		"contains_passphrase":           false,
		"contains_known_hosts_body":     false,
		"contains_runtime_secret":       false,
		"required_controls":             []string{"runtime_secret_binding", "known_hosts_review", "strict_host_key_policy", "operator_auth_review"},
		"disabled_backends":             []string{"runtime_auth_binding", "known_hosts_materialization", "ssh_client_configure"},
		"suppressed_fields":             []string{"private_key", "passphrase", "known_hosts_body", "runtime_secret", "secret_env"},
		"blocked_reasons":               blockedReasons,
		"execution_blockers":            []string{"runtime_auth_binding_not_approved", "runtime_auth_binding_not_performed"},
		"message":                       "SSH auth binding is planned only; no private key, passphrase, known_hosts body, runtime secret, or SSH client is materialized.",
	}
}

func sshRehearsalVerifyExecutionPlan(metadataReady, hasVerified bool) map[string]any {
	verifyState := "blocked"
	if metadataReady {
		verifyState = "planned"
	}
	if hasVerified {
		verifyState = "observed"
	}
	blockedReasons := []string{"ssh_verify_not_performed"}
	if !metadataReady {
		blockedReasons = append(blockedReasons, "machine_metadata_incomplete")
	}
	return map[string]any{
		"mode":                      "ssh_rehearsal_verify_execution_plan",
		"verify_state":              verifyState,
		"metadata_ready":            metadataReady,
		"completed_verify_evidence": hasVerified,
		"operation_enqueued":        false,
		"worker_job_created":        false,
		"ssh_process_started":       false,
		"verify_command_executed":   false,
		"exit_code_recorded":        false,
		"external_call_made":        false,
		"stdout_included":           false,
		"stderr_included":           false,
		"raw_error_included":        false,
		"contains_private_key":      false,
		"contains_runtime_secret":   false,
		"required_controls":         []string{"operation_approval", "runtime_auth_binding", "known_hosts_review", "connectivity_timeout_policy"},
		"disabled_backends":         []string{"worker_job_create", "ssh_process_start", "ssh_verify_execute", "ssh_result_write"},
		"suppressed_fields":         []string{"private_key", "passphrase", "known_hosts_body", "stdout", "stderr", "raw_error", "runtime_secret"},
		"blocked_reasons":           blockedReasons,
		"execution_blockers":        []string{"ssh_process_backend_disabled", "ssh_verify_not_performed"},
		"message":                   "SSH verify rehearsal is planned only; no SSH process, command execution, output, or result row is produced.",
	}
}

func sshRehearsalExecExecutionPlan(metadataReady, hasVerified, hasExecuted bool) map[string]any {
	execState := "blocked"
	if metadataReady && hasVerified {
		execState = "planned"
	}
	if hasExecuted {
		execState = "observed"
	}
	blockedReasons := []string{"ssh_exec_not_performed"}
	if !metadataReady {
		blockedReasons = append(blockedReasons, "machine_metadata_incomplete")
	}
	if !hasVerified && !hasExecuted {
		blockedReasons = append(blockedReasons, "ssh_verify_evidence_missing")
	}
	return map[string]any{
		"mode":                      "ssh_rehearsal_exec_execution_plan",
		"exec_state":                execState,
		"metadata_ready":            metadataReady,
		"completed_verify_evidence": hasVerified,
		"completed_exec_evidence":   hasExecuted,
		"operation_enqueued":        false,
		"worker_job_created":        false,
		"ssh_process_started":       false,
		"command_reviewed":          false,
		"command_executed":          false,
		"exit_code_recorded":        false,
		"external_call_made":        false,
		"stdout_included":           false,
		"stderr_included":           false,
		"raw_error_included":        false,
		"contains_command":          false,
		"contains_private_key":      false,
		"contains_runtime_secret":   false,
		"required_controls":         []string{"operation_approval", "completed_verify_evidence", "operator_command_review", "output_redaction_review"},
		"disabled_backends":         []string{"worker_job_create", "ssh_process_start", "ssh_exec_execute", "ssh_result_write"},
		"suppressed_fields":         []string{"command", "stdout", "stderr", "raw_error", "private_key", "passphrase", "runtime_secret"},
		"blocked_reasons":           blockedReasons,
		"execution_blockers":        []string{"ssh_process_backend_disabled", "ssh_exec_not_performed"},
		"message":                   "SSH exec rehearsal is planned only; no command, SSH process, stdout, stderr, raw error, or result row is produced.",
	}
}

func sshRehearsalApprovalRequestPlan(metadataReady, hasVerified, hasExecuted bool) map[string]any {
	requestState := "planned"
	metadataBlockedReasons := []string{}
	if !metadataReady {
		requestState = "blocked"
		metadataBlockedReasons = append(metadataBlockedReasons, "machine_metadata_incomplete")
	}
	return map[string]any{
		"mode":                        "ssh_rehearsal_approval_request_plan",
		"request_state":               requestState,
		"request_ready":               false,
		"request_ready_reason":        "ssh_rehearsal_live_execution_disabled",
		"metadata_ready":              metadataReady,
		"completed_verify_evidence":   hasVerified,
		"completed_exec_evidence":     hasExecuted,
		"operation_created":           false,
		"approval_request_created":    false,
		"worker_job_created":          false,
		"runtime_auth_binding_queued": false,
		"ssh_process_started":         false,
		"external_call_made":          false,
		"required_approval_fields":    []string{"operation_run_id", "ssh_machine_id", "operation_type", "host", "port", "username", "auth_type", "requested_by", "reason"},
		"suppressed_fields":           []string{"private_key", "passphrase", "known_hosts_body", "command", "stdout", "stderr", "raw_error", "runtime_secret"},
		"blocked_reasons":             metadataBlockedReasons,
		"execution_blockers":          []string{"ssh_rehearsal_operation_not_created", "approval_policy_not_applied", "runtime_auth_binding_not_approved", "ssh_process_backend_disabled"},
	}
}

func sshRehearsalResultRecordingPlan(evidence map[string]any) map[string]any {
	totalRuns := intFromAny(evidence["total_runs"], 0)
	verifyRuns := intFromAny(evidence["verify_runs"], 0)
	execRuns := intFromAny(evidence["exec_runs"], 0)
	recordingState := cleanPreviewString(evidence["evidence_state"])
	if recordingState == "" {
		recordingState = "blocked"
	}
	recordingReady := totalRuns > 0
	recordingReason := "sanitized_ssh_result_recorded"
	blockedReasons := []string{}
	message := "SSH rehearsal has recorded sanitized command-run metadata only; command output, raw errors, and auth material remain suppressed."
	if !recordingReady {
		recordingState = "blocked"
		recordingReason = "ssh_rehearsal_execution_not_performed"
		blockedReasons = []string{"ssh_rehearsal_execution_not_performed", "sanitized_ssh_result_not_recorded", "canonical_asset_sync_not_performed"}
		message = "SSH rehearsal results are not recorded by this preview; future execution must persist sanitized metadata without command output or auth material."
	} else {
		switch recordingState {
		case "waiting_for_workers":
			blockedReasons = append(blockedReasons, "ssh_rehearsal_worker_result_pending")
			recordingReason = "ssh_rehearsal_worker_result_pending"
		case "failed":
			blockedReasons = append(blockedReasons, "ssh_rehearsal_failed_result_recorded")
			recordingReason = "ssh_rehearsal_failed_result_recorded"
		case "canceled":
			blockedReasons = append(blockedReasons, "ssh_rehearsal_canceled_result_recorded")
			recordingReason = "ssh_rehearsal_canceled_result_recorded"
		case "partial_recorded":
			blockedReasons = append(blockedReasons, "ssh_rehearsal_partial_result_recorded")
			recordingReason = "ssh_rehearsal_partial_result_recorded"
		}
	}
	return map[string]any{
		"mode":                          "ssh_rehearsal_result_recording_plan",
		"recording_state":               recordingState,
		"recording_ready":               recordingReady,
		"recording_ready_reason":        recordingReason,
		"recording_enabled":             recordingReady,
		"result_written":                recordingReady,
		"operation_log_written":         false,
		"canonical_asset_sync_queued":   false,
		"status_snapshot_written":       false,
		"auth_binding_recorded":         recordingReady,
		"verify_result_recorded":        verifyRuns > 0,
		"exec_result_recorded":          execRuns > 0,
		"stdout_included":               false,
		"stderr_included":               false,
		"raw_error_included":            false,
		"private_key_included":          false,
		"known_hosts_included":          false,
		"authorization_header_included": false,
		"sanitized_metadata_only":       true,
		"has_failures":                  boolOnlyFromAny(evidence["has_failures"]),
		"has_cancellations":             boolOnlyFromAny(evidence["has_cancellations"]),
		"active_runs":                   intFromAny(evidence["active_runs"], 0),
		"terminal_runs":                 intFromAny(evidence["terminal_runs"], 0),
		"required_result_fields":        []string{"operation_run_id", "ssh_machine_id", "operation_type", "status", "exit_code", "started_at", "finished_at", "auth_binding_status", "verify_result_status", "exec_result_status", "sanitization_status"},
		"suppressed_fields":             []string{"private_key", "passphrase", "known_hosts_body", "command", "stdout", "stderr", "raw_error", "runtime_secret"},
		"blocked_reasons":               blockedReasons,
		"message":                       message,
	}
}

func summarizeSSHRehearsalEvidence(runs []map[string]any) map[string]any {
	evidence := map[string]any{
		"total_runs":              len(runs),
		"verify_runs":             0,
		"exec_runs":               0,
		"unknown_runs":            0,
		"completed_runs":          0,
		"failed_runs":             0,
		"running_runs":            0,
		"queued_runs":             0,
		"canceled_runs":           0,
		"terminal_runs":           0,
		"active_runs":             0,
		"completed_verify":        false,
		"completed_exec":          false,
		"has_live_evidence":       len(runs) > 0,
		"has_failures":            false,
		"has_cancellations":       false,
		"evidence_state":          "not_recorded",
		"sanitized_metadata_only": true,
		"stdout_included":         false,
		"stderr_included":         false,
		"raw_error_included":      false,
		"private_key_included":    false,
		"known_hosts_included":    false,
		"secret_included":         false,
		"suppressed_fields":       []string{"command", "stdout", "stderr", "raw_error", "private_key", "passphrase", "known_hosts_body", "runtime_secret"},
		"latest_verify":           nil,
		"latest_exec":             nil,
		"latest_unknown":          nil,
	}
	for _, run := range runs {
		operationType := cleanPreviewString(run["operation_type"])
		if operationType == "" {
			operationType = "unknown"
		}
		status := cleanPreviewString(run["status"])
		item := map[string]any{
			"id":             run["id"],
			"status":         status,
			"exit_code":      run["exit_code"],
			"created_at":     run["created_at"],
			"finished_at":    run["finished_at"],
			"operation_type": operationType,
		}
		switch status {
		case "completed":
			evidence["completed_runs"] = intFromAny(evidence["completed_runs"], 0) + 1
			evidence["terminal_runs"] = intFromAny(evidence["terminal_runs"], 0) + 1
		case "failed":
			evidence["failed_runs"] = intFromAny(evidence["failed_runs"], 0) + 1
			evidence["terminal_runs"] = intFromAny(evidence["terminal_runs"], 0) + 1
			evidence["has_failures"] = true
		case "canceled", "cancelled":
			evidence["canceled_runs"] = intFromAny(evidence["canceled_runs"], 0) + 1
			evidence["terminal_runs"] = intFromAny(evidence["terminal_runs"], 0) + 1
			evidence["has_cancellations"] = true
		case "running":
			evidence["running_runs"] = intFromAny(evidence["running_runs"], 0) + 1
			evidence["active_runs"] = intFromAny(evidence["active_runs"], 0) + 1
		case "queued", "pending":
			evidence["queued_runs"] = intFromAny(evidence["queued_runs"], 0) + 1
			evidence["active_runs"] = intFromAny(evidence["active_runs"], 0) + 1
		}
		switch operationType {
		case "ssh.verify":
			evidence["verify_runs"] = intFromAny(evidence["verify_runs"], 0) + 1
			if evidence["latest_verify"] == nil {
				evidence["latest_verify"] = item
			}
			if status == "completed" {
				evidence["completed_verify"] = true
			}
		case "ssh.exec":
			evidence["exec_runs"] = intFromAny(evidence["exec_runs"], 0) + 1
			if evidence["latest_exec"] == nil {
				evidence["latest_exec"] = item
			}
			if status == "completed" {
				evidence["completed_exec"] = true
			}
		default:
			evidence["unknown_runs"] = intFromAny(evidence["unknown_runs"], 0) + 1
			if evidence["latest_unknown"] == nil {
				evidence["latest_unknown"] = item
			}
		}
	}
	if len(runs) == 0 {
		return evidence
	}
	switch {
	case boolOnlyFromAny(evidence["has_failures"]):
		evidence["evidence_state"] = "failed"
	case intFromAny(evidence["active_runs"], 0) > 0:
		evidence["evidence_state"] = "waiting_for_workers"
	case boolOnlyFromAny(evidence["has_cancellations"]):
		evidence["evidence_state"] = "canceled"
	case boolOnlyFromAny(evidence["completed_verify"]) && boolOnlyFromAny(evidence["completed_exec"]):
		evidence["evidence_state"] = "recorded"
	default:
		evidence["evidence_state"] = "partial_recorded"
	}
	return evidence
}

func cleanPreviewString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func statusWhen(ok bool) string {
	if ok {
		return "planned"
	}
	return "blocked"
}

func reasonWhen(ok bool, ready, blocked string) string {
	if ok {
		return ready
	}
	return blocked
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
	s.createSSHRun(w, r, machineID, input, "ssh.exec", "ssh "+fmt.Sprint(machine["name"]), "ssh_command.enqueue")
}

func (s *Server) verifySSHMachine(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	machine, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM ssh_machines WHERE id=$1", machineID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	input := map[string]any{
		"ssh_machine_id":  machineID,
		"command":         "true",
		"timeout_seconds": 15,
		"verify":          true,
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: fmt.Sprint(machine["project_id"])}, "ssh.verify") {
		return
	}
	s.createSSHRun(w, r, machineID, input, "ssh.verify", "verify ssh "+fmt.Sprint(machine["name"]), "ssh_verify.enqueue")
}

func (s *Server) createSSHRun(w http.ResponseWriter, r *http.Request, machineID string, input map[string]any, operationType, title, syncReason string) {
	tx, err := s.store.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start SSH command transaction")
		return
	}
	defer tx.Rollback()
	op, run, err := s.enqueueSSHCommandRun(r.Context(), tx, machineID, input, currentUser(r).ID, operationType, title)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch syncReason {
	case "ssh_verify.enqueue":
		if !s.syncCanonicalAssetsInTransaction(w, r, tx, "ssh_verify.enqueue") {
			return
		}
	default:
		if !s.syncCanonicalAssetsInTransaction(w, r, tx, "ssh_command.enqueue") {
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit SSH command")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operation": op, "run": run})
}

func (s *Server) enqueueSSHCommandRun(ctx context.Context, tx *sqlx.Tx, machineID string, input map[string]any, actorID, operationType, title string) (map[string]any, map[string]any, error) {
	machine, err := queryOne(ctx, tx, "SELECT * FROM ssh_machines WHERE id=$1 FOR SHARE", machineID)
	if err != nil {
		return nil, nil, err
	}
	command := strings.TrimSpace(stringFromMap(input, "command"))
	if command == "" {
		return nil, nil, fmt.Errorf("command is required")
	}
	if operationType == "" {
		operationType = "ssh.exec"
	}
	if title == "" {
		title = "ssh " + fmt.Sprint(machine["name"])
	}
	op, err := enqueueOperationTx(
		ctx,
		tx,
		fmt.Sprint(machine["project_id"]),
		"",
		operationType,
		title,
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
			SELECT scr.*, op.operation_type
			FROM ssh_command_runs scr
			LEFT JOIN operation_runs op ON op.id=scr.operation_run_id
			WHERE scr.ssh_machine_id=$1
			ORDER BY scr.created_at DESC
			LIMIT 100`, machineID)
		writeQueryResult(w, items, err)
	case projectID != "":
		items, err := queryMaps(r.Context(), s.store.DB, `
			SELECT scr.*, op.operation_type
			FROM ssh_command_runs scr
			LEFT JOIN operation_runs op ON op.id=scr.operation_run_id
			WHERE scr.project_id=$1
			ORDER BY scr.created_at DESC
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
	enrichDeploymentTargetsWithExecutionReadiness(deploymentTargets)
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
		{"name": "ssh.verify", "description": "SSH machine connectivity check"},
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
	deploymentExecutionSummary := formatCountMap(countNestedStringField(deploymentTargets, "deployment_execution_readiness", "status"))
	if deploymentExecutionSummary == "" {
		deploymentExecutionSummary = "none"
	}
	brief := fmt.Sprintf("# ASSOPS Context\n\nProject: %s\n\nRepositories: %d\nRemotes: %d\nRecent operations: %d\nApprovals: %d\nDeployment targets: %d\nDeployment execution readiness: %s\nRollback points: %d\nRollback execution: %s\nSSH machines: %d\nGitHub Actions runs: %d\nAsset graph assets: %d\nAsset graph relations: %d\nAsset status snapshots: %d\n", project["name"], len(repos), len(remotes), len(operations), len(approvals), len(deploymentTargets), deploymentExecutionSummary, len(rollbackPoints), rollbackGuardrail["execution_mode"], len(sshMachines), len(githubRuns), len(assets), len(assetRelations), len(assetStatusSnapshots))
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
