package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigRepositoryRefRefreshSnapshotPayloadSanitizesEvidence(t *testing.T) {
	repo := configRepositoryPromotionSnapshotRepo()
	preview := configRepositoryRefRefreshSnapshotReadyPreview(repo)
	snapshot := configRepositoryRefRefreshSnapshotPayload(repo, preview, true)
	ready, state, missing := configRepositoryRefRefreshSnapshotReadiness(snapshot)
	if !ready || state != "ref_refresh_recorded" || len(missing) != 0 {
		t.Fatalf("readiness = %v/%s/%#v; snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["config_ref_refresh_observed"] != true ||
		snapshot["config_ref_refresh_completed"] != true ||
		snapshot["status_snapshot_write_eligible"] != true ||
		snapshot["status_snapshot_written"] != true ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		snapshot["git_fetch_performed"] != true ||
		snapshot["git_write_performed"] != false ||
		snapshot["git_commit_created"] != false ||
		snapshot["git_push_performed"] != false ||
		snapshot["project_version_pin_written"] != false ||
		snapshot["live_remote_validation_performed"] != false ||
		snapshot["contains_commit_sha"] != false ||
		snapshot["contains_remote_url"] != false ||
		snapshot["raw_git_output_recorded"] != false {
		t.Fatalf("unexpected ref refresh snapshot payload: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"https://token@", "Bearer secret", "abc123", "secret git output", "secret failure"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("ref refresh snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func configRepositoryPromotionSnapshotRepo() map[string]any {
	return map[string]any{
		"id":             "repo-1",
		"project_id":     "project-1",
		"name":           "Config Repository",
		"repo_key":       "config",
		"repo_role":      "config",
		"default_branch": "main",
	}
}

func configRepositoryRefRefreshSnapshotReadyPreview(repo map[string]any) map[string]any {
	return configRepositoryScaffoldPreview(
		repo,
		[]map[string]any{{
			"id":               "remote-1",
			"name":             "origin",
			"remote_key":       "github",
			"provider_type":    "github",
			"remote_role":      "target",
			"default_branch":   "main",
			"latest_sha":       "abc123",
			"last_sync_status": "completed",
			"remote_url":       "https://token@example.com/repo.git",
		}},
		nil,
		nil,
		[]map[string]any{{
			"id":             "op-refresh-1",
			"git_remote_id":  "remote-1",
			"status":         "completed",
			"error":          "",
			"remote_url":     "https://token@example.com/repo.git",
			"provider_token": "Bearer secret",
			"commit_sha":     "abc123",
			"git_output":     "secret git output",
		}},
	)
}

func configRepositoryPromotionSnapshotReadyPreview(repo map[string]any) map[string]any {
	return configRepositoryScaffoldPreview(
		repo,
		[]map[string]any{{
			"id":               "remote-1",
			"name":             "origin",
			"remote_key":       "github",
			"provider_type":    "github",
			"remote_role":      "target",
			"default_branch":   "main",
			"latest_sha":       "abc123",
			"last_sync_status": "completed",
		}},
		nil,
		[]map[string]any{{
			"id":                  "op-config-1",
			"status":              "completed",
			"operation_log_count": int64(2),
		}},
	)
}

func TestConfigRepositoryProjectVersionPinEvidenceBoundaries(t *testing.T) {
	repo := map[string]any{"id": "repo-config-a", "repo_key": "config-a", "repo_role": "config", "default_branch": "main"}
	remotes := []map[string]any{{"id": "remote-1", "latest_sha": "abc123"}}
	tests := []struct {
		name          string
		versions      []map[string]any
		wantPinned    int
		wantValidated int
		wantMismatch  int
		wantLiveState string
	}{
		{
			name: "same role but different repo key is ignored",
			versions: []map[string]any{{
				"id":      "version-other",
				"version": "v0.1.0",
				"metadata": map[string]any{"repositories": []any{
					map[string]any{"repo_key": "config-b", "repo_role": "config", "remote_id": "remote-1", "config_commit_sha": "abc123"},
				}},
			}},
			wantLiveState: "not_recorded",
		},
		{
			name: "mismatched synced latest sha is observed",
			versions: []map[string]any{{
				"id":      "version-mismatch",
				"version": "v0.2.0",
				"metadata": map[string]any{"repositories": []any{
					map[string]any{"repo_key": "config-a", "repo_role": "config", "remote_id": "remote-1", "config_commit_sha": "def456"},
				}},
			}},
			wantPinned:    1,
			wantMismatch:  1,
			wantLiveState: "mismatched",
		},
		{
			name: "missing remote latest sha waits for synced remote",
			versions: []map[string]any{{
				"id":      "version-waiting",
				"version": "v0.3.0",
				"metadata": map[string]any{"repositories": []any{
					map[string]any{"repository_id": "repo-config-a", "repo_role": "config", "remote_id": "remote-missing", "config_commit_sha": "abc123"},
				}},
			}},
			wantPinned:    1,
			wantLiveState: "waiting_for_synced_remote",
		},
		{
			name:          "empty versions are not recorded",
			wantLiveState: "not_recorded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := configRepositoryProjectVersionPinEvidence(repo, remotes, tt.versions)
			if intFromAny(evidence["pinned_version_count"], 0) != tt.wantPinned ||
				intFromAny(evidence["validated_version_count"], 0) != tt.wantValidated ||
				intFromAny(evidence["mismatched_version_count"], 0) != tt.wantMismatch ||
				evidence["live_validation_state"] != tt.wantLiveState {
				t.Fatalf("unexpected evidence: %#v", evidence)
			}
			encoded, _ := json.Marshal(evidence)
			for _, forbidden := range []string{"abc123", "def456"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("evidence leaked commit sha %q: %s", forbidden, encoded)
				}
			}
		})
	}
}
