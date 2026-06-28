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

func TestProviderAccountTokenRotationPlanSummaryNextActions(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rows []map[string]any
		want string
	}{
		{
			name: "empty",
			rows: nil,
			want: "No provider accounts configured.",
		},
		{
			name: "due",
			rows: []map[string]any{
				{
					"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
					"metadata":   map[string]any{},
					"created_at": now.AddDate(0, 0, -120),
				},
				{
					"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
					"metadata":   map[string]any{},
					"created_at": now.AddDate(0, 0, -10),
				},
			},
			want: "Rotate due provider token env values before external template provisioning.",
		},
		{
			name: "missing",
			rows: []map[string]any{{
				"metadata":   map[string]any{},
				"created_at": now,
			}},
			want: "Configure missing provider token env values before external template provisioning.",
		},
		{
			name: "soon",
			rows: []map[string]any{{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -80),
			}},
			want: "Schedule provider token env rotation before the next due window.",
		},
		{
			name: "unknown",
			rows: []map[string]any{{
				"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_UNKNOWN",
				"metadata":  map[string]any{},
			}},
			want: "Run a provider account check or rotate token env to establish rotation evidence.",
		},
		{
			name: "fresh",
			rows: []map[string]any{{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -10),
			}},
			want: "Provider token rotation evidence is fresh.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := providerAccountTokenRotationPlanSummary(tt.rows, now)
			if summary["next_action"] != tt.want {
				t.Fatalf("next_action = %v, want %s", summary["next_action"], tt.want)
			}
		})
	}
}

func TestProviderAccountAutomatedRotationPlan(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	plan := providerAccountAutomatedRotationPlan([]map[string]any{
		{
			"id":            "github-due",
			"name":          "github due",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT"},
			"created_at":    now.AddDate(0, 0, -120),
		},
		{
			"id":            "gitea-soon-missing-candidate",
			"name":          "gitea soon",
			"provider_type": "gitea",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
			"metadata":      map[string]any{},
			"created_at":    now.AddDate(0, 0, -80),
		},
		{
			"id":            "github-fresh",
			"name":          "github fresh",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_FRESH",
			"metadata":      map[string]any{},
			"created_at":    now.AddDate(0, 0, -10),
		},
		{
			"id":            "github-unsafe",
			"name":          "github unsafe",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "BAD_TOKEN_ENV"},
			"created_at":    now.AddDate(0, 0, -120),
		},
		{
			"id":            "github-same",
			"name":          "github same",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT"},
			"created_at":    now.AddDate(0, 0, -120),
		},
	}, now)
	if plan["mode"] != "dry_run" || plan["automation_enabled"] != false || plan["external_call_made"] != false {
		t.Fatalf("plan should be dry-run only: %#v", plan)
	}
	if plan["execution_available"] != true {
		t.Fatalf("plan should expose ready execution availability: %#v", plan)
	}
	if plan["ready"] != 1 || plan["blocked"] != 3 || plan["not_needed"] != 1 {
		t.Fatalf("plan counts = %#v", plan)
	}
	items := sliceOfMapsFromAny(plan["items"])
	byID := map[string]map[string]any{}
	for _, item := range items {
		byID[fmt.Sprint(item["provider_account_id"])] = item
	}
	if byID["github-due"]["status"] != "ready" {
		t.Fatalf("github due should be ready: %#v", byID["github-due"])
	}
	if byID["gitea-soon-missing-candidate"]["status"] != "blocked" {
		t.Fatalf("missing candidate should be blocked: %#v", byID["gitea-soon-missing-candidate"])
	}
	if byID["github-fresh"]["status"] != "not_needed" {
		t.Fatalf("fresh account should not need rotation: %#v", byID["github-fresh"])
	}
	if byID["github-unsafe"]["status"] != "blocked" || byID["github-same"]["status"] != "blocked" {
		t.Fatalf("unsafe/same candidates should be blocked: %#v %#v", byID["github-unsafe"], byID["github-same"])
	}
	if !strings.Contains(fmt.Sprint(byID["github-unsafe"]["blocked_reason"]), "not allowed") {
		t.Fatalf("unsafe candidate should explain allowlist failure: %#v", byID["github-unsafe"])
	}
	encoded, _ := json.Marshal(plan)
	for _, leak := range []string{
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
		"BAD_TOKEN_ENV",
	} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("automated rotation plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderAccountAutomatedRotationExecutionCandidates(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	items := []map[string]any{
		{
			"id":            "github-ready",
			"name":          "github ready",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT"},
			"created_at":    now.AddDate(0, 0, -120),
		},
		{
			"id":            "github-fresh",
			"name":          "github fresh",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_FRESH",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OTHER"},
			"created_at":    now.AddDate(0, 0, -10),
		},
		{
			"id":            "gitea-unsafe",
			"name":          "gitea unsafe",
			"provider_type": "gitea",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_WRONG"},
			"created_at":    now.AddDate(0, 0, -120),
		},
	}
	candidates := providerAccountAutomatedRotationExecutionCandidates(items, now)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
	if rawStringFromMap(candidates[0].account, "id") != "github-ready" ||
		candidates[0].tokenEnv != "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT" ||
		providerAccountAutomatedRotationPlanItem(candidates[0].account, now)["status"] != "ready" {
		t.Fatalf("candidate = %#v", candidates[0])
	}
	plan := providerAccountAutomatedRotationPlan(items, now)
	encoded, _ := json.Marshal(plan)
	for _, leak := range []string{
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_WRONG",
	} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("execution plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestValidateProviderAccountInputRejectsWrongTokenEnv(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	_, err := validateProviderAccountInput(context.Background(), "bad", "github", server.URL, "", "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN", "", "private", nil)
	if err == nil {
		t.Fatal("github account should reject gitea token env")
	}
}

func TestProviderAccountMetadataMergePreservesExistingKeys(t *testing.T) {
	got := mergeMaps(cloneMap(map[string]any{"region": "us", "team": "platform"}), map[string]any{"team": "ops"})
	if got["region"] != "us" || got["team"] != "ops" {
		t.Fatalf("merged metadata = %#v", got)
	}
}

func TestProviderAccountRotationMetadataDoesNotLeakEnvNames(t *testing.T) {
	got := providerAccountRotationMetadata(
		map[string]any{"team": "platform", "token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OLD"},
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OLD",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEW",
		"quarterly rotation",
		&User{ID: "user-1"},
	)
	if got["team"] != "platform" {
		t.Fatalf("existing metadata should be preserved: %#v", got)
	}
	if _, ok := got["token_env"]; ok {
		t.Fatalf("token_env should be removed from metadata: %#v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "GITHUB_OLD") || strings.Contains(string(encoded), "GITHUB_NEW") {
		t.Fatalf("rotation metadata leaked env names: %s", encoded)
	}
	rotation := mapFromAny(got["token_rotation"])
	if rotation["previous_token_present"] != true || rotation["new_token_present"] != true || rotation["rotated_by"] != "user-1" {
		t.Fatalf("rotation metadata = %#v", rotation)
	}
}
