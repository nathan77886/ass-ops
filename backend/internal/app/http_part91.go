package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"io"
	"net/http"
	"strings"
	"time"
)

func operationLogMaps(ctx context.Context, db *gorm.DB, opID string, cursor operationLogCursor, limited bool) ([]map[string]any, error) {
	var logs []GormOperationLog
	query := db.WithContext(ctx).
		Where(&GormOperationLog{OperationRunID: validNullString(opID)}).
		Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}}, {Column: clause.Column{Name: "id"}}}})
	if err := query.Find(&logs).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(logs))
	for _, log := range logs {
		if limited && !operationLogAfterCursor(log, cursor) {
			continue
		}
		items = append(items, operationLogMap(log))
		if limited && len(items) >= operationLogStreamBatchLimit {
			break
		}
	}
	return items, nil
}

func operationLogAfterCursor(log GormOperationLog, cursor operationLogCursor) bool {
	if cursor.CreatedAt == "" || cursor.ID == "" {
		return true
	}
	cursorTime, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil {
		return true
	}
	if log.CreatedAt.After(cursorTime) {
		return true
	}
	if log.CreatedAt.Equal(cursorTime) && log.ID > cursor.ID {
		return true
	}
	return false
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
	op, err := operationRunByID(r.Context(), s.store.Gorm, opID)
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	opMap := operationRunMap(op)
	projectID := strings.TrimSpace(fmt.Sprint(opMap["project_id"]))
	resource := PolicyResource{Type: "operation", ID: opID, ProjectID: projectID}
	payload := map[string]any{"kind": "operation_cancel", "operation_id": opID}
	if projectID != "" && projectID != "<nil>" {
		if !s.requireProjectPolicyOrApproval(w, r, resource, "operation.cancel", "cancel "+op.Title, payload) {
			return
		}
	} else if !s.requirePolicyOrApproval(w, r, resource, "operation.cancel", "cancel "+op.Title, payload) {
		return
	}
	var item map[string]any
	err = s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		item, err = s.cancelOperationRunGorm(r.Context(), tx, opID)
		if err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
			return fmt.Errorf("syncing canonical assets for operation cancel: %w", err)
		}
		return nil
	})
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	writeQueryOne(w, item, nil)
}

func (s *Server) cancelOperationRunGorm(ctx context.Context, db *gorm.DB, operationID string) (map[string]any, error) {
	var run GormOperationRun
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, &GormOperationRun{GormBase: GormBase{ID: operationID}}).Error; err != nil {
		return nil, err
	}
	if operationStreamTerminal(run.Status) {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	run.Status = "canceled"
	run.FinishedAt = validNullTime(now)
	if err := db.WithContext(ctx).Save(&run).Error; err != nil {
		return nil, err
	}
	var jobs []GormWorkerJob
	if err := db.WithContext(ctx).Where(&GormWorkerJob{OperationRunID: validNullString(operationID), Status: "queued"}).Find(&jobs).Error; err != nil {
		return nil, err
	}
	for i := range jobs {
		jobs[i].Status = "canceled"
		jobs[i].FinishedAt = validNullTime(now)
		if err := db.WithContext(ctx).Save(&jobs[i]).Error; err != nil {
			return nil, err
		}
	}
	return operationRunMap(run), nil
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
	var runtimes []GormAIRuntime
	err := s.store.Gorm.WithContext(r.Context()).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&runtimes).Error
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	credentials, err := s.connectionCredentialsForRuntimes(r.Context(), runtimes)
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	items := make([]map[string]any, 0, len(runtimes))
	for _, runtime := range runtimes {
		items = append(items, aiRuntimeMap(runtime, credentials[runtime.CredentialID.String]))
	}
	writeQueryResult(w, items, nil)
}

func (s *Server) createAIRuntime(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "ai_runtime"}, "create") {
		return
	}
	var req struct {
		ProjectID    string         `json:"project_id"`
		Name         string         `json:"name"`
		RuntimeType  string         `json:"runtime_type"`
		CodexBinary  string         `json:"codex_binary"`
		ProviderType string         `json:"provider_type"`
		APIBaseURL   string         `json:"api_base_url"`
		CredentialID string         `json:"credential_id"`
		Model        string         `json:"model"`
		Config       map[string]any `json:"config"`
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
	req.ProviderType = cleanAIProviderType(req.ProviderType)
	req.APIBaseURL = strings.TrimSpace(req.APIBaseURL)
	credentialID := cleanOptionalID(req.CredentialID)
	if req.APIBaseURL != "" && !validPublicHTTPURL(r.Context(), req.APIBaseURL) {
		writeError(w, http.StatusBadRequest, "api_base_url must be a public http or https URL")
		return
	}
	config := sanitizeAIRuntimeConfig(req.Config)
	var credential *GormConnectionCredential
	var err error
	if credentialID != "" {
		credential, err = s.connectionCredentialForProjectOrGlobal(r.Context(), req.ProjectID, credentialID, "ai_provider_api_key")
		if err != nil {
			writeError(w, http.StatusBadRequest, "credential_id must reference an AI provider API key credential in this project or globally")
			return
		}
	}
	runtime := GormAIRuntime{
		ProjectID:    validNullString(cleanOptionalID(req.ProjectID)),
		Name:         cleanOptionalText(req.Name),
		RuntimeType:  req.RuntimeType,
		CodexBinary:  req.CodexBinary,
		ProviderType: req.ProviderType,
		APIBaseURL:   req.APIBaseURL,
		CredentialID: validNullString(credentialID),
		Model:        strings.TrimSpace(req.Model),
		Config:       JSONValue{Data: config},
		Status:       "unknown",
	}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&runtime).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create AI runtime")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync AI runtime asset")
		return
	}
	writeJSON(w, http.StatusCreated, aiRuntimeMap(runtime, credential))
}

func (s *Server) verifyAIRuntime(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "ai_runtime", ID: chi.URLParam(r, "id")}, "update") {
		return
	}
	var runtime GormAIRuntime
	if err := s.store.Gorm.WithContext(r.Context()).Where(map[string]any{"id": chi.URLParam(r, "id")}).First(&runtime).Error; err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	runtime.Status = "verified"
	if err := s.store.Gorm.WithContext(r.Context()).Save(&runtime).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not verify AI runtime")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync AI runtime asset")
		return
	}
	credential, err := s.connectionCredentialByID(r.Context(), runtime.CredentialID.String)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, aiRuntimeMap(runtime, credential))
}
