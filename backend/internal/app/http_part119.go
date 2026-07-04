package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
	"time"
)

func (s *Server) sshCommandRunMaps(ctx context.Context, filter GormSSHCommandRun) ([]map[string]any, error) {
	if s.store == nil || s.store.Gorm == nil {
		return nil, fmt.Errorf("gorm store is not initialized")
	}
	var runs []GormSSHCommandRun
	if err := s.store.Gorm.WithContext(ctx).
		Where(&filter).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Limit(100).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	operationTypes := map[string]string{}
	opIDs := make([]string, 0, len(runs))
	seen := map[string]bool{}
	for _, run := range runs {
		opID := cleanOptionalID(run.OperationRunID.String)
		if opID != "" && !seen[opID] {
			seen[opID] = true
			opIDs = append(opIDs, opID)
		}
	}
	if len(opIDs) > 0 {
		var ops []GormOperationRun
		if err := s.store.Gorm.WithContext(ctx).Find(&ops, opIDs).Error; err != nil {
			return nil, err
		}
		for _, op := range ops {
			operationTypes[op.ID] = op.OperationType
		}
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		items = append(items, sshCommandRunMap(run, operationTypes[cleanOptionalID(run.OperationRunID.String)]))
	}
	return items, nil
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
	token := newToken()
	var node map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var model GormWorkerNode
		err := tx.Where(&GormWorkerNode{Name: req.Name}).First(&model).Error
		if err != nil && !errorsIsRecordNotFound(err) {
			return err
		}
		model.Name = req.Name
		model.Kind = req.Kind
		model.Capabilities = pq.StringArray(req.Capabilities)
		model.Metadata = JSONValue{Data: req.Metadata}
		model.Status = "online"
		model.LastHeartbeatAt = time.Now()
		if err := tx.Save(&model).Error; err != nil {
			return err
		}
		tokenModel := GormWorkerNodeToken{WorkerNodeID: model.ID, TokenHash: tokenHash(token)}
		if err := tx.Create(&tokenModel).Error; err != nil {
			return err
		}
		node = workerNodeMap(model)
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not register node")
		return
	}
	node["token"] = token
	writeJSON(w, http.StatusCreated, node)
}

func workerNodeMap(node GormWorkerNode) map[string]any {
	return map[string]any{
		"id":                node.ID,
		"name":              node.Name,
		"kind":              node.Kind,
		"capabilities":      []string(node.Capabilities),
		"status":            node.Status,
		"last_heartbeat_at": node.LastHeartbeatAt,
		"metadata":          mapFromAny(node.Metadata.Data),
		"created_at":        node.CreatedAt,
		"updated_at":        node.UpdatedAt,
	}
}

func (s *Server) nodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	var item map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var model GormWorkerNode
		if err := tx.First(&model, &GormWorkerNode{GormBase: GormBase{ID: cleanOptionalID(fmt.Sprint(node["id"]))}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		model.Status = "online"
		model.LastHeartbeatAt = time.Now()
		if err := tx.Save(&model).Error; err != nil {
			return err
		}
		item = workerNodeMap(model)
		_, err := syncWorkerNodeCanonicalAssetGorm(r.Context(), tx, fmt.Sprint(model.ID))
		return err
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeQueryOne(w, nil, err)
			return
		}
		writeError(w, http.StatusInternalServerError, "could not sync worker node canonical asset")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) claimJob(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authenticateNode(w, r)
	if !ok {
		return
	}
	var item map[string]any
	err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var jobs []GormWorkerJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where(&GormWorkerJob{Status: "queued"}).
			Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).
			Find(&jobs).Error; err != nil {
			return err
		}
		nodeKind := strings.TrimSpace(fmt.Sprint(node["kind"]))
		nodeCapabilities := stringSliceFromAny(node["capabilities"])
		selected := -1
		for i, job := range jobs {
			if workerJobMatchesRemoteNode(job, nodeKind, nodeCapabilities) {
				selected = i
				break
			}
		}
		if selected < 0 {
			return ErrNotFound
		}
		job := jobs[selected]
		now := time.Now()
		job.Status = "running"
		job.AssignedWorkerNodeID = validNullString(cleanOptionalID(fmt.Sprint(node["id"])))
		job.ClaimedAt = validNullTime(now)
		job.StartedAt = validNullTime(now)
		if err := tx.Save(&job).Error; err != nil {
			return err
		}
		if opID := cleanOptionalID(job.OperationRunID.String); opID != "" {
			if err := tx.Model(&GormOperationRun{}).
				Where(&GormOperationRun{GormBase: GormBase{ID: opID}}).
				Updates(map[string]any{"status": "running", "started_at": validNullTime(now)}).Error; err != nil {
				return err
			}
		}
		item = workerJobMap(job)
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"job": nil})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not claim job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": item})
}

func localWorkerPreferredKinds() []string {
	return []string{"", "control-worker", "local"}
}

func isRemoteWorkerPreferredKind(preferred string) bool {
	preferred = strings.TrimSpace(preferred)
	if preferred == "" || preferred == "control-worker" || preferred == "local" {
		return false
	}
	return true
}

func workerJobMatchesRemoteNode(job GormWorkerJob, nodeKind string, nodeCapabilities []string) bool {
	preferred := strings.TrimSpace(job.PreferredNodeKind)
	if !isRemoteWorkerPreferredKind(preferred) {
		return false
	}
	if preferred != strings.TrimSpace(nodeKind) {
		return false
	}
	return workerHasCapabilities(nodeCapabilities, []string(job.RequiredCapabilities))
}

func workerHasCapabilities(workerCapabilities, requiredCapabilities []string) bool {
	if len(requiredCapabilities) == 0 {
		return true
	}
	available := make(map[string]bool, len(workerCapabilities))
	for _, capability := range workerCapabilities {
		capability = strings.TrimSpace(capability)
		if capability != "" {
			available[capability] = true
		}
	}
	for _, required := range requiredCapabilities {
		required = strings.TrimSpace(required)
		if required != "" && !available[required] {
			return false
		}
	}
	return true
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
	var job GormWorkerJob
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormWorkerJob{GormBase: GormBase{ID: chi.URLParam(r, "id")}, AssignedWorkerNodeID: validNullString(cleanOptionalID(fmt.Sprint(node["id"])))}).First(&job).Error; err != nil {
		writeCreatedOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	log := GormOperationLog{OperationRunID: job.OperationRunID, WorkerJobID: validNullString(job.ID), Level: req.Level, Message: req.Message, Fields: JSONValue{Data: req.Fields}}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&log).Error; err != nil {
		writeCreatedOne(w, nil, err)
		return
	}
	writeCreatedOne(w, operationLogMap(log), nil)
}

func operationLogMap(log GormOperationLog) map[string]any {
	return map[string]any{"id": log.ID, "operation_run_id": nullableStringValue(log.OperationRunID), "worker_job_id": nullableStringValue(log.WorkerJobID), "level": log.Level, "message": log.Message, "fields": mapFromAny(log.Fields.Data), "created_at": log.CreatedAt}
}

func (s *Server) nodeJobComplete(w http.ResponseWriter, r *http.Request) {
	s.finishNodeJob(w, r, "completed")
}

func (s *Server) nodeJobFail(w http.ResponseWriter, r *http.Request) {
	s.finishNodeJob(w, r, "failed")
}
