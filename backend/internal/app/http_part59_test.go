package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProviderReviewAttemptCredentialSnapshotPayloadRequiresCredentialPlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "credential_binding_plan")

	snapshot := providerReviewAttemptCredentialSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptCredentialSnapshotReadiness(snapshot)
	if ready ||
		state != "credential_blocked" ||
		snapshot["credential_binding_plan_observed"] != false ||
		snapshot["credential_binding_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_credential_binding_plan_missing") {
		t.Fatalf("credential snapshot without plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "ASSOPS_TEMPLATE_PROVIDER_TOKEN", "Authorization", "feature/secret", "file content"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("credential snapshot without plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptCredentialSnapshotPayloadRejectsMismatchedCredentialContract(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	credentialPlan := mapFromAny(dispatchPlan["credential_binding_plan"])
	credentialPlan["operation_name"] = "commit_starter_files"
	credentialPlan["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptCredentialSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptCredentialSnapshotReadiness(snapshot)
	if ready ||
		state != "credential_blocked" ||
		snapshot["credential_binding_plan_observed"] != true ||
		snapshot["credential_binding_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != true ||
		!containsString(missing, "provider_review_credential_binding_contract_not_ready") {
		t.Fatalf("credential snapshot with mismatched contract = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "ASSOPS_TEMPLATE_PROVIDER_TOKEN", "Authorization", "feature/secret", "file content"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("credential snapshot with mismatched contract leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptRequestEnvelopeSnapshotPayloadRequiresEnvelopePlan(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "request_envelope_plan")

	snapshot := providerReviewAttemptRequestEnvelopeSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRequestEnvelopeSnapshotReadiness(snapshot)
	if ready ||
		state != "request_envelope_blocked" ||
		snapshot["request_envelope_plan_observed"] != false ||
		snapshot["request_envelope_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_request_envelope_plan_missing") {
		t.Fatalf("request envelope snapshot without envelope plan = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request envelope snapshot without envelope plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptRequestEnvelopeSnapshotPayloadRejectsMismatchedEnvelopeContract(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	requestEnvelopePlan := mapFromAny(dispatchPlan["request_envelope_plan"])
	requestEnvelopePlan["operation_name"] = "commit_starter_files"
	requestEnvelopePlan["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptRequestEnvelopeSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRequestEnvelopeSnapshotReadiness(snapshot)
	if ready ||
		state != "request_envelope_blocked" ||
		snapshot["request_envelope_plan_observed"] != true ||
		snapshot["request_envelope_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != true ||
		!containsString(missing, "provider_review_request_envelope_contract_not_ready") {
		t.Fatalf("request envelope snapshot with mismatched contract = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request envelope snapshot with mismatched contract leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptIdempotencySnapshotPayloadRequiresRequestSummary(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	delete(attempt, "request_summary")

	snapshot := providerReviewAttemptIdempotencySnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptIdempotencySnapshotReadiness(snapshot)
	if ready ||
		state != "idempotency_blocked" ||
		snapshot["request_summary_observed"] != false ||
		snapshot["idempotency_metadata_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_request_summary_missing") {
		t.Fatalf("idempotency snapshot without request summary = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
}

func TestProviderReviewAttemptIdempotencySnapshotPayloadRejectsClaimedOrSensitiveMarkers(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	requestSummary := mapFromAny(attempt["request_summary"])
	requestSummary["idempotency_key_included"] = true
	requestSummary["contains_token"] = true
	requestSummary["idempotency_key_hash"] = "idempotency_key_hash"
	requestSummary["idempotency_key_material"] = "raw_key_material"
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	claimPlan := mapFromAny(candidate["claim_plan"])
	claimPlan["idempotency_claim_recorded"] = true

	snapshot := providerReviewAttemptIdempotencySnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptIdempotencySnapshotReadiness(snapshot)
	if ready ||
		state != "idempotency_blocked" ||
		snapshot["idempotency_key_included"] != true ||
		snapshot["idempotency_claim_recorded"] != true ||
		snapshot["contains_token"] != true ||
		snapshot["no_call_observed"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != false ||
		!containsString(missing, "provider_review_idempotency_not_no_call") {
		t.Fatalf("idempotency snapshot with sensitive marker = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "idempotency_key_hash", "raw_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("idempotency snapshot with sensitive marker leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptLiveExecutionGuardSnapshotRejectsMutationMarkers(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("running", "independent")
	attempt["claimed_at"] = time.Now()
	snapshot := providerReviewAttemptLiveExecutionGuardSnapshotPayload(attempt, true, true, true)
	snapshot["provider_api_call_made"] = true
	snapshot["mutation_armed"] = true
	snapshot["contains_token"] = true

	ready, state, missing := providerReviewAttemptLiveExecutionGuardSnapshotReadiness(snapshot)
	if ready ||
		state != "live_execution_guard_blocked" ||
		snapshot["status_snapshot_write_eligible"] != true ||
		!containsString(missing, "provider_review_live_execution_guard_not_no_call") {
		t.Fatalf("live execution guard should reject mutation markers: snapshot %#v ready %v state %s missing %#v", snapshot, ready, state, missing)
	}
}

func TestProviderReviewAttemptLiveExecutionLaunchPlanRejectsNilStore(t *testing.T) {
	_, err := ProviderReviewAttemptLiveExecutionLaunchPlan(context.Background(), nil, ProviderReviewAttemptLiveExecutionLaunchPlanOptions{
		AttemptID: "attempt-1",
	})
	if err == nil || !strings.Contains(err.Error(), "store is required") {
		t.Fatalf("ProviderReviewAttemptLiveExecutionLaunchPlan nil store error = %v, want store is required", err)
	}
}

func TestProviderReviewAttemptLiveExecutionReadinessSnapshotRejectsMutationMarkers(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	snapshot := providerReviewAttemptLiveExecutionReadinessSnapshotPayload(attempt, ledger, true, providerReviewAttemptAllRequiredLiveExecutionStatusesForTest())
	snapshot["provider_api_call_made"] = true
	snapshot["mutation_armed"] = true

	ready, state, missing := providerReviewAttemptLiveExecutionReadinessSnapshotReadiness(snapshot)
	if ready ||
		state != "live_execution_review_blocked" ||
		!containsString(missing, "provider_review_live_execution_not_no_call") {
		t.Fatalf("live execution readiness snapshot with mutation markers = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
}

func TestProviderReviewAttemptRequestValidationSnapshotPayloadRequiresPreflight(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	delete(dispatchPlan, "request_validation_preflight")

	snapshot := providerReviewAttemptRequestValidationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRequestValidationSnapshotReadiness(snapshot)
	if ready ||
		state != "request_validation_blocked" ||
		snapshot["request_validation_preflight_observed"] != false ||
		snapshot["request_validation_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_request_validation_preflight_missing") {
		t.Fatalf("request-validation snapshot without preflight = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request-validation snapshot without preflight leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptRequestValidationSnapshotPayloadRejectsMismatchedPreflight(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	preflight := mapFromAny(dispatchPlan["request_validation_preflight"])
	preflight["operation_name"] = "commit_starter_files"
	preflight["endpoint_key"] = "github.commit_files"

	snapshot := providerReviewAttemptRequestValidationSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptRequestValidationSnapshotReadiness(snapshot)
	if ready ||
		state != "request_validation_blocked" ||
		snapshot["request_validation_preflight_observed"] != true ||
		snapshot["request_validation_contract_ready"] != false ||
		snapshot["status_snapshot_write_eligible"] != false ||
		!containsString(missing, "provider_review_request_validation_contract_not_ready") {
		t.Fatalf("request-validation snapshot with mismatched preflight = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request-validation snapshot with mismatched preflight leaked %q: %s", leak, encoded)
		}
	}
}
