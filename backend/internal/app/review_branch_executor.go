package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
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
	ProviderStatusClass   string
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
	baseSHA, err := e.githubBaseBranchSHA(ctx, client, normalized, token)
	result.ExternalCallMade = true
	if err != nil {
		result.ProviderStatusClass = providerStatusClassFromError(err)
		return result, err
	}
	result.BaseSHA = baseSHA
	if err := e.githubCreateBranchRef(ctx, client, normalized, token, baseSHA); err != nil {
		result.ProviderStatusClass = providerStatusClassFromError(err)
		return result, err
	}
	result.ProviderAPIMutation = true
	for _, path := range sortedReviewBranchFilePaths(normalized.Files) {
		if err := e.githubPutFile(ctx, client, normalized, token, path, normalized.Files[path]); err != nil {
			result.ProviderStatusClass = providerStatusClassFromError(err)
			result = e.cleanupGitHubReviewBranch(ctx, client, normalized, token, result)
			return result, err
		}
	}
	reviewURL, err := e.githubOpenPullRequest(ctx, client, normalized, token)
	if err != nil {
		result.ProviderStatusClass = providerStatusClassFromError(err)
		result = e.cleanupGitHubReviewBranch(ctx, client, normalized, token, result)
		return result, err
	}
	result.ReviewURL = sanitizeURLUserInfo(reviewURL)
	result.ProviderStatusClass = "2xx"
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
	if !strings.HasPrefix(input.ReviewBranch, "assops/review/") {
		return input, "", fmt.Errorf("review branch must use assops/review/ prefix")
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

func (e reviewBranchExecutor) githubPutFile(ctx context.Context, client *http.Client, input reviewBranchExecutionInput, token, path, content string) error {
	endpoint := reviewBranchGitHubURL(input, "/repos/%s/%s/contents/%s", input.Owner, input.Repository, path)
	_, err := e.githubJSON(ctx, client, http.MethodPut, endpoint, token, map[string]any{
		"message": "Apply ASSOPS staged file " + path,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  input.ReviewBranch,
	}, http.StatusOK, http.StatusCreated)
	return err
}

func (e reviewBranchExecutor) githubOpenPullRequest(ctx context.Context, client *http.Client, input reviewBranchExecutionInput, token string) (string, error) {
	endpoint := reviewBranchGitHubURL(input, "/repos/%s/%s/pulls", input.Owner, input.Repository)
	payload, err := e.githubJSON(ctx, client, http.MethodPost, endpoint, token, map[string]any{
		"title": input.Title,
		"body":  input.Body,
		"head":  input.ReviewBranch,
		"base":  input.BaseBranch,
	}, http.StatusCreated)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(firstNonEmptyString(stringFromMap(payload, "html_url"), stringFromMap(payload, "url"))), nil
}

func (e reviewBranchExecutor) cleanupGitHubReviewBranch(ctx context.Context, client *http.Client, input reviewBranchExecutionInput, token string, result reviewBranchExecutionResult) reviewBranchExecutionResult {
	result.CleanupAttempted = true
	endpoint := reviewBranchGitHubURL(input, "/repos/%s/%s/git/refs/heads/%s", input.Owner, input.Repository, input.ReviewBranch)
	if _, err := e.githubJSON(ctx, client, http.MethodDelete, endpoint, token, nil, http.StatusOK, http.StatusNoContent); err != nil {
		result.CleanupRequired = true
		return result
	}
	result.CleanupSucceeded = true
	result.CleanupRequired = false
	return result
}

func (e reviewBranchExecutor) githubJSON(ctx context.Context, client *http.Client, method, endpoint, token string, body map[string]any, okStatuses ...int) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider request failed")
	}
	defer res.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	for _, status := range okStatuses {
		if res.StatusCode == status {
			payload := map[string]any{}
			_ = json.Unmarshal(responseBody, &payload)
			return payload, nil
		}
	}
	return nil, reviewBranchProviderStatusError{StatusCode: res.StatusCode, Message: providerErrorMessage(responseBody)}
}

type reviewBranchProviderStatusError struct {
	StatusCode int
	Message    string
}

func (err reviewBranchProviderStatusError) Error() string {
	message := http.StatusText(err.StatusCode)
	if message == "" {
		message = "unexpected status"
	}
	return fmt.Sprintf("provider request returned %d: %s", err.StatusCode, message)
}

func providerStatusClassFromError(err error) string {
	var statusErr reviewBranchProviderStatusError
	if ok := errors.As(err, &statusErr); ok && statusErr.StatusCode > 0 {
		return fmt.Sprintf("%dxx", statusErr.StatusCode/100)
	}
	return "unknown"
}

func reviewBranchGitHubURL(input reviewBranchExecutionInput, format string, values ...string) string {
	escaped := make([]any, 0, len(values))
	for index, value := range values {
		if index == len(values)-1 && strings.Contains(value, "/") {
			segments := strings.Split(value, "/")
			for i, segment := range segments {
				segments[i] = url.PathEscape(segment)
			}
			escaped = append(escaped, strings.Join(segments, "/"))
			continue
		}
		escaped = append(escaped, url.PathEscape(value))
	}
	return input.APIBase + fmt.Sprintf(format, escaped...)
}

func sortedReviewBranchFilePaths(files map[string]string) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
