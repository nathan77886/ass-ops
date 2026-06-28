package app

import (
	"testing"
)

func TestProviderReviewAttemptAdapterActivationMetadataReadyReason(t *testing.T) {
	for _, tt := range []struct {
		name               string
		claimReady         bool
		executionLockReady bool
		credentialReady    bool
		runtimeReady       bool
		requestReady       bool
		transportReady     bool
		providerSendReady  bool
		responseReady      bool
		transactionReady   bool
		want               string
	}{
		{
			name: "claim not ready",
			want: "provider_review_activation_claim_metadata_not_ready",
		},
		{
			name:       "execution lock not ready",
			claimReady: true,
			want:       "provider_review_activation_execution_lock_not_ready",
		},
		{
			name:               "credential not ready",
			claimReady:         true,
			executionLockReady: true,
			want:               "provider_review_activation_credential_binding_not_ready",
		},
		{
			name:               "runtime not ready",
			claimReady:         true,
			executionLockReady: true,
			credentialReady:    true,
			want:               "provider_review_activation_adapter_runtime_not_ready",
		},
		{
			name:               "request not ready",
			claimReady:         true,
			executionLockReady: true,
			credentialReady:    true,
			runtimeReady:       true,
			want:               "provider_review_activation_request_materialization_not_ready",
		},
		{
			name:               "transport not ready",
			claimReady:         true,
			executionLockReady: true,
			credentialReady:    true,
			runtimeReady:       true,
			requestReady:       true,
			want:               "provider_review_activation_transport_not_ready",
		},
		{
			name:               "provider send not ready",
			claimReady:         true,
			executionLockReady: true,
			credentialReady:    true,
			runtimeReady:       true,
			requestReady:       true,
			transportReady:     true,
			want:               "provider_review_activation_provider_send_not_ready",
		},
		{
			name:               "response not ready",
			claimReady:         true,
			executionLockReady: true,
			credentialReady:    true,
			runtimeReady:       true,
			requestReady:       true,
			transportReady:     true,
			providerSendReady:  true,
			want:               "provider_review_activation_response_recording_not_ready",
		},
		{
			name:               "transaction not ready",
			claimReady:         true,
			executionLockReady: true,
			credentialReady:    true,
			runtimeReady:       true,
			requestReady:       true,
			transportReady:     true,
			providerSendReady:  true,
			responseReady:      true,
			want:               "provider_review_activation_transaction_not_ready",
		},
		{
			name:               "ready",
			claimReady:         true,
			executionLockReady: true,
			credentialReady:    true,
			runtimeReady:       true,
			requestReady:       true,
			transportReady:     true,
			providerSendReady:  true,
			responseReady:      true,
			transactionReady:   true,
			want:               "ready",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := providerReviewAttemptAdapterActivationMetadataReadyReason(
				tt.claimReady,
				tt.executionLockReady,
				tt.credentialReady,
				tt.runtimeReady,
				tt.requestReady,
				tt.transportReady,
				tt.providerSendReady,
				tt.responseReady,
				tt.transactionReady,
			)
			if got != tt.want {
				t.Fatalf("providerReviewAttemptAdapterActivationMetadataReadyReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
