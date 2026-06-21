package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

var ErrNotFound = errors.New("not found")

type Server struct {
	cfg   Config
	store *Store
	log   *slog.Logger
}

func NewServer(cfg Config, store *Store, log *slog.Logger) *Server {
	return &Server{cfg: cfg, store: store, log: log}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cors)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r.Post("/api/auth/login", s.login)

	r.Group(func(r chi.Router) {
		r.Use(s.auth)
		r.Get("/api/auth/me", s.me)
		r.Post("/api/projects", s.createProject)
		r.Get("/api/projects", s.listProjects)
		r.Get("/api/projects/{id}", s.getProject)
		r.Post("/api/projects/{id}/git-repositories", s.createGitRepository)
		r.Get("/api/projects/{id}/git-repositories", s.listGitRepositories)
		r.Post("/api/git-repositories/{id}/remotes", s.createGitRemote)
		r.Get("/api/git-repositories/{id}/remotes", s.listGitRemotes)
		r.Put("/api/git-remotes/{id}", s.updateGitRemote)
		r.Post("/api/git-remotes/{id}/sync", s.createRemoteOperation("repo.sync"))
		r.Post("/api/git-remotes/{id}/tag", s.createRemoteOperation("repo.tag"))
		r.Post("/api/git-remotes/{id}/github-actions/sync", s.createRemoteOperation("github.actions.sync"))
		r.Get("/api/operations", s.listOperations)
		r.Get("/api/operations/{id}", s.getOperation)
		r.Get("/api/operations/{id}/logs", s.getOperationLogs)
		r.Post("/api/operations/{id}/cancel", s.cancelOperation)
		r.Post("/api/worker-nodes/test-job", s.createNodeTestJob)
		r.Get("/api/ai-runtimes", s.listAIRuntimes)
		r.Post("/api/ai-runtimes", s.createAIRuntime)
		r.Post("/api/ai-runtimes/{id}/verify", s.verifyAIRuntime)
		r.Post("/api/projects/{id}/agent/tasks", s.createAgentTask)
		r.Get("/api/agent/tasks/{id}", s.getAgentTask)
		r.Post("/api/agent/tasks/{id}/generate-plan", s.generatePlan)
		r.Post("/api/agent/tasks/{id}/approve-plan", s.approvePlan)
		r.Post("/api/agent/tasks/{id}/execute", s.executePlan)
		r.Post("/api/projects/{id}/argo/connections", s.createArgoConnection)
		r.Get("/api/projects/{id}/argo/apps", s.listArgoApps)
		r.Post("/api/projects/{id}/ssh-machines", s.createSSHMachine)
		r.Get("/api/projects/{id}/ssh-machines", s.listSSHMachines)
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token, err := jwt.Parse(strings.TrimPrefix(header, "Bearer "), func(t *jwt.Token) (any, error) {
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
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit project")
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM projects ORDER BY created_at DESC")
	writeQueryResult(w, items, err)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	item, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM projects WHERE id=$1", chi.URLParam(r, "id"))
	writeQueryOne(w, item, err)
}

func (s *Server) createGitRepository(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		RepoKey       string `json:"repo_key"`
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
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO project_git_repositories(project_id, name, repo_key, description, default_branch)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING *`, chi.URLParam(r, "id"), req.Name, req.RepoKey, req.Description, req.DefaultBranch)
	writeCreatedOne(w, item, err)
}

func (s *Server) listGitRepositories(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT * FROM project_git_repositories WHERE project_id=$1 ORDER BY created_at DESC`, chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) createGitRemote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string         `json:"name"`
		Kind          string         `json:"kind"`
		URLs          []string       `json:"urls"`
		DefaultBranch string         `json:"default_branch"`
		Metadata      map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = "github"
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
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
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO git_remotes(project_git_repository_id, name, kind, urls, default_branch, metadata)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6::jsonb)
		RETURNING *`, chi.URLParam(r, "id"), req.Name, req.Kind, urls, req.DefaultBranch, metadata)
	writeCreatedOne(w, item, err)
}

func (s *Server) listGitRemotes(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, `
		SELECT * FROM git_remotes WHERE project_git_repository_id=$1 ORDER BY created_at DESC`, chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) updateGitRemote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string         `json:"name"`
		Kind          string         `json:"kind"`
		URLs          []string       `json:"urls"`
		DefaultBranch string         `json:"default_branch"`
		Metadata      map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	urls, _ := jsonParam(req.URLs)
	metadata, _ := jsonParam(req.Metadata)
	item, err := queryOne(r.Context(), s.store.DB, `
		UPDATE git_remotes
		SET name=COALESCE(NULLIF($2,''), name),
			kind=COALESCE(NULLIF($3,''), kind),
			urls=CASE WHEN $4='null' THEN urls ELSE $4::jsonb END,
			default_branch=COALESCE(NULLIF($5,''), default_branch),
			metadata=CASE WHEN $6='null' THEN metadata ELSE $6::jsonb END,
			updated_at=now()
		WHERE id=$1
		RETURNING *`, chi.URLParam(r, "id"), req.Name, req.Kind, urls, req.DefaultBranch, metadata)
	writeQueryOne(w, item, err)
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
		op, err := s.enqueueOperation(r.Context(), fmt.Sprint(remote["project_id"]), chi.URLParam(r, "id"), tool, tool+" "+fmt.Sprint(remote["name"]), input, []string{"git"}, "")
		writeCreatedOne(w, op, err)
	}
}

func (s *Server) enqueueOperation(ctx context.Context, projectID, remoteID, tool, title string, input map[string]any, capabilities []string, preferredKind string) (map[string]any, error) {
	payload, err := jsonParam(input)
	if err != nil {
		return nil, err
	}
	tx, err := s.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
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
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return op, nil
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM operation_runs ORDER BY created_at DESC LIMIT 100")
	writeQueryResult(w, items, err)
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	item, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM operation_runs WHERE id=$1", chi.URLParam(r, "id"))
	writeQueryOne(w, item, err)
}

func (s *Server) getOperationLogs(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM operation_logs WHERE operation_run_id=$1 ORDER BY created_at", chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) cancelOperation(w http.ResponseWriter, r *http.Request) {
	item, err := queryOne(r.Context(), s.store.DB, `
		UPDATE operation_runs SET status='canceled', finished_at=now(), updated_at=now()
		WHERE id=$1 RETURNING *`, chi.URLParam(r, "id"))
	writeQueryOne(w, item, err)
}

func (s *Server) createNodeTestJob(w http.ResponseWriter, r *http.Request) {
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
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO ai_runtimes(project_id, name, runtime_type, codex_binary, model, config)
		VALUES (NULLIF($1,'')::uuid, $2, $3, $4, $5, $6::jsonb)
		RETURNING *`, req.ProjectID, req.Name, req.RuntimeType, req.CodexBinary, req.Model, config)
	writeCreatedOne(w, item, err)
}

func (s *Server) verifyAIRuntime(w http.ResponseWriter, r *http.Request) {
	item, err := queryOne(r.Context(), s.store.DB, "UPDATE ai_runtimes SET status='verified', updated_at=now() WHERE id=$1 RETURNING *", chi.URLParam(r, "id"))
	writeQueryOne(w, item, err)
}

func (s *Server) createAgentTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO agent_tasks(project_id, title, prompt, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING *`, chi.URLParam(r, "id"), req.Title, req.Prompt, currentUser(r).ID)
	writeCreatedOne(w, item, err)
}

func (s *Server) getAgentTask(w http.ResponseWriter, r *http.Request) {
	task, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM agent_tasks WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	plans, _ := queryMaps(r.Context(), s.store.DB, "SELECT * FROM agent_plans WHERE agent_task_id=$1 ORDER BY created_at DESC", chi.URLParam(r, "id"))
	task["plans"] = plans
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) generatePlan(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	content := "MVP adapter plan:\n1. Build context snapshot.\n2. Validate requested tool access.\n3. Enqueue worker job for execution.\n4. Stream operation logs."
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO agent_plans(agent_task_id, content)
		VALUES ($1, $2)
		RETURNING *`, taskID, content)
	writeCreatedOne(w, item, err)
}

func (s *Server) approvePlan(w http.ResponseWriter, r *http.Request) {
	item, err := queryOne(r.Context(), s.store.DB, `
		UPDATE agent_plans SET status='approved', approved_at=now()
		WHERE agent_task_id=$1 AND id=(SELECT id FROM agent_plans WHERE agent_task_id=$1 ORDER BY created_at DESC LIMIT 1)
		RETURNING *`, chi.URLParam(r, "id"))
	writeQueryOne(w, item, err)
}

func (s *Server) executePlan(w http.ResponseWriter, r *http.Request) {
	task, err := queryOne(r.Context(), s.store.DB, "SELECT * FROM agent_tasks WHERE id=$1", chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	op, err := s.enqueueOperation(r.Context(), fmt.Sprint(task["project_id"]), "", "agent.execute", "execute agent task "+fmt.Sprint(task["title"]), map[string]any{"agent_task_id": task["id"]}, []string{"ai"}, "")
	if err == nil {
		_, _ = s.store.DB.ExecContext(r.Context(), "UPDATE agent_tasks SET status='queued', updated_at=now() WHERE id=$1", chi.URLParam(r, "id"))
	}
	writeCreatedOne(w, op, err)
}

func (s *Server) createArgoConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string         `json:"name"`
		ServerURL string         `json:"server_url"`
		AuthType  string         `json:"auth_type"`
		Config    map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AuthType == "" {
		req.AuthType = "token"
	}
	config, _ := jsonParam(req.Config)
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO argo_connections(project_id, name, server_url, auth_type, config)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		RETURNING *`, chi.URLParam(r, "id"), req.Name, req.ServerURL, req.AuthType, config)
	writeCreatedOne(w, item, err)
}

func (s *Server) listArgoApps(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM argo_apps WHERE project_id=$1 ORDER BY created_at DESC", chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
}

func (s *Server) createSSHMachine(w http.ResponseWriter, r *http.Request) {
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
	item, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO ssh_machines(project_id, name, host, port, username, auth_type, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		RETURNING *`, chi.URLParam(r, "id"), req.Name, req.Host, req.Port, req.Username, req.AuthType, metadata)
	writeCreatedOne(w, item, err)
}

func (s *Server) listSSHMachines(w http.ResponseWriter, r *http.Request) {
	items, err := queryMaps(r.Context(), s.store.DB, "SELECT * FROM ssh_machines WHERE project_id=$1 ORDER BY created_at DESC", chi.URLParam(r, "id"))
	writeQueryResult(w, items, err)
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
	metadata, _ := jsonParam(req.Metadata)
	node, err := queryOne(r.Context(), s.store.DB, `
		INSERT INTO worker_nodes(name, kind, capabilities, metadata)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT(name) DO UPDATE SET kind=EXCLUDED.kind, capabilities=EXCLUDED.capabilities, metadata=EXCLUDED.metadata, status='online', last_heartbeat_at=now(), updated_at=now()
		RETURNING *`, req.Name, req.Kind, pq.Array(req.Capabilities), metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not register node")
		return
	}
	token := newToken()
	_, err = s.store.DB.ExecContext(r.Context(), `
		INSERT INTO worker_node_tokens(worker_node_id, token_hash)
		VALUES ($1, $2)`, node["id"], tokenHash(token))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create node token")
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
	item, err := queryOne(r.Context(), s.store.DB, `
		UPDATE worker_nodes SET status='online', last_heartbeat_at=now(), updated_at=now()
		WHERE id=$1 RETURNING *`, node["id"])
	writeQueryOne(w, item, err)
}

func (s *Server) claimJob(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	item, err := queryOne(r.Context(), s.store.DB, `
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
	_, _ = s.store.DB.ExecContext(r.Context(), "UPDATE operation_runs SET status='running', started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=$1", item["operation_run_id"])
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
	job, err := queryOne(r.Context(), s.store.DB, `
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
	_, _ = s.store.DB.ExecContext(r.Context(), `
		UPDATE operation_runs SET status=$2, result=$3::jsonb, error=$4, finished_at=now(), updated_at=now()
		WHERE id=$1`, job["operation_run_id"], opStatus, result, req.Error)
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
	files, snapshot, err := s.BuildContextFiles(r.Context(), chi.URLParam(r, "id"))
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
	tools := []map[string]any{
		{"name": "repo.sync", "description": "sync repository adapter"},
		{"name": "repo.tag", "description": "tag repository adapter"},
		{"name": "github.actions.sync", "description": "GitHub Actions query adapter"},
		{"name": "ssh.exec", "description": "SSH command adapter"},
	}
	contextJSON := map[string]any{"project": project, "repositories": repos, "remotes": remotes}
	manifest := map[string]any{"tools": tools}
	brief := fmt.Sprintf("# ASSOPS Context\n\nProject: %s\n\nRepositories: %d\nRemotes: %d\n", project["name"], len(repos), len(remotes))
	base := filepath.Join(s.cfg.ContextDir, projectID)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, nil, err
	}
	files := map[string]string{
		"ASSOPS_CONTEXT.md":   filepath.Join(base, "ASSOPS_CONTEXT.md"),
		"assops-context.json": filepath.Join(base, "assops-context.json"),
		"tool-manifest.json":  filepath.Join(base, "tool-manifest.json"),
	}
	if err := os.WriteFile(files["ASSOPS_CONTEXT.md"], []byte(brief), 0o644); err != nil {
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
	return os.WriteFile(path, bytes, 0o644)
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
