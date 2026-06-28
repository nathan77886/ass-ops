package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const reviewBranchExecutorMaxFiles = 100

const reviewBranchExecutorMaxFileBytes = 1024 * 1024

type reviewBranchExecutor struct {
	HTTPClient *http.Client
}

type reviewBranchExecutionInput struct {
	ProviderType string
	APIBase      string
	Owner        string
	Repository   string
	BaseBranch   string
	ReviewBranch string
	Files        map[string]string
	Title        string
	Body         string
	TokenEnv     string
}

type reviewBranchExecutionResult struct {
	ProviderType          string
	Owner                 string
	Repository            string
	BaseBranch            string
	ReviewBranch          string
	BranchRef             string
	BaseSHA               string
	FileCount             int
	ReviewURL             string
	ExecutionPhase        string
	ProviderStatusClass   string
	Retryable             bool
	ManualCleanupHint     string
	ExternalCallMade      bool
	ProviderAPIMutation   bool
	CleanupAttempted      bool
	CleanupSucceeded      bool
	CleanupRequired       bool
	TokenConfigured       bool
	TokenIncluded         bool
	RequestBodiesIncluded bool
	ResponseBodyIncluded  bool
}

func (e reviewBranchExecutor) Execute(ctx context.Context, input reviewBranchExecutionInput) (reviewBranchExecutionResult, error) {
	normalized, token, err := normalizeReviewBranchExecutionInput(input)
	result := reviewBranchExecutionResult{
		ProviderType:          normalized.ProviderType,
		Owner:                 normalized.Owner,
		Repository:            normalized.Repository,
		BaseBranch:            normalized.BaseBranch,
		ReviewBranch:          normalized.ReviewBranch,
		BranchRef:             "refs/heads/" + normalized.ReviewBranch,
		FileCount:             len(normalized.Files),
		ExecutionPhase:        "input_validation",
		ExternalCallMade:      false,
		ProviderAPIMutation:   false,
		TokenConfigured:       token != "",
		TokenIncluded:         false,
		RequestBodiesIncluded: false,
		ResponseBodyIncluded:  false,
	}
	if err != nil {
		return result, err
	}
	if token == "" {
		return result, fmt.Errorf("provider token environment is not configured")
	}
	if err := validateTemplateProviderURL(ctx, normalized.APIBase); err != nil {
		return result, fmt.Errorf("unsafe provider API URL: %w", err)
	}
	client := e.HTTPClient
	if client == nil {
		client = newTemplateProviderHTTPClient()
	}
	result.ExecutionPhase = "read_base_ref"
	baseSHA, err := e.githubBaseBranchSHA(ctx, client, normalized, token)
	result.ExternalCallMade = true
	if err != nil {
		result.ProviderStatusClass = providerStatusClassFromError(err)
		result.Retryable = reviewBranchExecutionRetryable(result.ProviderStatusClass, result.ExecutionPhase)
		return result, err
	}
	result.BaseSHA = baseSHA
	result.ExecutionPhase = "create_review_branch"
	if err := e.githubCreateBranchRef(ctx, client, normalized, token, baseSHA); err != nil {
		result.ProviderStatusClass = providerStatusClassFromError(err)
		result.Retryable = reviewBranchExecutionRetryable(result.ProviderStatusClass, result.ExecutionPhase)
		return result, err
	}
	result.ProviderAPIMutation = true
	result.ExecutionPhase = "commit_starter_files"
	for _, path := range sortedReviewBranchFilePaths(normalized.Files) {
		if err := e.githubPutFile(ctx, client, normalized, token, path, normalized.Files[path]); err != nil {
			result.ProviderStatusClass = providerStatusClassFromError(err)
			result = e.cleanupGitHubReviewBranch(ctx, client, normalized, token, result)
			result.Retryable = reviewBranchExecutionRetryable(result.ProviderStatusClass, result.ExecutionPhase) && !result.CleanupRequired
			return result, err
		}
	}
	result.ExecutionPhase = "open_review_request"
	reviewURL, err := e.githubOpenPullRequest(ctx, client, normalized, token)
	if err != nil {
		result.ProviderStatusClass = providerStatusClassFromError(err)
		result = e.cleanupGitHubReviewBranch(ctx, client, normalized, token, result)
		result.Retryable = reviewBranchExecutionRetryable(result.ProviderStatusClass, result.ExecutionPhase) && !result.CleanupRequired
		return result, err
	}
	result.ReviewURL = sanitizeURLUserInfo(reviewURL)
	result.ExecutionPhase = "completed"
	result.ProviderStatusClass = "2xx"
	result.Retryable = false
	return result, nil
}

func (e reviewBranchExecutor) Cleanup(ctx context.Context, input reviewBranchExecutionInput) (reviewBranchExecutionResult, error) {
	normalized, token, err := normalizeReviewBranchCleanupInput(input)
	result := reviewBranchExecutionResult{
		ProviderType:          normalized.ProviderType,
		Owner:                 normalized.Owner,
		Repository:            normalized.Repository,
		ReviewBranch:          normalized.ReviewBranch,
		BranchRef:             "refs/heads/" + normalized.ReviewBranch,
		ExecutionPhase:        "cleanup_review_branch",
		ExternalCallMade:      false,
		ProviderAPIMutation:   false,
		TokenConfigured:       token != "",
		TokenIncluded:         false,
		RequestBodiesIncluded: false,
		ResponseBodyIncluded:  false,
		CleanupAttempted:      true,
		ManualCleanupHint:     "review_branch_delete_required",
	}
	if err != nil {
		return result, err
	}
	if token == "" {
		return result, fmt.Errorf("provider token environment is not configured")
	}
	if err := validateTemplateProviderURL(ctx, normalized.APIBase); err != nil {
		return result, fmt.Errorf("unsafe provider API URL: %w", err)
	}
	client := e.HTTPClient
	if client == nil {
		client = newTemplateProviderHTTPClient()
	}
	endpoint := reviewBranchGitHubURL(normalized, "/repos/%s/%s/git/refs/heads/%s", normalized.Owner, normalized.Repository, normalized.ReviewBranch)
	result.ExternalCallMade = true
	result.ProviderAPIMutation = true
	if _, err := e.githubJSON(ctx, client, http.MethodDelete, endpoint, token, nil, http.StatusOK, http.StatusNoContent); err != nil {
		result.ProviderStatusClass = providerStatusClassFromError(err)
		result.CleanupRequired = true
		result.Retryable = reviewBranchExecutionRetryable(result.ProviderStatusClass, result.ExecutionPhase)
		return result, err
	}
	result.ProviderStatusClass = "2xx"
	result.CleanupSucceeded = true
	result.CleanupRequired = false
	result.ManualCleanupHint = ""
	result.Retryable = false
	return result, nil
}

func normalizeReviewBranchExecutionInput(input reviewBranchExecutionInput) (reviewBranchExecutionInput, string, error) {
	input.ProviderType = strings.ToLower(strings.TrimSpace(input.ProviderType))
	input.APIBase = strings.TrimRight(strings.TrimSpace(input.APIBase), "/")
	input.Owner = strings.TrimSpace(input.Owner)
	input.Repository = strings.TrimSpace(input.Repository)
	input.BaseBranch = strings.TrimSpace(input.BaseBranch)
	input.ReviewBranch = strings.TrimSpace(input.ReviewBranch)
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	input.TokenEnv = strings.TrimSpace(input.TokenEnv)
	if input.ProviderType != "github" {
		return input, "", fmt.Errorf("unsupported review branch provider")
	}
	if input.APIBase == "" {
		input.APIBase = "https://api.github.com"
	}
	if !isSafeRepositoryName(input.Owner) || !isSafeRepositoryName(input.Repository) {
		return input, "", fmt.Errorf("unsafe repository owner or name")
	}
	if !isSafeGitRefPart(input.BaseBranch) || !isSafeGitRefPart(input.ReviewBranch) || input.BaseBranch == input.ReviewBranch {
		return input, "", fmt.Errorf("unsafe review branch refs")
	}
	if !strings.HasPrefix(input.ReviewBranch, "assops/review/") && !strings.HasPrefix(input.ReviewBranch, "assops/template/") {
		return input, "", fmt.Errorf("review branch must use assops review or template prefix")
	}
	if input.Title == "" {
		input.Title = "ASSOPS review branch"
	}
	if input.TokenEnv == "" {
		input.TokenEnv = defaultTemplateProviderTokenEnv(input.ProviderType)
	}
	if !safeTemplateProviderTokenEnv(input.ProviderType, input.TokenEnv) {
		return input, "", fmt.Errorf("unsafe provider token environment")
	}
	if len(input.Files) == 0 || len(input.Files) > reviewBranchExecutorMaxFiles {
		return input, "", fmt.Errorf("invalid review branch file count")
	}
	files := make(map[string]string, len(input.Files))
	for rawPath, content := range input.Files {
		path := safeTemplateFilePath(rawPath)
		if path == "" {
			return input, "", fmt.Errorf("unsafe review branch file path")
		}
		if len([]byte(content)) > reviewBranchExecutorMaxFileBytes {
			return input, "", fmt.Errorf("review branch file is too large")
		}
		files[path] = content
	}
	input.Files = files
	return input, strings.TrimSpace(os.Getenv(input.TokenEnv)), nil
}

func normalizeReviewBranchCleanupInput(input reviewBranchExecutionInput) (reviewBranchExecutionInput, string, error) {
	input.ProviderType = strings.ToLower(strings.TrimSpace(input.ProviderType))
	input.APIBase = strings.TrimRight(strings.TrimSpace(input.APIBase), "/")
	input.Owner = strings.TrimSpace(input.Owner)
	input.Repository = strings.TrimSpace(input.Repository)
	input.ReviewBranch = strings.TrimSpace(input.ReviewBranch)
	input.TokenEnv = strings.TrimSpace(input.TokenEnv)
	if input.ProviderType != "github" {
		return input, "", fmt.Errorf("unsupported review branch provider")
	}
	if input.APIBase == "" {
		input.APIBase = "https://api.github.com"
	}
	if !isSafeRepositoryName(input.Owner) || !isSafeRepositoryName(input.Repository) {
		return input, "", fmt.Errorf("unsafe repository owner or name")
	}
	if !isSafeGitRefPart(input.ReviewBranch) {
		return input, "", fmt.Errorf("unsafe review branch ref")
	}
	if !strings.HasPrefix(input.ReviewBranch, "assops/review/") && !strings.HasPrefix(input.ReviewBranch, "assops/template/") {
		return input, "", fmt.Errorf("review branch must use assops review or template prefix")
	}
	if input.TokenEnv == "" {
		input.TokenEnv = defaultTemplateProviderTokenEnv(input.ProviderType)
	}
	if !safeTemplateProviderTokenEnv(input.ProviderType, input.TokenEnv) {
		return input, "", fmt.Errorf("unsafe provider token environment")
	}
	return input, strings.TrimSpace(os.Getenv(input.TokenEnv)), nil
}

func (e reviewBranchExecutor) githubBaseBranchSHA(ctx context.Context, client *http.Client, input reviewBranchExecutionInput, token string) (string, error) {
	endpoint := reviewBranchGitHubURL(input, "/repos/%s/%s/git/ref/heads/%s", input.Owner, input.Repository, input.BaseBranch)
	payload, err := e.githubJSON(ctx, client, http.MethodGet, endpoint, token, nil, http.StatusOK)
	if err != nil {
		return "", err
	}
	object := mapFromAny(payload["object"])
	sha := strings.TrimSpace(stringFromMap(object, "sha"))
	if !isFullHexSHA(sha) {
		return "", fmt.Errorf("provider returned invalid base ref")
	}
	return sha, nil
}

func (e reviewBranchExecutor) githubCreateBranchRef(ctx context.Context, client *http.Client, input reviewBranchExecutionInput, token, baseSHA string) error {
	endpoint := reviewBranchGitHubURL(input, "/repos/%s/%s/git/refs", input.Owner, input.Repository)
	_, err := e.githubJSON(ctx, client, http.MethodPost, endpoint, token, map[string]any{
		"ref": "refs/heads/" + input.ReviewBranch,
		"sha": baseSHA,
	}, http.StatusCreated)
	return err
}
