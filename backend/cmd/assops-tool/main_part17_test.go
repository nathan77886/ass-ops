package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func intMapFromAny(value any) map[string]int {
	switch typed := value.(type) {
	case map[string]int:
		return typed
	case map[string]any:
		items := map[string]int{}
		for key, value := range typed {
			switch number := value.(type) {
			case int:
				items[key] = number
			case int64:
				items[key] = int(number)
			case float64:
				items[key] = int(number)
			}
		}
		return items
	default:
		return nil
	}
}

func assertDemoDataRehearsalPlanSafe(t *testing.T, plan map[string]any) {
	t.Helper()
	if plan["mode"] != "first_version_demo_data_rehearsal_plan" ||
		plan["execution_enabled"] != false ||
		plan["external_call_made"] != false ||
		plan["demo_seed_written"] != false ||
		plan["project_created"] != false ||
		plan["repository_created"] != false ||
		plan["git_remote_created"] != false ||
		plan["asset_graph_written"] != false ||
		plan["contains_remote_url"] != false ||
		plan["contains_credentials"] != false {
		t.Fatalf("demo data rehearsal plan should stay audit-only and redacted: %#v", plan)
	}
	for _, backend := range []string{"project_create", "repository_create", "git_remote_create", "demo_seed_write", "asset_graph_write"} {
		if !containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("demo data rehearsal disabled backends missing %q: %#v", backend, plan["disabled_backends"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(plan["suppressed_fields"]), field) {
			t.Fatalf("demo data rehearsal suppressed fields missing %q: %#v", field, plan["suppressed_fields"])
		}
	}
	environmentPlan := mapFromAny(plan["environment_evidence_plan"])
	if environmentPlan["mode"] != "first_version_demo_environment_evidence_plan" ||
		environmentPlan["evidence_ready"] != false ||
		environmentPlan["evidence_ready_reason"] != "demo_environment_execution_disabled" ||
		environmentPlan["execution_enabled"] != false ||
		environmentPlan["demo_seed_written"] != false ||
		environmentPlan["project_created"] != false ||
		environmentPlan["repository_created"] != false ||
		environmentPlan["git_remote_created"] != false ||
		environmentPlan["external_call_made"] != false ||
		environmentPlan["contains_remote_url"] != false ||
		environmentPlan["contains_credentials"] != false {
		t.Fatalf("demo environment evidence plan should stay disabled and redacted: %#v", environmentPlan)
	}
	if plan["readiness_status"] == "ready" && environmentPlan["metadata_ready"] != true {
		t.Fatalf("ready demo environment plan should mark metadata ready: %#v", environmentPlan)
	}
	if plan["readiness_status"] != "ready" && environmentPlan["metadata_ready"] != false {
		t.Fatalf("non-ready demo environment plan should mark metadata not ready: %#v", environmentPlan)
	}
	for _, field := range []string{"project_asset", "project_graph_node", "repository_asset", "two_git_remote_assets", "project_repository_graph_link", "repository_to_two_remotes_graph_path"} {
		if !containsString(stringSliceFromAny(environmentPlan["required_environment_fields"]), field) {
			t.Fatalf("demo environment required fields missing %q: %#v", field, environmentPlan["required_environment_fields"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(environmentPlan["suppressed_fields"]), field) {
			t.Fatalf("demo environment suppressed fields missing %q: %#v", field, environmentPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"demo_seed_execution_disabled", "live_environment_not_recorded"} {
		if !containsString(stringSliceFromAny(environmentPlan["blocked_reasons"]), reason) {
			t.Fatalf("demo environment blocked reasons missing %q: %#v", reason, environmentPlan["blocked_reasons"])
		}
	}
	if plan["readiness_status"] == "ready" {
		if containsString(stringSliceFromAny(environmentPlan["blocked_reasons"]), "required_graph_evidence_missing") {
			t.Fatalf("ready demo environment should not report missing graph evidence: %#v", environmentPlan["blocked_reasons"])
		}
	} else if !containsString(stringSliceFromAny(environmentPlan["blocked_reasons"]), "required_graph_evidence_missing") {
		t.Fatalf("non-ready demo environment should report missing graph evidence: %#v", environmentPlan["blocked_reasons"])
	}

	environmentProof := mapFromAny(plan["environment_demo_proof"])
	if environmentProof["mode"] != "first_version_demo_environment_proof" ||
		environmentProof["external_call_made"] != false ||
		environmentProof["demo_seed_written"] != false ||
		environmentProof["project_created"] != false ||
		environmentProof["repository_created"] != false ||
		environmentProof["git_remote_created"] != false ||
		environmentProof["asset_graph_written"] != false ||
		environmentProof["contains_remote_url"] != false ||
		environmentProof["contains_credentials"] != false {
		t.Fatalf("demo environment proof should stay observed-only and redacted: %#v", environmentProof)
	}
	if plan["readiness_status"] == "ready" {
		if environmentProof["proof_state"] != "observed" ||
			environmentProof["proof_ready"] != true ||
			environmentProof["live_environment_data_observed"] != true ||
			len(stringSliceFromAny(environmentProof["missing_evidence"])) != 0 {
			t.Fatalf("ready demo environment proof should be observed: %#v", environmentProof)
		}
	} else if environmentProof["proof_ready"] != false {
		t.Fatalf("non-ready demo environment proof should not be ready: %#v", environmentProof)
	}
	for _, field := range []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(environmentProof["suppressed_fields"]), field) {
			t.Fatalf("demo environment proof suppressed fields missing %q: %#v", field, environmentProof["suppressed_fields"])
		}
	}

	graphPlan := mapFromAny(plan["graph_proof_plan"])
	if graphPlan["mode"] != "first_version_demo_graph_proof_plan" ||
		graphPlan["proof_ready"] != false ||
		graphPlan["proof_ready_reason"] != "demo_graph_proof_execution_disabled" ||
		graphPlan["asset_graph_written"] != false ||
		graphPlan["asset_sync_triggered"] != false ||
		graphPlan["graph_query_performed"] != false ||
		graphPlan["external_call_made"] != false {
		t.Fatalf("demo graph proof plan should stay disabled and redacted: %#v", graphPlan)
	}
	if plan["readiness_status"] == "ready" && graphPlan["metadata_ready"] != true {
		t.Fatalf("ready demo graph proof plan should mark metadata ready: %#v", graphPlan)
	}
	if plan["readiness_status"] != "ready" && graphPlan["metadata_ready"] != false {
		t.Fatalf("non-ready demo graph proof plan should mark metadata not ready: %#v", graphPlan)
	}
	for _, path := range []string{"project:*", "project:* -> repository:*", "repository:* -> git_remote:*", "repository:* -> second git_remote:*"} {
		if !containsString(stringSliceFromAny(graphPlan["required_graph_paths"]), path) {
			t.Fatalf("demo graph required paths missing %q: %#v", path, graphPlan["required_graph_paths"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(graphPlan["suppressed_fields"]), field) {
			t.Fatalf("demo graph suppressed fields missing %q: %#v", field, graphPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"asset_graph_write_disabled"} {
		if !containsString(stringSliceFromAny(graphPlan["blocked_reasons"]), reason) {
			t.Fatalf("demo graph blocked reasons missing %q: %#v", reason, graphPlan["blocked_reasons"])
		}
	}
	if plan["readiness_status"] == "ready" {
		if containsString(stringSliceFromAny(graphPlan["blocked_reasons"]), "graph_proof_incomplete") {
			t.Fatalf("ready demo graph proof should not report incomplete graph proof: %#v", graphPlan["blocked_reasons"])
		}
	} else if !containsString(stringSliceFromAny(graphPlan["blocked_reasons"]), "graph_proof_incomplete") {
		t.Fatalf("non-ready demo graph proof should report incomplete graph proof: %#v", graphPlan["blocked_reasons"])
	}

	resultPlan := mapFromAny(plan["result_recording_plan"])
	if resultPlan["mode"] != "first_version_demo_data_result_recording_plan" ||
		resultPlan["result_recording_state"] != "blocked" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_recording_ready_reason"] != "demo_data_execution_not_performed" ||
		resultPlan["recording_enabled"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["readiness_snapshot_written"] != false ||
		resultPlan["asset_graph_snapshot_written"] != false ||
		resultPlan["raw_remote_url_recorded"] != false ||
		resultPlan["raw_credentials_recorded"] != false {
		t.Fatalf("demo result recording plan should stay disabled and redacted: %#v", resultPlan)
	}
	for _, field := range []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status"} {
		if !containsString(stringSliceFromAny(resultPlan["required_result_fields"]), field) {
			t.Fatalf("demo result required fields missing %q: %#v", field, resultPlan["required_result_fields"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret"} {
		if !containsString(stringSliceFromAny(resultPlan["suppressed_fields"]), field) {
			t.Fatalf("demo result suppressed fields missing %q: %#v", field, resultPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"demo_data_execution_not_performed", "readiness_snapshot_not_recorded", "asset_graph_snapshot_not_recorded"} {
		if !containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), reason) {
			t.Fatalf("demo result blocked reasons missing %q: %#v", reason, resultPlan["blocked_reasons"])
		}
	}
	preflight := mapFromAny(resultPlan["result_recording_preflight"])
	if preflight["mode"] != "first_version_demo_data_result_recording_preflight" ||
		preflight["readiness_status"] != plan["readiness_status"] ||
		preflight["snapshot_write_enabled"] != false ||
		preflight["asset_graph_write_enabled"] != false ||
		preflight["operation_log_write_enabled"] != false ||
		preflight["external_call_made"] != false ||
		preflight["contains_remote_url"] != false ||
		preflight["contains_credentials"] != false {
		t.Fatalf("demo result preflight should stay review-only and redacted: %#v", preflight)
	}
	if plan["readiness_status"] == "ready" {
		if preflight["readiness_snapshot_ready_for_review"] != true ||
			preflight["asset_graph_snapshot_ready_for_review"] != true ||
			preflight["snapshot_contract_ready"] != true ||
			len(stringSliceFromAny(preflight["missing_required_evidence"])) != 0 {
			t.Fatalf("ready demo result preflight should be review-ready without writes: %#v", preflight)
		}
	} else if preflight["readiness_snapshot_ready_for_review"] != false ||
		preflight["asset_graph_snapshot_ready_for_review"] != false ||
		preflight["snapshot_contract_ready"] != false ||
		!containsString(stringSliceFromAny(preflight["blocked_reasons"]), "required_demo_evidence_missing") {
		t.Fatalf("non-ready demo result preflight should stay blocked: %#v", preflight)
	}
	for _, field := range []string{"project_asset_id", "repository_asset_id", "source_remote_asset_id", "mirror_remote_asset_id", "graph_proof_status", "readiness_status", "evidence_counts", "missing_required_evidence"} {
		if !containsString(stringSliceFromAny(preflight["required_snapshot_fields"]), field) {
			t.Fatalf("demo result preflight required snapshot field missing %q: %#v", field, preflight["required_snapshot_fields"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "repository_secret", "webhook_secret", "raw_graph_payload", "operation_log_body"} {
		if !containsString(stringSliceFromAny(preflight["suppressed_fields"]), field) {
			t.Fatalf("demo result preflight suppressed field missing %q: %#v", field, preflight["suppressed_fields"])
		}
	}
	for _, backend := range []string{"demo_result_write", "readiness_snapshot_write", "asset_graph_snapshot_write", "operation_log_write"} {
		if !containsString(stringSliceFromAny(preflight["disabled_backends"]), backend) {
			t.Fatalf("demo result preflight disabled backend missing %q: %#v", backend, preflight["disabled_backends"])
		}
	}
	for _, reason := range []string{"demo_result_write_disabled", "readiness_snapshot_write_disabled", "asset_graph_snapshot_write_disabled"} {
		if !containsString(stringSliceFromAny(preflight["blocked_reasons"]), reason) {
			t.Fatalf("demo result preflight blocked reason missing %q: %#v", reason, preflight["blocked_reasons"])
		}
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func writeSHA256SUMS(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	var lines []string
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
		lines = append(lines, fmt.Sprintf("%x  %s", sha256.Sum256([]byte(content)), name))
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write SHA256SUMS: %v", err)
	}
}
