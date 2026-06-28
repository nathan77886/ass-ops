package app

import (
	"reflect"
	"testing"
)

func TestProviderReviewAttemptResponseDiagnosticSanitizers(t *testing.T) {
	for _, item := range []struct {
		input string
		want  string
	}{
		{"pending", "pending"},
		{"success", "success"},
		{"retryable", "retryable"},
		{"failed", "failed"},
		{"blocked", "blocked"},
		{"  success  ", "success"},
		{"ready", "blocked"},
		{"rate_limited", "blocked"},
		{"FAILED", "blocked"},
		{"", "blocked"},
		{"<script>alert(1)</script>", "blocked"},
	} {
		if got := safeProviderReviewAttemptResponseStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptResponseStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"2xx", "2xx"},
		{"4xx", "4xx"},
		{"5xx", "5xx"},
		{"unknown", "unknown"},
		{"  4xx  ", "4xx"},
		{"3xx", ""},
		{"secret-token", ""},
		{"", ""},
		{"<script>alert(1)</script>", ""},
	} {
		if got := safeProviderReviewStatusClass(item.input); got != item.want {
			t.Fatalf("safeProviderReviewStatusClass(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	classes := safeProviderReviewStatusClasses([]string{"5xx", "4xx", "5xx", "3xx", "secret-token", "unknown", "2xx"})
	if len(classes) != 4 || classes[0] != "5xx" || classes[1] != "4xx" || classes[2] != "unknown" || classes[3] != "2xx" {
		t.Fatalf("safeProviderReviewStatusClasses mixed = %#v", classes)
	}
	if got := safeProviderReviewStatusClasses(nil); len(got) != 0 {
		t.Fatalf("safeProviderReviewStatusClasses nil = %#v", got)
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"github.create_branch_ref", "github.create_branch_ref"},
		{"github.commit_files", "github.commit_files"},
		{"github.open_review", "github.open_review"},
		{"gitea.create_branch_ref", "gitea.create_branch_ref"},
		{"gitea.commit_files", "gitea.commit_files"},
		{"gitea.open_review", "gitea.open_review"},
		{"  github.open_review  ", "github.open_review"},
		{"github.secret", ""},
		{"secret-token", ""},
		{"<script>alert(1)</script>", ""},
		{"", ""},
	} {
		if got := safeProviderReviewEndpointKey(item.input); got != item.want {
			t.Fatalf("safeProviderReviewEndpointKey(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		endpoint string
		provider string
		adapter  string
	}{
		{"github.create_branch_ref", "github", "github_provider_review_adapter"},
		{"github.commit_files", "github", "github_provider_review_adapter"},
		{"github.open_review", "github", "github_provider_review_adapter"},
		{"gitea.create_branch_ref", "gitea", "gitea_provider_review_adapter"},
		{"gitea.commit_files", "gitea", "gitea_provider_review_adapter"},
		{"gitea.open_review", "gitea", "gitea_provider_review_adapter"},
		{"provider.open_review", "", ""},
		{"", "", ""},
	} {
		if got := providerReviewProviderFromEndpointKey(item.endpoint); got != item.provider {
			t.Fatalf("providerReviewProviderFromEndpointKey(%q) = %q, want %q", item.endpoint, got, item.provider)
		}
		if got := providerReviewAdapterKindForProvider(item.provider); got != item.adapter {
			t.Fatalf("providerReviewAdapterKindForProvider(%q) = %q, want %q", item.provider, got, item.adapter)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"github", "github"},
		{"gitea", "gitea"},
		{"GitHub", ""},
		{"raw_provider", ""},
		{"", ""},
	} {
		if got := safeProviderReviewProviderType(item.input); got != item.want {
			t.Fatalf("safeProviderReviewProviderType(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		operation string
		method    string
		shape     string
	}{
		{"create_branch_ref", "POST", "ref_from_target_branch"},
		{"commit_starter_files", "PUT", "content_redacted_file_batch"},
		{"open_review_request", "POST", "review_request"},
		{"raw_operation", "", ""},
	} {
		if got := providerReviewMethodForOperation(item.operation); got != item.method {
			t.Fatalf("providerReviewMethodForOperation(%q) = %q, want %q", item.operation, got, item.method)
		}
		if got := providerReviewPayloadShapeForOperation(item.operation); got != item.shape {
			t.Fatalf("providerReviewPayloadShapeForOperation(%q) = %q, want %q", item.operation, got, item.shape)
		}
	}
	for _, item := range []struct {
		operation         string
		endpointOperation string
		successClasses    []string
		retryClasses      []string
		failureClasses    []string
		unlockOperation   string
		unlockStatus      string
	}{
		{"create_branch_ref", "create_branch_ref", []string{"2xx"}, []string{"5xx"}, []string{"4xx"}, "commit_starter_files", "dependency_satisfied"},
		{"commit_starter_files", "commit_files", []string{"2xx"}, []string{"5xx"}, []string{"4xx"}, "open_review_request", "dependency_satisfied"},
		{"open_review_request", "open_review", []string{"2xx"}, []string{"5xx"}, []string{"4xx"}, "", ""},
		{"raw_operation", "", []string{}, []string{}, []string{}, "", ""},
	} {
		if got := providerReviewEndpointOperationForAttempt(item.operation); got != item.endpointOperation {
			t.Fatalf("providerReviewEndpointOperationForAttempt(%q) = %q, want %q", item.operation, got, item.endpointOperation)
		}
		if got := providerReviewExpectedSuccessClassesForOperation(item.operation); !reflect.DeepEqual(got, item.successClasses) {
			t.Fatalf("providerReviewExpectedSuccessClassesForOperation(%q) = %#v, want %#v", item.operation, got, item.successClasses)
		}
		if got := providerReviewExpectedRetryClassesForOperation(item.operation); !reflect.DeepEqual(got, item.retryClasses) {
			t.Fatalf("providerReviewExpectedRetryClassesForOperation(%q) = %#v, want %#v", item.operation, got, item.retryClasses)
		}
		if got := providerReviewTerminalFailureClassesForOperation(item.operation); !reflect.DeepEqual(got, item.failureClasses) {
			t.Fatalf("providerReviewTerminalFailureClassesForOperation(%q) = %#v, want %#v", item.operation, got, item.failureClasses)
		}
		if got := providerReviewAttemptDependencyUnlockOperation(item.operation); got != item.unlockOperation {
			t.Fatalf("providerReviewAttemptDependencyUnlockOperation(%q) = %q, want %q", item.operation, got, item.unlockOperation)
		}
		if got := providerReviewAttemptDependencyUnlockStatus(item.unlockOperation); got != item.unlockStatus {
			t.Fatalf("providerReviewAttemptDependencyUnlockStatus(%q) = %q, want %q", item.unlockOperation, got, item.unlockStatus)
		}
	}
	for _, item := range []struct {
		provider  string
		operation string
		want      string
	}{
		{"github", "create_branch_ref", "github_git_refs_path_template"},
		{"github", "commit_starter_files", "github_repository_contents_path_template"},
		{"github", "open_review_request", "github_pull_request_path_template"},
		{"gitea", "create_branch_ref", "gitea_git_refs_path_template"},
		{"gitea", "commit_starter_files", "gitea_repository_contents_path_template"},
		{"gitea", "open_review_request", "gitea_merge_request_path_template"},
		{"raw_provider", "create_branch_ref", ""},
		{"github", "raw_operation", ""},
	} {
		if got := providerReviewEndpointPathTemplateKeyForOperation(item.provider, item.operation); got != item.want {
			t.Fatalf("providerReviewEndpointPathTemplateKeyForOperation(%q, %q) = %q, want %q", item.provider, item.operation, got, item.want)
		}
	}
	for _, item := range []struct {
		provider string
		auth     string
		accept   string
	}{
		{"github", "bearer_token", "application/vnd.github+json"},
		{"GitHub", "bearer_token", "application/vnd.github+json"},
		{"gitea", "token", "application/json"},
		{"Gitea", "token", "application/json"},
		{"raw_provider", "", ""},
	} {
		if got := providerReviewAuthSchemeForProvider(item.provider); got != item.auth {
			t.Fatalf("providerReviewAuthSchemeForProvider(%q) = %q, want %q", item.provider, got, item.auth)
		}
		if got := providerReviewAcceptHeaderForProvider(item.provider); got != item.accept {
			t.Fatalf("providerReviewAcceptHeaderForProvider(%q) = %q, want %q", item.provider, got, item.accept)
		}
	}
	for _, item := range []struct {
		provider  string
		operation string
		endpoint  string
		auth      string
		accept    string
	}{
		{"github", "create_branch_ref", "github.create_branch_ref", "bearer_token", "application/vnd.github+json"},
		{"gitea", "create_branch_ref", "gitea.create_branch_ref", "token", "application/json"},
		{"gitea", "commit_starter_files", "gitea.commit_files", "token", "application/json"},
		{"gitea", "open_review_request", "gitea.open_review", "token", "application/json"},
	} {
		transportPlan := providerReviewAttemptAdapterTransportPlan(item.provider, item.operation)
		if transportPlan["mode"] != "redacted_attempt_adapter_transport_plan" ||
			transportPlan["transport_ready"] != true ||
			transportPlan["transport_ready_reason"] != "ready" ||
			transportPlan["provider_type"] != item.provider ||
			transportPlan["operation_name"] != item.operation ||
			transportPlan["endpoint_key"] != item.endpoint ||
			transportPlan["auth_scheme"] != item.auth ||
			transportPlan["accept_header"] != item.accept ||
			transportPlan["provider_api_call_made"] != false ||
			transportPlan["provider_api_mutation"] != "disabled" ||
			transportPlan["contains_token"] != false ||
			transportPlan["contains_provider_url"] != false {
			t.Fatalf("providerReviewAttemptAdapterTransportPlan(%q, %q) = %#v", item.provider, item.operation, transportPlan)
		}
	}
}
