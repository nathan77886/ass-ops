package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
)

func (s *Server) requestArgoPodRestart(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	var req argoPodRestartRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cleaned, err := cleanArgoPodRestartRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := s.loadArgoPodLogTargetGorm(r.Context(), projectID, cleaned.DeploymentTargetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if cleanOptionalText(fmt.Sprint(target["namespace"])) == "" || cleanOptionalText(fmt.Sprint(target["cluster_name"])) == "" {
		writeError(w, http.StatusBadRequest, "deployment target Kubernetes metadata is incomplete")
		return
	}
	if cleanPreviewString(target["kubernetes_environment_status"]) != "ready" ||
		cleanPreviewString(target["token_subject_review_status"]) != "reviewed" ||
		cleanPreviewString(target["rbac_restart_pods_status"]) != "reviewed" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":                    "Kubernetes restart access is not reviewed for this target",
			"restart_backend_enabled":  s.cfg.KubernetesRestartsEnabled,
			"kubernetes_environment":   sanitizedDeploymentTargetForPodMetadata(target),
			"required_review_statuses": []string{"kubernetes_environment_status=ready", "token_subject_review_status=reviewed", "rbac_restart_pods_status=reviewed"},
		})
		return
	}
	input := argoPodRestartOperationInput(projectID, target, cleaned.DeploymentName)
	payload := map[string]any{
		"kind":                 "argo_pod_restart",
		"project_id":           projectID,
		"deployment_target_id": cleaned.DeploymentTargetID,
		"input":                input,
		"result_scope":         "sanitized_rollout_restart_metadata",
	}
	resource := PolicyResource{Type: "deployment_target", ID: cleaned.DeploymentTargetID, ProjectID: projectID}
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return
	}
	decision := NewPolicyChecker().Check(currentUser(r), resource, "argo.pod_restart")
	if decision.Effect == PolicyDeny {
		writeJSON(w, http.StatusForbidden, decision)
		return
	}
	approval, err := s.createOperationApproval(r.Context(), resource, "argo.pod_restart", "restart deployment "+cleaned.DeploymentName, payload, currentUser(r).ID)
	if err != nil {
		if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
			writeError(w, http.StatusConflict, "approval request is already pending")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create pod restart approval request")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"approval":                approval,
		"decision":                decision,
		"operation_type":          "argo.pod_restart",
		"worker_job_created":      false,
		"restart_backend_enabled": s.cfg.KubernetesRestartsEnabled,
		"log_body_included":       false,
		"raw_response_included":   false,
		"message":                 "Pod restart approval requested; worker job will be created only after approval.",
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
	liveLogBackend := cleanPreviewString(executionPlan["live_log_backend"])
	if liveLogBackend != "kubernetes_client_logs" {
		liveLogBackend = "disabled"
	}
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
		"result_scope":           "sanitized_live_log_metadata",
		"execution_mode":         "approval_gated_audit",
		"live_log_backend":       liveLogBackend,
		"execution_plan":         executionPlan,
	}
}

func argoPodRestartOperationInput(projectID string, target map[string]any, deploymentName string) map[string]any {
	return map[string]any{
		"project_id":             projectID,
		"deployment_target_id":   cleanOptionalID(fmt.Sprint(target["id"])),
		"deployment_target_name": cleanOptionalText(fmt.Sprint(target["name"])),
		"environment":            cleanOptionalText(fmt.Sprint(target["environment"])),
		"cluster_name":           cleanOptionalText(fmt.Sprint(target["cluster_name"])),
		"namespace":              cleanOptionalText(fmt.Sprint(target["namespace"])),
		"deployment_name":        cleanOptionalText(deploymentName),
		"result_scope":           "sanitized_rollout_restart_metadata",
		"execution_mode":         "approval_gated_rollout_restart",
		"restart_backend":        "kubernetes_client_rollout_restart",
	}
}

func (s *Server) enqueueArgoPodRestartOperationGorm(ctx context.Context, tx *gorm.DB, input map[string]any) (map[string]any, error) {
	projectID := cleanOptionalID(fmt.Sprint(input["project_id"]))
	targetID := cleanOptionalID(fmt.Sprint(input["deployment_target_id"]))
	deploymentName := cleanOptionalText(fmt.Sprint(input["deployment_name"]))
	if projectID == "" || targetID == "" || deploymentName == "" {
		return nil, fmt.Errorf("invalid pod restart operation input")
	}
	title := "restart deployment " + deploymentName
	op, err := enqueueOperationGorm(ctx, tx, projectID, "", "argo.pod_restart", title, input, []string{"argo", "kubernetes"}, "control-worker")
	if err != nil {
		return nil, fmt.Errorf("could not enqueue pod restart operation")
	}
	return op, nil
}

func (s *Server) enqueueArgoPodLogOperationGorm(ctx context.Context, tx *gorm.DB, input map[string]any) (map[string]any, error) {
	projectID := cleanOptionalID(fmt.Sprint(input["project_id"]))
	targetID := cleanOptionalID(fmt.Sprint(input["deployment_target_id"]))
	podName := cleanOptionalText(fmt.Sprint(input["pod_name"]))
	if projectID == "" || targetID == "" || podName == "" {
		return nil, fmt.Errorf("invalid pod log operation input")
	}
	title := "retrieve pod logs for " + podName
	op, err := enqueueOperationGorm(ctx, tx, projectID, "", "argo.pod_logs", title, input, []string{"argo", "kubernetes"}, "control-worker")
	if err != nil {
		return nil, fmt.Errorf("could not enqueue pod log operation")
	}
	return op, nil
}

func argoPodLogQueryPreview(podName, containerName string, tailLines, sinceSeconds int, target map[string]any, auditRows ...[]map[string]any) map[string]any {
	return argoPodLogQueryPreviewWithConfig(Config{}, podName, containerName, tailLines, sinceSeconds, target, auditRows...)
}
