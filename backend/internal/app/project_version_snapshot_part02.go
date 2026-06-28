package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"strings"
	"time"
)

func projectVersionArgoAppMaps(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var apps []GormArgoApp
	if err := db.WithContext(ctx).Where(&GormArgoApp{ProjectID: projectID}).Find(&apps).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, app := range apps {
		items = append(items, map[string]any{"id": app.ID, "name": app.Name, "namespace": app.Namespace, "status": app.Status, "metadata": mapFromAny(app.Metadata.Data), "synced_at": nullableTimeAny(app.SyncedAt), "updated_at": app.UpdatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["updated_at"]).After(projectVersionTimeFromAny(items[j]["updated_at"]))
	})
	return limitMaps(items, 500), nil
}

func projectVersionArgoConnectionMaps(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var connections []GormArgoConnection
	if err := db.WithContext(ctx).Where(&GormArgoConnection{ProjectID: projectID}).Find(&connections).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, connection := range connections {
		items = append(items, map[string]any{"id": connection.ID, "name": connection.Name, "last_sync_status": connection.LastSyncStatus, "updated_at": connection.UpdatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["updated_at"]).After(projectVersionTimeFromAny(items[j]["updated_at"]))
	})
	return limitMaps(items, 100), nil
}

func queryProjectVersionRefreshOperationsGorm(ctx context.Context, db *gorm.DB, versionID, projectID string) ([]map[string]any, error) {
	return queryProjectVersionOperationsGorm(ctx, db, versionID, projectID, map[string]bool{"git.refs.refresh": true, "github.actions.sync": true, "argo.apps.sync": true}, 50)
}

func queryProjectVersionValidationRerunOperationsGorm(ctx context.Context, db *gorm.DB, versionID, projectID string) ([]map[string]any, error) {
	return queryProjectVersionOperationsGorm(ctx, db, versionID, projectID, map[string]bool{"project_version.validation_rerun": true}, 20)
}

func queryProjectVersionOperationsGorm(ctx context.Context, db *gorm.DB, versionID, projectID string, types map[string]bool, limit int) ([]map[string]any, error) {
	var runs []GormOperationRun
	if err := db.WithContext(ctx).Find(&runs).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, run := range runs {
		if !types[run.OperationType] {
			continue
		}
		if run.ProjectID.Valid && run.ProjectID.String != projectID {
			continue
		}
		input := mapFromAny(run.Input.Data)
		if strings.TrimSpace(fmt.Sprint(input["project_version_id"])) != versionID {
			continue
		}
		items = append(items, map[string]any{"id": run.ID, "operation_type": run.OperationType, "status": run.Status, "error": run.Error, "input": input, "result": mapFromAny(run.Result.Data), "started_at": nullableTimeAny(run.StartedAt), "finished_at": nullableTimeAny(run.FinishedAt), "created_at": run.CreatedAt, "updated_at": run.UpdatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["created_at"]).After(projectVersionTimeFromAny(items[j]["created_at"]))
	})
	return limitMaps(items, limit), nil
}

func limitMaps(items []map[string]any, limit int) []map[string]any {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func projectVersionTimeFromAny(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case *time.Time:
		if typed != nil {
			return *typed
		}
	}
	return time.Time{}
}

func projectVersionValidationSnapshotPayload(preview map[string]any, assetObserved bool) map[string]any {
	summary := mapFromAny(preview["provider_refresh_result_summary"])
	rerunEvidence := mapFromAny(preview["validation_rerun_evidence"])
	backgroundPlan := mapFromAny(preview["background_validation_rerun_plan"])
	snapshotPlan := mapFromAny(backgroundPlan["validation_snapshot_write_plan"])
	return map[string]any{
		"mode":                                 "project_version_validation_snapshot",
		"project_version_id":                   preview["version_id"],
		"validation_state":                     preview["validation_state"],
		"repository_count":                     intFromAny(preview["repository_count"], 0),
		"ready_count":                          intFromAny(preview["ready_count"], 0),
		"partial_count":                        intFromAny(preview["partial_count"], 0),
		"blocked_count":                        intFromAny(preview["blocked_count"], 0),
		"provider_refresh_status":              summary["validation_rerun_status"],
		"operation_count":                      intFromAny(summary["operation_count"], 0),
		"active_count":                         intFromAny(summary["active_count"], 0),
		"terminal_count":                       intFromAny(summary["terminal_count"], 0),
		"server_side_validation_recheck":       boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"]),
		"server_side_validation_recheck_ready": boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]),
		"validation_rerun_recorded":            boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"snapshot_state":                       snapshotPlan["snapshot_state"],
		"snapshot_ready_for_review":            boolOnlyFromAny(snapshotPlan["snapshot_ready_for_review"]),
		"project_version_asset_observed":       assetObserved,
		"validation_source":                    "local_synced_database_state",
		"external_call_made":                   false,
		"provider_api_called":                  false,
		"git_fetch_performed":                  false,
		"argocd_api_called":                    false,
		"raw_response_included":                false,
		"secret_included":                      false,
		"operation_log_written":                false,
		"background_worker_enqueued":           false,
		"missing_required_evidence":            projectVersionValidationSnapshotMissingEvidence(preview, summary, rerunEvidence, assetObserved),
	}
}

func projectVersionValidationSnapshotMissingEvidence(preview, summary, rerunEvidence map[string]any, assetObserved bool) []string {
	missing := []string{}
	if !assetObserved {
		missing = append(missing, "project_version_asset_missing")
	}
	if intFromAny(preview["repository_count"], 0) == 0 {
		missing = append(missing, "project_version_repository_manifest_missing")
	}
	if !boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) {
		missing = append(missing, "server_side_validation_recheck_not_terminal")
	}
	if !boolOnlyFromAny(summary["validation_rerun_recorded"]) {
		missing = append(missing, "validation_rerun_not_recorded")
	}
	return missing
}
