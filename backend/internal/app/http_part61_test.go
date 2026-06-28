package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptInvocationSnapshotPayloadRejectsMismatchedPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	invocationPlan["operation_name"] = "commit_starter_files"
	invocationPlan["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptInvocationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptInvocationSnapshotReadiness(snapshot)
	if ready ||
		state != "invocation_blocked" ||
		snapshot["invocation_plan_observed"] != true ||
		snapshot["invocation_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_invocation_contract_not_ready") {
		t.Fatalf("invocation snapshot with mismatched plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("invocation snapshot with mismatched plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptInvocationSnapshotPayloadRejectsProviderRequestSent(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	invocationPlan["provider_request_sent"] = true

	snapshot := providerReviewAttemptInvocationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptInvocationSnapshotReadiness(snapshot)
	if ready ||
		state != "invocation_contract_ready" ||
		snapshot["invocation_contract_ready"] != true ||
		snapshot["provider_request_sent"] != true ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_invocation_not_no_call") {
		t.Fatalf("invocation snapshot with provider request sent = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("invocation snapshot with provider request sent leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptInvocationSnapshotPayloadRejectsContainsToken(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	invocationPlan["contains_token"] = true
	invocationPlan["invocation_ready_reason"] = "secret-token"

	snapshot := providerReviewAttemptInvocationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptInvocationSnapshotReadiness(snapshot)
	if ready ||
		state != "invocation_contract_ready" ||
		snapshot["invocation_contract_ready"] != true ||
		snapshot["contains_token"] != true ||
		snapshot["invocation_ready_reason"] != "" ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != false ||
		!containsString(missing, "provider_review_invocation_not_no_call") {
		t.Fatalf("invocation snapshot with token marker = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("invocation snapshot with token marker leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptBranchPolicySnapshotPayloadRequiresBranchPolicyPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "branch_policy_plan")

	snapshot := providerReviewAttemptBranchPolicySnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptBranchPolicySnapshotReadiness(snapshot)
	if ready ||
		state != "branch_policy_blocked" ||
		snapshot["branch_policy_plan_observed"] != false ||
		snapshot["branch_policy_contract_ready"] != false ||
		snapshot["branch_policy_metadata_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_branch_policy_plan_missing") {
		t.Fatalf("branch policy snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "main", "file content", "Authorization"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("branch policy snapshot without plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptBranchPolicySnapshotPayloadRejectsMismatchedBranchPolicyContract(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	branchPolicyPlan := mapFromAny(dispatchPlan["branch_policy_plan"])
	branchPolicyPlan["operation_name"] = "commit_starter_files"
	branchPolicyPlan["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptBranchPolicySnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptBranchPolicySnapshotReadiness(snapshot)
	if ready ||
		state != "branch_policy_blocked" ||
		snapshot["branch_policy_plan_observed"] != true ||
		snapshot["branch_policy_contract_ready"] != false ||
		snapshot["branch_policy_metadata_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != true ||
		!containsString(missing, "provider_review_branch_policy_contract_not_ready") ||
		!containsString(missing, "provider_review_branch_policy_metadata_not_ready") {
		t.Fatalf("branch policy snapshot with mismatched contract = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "main", "file content", "Authorization"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("branch policy snapshot with mismatched contract leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptExecutionLockSnapshotPayloadRequiresExecutionLockPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	delete(invocationPlan, "execution_lock_plan")

	snapshot := providerReviewAttemptExecutionLockSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptExecutionLockSnapshotReadiness(snapshot)
	if ready ||
		state != "execution_lock_blocked" ||
		snapshot["execution_lock_plan_observed"] != false ||
		snapshot["execution_lock_contract_ready"] != false ||
		snapshot["execution_lock_metadata_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_execution_lock_plan_missing") {
		t.Fatalf("execution lock snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "idempotency_key_material", "lock_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("execution lock snapshot without plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptExecutionLockSnapshotPayloadRejectsMismatchedExecutionLockContract(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	executionLockPlan := mapFromAny(invocationPlan["execution_lock_plan"])
	executionLockPlan["operation_name"] = "commit_starter_files"
	executionLockPlan["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptExecutionLockSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptExecutionLockSnapshotReadiness(snapshot)
	if ready ||
		state != "execution_lock_blocked" ||
		snapshot["execution_lock_plan_observed"] != true ||
		snapshot["execution_lock_contract_ready"] != false ||
		snapshot["execution_lock_metadata_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != true ||
		!containsString(missing, "provider_review_execution_lock_contract_not_ready") ||
		!containsString(missing, "provider_review_execution_lock_metadata_not_ready") {
		t.Fatalf("execution lock snapshot with mismatched contract = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "idempotency_key_material", "lock_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("execution lock snapshot with mismatched contract leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptTransportSnapshotPayloadRequiresTransportPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "transport_plan")

	snapshot := providerReviewAttemptTransportSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptTransportSnapshotReadiness(snapshot)
	if ready ||
		state != "transport_blocked" ||
		snapshot["transport_plan_observed"] != false ||
		snapshot["transport_metadata_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_transport_plan_missing") {
		t.Fatalf("transport snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
}

func TestProviderReviewAttemptTransportSnapshotPayloadRejectsMaterializedOrSensitiveMarkers(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	transportPlan := mapFromAny(dispatchPlan["transport_plan"])
	transportPlan["provider_request_sent"] = true
	transportPlan["authorization_header_included"] = true
	transportPlan["contains_token"] = true
	transportPlan["transport_ready_reason"] = "secret-token"

	snapshot := providerReviewAttemptTransportSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptTransportSnapshotReadiness(snapshot)
	if ready ||
		state != "transport_blocked" ||
		snapshot["provider_request_sent"] != true ||
		snapshot["authorization_header_included"] != true ||
		snapshot["contains_token"] != true ||
		snapshot["no_call_observed"] != false ||
		snapshot["transport_ready_reason"] != "" ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != false ||
		!containsString(missing, "provider_review_transport_not_no_call") {
		t.Fatalf("transport snapshot with materialized marker = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("transport snapshot with sensitive marker leaked %q: %s", leak, encoded)
		}
	}
}
