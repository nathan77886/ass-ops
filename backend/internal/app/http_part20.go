package app

import (
	"fmt"
	"strings"
)

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
	} else if workflowState == "unknown" {
		recordingState = "blocked"
		recordingReason = "config_git_commit_audit_operation_unknown"
	} else if workflowState == "recorded" && workflowObserved && !workflowRecorded {
		recordingState = "blocked"
		recordingReason = "config_git_commit_audit_operation_log_missing"
	} else if pinObserved && liveObserved && workflowRecorded {
		recordingState = "recorded"
		recordingReason = "audit_result_pin_and_live_validation_observed"
	} else if workflowRecorded {
		recordingState = "audit_recorded"
		recordingReason = "sanitized_config_git_workflow_audit_result_observed"
	} else if pinObserved {
		recordingState = "partial"
		recordingReason = "project_version_config_commit_pin_observed"
	}
	resultWritten := workflowRecorded
	operationLogWritten := intFromAny(workflowEvidence["operation_log_count"], 0) > 0
	blockedReasons := []string{
		"project_version_config_commit_pin_not_written",
		"live_remote_commit_validation_not_performed",
	}
	if !resultWritten {
		blockedReasons = append([]string{recordingReason}, blockedReasons...)
	}
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
		"blocked_reasons": blockedReasons,
		"message":         "Config Git workflow result recording only reconciles sanitized audit operation metadata; no scaffold artifact, Git result, provider review, ProjectVersion pin write, or live validation record is persisted.",
	}
}
