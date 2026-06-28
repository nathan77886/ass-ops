package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSHMachineRehearsalSnapshotPayloadSanitizesAttestation(t *testing.T) {
	preview := buildSSHMachineRehearsalPreview(
		map[string]any{
			"id":         "machine-1",
			"project_id": "project-1",
			"name":       "prod-api",
			"host":       "10.0.0.12",
			"port":       22,
			"username":   "deploy",
			"auth_type":  "key",
			"metadata": map[string]any{
				"key_path":                      "/etc/assops/ssh/prod-api",
				"known_hosts_path":              "/etc/assops/ssh/known_hosts",
				"live_rehearsal_runbook":        "https://runbooks.example.com/ssh/prod-api",
				"authorized_fixture_id":         "fixture-prod-api-1",
				"operator_approved_by":          "alice@example.com",
				"operator_approval_note":        "approved for production rehearsal",
				"live_rehearsal_environment":    "prod",
				"operator_environment_proof_id": "env-proof-1",
				"private_key_should_not_leak":   "BEGIN OPENSSH PRIVATE KEY",
			},
		},
		[]map[string]any{
			{"id": "run-2", "status": "completed", "exit_code": 0, "operation_type": "ssh.exec", "command": "cat /etc/passwd", "stdout": "secret output", "stderr": "secret error"},
			{"id": "run-1", "status": "completed", "exit_code": 0, "operation_type": "ssh.verify", "command": "true"},
		},
	)
	snapshot := sshMachineRehearsalSnapshotPayload(preview, true)
	ready, state, missing := sshMachineRehearsalSnapshotReadiness(preview, snapshot)
	if !ready || state != "ready_to_record" || len(missing) != 0 {
		t.Fatalf("snapshot readiness = %v/%s/%#v; snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["target_environment_attestation_ready"] != true ||
		snapshot["environment_proof_ready"] != true ||
		snapshot["live_rehearsal_controls_ready"] != true ||
		snapshot["sanitized_result_recorded"] != true ||
		snapshot["status_snapshot_write_eligible"] != true ||
		snapshot["status_snapshot_written"] != true ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		snapshot["external_call_made"] != false ||
		snapshot["ssh_process_started"] != false ||
		snapshot["command_executed"] != false ||
		snapshot["stdout_included"] != false ||
		snapshot["stderr_included"] != false ||
		snapshot["private_key_included"] != false ||
		snapshot["operator_identity_included"] != false ||
		snapshot["environment_identifier_included"] != false {
		t.Fatalf("unexpected sanitized ssh snapshot: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"10.0.0.12", "deploy", "/etc/assops/ssh/prod-api", "runbooks.example.com", "fixture-prod-api-1", "alice@example.com", "approved for production rehearsal", "env-proof-1", "BEGIN OPENSSH PRIVATE KEY", "secret output", "secret error", "cat /etc/passwd"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("ssh rehearsal snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSSHMachineRehearsalSnapshotBlocksCompletedRunsWithoutExitCode(t *testing.T) {
	preview := buildSSHMachineRehearsalPreview(
		map[string]any{
			"id":         "machine-1",
			"project_id": "project-1",
			"name":       "prod-api",
			"host":       "10.0.0.12",
			"port":       22,
			"username":   "deploy",
			"auth_type":  "key",
			"metadata": map[string]any{
				"live_rehearsal_runbook":        "runbook-1",
				"authorized_fixture_id":         "fixture-1",
				"operator_approved":             true,
				"live_rehearsal_environment":    "prod",
				"operator_environment_proof_id": "env-proof-1",
			},
		},
		[]map[string]any{
			{"id": "run-2", "status": "completed", "operation_type": "ssh.exec"},
			{"id": "run-1", "status": "completed", "operation_type": "ssh.verify"},
		},
	)
	snapshot := sshMachineRehearsalSnapshotPayload(preview, true)
	ready, state, missing := sshMachineRehearsalSnapshotReadiness(preview, snapshot)
	if ready ||
		state != "partial_recorded" ||
		snapshot["sanitized_result_recorded"] != false ||
		intFromAny(snapshot["completed_without_exit_code_runs"], 0) != 2 ||
		!containsString(missing, "ssh_completed_result_exit_code_missing") ||
		!containsString(missing, "completed_ssh_verify_missing") ||
		!containsString(missing, "completed_ssh_exec_missing") {
		t.Fatalf("completed runs without exit code should block snapshot: ready=%v state=%s missing=%#v snapshot=%#v", ready, state, missing, snapshot)
	}
}

type sshSnapshotRun struct {
	id            string
	status        string
	operationType string
}

func newSSHRehearsalSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/ssh-machines/machine-1/rehearsal-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "machine-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newSSHTargetEnvironmentProofRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/ssh-machines/machine-1/target-environment-proof", strings.NewReader(body))
	req = withRouteParam(req, "id", "machine-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func TestGormSchemaIncludesProviderAndThresholdModels(t *testing.T) {
	requireGormSchemaModel(t, &GormProviderAccount{})
	requireGormSchemaModel(t, &GormProjectVersion{})
	requireGormSchemaModel(t, &GormWebhookThresholdDecisionAudit{})
	requireGormSchemaModel(t, &GormWebhookThresholdConfiguration{})
}

func TestProviderAccountSanitizeDoesNotReturnRawTokenEnv(t *testing.T) {
	item := sanitizeProviderAccount(map[string]any{
		"id":            "account-1",
		"name":          "github-main",
		"provider_type": "github",
		"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
		"created_at":    time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		"metadata": map[string]any{
			"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT",
			"next_token_env":               "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OTHER",
			"note":                         "safe",
		},
	})
	if _, ok := item["token_env"]; ok {
		t.Fatal("sanitizeProviderAccount should remove token_env")
	}
	if item["token_configured"] != true {
		t.Fatalf("token_configured = %v, want true", item["token_configured"])
	}
	if got := fmt.Sprint(item["masked_token_env"]); strings.Contains(got, "GITHUB_MAIN") {
		t.Fatalf("masked token env leaked suffix: %q", got)
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN") {
		t.Fatalf("sanitized account leaked token env: %s", encoded)
	}
	if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT") ||
		strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OTHER") {
		t.Fatalf("sanitized account leaked candidate token env: %s", encoded)
	}
	if status := mapFromAny(item["token_rotation_status"]); status["status"] == "" {
		t.Fatalf("token_rotation_status missing: %#v", item)
	}
	metadata := mapFromAny(item["metadata"])
	if metadata["note"] != "safe" {
		t.Fatalf("metadata note should be preserved without token env fields: %#v", metadata)
	}
	candidate := mapFromAny(item["token_rotation_candidate"])
	if candidate["safe"] != true || candidate["same_as_current"] != false {
		t.Fatalf("candidate status = %#v", candidate)
	}
}

func TestProviderAccountTokenRotationStatus(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		item map[string]any
		want string
		src  string
	}{
		{
			name: "fresh from rotation metadata",
			item: map[string]any{
				"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":  map[string]any{"token_rotation": map[string]any{"rotated_at": now.AddDate(0, 0, -10).Format(time.RFC3339)}},
			},
			want: "fresh",
			src:  "token_rotation",
		},
		{
			name: "soon from created at fallback",
			item: map[string]any{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -80),
			},
			want: "soon",
			src:  "created_at",
		},
		{
			name: "due from created at fallback",
			item: map[string]any{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -120),
			},
			want: "due",
			src:  "created_at",
		},
		{
			name: "missing token env",
			item: map[string]any{
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -120),
			},
			want: "missing",
			src:  "unknown",
		},
		{
			name: "unknown without timestamps",
			item: map[string]any{
				"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":  map[string]any{},
			},
			want: "unknown",
			src:  "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := providerAccountTokenRotationStatus(tt.item, now)
			if got["status"] != tt.want || got["source"] != tt.src {
				t.Fatalf("status = %#v, want status=%s source=%s", got, tt.want, tt.src)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN") {
				t.Fatalf("rotation status leaked token env: %s", encoded)
			}
		})
	}
}

func TestProviderAccountTokenRotationPlanSummaryDoesNotLeakTokenEnv(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	summary := providerAccountTokenRotationPlanSummary([]map[string]any{
		{
			"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
			"metadata":   map[string]any{},
			"created_at": now.AddDate(0, 0, -120),
		},
		{
			"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
			"metadata":   map[string]any{},
			"created_at": now.AddDate(0, 0, -80),
		},
		{
			"metadata":   map[string]any{},
			"created_at": now,
		},
		{
			"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_UNKNOWN",
			"metadata":  map[string]any{},
		},
	}, now)
	if summary["total"] != 4 || summary["due"] != 1 || summary["soon"] != 1 ||
		summary["missing"] != 1 || summary["unknown"] != 1 || summary["action_required"] != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN") {
		t.Fatalf("rotation plan summary leaked token env: %s", encoded)
	}
	if !strings.Contains(fmt.Sprint(summary["next_action"]), "Rotate due or missing") {
		t.Fatalf("next action = %v", summary["next_action"])
	}
}
