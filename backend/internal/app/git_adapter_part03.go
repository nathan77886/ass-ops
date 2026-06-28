package app

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"strings"
)

func (e *GitExecutor) LookupTag(ctx context.Context, db *gorm.DB, opID string) (*gitExecutionResult, error) {
	op, err := operationRunByID(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	if op.OperationType != "repo.tag.lookup" {
		return nil, ErrNotFound
	}
	input := mapFromAny(op.Input.Data)
	runID := strings.TrimSpace(stringFromMap(input, "repo_tag_run_id"))
	targetRemoteID := strings.TrimSpace(stringFromMap(input, "target_remote_id"))
	tagName := strings.TrimSpace(stringFromMap(input, "tag_name"))
	if runID == "" {
		return nil, fmt.Errorf("repo_tag_run_id is required")
	}
	if targetRemoteID == "" {
		return nil, fmt.Errorf("target_remote_id is required")
	}
	if tagName == "" || !isSafeGitRefPart(tagName) {
		return nil, fmt.Errorf("unsafe tag ref")
	}
	run, err := repoTagRunMapByID(ctx, db, runID)
	if err != nil {
		return nil, fmt.Errorf("loading repo tag run: %w", err)
	}
	runRemoteID := strings.TrimSpace(firstNonEmptyString(stringFromMap(run, "target_remote_id"), stringFromMap(run, "git_remote_id")))
	if runRemoteID != "" && runRemoteID != targetRemoteID {
		return nil, fmt.Errorf("target_remote_id does not match repo tag run")
	}
	runTagName := strings.TrimSpace(stringFromMap(run, "tag_name"))
	if runTagName != "" && runTagName != tagName {
		return nil, fmt.Errorf("tag_name does not match repo tag run")
	}
	remote, err := gitRemoteMapByID(ctx, db, targetRemoteID)
	if err != nil {
		return nil, fmt.Errorf("loading target remote: %w", err)
	}
	remoteURL := remoteURLFromRow(remote)
	if remoteURL == "" {
		return nil, fmt.Errorf("target remote must have remote_url or urls")
	}
	result := &gitExecutionResult{Details: map[string]any{
		"mode":                         "repo_tag_live_remote_lookup",
		"repo_tag_run_id":              runID,
		"target_remote_id":             targetRemoteID,
		"tag_lookup_performed":         true,
		"external_call_made":           true,
		"git_remote_lookup_performed":  true,
		"raw_git_output_recorded":      false,
		"remote_url_recorded":          false,
		"credentials_recorded":         false,
		"contains_token":               false,
		"credential_userinfo_stripped": false,
	}}
	if err := e.validateExistingLocalBareRemote(ctx, result, remote, remoteURL, "target"); err != nil {
		return result, err
	}
	safeRemoteURL, stripped := stripGitRemoteURLUserinfo(remoteURL)
	if safeRemoteURL == "" {
		return result, fmt.Errorf("target remote URL is invalid")
	}
	result.Details["credential_userinfo_stripped"] = stripped

	runner := e.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	stdout, _, err := runner.Run(ctx, "", "git", "ls-remote", "--tags", safeRemoteURL, "refs/tags/"+tagName)
	if err != nil {
		return result, fmt.Errorf("git ls-remote failed: %s", sanitizeLookupError(err))
	}
	matchedSHA, matchedCount := parseLsRemoteTagLookup(stdout, tagName)
	found := matchedCount > 0
	result.AfterSHA = matchedSHA
	result.Details["remote_tag_found"] = found
	result.Details["matched_sha_present"] = matchedSHA != ""
	result.Details["matched_sha"] = matchedSHA
	result.Details["matched_count"] = matchedCount
	return result, nil
}

func (e *GitExecutor) ProvisionTemplateRepository(ctx context.Context, repo map[string]any, remotes []map[string]any, files []map[string]any) (*gitExecutionResult, error) {
	result := &gitExecutionResult{Details: map[string]any{
		"provisioned":    false,
		"repository_id":  repo["id"],
		"repository_key": repo["repo_key"],
	}}
	remote := localBareTemplateRemote(remotes)
	external := externalTemplateRemote(remotes)
	externalSelected := false
	if remote == nil {
		remote = external
		externalSelected = remote != nil
	}
	if remote == nil {
		result.Details["reason"] = "no provisionable template remote configured"
		return result, nil
	}
	remoteURL := remoteURLFromRow(remote)
	if externalSelected {
		if err := e.provisionExternalTemplateRepository(ctx, result, repo, remote); err != nil {
			return result, err
		}
		if value, _ := result.Details["remote_url"].(string); strings.TrimSpace(value) != "" {
			remoteURL = value
		}
		if len(files) == 0 {
			return result, nil
		}
		if alreadyExists, _ := result.Details["already_provisioned"].(bool); alreadyExists && !templateRemoteAllowsExistingRepositoryPush(remote) {
			defaultBranch := defaultBranchFromRow(repo)
			result.Details["provisioned"] = false
			result.Details["repository_exists"] = true
			result.Details["starter_push_skipped"] = true
			result.Details["reason"] = "starter files were not pushed because the external repository already exists"
			result.Details["remote_id"] = remote["id"]
			result.Details["remote_url"] = remoteURL
			result.Details["default_branch"] = defaultBranch
			result.Details["file_count"] = len(files)
			result.Details["repository_reconciliation"] = templateRepositoryReconciliation("existing_repository", repo, remote, defaultBranch, len(files))
			return result, nil
		}
	}
	if len(files) == 0 {
		result.Details["reason"] = "no starter files configured"
		return result, nil
	}
	defaultBranch := defaultBranchFromRow(repo)
	if !isSafeGitRefPart(defaultBranch) {
		return result, fmt.Errorf("unsafe default branch %q", defaultBranch)
	}
	if externalSelected && templateRemoteProtectsDefaultBranch(remote) && !templateRemoteAllowsProtectedBranchPush(remote) {
		result.Details["provisioned"] = false
		result.Details["repository_created"] = true
		result.Details["starter_push_skipped"] = true
		result.Details["reason"] = "starter files were not pushed because the template remote is marked protected"
		result.Details["remote_id"] = remote["id"]
		result.Details["remote_url"] = remoteURL
		result.Details["default_branch"] = defaultBranch
		result.Details["file_count"] = len(files)
		result.Details["repository_reconciliation"] = templateRepositoryReconciliation("protected_branch", repo, remote, defaultBranch, len(files))
		return result, nil
	}
	if strings.EqualFold(strings.TrimSpace(fmt.Sprint(remote["provider_type"])), "local_bare") ||
		strings.EqualFold(strings.TrimSpace(fmt.Sprint(remote["kind"])), "local_bare") {
		if !safeLocalBareRemotePath(remoteURL, e.LocalBareBaseDirs) {
			return result, fmt.Errorf("local_bare remote_url must be under an allowed absolute base directory")
		}
		if err := os.MkdirAll(filepath.Dir(remoteURL), 0o700); err != nil {
			return result, fmt.Errorf("creating local bare repo parent: %w", err)
		}
		if !safeResolvedLocalBareRemotePath(remoteURL, e.LocalBareBaseDirs) {
			return result, fmt.Errorf("local_bare remote_url resolves outside allowed base directories")
		}
		existed := true
		if _, err := os.Stat(remoteURL); errors.Is(err, os.ErrNotExist) {
			existed = false
			if err := e.run(ctx, result, "", "git", "init", "--bare", remoteURL); err != nil {
				return result, err
			}
		} else if err != nil {
			return result, fmt.Errorf("checking local bare repo: %w", err)
		} else if err := e.ensureBareRepository(ctx, result, remoteURL); err != nil {
			return result, err
		}
		if existed {
			if sha, ok, err := e.bareBranchSHA(ctx, result, remoteURL, defaultBranch); err != nil {
				return result, err
			} else if ok {
				result.AfterSHA = sha
				result.Details["provisioned"] = true
				result.Details["already_provisioned"] = true
				result.Details["remote_id"] = remote["id"]
				result.Details["remote_url"] = remoteURL
				result.Details["default_branch"] = defaultBranch
				result.Details["file_count"] = len(files)
				return result, nil
			}
		}
	} else if remoteURL == "" {
		return result, fmt.Errorf("template remote must have remote_url before starter files can be pushed")
	}

	if err := e.pushTemplateFiles(ctx, result, repo, remote, remoteURL, defaultBranch, files); err != nil {
		return result, err
	}
	return result, nil
}

func (e *GitExecutor) pushTemplateFiles(ctx context.Context, result *gitExecutionResult, repo, remote map[string]any, remoteURL, defaultBranch string, files []map[string]any) error {
	repoDir, cleanup, err := e.newWorkDir("assops-template-*")
	if err != nil {
		return err
	}
	defer cleanup()

	if err := e.run(ctx, result, repoDir, "git", "init", "repo"); err != nil {
		return err
	}
	workTree := filepath.Join(repoDir, "repo")
	for _, file := range files {
		path := safeTemplateFilePath(strings.TrimSpace(fmt.Sprint(file["path"])))
		if path == "" {
			return fmt.Errorf("unsafe template file path %q", file["path"])
		}
		target := filepath.Join(workTree, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("creating template file parent: %w", err)
		}
		if err := os.WriteFile(target, []byte(templateFileContent(file)), 0o600); err != nil {
			return fmt.Errorf("writing template file %s: %w", path, err)
		}
	}
	if err := e.run(ctx, result, workTree, "git", "add", "."); err != nil {
		return err
	}
	if err := e.run(ctx, result, workTree, "git", "commit", "-m", "Initialize repository from ASSOPS template"); err != nil {
		return err
	}
	if defaultBranch != "master" {
		if err := e.run(ctx, result, workTree, "git", "branch", "-M", defaultBranch); err != nil {
			return err
		}
	}
	if err := e.run(ctx, result, workTree, "git", "remote", "add", "origin", remoteURL); err != nil {
		return err
	}
	if err := e.run(ctx, result, workTree, "git", "push", "origin", "HEAD:refs/heads/"+defaultBranch); err != nil {
		return err
	}
	sha, _ := e.revParse(ctx, workTree, "HEAD")
	result.AfterSHA = sha
	result.Details["provisioned"] = true
	result.Details["remote_id"] = remote["id"]
	result.Details["remote_url"] = remoteURL
	result.Details["default_branch"] = defaultBranch
	result.Details["file_count"] = len(files)
	return nil
}
