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
)

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
