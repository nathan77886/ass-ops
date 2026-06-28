package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptRetryBackoffSnapshotPayloadRequiresRetryBackoffPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	providerSendPlan := mapFromAny(invocationPlan["provider_send_plan"])
	delete(providerSendPlan, "retry_backoff_plan")

	snapshot := providerReviewAttemptRetryBackoffSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRetryBackoffSnapshotReadiness(snapshot)
	if ready ||
		state != "retry_backoff_blocked" ||
		snapshot["retry_backoff_plan_observed"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_retry_backoff_plan_missing") {
		t.Fatalf("retry/backoff snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
}

func TestProviderReviewAttemptRetryBackoffSnapshotPayloadRejectsScheduledOrSensitiveMarkers(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	providerSendPlan := mapFromAny(invocationPlan["provider_send_plan"])
	retryBackoffPlan := mapFromAny(providerSendPlan["retry_backoff_plan"])
	retryBackoffPlan["retry_scheduled"] = true
	retryBackoffPlan["retry_after_value_recorded"] = true
	retryBackoffPlan["provider_error_code_included"] = true
	retryBackoffPlan["contains_token"] = true
	retryBackoffPlan["retry_backoff_ready_reason"] = "secret-token"

	snapshot := providerReviewAttemptRetryBackoffSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRetryBackoffSnapshotReadiness(snapshot)
	if ready ||
		state != "retry_backoff_blocked" ||
		snapshot["retry_scheduled"] != true ||
		snapshot["retry_after_value_recorded"] != true ||
		snapshot["provider_error_code_included"] != true ||
		snapshot["contains_token"] != true ||
		snapshot["no_call_observed"] != false ||
		snapshot["retry_backoff_ready_reason"] != "" ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != false ||
		!containsString(missing, "provider_review_retry_backoff_not_no_call") {
		t.Fatalf("retry/backoff snapshot with scheduled marker = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "Retry-After", "rate-limit-remaining"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("retry/backoff snapshot with sensitive marker leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptProviderCallBoundarySnapshotPayloadRequiresBoundaryPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	delete(transactionPlan, "provider_call_boundary_plan")

	snapshot := providerReviewAttemptProviderCallBoundarySnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptProviderCallBoundarySnapshotReadiness(snapshot)
	if ready ||
		state != "provider_call_boundary_blocked" ||
		snapshot["provider_call_boundary_plan_observed"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_provider_call_boundary_plan_missing") {
		t.Fatalf("provider-call-boundary snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
}

func TestProviderReviewAttemptProviderCallBoundarySnapshotPayloadRejectsProviderCallRecorded(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	boundaryPlan := mapFromAny(transactionPlan["provider_call_boundary_plan"])
	boundaryPlan["provider_call_boundary_recorded"] = true
	boundaryPlan["provider_request_id_recorded"] = true
	boundaryPlan["provider_response_body_recorded"] = true
	boundaryPlan["contains_token"] = true
	boundaryPlan["provider_call_boundary_ready_reason"] = "secret-token"

	snapshot := providerReviewAttemptProviderCallBoundarySnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptProviderCallBoundarySnapshotReadiness(snapshot)
	if ready ||
		state != "provider_call_boundary_blocked" ||
		snapshot["provider_call_boundary_recorded"] != true ||
		snapshot["provider_request_id_recorded"] != true ||
		snapshot["provider_response_body_recorded"] != true ||
		snapshot["contains_token"] != true ||
		snapshot["no_call_observed"] != false ||
		snapshot["provider_call_boundary_ready_reason"] != "" ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != false ||
		!containsString(missing, "provider_review_provider_call_boundary_not_no_call") {
		t.Fatalf("provider-call-boundary snapshot with recorded provider call = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider-call-boundary snapshot with provider call marker leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptLiveAdapterContractSnapshotPayloadRequiresContractPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	activationPlan := mapFromAny(invocationPlan["adapter_activation_plan"])
	liveAdapterPlan := mapFromAny(activationPlan["live_adapter_plan"])
	delete(liveAdapterPlan, "contract_plan")

	snapshot := providerReviewAttemptLiveAdapterContractSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptLiveAdapterContractSnapshotReadiness(snapshot)
	if ready ||
		state != "live_adapter_contract_blocked" ||
		snapshot["live_adapter_contract_plan_observed"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_live_adapter_contract_plan_missing") {
		t.Fatalf("live-adapter contract snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
}

func TestProviderReviewAttemptLiveAdapterContractSnapshotPayloadRequiresCapabilitiesAndRegistration(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(map[string]any)
		wantMissing string
	}{
		{
			name: "capabilities",
			mutate: func(contractPlan map[string]any) {
				contractPlan["required_capabilities"] = []string{}
			},
			wantMissing: "provider_review_live_adapter_contract_capabilities_missing",
		},
		{
			name: "registration",
			mutate: func(contractPlan map[string]any) {
				contractPlan["contract_registered"] = false
			},
			wantMissing: "provider_review_live_adapter_contract_not_registered",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
			ledger := providerReviewActivationSnapshotLedger(attempt)
			orchestration := mapFromAny(ledger["orchestration"])
			candidate := mapFromAny(orchestration["execution_candidate"])
			dispatchPlan := mapFromAny(candidate["dispatch_plan"])
			invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
			activationPlan := mapFromAny(invocationPlan["adapter_activation_plan"])
			liveAdapterPlan := mapFromAny(activationPlan["live_adapter_plan"])
			contractPlan := mapFromAny(liveAdapterPlan["contract_plan"])
			tt.mutate(contractPlan)

			snapshot := providerReviewAttemptLiveAdapterContractSnapshotPayload(attempt, ledger, true)
			ready, state, missing := providerReviewAttemptLiveAdapterContractSnapshotReadiness(snapshot)
			if ready ||
				state != "live_adapter_contract_blocked" ||
				snapshot["status_snapshot_write_eligible"] != false ||
				!containsString(missing, tt.wantMissing) {
				t.Fatalf("live-adapter contract snapshot missing %s = snapshot %#v, ready %v, state %s, missing %#v", tt.wantMissing, snapshot, ready, state, missing)
			}
		})
	}
}

func TestProviderReviewAttemptLiveAdapterContractSnapshotPayloadRejectsMaterializedOrSensitiveMarkers(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	activationPlan := mapFromAny(invocationPlan["adapter_activation_plan"])
	liveAdapterPlan := mapFromAny(activationPlan["live_adapter_plan"])
	contractPlan := mapFromAny(liveAdapterPlan["contract_plan"])
	contractPlan["request_contract_materialized"] = true
	contractPlan["response_contract_materialized"] = true
	contractPlan["provider_request_sent"] = true
	contractPlan["contains_token"] = true
	contractPlan["provider_url_included"] = true
	contractPlan["contract_ready_reason"] = "secret-token"

	snapshot := providerReviewAttemptLiveAdapterContractSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptLiveAdapterContractSnapshotReadiness(snapshot)
	if ready ||
		state != "live_adapter_contract_blocked" ||
		snapshot["request_contract_materialized"] != true ||
		snapshot["response_contract_materialized"] != true ||
		snapshot["provider_request_sent"] != true ||
		snapshot["contains_token"] != true ||
		snapshot["provider_url_included"] != true ||
		snapshot["no_call_observed"] != false ||
		snapshot["contract_ready_reason"] != "" ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != false ||
		!containsString(missing, "provider_review_live_adapter_contract_not_no_call") {
		t.Fatalf("live-adapter contract snapshot with materialized marker = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("live-adapter contract snapshot with sensitive marker leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptTransactionSnapshotPayloadRequiresTransactionPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "transaction_plan")

	snapshot := providerReviewAttemptTransactionSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptTransactionSnapshotReadiness(snapshot)
	if ready ||
		state != "transaction_blocked" ||
		snapshot["transaction_plan_observed"] != false ||
		snapshot["provider_call_boundary_plan_observed"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_transaction_plan_missing") ||
		!containsString(missing, "provider_review_provider_call_boundary_plan_missing") {
		t.Fatalf("transaction snapshot without transaction plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
}
