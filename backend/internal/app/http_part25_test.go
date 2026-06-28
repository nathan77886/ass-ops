package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectVersionValidationPreviewAvoidsArgoMetadataSubstringFalsePositive(t *testing.T) {
	preview := projectVersionValidationPreview(
		map[string]any{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{"repo_key": "service", "remote_id": "remote-1", "argo_revision": "abc123"},
				map[string]any{"repo_key": "config", "remote_id": "remote-1"},
			}},
		},
		[]map[string]any{{"id": "remote-1", "latest_sha": "abc123"}},
		nil,
		nil,
		[]map[string]any{{"metadata": map[string]any{"message": "mentions abc123", "revision": "different"}}},
	)
	items := sliceOfMapsFromAny(preview["items"])
	if len(items) != 2 || items[0]["status"] != "partial" || items[1]["status"] != "partial" {
		t.Fatalf("validation items = %#v", items)
	}
	checks := sliceOfMapsFromAny(items[0]["checks"])
	if statusByName(checks, "argo_revision_observed") != "partial" {
		t.Fatalf("argo revision should only match structured revision fields: %#v", checks)
	}
	remoteOnlyChecks := sliceOfMapsFromAny(items[1]["checks"])
	if statusByName(remoteOnlyChecks, "version_refs_configured") != "partial" {
		t.Fatalf("remote-only item should remain partial: %#v", remoteOnlyChecks)
	}
	refreshPlan := mapFromAny(preview["provider_refresh_plan"])
	if refreshPlan["step_count"] != 1 || refreshPlan["blocked_count"] != 1 {
		t.Fatalf("empty-ref manifest items should not create refresh steps, only Argo item should: %#v", refreshPlan)
	}
}

func TestSSHMachineRehearsalPreviewSanitizesEvidence(t *testing.T) {
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
				"strict_host_key_checking":      "yes",
				"runbook_url":                   "https://runbooks.example.com/ssh/prod-api",
				"authorized_fixture_id":         "fixture-prod-api-1",
				"operator_approved_by":          "alice@example.com",
				"operator_approval_note":        "approved for production rehearsal",
				"live_rehearsal_environment":    "prod",
				"operator_environment_proof_id": "env-proof-1",
				"private_key_should_not_leak":   "SECRET",
			},
		},
		[]map[string]any{
			{
				"id":             "run-2",
				"status":         "completed",
				"exit_code":      0,
				"operation_type": "ssh.exec",
				"command":        "cat /etc/passwd",
				"stdout":         "secret output",
				"stderr":         "secret error",
			},
			{
				"id":             "run-1",
				"status":         "completed",
				"exit_code":      0,
				"operation_type": "ssh.verify",
				"command":        "true",
			},
		},
	)

	if preview["mode"] != "ssh_rehearsal_plan_preview" || preview["rehearsal_state"] != "ready" {
		t.Fatalf("preview state = %#v", preview)
	}
	for _, key := range []string{"execution_enabled", "external_call_made", "ssh_process_started", "command_executed", "stdout_included", "stderr_included", "private_key_included", "known_hosts_included", "secret_included"} {
		if preview[key] != false {
			t.Fatalf("%s = %#v, want false", key, preview[key])
		}
	}
	evidence := mapFromAny(preview["recent_evidence"])
	if evidence["completed_verify"] != true || evidence["completed_exec"] != true || intFromAny(evidence["verify_runs"], 0) != 1 || intFromAny(evidence["exec_runs"], 0) != 1 {
		t.Fatalf("unexpected evidence summary: %#v", evidence)
	}
	if evidence["evidence_state"] != "recorded" || evidence["has_live_evidence"] != true || evidence["sanitized_metadata_only"] != true {
		t.Fatalf("unexpected recorded evidence state: %#v", evidence)
	}
	resultPlan := mapFromAny(preview["result_recording_plan"])
	if resultPlan["recording_state"] != "recorded" ||
		resultPlan["recording_ready"] != true ||
		resultPlan["result_written"] != true ||
		resultPlan["auth_binding_recorded"] != true ||
		resultPlan["verify_result_recorded"] != true ||
		resultPlan["exec_result_recorded"] != true ||
		resultPlan["stdout_included"] != false ||
		resultPlan["stderr_included"] != false ||
		resultPlan["raw_error_included"] != false ||
		resultPlan["private_key_included"] != false {
		t.Fatalf("unexpected result recording plan for recorded evidence: %#v", resultPlan)
	}
	if preview["live_evidence_recorded"] != true || preview["sanitized_result_recorded"] != true {
		t.Fatalf("recorded evidence should set both top-level evidence flags: %#v", preview)
	}
	controlEvidence := mapFromAny(preview["live_rehearsal_control_evidence"])
	if controlEvidence["mode"] != "ssh_live_rehearsal_control_evidence" ||
		controlEvidence["control_state"] != "ready" ||
		controlEvidence["controls_ready"] != true ||
		controlEvidence["runbook_reference_recorded"] != true ||
		controlEvidence["fixture_reference_recorded"] != true ||
		controlEvidence["operator_approval_recorded"] != true ||
		controlEvidence["contains_runbook_body"] != false ||
		controlEvidence["contains_fixture_identifier"] != false ||
		controlEvidence["contains_operator_identity"] != false ||
		controlEvidence["contains_operator_note"] != false {
		t.Fatalf("unexpected live control evidence: %#v", controlEvidence)
	}
	if preview["live_rehearsal_controls_ready"] != true || preview["operator_approved_proof_recorded"] != true {
		t.Fatalf("preview should expose ready live controls as booleans only: %#v", preview)
	}
	environmentProofPlan := mapFromAny(preview["environment_proof_plan"])
	if preview["environment_proof_ready"] != true ||
		environmentProofPlan["mode"] != "ssh_rehearsal_environment_proof_plan" ||
		environmentProofPlan["environment_proof_state"] != "ready" ||
		environmentProofPlan["environment_proof_ready"] != true ||
		environmentProofPlan["target_environment_reference_recorded"] != true ||
		environmentProofPlan["operator_environment_proof_recorded"] != true ||
		environmentProofPlan["completed_verify_evidence"] != true ||
		environmentProofPlan["completed_exec_evidence"] != true ||
		environmentProofPlan["sanitized_result_recorded"] != true ||
		environmentProofPlan["environment_probe_performed"] != false ||
		environmentProofPlan["ssh_process_started"] != false ||
		environmentProofPlan["command_executed"] != false ||
		environmentProofPlan["environment_identifier_included"] != false ||
		environmentProofPlan["operator_identity_included"] != false ||
		environmentProofPlan["fixture_identifier_included"] != false ||
		environmentProofPlan["stdout_included"] != false ||
		environmentProofPlan["private_key_included"] != false {
		t.Fatalf("unexpected environment proof plan: %#v", environmentProofPlan)
	}
	attestationPlan := mapFromAny(preview["target_environment_attestation_plan"])
	if preview["target_environment_attestation_ready"] != true ||
		attestationPlan["mode"] != "ssh_target_environment_attestation_plan" ||
		attestationPlan["attestation_state"] != "ready_for_operator_review" ||
		attestationPlan["attestation_ready_for_review"] != true ||
		attestationPlan["runbook_reference_observed"] != true ||
		attestationPlan["authorized_machine_fixture_observed"] != true ||
		attestationPlan["operator_approval_proof_observed"] != true ||
		attestationPlan["target_environment_proof_observed"] != true ||
		attestationPlan["verify_result_observed"] != true ||
		attestationPlan["exec_result_observed"] != true ||
		attestationPlan["sanitized_result_recorded"] != true ||
		attestationPlan["environment_probe_performed"] != false ||
		attestationPlan["ssh_process_started"] != false ||
		attestationPlan["ssh_verify_executed"] != false ||
		attestationPlan["ssh_exec_executed"] != false ||
		attestationPlan["raw_output_recorded"] != false ||
		attestationPlan["operator_identity_recorded"] != false ||
		attestationPlan["key_material_included"] != false ||
		attestationPlan["external_call_made"] != false {
		t.Fatalf("unexpected target environment attestation plan: %#v", attestationPlan)
	}
	assertSSHRehearsalPlansSafe(t, preview)
	latestExec := mapFromAny(evidence["latest_exec"])
	if latestExec["command"] != nil || latestExec["stdout"] != nil || latestExec["stderr"] != nil {
		t.Fatalf("latest exec leaked sensitive fields: %#v", latestExec)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"/etc/assops/ssh/prod-api", "secret output", "secret error", "SECRET", "runbooks.example.com", "fixture-prod-api-1", "alice@example.com", "approved for production rehearsal", "env-proof-1"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("preview leaked %q: %s", forbidden, encoded)
		}
	}
	if statusByKind(sliceOfMapsFromAny(preview["steps"]), "verify_rehearsal") != "completed" ||
		statusByKind(sliceOfMapsFromAny(preview["steps"]), "exec_rehearsal") != "completed" ||
		statusByKind(sliceOfMapsFromAny(preview["steps"]), "live_rehearsal_controls") != "ready" {
		t.Fatalf("expected completed rehearsal steps: %#v", preview["steps"])
	}
}
