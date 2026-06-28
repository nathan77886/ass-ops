package app

import (
	"fmt"
	"strings"
)

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
