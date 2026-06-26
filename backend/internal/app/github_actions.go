package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type GitHubActionsSyncer struct {
	HTTPClient *http.Client
	APIBase    string
}

type GitHubActionsSyncResult struct {
	RemoteID string
	Owner    string
	Repo     string
	Runs     []GitHubActionRunInput
}

type GitHubRepositoryLabelsSyncResult struct {
	RemoteID string
	Owner    string
	Repo     string
	Labels   []GitHubRepositoryLabelInput
}

type GitHubActionRunInput struct {
	ExternalRunID string
	WorkflowName  string
	RunID         string
	Branch        string
	CommitSHA     string
	Status        string
	Conclusion    string
	HTMLURL       string
	StartedAt     *time.Time
	UpdatedAt     *time.Time
	Metadata      map[string]any
	Artifacts     []GitHubActionArtifactInput
}

type GitHubActionArtifactInput struct {
	ExternalArtifactID string
	Name               string
	SizeInBytes        int64
	Expired            bool
	CreatedAt          *time.Time
	UpdatedAt          *time.Time
	ExpiresAt          *time.Time
	Metadata           map[string]any
}

type GitHubRepositoryLabelInput struct {
	ExternalLabelID string
	NodeID          string
	Name            string
	Color           string
	Description     string
	IsDefault       bool
}

func NewGitHubActionsSyncer() *GitHubActionsSyncer {
	return &GitHubActionsSyncer{HTTPClient: &http.Client{Timeout: 15 * time.Second}, APIBase: "https://api.github.com"}
}

func (s *GitHubActionsSyncer) Sync(ctx context.Context, db sqlx.ExtContext, opID string) (*GitHubActionsSyncResult, error) {
	op, err := queryOne(ctx, db, "SELECT * FROM operation_runs WHERE id=$1", opID)
	if err != nil {
		return nil, err
	}
	remoteID := strings.TrimSpace(fmt.Sprint(op["git_remote_id"]))
	if remoteID == "" || remoteID == "<nil>" {
		return nil, fmt.Errorf("operation is missing git_remote_id")
	}
	remote, err := queryOne(ctx, db, "SELECT * FROM git_remotes WHERE id=$1", remoteID)
	if err != nil {
		return nil, fmt.Errorf("loading GitHub remote: %w", err)
	}
	owner, repo, err := gitHubRepositoryFromRemote(remote)
	if err != nil {
		return nil, err
	}
	input := mapFromAny(op["input"])
	branch := strings.TrimSpace(fmt.Sprint(input["branch"]))
	if branch == "<nil>" {
		branch = ""
	}
	limit := intFromAny(input["limit"], 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}
	result := &GitHubActionsSyncResult{RemoteID: remoteID, Owner: owner, Repo: repo}
	runs, err := s.fetchWorkflowRuns(ctx, owner, repo, branch, limit, tokenFromRemote(remote))
	if err != nil {
		return result, err
	}
	result.Runs = runs
	return result, nil
}

func (s *GitHubActionsSyncer) SyncLabels(ctx context.Context, db sqlx.ExtContext, opID string) (*GitHubRepositoryLabelsSyncResult, error) {
	op, err := queryOne(ctx, db, "SELECT * FROM operation_runs WHERE id=$1", opID)
	if err != nil {
		return nil, err
	}
	remoteID := strings.TrimSpace(fmt.Sprint(op["git_remote_id"]))
	if remoteID == "" || remoteID == "<nil>" {
		return nil, fmt.Errorf("operation is missing git_remote_id")
	}
	remote, err := queryOne(ctx, db, "SELECT * FROM git_remotes WHERE id=$1", remoteID)
	if err != nil {
		return nil, fmt.Errorf("loading GitHub remote: %w", err)
	}
	owner, repo, err := gitHubRepositoryFromRemote(remote)
	if err != nil {
		return nil, err
	}
	result := &GitHubRepositoryLabelsSyncResult{RemoteID: remoteID, Owner: owner, Repo: repo}
	labels, err := s.fetchRepositoryLabels(ctx, owner, repo, tokenFromRemote(remote))
	if err != nil {
		return result, err
	}
	result.Labels = labels
	return result, nil
}

func (s *GitHubActionsSyncer) fetchWorkflowRuns(ctx context.Context, owner, repo, branch string, limit int, token string) ([]GitHubActionRunInput, error) {
	apiBase := strings.TrimRight(s.APIBase, "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	endpoint, err := url.Parse(apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/runs")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("per_page", strconv.Itoa(limit))
	if branch != "" {
		query.Set("branch", branch)
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "assops-mvp")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying GitHub Actions: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("GitHub Actions API returned %s", res.Status)
	}
	if err := validateGitHubTokenScopes(res.Header.Get("X-OAuth-Scopes")); err != nil {
		return nil, err
	}
	var payload struct {
		WorkflowRuns []struct {
			ID           int64      `json:"id"`
			Name         string     `json:"name"`
			DisplayTitle string     `json:"display_title"`
			RunNumber    int64      `json:"run_number"`
			HeadBranch   string     `json:"head_branch"`
			HeadSHA      string     `json:"head_sha"`
			Status       string     `json:"status"`
			Conclusion   string     `json:"conclusion"`
			HTMLURL      string     `json:"html_url"`
			RunStartedAt *time.Time `json:"run_started_at"`
			UpdatedAt    *time.Time `json:"updated_at"`
			Event        string     `json:"event"`
		} `json:"workflow_runs"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding GitHub Actions response: %w", err)
	}
	runs := make([]GitHubActionRunInput, 0, len(payload.WorkflowRuns))
	for _, run := range payload.WorkflowRuns {
		name := run.Name
		if name == "" {
			name = run.DisplayTitle
		}
		runID := strconv.FormatInt(run.ID, 10)
		artifacts, err := s.fetchWorkflowRunArtifacts(ctx, owner, repo, runID, token)
		if err != nil {
			artifacts = []GitHubActionArtifactInput{}
		}
		metadata := map[string]any{
			"event":                run.Event,
			"run_number":           run.RunNumber,
			"artifact_sync_status": "synced",
		}
		if err != nil {
			metadata["artifact_sync_status"] = "unavailable"
		}
		runs = append(runs, GitHubActionRunInput{
			ExternalRunID: runID,
			WorkflowName:  name,
			RunID:         runID,
			Branch:        run.HeadBranch,
			CommitSHA:     run.HeadSHA,
			Status:        run.Status,
			Conclusion:    run.Conclusion,
			HTMLURL:       run.HTMLURL,
			StartedAt:     run.RunStartedAt,
			UpdatedAt:     run.UpdatedAt,
			Metadata:      metadata,
			Artifacts:     artifacts,
		})
	}
	return runs, nil
}

func (s *GitHubActionsSyncer) fetchRepositoryLabels(ctx context.Context, owner, repo, token string) ([]GitHubRepositoryLabelInput, error) {
	const perPage = 100
	const maxLabels = 500
	apiBase := strings.TrimRight(s.APIBase, "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	labels := []GitHubRepositoryLabelInput{}
	for page := 1; len(labels) < maxLabels; page++ {
		endpoint, err := url.Parse(apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/labels")
		if err != nil {
			return nil, err
		}
		query := endpoint.Query()
		query.Set("per_page", strconv.Itoa(perPage))
		query.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "assops-mvp")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("querying GitHub labels: %w", err)
		}
		if res.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
			_ = res.Body.Close()
			return nil, fmt.Errorf("GitHub labels API returned %s", res.Status)
		}
		if err := validateGitHubTokenScopes(res.Header.Get("X-OAuth-Scopes")); err != nil {
			_ = res.Body.Close()
			return nil, err
		}
		var payload []struct {
			ID          int64  `json:"id"`
			NodeID      string `json:"node_id"`
			Name        string `json:"name"`
			Color       string `json:"color"`
			Description string `json:"description"`
			Default     bool   `json:"default"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decoding GitHub labels response: %w", err)
		}
		_ = res.Body.Close()
		for _, label := range payload {
			if len(labels) >= maxLabels {
				break
			}
			name := strings.TrimSpace(label.Name)
			if name == "" {
				continue
			}
			labels = append(labels, GitHubRepositoryLabelInput{
				ExternalLabelID: strconv.FormatInt(label.ID, 10),
				NodeID:          strings.TrimSpace(label.NodeID),
				Name:            name,
				Color:           cleanGitHubLabelColor(label.Color),
				Description:     strings.TrimSpace(label.Description),
				IsDefault:       label.Default,
			})
		}
		if !githubLinkHasNext(res.Header.Get("Link")) {
			break
		}
	}
	return labels, nil
}

func githubLinkHasNext(linkHeader string) bool {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, `rel="next"`) {
			return true
		}
	}
	return false
}

func cleanGitHubLabelColor(color string) string {
	color = strings.TrimPrefix(strings.TrimSpace(color), "#")
	if len(color) > 64 {
		return color[:64]
	}
	if len(color) == 6 {
		for _, ch := range color {
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
				return color
			}
		}
		return strings.ToLower(color)
	}
	return color
}

func (s *GitHubActionsSyncer) fetchWorkflowRunArtifacts(ctx context.Context, owner, repo, runID, token string) ([]GitHubActionArtifactInput, error) {
	const perPage = 100
	const maxArtifacts = 500
	apiBase := strings.TrimRight(s.APIBase, "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	artifacts := []GitHubActionArtifactInput{}
	for page := 1; len(artifacts) < maxArtifacts; page++ {
		endpoint, err := url.Parse(apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/runs/" + url.PathEscape(runID) + "/artifacts")
		if err != nil {
			return nil, err
		}
		query := endpoint.Query()
		query.Set("per_page", strconv.Itoa(perPage))
		query.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "assops-mvp")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("querying GitHub Actions artifacts: %w", err)
		}
		if res.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
			_ = res.Body.Close()
			return nil, fmt.Errorf("GitHub Actions artifacts API returned %s", res.Status)
		}
		if err := validateGitHubTokenScopes(res.Header.Get("X-OAuth-Scopes")); err != nil {
			_ = res.Body.Close()
			return nil, err
		}
		var payload struct {
			TotalCount int `json:"total_count"`
			Artifacts  []struct {
				ID          int64      `json:"id"`
				NodeID      string     `json:"node_id"`
				Name        string     `json:"name"`
				SizeInBytes int64      `json:"size_in_bytes"`
				URL         string     `json:"url"`
				Expired     bool       `json:"expired"`
				CreatedAt   *time.Time `json:"created_at"`
				UpdatedAt   *time.Time `json:"updated_at"`
				ExpiresAt   *time.Time `json:"expires_at"`
			} `json:"artifacts"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decoding GitHub Actions artifacts response: %w", err)
		}
		_ = res.Body.Close()
		for _, artifact := range payload.Artifacts {
			if len(artifacts) >= maxArtifacts {
				break
			}
			artifacts = append(artifacts, GitHubActionArtifactInput{
				ExternalArtifactID: strconv.FormatInt(artifact.ID, 10),
				Name:               artifact.Name,
				SizeInBytes:        artifact.SizeInBytes,
				Expired:            artifact.Expired,
				CreatedAt:          artifact.CreatedAt,
				UpdatedAt:          artifact.UpdatedAt,
				ExpiresAt:          artifact.ExpiresAt,
				Metadata: map[string]any{
					"node_id": artifact.NodeID,
					"url":     artifact.URL,
				},
			})
		}
		if len(payload.Artifacts) == 0 || len(artifacts) >= payload.TotalCount {
			break
		}
	}
	return artifacts, nil
}

func gitHubRepositoryFromRemote(remote map[string]any) (string, string, error) {
	for _, key := range []string{"web_url", "remote_url"} {
		owner, repo, ok := parseGitHubRepository(fmt.Sprint(remote[key]))
		if ok {
			return owner, repo, nil
		}
	}
	if urls, ok := remote["urls"].([]any); ok {
		for _, rawURL := range urls {
			owner, repo, ok := parseGitHubRepository(fmt.Sprint(rawURL))
			if ok {
				return owner, repo, nil
			}
		}
	}
	return "", "", fmt.Errorf("remote is not a GitHub repository")
}

func parseGitHubRepository(rawURL string) (string, string, bool) {
	rawURL = strings.TrimSpace(strings.TrimSuffix(rawURL, ".git"))
	if rawURL == "" || rawURL == "<nil>" {
		return "", "", false
	}
	for _, prefix := range []string{"git@github.com:", "git@www.github.com:"} {
		if strings.HasPrefix(rawURL, prefix) {
			parts := strings.Split(strings.TrimPrefix(rawURL, prefix), "/")
			if len(parts) >= 2 {
				return parts[0], parts[1], true
			}
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", false
	}
	if parsed.Host != "github.com" && parsed.Host != "www.github.com" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), true
}

func tokenFromRemote(remote map[string]any) string {
	return strings.TrimSpace(os.Getenv("ASSOPS_GITHUB_ACTIONS_READ_TOKEN"))
}

func validateGitHubTokenScopes(rawScopes string) error {
	if strings.TrimSpace(rawScopes) == "" {
		return nil
	}
	for _, rawScope := range strings.Split(rawScopes, ",") {
		scope := strings.TrimSpace(rawScope)
		switch scope {
		case "repo", "admin:org", "delete_repo", "workflow", "write:packages", "admin:repo_hook":
			return fmt.Errorf("GitHub token has disallowed scope %q; use a read-only Actions token", scope)
		}
	}
	return nil
}

func intFromAny(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}
