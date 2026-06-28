package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
)

func sshMachineAssetID(ctx context.Context, db *gorm.DB, machineID string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("gorm database is not configured")
	}
	var asset GormAsset
	if err := db.WithContext(ctx).
		Where(&GormAsset{AssetType: "host", SourceTable: "ssh_machines", SourceID: validNullString(machineID)}).
		First(&asset).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return "", fmt.Errorf("ssh_machine host asset for %s not found; run db sync-assets first", machineID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(asset.ID)
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("ssh_machine host asset for %s has empty id", machineID)
	}
	return assetID, nil
}

func sshMachineTargetEnvironmentProofEvidence(ctx context.Context, db *gorm.DB, machineID string) (map[string]any, error) {
	result := map[string]any{
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
	assetID, err := sshMachineAssetID(ctx, db, machineID)
	if err != nil {
		result["proof_state"] = "asset_missing"
		return result, nil
	}
	var snapshot GormAssetStatusSnapshot
	if err := db.WithContext(ctx).
		Where(&GormAssetStatusSnapshot{AssetID: assetID, Status: sshTargetEnvironmentProofStatus}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "collected_at"}, Desc: true}).
		First(&snapshot).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return result, nil
		}
		result["proof_state"] = "lookup_failed"
		return result, err
	}
	result["proof_state"] = "recorded"
	result["proof_registered"] = true
	result["asset_status_observed"] = true
	result["status"] = snapshot.Status
	result["health"] = snapshot.Health
	result["proof_registered_at"] = snapshot.CollectedAt
	return result, nil
}

func sshMachineRehearsalSnapshotPayload(preview map[string]any, assetObserved bool) map[string]any {
	machine := mapFromAny(preview["machine"])
	evidence := mapFromAny(preview["recent_evidence"])
	resultPlan := mapFromAny(preview["result_recording_plan"])
	controlEvidence := mapFromAny(preview["live_rehearsal_control_evidence"])
	environmentProof := mapFromAny(preview["environment_proof_plan"])
	attestation := mapFromAny(preview["target_environment_attestation_plan"])
	statusSnapshotWriteEligible := true
	return map[string]any{
		"mode":                                      "ssh_rehearsal_attestation_snapshot",
		"ssh_machine_id":                            cleanPreviewString(machine["id"]),
		"project_id":                                cleanPreviewString(machine["project_id"]),
		"ssh_machine_asset_observed":                assetObserved,
		"rehearsal_state":                           cleanPreviewString(preview["rehearsal_state"]),
		"recent_evidence_state":                     cleanPreviewString(evidence["evidence_state"]),
		"total_runs":                                intFromAny(evidence["total_runs"], 0),
		"verify_runs":                               intFromAny(evidence["verify_runs"], 0),
		"exec_runs":                                 intFromAny(evidence["exec_runs"], 0),
		"unknown_runs":                              intFromAny(evidence["unknown_runs"], 0),
		"completed_runs":                            intFromAny(evidence["completed_runs"], 0),
		"completed_without_exit_code_runs":          intFromAny(evidence["completed_without_exit_code_runs"], 0),
		"failed_runs":                               intFromAny(evidence["failed_runs"], 0),
		"active_runs":                               intFromAny(evidence["active_runs"], 0),
		"canceled_runs":                             intFromAny(evidence["canceled_runs"], 0),
		"completed_verify":                          boolOnlyFromAny(evidence["completed_verify"]),
		"completed_exec":                            boolOnlyFromAny(evidence["completed_exec"]),
		"live_evidence_recorded":                    boolOnlyFromAny(preview["live_evidence_recorded"]),
		"sanitized_result_recorded":                 boolOnlyFromAny(preview["sanitized_result_recorded"]),
		"result_recording_state":                    cleanPreviewString(resultPlan["recording_state"]),
		"result_written":                            boolOnlyFromAny(resultPlan["result_written"]),
		"live_rehearsal_control_state":              cleanPreviewString(controlEvidence["control_state"]),
		"live_rehearsal_controls_ready":             boolOnlyFromAny(controlEvidence["controls_ready"]),
		"runbook_reference_recorded":                boolOnlyFromAny(controlEvidence["runbook_reference_recorded"]),
		"fixture_reference_recorded":                boolOnlyFromAny(controlEvidence["fixture_reference_recorded"]),
		"operator_approval_recorded":                boolOnlyFromAny(controlEvidence["operator_approval_recorded"]),
		"environment_proof_state":                   cleanPreviewString(environmentProof["environment_proof_state"]),
		"environment_proof_ready":                   boolOnlyFromAny(environmentProof["environment_proof_ready"]),
		"target_environment_reference_recorded":     boolOnlyFromAny(environmentProof["target_environment_reference_recorded"]),
		"operator_environment_proof_recorded":       boolOnlyFromAny(environmentProof["operator_environment_proof_recorded"]),
		"target_environment_attestation_state":      cleanPreviewString(attestation["attestation_state"]),
		"target_environment_attestation_ready":      boolOnlyFromAny(attestation["attestation_ready_for_review"]),
		"target_environment_proof_observed":         boolOnlyFromAny(attestation["target_environment_proof_observed"]),
		"verify_result_observed":                    boolOnlyFromAny(attestation["verify_result_observed"]),
		"exec_result_observed":                      boolOnlyFromAny(attestation["exec_result_observed"]),
		"status_snapshot_write_eligible":            statusSnapshotWriteEligible,
		"status_snapshot_written":                   statusSnapshotWriteEligible,
		"canonical_asset_status_snapshot_attempted": assetObserved,
		"external_call_made":                        false,
		"ssh_process_started":                       false,
		"command_executed":                          false,
		"environment_probe_performed":               false,
		"stdout_included":                           false,
		"stderr_included":                           false,
		"raw_error_included":                        false,
		"raw_output_recorded":                       false,
		"private_key_included":                      false,
		"known_hosts_included":                      false,
		"secret_included":                           false,
		"operator_identity_included":                false,
		"operator_note_included":                    false,
		"environment_identifier_included":           false,
		"fixture_identifier_included":               false,
		"sanitized_metadata_only":                   true,
		"suppressed_fields": []string{
			"host", "username", "ssh_host", "ssh_user", "private_key", "passphrase", "known_hosts_body",
			"command", "stdout", "stderr", "raw_error", "raw_output", "runtime_secret", "operator_identity",
			"operator_notes", "environment_identifier", "fixture_identifier", "runbook_url", "runbook_path",
		},
	}
}

func sshMachineTargetEnvironmentProofPayload(snapshot map[string]any) map[string]any {
	return map[string]any{
		"mode":                                  "ssh_target_environment_proof_approval",
		"ssh_machine_id":                        cleanPreviewString(snapshot["ssh_machine_id"]),
		"project_id":                            cleanPreviewString(snapshot["project_id"]),
		"rehearsal_state":                       cleanPreviewString(snapshot["rehearsal_state"]),
		"recent_evidence_state":                 cleanPreviewString(snapshot["recent_evidence_state"]),
		"total_runs":                            intFromAny(snapshot["total_runs"], 0),
		"verify_runs":                           intFromAny(snapshot["verify_runs"], 0),
		"exec_runs":                             intFromAny(snapshot["exec_runs"], 0),
		"completed_verify":                      boolOnlyFromAny(snapshot["completed_verify"]),
		"completed_exec":                        boolOnlyFromAny(snapshot["completed_exec"]),
		"sanitized_result_recorded":             boolOnlyFromAny(snapshot["sanitized_result_recorded"]),
		"live_rehearsal_controls_ready":         boolOnlyFromAny(snapshot["live_rehearsal_controls_ready"]),
		"runbook_reference_recorded":            boolOnlyFromAny(snapshot["runbook_reference_recorded"]),
		"fixture_reference_recorded":            boolOnlyFromAny(snapshot["fixture_reference_recorded"]),
		"operator_approval_recorded":            boolOnlyFromAny(snapshot["operator_approval_recorded"]),
		"environment_proof_ready":               boolOnlyFromAny(snapshot["environment_proof_ready"]),
		"target_environment_reference_recorded": boolOnlyFromAny(snapshot["target_environment_reference_recorded"]),
		"operator_environment_proof_recorded":   boolOnlyFromAny(snapshot["operator_environment_proof_recorded"]),
		"target_environment_attestation_ready":  boolOnlyFromAny(snapshot["target_environment_attestation_ready"]),
		"target_environment_proof_observed":     boolOnlyFromAny(snapshot["target_environment_proof_observed"]),
		"verify_result_observed":                boolOnlyFromAny(snapshot["verify_result_observed"]),
		"exec_result_observed":                  boolOnlyFromAny(snapshot["exec_result_observed"]),
		"operator_approved_target_environment_proof_registered": true,
		"external_call_made":              false,
		"ssh_process_started":             false,
		"command_executed":                false,
		"environment_probe_performed":     false,
		"stdout_included":                 false,
		"stderr_included":                 false,
		"raw_error_included":              false,
		"raw_output_recorded":             false,
		"private_key_included":            false,
		"known_hosts_included":            false,
		"secret_included":                 false,
		"operator_identity_included":      false,
		"operator_note_included":          false,
		"environment_identifier_included": false,
		"fixture_identifier_included":     false,
		"sanitized_metadata_only":         true,
		"suppressed_fields": []string{
			"host", "username", "ssh_host", "ssh_user", "private_key", "passphrase", "known_hosts_body",
			"command", "stdout", "stderr", "raw_error", "raw_output", "runtime_secret", "operator_identity",
			"operator_notes", "environment_identifier", "fixture_identifier", "runbook_url", "runbook_path",
		},
	}
}

func sshMachineRehearsalSnapshotReadiness(preview map[string]any, snapshot map[string]any) (bool, string, []string) {
	missing := make([]string, 0)
	if cleanPreviewString(preview["rehearsal_state"]) != "ready" {
		missing = append(missing, "ssh_rehearsal_not_ready")
	}
	if intFromAny(snapshot["total_runs"], 0) == 0 {
		missing = append(missing, "ssh_rehearsal_evidence_missing")
	}
	if intFromAny(snapshot["active_runs"], 0) > 0 {
		missing = append(missing, "ssh_rehearsal_worker_result_pending")
	}
	if intFromAny(snapshot["failed_runs"], 0) > 0 {
		missing = append(missing, "ssh_rehearsal_failed_result_recorded")
	}
	if !boolOnlyFromAny(snapshot["completed_verify"]) {
		missing = append(missing, "completed_ssh_verify_missing")
	}
	if !boolOnlyFromAny(snapshot["completed_exec"]) {
		missing = append(missing, "completed_ssh_exec_missing")
	}
	if !boolOnlyFromAny(snapshot["sanitized_result_recorded"]) {
		missing = append(missing, "sanitized_ssh_result_not_recorded")
	}
	if intFromAny(snapshot["completed_without_exit_code_runs"], 0) > 0 {
		missing = append(missing, "ssh_completed_result_exit_code_missing")
	}
	if !boolOnlyFromAny(snapshot["live_rehearsal_controls_ready"]) {
		missing = append(missing, "live_rehearsal_controls_not_ready")
	}
	if !boolOnlyFromAny(snapshot["environment_proof_ready"]) {
		missing = append(missing, "target_environment_proof_not_ready")
	}
	if !boolOnlyFromAny(snapshot["target_environment_attestation_ready"]) {
		missing = append(missing, "target_environment_attestation_not_ready")
	}
	if len(missing) > 0 {
		state := cleanPreviewString(snapshot["recent_evidence_state"])
		if state == "" || state == "recorded" {
			state = "waiting_for_attestation"
		}
		return false, state, missing
	}
	return true, "ready_to_record", nil
}
