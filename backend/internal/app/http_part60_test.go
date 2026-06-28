package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptRequestMaterializationSnapshotPayloadRequiresPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "request_materialization_plan")

	snapshot := providerReviewAttemptRequestMaterializationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRequestMaterializationSnapshotReadiness(snapshot)
	if ready ||
		state != "request_materialization_blocked" ||
		snapshot["request_materialization_plan_observed"] != false ||
		snapshot["request_materialization_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_request_materialization_plan_missing") {
		t.Fatalf("request-materialization snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request-materialization snapshot without plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptRequestMaterializationSnapshotPayloadRejectsMismatchedPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	requestPlan := mapFromAny(dispatchPlan["request_materialization_plan"])
	requestPlan["operation_name"] = "commit_starter_files"
	requestPlan["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptRequestMaterializationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRequestMaterializationSnapshotReadiness(snapshot)
	if ready ||
		state != "request_materialization_blocked" ||
		snapshot["request_materialization_plan_observed"] != true ||
		snapshot["request_materialization_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_request_materialization_contract_not_ready") {
		t.Fatalf("request-materialization snapshot with mismatched plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request-materialization snapshot with mismatched plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptRequestMaterializationSnapshotPayloadRejectsIncludedRequestBody(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	requestPlan := mapFromAny(dispatchPlan["request_materialization_plan"])
	requestPlan["request_body_included"] = true

	snapshot := providerReviewAttemptRequestMaterializationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRequestMaterializationSnapshotReadiness(snapshot)
	if ready ||
		state != "request_materialization_contract_ready" ||
		snapshot["request_materialization_plan_observed"] != true ||
		snapshot["request_materialization_contract_ready"] != true ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_request_materialization_not_no_call") {
		t.Fatalf("request-materialization snapshot with included request body = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request-materialization snapshot with included request body leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptRuntimeSnapshotPayloadRequiresRuntimePlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "adapter_runtime_plan")

	snapshot := providerReviewAttemptRuntimeSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRuntimeSnapshotReadiness(snapshot)
	if ready ||
		state != "runtime_blocked" ||
		snapshot["adapter_runtime_plan_observed"] != false ||
		snapshot["runtime_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_runtime_plan_missing") {
		t.Fatalf("runtime snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("runtime snapshot without plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptRuntimeSnapshotPayloadRejectsMismatchedRuntimeContract(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	runtimePlan := mapFromAny(dispatchPlan["adapter_runtime_plan"])
	runtimePlan["operation_name"] = "commit_starter_files"
	runtimePlan["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptRuntimeSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRuntimeSnapshotReadiness(snapshot)
	if ready ||
		state != "runtime_blocked" ||
		snapshot["adapter_runtime_plan_observed"] != true ||
		snapshot["runtime_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != true ||
		!containsString(missing, "provider_review_runtime_contract_not_ready") {
		t.Fatalf("runtime snapshot with mismatched contract = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("runtime snapshot with mismatched contract leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptAdapterRehearsalSnapshotPayloadRequiresRehearsalPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	delete(candidate, "dispatch_plan")

	snapshot := providerReviewAttemptAdapterRehearsalSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptAdapterRehearsalSnapshotReadiness(snapshot)
	if ready ||
		state != "adapter_rehearsal_blocked" ||
		snapshot["adapter_rehearsal_observed"] != false ||
		snapshot["adapter_rehearsal_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_adapter_rehearsal_missing") {
		t.Fatalf("adapter rehearsal snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("adapter rehearsal snapshot without plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptAdapterRehearsalSnapshotPayloadRejectsMismatchedCandidate(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	candidate["next_operation"] = "commit_starter_files"
	candidate["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptAdapterRehearsalSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptAdapterRehearsalSnapshotReadiness(snapshot)
	if ready ||
		state != "adapter_rehearsal_contract_ready" ||
		snapshot["candidate_matches_attempt"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_attempt_not_current_candidate") {
		t.Fatalf("adapter rehearsal snapshot with mismatched candidate = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("adapter rehearsal snapshot with mismatched candidate leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptAdapterBlueprintSnapshotPayloadRequiresInvocationPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "invocation_plan")

	snapshot := providerReviewAttemptAdapterBlueprintSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptAdapterBlueprintSnapshotReadiness(snapshot)
	if ready ||
		state != "adapter_blueprint_blocked" ||
		snapshot["invocation_plan_observed"] != false ||
		snapshot["invocation_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_invocation_plan_missing") {
		t.Fatalf("adapter blueprint snapshot without invocation = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("adapter blueprint snapshot without invocation leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptAdapterBlueprintSnapshotPayloadRejectsMismatchedCandidate(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	candidate["next_operation"] = "commit_starter_files"
	candidate["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptAdapterBlueprintSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptAdapterBlueprintSnapshotReadiness(snapshot)
	if ready ||
		state != "adapter_blueprint_contract_ready" ||
		snapshot["candidate_matches_attempt"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_attempt_not_current_candidate") {
		t.Fatalf("adapter blueprint snapshot with mismatched candidate = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("adapter blueprint snapshot with mismatched candidate leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptInvocationSnapshotPayloadRequiresInvocationPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "invocation_plan")

	snapshot := providerReviewAttemptInvocationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptInvocationSnapshotReadiness(snapshot)
	if ready ||
		state != "invocation_blocked" ||
		snapshot["invocation_plan_observed"] != false ||
		snapshot["invocation_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_invocation_plan_missing") {
		t.Fatalf("invocation snapshot without invocation plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("invocation snapshot without invocation plan leaked %q: %s", leak, encoded)
		}
	}
}
