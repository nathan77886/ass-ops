package app

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) recordSSHMachineRehearsalSnapshot(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	machine, err := sshMachineForRehearsalSnapshot(r.Context(), s.store.Gorm, machineID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := cleanPreviewString(machine["project_id"])
	if projectID == "" {
		writeError(w, http.StatusInternalServerError, "SSH machine has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: projectID}, "update") {
		return
	}
	result, err := RecordSSHMachineRehearsalSnapshot(r.Context(), s.store, SSHMachineRehearsalSnapshotOptions{
		MachineID: machineID,
		DryRun:    req.DryRun,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("ssh rehearsal snapshot failed", "ssh_machine_id", machineID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record SSH rehearsal snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func buildSSHMachineRehearsalPreview(machine map[string]any, runs []map[string]any, proofEvidenceOpt ...map[string]any) map[string]any {
	host := cleanPreviewString(machine["host"])
	username := cleanPreviewString(machine["username"])
	authType := cleanPreviewString(machine["auth_type"])
	portValue := intFromAny(machine["port"], 22)
	metadata := mapFromAny(machine["metadata"])
	hasKeyReference := cleanPreviewString(metadata["key_path"]) != ""
	hasKnownHostsReference := cleanPreviewString(metadata["known_hosts_path"]) != ""
	strictHostKeyChecking := cleanPreviewString(metadata["strict_host_key_checking"])
	if strictHostKeyChecking == "" {
		strictHostKeyChecking = "accept-new"
	}
	metadataReady := host != "" && username != "" && authType != "" && portValue > 0 && portValue <= 65535

	evidence := summarizeSSHRehearsalEvidence(runs)
	hasVerified := boolOnlyFromAny(evidence["completed_verify"])
	hasExecuted := boolOnlyFromAny(evidence["completed_exec"])
	state := "planned"
	if !metadataReady {
		state = "blocked"
	} else if hasVerified && hasExecuted {
		state = "ready"
	} else if intFromAny(evidence["total_runs"], 0) > 0 {
		state = "partial"
	}

	verifyStatus := "planned"
	if !metadataReady {
		verifyStatus = "blocked"
	} else if hasVerified {
		verifyStatus = "completed"
	}
	execStatus := "blocked"
	if hasExecuted {
		execStatus = "completed"
	} else if hasVerified {
		execStatus = "planned"
	}

	requiredLiveRehearsal := []string{}
	if !hasVerified {
		requiredLiveRehearsal = append(requiredLiveRehearsal, "ssh.verify")
	}
	if !hasExecuted {
		requiredLiveRehearsal = append(requiredLiveRehearsal, "ssh.exec")
	}
	approvalPlan := sshRehearsalApprovalRequestPlan(metadataReady, hasVerified, hasExecuted)
	resultRecordingPlan := sshRehearsalResultRecordingPlan(evidence)
	authBindingPlan := sshRehearsalAuthBindingPlan(metadataReady, authType, hasKeyReference, hasKnownHostsReference)
	verifyPlan := sshRehearsalVerifyExecutionPlan(metadataReady, hasVerified)
	execPlan := sshRehearsalExecExecutionPlan(metadataReady, hasVerified, hasExecuted)
	liveControlEvidence := sshRehearsalLiveControlEvidence(metadata, metadataReady, hasVerified, hasExecuted)
	environmentProofPlan := sshRehearsalEnvironmentProofPlan(metadata, metadataReady, hasVerified, hasExecuted, liveControlEvidence, evidence)
	targetEnvironmentAttestationPlan := sshTargetEnvironmentAttestationPlan(metadataReady, liveControlEvidence, environmentProofPlan, evidence)
	proofEvidence := map[string]any{
		"mode":                    "ssh_target_environment_proof_registration",
		"proof_state":             "not_recorded",
		"proof_registered":        false,
		"asset_status_observed":   false,
		"external_call_made":      false,
		"ssh_process_started":     false,
		"command_executed":        false,
		"raw_output_recorded":     false,
		"private_key_included":    false,
		"operator_identity_saved": false,
	}
	if len(proofEvidenceOpt) > 0 && proofEvidenceOpt[0] != nil {
		proofEvidence = proofEvidenceOpt[0]
	}

	steps := []map[string]any{
		{
			"kind":   "machine_metadata",
			"status": statusWhen(metadataReady),
			"checks": []string{"host", "port", "username", "auth_type"},
			"reason": reasonWhen(metadataReady, "machine metadata is complete", "host, username, auth_type, and valid port are required"),
		},
		{
			"kind":   "auth_material_reference",
			"status": statusWhen(authType != ""),
			"checks": []string{"auth_type", "runtime_secret_binding"},
			"reason": reasonWhen(authType != "", "auth material must be resolved by the runtime worker", "auth_type is required before a live SSH rehearsal"),
		},
		{
			"kind":   "known_hosts_policy",
			"status": "planned",
			"checks": []string{"known_hosts_reference", "strict_host_key_checking"},
			"reason": map[string]any{
				"known_hosts_reference_present": hasKnownHostsReference,
				"strict_host_key_checking":      strictHostKeyChecking,
			},
		},
		{
			"kind":   "verify_rehearsal",
			"status": verifyStatus,
			"checks": []string{"POST /api/ssh-machines/{id}/verify", "ssh.verify operation evidence"},
			"reason": reasonWhen(metadataReady, "verify can be queued after operator approval and runtime auth binding", "machine metadata is incomplete"),
		},
		{
			"kind":   "exec_rehearsal",
			"status": execStatus,
			"checks": []string{"POST /api/ssh-machines/{id}/commands", "ssh.exec operation evidence", "operator command review"},
			"reason": reasonWhen(hasVerified || hasExecuted, "exec rehearsal can follow a successful verify rehearsal", "complete ssh.verify evidence first"),
		},
		{
			"kind":   "live_rehearsal_controls",
			"status": liveControlEvidence["control_state"],
			"checks": []string{"authorized_machine_fixture", "live_rehearsal_runbook", "operator_approval_proof"},
			"reason": liveControlEvidence["control_ready_reason"],
		},
	}

	return map[string]any{
		"mode":                                   "ssh_rehearsal_plan_preview",
		"rehearsal_state":                        state,
		"execution_enabled":                      false,
		"external_call_made":                     false,
		"ssh_process_started":                    false,
		"command_executed":                       false,
		"stdout_included":                        false,
		"stderr_included":                        false,
		"private_key_included":                   false,
		"known_hosts_included":                   false,
		"secret_included":                        false,
		"live_evidence_recorded":                 boolOnlyFromAny(evidence["has_live_evidence"]),
		"sanitized_result_recorded":              cleanPreviewString(evidence["evidence_state"]) == "recorded",
		"result_recording_state":                 resultRecordingPlan["recording_state"],
		"auth_reference_present":                 hasKeyReference || authType != "",
		"known_hosts_configured":                 hasKnownHostsReference,
		"approval_request_plan":                  approvalPlan,
		"auth_binding_plan":                      authBindingPlan,
		"verify_execution_plan":                  verifyPlan,
		"exec_execution_plan":                    execPlan,
		"result_recording_plan":                  resultRecordingPlan,
		"live_rehearsal_control_evidence":        liveControlEvidence,
		"live_rehearsal_controls_ready":          liveControlEvidence["controls_ready"],
		"environment_proof_plan":                 environmentProofPlan,
		"environment_proof_ready":                environmentProofPlan["environment_proof_ready"],
		"target_environment_attestation_plan":    targetEnvironmentAttestationPlan,
		"target_environment_attestation_ready":   targetEnvironmentAttestationPlan["attestation_ready_for_review"],
		"target_environment_proof_registration":  proofEvidence,
		"target_environment_proof_registered":    boolOnlyFromAny(proofEvidence["proof_registered"]),
		"target_environment_proof_state":         cleanPreviewString(proofEvidence["proof_state"]),
		"target_environment_proof_registered_at": proofEvidence["proof_registered_at"],
		"operator_approved_proof_recorded":       liveControlEvidence["operator_approval_recorded"],
		"required_live_rehearsal":                requiredLiveRehearsal,
		"required_controls": []string{
			"machine_metadata_review",
			"ssh_auth_material_binding",
			"known_hosts_review",
			"operation_approval",
			"operator_command_review",
			"live_rehearsal_runbook",
			"authorized_machine_fixture",
		},
		"suppressed_fields": []string{
			"private_key",
			"passphrase",
			"known_hosts_body",
			"stdout",
			"stderr",
			"raw_error",
			"command_output",
			"runbook_url",
			"runbook_path",
			"fixture_id",
			"fixture_name",
			"operator_approved_by",
			"operator_approval_note",
		},
		"execution_blockers": approvalPlan["execution_blockers"],
		"machine": map[string]any{
			"id":         machine["id"],
			"project_id": machine["project_id"],
			"name":       machine["name"],
			"host":       host,
			"port":       portValue,
			"username":   username,
			"auth_type":  authType,
		},
		"steps":           steps,
		"recent_evidence": evidence,
	}
}
