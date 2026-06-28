package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepoTagActionsRefreshPlanMarksCanonicalActionLink(t *testing.T) {
	lookupPreflight := map[string]any{"lookup_state": "observed"}
	plan := repoTagActionsRefreshPlan("observed", true, false, lookupPreflight, "completed", map[string]any{"count": 2})
	if plan["refresh_state"] != "recorded" ||
		plan["github_actions_refresh_performed"] != true ||
		plan["github_action_runs_synced"] != true ||
		intFromAny(plan["github_action_runs_synced_count"], 0) != 2 ||
		plan["repo_tag_run_link_written"] != true ||
		plan["repo_tag_run_link_source"] != "canonical_asset_relation" ||
		plan["repo_tag_run_link_write_mode"] != "derived_canonical_relation" ||
		plan["external_call_made"] != true {
		t.Fatalf("completed actions refresh plan should mark canonical action links: %#v", plan)
	}
	if !containsString(stringSliceFromAny(plan["disabled_backends"]), "github_action_run_link_write") {
		t.Fatalf("direct action-link write should remain disabled while graph link is derived: %#v", plan["disabled_backends"])
	}
	if !containsString(stringSliceFromAny(plan["disabled_backends"]), "provider_response_recording") ||
		!containsString(stringSliceFromAny(plan["blocked_reasons"]), "provider_response_recording_not_performed") {
		t.Fatalf("plan should keep only provider response recording disabled: %#v", plan)
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{"abc123", "v1.0.0", "deploy.yml", "https://github.com/example/actions"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("actions refresh plan leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRepoTagActionsRefreshPlanCompletedWithoutActionsEvidenceWaits(t *testing.T) {
	lookupPreflight := map[string]any{"lookup_state": "observed"}
	plan := repoTagActionsRefreshPlan("observed", true, false, lookupPreflight, "completed", map[string]any{"count": 0})
	if plan["refresh_state"] != "waiting_for_actions_refresh" ||
		plan["github_actions_refresh_performed"] != true ||
		plan["github_action_runs_synced"] != false ||
		intFromAny(plan["github_action_runs_synced_count"], 0) != 0 ||
		plan["repo_tag_run_link_written"] != false ||
		plan["external_call_made"] != true {
		t.Fatalf("completed refresh without actions evidence should wait for synced action rows: %#v", plan)
	}
	if !containsString(stringSliceFromAny(plan["blocked_reasons"]), "github_actions_refresh_evidence_missing") ||
		!containsString(stringSliceFromAny(plan["blocked_reasons"]), "github_action_run_link_write_not_performed") ||
		!containsString(stringSliceFromAny(plan["execution_blockers"]), "github_actions_refresh_evidence_missing") {
		t.Fatalf("completed refresh without actions evidence should expose missing evidence blockers: %#v", plan)
	}
	if containsString(stringSliceFromAny(plan["disabled_backends"]), "github_actions_api_sync") {
		t.Fatalf("completed refresh should not claim provider sync backend is still pending: %#v", plan["disabled_backends"])
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{"abc123", "v1.0.0", "deploy.yml", "https://github.com/example/actions"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("actions refresh plan leaked %q: %s", forbidden, encoded)
		}
	}
}
