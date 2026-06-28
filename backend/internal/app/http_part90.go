package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"io"
	"net/http"
	"strings"
	"time"
)

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
	item, err := operationRunByID(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	itemMap := operationRunMap(item)
	if !s.requireOperationRead(w, r, itemMap) {
		return
	}
	writeQueryOne(w, itemMap, nil)
}

func (s *Server) getOperationLogs(w http.ResponseWriter, r *http.Request) {
	op, err := operationRunByID(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if !s.requireOperationRead(w, r, operationRunMap(op)) {
		return
	}
	items, err := operationLogMaps(r.Context(), s.store.Gorm, chi.URLParam(r, "id"), operationLogCursor{}, false)
	writeQueryResult(w, items, err)
}

func (s *Server) streamOperationLogs(w http.ResponseWriter, r *http.Request) {
	opID := chi.URLParam(r, "id")
	op, err := operationRunByID(r.Context(), s.store.Gorm, opID)
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if !s.requireOperationRead(w, r, operationRunMap(op)) {
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
	items, err := operationLogStreamBatch(ctx, s.store.Gorm, opID, *cursor)
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
	status, err := operationStatus(ctx, s.store.Gorm, opID)
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

func operationLogStreamBatch(ctx context.Context, db *gorm.DB, opID string, cursor operationLogCursor) ([]map[string]any, error) {
	return operationLogMaps(ctx, db, opID, cursor, true)
}

func operationStatus(ctx context.Context, db *gorm.DB, opID string) (string, error) {
	var run GormOperationRun
	if err := db.WithContext(ctx).First(&run, &GormOperationRun{GormBase: GormBase{ID: opID}}).Error; err != nil {
		return "", err
	}
	return run.Status, nil
}

func operationRunMap(run GormOperationRun) map[string]any {
	return map[string]any{
		"id":             run.ID,
		"project_id":     nullableStringValue(run.ProjectID),
		"git_remote_id":  nullableStringValue(run.GitRemoteID),
		"operation_type": run.OperationType,
		"status":         run.Status,
		"title":          run.Title,
		"input":          mapFromAny(run.Input.Data),
		"result":         mapFromAny(run.Result.Data),
		"error":          run.Error,
		"started_at":     nullableTimeAny(run.StartedAt),
		"finished_at":    nullableTimeAny(run.FinishedAt),
		"created_at":     run.CreatedAt,
		"updated_at":     run.UpdatedAt,
	}
}
