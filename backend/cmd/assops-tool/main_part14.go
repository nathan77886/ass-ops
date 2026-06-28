package main

import (
	"fmt"
	"strings"
)

func mergeBoolMaps(maps ...map[string]bool) map[string]bool {
	merged := map[string]bool{}
	for _, values := range maps {
		for key, value := range values {
			if value {
				merged[key] = true
			}
		}
	}
	return merged
}

func countContextGenerationEvidence(assets []map[string]any) int {
	count := 0
	for _, row := range assets {
		metadata := mapFromAPI(row["metadata"])
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["status"])))
		if fmt.Sprint(row["asset_type"]) == "agent_tool_call" &&
			fmt.Sprint(metadata["tool_name"]) == "context.generate" &&
			status == "completed" {
			count++
		}
	}
	return count
}

func apiAssetGraphID(row map[string]any) string {
	for _, key := range []string{"asset_id", "id"} {
		raw, ok := row[key].(string)
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		if value != "" && value != "<nil>" {
			return value
		}
	}
	typ := strings.TrimSpace(fmt.Sprint(row["asset_type"]))
	sourceID := strings.TrimSpace(fmt.Sprint(row["source_id"]))
	if typ != "" && typ != "<nil>" && sourceID != "" && sourceID != "<nil>" {
		return typ + ":" + sourceID
	}
	return ""
}

type contextGraphLinkCounts struct {
	TaskRuntimes              int
	TaskContextToolCalls      int
	CompleteContextTasks      int
	CompleteContextTaskAssets int
}

func countContextGraphLinks(assets []map[string]any, graph map[string]any) contextGraphLinkCounts {
	counts := contextGraphLinkCounts{}
	contextToolCalls := map[string]bool{}
	taskAssetIDs := assetIDsByType(assets, "agent_task")
	runtimeAssetIDs := assetIDsByType(assets, "ai_runtime")
	for _, row := range assets {
		metadata := mapFromAPI(row["metadata"])
		if metadata == nil {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["status"])))
		if fmt.Sprint(row["asset_type"]) == "agent_tool_call" &&
			fmt.Sprint(metadata["tool_name"]) == "context.generate" &&
			status == "completed" {
			if assetID := apiAssetGraphID(row); strings.HasPrefix(assetID, "agent_tool_call:") {
				contextToolCalls[assetID] = true
			}
		}
	}

	type taskLinks struct {
		runtimes     map[string]bool
		contextTools map[string]bool
	}
	byTask := map[string]*taskLinks{}
	taskEntry := func(assetID string) *taskLinks {
		entry := byTask[assetID]
		if entry == nil {
			entry = &taskLinks{runtimes: map[string]bool{}, contextTools: map[string]bool{}}
			byTask[assetID] = entry
		}
		return entry
	}
	for _, edge := range apiItemsByKey(graph, "edges") {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "uses_runtime":
			if strings.HasPrefix(from, "agent_task:") && strings.HasPrefix(to, "ai_runtime:") {
				counts.TaskRuntimes++
				taskEntry(from).runtimes[to] = true
			}
		case "records_tool_call":
			if strings.HasPrefix(from, "agent_task:") && contextToolCalls[to] {
				counts.TaskContextToolCalls++
				taskEntry(from).contextTools[to] = true
			}
		}
	}
	for taskID, entry := range byTask {
		if len(entry.runtimes) > 0 && len(entry.contextTools) > 0 {
			counts.CompleteContextTasks++
			if taskAssetIDs[taskID] && hasAnyKnownID(entry.runtimes, runtimeAssetIDs) && hasAnyKnownID(entry.contextTools, contextToolCalls) {
				counts.CompleteContextTaskAssets++
			}
		}
	}
	return counts
}

func hasAnyKnownID(ids, knownIDs map[string]bool) bool {
	for id := range ids {
		if knownIDs[id] {
			return true
		}
	}
	return false
}

func hasAnyIDInBoth(ids, firstKnownIDs, secondKnownIDs map[string]bool) bool {
	for id := range ids {
		if firstKnownIDs[id] && secondKnownIDs[id] {
			return true
		}
	}
	return false
}

func countGraphNodesByPrefix(graph map[string]any, prefix string) int {
	count := 0
	for _, node := range apiItemsByKey(graph, "nodes") {
		id := fmt.Sprint(node["id"])
		if strings.HasPrefix(id, prefix) {
			count++
		}
	}
	return count
}

func countGraphNodesByKnownIDs(graph map[string]any, knownIDs map[string]bool) int {
	count := 0
	for _, node := range apiItemsByKey(graph, "nodes") {
		id := fmt.Sprint(node["id"])
		if knownIDs[id] {
			count++
		}
	}
	return count
}

func projectDemoDataRehearsalPlan(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	planState := "planned"
	if status == "ready" {
		planState = "observed"
	} else if status == "missing" {
		planState = "blocked"
	}
	blockedReasons := []string{}
	if status != "ready" {
		blockedReasons = append(blockedReasons, "live_demo_graph_evidence_incomplete")
	}
	environmentPlan := demoDataEnvironmentEvidencePlan(status, evidence, requiredEvidence)
	graphPlan := demoDataGraphProofPlan(status, evidence, requiredEvidence)
	environmentProof := demoDataEnvironmentProof(status, evidence, requiredEvidence)
	resultPlan := demoDataResultRecordingPlan(status, evidence, requiredEvidence)
	return map[string]any{
		"mode":                      "first_version_demo_data_rehearsal_plan",
		"plan_state":                planState,
		"readiness_status":          status,
		"execution_enabled":         false,
		"external_call_made":        false,
		"demo_seed_written":         false,
		"project_created":           false,
		"repository_created":        false,
		"git_remote_created":        false,
		"asset_graph_written":       false,
		"contains_remote_url":       false,
		"contains_credentials":      false,
		"required_evidence":         requiredEvidence,
		"evidence_counts":           evidence,
		"environment_evidence_plan": environmentPlan,
		"environment_demo_proof":    environmentProof,
		"graph_proof_plan":          graphPlan,
		"result_recording_plan":     resultPlan,
		"disabled_backends": []string{
			"project_create",
			"repository_create",
			"git_remote_create",
			"demo_seed_write",
			"asset_graph_write",
		},
		"suppressed_fields": []string{
			"remote_url",
			"git_credentials",
			"provider_token",
			"repository_secret",
			"webhook_secret",
		},
		"blocked_reasons": blockedReasons,
		"message":         "Demo data rehearsal is audit-only; create project/repository/remote evidence in the live environment, then sync the canonical asset graph.",
	}
}

// Keep this proof contract in sync with web/src/main.tsx demoDataEnvironmentProof.
func demoDataEnvironmentProof(status string, evidence map[string]int, requiredEvidence []string) map[string]any {
	if evidence == nil {
		evidence = map[string]int{}
	}
	checks := demoDataEvidenceChecks(evidence)
	missing := missingDemoDataEvidence(checks, requiredEvidence)
	proofState := "observed"
	if len(missing) > 0 {
		proofState = "partial"
	}
	if status == "missing" {
		proofState = "blocked"
	}
	liveEnvironmentDataObserved := len(missing) == 0
	if status == "missing" {
		liveEnvironmentDataObserved = false
	}
	multiRemoteObserved := evidence["repository_assets"] > 0 &&
		evidence["git_remote_assets"] >= 2 &&
		evidence["project_repository_links"] > 0 &&
		evidence["complete_repository_paths"] > 0 &&
		evidence["repository_remote_links"] >= 2 &&
		proofState != "blocked"
	return map[string]any{
		"mode":                           "first_version_demo_environment_proof",
		"proof_state":                    proofState,
		"proof_ready":                    len(missing) == 0 && status == "ready",
		"proof_source":                   "canonical_asset_graph_counts",
		"live_environment_data_observed": liveEnvironmentDataObserved,
		"complete_repository_multi_remote_path_observed": multiRemoteObserved,
		"required_evidence":    requiredEvidence,
		"missing_evidence":     missing,
		"evidence_counts":      evidence,
		"external_call_made":   false,
		"demo_seed_written":    false,
		"project_created":      false,
		"repository_created":   false,
		"git_remote_created":   false,
		"asset_graph_written":  false,
		"contains_remote_url":  false,
		"contains_credentials": false,
		"suppressed_fields":    []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"},
	}
}

func demoDataEvidenceChecks(evidence map[string]int) map[string]bool {
	if evidence == nil {
		evidence = map[string]int{}
	}
	return map[string]bool{
		"project_asset":                        evidence["project_assets"] > 0,
		"project_graph_node":                   evidence["project_graph_nodes"] > 0,
		"project_asset_node":                   evidence["project_asset_nodes"] > 0,
		"repository_asset":                     evidence["repository_assets"] > 0,
		"two_git_remote_assets":                evidence["git_remote_assets"] >= 2,
		"project_to_repository_graph_link":     evidence["project_repository_links"] > 0,
		"repository_to_two_remotes_graph_path": evidence["complete_repository_paths"] > 0,
	}
}
