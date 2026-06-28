package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func (s *Server) enqueueProjectVersionRefreshOperationsGorm(ctx context.Context, tx *gorm.DB, version map[string]any, steps, argoConnections []map[string]any, actorID string) (map[string]any, error) {
	projectID := strings.TrimSpace(fmt.Sprint(version["project_id"]))
	versionID := strings.TrimSpace(fmt.Sprint(version["id"]))
	if projectID == "" || projectID == "<nil>" || versionID == "" || versionID == "<nil>" {
		return nil, fmt.Errorf("project version metadata is incomplete")
	}
	existing, err := projectVersionOperationRunsGorm(ctx, tx, versionID, []string{"git.refs.refresh", "github.actions.sync", "argo.apps.sync"}, []string{"queued", "running"}, 1)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, errProjectVersionRefreshAlreadyQueued
	}
	operations := []map[string]any{}
	blockedSteps := []map[string]any{}
	enqueuedKeys := map[string]bool{}
	enqueueSummary := func(kind string, op map[string]any, remoteID, connectionID string) {
		operations = append(operations, map[string]any{
			"operation_run_id":   op["id"],
			"operation_type":     op["operation_type"],
			"kind":               kind,
			"refresh_kind":       kind,
			"remote_id":          remoteID,
			"argo_connection_id": connectionID,
			"status":             op["status"],
		})
	}
	for _, step := range steps {
		kind := strings.TrimSpace(fmt.Sprint(step["kind"]))
		status := strings.TrimSpace(fmt.Sprint(step["status"]))
		if status != "planned" {
			blockedSteps = append(blockedSteps, sanitizedProjectVersionRefreshStep(step))
			continue
		}
		remoteID := strings.TrimSpace(fmt.Sprint(step["remote_id"]))
		switch kind {
		case "git_ref_fetch":
			key := kind + ":" + remoteID
			if remoteID == "" || remoteID == "<nil>" || enqueuedKeys[key] {
				continue
			}
			input := map[string]any{
				"project_version_id": versionID,
				"refresh_kind":       kind,
				"remote_id":          remoteID,
				"repo_key":           step["repo_key"],
				"tag":                step["tag_name"],
			}
			op, err := enqueueOperationGorm(ctx, tx, projectID, remoteID, "git.refs.refresh", "refresh Git refs for project version "+fmt.Sprint(version["version"]), input, []string{"git"}, "")
			if err != nil {
				return nil, err
			}
			enqueuedKeys[key] = true
			enqueueSummary(kind, op, remoteID, "")
		case "github_actions_api_refresh":
			key := kind + ":" + remoteID
			if remoteID == "" || remoteID == "<nil>" || enqueuedKeys[key] {
				continue
			}
			op, err := s.enqueueRemoteOperationRunGorm(ctx, tx, remoteID, "github.actions.sync", map[string]any{
				"project_version_id": versionID,
				"refresh_kind":       kind,
			}, actorID)
			if err != nil {
				return nil, err
			}
			enqueuedKeys[key] = true
			enqueueSummary(kind, op, remoteID, "")
		case "argocd_app_refresh":
			for _, connection := range argoConnections {
				connectionID := strings.TrimSpace(fmt.Sprint(connection["id"]))
				key := kind + ":" + connectionID
				if connectionID == "" || connectionID == "<nil>" || enqueuedKeys[key] {
					continue
				}
				op, err := enqueueOperationGorm(ctx, tx, projectID, "", "argo.apps.sync", "refresh Argo apps for project version "+fmt.Sprint(version["version"]), map[string]any{
					"project_version_id": versionID,
					"refresh_kind":       kind,
					"argo_connection_id": connectionID,
				}, []string{"argo"}, "control-worker")
				if err != nil {
					return nil, err
				}
				enqueuedKeys[key] = true
				enqueueSummary(kind, op, "", connectionID)
			}
		default:
			blockedSteps = append(blockedSteps, sanitizedProjectVersionRefreshStep(step))
		}
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("no planned provider refresh operations are available")
	}
	return map[string]any{
		"mode":                             "project_version_provider_refresh_execution",
		"project_version_id":               versionID,
		"version":                          version["version"],
		"operation_enqueued":               true,
		"worker_job_created":               true,
		"external_call_made":               false,
		"secret_included":                  false,
		"raw_provider_response":            false,
		"operation_count":                  len(operations),
		"blocked_step_count":               len(blockedSteps),
		"operations":                       operations,
		"blocked_steps":                    blockedSteps,
		"validation_rerun_required":        true,
		"validation_auto_reload_supported": true,
		"server_side_validation_rerun":     false,
		"result_recording_scope":           "operation_ids_and_sanitized_refresh_kinds",
		"required_operator_action":         "Keep the validation panel open; the UI can reload ProjectVersion validation until queued refresh operations finish.",
	}, nil
}

func (s *Server) enqueueProjectVersionValidationRerunGorm(ctx context.Context, tx *gorm.DB, version map[string]any, actorID string) (map[string]any, error) {
	projectID := strings.TrimSpace(fmt.Sprint(version["project_id"]))
	versionID := strings.TrimSpace(fmt.Sprint(version["id"]))
	if projectID == "" || projectID == "<nil>" || versionID == "" || versionID == "<nil>" {
		return nil, fmt.Errorf("project version metadata is incomplete")
	}
	existing, err := projectVersionOperationRunsGorm(ctx, tx, versionID, []string{"project_version.validation_rerun"}, []string{"queued", "running"}, 1)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, errProjectVersionRefreshAlreadyQueued
	}
	input := map[string]any{
		"project_version_id":             versionID,
		"validation_source":              "local_synced_database_state",
		"recording_trigger":              "standalone_background_validation_rerun",
		"require_recorded_refresh":       true,
		"external_call_made":             false,
		"provider_api_called":            false,
		"git_fetch_performed":            false,
		"argocd_api_called":              false,
		"raw_provider_response_recorded": false,
		"actor_user_id":                  actorID,
	}
	op, err := enqueueOperationGorm(ctx, tx, projectID, "", "project_version.validation_rerun", "rerun validation for project version "+fmt.Sprint(version["version"]), input, []string{"validation"}, "control-worker")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"mode":                                   "project_version_background_validation_rerun_request",
		"project_version_id":                     versionID,
		"version":                                version["version"],
		"operation":                              op,
		"operation_run_id":                       op["id"],
		"operation_enqueued":                     true,
		"worker_job_created":                     true,
		"background_worker_enqueued":             true,
		"automatic_background_rerun":             true,
		"validation_snapshot_write_requested":    true,
		"validation_source":                      "local_synced_database_state",
		"requires_recorded_refresh":              true,
		"external_call_made":                     false,
		"provider_api_called":                    false,
		"git_fetch_performed":                    false,
		"argocd_api_called":                      false,
		"raw_provider_response_recorded":         false,
		"secret_included":                        false,
		"result_recording_scope":                 "sanitized_validation_snapshot_metadata",
		"suppressed_fields":                      []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"required_operator_action":               "Wait for the control worker to rerun local validation and record a sanitized ProjectVersion validation snapshot.",
		"provider_refresh_operation_performed":   false,
		"standalone_background_worker_enabled":   true,
		"control_worker_auto_snapshot_supported": true,
	}, nil
}

func sanitizedProjectVersionRefreshStep(step map[string]any) map[string]any {
	return map[string]any{
		"kind":       step["kind"],
		"status":     step["status"],
		"repo_key":   step["repo_key"],
		"repo_role":  step["repo_role"],
		"remote_id":  step["remote_id"],
		"remote_key": step["remote_key"],
		"reason":     step["reason"],
	}
}

func projectVersionOperationRunsGorm(ctx context.Context, db *gorm.DB, versionID string, operationTypes, statuses []string, limit int) ([]map[string]any, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" || versionID == "<nil>" {
		return nil, nil
	}
	var operations []GormOperationRun
	query := db.WithContext(ctx).Where(map[string]any{"operation_type": operationTypes}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}})
	if len(statuses) > 0 {
		query = query.Where(map[string]any{"status": statuses})
	}
	if limit > 0 {
		query = query.Limit(limit * 5)
	}
	if err := query.Find(&operations).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(operations))
	for _, operation := range operations {
		input := mapFromAny(operation.Input.Data)
		if strings.TrimSpace(fmt.Sprint(input["project_version_id"])) != versionID {
			continue
		}
		items = append(items, operationRunGormMap(operation))
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items, nil
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	s.writeAssets(w, r, r.URL.Query().Get("project_id"))
}

func (s *Server) listProjectAssets(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ProjectID: projectID}, "read") {
		return
	}
	s.writeAssets(w, r, projectID)
}

func (s *Server) writeAssets(w http.ResponseWriter, r *http.Request, projectID string) {
	user := currentUser(r)
	if projectID != "" && !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ProjectID: projectID}, "read") {
		return
	}
	assets, err := s.visibleAssetsGorm(r.Context(), user, projectID, r.URL.Query().Get("asset_type"), r.URL.Query().Get("q"), 500)
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	writeQueryResult(w, assetMaps(assets), nil)
}

func (s *Server) listAssetGraph(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" && !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ProjectID: projectID}, "read") {
		return
	}
	limit := assetGraphLimit(r.URL.Query().Get("limit"))
	nodeAssets, err := s.visibleAssetsGorm(r.Context(), currentUser(r), projectID, r.URL.Query().Get("asset_type"), r.URL.Query().Get("q"), limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load asset graph nodes")
		return
	}
	nodes, edges, err := s.assetGraphFromModels(r.Context(), nodeAssets, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load asset graph edges")
		return
	}
	truncated := len(nodes) > limit
	if truncated {
		nodes = nodes[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges, "truncated": truncated})
}
