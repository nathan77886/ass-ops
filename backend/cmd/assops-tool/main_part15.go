package main

import (
	"fmt"
	"strings"
)

func missingDemoDataEvidence(checks map[string]bool, requiredEvidence []string) []string {
	missing := make([]string, 0)
	for _, key := range requiredEvidence {
		if !checks[key] {
			missing = append(missing, key)
		}
	}
	return missing
}

func demoDataEnvironmentEvidencePlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	metadataReady := status == "ready"
	blockedReasons := []string{"demo_seed_execution_disabled", "live_environment_not_recorded"}
	if status != "ready" {
		blockedReasons = append(blockedReasons, "required_graph_evidence_missing")
	}
	return map[string]any{
		"mode":                        "first_version_demo_environment_evidence_plan",
		"evidence_state":              mapStatusToPlanState(status),
		"evidence_ready":              false,
		"evidence_ready_reason":       "demo_environment_execution_disabled",
		"metadata_ready":              metadataReady,
		"execution_enabled":           false,
		"demo_seed_written":           false,
		"project_created":             false,
		"repository_created":          false,
		"git_remote_created":          false,
		"external_call_made":          false,
		"contains_remote_url":         false,
		"contains_credentials":        false,
		"required_evidence":           requiredEvidence,
		"evidence_counts":             evidence,
		"required_environment_fields": []string{"project_asset", "project_graph_node", "project_asset_node", "repository_asset", "two_git_remote_assets", "project_repository_graph_link", "repository_to_two_remotes_graph_path"},
		"suppressed_fields":           []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"},
		"blocked_reasons":             blockedReasons,
		"message":                     "Demo environment evidence is observed only; this plan does not create demo project, repository, or remote rows.",
	}
}

func demoDataGraphProofPlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	metadataReady := status == "ready"
	blockedReasons := []string{"asset_graph_write_disabled"}
	if status != "ready" {
		blockedReasons = append(blockedReasons, "graph_proof_incomplete")
	}
	return map[string]any{
		"mode":                  "first_version_demo_graph_proof_plan",
		"proof_state":           mapStatusToPlanState(status),
		"proof_ready":           false,
		"proof_ready_reason":    "demo_graph_proof_execution_disabled",
		"metadata_ready":        metadataReady,
		"asset_graph_written":   false,
		"asset_sync_triggered":  false,
		"graph_query_performed": false,
		"external_call_made":    false,
		"required_evidence":     requiredEvidence,
		"evidence_counts":       evidence,
		"required_graph_paths":  []string{"project:*", "project:* -> repository:*", "repository:* -> git_remote:*", "repository:* -> second git_remote:*"},
		"suppressed_fields":     []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"},
		"blocked_reasons":       blockedReasons,
		"message":               "Demo graph proof is read-only; future execution must sync canonical assets and prove one repository has at least two remotes.",
	}
}

func demoDataResultRecordingPlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	// Result recording stays blocked even when graph evidence is observed; it only
	// becomes meaningful after a future live demo-data execution writes a result.
	checks := demoDataEvidenceChecks(evidence)
	missing := missingDemoDataEvidence(checks, requiredEvidence)
	readinessSnapshotReady := status == "ready" && len(missing) == 0
	graphSnapshotReady := readinessSnapshotReady
	blockedReasons := []string{"demo_result_write_disabled", "readiness_snapshot_write_disabled", "asset_graph_snapshot_write_disabled"}
	if !readinessSnapshotReady {
		blockedReasons = append(blockedReasons, "required_demo_evidence_missing")
	}
	preflight := map[string]any{
		"mode":                                  "first_version_demo_data_result_recording_preflight",
		"readiness_status":                      status,
		"readiness_snapshot_ready_for_review":   readinessSnapshotReady,
		"asset_graph_snapshot_ready_for_review": graphSnapshotReady,
		"snapshot_contract_ready":               readinessSnapshotReady && graphSnapshotReady,
		"snapshot_write_enabled":                false,
		"asset_graph_write_enabled":             false,
		"operation_log_write_enabled":           false,
		"external_call_made":                    false,
		"contains_remote_url":                   false,
		"contains_credentials":                  false,
		"required_evidence":                     requiredEvidence,
		"missing_required_evidence":             missing,
		"evidence_counts":                       evidence,
		"required_snapshot_fields":              []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status", "evidence_counts", "missing_required_evidence"},
		"suppressed_fields":                     []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret", "raw_graph_payload", "operation_log_body"},
		"disabled_backends":                     []string{"demo_result_write", "readiness_snapshot_write", "asset_graph_snapshot_write", "operation_log_write"},
		"blocked_reasons":                       blockedReasons,
		"message":                               "Demo result recording preflight is review metadata only; snapshot and operation-log writes remain disabled.",
	}
	return map[string]any{
		"mode":                          "first_version_demo_data_result_recording_plan",
		"result_recording_state":        "blocked",
		"result_recording_ready":        false,
		"result_recording_ready_reason": "demo_data_execution_not_performed",
		"recording_enabled":             false,
		"result_written":                false,
		"operation_log_written":         false,
		"readiness_snapshot_written":    false,
		"asset_graph_snapshot_written":  false,
		"raw_remote_url_recorded":       false,
		"raw_credentials_recorded":      false,
		"required_result_fields":        []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status"},
		"result_recording_preflight":    preflight,
		"suppressed_fields":             []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret", "raw_graph_payload", "operation_log_body"},
		"blocked_reasons":               []string{"demo_data_execution_not_performed", "readiness_snapshot_not_recorded", "asset_graph_snapshot_not_recorded"},
		"message":                       "Demo data result recording is disabled until a live environment run creates and proves the graph-backed demo evidence.",
	}
}

func mapStatusToPlanState(status string) string {
	if status == "ready" {
		return "observed"
	}
	if status == "missing" {
		return "blocked"
	}
	return "planned"
}

func readinessItem(key, label, next string, done bool, evidence any, partial bool) readinessRow {
	status := "missing"
	if done {
		status = "ready"
	} else if partial {
		status = "partial"
	}
	return readinessRow{Key: key, Label: label, Status: status, Evidence: evidence, Next: next}
}

func apiItems(payload map[string]any) []map[string]any {
	return apiItemsByKey(payload, "items")
}

func apiItemsByKey(payload map[string]any, key string) []map[string]any {
	rawItems, ok := payload[key].([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item := mapFromAPI(raw)
		if item != nil {
			items = append(items, item)
		}
	}
	return items
}

func assetGraphPayloadAvailable(graph map[string]any) bool {
	if graph == nil {
		return false
	}
	_, hasNodes := graph["nodes"]
	_, hasEdges := graph["edges"]
	return hasNodes || hasEdges
}

func mapFromAPI(value any) map[string]any {
	item, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return item
}

func countAPIField(rows []map[string]any, field string) map[string]int {
	counts := map[string]int{}
	for _, row := range rows {
		key := strings.TrimSpace(fmt.Sprint(row[field]))
		if key != "" && key != "<nil>" {
			counts[key]++
		}
	}
	return counts
}

func countAPIStatus(rows []map[string]any, status string) int {
	count := 0
	for _, row := range rows {
		if fmt.Sprint(row["status"]) == status {
			count++
		}
	}
	return count
}

func countAPITypeStatus(rows []map[string]any, typ, status string) int {
	count := 0
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) == typ && fmt.Sprint(row["status"]) == status {
			count++
		}
	}
	return count
}

func activeAssetIDsByTypeStatus(rows []map[string]any, typ, status string) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["asset_type"]) != typ || fmt.Sprint(row["status"]) != status {
			continue
		}
		if assetID := apiAssetGraphID(row); assetID != "" {
			ids[assetID] = true
		}
	}
	return ids
}

func countAPITypeMetadata(rows []map[string]any, typ, key, value string) int {
	count := 0
	for _, row := range rows {
		metadata := mapFromAPI(row["metadata"])
		if fmt.Sprint(row["asset_type"]) == typ && metadataValueEqual(metadata[key], value) {
			count++
		}
	}
	return count
}

func metadataValueEqual(raw any, value string) bool {
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw)), strings.TrimSpace(value))
}

type repositoryGraphLinkCounts struct {
	ProjectRepository  int
	RepositoryRemotes  int
	CompleteRepos      int
	CompleteRepoAssets int
}
