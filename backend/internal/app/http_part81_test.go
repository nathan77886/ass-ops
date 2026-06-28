package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentExecutionReadinessGatesKeepMutationBlocked(t *testing.T) {
	gates := agentExecutionReadinessGates()
	if len(gates) != 5 {
		t.Fatalf("gates = %#v", gates)
	}
	for _, gate := range []string{"codex_cli_process", "repository_mutation", "pull_request_workflow"} {
		if statusByGate(gates, gate) != "blocked" {
			t.Fatalf("%s gate should be blocked: %#v", gate, gates)
		}
	}
	encoded, _ := json.Marshal(gates)
	for _, forbidden := range []string{"ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("readiness gates should not expose sensitive config hints: %s", encoded)
		}
	}
}

func TestAgentCodexCLIReadiness(t *testing.T) {
	tests := []struct {
		name           string
		runtime        map[string]any
		wantReadiness  string
		wantConfigured string
		wantVerified   string
		wantBinary     string
	}{
		{
			name:           "missing runtime blocks all execution",
			runtime:        map[string]any{},
			wantReadiness:  "blocked",
			wantConfigured: "blocked",
			wantVerified:   "blocked",
			wantBinary:     "blocked",
		},
		{
			name:           "config-only runtime is not configured",
			runtime:        map[string]any{"config": map[string]any{"token": "do-not-serialize"}},
			wantReadiness:  "blocked",
			wantConfigured: "blocked",
			wantVerified:   "blocked",
			wantBinary:     "blocked",
		},
		{
			name: "runtime with error status remains blocked",
			runtime: map[string]any{
				"id":           "runtime-2",
				"name":         "Broken Codex",
				"runtime_type": "codex-cli",
				"codex_binary": "codex",
				"model":        "gpt-5-codex",
				"status":       "error",
			},
			wantReadiness:  "blocked",
			wantConfigured: "ready",
			wantVerified:   "blocked",
			wantBinary:     "ready",
		},
		{
			name: "verified runtime is metadata ready but still cannot spawn processes",
			runtime: map[string]any{
				"id":           "runtime-1",
				"name":         "Demo Codex",
				"runtime_type": "codex-cli",
				"codex_binary": "codex",
				"model":        "gpt-5-codex",
				"status":       "verified",
				"config":       map[string]any{"token": "do-not-serialize"},
			},
			wantReadiness:  "metadata_ready",
			wantConfigured: "ready",
			wantVerified:   "ready",
			wantBinary:     "ready",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentCodexCLIReadiness(tt.runtime)
			if got["readiness"] != tt.wantReadiness {
				t.Fatalf("readiness = %v, want %s: %#v", got["readiness"], tt.wantReadiness, got)
			}
			if got["execution_enabled"] != false ||
				got["process_spawn_enabled"] != false ||
				got["external_call_made"] != false ||
				got["repository_mutation_allowed"] != false ||
				got["pull_request_creation"] != false {
				t.Fatalf("Codex CLI readiness should keep external execution disabled: %#v", got)
			}
			gates := sliceOfMapsFromAny(got["gates"])
			if statusByGate(gates, "runtime_configured") != tt.wantConfigured ||
				statusByGate(gates, "runtime_verified") != tt.wantVerified ||
				statusByGate(gates, "codex_binary_configured") != tt.wantBinary {
				t.Fatalf("runtime metadata gates mismatch: %#v", gates)
			}
			for _, gate := range []string{"codex_cli_process", "repository_mutation", "pull_request_workflow"} {
				if statusByGate(gates, gate) != "blocked" {
					t.Fatalf("%s should stay blocked: %#v", gate, gates)
				}
			}
			encoded, _ := json.Marshal(got)
			for _, forbidden := range []string{"do-not-serialize", "ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("Codex CLI readiness should not expose sensitive config hints: %s", encoded)
				}
			}
		})
	}
}

func TestAgentPatchWorkflowGuardrail(t *testing.T) {
	got := agentPatchWorkflowGuardrail()
	if got["execution_mode"] != "simulation_only" || got["mutation_enabled"] != false {
		t.Fatalf("guardrail mode = %#v", got)
	}
	required := stringSliceFromAny(got["required_approvals"])
	if !containsString(required, "agent.execute") || !containsString(required, "future.patch.apply") {
		t.Fatalf("guardrail required approvals = %#v", required)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("guardrail should not include sensitive config hints: %s", encoded)
		}
	}
	if statusByGate(sliceOfMapsFromAny(got["execution_readiness"]), "repository_mutation") != "blocked" {
		t.Fatalf("execution_readiness should keep repository mutation blocked: %#v", got["execution_readiness"])
	}
	assertAgentCodeModificationPlanSafe(t, mapFromAny(got["code_modification_plan"]))
}
