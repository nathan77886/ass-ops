package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func (s *Server) loadRollbackPointForExecutionGateGorm(ctx context.Context, rollbackPointID string) (map[string]any, error) {
	var point GormRollbackPoint
	if err := s.store.Gorm.WithContext(ctx).First(&point, &GormRollbackPoint{ID: rollbackPointID}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	item := map[string]any{"id": point.ID, "project_id": point.ProjectID, "deployment_record_id": nullableStringValue(point.DeploymentRecordID), "deployment_target_id": nullableStringValue(point.DeploymentTargetID), "name": point.Name, "environment": point.Environment, "revision": point.Revision, "source": point.Source, "status": point.Status, "captured_at": point.CapturedAt, "created_at": point.CreatedAt}
	if targetID := cleanOptionalID(point.DeploymentTargetID.String); targetID != "" {
		var target GormDeploymentTarget
		if err := s.store.Gorm.WithContext(ctx).First(&target, &GormDeploymentTarget{GormBase: GormBase{ID: targetID}}).Error; err == nil {
			item["deployment_target_name"] = target.Name
			item["deployment_namespace"] = target.Namespace
			item["deployment_cluster_name"] = target.ClusterName
		} else if !errorsIsRecordNotFound(err) {
			return nil, err
		}
	}
	if recordID := cleanOptionalID(point.DeploymentRecordID.String); recordID != "" {
		var record GormDeploymentRecord
		if err := s.store.Gorm.WithContext(ctx).First(&record, &GormDeploymentRecord{GormBase: GormBase{ID: recordID}}).Error; err == nil {
			item["deployment_status"] = record.Status
		} else if !errorsIsRecordNotFound(err) {
			return nil, err
		}
	}
	return item, nil
}

func (s *Server) listRollbackPoints(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "rollback_point", ProjectID: projectID}, "read") {
		return
	}
	items, err := contextRollbackPointMaps(r.Context(), s.store.Gorm, projectID, 500)
	writeQueryResult(w, items, err)
}

func (s *Server) rollbackPointExecutionGate(w http.ResponseWriter, r *http.Request) {
	rollbackPointID := cleanOptionalID(chi.URLParam(r, "id"))
	if rollbackPointID == "" {
		writeError(w, http.StatusBadRequest, "rollback point id is required")
		return
	}
	rollbackPoint, err := s.loadRollbackPointForExecutionGateGorm(r.Context(), rollbackPointID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "rollback point not found")
			return
		}
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanOptionalID(fmt.Sprint(rollbackPoint["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "rollback_point", ID: rollbackPointID, ProjectID: projectID}, "read") {
		return
	}
	writeJSON(w, http.StatusOK, rollbackPointExecutionGatePayload(rollbackPoint))
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
	target, err := s.loadArgoPodLogTargetGorm(r.Context(), projectID, cleaned.DeploymentTargetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	auditRows, err := s.queryArgoPodLogAuditOperationsGorm(r.Context(), projectID, cleaned.DeploymentTargetID, cleaned.PodName, cleaned.ContainerName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load pod log audit evidence")
		return
	}
	writeJSON(w, http.StatusOK, argoPodLogQueryPreviewWithConfig(s.cfg, cleaned.PodName, cleaned.ContainerName, cleaned.TailLines, cleaned.SinceSeconds, target, auditRows))
}

type argoPodLogRequest struct {
	DeploymentTargetID string `json:"deployment_target_id"`
	PodName            string `json:"pod_name"`
	ContainerName      string `json:"container_name"`
	TailLines          int    `json:"tail_lines"`
	SinceSeconds       int    `json:"since_seconds"`
}

type argoPodRestartRequest struct {
	DeploymentTargetID string `json:"deployment_target_id"`
	DeploymentName     string `json:"deployment_name"`
}

func cleanArgoPodRestartRequest(req argoPodRestartRequest) (argoPodRestartRequest, error) {
	req.DeploymentTargetID = strings.TrimSpace(req.DeploymentTargetID)
	req.DeploymentName = strings.TrimSpace(req.DeploymentName)
	if req.DeploymentTargetID == "" {
		return req, fmt.Errorf("deployment_target_id is required")
	}
	if req.DeploymentName == "" {
		return req, fmt.Errorf("deployment_name is required")
	}
	if !kubernetesPodPattern.MatchString(req.DeploymentName) || len(req.DeploymentName) > 253 {
		return req, fmt.Errorf("invalid Kubernetes deployment name")
	}
	return req, nil
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

func (s *Server) loadArgoPodLogTargetGorm(ctx context.Context, projectID, deploymentTargetID string) (map[string]any, error) {
	target, err := s.loadDeploymentTargetForKubernetesAccessGorm(ctx, deploymentTargetID)
	if err != nil {
		return nil, err
	}
	if cleanOptionalID(fmt.Sprint(target["project_id"])) != cleanOptionalID(projectID) {
		return nil, ErrNotFound
	}
	return target, nil
}

func (s *Server) queryArgoPodLogAuditOperationsGorm(ctx context.Context, projectID, deploymentTargetID, podName, containerName string) ([]map[string]any, error) {
	var ops []GormOperationRun
	if err := s.store.Gorm.WithContext(ctx).Where(&GormOperationRun{ProjectID: validNullString(projectID), OperationType: "argo.pod_logs"}).Order(gormOrderDesc("created_at")).Limit(200).Find(&ops).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, 20)
	for _, op := range ops {
		input := mapFromAny(op.Input.Data)
		if stringFromMap(input, "deployment_target_id") != deploymentTargetID || stringFromMap(input, "pod_name") != podName || stringFromMap(input, "container_name") != containerName {
			continue
		}
		logCount, err := operationLogCountGorm(ctx, s.store.Gorm, op.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": op.ID, "status": op.Status, "result": mapFromAny(op.Result.Data), "created_at": op.CreatedAt, "updated_at": op.UpdatedAt, "finished_at": nullableTimeAny(op.FinishedAt), "operation_log_count": logCount})
		if len(items) >= 20 {
			break
		}
	}
	return items, nil
}

func operationLogCountGorm(ctx context.Context, db *gorm.DB, operationID string) (int, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&GormOperationLog{}).Where(&GormOperationLog{OperationRunID: validNullString(operationID)}).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
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
	target, err := s.loadArgoPodLogTargetGorm(r.Context(), projectID, cleaned.DeploymentTargetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	preview := argoPodLogQueryPreviewWithConfig(s.cfg, cleaned.PodName, cleaned.ContainerName, cleaned.TailLines, cleaned.SinceSeconds, target)
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
