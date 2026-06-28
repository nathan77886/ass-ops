package app

func sshTargetEnvironmentAttestationPlan(metadataReady bool, controlEvidence, environmentProofPlan, evidence map[string]any) map[string]any {
	runbookRecorded := boolOnlyFromAny(controlEvidence["runbook_reference_recorded"])
	fixtureRecorded := boolOnlyFromAny(controlEvidence["fixture_reference_recorded"])
	operatorApprovalRecorded := boolOnlyFromAny(controlEvidence["operator_approval_recorded"])
	targetEnvironmentProofObserved := boolOnlyFromAny(environmentProofPlan["target_environment_reference_recorded"]) &&
		boolOnlyFromAny(environmentProofPlan["operator_environment_proof_recorded"])
	verifyObserved := boolOnlyFromAny(environmentProofPlan["completed_verify_evidence"])
	execObserved := boolOnlyFromAny(environmentProofPlan["completed_exec_evidence"])
	sanitizedResultRecorded := boolOnlyFromAny(environmentProofPlan["sanitized_result_recorded"]) &&
		cleanPreviewString(evidence["evidence_state"]) == "recorded"
	readyForReview := metadataReady && runbookRecorded && fixtureRecorded && operatorApprovalRecorded &&
		targetEnvironmentProofObserved && verifyObserved && execObserved && sanitizedResultRecorded

	attestationState := "blocked"
	readyReason := "ssh_target_environment_attestation_machine_metadata_incomplete"
	switch {
	case readyForReview:
		attestationState = "ready_for_operator_review"
		readyReason = "ssh_target_environment_attestation_ready_for_operator_review"
	case metadataReady && (runbookRecorded || fixtureRecorded || operatorApprovalRecorded || targetEnvironmentProofObserved || verifyObserved || execObserved || sanitizedResultRecorded):
		attestationState = "partial"
		readyReason = "ssh_target_environment_attestation_incomplete"
	case metadataReady:
		attestationState = "planned"
		readyReason = "ssh_target_environment_attestation_not_recorded"
	}

	blockedReasons := []string{}
	if !metadataReady {
		blockedReasons = append(blockedReasons, "machine_metadata_incomplete")
	}
	if !runbookRecorded {
		blockedReasons = append(blockedReasons, "runbook_reference_not_recorded")
	}
	if !fixtureRecorded {
		blockedReasons = append(blockedReasons, "authorized_machine_fixture_not_recorded")
	}
	if !operatorApprovalRecorded {
		blockedReasons = append(blockedReasons, "operator_approval_proof_not_recorded")
	}
	if !targetEnvironmentProofObserved {
		blockedReasons = append(blockedReasons, "target_environment_proof_not_recorded")
	}
	if !verifyObserved {
		blockedReasons = append(blockedReasons, "completed_ssh_verify_not_recorded")
	}
	if !execObserved {
		blockedReasons = append(blockedReasons, "completed_ssh_exec_not_recorded")
	}
	if !sanitizedResultRecorded {
		blockedReasons = append(blockedReasons, "sanitized_ssh_result_not_recorded")
	}

	return map[string]any{
		"mode":                                "ssh_target_environment_attestation_plan",
		"attestation_state":                   attestationState,
		"attestation_ready_for_review":        readyForReview,
		"attestation_ready_reason":            readyReason,
		"machine_metadata_ready":              metadataReady,
		"runbook_reference_observed":          runbookRecorded,
		"authorized_machine_fixture_observed": fixtureRecorded,
		"operator_approval_proof_observed":    operatorApprovalRecorded,
		"target_environment_proof_observed":   targetEnvironmentProofObserved,
		"verify_result_observed":              verifyObserved,
		"exec_result_observed":                execObserved,
		"sanitized_result_recorded":           sanitizedResultRecorded,
		"environment_probe_performed":         false,
		"ssh_process_started":                 false,
		"ssh_verify_executed":                 false,
		"ssh_exec_executed":                   false,
		"raw_output_recorded":                 false,
		"operator_identity_recorded":          false,
		"key_material_included":               false,
		"external_call_made":                  false,
		"required_attestation_fields":         []string{"runbook_reference", "authorized_machine_fixture", "operator_approval_proof", "target_environment_proof", "completed_verify", "completed_exec", "sanitized_result_recording"},
		"disabled_backends":                   []string{"environment_probe", "ssh_process_start", "ssh_verify_execute", "ssh_exec_execute", "raw_output_recording", "operator_identity_recording"},
		"suppressed_fields":                   []string{"runbook_url", "runbook_path", "environment_identifier", "fixture_identifier", "operator_identity", "operator_notes", "ssh_host", "ssh_user", "ssh_key_material", "command", "stdout", "stderr", "raw_output", "known_hosts"},
		"blocked_reasons":                     blockedReasons,
		"message":                             "Target environment attestation is a redacted operator-review preflight only; it does not probe the environment, start SSH, execute verify/exec, record raw output, or include operator identity.",
	}
}

func sshRehearsalEnvironmentProofPlan(metadata map[string]any, metadataReady, hasVerified, hasExecuted bool, controlEvidence, evidence map[string]any) map[string]any {
	environmentReferenceRecorded := firstNonEmptyString(
		stringFromMap(metadata, "live_rehearsal_environment"),
		stringFromMap(metadata, "rehearsal_environment"),
		stringFromMap(metadata, "authorized_environment"),
		stringFromMap(metadata, "target_environment"),
		stringFromMap(metadata, "environment_id"),
	) != ""
	operatorEnvironmentProofRecorded := boolOnlyFromAny(metadata["operator_environment_approved"]) ||
		boolOnlyFromAny(metadata["environment_proof_recorded"]) ||
		firstNonEmptyString(
			stringFromMap(metadata, "operator_environment_approval_id"),
			stringFromMap(metadata, "operator_environment_approved_at"),
			stringFromMap(metadata, "operator_environment_proof_id"),
			stringFromMap(metadata, "environment_proof_id"),
			stringFromMap(metadata, "environment_proof_recorded_at"),
		) != ""
	controlsReady := boolOnlyFromAny(controlEvidence["controls_ready"])
	sanitizedResultRecorded := cleanPreviewString(evidence["evidence_state"]) == "recorded"
	environmentProofReady := metadataReady && controlsReady && environmentReferenceRecorded && operatorEnvironmentProofRecorded && hasVerified && hasExecuted && sanitizedResultRecorded
	proofState := "blocked"
	proofReason := "ssh_environment_proof_machine_metadata_incomplete"
	switch {
	case environmentProofReady:
		proofState = "ready"
		proofReason = "authorized_machine_environment_proof_ready"
	case metadataReady && (environmentReferenceRecorded || operatorEnvironmentProofRecorded || controlsReady || hasVerified || hasExecuted):
		proofState = "partial"
		proofReason = "authorized_machine_environment_proof_incomplete"
	case metadataReady:
		proofState = "planned"
		proofReason = "authorized_machine_environment_proof_not_recorded"
	}
	missing := []string{}
	if !metadataReady {
		missing = append(missing, "machine_metadata")
	}
	if !environmentReferenceRecorded {
		missing = append(missing, "target_environment_reference")
	}
	if !boolOnlyFromAny(controlEvidence["fixture_reference_recorded"]) {
		missing = append(missing, "authorized_machine_fixture")
	}
	if !boolOnlyFromAny(controlEvidence["operator_approval_recorded"]) {
		missing = append(missing, "operator_approval_proof")
	}
	if !operatorEnvironmentProofRecorded {
		missing = append(missing, "operator_environment_proof")
	}
	if !hasVerified {
		missing = append(missing, "completed_ssh_verify")
	}
	if !hasExecuted {
		missing = append(missing, "completed_ssh_exec")
	}
	if !sanitizedResultRecorded {
		missing = append(missing, "sanitized_ssh_result_recorded")
	}
	return map[string]any{
		"mode":                                  "ssh_rehearsal_environment_proof_plan",
		"environment_proof_state":               proofState,
		"environment_proof_ready":               environmentProofReady,
		"environment_proof_ready_reason":        proofReason,
		"machine_metadata_ready":                metadataReady,
		"target_environment_reference_recorded": environmentReferenceRecorded,
		"authorized_fixture_recorded":           boolOnlyFromAny(controlEvidence["fixture_reference_recorded"]),
		"operator_approval_recorded":            boolOnlyFromAny(controlEvidence["operator_approval_recorded"]),
		"operator_environment_proof_recorded":   operatorEnvironmentProofRecorded,
		"completed_verify_evidence":             hasVerified,
		"completed_exec_evidence":               hasExecuted,
		"sanitized_result_recorded":             sanitizedResultRecorded,
		"external_call_made":                    false,
		"ssh_process_started":                   false,
		"command_executed":                      false,
		"environment_probe_performed":           false,
		"operator_identity_included":            false,
		"operator_note_included":                false,
		"fixture_identifier_included":           false,
		"environment_identifier_included":       false,
		"stdout_included":                       false,
		"stderr_included":                       false,
		"private_key_included":                  false,
		"known_hosts_included":                  false,
		"runtime_secret_included":               false,
		"required_evidence":                     []string{"target_environment_reference", "authorized_machine_fixture", "operator_approval_proof", "operator_environment_proof", "completed_ssh_verify", "completed_ssh_exec", "sanitized_ssh_result_recorded"},
		"missing_evidence":                      missing,
		"disabled_backends":                     []string{"environment_probe", "ssh_process_start", "ssh_verify_execute", "ssh_exec_execute", "raw_output_recording", "operator_identity_recording"},
		"suppressed_fields":                     []string{"live_rehearsal_environment", "rehearsal_environment", "authorized_environment", "target_environment", "environment_id", "authorized_machine_fixture", "authorized_fixture_id", "fixture_id", "fixture_name", "operator_approved_by", "operator_approval_note", "operator_environment_approval_id", "operator_environment_approved_at", "operator_environment_proof_id", "environment_proof_id", "environment_proof_recorded_at", "private_key", "passphrase", "known_hosts_body", "stdout", "stderr", "raw_error", "runtime_secret"},
		"message":                               "SSH environment proof is reconciled as booleans only; target environment identifiers, fixture identifiers, operator identity, auth material, and command output remain suppressed.",
	}
}

func sshRehearsalLiveControlEvidence(metadata map[string]any, metadataReady, hasVerified, hasExecuted bool) map[string]any {
	runbookRecorded := firstNonEmptyString(
		stringFromMap(metadata, "live_rehearsal_runbook"),
		stringFromMap(metadata, "rehearsal_runbook"),
		stringFromMap(metadata, "runbook_url"),
		stringFromMap(metadata, "runbook_path"),
	) != ""
	fixtureRecorded := firstNonEmptyString(
		stringFromMap(metadata, "authorized_machine_fixture"),
		stringFromMap(metadata, "authorized_fixture_id"),
		stringFromMap(metadata, "fixture_id"),
		stringFromMap(metadata, "fixture_name"),
	) != ""
	operatorApprovalRecorded := boolOnlyFromAny(metadata["operator_approved"]) ||
		firstNonEmptyString(
			stringFromMap(metadata, "operator_approval_id"),
			stringFromMap(metadata, "operator_approved_at"),
			stringFromMap(metadata, "operator_approved_by"),
		) != ""
	controlsReady := metadataReady && hasVerified && hasExecuted && runbookRecorded && fixtureRecorded && operatorApprovalRecorded
	controlState := "blocked"
	controlReadyReason := "ssh_live_rehearsal_machine_metadata_incomplete"
	switch {
	case controlsReady:
		controlState = "ready"
		controlReadyReason = "authorized_machine_live_rehearsal_controls_recorded"
	case metadataReady && (runbookRecorded || fixtureRecorded || operatorApprovalRecorded || hasVerified || hasExecuted):
		controlState = "partial"
		controlReadyReason = "authorized_machine_live_rehearsal_controls_incomplete"
	case metadataReady:
		controlState = "planned"
		controlReadyReason = "authorized_machine_live_rehearsal_controls_not_recorded"
	}
	missing := []string{}
	if !metadataReady {
		missing = append(missing, "machine_metadata")
	}
	if !runbookRecorded {
		missing = append(missing, "live_rehearsal_runbook")
	}
	if !fixtureRecorded {
		missing = append(missing, "authorized_machine_fixture")
	}
	if !operatorApprovalRecorded {
		missing = append(missing, "operator_approval_proof")
	}
	if !hasVerified {
		missing = append(missing, "completed_ssh_verify")
	}
	if !hasExecuted {
		missing = append(missing, "completed_ssh_exec")
	}
	return map[string]any{
		"mode":                        "ssh_live_rehearsal_control_evidence",
		"control_state":               controlState,
		"controls_ready":              controlsReady,
		"control_ready_reason":        controlReadyReason,
		"machine_metadata_ready":      metadataReady,
		"runbook_reference_recorded":  runbookRecorded,
		"fixture_reference_recorded":  fixtureRecorded,
		"operator_approval_recorded":  operatorApprovalRecorded,
		"completed_verify_evidence":   hasVerified,
		"completed_exec_evidence":     hasExecuted,
		"external_call_made":          false,
		"ssh_process_started":         false,
		"command_executed":            false,
		"contains_runbook_body":       false,
		"contains_fixture_identifier": false,
		"contains_operator_identity":  false,
		"contains_operator_note":      false,
		"contains_private_key":        false,
		"contains_known_hosts_body":   false,
		"contains_stdout":             false,
		"contains_stderr":             false,
		"required_evidence":           []string{"live_rehearsal_runbook", "authorized_machine_fixture", "operator_approval_proof", "completed_ssh_verify", "completed_ssh_exec"},
		"missing_evidence":            missing,
		"suppressed_fields":           []string{"live_rehearsal_runbook", "rehearsal_runbook", "runbook_url", "runbook_path", "runbook_body", "authorized_machine_fixture", "authorized_fixture_id", "fixture_id", "fixture_name", "operator_approved", "operator_approval_id", "operator_approved_by", "operator_approved_at", "operator_approval_note", "private_key", "passphrase", "known_hosts_body", "stdout", "stderr", "raw_error", "runtime_secret"},
		"message":                     "SSH live rehearsal controls are reconciled from metadata as booleans only; runbook, fixture, operator identity, auth material, and command output remain suppressed.",
	}
}
