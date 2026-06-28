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
	"sort"
	"strings"
)

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
		result.ManualCleanupHint = "review_branch_delete_required"
		return result
	}
	result.CleanupSucceeded = true
	result.CleanupRequired = false
	result.ManualCleanupHint = ""
	return result
}

func reviewBranchExecutionRetryable(statusClass, phase string) bool {
	switch phase {
	case "read_base_ref", "create_review_branch", "commit_starter_files", "open_review_request", "cleanup_review_branch":
	default:
		return false
	}
	switch safeProviderReviewStatusClass(statusClass) {
	case "5xx", "unknown":
		return true
	default:
		return false
	}
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
