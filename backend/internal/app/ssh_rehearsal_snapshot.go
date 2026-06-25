package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type SSHMachineRehearsalSnapshotOptions struct {
	MachineID string
	DryRun    bool
}

type SSHMachineTargetEnvironmentProofOptions struct {
	MachineID string
	DryRun    bool
}

const sshTargetEnvironmentProofStatus = "ssh_target_environment_proof_approved"

func RecordSSHMachineRehearsalSnapshot(ctx context.Context, store *Store, opts SSHMachineRehearsalSnapshotOptions) (map[string]any, error) {
	machineID := strings.TrimSpace(opts.MachineID)
	if machineID == "" {
		return nil, fmt.Errorf("ssh machine id is required")
	}
	machine, err := sshMachineForRehearsalSnapshot(ctx, store.DB, machineID)
	if err != nil {
		return nil, err
	}
	runs, err := sshMachineRehearsalRuns(ctx, store.DB, machineID)
	if err != nil {
		return nil, fmt.Errorf("loading ssh rehearsal evidence: %w", err)
	}
	preview := buildSSHMachineRehearsalPreview(machine, runs)
	assetID, assetErr := sshMachineAssetID(ctx, store.DB, machineID)
	snapshot := sshMachineRehearsalSnapshotPayload(preview, assetErr == nil)
	ready, state, missing := sshMachineRehearsalSnapshotReadiness(preview, snapshot)
	projectID := strings.TrimSpace(fmt.Sprint(machine["project_id"]))
	result := map[string]any{
		"mode":                                      "ssh_rehearsal_snapshot_recording",
		"recording_state":                           state,
		"recording_ready":                           ready,
		"recording_enabled":                         ready && !opts.DryRun,
		"dry_run":                                   opts.DryRun,
		"project_id":                                projectID,
		"ssh_machine_id":                            machineID,
		"ssh_machine_asset_observed":                assetErr == nil,
		"snapshot":                                  snapshot,
		"snapshots_written":                         0,
		"snapshots_skipped_as_duplicate":            0,
		"ssh_rehearsal_snapshot_written":            false,
		"asset_status_snapshot_written":             false,
		"operation_log_written":                     false,
		"external_call_made":                        false,
		"ssh_process_started":                       false,
		"command_executed":                          false,
		"stdout_included":                           false,
		"stderr_included":                           false,
		"raw_error_included":                        false,
		"private_key_included":                      false,
		"known_hosts_included":                      false,
		"secret_included":                           false,
		"operator_identity_included":                false,
		"environment_identifier_included":           false,
		"fixture_identifier_included":               false,
		"target_environment_attestation_ready":      boolOnlyFromAny(snapshot["target_environment_attestation_ready"]),
		"target_environment_attestation_state":      snapshot["target_environment_attestation_state"],
		"environment_proof_ready":                   boolOnlyFromAny(snapshot["environment_proof_ready"]),
		"sanitized_result_recorded":                 boolOnlyFromAny(snapshot["sanitized_result_recorded"]),
		"live_rehearsal_controls_ready":             boolOnlyFromAny(snapshot["live_rehearsal_controls_ready"]),
		"completed_verify":                          boolOnlyFromAny(snapshot["completed_verify"]),
		"completed_exec":                            boolOnlyFromAny(snapshot["completed_exec"]),
		"canonical_asset_status_snapshot_attempted": false,
		"snapshot_commit_attempted":                 false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"ssh_machine_asset_missing"}
		result["message"] = "SSH rehearsal snapshot is derived, but the canonical ssh_machine host asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "SSH rehearsal snapshot is waiting for completed sanitized verify/exec evidence and target environment attestation; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized SSH rehearsal snapshot was not written."
		return result, nil
	}
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting ssh rehearsal snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, "ssh_rehearsal_attested"); err != nil {
		return nil, fmt.Errorf("locking ssh rehearsal snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'ssh rehearsal attestation snapshot recorded', $4
		WHERE NOT EXISTS (
			SELECT 1
			FROM asset_status_snapshots latest
			WHERE latest.asset_id=$1
				AND latest.status=$2
				AND latest.health=$3
				AND latest.raw=$4
				AND latest.collected_at=(
					SELECT max(collected_at)
					FROM asset_status_snapshots newest
					WHERE newest.asset_id=$1
				)
		)`,
		assetID, "ssh_rehearsal_attested", "warning", JSONValue{Data: snapshot})
	if err != nil {
		return nil, fmt.Errorf("inserting ssh rehearsal snapshot: %w", err)
	}
	written := 0
	rowsAffectedWarning := ""
	if rows, err := execResult.RowsAffected(); err == nil {
		written = int(rows)
	} else {
		written = -1
		rowsAffectedWarning = "rows affected unavailable"
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing ssh rehearsal snapshot: %w", err)
	}
	committed = true
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["ssh_rehearsal_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshot_commit_attempted"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["ssh_rehearsal_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["canonical_asset_status_snapshot_attempted"] = true
	result["message"] = "Sanitized SSH rehearsal attestation snapshot recorded from local operation evidence."
	return result, nil
}

func RecordSSHMachineTargetEnvironmentProof(ctx context.Context, store *Store, opts SSHMachineTargetEnvironmentProofOptions) (map[string]any, error) {
	machineID := strings.TrimSpace(opts.MachineID)
	if machineID == "" {
		return nil, fmt.Errorf("ssh machine id is required")
	}
	machine, err := sshMachineForRehearsalSnapshot(ctx, store.DB, machineID)
	if err != nil {
		return nil, err
	}
	runs, err := sshMachineRehearsalRuns(ctx, store.DB, machineID)
	if err != nil {
		return nil, fmt.Errorf("loading ssh rehearsal evidence: %w", err)
	}
	preview := buildSSHMachineRehearsalPreview(machine, runs)
	assetID, assetErr := sshMachineAssetID(ctx, store.DB, machineID)
	snapshot := sshMachineRehearsalSnapshotPayload(preview, assetErr == nil)
	ready, state, missing := sshMachineRehearsalSnapshotReadiness(preview, snapshot)
	proof := sshMachineTargetEnvironmentProofPayload(snapshot)
	projectID := strings.TrimSpace(fmt.Sprint(machine["project_id"]))
	result := map[string]any{
		"mode":                                 "ssh_target_environment_proof_recording",
		"recording_state":                      state,
		"recording_ready":                      ready,
		"recording_enabled":                    ready && !opts.DryRun,
		"dry_run":                              opts.DryRun,
		"project_id":                           projectID,
		"ssh_machine_id":                       machineID,
		"ssh_machine_asset_observed":           assetErr == nil,
		"proof":                                proof,
		"proof_registered":                     false,
		"asset_status_snapshot_written":        false,
		"snapshots_written":                    0,
		"snapshots_skipped_as_duplicate":       0,
		"operation_log_written":                false,
		"external_call_made":                   false,
		"ssh_process_started":                  false,
		"command_executed":                     false,
		"stdout_included":                      false,
		"stderr_included":                      false,
		"raw_error_included":                   false,
		"raw_output_recorded":                  false,
		"private_key_included":                 false,
		"known_hosts_included":                 false,
		"secret_included":                      false,
		"operator_identity_included":           false,
		"operator_note_included":               false,
		"environment_identifier_included":      false,
		"fixture_identifier_included":          false,
		"target_environment_attestation_ready": boolOnlyFromAny(snapshot["target_environment_attestation_ready"]),
		"environment_proof_ready":              boolOnlyFromAny(snapshot["environment_proof_ready"]),
		"sanitized_result_recorded":            boolOnlyFromAny(snapshot["sanitized_result_recorded"]),
		"live_rehearsal_controls_ready":        boolOnlyFromAny(snapshot["live_rehearsal_controls_ready"]),
		"completed_verify":                     boolOnlyFromAny(snapshot["completed_verify"]),
		"completed_exec":                       boolOnlyFromAny(snapshot["completed_exec"]),
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"ssh_machine_asset_missing"}
		result["message"] = "SSH target environment proof is derived, but the canonical ssh_machine host asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "SSH target environment proof is waiting for completed sanitized verify/exec evidence and target environment attestation; no proof was written."
		return result, nil
	}
	if opts.DryRun {
		result["recording_state"] = "ready_to_record"
		result["message"] = "Dry run only; sanitized SSH target environment proof was not written."
		return result, nil
	}
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting ssh target environment proof transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, sshTargetEnvironmentProofStatus); err != nil {
		return nil, fmt.Errorf("locking ssh target environment proof asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'ssh target environment proof approved', $4
		WHERE NOT EXISTS (
			SELECT 1
			FROM asset_status_snapshots latest
			WHERE latest.asset_id=$1
				AND latest.status=$2
				AND latest.health=$3
				AND latest.raw=$4
				AND latest.collected_at=(
					SELECT max(collected_at)
					FROM asset_status_snapshots newest
					WHERE newest.asset_id=$1
				)
		)`,
		assetID, sshTargetEnvironmentProofStatus, "ok", JSONValue{Data: proof})
	if err != nil {
		return nil, fmt.Errorf("inserting ssh target environment proof: %w", err)
	}
	written := 0
	rowsAffectedWarning := ""
	if rows, err := execResult.RowsAffected(); err == nil {
		written = int(rows)
	} else {
		written = -1
		rowsAffectedWarning = "rows affected unavailable"
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing ssh target environment proof: %w", err)
	}
	committed = true
	result["recording_state"] = "recorded"
	result["snapshot_commit_attempted"] = true
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["proof_registered"] = true
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["proof_registered"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized SSH target environment proof recorded from local operation evidence."
	return result, nil
}

func sshMachineForRehearsalSnapshot(ctx context.Context, db sqlx.ExtContext, machineID string) (map[string]any, error) {
	return queryOne(ctx, db, `
		SELECT id, project_id, name, host, port, username, auth_type, metadata, created_at, updated_at
		FROM ssh_machines
		WHERE id=$1`, machineID)
}

func sshMachineRehearsalRuns(ctx context.Context, db sqlx.ExtContext, machineID string) ([]map[string]any, error) {
	return queryMaps(ctx, db, `
		SELECT scr.id, scr.status, scr.exit_code, scr.created_at, scr.finished_at, op.operation_type
		FROM ssh_command_runs scr
		LEFT JOIN operation_runs op ON op.id=scr.operation_run_id
		WHERE scr.ssh_machine_id=$1
		ORDER BY scr.created_at DESC
		LIMIT 50`, machineID)
}

func sshMachineAssetID(ctx context.Context, db sqlx.ExtContext, machineID string) (string, error) {
	row, err := queryOne(ctx, db, `
		SELECT id::text AS id
		FROM assets
		WHERE asset_type='host'
			AND source_table='ssh_machines'
			AND source_id=$1::uuid
		LIMIT 1`, machineID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("ssh_machine host asset for %s not found; run db sync-assets first", machineID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(fmt.Sprint(row["id"]))
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("ssh_machine host asset for %s has empty id", machineID)
	}
	return assetID, nil
}

func sshMachineTargetEnvironmentProofEvidence(ctx context.Context, db sqlx.ExtContext, machineID string) (map[string]any, error) {
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
	row, err := queryOne(ctx, db, `
		SELECT status, health, collected_at
		FROM asset_status_snapshots
		WHERE asset_id=$1
			AND status=$2
		ORDER BY collected_at DESC
		LIMIT 1`, assetID, sshTargetEnvironmentProofStatus)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return result, nil
		}
		result["proof_state"] = "lookup_failed"
		return result, err
	}
	result["proof_state"] = "recorded"
	result["proof_registered"] = true
	result["asset_status_observed"] = true
	result["status"] = row["status"]
	result["health"] = row["health"]
	result["proof_registered_at"] = row["collected_at"]
	return result, nil
}

func sshMachineRehearsalSnapshotPayload(preview map[string]any, assetObserved bool) map[string]any {
	machine := mapFromAny(preview["machine"])
	evidence := mapFromAny(preview["recent_evidence"])
	resultPlan := mapFromAny(preview["result_recording_plan"])
	controlEvidence := mapFromAny(preview["live_rehearsal_control_evidence"])
	environmentProof := mapFromAny(preview["environment_proof_plan"])
	attestation := mapFromAny(preview["target_environment_attestation_plan"])
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
		"status_snapshot_written":                   true,
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
