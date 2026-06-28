package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
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
	machine, err := sshMachineForRehearsalSnapshot(ctx, store.Gorm, machineID)
	if err != nil {
		return nil, err
	}
	runs, err := sshMachineRehearsalRuns(ctx, store.Gorm, machineID)
	if err != nil {
		return nil, fmt.Errorf("loading ssh rehearsal evidence: %w", err)
	}
	preview := buildSSHMachineRehearsalPreview(machine, runs)
	assetID, assetErr := sshMachineAssetID(ctx, store.Gorm, machineID)
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
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, "ssh_rehearsal_attested", "warning", "ssh rehearsal attestation snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording ssh rehearsal snapshot: %w", err)
	}
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["ssh_rehearsal_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["canonical_asset_status_snapshot_attempted"] = true
	result["message"] = "Sanitized SSH rehearsal attestation snapshot recorded from local operation evidence."
	return result, nil
}

func RecordSSHMachineTargetEnvironmentProof(ctx context.Context, store *Store, opts SSHMachineTargetEnvironmentProofOptions) (map[string]any, error) {
	machineID := strings.TrimSpace(opts.MachineID)
	if machineID == "" {
		return nil, fmt.Errorf("ssh machine id is required")
	}
	machine, err := sshMachineForRehearsalSnapshot(ctx, store.Gorm, machineID)
	if err != nil {
		return nil, err
	}
	runs, err := sshMachineRehearsalRuns(ctx, store.Gorm, machineID)
	if err != nil {
		return nil, fmt.Errorf("loading ssh rehearsal evidence: %w", err)
	}
	preview := buildSSHMachineRehearsalPreview(machine, runs)
	assetID, assetErr := sshMachineAssetID(ctx, store.Gorm, machineID)
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
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, sshTargetEnvironmentProofStatus, "ok", "ssh target environment proof approved", proof)
	if err != nil {
		return nil, fmt.Errorf("recording ssh target environment proof: %w", err)
	}
	result["recording_state"] = "recorded"
	result["snapshot_commit_attempted"] = true
	result["snapshots_written"] = written
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["proof_registered"] = true
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized SSH target environment proof recorded from local operation evidence."
	return result, nil
}

func sshMachineForRehearsalSnapshot(ctx context.Context, db *gorm.DB, machineID string) (map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var machine GormSSHMachine
	if err := db.WithContext(ctx).First(&machine, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return map[string]any{
		"id":         machine.ID,
		"project_id": machine.ProjectID,
		"name":       machine.Name,
		"host":       machine.Host,
		"port":       machine.Port,
		"username":   machine.Username,
		"auth_type":  machine.AuthType,
		"metadata":   machine.Metadata,
		"created_at": machine.CreatedAt,
		"updated_at": machine.UpdatedAt,
	}, nil
}

func sshMachineRehearsalRuns(ctx context.Context, db *gorm.DB, machineID string) ([]map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var runs []GormSSHCommandRun
	if err := db.WithContext(ctx).
		Where(&GormSSHCommandRun{SSHMachineID: validNullString(machineID)}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Limit(50).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	opIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		if run.OperationRunID.Valid && strings.TrimSpace(run.OperationRunID.String) != "" {
			opIDs = append(opIDs, run.OperationRunID.String)
		}
	}
	operations := map[string]GormOperationRun{}
	if len(opIDs) > 0 {
		var opRuns []GormOperationRun
		if err := db.WithContext(ctx).Find(&opRuns, opIDs).Error; err != nil {
			return nil, err
		}
		for _, opRun := range opRuns {
			operations[opRun.ID] = opRun
		}
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		operationType := ""
		if run.OperationRunID.Valid {
			operationType = operations[run.OperationRunID.String].OperationType
		}
		items = append(items, map[string]any{
			"id":             run.ID,
			"status":         run.Status,
			"exit_code":      run.ExitCode,
			"created_at":     run.CreatedAt,
			"finished_at":    run.FinishedAt,
			"operation_type": operationType,
		})
	}
	return items, nil
}
