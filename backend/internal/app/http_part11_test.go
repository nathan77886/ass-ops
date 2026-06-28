package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPinConfigCommitMetadataAppendsMissingManifest(t *testing.T) {
	next, changed, err := pinConfigCommitMetadata(map[string]any{
		"repositories": []map[string]any{{
			"repository_id": "repo-other",
			"repo_key":      "service",
		}},
	}, map[string]any{
		"id":        "repo-1",
		"repo_key":  "config",
		"repo_role": "config",
	}, map[string]any{
		"id":            "remote-1",
		"remote_key":    "github",
		"remote_role":   "target",
		"provider_type": "github",
	}, "abc123")
	if err != nil {
		t.Fatalf("pinConfigCommitMetadata returned error: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	repositories := sliceOfMapsFromAny(next["repositories"])
	if len(repositories) != 2 {
		t.Fatalf("repositories = %#v", repositories)
	}
	appended := repositories[1]
	if appended["repository_id"] != "repo-1" ||
		appended["repo_key"] != "config" ||
		appended["remote_id"] != "remote-1" ||
		appended["config_commit_sha"] != "abc123" {
		t.Fatalf("appended manifest = %#v", appended)
	}
}

func TestPinConfigCommitMetadataMatchesExistingManifestByRepoKey(t *testing.T) {
	next, changed, err := pinConfigCommitMetadata(map[string]any{
		"repositories": []map[string]any{{
			"repository_id": "old-repo-id",
			"repo_key":      "config",
			"tag":           "keep-me",
		}},
	}, map[string]any{
		"id":        "repo-1",
		"repo_key":  "config",
		"repo_role": "config",
	}, map[string]any{
		"id":            "remote-1",
		"remote_key":    "github",
		"remote_role":   "target",
		"provider_type": "github",
	}, "abc123")
	if err != nil {
		t.Fatalf("pinConfigCommitMetadata returned error: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	repositories := sliceOfMapsFromAny(next["repositories"])
	if len(repositories) != 1 {
		t.Fatalf("repositories should update in place by repo_key, got %#v", repositories)
	}
	item := repositories[0]
	if item["repository_id"] != "repo-1" ||
		item["repo_key"] != "config" ||
		item["config_commit_sha"] != "abc123" ||
		item["tag"] != "keep-me" {
		t.Fatalf("repo_key matched manifest = %#v", item)
	}
}

func TestPinConfigCommitMetadataAlreadyPinned(t *testing.T) {
	next, changed, err := pinConfigCommitMetadata(map[string]any{
		"repositories": []map[string]any{{
			"repository_id":     "repo-1",
			"repo_key":          "config",
			"repo_role":         "config",
			"remote_id":         "remote-1",
			"remote_key":        "github",
			"remote_role":       "target",
			"provider_type":     "github",
			"config_commit_sha": "abc123",
			"validation_status": "local_synced_remote_latest_sha",
		}},
	}, map[string]any{
		"id":        "repo-1",
		"repo_key":  "config",
		"repo_role": "config",
	}, map[string]any{
		"id":            "remote-1",
		"remote_key":    "github",
		"remote_role":   "target",
		"provider_type": "github",
	}, "abc123")
	if err != nil {
		t.Fatalf("pinConfigCommitMetadata returned error: %v", err)
	}
	if changed {
		t.Fatalf("changed = true, want false")
	}
	repositories := sliceOfMapsFromAny(next["repositories"])
	if len(repositories) != 1 || repositories[0]["config_commit_sha"] != "abc123" {
		t.Fatalf("repositories = %#v", repositories)
	}
}

func TestConfigRepositoryScaffoldPreviewReconcilesProjectVersionPinEvidence(t *testing.T) {
	preview := configRepositoryScaffoldPreview(
		map[string]any{
			"id":             "repo-1",
			"name":           "Config Repository",
			"repo_key":       "config",
			"repo_role":      "config",
			"default_branch": "main",
		},
		[]map[string]any{{
			"id":               "remote-1",
			"name":             "origin",
			"remote_key":       "github",
			"provider_type":    "github",
			"remote_role":      "target",
			"default_branch":   "main",
			"latest_sha":       "ABC123",
			"last_sync_status": "completed",
		}},
		[]map[string]any{{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{
					"repo_key":           "config",
					"repo_role":          "config",
					"remote_id":          "remote-1",
					"config_commit_sha":  "abc123",
					"provider_token":     "secret-token",
					"remote_url":         "https://token@example.com/repo.git",
					"raw_provider_body":  "secret-body",
					"git_credentials":    "secret-credentials",
					"authorization":      "Bearer secret",
					"unrelated_password": "password",
				},
			}},
		}},
	)

	evidence := mapFromAny(preview["project_version_pin_evidence"])
	if evidence["pin_state"] != "recorded" ||
		evidence["live_validation_state"] != "recorded" ||
		evidence["config_commit_sha_recorded"] != true ||
		evidence["live_validation_recorded"] != true ||
		evidence["commit_sha_included"] != false ||
		evidence["remote_url_included"] != false ||
		evidence["secret_included"] != false ||
		intFromAny(evidence["pinned_version_count"], 0) != 1 ||
		intFromAny(evidence["validated_version_count"], 0) != 1 {
		t.Fatalf("unexpected pin evidence: %#v", evidence)
	}
	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["project_version_pin_written"] != false ||
		commitPlan["project_version_pin_observed"] != true ||
		commitPlan["live_commit_validation_performed"] != false ||
		commitPlan["live_commit_validation_observed"] != true ||
		statusByKind(sliceOfMapsFromAny(commitPlan["steps"]), "project_version_pin") != "planned" ||
		statusByKind(sliceOfMapsFromAny(commitPlan["steps"]), "live_commit_validation") != "planned" {
		t.Fatalf("unexpected commit plan evidence: %#v", commitPlan)
	}
	pinPlan := mapFromAny(commitPlan["project_version_pin_plan"])
	if pinPlan["pin_state"] != "observed" ||
		pinPlan["pin_ready"] != true ||
		pinPlan["project_version_pin_written"] != false ||
		pinPlan["project_version_pin_observed"] != true ||
		pinPlan["config_commit_sha_recorded"] != true ||
		pinPlan["live_commit_validation_recorded"] != true ||
		pinPlan["contains_commit_sha"] != false ||
		pinPlan["contains_remote_url"] != false {
		t.Fatalf("unexpected pin plan: %#v", pinPlan)
	}
	pinWritePreflight := mapFromAny(pinPlan["pin_write_preflight_plan"])
	if pinWritePreflight["preflight_state"] != "observed" ||
		pinWritePreflight["pin_write_ready_for_review"] != false ||
		pinWritePreflight["project_version_pin_observed"] != true ||
		pinWritePreflight["live_commit_validation_observed"] != true ||
		pinWritePreflight["project_version_pin_written"] != false ||
		intFromAny(pinWritePreflight["pinned_version_count"], 0) != 1 ||
		intFromAny(pinWritePreflight["validated_version_count"], 0) != 1 {
		t.Fatalf("unexpected pin write preflight: %#v", pinWritePreflight)
	}
	assertConfigRepositoryPinWritePreflightPlanSafe(t, pinWritePreflight)
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "partial" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["project_version_pin_written"] != false ||
		resultPlan["project_version_pin_observed"] != true ||
		resultPlan["config_commit_sha_recorded"] != true ||
		resultPlan["live_validation_recorded"] != true ||
		resultPlan["sanitized_audit_result_recorded"] != false ||
		resultPlan["raw_git_output_recorded"] != false ||
		resultPlan["raw_provider_response_recorded"] != false {
		t.Fatalf("unexpected result plan: %#v", resultPlan)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"secret-token", "https://token@", "secret-body", "secret-credentials", "Bearer secret", "password"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("config pin evidence leaked %q: %s", forbidden, encoded)
		}
	}
}
