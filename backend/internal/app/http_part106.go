package app

import (
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func (s *Server) listDeploymentTargets(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_target", ProjectID: projectID}, "read") {
		return
	}
	items, err := s.deploymentTargetMapsGorm(r.Context(), projectID, 500)
	enrichDeploymentTargetsWithExecutionReadiness(items)
	writeQueryResult(w, items, err)
}

func (s *Server) deploymentTargetExecutionGate(w http.ResponseWriter, r *http.Request) {
	targetID := cleanOptionalID(chi.URLParam(r, "id"))
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "deployment target id is required")
		return
	}
	target, err := s.loadDeploymentTargetForExecutionGateGorm(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "deployment target not found")
			return
		}
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanOptionalID(fmt.Sprint(target["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_target", ID: targetID, ProjectID: projectID}, "read") {
		return
	}
	writeJSON(w, http.StatusOK, deploymentTargetExecutionGatePayload(target))
}

func (s *Server) listDeploymentTargetPods(w http.ResponseWriter, r *http.Request) {
	targetID := cleanOptionalID(chi.URLParam(r, "id"))
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "deployment target id is required")
		return
	}
	target, err := s.loadDeploymentTargetForKubernetesAccessGorm(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "deployment target not found")
			return
		}
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanOptionalID(fmt.Sprint(target["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_target", ID: targetID, ProjectID: projectID}, "read") {
		return
	}
	plan := kubernetesPodLogBackendPlan(s.cfg, target)
	if !boolOnlyFromAny(plan["ready"]) {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                  "deployment_target_pod_metadata",
			"backend":               "kubernetes_client_get_pods",
			"backend_state":         "blocked",
			"result_scope":          "sanitized_pod_metadata",
			"deployment_target":     sanitizedDeploymentTargetForPodMetadata(target),
			"backend_plan":          plan,
			"items":                 []map[string]any{},
			"item_count":            0,
			"kubernetes_api_call":   false,
			"raw_response_included": false,
			"secret_included":       false,
			"log_body_included":     false,
			"message":               "Pod metadata listing is blocked until the Kubernetes log backend and reviewed namespace kubeconfig are ready.",
		})
		return
	}
	kubeconfigSecret := ""
	var kube GormKubernetesEnvironment
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormKubernetesEnvironment{
		ProjectID:   projectID,
		Environment: cleanOptionalText(fmt.Sprint(target["environment"])),
		ClusterName: cleanOptionalText(fmt.Sprint(target["cluster_name"])),
		Namespace:   cleanOptionalText(fmt.Sprint(target["namespace"])),
	}).First(&kube).Error; err == nil && strings.TrimSpace(kube.KubeconfigSecretCiphertext) != "" {
		kubeconfigSecret, err = s.decryptWebhookSecret(kube.KubeconfigSecretCiphertext)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"mode":                  "deployment_target_pod_metadata",
				"backend":               "kubernetes_client_get_pods",
				"backend_state":         "blocked",
				"result_scope":          "sanitized_pod_metadata",
				"deployment_target":     sanitizedDeploymentTargetForPodMetadata(target),
				"backend_plan":          plan,
				"items":                 []map[string]any{},
				"item_count":            0,
				"kubernetes_api_call":   false,
				"raw_response_included": false,
				"secret_included":       false,
				"log_body_included":     false,
				"message":               "decrypting kubeconfig secret failed",
			})
			return
		}
	}
	result, err := runKubernetesPodList(r.Context(), s.cfg, kubernetesPodListRequest{
		DeploymentTargetID: targetID,
		Environment:        cleanOptionalText(fmt.Sprint(target["environment"])),
		ClusterName:        cleanOptionalText(fmt.Sprint(target["cluster_name"])),
		Namespace:          cleanOptionalText(fmt.Sprint(target["namespace"])),
		KubeconfigRef:      cleanOptionalText(fmt.Sprint(target["kubeconfig_secret_ref"])),
		KubeconfigSecret:   kubeconfigSecret,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                  "deployment_target_pod_metadata",
			"backend":               "kubernetes_client_get_pods",
			"backend_state":         cleanPreviewString(result["backend_state"]),
			"result_scope":          "sanitized_pod_metadata",
			"deployment_target":     sanitizedDeploymentTargetForPodMetadata(target),
			"backend_plan":          plan,
			"items":                 []map[string]any{},
			"item_count":            0,
			"kubernetes_api_call":   boolOnlyFromAny(result["kubernetes_api_call"]),
			"raw_response_included": false,
			"secret_included":       false,
			"log_body_included":     false,
			"message":               err.Error(),
		})
		return
	}
	result["mode"] = "deployment_target_pod_metadata"
	result["deployment_target"] = sanitizedDeploymentTargetForPodMetadata(target)
	result["backend_plan"] = plan
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listDeploymentRecords(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "deployment_record", ProjectID: projectID}, "read") {
		return
	}
	items, err := s.deploymentRecordMapsGorm(r.Context(), projectID, 500)
	writeQueryResult(w, items, err)
}

func sanitizedDeploymentTargetForPodMetadata(target map[string]any) map[string]any {
	return map[string]any{
		"id":                            cleanOptionalID(fmt.Sprint(target["id"])),
		"project_id":                    cleanOptionalID(fmt.Sprint(target["project_id"])),
		"name":                          cleanOptionalText(fmt.Sprint(target["name"])),
		"environment":                   cleanOptionalText(fmt.Sprint(target["environment"])),
		"cluster_name":                  cleanOptionalText(fmt.Sprint(target["cluster_name"])),
		"namespace":                     cleanOptionalText(fmt.Sprint(target["namespace"])),
		"status":                        cleanOptionalText(fmt.Sprint(target["status"])),
		"kubernetes_environment_id":     cleanOptionalID(fmt.Sprint(target["kubernetes_environment_id"])),
		"kubernetes_environment_name":   cleanOptionalText(fmt.Sprint(target["kubernetes_environment_name"])),
		"kubeconfig_secret_ref_present": boolOnlyFromAny(target["kubeconfig_secret_ref_present"]),
		"service_account_present":       boolOnlyFromAny(target["service_account_present"]),
		"token_subject_review_status":   cleanPreviewString(target["token_subject_review_status"]),
		"rbac_read_logs_status":         cleanPreviewString(target["rbac_read_logs_status"]),
		"rbac_restart_pods_status":      cleanPreviewString(target["rbac_restart_pods_status"]),
		"kubernetes_environment_status": cleanPreviewString(target["kubernetes_environment_status"]),
	}
}

func deploymentTargetExecutionGatePayload(target map[string]any) map[string]any {
	readiness := deploymentExecutionReadiness(target)
	executionPlan := mapFromAny(readiness["execution_plan"])
	return map[string]any{
		"mode":                            "deployment_target_execution_gate",
		"execution_gate_state":            "deployment_execution_gate_blocked",
		"execution_gate_ready":            false,
		"deployment_target_id":            cleanOptionalID(fmt.Sprint(target["id"])),
		"project_id":                      cleanOptionalID(fmt.Sprint(target["project_id"])),
		"deployment_target_name":          cleanOptionalText(fmt.Sprint(target["name"])),
		"environment":                     cleanOptionalText(fmt.Sprint(target["environment"])),
		"cluster_name_observed":           cleanOptionalText(fmt.Sprint(target["cluster_name"])) != "",
		"namespace_observed":              cleanOptionalText(fmt.Sprint(target["namespace"])) != "",
		"argo_app_count":                  intFromAny(target["argo_app_count"], 0),
		"readiness_state":                 cleanOptionalText(fmt.Sprint(readiness["status"])),
		"readiness":                       readiness,
		"execution_plan":                  executionPlan,
		"target_metadata_ready":           boolOnlyFromAny(executionPlan["target_metadata_ready"]),
		"requires_approval":               true,
		"requires_environment_review":     true,
		"requires_kubeconfig_binding":     true,
		"requires_manifest_render":        true,
		"requires_dry_run_preflight":      true,
		"requires_rollback_plan":          true,
		"requires_operator_confirmation":  true,
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
		"argocd_api_call_made":            false,
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
		"disabled_backends":               stringSliceFromAny(executionPlan["disabled_backends"]),
		"suppressed_fields":               stringSliceFromAny(executionPlan["suppressed_fields"]),
		"missing_evidence":                stringSliceFromAny(executionPlan["blocked_reasons"]),
		"message":                         "Deployment execution gate is blocked; Helm, kubectl, Argo rollout, kubeconfig binding, manifest rendering, dry-run, rollback selection, and rollout mutation remain disabled.",
	}
}

func rollbackPointExecutionGatePayload(rollbackPoint map[string]any) map[string]any {
	readiness, readinessReason := rollbackPointReadiness(rollbackPoint)
	executionPlan := rollbackExecutionPlan(readiness, "read_only_preview")
	return map[string]any{
		"mode":                           "rollback_point_execution_gate",
		"execution_gate_state":           "rollback_execution_gate_blocked",
		"execution_gate_ready":           false,
		"rollback_point_id":              cleanOptionalID(fmt.Sprint(rollbackPoint["id"])),
		"project_id":                     cleanOptionalID(fmt.Sprint(rollbackPoint["project_id"])),
		"deployment_target_id":           cleanOptionalID(fmt.Sprint(rollbackPoint["deployment_target_id"])),
		"deployment_record_id":           cleanOptionalID(fmt.Sprint(rollbackPoint["deployment_record_id"])),
		"rollback_point_name":            cleanOptionalText(fmt.Sprint(rollbackPoint["name"])),
		"environment":                    cleanOptionalText(fmt.Sprint(rollbackPoint["environment"])),
		"deployment_target_name":         cleanOptionalText(fmt.Sprint(rollbackPoint["deployment_target_name"])),
		"deployment_namespace_observed":  cleanOptionalText(fmt.Sprint(rollbackPoint["deployment_namespace"])) != "",
		"deployment_cluster_observed":    cleanOptionalText(fmt.Sprint(rollbackPoint["deployment_cluster_name"])) != "",
		"readiness_state":                readiness,
		"readiness_reason":               readinessReason,
		"rollback_execution_plan":        executionPlan,
		"target_metadata_ready":          cleanOptionalText(fmt.Sprint(rollbackPoint["deployment_target_id"])) != "",
		"revision_metadata_ready":        cleanOptionalText(fmt.Sprint(rollbackPoint["revision"])) != "",
		"requires_approval":              true,
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
		"disabled_backends":              stringSliceFromAny(executionPlan["disabled_backends"]),
		"suppressed_fields":              stringSliceFromAny(executionPlan["suppressed_fields"]),
		"missing_evidence":               stringSliceFromAny(executionPlan["blocked_reasons"]),
		"message":                        "Rollback execution gate is blocked; Helm rollback, kubectl rollout undo, Argo rollback, kubeconfig binding, revision verification, manifest diff, dry-run, and rollback mutation remain disabled.",
	}
}
