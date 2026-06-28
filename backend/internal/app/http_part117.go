package app

import (
	"strings"
)

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
	evidenceObserved := totalRuns > 0
	recordingReady := recordingState == "recorded"
	recordingReason := "sanitized_ssh_result_recorded"
	blockedReasons := []string{}
	completedWithoutExitCode := intFromAny(evidence["completed_without_exit_code_runs"], 0)
	message := "SSH rehearsal has recorded sanitized command-run metadata only; command output, raw errors, and auth material remain suppressed."
	if !evidenceObserved {
		recordingState = "blocked"
		recordingReason = "ssh_rehearsal_execution_not_performed"
		blockedReasons = []string{"ssh_rehearsal_execution_not_performed", "sanitized_ssh_result_not_recorded", "canonical_asset_sync_not_performed"}
		message = "SSH rehearsal results are not recorded by this preview; future execution must persist sanitized metadata without command output or auth material."
	} else {
		if !recordingReady {
			blockedReasons = append(blockedReasons, "sanitized_ssh_result_not_recorded")
		}
		if completedWithoutExitCode > 0 {
			blockedReasons = append(blockedReasons, "ssh_completed_result_exit_code_missing")
		}
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
		"mode":                             "ssh_rehearsal_result_recording_plan",
		"recording_state":                  recordingState,
		"recording_ready":                  recordingReady,
		"recording_ready_reason":           recordingReason,
		"recording_enabled":                recordingReady,
		"result_written":                   recordingReady,
		"operation_log_written":            false,
		"canonical_asset_sync_queued":      false,
		"status_snapshot_write_eligible":   false,
		"status_snapshot_written":          false,
		"auth_binding_recorded":            recordingReady,
		"verify_result_recorded":           verifyRuns > 0,
		"exec_result_recorded":             execRuns > 0,
		"stdout_included":                  false,
		"stderr_included":                  false,
		"raw_error_included":               false,
		"private_key_included":             false,
		"known_hosts_included":             false,
		"authorization_header_included":    false,
		"sanitized_metadata_only":          true,
		"has_failures":                     boolOnlyFromAny(evidence["has_failures"]),
		"has_cancellations":                boolOnlyFromAny(evidence["has_cancellations"]),
		"active_runs":                      intFromAny(evidence["active_runs"], 0),
		"terminal_runs":                    intFromAny(evidence["terminal_runs"], 0),
		"completed_without_exit_code_runs": completedWithoutExitCode,
		"required_result_fields":           []string{"operation_run_id", "ssh_machine_id", "operation_type", "status", "exit_code", "started_at", "finished_at", "auth_binding_status", "verify_result_status", "exec_result_status", "sanitization_status"},
		"suppressed_fields":                []string{"private_key", "passphrase", "known_hosts_body", "command", "stdout", "stderr", "raw_error", "runtime_secret"},
		"blocked_reasons":                  blockedReasons,
		"message":                          message,
	}
}
