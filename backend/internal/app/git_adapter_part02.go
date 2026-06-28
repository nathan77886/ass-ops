package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"path/filepath"
	"strings"
)

func (e *GitExecutor) Sync(ctx context.Context, db *gorm.DB, opID string) (*gitExecutionResult, error) {
	run, err := repoSyncRunMapForOperation(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	source, err := gitRemoteMapByID(ctx, db, strings.TrimSpace(fmt.Sprint(run["source_remote_id"])))
	if err != nil {
		return nil, fmt.Errorf("loading source remote: %w", err)
	}
	target, err := gitRemoteMapByID(ctx, db, strings.TrimSpace(fmt.Sprint(run["target_remote_id"])))
	if err != nil {
		return nil, fmt.Errorf("loading target remote: %w", err)
	}
	sourceURL := remoteURLFromRow(source)
	targetURL := remoteURLFromRow(target)
	if sourceURL == "" || targetURL == "" {
		return nil, fmt.Errorf("source and target remotes must have remote_url or urls")
	}
	result := &gitExecutionResult{Details: map[string]any{"source_remote_id": run["source_remote_id"], "target_remote_id": run["target_remote_id"]}}
	if err := e.validateExistingLocalBareRemote(ctx, result, source, sourceURL, "source"); err != nil {
		return result, err
	}
	if err := e.validateExistingLocalBareRemote(ctx, result, target, targetURL, "target"); err != nil {
		return result, err
	}
	defaultBranch := defaultBranchFromRow(source)
	refs := gitRefsFromInput(run["input"], defaultBranch)
	if len(refs.Branches) == 0 && len(refs.Tags) == 0 {
		refs.Branches = []string{defaultBranch}
	}

	repoDir, cleanup, err := e.newWorkDir("assops-sync-*")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	branchSHAs := map[string]string{}
	if err := e.run(ctx, result, repoDir, "git", "init", "--bare", "repo.git"); err != nil {
		return result, err
	}
	gitDir := filepath.Join(repoDir, "repo.git")
	if err := e.run(ctx, result, gitDir, "git", "remote", "add", "source", sourceURL); err != nil {
		return result, err
	}
	if err := e.run(ctx, result, gitDir, "git", "remote", "add", "target", targetURL); err != nil {
		return result, err
	}
	for _, branch := range refs.Branches {
		if !isSafeGitRefPart(branch) {
			return result, fmt.Errorf("unsafe branch ref %q", branch)
		}
		refspec := "refs/heads/" + branch + ":refs/heads/" + branch
		if err := e.run(ctx, result, gitDir, "git", "fetch", "source", refspec); err != nil {
			return result, err
		}
		if err := e.run(ctx, result, gitDir, "git", "push", "target", "refs/heads/"+branch+":refs/heads/"+branch); err != nil {
			return result, err
		}
		sha, _ := e.revParse(ctx, gitDir, "refs/heads/"+branch)
		if sha != "" {
			branchSHAs[branch] = sha
			if branch == defaultBranch {
				result.AfterSHA = sha
			} else if result.AfterSHA == "" {
				result.AfterSHA = sha
			}
		}
	}
	for _, tag := range refs.Tags {
		if tag == "*" {
			if err := e.run(ctx, result, gitDir, "git", "fetch", "source", "--tags"); err != nil {
				return result, err
			}
			if err := e.run(ctx, result, gitDir, "git", "push", "target", "--tags"); err != nil {
				return result, err
			}
			continue
		}
		if !isSafeGitRefPart(tag) {
			return result, fmt.Errorf("unsafe tag ref %q", tag)
		}
		refspec := "refs/tags/" + tag + ":refs/tags/" + tag
		if err := e.run(ctx, result, gitDir, "git", "fetch", "source", refspec); err != nil {
			return result, err
		}
		if err := e.run(ctx, result, gitDir, "git", "push", "target", refspec); err != nil {
			return result, err
		}
	}
	result.Details["branches"] = refs.Branches
	result.Details["tags"] = refs.Tags
	result.Details["branch_shas"] = branchSHAs
	return result, nil
}

func (e *GitExecutor) RefreshRemoteRefs(ctx context.Context, db *gorm.DB, opID string) (*gitExecutionResult, error) {
	op, err := operationRunByID(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	remoteID := strings.TrimSpace(op.GitRemoteID.String)
	if remoteID == "" || remoteID == "<nil>" {
		input := mapFromAny(op.Input.Data)
		remoteID = strings.TrimSpace(fmt.Sprint(input["remote_id"]))
	}
	if remoteID == "" || remoteID == "<nil>" {
		return nil, fmt.Errorf("git remote id is required")
	}
	remote, err := gitRemoteMapByID(ctx, db, remoteID)
	if err != nil {
		return nil, fmt.Errorf("loading remote: %w", err)
	}
	remoteURL := remoteURLFromRow(remote)
	if remoteURL == "" {
		return nil, fmt.Errorf("remote must have remote_url or urls")
	}
	input := mapFromAny(op.Input.Data)
	defaultBranch := strings.TrimSpace(firstNonEmptyString(stringFromMap(input, "branch"), defaultBranchFromRow(remote)))
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	if !isSafeGitRefPart(defaultBranch) {
		return nil, fmt.Errorf("unsafe branch ref %q", defaultBranch)
	}
	tagName := strings.TrimSpace(stringFromMap(input, "tag"))
	if tagName != "" && !isSafeGitRefPart(tagName) {
		return nil, fmt.Errorf("unsafe tag ref %q", tagName)
	}
	result := &gitExecutionResult{Details: map[string]any{"remote_id": remoteID, "branch": defaultBranch, "tag": tagName}}
	if err := e.validateExistingLocalBareRemote(ctx, result, remote, remoteURL, "remote"); err != nil {
		return result, err
	}

	repoDir, cleanup, err := e.newWorkDir("assops-ref-refresh-*")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := e.run(ctx, result, repoDir, "git", "init", "--bare", "repo.git"); err != nil {
		return result, err
	}
	gitDir := filepath.Join(repoDir, "repo.git")
	if err := e.run(ctx, result, gitDir, "git", "remote", "add", "origin", remoteURL); err != nil {
		return result, err
	}
	refspec := "refs/heads/" + defaultBranch + ":refs/heads/" + defaultBranch
	if tagName != "" {
		refspec = "refs/tags/" + tagName + ":refs/tags/" + tagName
	}
	if err := e.run(ctx, result, gitDir, "git", "fetch", "--depth=1", "origin", refspec); err != nil {
		return result, err
	}
	sha, revParseErr := e.revParse(ctx, gitDir, "FETCH_HEAD")
	if revParseErr != nil {
		result.Details["rev_parse_error"] = truncateProviderError(revParseErr.Error(), providerRunErrorLimit)
	}
	if sha == "" {
		targetRef := "refs/heads/" + defaultBranch
		if tagName != "" {
			targetRef = "refs/tags/" + tagName
		}
		sha, revParseErr = e.revParse(ctx, gitDir, targetRef)
		if revParseErr != nil {
			result.Details["target_rev_parse_error"] = truncateProviderError(revParseErr.Error(), providerRunErrorLimit)
		}
	}
	result.AfterSHA = sha
	result.Details["after_sha"] = sha
	return result, nil
}

func (e *GitExecutor) Tag(ctx context.Context, db *gorm.DB, opID string) (*gitExecutionResult, error) {
	run, err := repoTagRunMapForOperation(ctx, db, opID)
	if err != nil {
		return nil, err
	}
	target, err := gitRemoteMapByID(ctx, db, strings.TrimSpace(fmt.Sprint(run["target_remote_id"])))
	if err != nil {
		return nil, fmt.Errorf("loading target remote: %w", err)
	}
	targetURL := remoteURLFromRow(target)
	if targetURL == "" {
		return nil, fmt.Errorf("target remote must have remote_url or urls")
	}
	tagName := strings.TrimSpace(fmt.Sprint(run["tag_name"]))
	if !isSafeGitRefPart(tagName) {
		return nil, fmt.Errorf("unsafe tag ref %q", tagName)
	}
	input := mapFromAny(run["input"])
	branch := strings.TrimSpace(fmt.Sprint(input["branch"]))
	if branch == "" || branch == "<nil>" {
		branch = defaultBranchFromRow(target)
	}
	if !isSafeGitRefPart(branch) {
		return nil, fmt.Errorf("unsafe branch ref %q", branch)
	}
	result := &gitExecutionResult{Details: map[string]any{"target_remote_id": run["target_remote_id"], "tag_name": tagName}}
	if err := e.validateExistingLocalBareRemote(ctx, result, target, targetURL, "target"); err != nil {
		return result, err
	}

	repoDir, cleanup, err := e.newWorkDir("assops-tag-*")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := e.run(ctx, result, repoDir, "git", "init", "repo"); err != nil {
		return result, err
	}
	workTree := filepath.Join(repoDir, "repo")
	if err := e.run(ctx, result, workTree, "git", "remote", "add", "target", targetURL); err != nil {
		return result, err
	}
	if err := e.run(ctx, result, workTree, "git", "fetch", "target", "refs/heads/"+branch+":refs/remotes/target/"+branch, "--tags"); err != nil {
		return result, err
	}
	targetRef := strings.TrimSpace(fmt.Sprint(run["target_sha"]))
	explicitTargetSHA := targetRef != "" && targetRef != "<nil>"
	if !explicitTargetSHA {
		targetRef = "refs/remotes/target/" + branch
	} else if !isFullHexSHA(targetRef) {
		return result, fmt.Errorf("target_sha must be a full commit SHA")
	}
	if !isSafeGitRefPart(targetRef) {
		return result, fmt.Errorf("unsafe target ref %q", targetRef)
	}
	message := strings.TrimSpace(fmt.Sprint(run["tag_message"]))
	if message == "" || message == "<nil>" {
		if err := e.run(ctx, result, workTree, "git", "tag", tagName, targetRef); err != nil {
			return result, err
		}
	} else {
		if err := e.run(ctx, result, workTree, "git", "tag", "-a", tagName, targetRef, "-m", message); err != nil {
			return result, err
		}
	}
	if err := e.run(ctx, result, workTree, "git", "push", "target", "refs/tags/"+tagName+":refs/tags/"+tagName); err != nil {
		return result, err
	}
	if peeledSHA, err := e.revParse(ctx, workTree, "refs/tags/"+tagName+"^{}"); err == nil && peeledSHA != "" {
		result.AfterSHA = peeledSHA
	} else {
		result.AfterSHA, _ = e.revParse(ctx, workTree, "refs/tags/"+tagName)
	}
	return result, nil
}
