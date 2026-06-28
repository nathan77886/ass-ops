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

func (e *GitExecutor) CommitConfigScaffold(ctx context.Context, db *gorm.DB, repoID, remoteID string) (*gitExecutionResult, error) {
	result := &gitExecutionResult{Details: map[string]any{
		"project_git_repository_id": repoID,
		"config_remote_id":          remoteID,
		"result_scope":              "sanitized_config_git_workflow_local_bare",
	}}
	repo, err := projectGitRepositoryMapByID(ctx, db, repoID)
	if err != nil {
		return result, fmt.Errorf("loading config repository: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(stringFromMap(repo, "repo_role"))) != "config" {
		return result, fmt.Errorf("repository role must be config")
	}
	remote, err := remoteForRepositoryGorm(ctx, db, repoID, remoteID)
	if err != nil {
		return result, fmt.Errorf("loading config remote: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(remote["provider_type"])), "local_bare") {
		return result, fmt.Errorf("config git local write requires local_bare provider")
	}
	var candidateCount int64
	if err := db.WithContext(ctx).
		Model(&GormGitRemote{}).
		Where(&GormGitRemote{ProjectGitRepositoryID: repoID, ProviderType: "local_bare"}).
		Count(&candidateCount).Error; err != nil {
		return result, fmt.Errorf("counting local_bare config remotes: %w", err)
	}
	if candidateCount != 1 {
		return result, fmt.Errorf("config git local write requires exactly one local_bare remote")
	}
	remoteURL := remoteURLFromRow(remote)
	if !safeLocalBareRemotePath(remoteURL, e.LocalBareBaseDirs) {
		return result, fmt.Errorf("local_bare remote_url must be under an allowed absolute base directory")
	}
	existed := true
	if _, err := os.Stat(remoteURL); errors.Is(err, os.ErrNotExist) {
		existed = false
		if err := os.MkdirAll(filepath.Dir(remoteURL), 0o700); err != nil {
			return result, fmt.Errorf("creating local bare repo parent: %w", err)
		}
		if !safeResolvedLocalBareRemotePath(remoteURL, e.LocalBareBaseDirs) {
			return result, fmt.Errorf("local_bare remote_url resolves outside allowed base directories")
		}
		if err := e.run(ctx, result, "", "git", "init", "--bare", remoteURL); err != nil {
			return result, err
		}
	} else if err != nil {
		return result, fmt.Errorf("checking local bare repo: %w", err)
	} else if !safeResolvedLocalBareRemotePath(remoteURL, e.LocalBareBaseDirs) {
		return result, fmt.Errorf("local_bare remote_url resolves outside allowed base directories")
	} else if err := e.ensureBareRepository(ctx, result, remoteURL); err != nil {
		return result, err
	}
	defaultBranch := defaultBranchFromRow(repo)
	if !isSafeGitRefPart(defaultBranch) {
		return result, fmt.Errorf("unsafe default branch %q", defaultBranch)
	}
	files := configScaffoldMaterializedFiles(repo)
	if err := e.commitConfigScaffoldFiles(ctx, result, repo, remote, remoteURL, defaultBranch, files, existed); err != nil {
		return result, err
	}
	result.Details["provider_type"] = "local_bare"
	result.Details["default_branch_configured"] = true
	result.Details["remote_existed"] = existed
	result.Details["scaffold_file_count"] = len(files)
	result.Details["git_write_performed"] = true
	result.Details["git_push_performed"] = !boolOnlyFromAny(result.Details["no_changes"])
	result.Details["external_call_made"] = false
	result.Details["file_content_included"] = false
	result.Details["secret_included"] = false
	result.Details["secret_scan_kind"] = "template_secret_marker_scan"
	result.Details["raw_git_output_recorded"] = false
	result.Details["commit_sha_included"] = false
	result.Details["commit_sha_present"] = result.AfterSHA != ""
	result.Details["suppressed_fields"] = []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "git_output"}
	return result, nil
}

func (e *GitExecutor) commitConfigScaffoldFiles(ctx context.Context, result *gitExecutionResult, repo, remote map[string]any, remoteURL, defaultBranch string, files []map[string]any, remoteExisted bool) error {
	repoDir, cleanup, err := e.newWorkDir("assops-config-*")
	if err != nil {
		return err
	}
	defer cleanup()
	if err := e.run(ctx, result, repoDir, "git", "init", "repo"); err != nil {
		return err
	}
	workTree := filepath.Join(repoDir, "repo")
	if err := e.run(ctx, result, workTree, "git", "remote", "add", "origin", remoteURL); err != nil {
		return err
	}
	branchExists := false
	if remoteExisted {
		var lookupErr error
		if _, branchExists, lookupErr = e.bareBranchSHA(ctx, result, remoteURL, defaultBranch); lookupErr != nil {
			return lookupErr
		}
	}
	if branchExists {
		if err := e.run(ctx, result, workTree, "git", "fetch", "--depth=1", "origin", "refs/heads/"+defaultBranch+":refs/remotes/origin/"+defaultBranch); err != nil {
			return err
		}
		if err := e.run(ctx, result, workTree, "git", "checkout", "-B", defaultBranch, "refs/remotes/origin/"+defaultBranch); err != nil {
			return err
		}
	} else if err := e.run(ctx, result, workTree, "git", "checkout", "-B", defaultBranch); err != nil {
		return err
	}
	for _, file := range files {
		path := safeTemplateFilePath(strings.TrimSpace(fmt.Sprint(file["path"])))
		if path == "" {
			return fmt.Errorf("unsafe config scaffold file path %q", file["path"])
		}
		content := templateFileContent(file)
		if configScaffoldContentLooksSensitive(content) {
			return fmt.Errorf("config scaffold content failed secret scan")
		}
		target := filepath.Join(workTree, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("creating config scaffold file parent: %w", err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return fmt.Errorf("writing config scaffold file %s: %w", path, err)
		}
	}
	if err := e.run(ctx, result, workTree, "git", "add", "."); err != nil {
		return err
	}
	changed, err := e.hasStagedChanges(ctx, workTree)
	if err != nil {
		return err
	}
	if !changed {
		sha, err := e.revParse(ctx, workTree, "HEAD")
		if err != nil {
			return err
		}
		result.AfterSHA = sha
		result.Details["git_commit_created"] = false
		result.Details["git_push_performed"] = false
		result.Details["no_changes"] = true
		return nil
	}
	if err := e.run(ctx, result, workTree, "git", "commit", "-m", "Add ASSOPS config scaffold"); err != nil {
		return err
	}
	if err := e.run(ctx, result, workTree, "git", "push", "origin", "HEAD:refs/heads/"+defaultBranch); err != nil {
		return err
	}
	sha, _ := e.revParse(ctx, workTree, "HEAD")
	result.AfterSHA = sha
	result.Details["git_commit_created"] = true
	result.Details["git_push_performed"] = true
	result.Details["no_changes"] = false
	result.Details["remote_id"] = remote["id"]
	return nil
}

func configScaffoldMaterializedFiles(repo map[string]any) []map[string]any {
	repoName := cleanOptionalText(stringFromMap(repo, "name"))
	repoKey := cleanOptionalText(stringFromMap(repo, "repo_key"))
	if repoName == "" {
		repoName = "ASSOPS config repository"
	}
	if repoKey == "" {
		repoKey = "config"
	}
	files := []map[string]any{{
		"path": "README.md",
		"content": "# " + repoName + "\n\n" +
			"This repository stores environment configuration reviewed through ASSOPS.\n\n" +
			"- Keep real secrets outside Git.\n" +
			"- Use secrets.example.yaml files for shape only.\n" +
			"- Pin deployed versions to the reviewed config commit.\n",
	}}
	for _, env := range []string{"dev", "test", "prod"} {
		files = append(files,
			map[string]any{
				"path": "envs/" + env + "/values.yaml",
				"content": "repository_key: " + repoKey + "\n" +
					"environment: " + env + "\n" +
					"image:\n" +
					"  tag: \"\"\n" +
					"rollout:\n" +
					"  notes: \"\"\n",
			},
			map[string]any{
				"path": "envs/" + env + "/secrets.example.yaml",
				"content": "# Shape only. Store real secret values in the target secret manager.\n" +
					"secrets:\n" +
					"  example_key: \"REDACTED\"\n",
			},
			map[string]any{
				"path": "envs/" + env + "/README.md",
				"content": "# " + env + "\n\n" +
					"Review changes in ASSOPS before promoting this environment.\n",
			},
		)
	}
	return files
}

func configScaffoldContentLooksSensitive(content string) bool {
	lowered := strings.ToLower(content)
	for _, marker := range []string{"private key", "bearer ", "api_key:", "password:", "token:"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

func templateFileContent(file map[string]any) string {
	content, _ := file["content"].(string)
	return content
}
