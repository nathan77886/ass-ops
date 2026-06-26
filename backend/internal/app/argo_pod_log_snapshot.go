package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type ArgoPodLogAuditSnapshotOptions struct {
	ProjectID          string
	DeploymentTargetID string
	PodName            string
	ContainerName      string
	TailLines          int
	SinceSeconds       int
	DryRun             bool
}

func RecordArgoPodLogAuditSnapshot(ctx context.Context, store *Store, opts ArgoPodLogAuditSnapshotOptions) (map[string]any, error) {
	projectID := strings.TrimSpace(opts.ProjectID)
	if projectID == "" {
		return nil, fmt.Errorf("project id is required")
	}
	cleaned, err := cleanArgoPodLogRequest(argoPodLogRequest{
		DeploymentTargetID: opts.DeploymentTargetID,
		PodName:            opts.PodName,
		ContainerName:      opts.ContainerName,
		TailLines:          opts.TailLines,
		SinceSeconds:       opts.SinceSeconds,
	})
	if err != nil {
		return nil, err
	}
	target, err := loadArgoPodLogTarget(ctx, store.DB, projectID, cleaned.DeploymentTargetID)
	if err != nil {
		return nil, err
	}
	auditRows, err := queryArgoPodLogAuditOperations(ctx, store.DB, projectID, cleaned.DeploymentTargetID, cleaned.PodName, cleaned.ContainerName)
	if err != nil {
		return nil, fmt.Errorf("loading pod log audit evidence: %w", err)
	}
	preview := argoPodLogQueryPreview(cleaned.PodName, cleaned.ContainerName, cleaned.TailLines, cleaned.SinceSeconds, target, auditRows)
	assetID, assetErr := deploymentTargetAssetID(ctx, store.DB, cleaned.DeploymentTargetID)
	snapshot := argoPodLogAuditSnapshotPayload(preview, assetErr == nil)
	ready, state, missing := argoPodLogAuditSnapshotReadiness(preview, snapshot)
	result := map[string]any{
		"mode":                             "pod_log_audit_snapshot_recording",
		"recording_state":                  state,
		"recording_ready":                  ready,
		"recording_enabled":                ready && !opts.DryRun,
		"dry_run":                          opts.DryRun,
		"project_id":                       projectID,
		"deployment_target_id":             cleaned.DeploymentTargetID,
		"deployment_target_asset_observed": assetErr == nil,
		"snapshot":                         snapshot,
		"snapshots_written":                0,
		"snapshots_skipped_as_duplicate":   0,
		"pod_log_audit_snapshot_written":   false,
		"asset_status_snapshot_written":    false,
		"operation_log_written":            false,
		"external_call_made":               false,
		"kubernetes_api_call":              false,
		"argocd_api_call":                  false,
		"log_body_included":                false,
		"redacted_log_body_included":       false,
		"kubeconfig_included":              false,
		"raw_response_included":            false,
		"secret_included":                  false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"deployment_target_asset_missing"}
		result["message"] = "Pod log audit snapshot is derived, but the canonical deployment_target asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Pod log audit snapshot is waiting for recorded sanitized audit evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized pod log audit snapshot was not written."
		return result, nil
	}
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting pod log audit snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'pod log audit snapshot recorded', $4
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
		assetID, "pod_log_audit_recorded", "warning", JSONValue{Data: snapshot})
	if err != nil {
		return nil, fmt.Errorf("inserting pod log audit snapshot: %w", err)
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
		return nil, fmt.Errorf("committing pod log audit snapshot: %w", err)
	}
	committed = true
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["pod_log_audit_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshot_commit_attempted"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["pod_log_audit_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized pod log audit snapshot recorded from local audit evidence."
	return result, nil
}

func deploymentTargetAssetID(ctx context.Context, db sqlx.ExtContext, targetID string) (string, error) {
	row, err := queryOne(ctx, db, `
		SELECT id::text AS id
		FROM assets
		WHERE asset_type='deployment_target'
			AND source_table='deployment_targets'
			AND source_id=$1::uuid
		LIMIT 1`, targetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("deployment_target asset for %s not found; run db sync-assets first", targetID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(fmt.Sprint(row["id"]))
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("deployment_target asset for %s has empty id", targetID)
	}
	return assetID, nil
}

func argoPodLogAuditSnapshotPayload(preview map[string]any, assetObserved bool) map[string]any {
	target := mapFromAny(preview["deployment_target"])
	query := mapFromAny(preview["query"])
	evidence := mapFromAny(preview["audit_evidence"])
	retrievalPlan := mapFromAny(preview["retrieval_plan"])
	executionPlan := mapFromAny(retrievalPlan["execution_plan"])
	resultPlan := mapFromAny(executionPlan["result_recording_plan"])
	kubeReadiness := mapFromAny(resultPlan["kubeconfig_readiness_plan"])
	liveStreamPlan := mapFromAny(executionPlan["live_log_stream_plan"])
	return map[string]any{
		"mode":                             "pod_log_audit_snapshot",
		"deployment_target_id":             target["id"],
		"deployment_target_asset_observed": assetObserved,
		"environment":                      target["environment"],
		"cluster_name":                     target["cluster_name"],
		"namespace":                        target["namespace"],
		"pod_name":                         query["pod_name"],
		"container_name_present":           strings.TrimSpace(fmt.Sprint(query["container_name"])) != "",
		"tail_lines":                       intFromAny(query["tail_lines"], 200),
		"since_seconds":                    intFromAny(query["since_seconds"], 0),
		"audit_evidence_state":             evidence["evidence_state"],
		"operation_count":                  intFromAny(evidence["operation_count"], 0),
		"completed_count":                  intFromAny(evidence["completed_count"], 0),
		"failed_count":                     intFromAny(evidence["failed_count"], 0),
		"canceled_count":                   intFromAny(evidence["canceled_count"], 0),
		"active_count":                     intFromAny(evidence["active_count"], 0),
		"operation_log_count":              intFromAny(evidence["operation_log_count"], 0),
		"sanitized_result_recorded":        boolOnlyFromAny(evidence["sanitized_result_recorded"]),
		"redacted_preview_available":       boolOnlyFromAny(evidence["redacted_preview_available"]),
		"preview_line_count":               intFromAny(evidence["preview_line_count"], 0),
		"preview_truncated":                boolOnlyFromAny(evidence["preview_truncated"]),
		"preview_operation_run_id":         cleanOptionalID(fmt.Sprint(evidence["preview_operation_run_id"])),
		"kubeconfig_readiness_state":       kubeReadiness["readiness_state"],
		"live_stream_review_state":         liveStreamPlan["stream_state"],
		"live_stream_review_ready":         boolOnlyFromAny(liveStreamPlan["stream_ready_for_review"]),
		"result_recording_state":           resultPlan["recording_state"],
		"result_written":                   boolOnlyFromAny(resultPlan["result_written"]),
		"validation_source":                "local_pod_log_audit_operation_evidence",
		"external_call_made":               false,
		"kubernetes_api_call":              false,
		"argocd_api_call":                  false,
		"kubectl_command_invoked":          false,
		"log_stream_opened":                false,
		"log_body_included":                false,
		"redacted_log_body_included":       false,
		"kubeconfig_included":              false,
		"authorization_header_included":    false,
		"raw_response_included":            false,
		"secret_included":                  false,
		"operation_log_written":            false,
		"disabled_backends":                []string{"kubeconfig_secret_binding", "kubernetes_client_create", "kubernetes_pod_log_api", "kubectl_logs", "argocd_pod_logs", "live_log_stream_open", "log_body_storage", "redacted_log_body_storage"},
		"suppressed_fields":                []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret", "pod_annotations", "operation_input"},
	}
}

func argoPodLogAuditSnapshotReadiness(preview map[string]any, snapshot map[string]any) (bool, string, []string) {
	queryState := cleanPreviewString(preview["query_state"])
	evidenceState := cleanPreviewString(snapshot["audit_evidence_state"])
	activeCount := intFromAny(snapshot["active_count"], 0)
	state := "blocked"
	if evidenceState == "waiting_for_worker" || activeCount > 0 {
		state = "waiting_for_worker"
	}
	missing := []string{}
	if queryState != "ready_for_approval" {
		missing = append(missing, "pod_log_target_metadata_incomplete")
	}
	if intFromAny(snapshot["operation_count"], 0) == 0 {
		missing = append(missing, "pod_log_audit_not_requested")
	}
	if activeCount > 0 {
		missing = append(missing, "pod_log_audit_worker_still_running")
	}
	if evidenceState != "recorded" {
		missing = append(missing, "sanitized_pod_log_audit_result_not_recorded")
	}
	if !boolOnlyFromAny(snapshot["sanitized_result_recorded"]) {
		missing = append(missing, "sanitized_result_missing")
	}
	if cleanPreviewString(snapshot["audit_evidence_state"]) == "recorded" && intFromAny(snapshot["operation_log_count"], 0) == 0 {
		missing = append(missing, "sanitized_result_operation_log_missing")
	}
	if !boolOnlyFromAny(snapshot["live_stream_review_ready"]) {
		missing = append(missing, "live_stream_review_not_ready")
	}
	ready := queryState == "ready_for_approval" &&
		evidenceState == "recorded" &&
		activeCount == 0 &&
		boolOnlyFromAny(snapshot["sanitized_result_recorded"]) &&
		boolOnlyFromAny(snapshot["live_stream_review_ready"])
	if ready {
		return true, "ready_to_record", nil
	}
	return false, state, missing
}
