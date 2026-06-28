package app

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

func (s *GitHubActionsSyncer) Sync(ctx context.Context, db *gorm.DB, opID string) (*GitHubActionsSyncResult, error) {
	op, remote, err := loadGitHubOperationRemote(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	remoteMap := gitRemoteMap(remote, nil, "")
	owner, repo, err := gitHubRepositoryFromRemote(remoteMap)
	if err != nil {
		return nil, err
	}
	input := mapFromAny(op.Input.Data)
	branch := strings.TrimSpace(fmt.Sprint(input["branch"]))
	if branch == "<nil>" {
		branch = ""
	}
	limit := intFromAny(input["limit"], 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}
	result := &GitHubActionsSyncResult{RemoteID: remote.ID, Owner: owner, Repo: repo}
	runs, err := s.fetchWorkflowRuns(ctx, owner, repo, branch, limit, tokenFromRemote(remoteMap))
	if err != nil {
		return result, err
	}
	result.Runs = runs
	return result, nil
}

func (s *GitHubActionsSyncer) SyncLabels(ctx context.Context, db *gorm.DB, opID string) (*GitHubRepositoryLabelsSyncResult, error) {
	_, remote, err := loadGitHubOperationRemote(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	remoteMap := gitRemoteMap(remote, nil, "")
	owner, repo, err := gitHubRepositoryFromRemote(remoteMap)
	if err != nil {
		return nil, err
	}
	result := &GitHubRepositoryLabelsSyncResult{RemoteID: remote.ID, Owner: owner, Repo: repo}
	labels, err := s.fetchRepositoryLabels(ctx, owner, repo, tokenFromRemote(remoteMap))
	if err != nil {
		return result, err
	}
	result.Labels = labels
	return result, nil
}

func loadGitHubOperationRemote(ctx context.Context, db *gorm.DB, opID string) (GormOperationRun, GormGitRemote, error) {
	if db == nil {
		return GormOperationRun{}, GormGitRemote{}, fmt.Errorf("database is not configured")
	}
	var op GormOperationRun
	if err := db.WithContext(ctx).First(&op, "id = ?", opID).Error; err != nil {
		return GormOperationRun{}, GormGitRemote{}, err
	}
	remoteID := strings.TrimSpace(op.GitRemoteID.String)
	if !op.GitRemoteID.Valid || remoteID == "" {
		return GormOperationRun{}, GormGitRemote{}, fmt.Errorf("operation is missing git_remote_id")
	}
	var remote GormGitRemote
	if err := db.WithContext(ctx).First(&remote, "id = ?", remoteID).Error; err != nil {
		return GormOperationRun{}, GormGitRemote{}, fmt.Errorf("loading GitHub remote: %w", err)
	}
	return op, remote, nil
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
