package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONValueScanAndMarshal(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "object", input: `{"adapter":true}`},
		{name: "array", input: `["main","dev"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var value JSONValue
			if err := value.Scan([]byte(tt.input)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tt.input {
				t.Fatalf("json = %s, want %s", got, tt.input)
			}
		})
	}
}

func TestSanitizeMetadataRedactsSensitiveKeys(t *testing.T) {
	got := sanitizeMetadata(map[string]any{
		"github_token": "secret",
		"github": map[string]any{
			"access_token": "nested-secret",
			"owner":        "acme",
		},
		"owner": "acme",
	})
	if got["github_token"] != "<redacted>" {
		t.Fatalf("github_token was not redacted: %v", got["github_token"])
	}
	nested := got["github"].(map[string]any)
	if nested["access_token"] != "<redacted>" {
		t.Fatalf("nested access_token was not redacted: %v", nested["access_token"])
	}
	if nested["owner"] != "acme" || got["owner"] != "acme" {
		t.Fatalf("non-sensitive metadata changed: %#v", got)
	}
}

func TestSanitizeMetadataNilReturnsEmptyMap(t *testing.T) {
	got := sanitizeMetadata(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("sanitizeMetadata(nil) = %#v, want empty map", got)
	}
}

func TestSanitizeURLUserInfo(t *testing.T) {
	got := sanitizeURLUserInfo("clone https://token@example.com/org/repo.git and ssh://user:pass@git.example.com/org/repo.git")
	for _, leaked := range []string{"token@example.com", "user:pass@git.example.com"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitizeURLUserInfo leaked %q in %q", leaked, got)
		}
	}
	if !strings.Contains(got, "https://<redacted>@example.com") || !strings.Contains(got, "ssh://<redacted>@git.example.com") {
		t.Fatalf("sanitizeURLUserInfo did not preserve redacted hosts: %q", got)
	}
}

func TestSanitizeRowValueRedactsURLArrays(t *testing.T) {
	got, ok := sanitizeRowValue("urls", []any{"https://token@example.com/org/repo.git"}).([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("sanitizeRowValue returned %#v", got)
	}
	text, _ := got[0].(string)
	if strings.Contains(text, "token@example.com") {
		t.Fatalf("sanitizeRowValue leaked URL credentials in %q", text)
	}
	if text != "https://<redacted>@example.com/org/repo.git" {
		t.Fatalf("sanitized URL = %q", text)
	}
}

func TestNormalizeRowRedactsSensitiveByteStrings(t *testing.T) {
	row := map[string]any{"secret_token": []byte("plain-secret")}
	normalizeRow(row)
	if row["secret_token"] != "<redacted>" {
		t.Fatalf("secret_token = %v, want redacted", row["secret_token"])
	}
}

func TestNormalizeRowKeepsReviewStatusAndPresenceBooleans(t *testing.T) {
	row := map[string]any{
		"token_subject_review_status":   []byte("reviewed"),
		"kubeconfig_secret_ref_present": true,
		"rbac_restart_pods_status":      []byte("reviewed"),
	}
	normalizeRow(row)
	if row["token_subject_review_status"] != "reviewed" ||
		row["kubeconfig_secret_ref_present"] != true ||
		row["rbac_restart_pods_status"] != "reviewed" {
		t.Fatalf("review/presence metadata should not be redacted: %#v", row)
	}
}

func TestCanonicalAssetSpecKeysAreStable(t *testing.T) {
	if got := assetKey("project", "projects", "project-1"); got != "project|projects|project-1" {
		t.Fatalf("assetKey = %q", got)
	}
	if got := relationKey("from", "to", "owns"); got != "from|to|owns" {
		t.Fatalf("relationKey = %q", got)
	}
}

func TestWorkerNodeAssetSpecIsNarrow(t *testing.T) {
	node := GormWorkerNode{GormBase: GormBase{ID: "node-1"}, Name: "node-a", Kind: "local", Status: "online"}
	spec := workerNodeAssetSpec(node)
	if spec.AssetType != "node_agent" || spec.SourceTable != "worker_nodes" || spec.SourceID != "node-1" {
		t.Fatalf("worker node asset spec = %#v", spec)
	}
	if spec.ProjectID != "" {
		t.Fatalf("worker node asset should be global: %#v", spec)
	}
}

func TestDemoSeedDefaultsAreSafeForLocalDemos(t *testing.T) {
	defaults := defaultDemoSeedDefaults()
	if defaults.ProjectSlug != "assops-demo" {
		t.Fatalf("ProjectSlug = %q, want assops-demo", defaults.ProjectSlug)
	}
	if defaults.SourceRemoteKey == defaults.TargetRemoteKey {
		t.Fatalf("source and target remotes should be distinct: %#v", defaults)
	}
	if defaults.RepoSyncEnabled {
		t.Fatalf("repo sync demo asset should be disabled by default")
	}
	if !strings.HasPrefix(defaults.SSHHost, "192.0.2.") {
		t.Fatalf("SSHHost = %q, want TEST-NET-1 demo address", defaults.SSHHost)
	}
}

func TestDemoSeedFixturesAreNonDestructive(t *testing.T) {
	if got := demoRepoSyncStdout("completed"); !strings.Contains(got, "Fetched refs/heads/main") {
		t.Fatalf("completed stdout = %q", got)
	}
	if got := demoRepoSyncStdout("failed"); !strings.Contains(got, "push was rejected") {
		t.Fatalf("failed stdout = %q", got)
	}
	if prompt := demoAgentPrompt(); !strings.Contains(prompt, "Do not mutate anything") {
		t.Fatalf("demo agent prompt should be read-only: %q", prompt)
	}
}
