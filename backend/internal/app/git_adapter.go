package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type commandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, string, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=ASSOPS",
		"GIT_AUTHOR_EMAIL=assops@local",
		"GIT_COMMITTER_NAME=ASSOPS",
		"GIT_COMMITTER_EMAIL=assops@local",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

type GitExecutor struct {
	Runner            commandRunner
	HTTPClient        *http.Client
	WorkDir           string
	LocalBareBaseDirs []string
}

type gitExecutionResult struct {
	Stdout   string
	Stderr   string
	AfterSHA string
	Details  map[string]any
}

const (
	providerDiagnosticErrorLimit = 240
	providerRunErrorLimit        = 512
)

type gitRefs struct {
	Branches []string
	Tags     []string
}

func NewGitExecutor(workDir string) *GitExecutor {
	return &GitExecutor{Runner: execCommandRunner{}, WorkDir: workDir}
}

func (e *GitExecutor) Sync(ctx context.Context, db sqlx.ExtContext, opID string) (*gitExecutionResult, error) {
	run, err := queryOne(ctx, db, `
		SELECT rsr.*, opr.input
		FROM repo_sync_runs rsr
		JOIN operation_runs opr ON opr.id=rsr.operation_run_id
		WHERE rsr.operation_run_id=$1
		LIMIT 1`, opID)
	if err != nil {
		return nil, err
	}
	source, err := queryOne(ctx, db, "SELECT * FROM git_remotes WHERE id=$1", run["source_remote_id"])
	if err != nil {
		return nil, fmt.Errorf("loading source remote: %w", err)
	}
	target, err := queryOne(ctx, db, "SELECT * FROM git_remotes WHERE id=$1", run["target_remote_id"])
	if err != nil {
		return nil, fmt.Errorf("loading target remote: %w", err)
	}
	sourceURL := remoteURLFromRow(source)
	targetURL := remoteURLFromRow(target)
	if sourceURL == "" || targetURL == "" {
		return nil, fmt.Errorf("source and target remotes must have remote_url or urls")
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
	result := &gitExecutionResult{Details: map[string]any{"source_remote_id": run["source_remote_id"], "target_remote_id": run["target_remote_id"]}}
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

func (e *GitExecutor) Tag(ctx context.Context, db sqlx.ExtContext, opID string) (*gitExecutionResult, error) {
	run, err := queryOne(ctx, db, `
		SELECT rtr.*, opr.input, gr.default_branch
		FROM repo_tag_runs rtr
		JOIN operation_runs opr ON opr.id=rtr.operation_run_id
		JOIN git_remotes gr ON gr.id=rtr.target_remote_id
		WHERE rtr.operation_run_id=$1
		LIMIT 1`, opID)
	if err != nil {
		return nil, err
	}
	target, err := queryOne(ctx, db, "SELECT * FROM git_remotes WHERE id=$1", run["target_remote_id"])
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

	repoDir, cleanup, err := e.newWorkDir("assops-tag-*")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	result := &gitExecutionResult{Details: map[string]any{"target_remote_id": run["target_remote_id"], "tag_name": tagName}}
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
	result.AfterSHA, _ = e.revParse(ctx, workTree, "refs/tags/"+tagName)
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

func templateFileContent(file map[string]any) string {
	content, _ := file["content"].(string)
	return content
}

func (e *GitExecutor) provisionExternalTemplateRepository(ctx context.Context, result *gitExecutionResult, repo, remote map[string]any) error {
	spec, ok := buildExternalTemplateProviderSpec(repo, remote)
	if !ok {
		result.Details["reason"] = "external template remote is missing provider configuration"
		return nil
	}
	if spec.Token == "" {
		result.Details["reason"] = "external template provider token is not configured"
		result.Details["provider_type"] = spec.Provider
		result.Details["token_configured"] = false
		result.Details["repository_reconciliation"] = templateRepositoryReconciliation("missing_token", repo, remote, defaultBranchFromRow(repo), 0)
		return nil
	}
	if err := validateTemplateProviderURL(ctx, spec.CreateURL); err != nil {
		return fmt.Errorf("unsafe %s provider API URL: %w", spec.Provider, err)
	}
	setTemplateProviderDiagnostics(result, spec, 0, "")
	client := e.HTTPClient
	if client == nil {
		client = newTemplateProviderHTTPClient()
	}
	requestBody := map[string]any{
		"name":        spec.RepositoryName,
		"description": spec.Description,
		"private":     spec.Private,
		"auto_init":   false,
	}
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.CreateURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	switch spec.Provider {
	case "github":
		req.Header.Set("Authorization", "Bearer "+spec.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	case "gitea":
		req.Header.Set("Authorization", "token "+spec.Token)
		req.Header.Set("Accept", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		setTemplateProviderDiagnostics(result, spec, 0, err.Error())
		return fmt.Errorf("creating %s repository: %w", spec.Provider, err)
	}
	defer res.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if templateProviderAlreadyExists(res.StatusCode, responseBody) {
		setTemplateProviderDiagnostics(result, spec, res.StatusCode, "already exists")
		result.Details["provisioned"] = true
		result.Details["already_provisioned"] = true
		result.Details["provider_type"] = spec.Provider
		result.Details["remote_id"] = remote["id"]
		result.Details["repository_name"] = spec.RepositoryName
		result.Details["owner"] = spec.Owner
		result.Details["remote_url"] = remoteURLFromRow(remote)
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		setTemplateProviderDiagnostics(result, spec, res.StatusCode, providerErrorMessage(responseBody))
		return fmt.Errorf("creating %s repository returned %s%s", spec.Provider, res.Status, providerErrorSuffix(responseBody))
	}
	payload := map[string]any{}
	_ = json.Unmarshal(responseBody, &payload)
	remoteURL := firstNonEmptyString(stringFromMap(payload, "ssh_url"), stringFromMap(payload, "clone_url"), remoteURLFromRow(remote))
	result.Details["provisioned"] = true
	result.Details["provider_type"] = spec.Provider
	result.Details["remote_id"] = remote["id"]
	result.Details["repository_name"] = spec.RepositoryName
	result.Details["owner"] = spec.Owner
	result.Details["remote_url"] = remoteURL
	result.Details["web_url"] = firstNonEmptyString(stringFromMap(payload, "html_url"), stringFromMap(payload, "web_url"))
	return nil
}

func setTemplateProviderDiagnostics(result *gitExecutionResult, spec externalTemplateProviderConfig, status int, message string) {
	if result == nil {
		return
	}
	if result.Details == nil {
		result.Details = map[string]any{}
	}
	result.Details["provider_type"] = spec.Provider
	result.Details["repository_name"] = spec.RepositoryName
	result.Details["owner"] = spec.Owner
	result.Details["token_configured"] = spec.Token != ""
	if status > 0 {
		result.Details["provider_status"] = status
	}
	if message = truncateProviderError(message, providerDiagnosticErrorLimit); message != "" {
		result.Details["provider_error"] = message
	}
}

func templateRepositoryReconciliation(kind string, repo, remote map[string]any, defaultBranch string, fileCount int) map[string]any {
	provider := strings.ToLower(strings.TrimSpace(firstNonEmptyString(
		stringFromMap(remote, "provider_type"),
		stringFromMap(remote, "kind"),
	)))
	branchStrategy := templateProtectedBranchStrategy(repo, remote, defaultBranch)
	credentialStrategy := templateProviderReviewCredentialStrategy(provider, remote)
	reviewReadiness := templateProviderReviewReadiness(kind, provider, branchStrategy, credentialStrategy)
	summary := map[string]any{
		"kind":                      kind,
		"provider_type":             provider,
		"remote_id":                 remote["id"],
		"repository_key":            repo["repo_key"],
		"default_branch":            defaultBranch,
		"file_count":                fileCount,
		"starter_push_state":        "skipped",
		"credential_strategy":       credentialStrategy,
		"provider_review_readiness": reviewReadiness,
	}
	switch kind {
	case "existing_repository":
		summary["guardrail"] = "existing_repository_push_blocked"
		summary["action_required"] = "Review the existing repository contents before allowing ASSOPS to push starter files."
		summary["retry_after"] = "Set allow_existing_repository_push only after the repository is confirmed safe to overwrite or extend."
	case "protected_branch":
		summary["guardrail"] = "protected_branch_push_blocked"
		summary["action_required"] = "Review provider branch protection and choose a provider-specific reconciliation path."
		summary["retry_after"] = "Configure a branch strategy or set allow_protected_branch_push only after branch protection is approved."
		if stringFromMap(branchStrategy, "strategy_status") == "planned" {
			summary["branch_strategy"] = branchStrategy
			summary["action_required"] = templateBranchStrategyActionRequired(branchStrategy, defaultBranch)
			summary["retry_after"] = "Retry after the proposed branch is reviewed and merged, or enable allow_protected_branch_push after approval."
		} else if len(branchStrategy) > 0 {
			summary["branch_strategy"] = branchStrategy
		}
	case "missing_token":
		summary["guardrail"] = "provider_token_missing"
		summary["action_required"] = "Rotate the provider account to a configured token environment and run the provider health check."
		summary["retry_after"] = "Retry after the provider account check succeeds."
	default:
		summary["guardrail"] = "manual_reconciliation_required"
		summary["action_required"] = "Review template remote metadata before retrying repository provisioning."
		summary["retry_after"] = "Retry after the missing provider condition is fixed."
	}
	return summary
}

func templateProviderReviewCredentialStrategy(provider string, remote map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	metadata := mapFromAny(remote["metadata"])
	tokenEnv := firstNonEmptyString(stringFromMap(metadata, "token_env"), stringFromMap(metadata, "provider_account_env"), defaultTemplateProviderTokenEnv(provider))
	tokenEnvConfigured := strings.TrimSpace(tokenEnv) != "" && safeTemplateProviderTokenEnv(provider, tokenEnv)
	return map[string]any{
		"mode":                      map[bool]string{true: "provider_account_token_env", false: "template_remote_token_env"}[templateRemoteUsesProviderAccount(remote, metadata)],
		"provider_account_attached": templateRemoteUsesProviderAccount(remote, metadata),
		"token_env_configured":      tokenEnvConfigured,
		"token_env_present":         tokenEnvConfigured && strings.TrimSpace(os.Getenv(tokenEnv)) != "",
		"token_stored":              false,
		"external_call_made":        false,
	}
}

func templateProviderReviewReadiness(kind, provider string, branchStrategy map[string]any, credentialStrategies ...map[string]any) map[string]any {
	credentialStrategy := firstProviderReviewCredentialStrategy(credentialStrategies...)
	readiness := map[string]any{
		"status":             "blocked",
		"provider_type":      provider,
		"execution_enabled":  false,
		"external_call_made": false,
		"branch_creation":    "disabled",
		"review_request":     "disabled",
		"message":            "Provider branch and review execution are disabled in this first version.",
	}
	switch kind {
	case "protected_branch":
		if stringFromMap(branchStrategy, "strategy_status") == "planned" {
			readiness["status"] = "planned"
			readiness["mode"] = branchStrategy["mode"]
			readiness["proposed_branch"] = branchStrategy["proposed_branch"]
			readiness["target_branch"] = branchStrategy["target_branch"]
			readiness["branch_creation"] = "locally_planned"
			readiness["review_request"] = "locally_planned"
			readiness["provider_next_action"] = branchStrategy["provider_next_action"]
			readiness["execution_plan"] = templateProviderReviewExecutionPlan(provider, branchStrategy, credentialStrategy)
			readiness["message"] = "Local branch/review plan is ready; provider API-backed branch creation and PR/MR execution remain disabled."
			return readiness
		}
		readiness["message"] = "Configure a supported branch strategy before provider review execution can be planned."
	case "existing_repository":
		readiness["message"] = "Review existing repository contents before planning provider branch/review execution."
	case "missing_token":
		readiness["message"] = "Provider token readiness is blocked; rotate and health-check the provider account before review execution."
	default:
		readiness["message"] = "Manual repository reconciliation is required before provider review execution can be planned."
	}
	return readiness
}

const templateProviderReviewExecuteApprovalAction = "project_template.provider_review.execute"

func templateProviderReviewExecutionPlan(provider string, branchStrategy map[string]any, credentialStrategies ...map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(firstNonEmptyString(provider, stringFromMap(branchStrategy, "provider_type"))))
	credentialStrategy := firstProviderReviewCredentialStrategy(credentialStrategies...)
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(branchStrategy["mode"])))
	sourceBranch := strings.TrimSpace(fmt.Sprint(branchStrategy["proposed_branch"]))
	targetBranch := strings.TrimSpace(fmt.Sprint(branchStrategy["target_branch"]))
	reviewKind := templateProviderReviewKind(provider, mode)
	executionRequest := templateProviderReviewExecutionRequest(provider, reviewKind, sourceBranch, targetBranch)
	guardrail := templateProviderReviewExecutionGuardrail(provider, reviewKind, sourceBranch, targetBranch, false)
	// Starter files are staged later when the approval payload is built.
	apiRequestPlan := templateProviderReviewAPIRequestPlan(provider, reviewKind, sourceBranch, targetBranch, nil)
	reconciliation := templateProviderReviewExecutionReconciliation(provider, reviewKind, nil, guardrail, apiRequestPlan, credentialStrategy)
	targetSummary := providerReviewExecutionTargetSummary(provider, reviewKind, apiRequestPlan, nil, reconciliation)
	steps := []map[string]any{
		{
			"name":      "create_branch",
			"status":    "planned",
			"provider":  provider,
			"from":      targetBranch,
			"to":        sourceBranch,
			"api_call":  false,
			"guardrail": "provider API execution disabled",
		},
		{
			"name":       "commit_starter_files",
			"status":     "planned",
			"branch":     sourceBranch,
			"api_call":   false,
			"repository": "external provider repository",
			"guardrail":  "external repository mutation disabled",
		},
		{
			"name":          "open_review",
			"status":        "planned",
			"provider":      provider,
			"review_kind":   reviewKind,
			"source_branch": sourceBranch,
			"target_branch": targetBranch,
			"api_call":      false,
			"guardrail":     "provider review request execution disabled",
		},
	}
	return map[string]any{
		"mode":                           "dry_run",
		"provider_type":                  provider,
		"strategy_mode":                  mode,
		"review_kind":                    reviewKind,
		"source_branch":                  sourceBranch,
		"target_branch":                  targetBranch,
		"execution_enabled":              false,
		"external_call_made":             false,
		"requires_approval":              true,
		"approval_action":                templateProviderReviewExecuteApprovalAction,
		"provider_api_mutation":          "disabled",
		"execution_request":              executionRequest,
		"execution_guardrail":            guardrail,
		"credential_strategy":            credentialStrategy,
		"provider_api_request_plan":      apiRequestPlan,
		"provider_review_reconciliation": reconciliation,
		"provider_review_target_summary": targetSummary,
		"steps":                          steps,
		"message":                        "Provider review execution request is prepared for approval, but branch creation, starter-file commits, and PR/MR creation remain disabled.",
	}
}

func providerReviewExecutionTargetSummary(provider, reviewKind string, apiRequestPlan, starterFilePayload, reconciliation map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "source_branch"))
	targetBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "target_branch"))
	branchRefsReady := sourceBranch != "" &&
		targetBranch != "" &&
		isSafeGitRefPart(sourceBranch) &&
		isSafeGitRefPart(targetBranch)
	starterReady := starterFilePayloadReady(starterFilePayload)
	planReady := fmt.Sprint(apiRequestPlan["status"]) == "ready"
	fileCount := intFromAny(starterFilePayload["file_count"], intFromAny(apiRequestPlan["file_count"], 0))
	operations := make([]map[string]any, 0, len(mapSliceFromAny(apiRequestPlan["operations"])))
	for _, operation := range mapSliceFromAny(apiRequestPlan["operations"]) {
		operations = append(operations, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"payload_shape":         cleanOptionalText(stringFromMap(operation, "payload_shape")),
			"status":                "planned",
			"api_call":              false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		})
	}
	blockedReasons := append([]string{}, stringSliceFromAny(apiRequestPlan["blocked_reasons"])...)
	blockedSeen := map[string]bool{}
	for _, reason := range blockedReasons {
		blockedSeen[reason] = true
	}
	for _, reason := range stringSliceFromAny(reconciliation["blocked_reasons"]) {
		if reason != "" && !blockedSeen[reason] {
			blockedReasons = append(blockedReasons, reason)
			blockedSeen[reason] = true
		}
	}
	status := "blocked"
	if branchRefsReady && starterReady && planReady {
		status = "adapter_blocked"
		if stringFromMap(reconciliation, "adapter_status") == "planned" {
			status = "mutation_blocked"
		}
	}
	return map[string]any{
		"status":                          status,
		"mode":                            "redacted_execution_target_summary",
		"provider_type":                   provider,
		"review_kind":                     reviewKind,
		"source_branch":                   sourceBranch,
		"target_branch":                   targetBranch,
		"branch_refs_ready":               branchRefsReady,
		"starter_file_payload_ready":      starterReady,
		"provider_api_request_ready":      planReady,
		"file_count":                      fileCount,
		"operation_count":                 len(operations),
		"operations":                      operations,
		"adapter_status":                  cleanOptionalText(stringFromMap(reconciliation, "adapter_status")),
		"blocked_reasons":                 blockedReasons,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"payload_redacted":                true,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_file_content":           false,
		"idempotency_key_included":        false,
		"requires_persisted_attempt":      true,
		"requires_response_diagnostics":   true,
		"requires_provider_api_adapter":   true,
		"requires_adapter_mutation_armed": true,
		"requires_operator_review":        true,
		"future_adapter_input_boundary":   "branch_ref_commit_review_request",
		"adapter_mutation_currently_off":  true,
	}
}

func templateProviderReviewAPIRequestPlan(provider, reviewKind, sourceBranch, targetBranch string, starterFilePayload map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch = strings.TrimSpace(sourceBranch)
	targetBranch = strings.TrimSpace(targetBranch)
	fileCount := intFromAny(starterFilePayload["file_count"], 0)
	ready := sourceBranch != "" &&
		targetBranch != "" &&
		isSafeGitRefPart(sourceBranch) &&
		isSafeGitRefPart(targetBranch) &&
		starterFilePayloadReady(starterFilePayload)
	status := "blocked"
	blockedReasons := []string{}
	if sourceBranch == "" || targetBranch == "" || !isSafeGitRefPart(sourceBranch) || !isSafeGitRefPart(targetBranch) {
		blockedReasons = append(blockedReasons, "review_branches_valid")
	}
	if !starterFilePayloadReady(starterFilePayload) {
		blockedReasons = append(blockedReasons, "starter_file_payload_staged")
	}
	if ready {
		status = "ready"
	}
	return map[string]any{
		"status":                 status,
		"mode":                   "redacted_request_plan",
		"provider_type":          provider,
		"review_kind":            reviewKind,
		"source_branch":          sourceBranch,
		"target_branch":          targetBranch,
		"file_count":             fileCount,
		"payload_redacted":       true,
		"contains_token":         false,
		"contains_file_content":  false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        blockedReasons,
		"operations": []map[string]any{
			{
				"name":                  "create_branch_ref",
				"method":                "POST",
				"endpoint_key":          providerReviewEndpointKey(provider, "create_branch_ref"),
				"payload_shape":         "ref_from_target_branch",
				"payload_redacted":      true,
				"contains_token":        false,
				"contains_file_content": false,
				"api_call":              false,
			},
			{
				"name":                  "commit_starter_files",
				"method":                "PUT",
				"endpoint_key":          providerReviewEndpointKey(provider, "commit_files"),
				"payload_shape":         "content_redacted_file_batch",
				"file_count":            fileCount,
				"payload_redacted":      true,
				"contains_token":        false,
				"contains_file_content": false,
				"api_call":              false,
			},
			{
				"name":                  "open_review_request",
				"method":                "POST",
				"endpoint_key":          providerReviewEndpointKey(provider, "open_review"),
				"payload_shape":         reviewKind,
				"payload_redacted":      true,
				"contains_token":        false,
				"contains_file_content": false,
				"api_call":              false,
			},
		},
	}
}

func templateProviderReviewExecutionReconciliation(provider, reviewKind string, starterFilePayload, guardrail, apiRequestPlan map[string]any, credentialStrategies ...map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	credentialStrategy := firstProviderReviewCredentialStrategy(credentialStrategies...)
	adapterContract := providerReviewAdapterContract(provider, reviewKind, apiRequestPlan, starterFilePayload)
	providerSupported := provider == "github" || provider == "gitea"
	starterReady := starterFilePayloadReady(starterFilePayload)
	planReady := fmt.Sprint(apiRequestPlan["status"]) == "ready"
	credentialConfigured := boolValueFromAny(credentialStrategy["token_env_configured"])
	credentialPresent := boolValueFromAny(credentialStrategy["token_env_present"])
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	adapterReady := adapterStatus == "planned"
	mutationArmed := false
	executionEnabledConfig := boolValueFromAny(guardrail["execution_enabled_config"])
	requestEnvelopes := providerReviewAdapterRequestEnvelopes(provider, reviewKind, apiRequestPlan, starterFilePayload)
	adapterRehearsal := providerReviewAdapterRehearsal(provider, reviewKind, adapterStatus, credentialStrategy, requestEnvelopes)
	mutationArmingPlan := providerReviewMutationArmingPlan(provider, reviewKind, executionEnabledConfig, mutationArmed, adapterRehearsal)
	gates := []map[string]any{
		{
			"gate":              "provider_supported",
			"status":            map[bool]string{true: "ready", false: "blocked"}[providerSupported],
			"provider_type":     provider,
			"message":           "Provider review adapters are only planned for GitHub and Gitea.",
			"sensitive_payload": false,
		},
		{
			"gate":              "starter_file_payload_staged",
			"status":            map[bool]string{true: "ready", false: "blocked"}[starterReady],
			"message":           "Starter-file payload must be staged as a content-redacted summary before provider review execution.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_api_request_plan_ready",
			"status":            map[bool]string{true: "ready", false: "blocked"}[planReady],
			"message":           "Provider API request plan must have valid branches and staged starter-file payload metadata.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_execution_enabled",
			"status":            map[bool]string{true: "ready", false: "blocked"}[executionEnabledConfig],
			"required_config":   "ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION",
			"message":           "Provider review execution must be explicitly enabled before provider API mutation can be considered.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_credential_configured",
			"status":            map[bool]string{true: "ready", false: "blocked"}[credentialConfigured],
			"mode":              cleanOptionalText(stringFromMap(credentialStrategy, "mode")),
			"message":           "Provider account token environment must be configured using an allowed ASSOPS provider token env name.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_token_env_present",
			"status":            map[bool]string{true: "ready", false: "blocked"}[credentialPresent],
			"message":           "Provider token environment variable must be present at runtime before provider API mutation can be enabled.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_api_adapter",
			"status":            map[bool]string{true: "ready", false: "blocked"}[adapterReady],
			"provider_type":     provider,
			"review_kind":       reviewKind,
			"adapter_status":    adapterStatus,
			"message":           "Provider branch creation, starter-file commit, and PR/MR adapter contract is registered for supported providers.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_mutation_armed",
			"status":            map[bool]string{true: "ready", false: "blocked"}[mutationArmed],
			"provider_type":     provider,
			"review_kind":       reviewKind,
			"adapter_status":    adapterStatus,
			"message":           "Provider API mutation remains disabled until the execution adapter is explicitly armed after rehearsal.",
			"sensitive_payload": false,
		},
	}
	blocked := make([]string, 0, len(gates))
	for _, gate := range gates {
		if gate["status"] != "ready" {
			blocked = append(blocked, stringFromMap(gate, "gate"))
		}
	}
	return map[string]any{
		"status":                 map[bool]string{true: "ready", false: "blocked"}[executionEnabledConfig && providerSupported && starterReady && planReady && credentialConfigured && credentialPresent && adapterReady && mutationArmed],
		"mode":                   "preflight_reconciliation",
		"provider_type":          provider,
		"review_kind":            reviewKind,
		"credential_strategy":    sanitizedProviderReviewCredentialStrategy(credentialStrategy),
		"adapter_contract":       adapterContract,
		"request_envelopes":      requestEnvelopes,
		"adapter_rehearsal":      adapterRehearsal,
		"mutation_arming_plan":   mutationArmingPlan,
		"response_diagnostics":   providerReviewAdapterResponseDiagnostics(provider, reviewKind),
		"idempotency_plan":       providerReviewAdapterIdempotencyPlan(provider, reviewKind),
		"adapter_status":         adapterStatus,
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        blocked,
		"gates":                  gates,
		"operations": []map[string]any{
			{
				"name":               "create_branch_ref",
				"endpoint_key":       providerReviewEndpointKey(provider, "create_branch_ref"),
				"status":             "planned",
				"blocked_reason":     "provider_review_mutation_armed",
				"external_call_made": false,
			},
			{
				"name":               "commit_starter_files",
				"endpoint_key":       providerReviewEndpointKey(provider, "commit_files"),
				"status":             "planned",
				"blocked_reason":     "provider_review_mutation_armed",
				"external_call_made": false,
			},
			{
				"name":               "open_review_request",
				"endpoint_key":       providerReviewEndpointKey(provider, "open_review"),
				"status":             "planned",
				"blocked_reason":     "provider_review_mutation_armed",
				"external_call_made": false,
			},
		},
		"next_step": "Rehearse and arm the provider review execution adapter before enabling provider API mutation.",
	}
}

func providerReviewMutationArmingPlan(provider, reviewKind string, executionEnabledConfig, mutationArmed bool, adapterRehearsal map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	rehearsalReady := stringFromMap(adapterRehearsal, "status") == "ready" && boolValueFromAny(adapterRehearsal["mutation_arming_candidate"])
	blockedReasons := []string{}
	if !executionEnabledConfig {
		blockedReasons = append(blockedReasons, "provider_review_execution_enabled")
	}
	if !rehearsalReady {
		blockedReasons = append(blockedReasons, "provider_review_adapter_rehearsal")
	}
	if !mutationArmed {
		blockedReasons = append(blockedReasons, "provider_review_mutation_armed")
	}
	status := "blocked"
	if executionEnabledConfig && rehearsalReady && !mutationArmed {
		status = "ready_to_arm"
	}
	if mutationArmed && executionEnabledConfig && rehearsalReady {
		status = "armed"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_mutation_arming_plan",
		"provider_type":                  provider,
		"review_kind":                    reviewKind,
		"required_config":                "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
		"execution_enabled_config":       executionEnabledConfig,
		"adapter_rehearsal_ready":        rehearsalReady,
		"mutation_armed":                 mutationArmed,
		"blocked_reasons":                blockedReasons,
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_adapter_rehearsal":     true,
		"adapter_mutation_currently_off": true,
		"next_step":                      "Only arm provider review mutation after rehearsal evidence, operator approval, and environment-specific rollout controls are reviewed.",
	}
}

func providerReviewAdapterRehearsal(provider, reviewKind, adapterStatus string, credentialStrategy map[string]any, requestEnvelopes []map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	credentialConfigured := boolValueFromAny(credentialStrategy["token_env_configured"])
	credentialPresent := boolValueFromAny(credentialStrategy["token_env_present"])
	readyCount := 0
	blockedCount := 0
	blockedReasons := []string{}
	seenBlockedReasons := map[string]bool{}
	addBlockedReason := func(reason string) {
		reason = providerReviewRehearsalBlockedReason(reason)
		if reason == "" || seenBlockedReasons[reason] {
			return
		}
		seenBlockedReasons[reason] = true
		blockedReasons = append(blockedReasons, reason)
	}
	if adapterStatus != "planned" {
		addBlockedReason("provider_review_api_adapter")
	}
	if !credentialConfigured {
		addBlockedReason("provider_credential_configured")
	}
	if !credentialPresent {
		addBlockedReason("provider_token_env_present")
	}
	operations := make([]map[string]any, 0, len(requestEnvelopes))
	for _, envelope := range requestEnvelopes {
		status := "ready"
		operationBlockedReasons := []string{}
		operationSeen := map[string]bool{}
		for _, readiness := range mapSliceFromAny(envelope["readiness"]) {
			if stringFromMap(readiness, "status") == "ready" {
				continue
			}
			reason := providerReviewRehearsalBlockedReason(stringFromMap(readiness, "evidence"))
			if reason == "" || operationSeen[reason] {
				continue
			}
			operationSeen[reason] = true
			operationBlockedReasons = append(operationBlockedReasons, reason)
			addBlockedReason(reason)
		}
		if len(operationBlockedReasons) > 0 {
			status = "blocked"
			blockedCount++
		} else {
			readyCount++
		}
		operations = append(operations, map[string]any{
			"name":                   cleanOptionalText(stringFromMap(envelope, "name")),
			"endpoint_key":           cleanOptionalText(stringFromMap(envelope, "endpoint_key")),
			"status":                 status,
			"blocked_reasons":        operationBlockedReasons,
			"external_call_made":     false,
			"provider_api_call_made": false,
			"provider_api_mutation":  "disabled",
		})
	}
	status := "blocked"
	if adapterStatus == "planned" && credentialConfigured && credentialPresent && blockedCount == 0 && len(requestEnvelopes) > 0 {
		status = "ready"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_adapter_rehearsal",
		"provider_type":                  provider,
		"review_kind":                    reviewKind,
		"adapter_status":                 adapterStatus,
		"operation_count":                len(operations),
		"ready_operation_count":          readyCount,
		"blocked_operation_count":        blockedCount,
		"blocked_reasons":                blockedReasons,
		"operations":                     operations,
		"mutation_arming_candidate":      status == "ready",
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"adapter_mutation_currently_off": true,
	}
}

func providerReviewRehearsalBlockedReason(value string) string {
	switch strings.TrimSpace(value) {
	case "provider_review_api_adapter":
		return "provider_review_api_adapter"
	case "provider_credential_configured":
		return "provider_credential_configured"
	case "provider_token_env_present":
		return "provider_token_env_present"
	case "provider_api_request_plan_ready":
		return "provider_api_request_plan_ready"
	case "review_branch_refs_valid", "review_branches_valid":
		return "review_branches_valid"
	case "starter_file_payload_staged":
		return "starter_file_payload_staged"
	default:
		return ""
	}
}

func providerReviewAdapterContract(provider, reviewKind string, requestInputs ...map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	supported := provider == "github" || provider == "gitea"
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	apiRequestPlan := map[string]any{}
	starterFilePayload := map[string]any{}
	if len(requestInputs) > 0 {
		apiRequestPlan = requestInputs[0]
	}
	if len(requestInputs) > 1 {
		starterFilePayload = requestInputs[1]
	}
	return map[string]any{
		"status":                map[bool]string{true: "planned", false: "unsupported"}[supported],
		"adapter_status":        adapterStatus,
		"contract_version":      "provider-review-v1",
		"provider_type":         provider,
		"review_kind":           reviewKind,
		"external_call_made":    false,
		"provider_api_mutation": "disabled",
		"contains_token":        false,
		"contains_file_content": false,
		"operations":            providerReviewAdapterContractOperations(provider, reviewKind),
		"request_envelopes":     providerReviewAdapterRequestEnvelopes(provider, reviewKind, apiRequestPlan, starterFilePayload),
		"response_diagnostics":  providerReviewAdapterResponseDiagnostics(provider, reviewKind),
		"idempotency_plan":      providerReviewAdapterIdempotencyPlan(provider, reviewKind),
		"next_step":             "Rehearse and arm operation adapters only after provider credentials, approval, payload staging, and protected-branch rules pass preflight.",
	}
}

func providerReviewAdapterStatus(provider, reviewKind string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	switch provider {
	case "github":
		if reviewKind == "pull_request" {
			return "planned"
		}
	case "gitea":
		if reviewKind == "merge_request" {
			return "planned"
		}
	}
	return "missing"
}

func providerReviewAdapterRequestEnvelopes(provider, reviewKind string, apiRequestPlan, starterFilePayload map[string]any) []map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "source_branch"))
	targetBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "target_branch"))
	fileCount := intFromAny(starterFilePayload["file_count"], intFromAny(apiRequestPlan["file_count"], 0))
	planReady := fmt.Sprint(apiRequestPlan["status"]) == "ready"
	starterReady := starterFilePayloadReady(starterFilePayload)
	branchRefsReady := sourceBranch != "" &&
		targetBranch != "" &&
		isSafeGitRefPart(sourceBranch) &&
		isSafeGitRefPart(targetBranch)

	return []map[string]any{
		providerReviewAdapterRequestEnvelope(
			provider,
			"create_branch_ref",
			"create_branch_ref",
			"POST",
			"ref_from_target_branch",
			0,
			branchRefsReady,
			planReady,
			starterReady,
			false,
		),
		providerReviewAdapterRequestEnvelope(
			provider,
			"commit_starter_files",
			"commit_files",
			"PUT",
			"content_redacted_file_batch",
			fileCount,
			branchRefsReady,
			planReady,
			starterReady,
			true,
		),
		providerReviewAdapterRequestEnvelope(
			provider,
			"open_review_request",
			"open_review",
			"POST",
			reviewKind,
			0,
			branchRefsReady,
			planReady,
			starterReady,
			false,
		),
	}
}

func providerReviewAdapterRequestEnvelope(
	provider,
	operation,
	endpointOperation,
	method,
	payloadShape string,
	fileCount int,
	branchRefsReady,
	planReady,
	starterReady,
	requiresStarterFiles bool,
) map[string]any {
	readiness := []map[string]any{
		{"evidence": "provider_api_request_plan_ready", "status": readyStatus(planReady)},
		{"evidence": "review_branch_refs_valid", "status": readyStatus(branchRefsReady)},
	}
	if requiresStarterFiles {
		readiness = append(readiness, map[string]any{
			"evidence": "starter_file_payload_staged",
			"status":   readyStatus(starterReady),
		})
	}
	return map[string]any{
		"name":                    operation,
		"method":                  method,
		"endpoint_key":            providerReviewEndpointKey(provider, endpointOperation),
		"payload_shape":           payloadShape,
		"file_count":              fileCount,
		"payload_redacted":        true,
		"contains_token":          false,
		"contains_file_content":   false,
		"contains_provider_url":   false,
		"contains_repository_ref": false,
		"api_call":                false,
		"provider_api_mutation":   "disabled",
		"execution_status":        "blocked",
		"blocked_reason":          "provider_review_mutation_armed",
		"readiness":               readiness,
	}
}

func readyStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "blocked"
}

func providerReviewAdapterResponseDiagnostics(provider, reviewKind string) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	return map[string]any{
		"status":                 "pending",
		"mode":                   "redacted_response_diagnostics",
		"provider_type":          provider,
		"review_kind":            reviewKind,
		"adapter_status":         providerReviewAdapterStatus(provider, reviewKind),
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"response_body_included": false,
		"headers_included":       false,
		"contains_token":         false,
		"contains_provider_url":  false,
		"diagnostic_fields": []string{
			"status_code_class",
			"provider_request_id_present",
			"rate_limit_state",
			"retryable",
			"sanitized_error_code",
		},
		"operations": providerReviewAdapterResponseDiagnosticOperations(provider, reviewKind),
	}
}

func providerReviewAdapterResponseDiagnosticOperations(provider, reviewKind string) []map[string]any {
	return []map[string]any{
		providerReviewAdapterResponseDiagnosticOperation(provider, "create_branch_ref", "create_branch_ref", "2xx_or_already_exists"),
		providerReviewAdapterResponseDiagnosticOperation(provider, "commit_starter_files", "commit_files", "2xx"),
		providerReviewAdapterResponseDiagnosticOperation(provider, "open_review_request", "open_review", "2xx_or_already_exists"),
	}
}

func providerReviewAdapterResponseDiagnosticOperation(provider, name, endpointOperation, successClass string) map[string]any {
	return map[string]any{
		"name":                     name,
		"endpoint_key":             providerReviewEndpointKey(provider, endpointOperation),
		"status":                   "pending",
		"success_status_class":     successClass,
		"retryable_status_classes": []string{"429", "5xx"},
		"response_body_included":   false,
		"headers_included":         false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"external_call_made":       false,
		"provider_api_mutation":    "disabled",
	}
}

func providerReviewAdapterIdempotencyPlan(provider, reviewKind string) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	return map[string]any{
		"status":                     "planned",
		"mode":                       "redacted_idempotency_plan",
		"provider_type":              provider,
		"review_kind":                reviewKind,
		"adapter_status":             providerReviewAdapterStatus(provider, reviewKind),
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"contains_token":             false,
		"contains_provider_url":      false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
		"idempotency_key_included":   false,
		"idempotency_key_material":   "redacted_required_material_only",
		"requires_persisted_attempt": true,
		"retry_after_diagnostics":    true,
		"operations":                 providerReviewAdapterIdempotencyOperations(provider),
	}
}

func providerReviewAdapterIdempotencyOperations(provider string) []map[string]any {
	return []map[string]any{
		providerReviewAdapterIdempotencyOperation(
			provider,
			"create_branch_ref",
			"create_branch_ref",
			"detect_existing_branch_ref",
			"treat_existing_matching_ref_as_success",
		),
		providerReviewAdapterIdempotencyOperation(
			provider,
			"commit_starter_files",
			"commit_files",
			"detect_existing_commit_batch",
			"block_on_content_or_parent_conflict",
		),
		providerReviewAdapterIdempotencyOperation(
			provider,
			"open_review_request",
			"open_review",
			"detect_existing_open_review",
			"reuse_existing_review_request",
		),
	}
}

func providerReviewAdapterIdempotencyOperation(provider, name, endpointOperation, replayCheck, conflictPolicy string) map[string]any {
	return map[string]any{
		"name":                          name,
		"endpoint_key":                  providerReviewEndpointKey(provider, endpointOperation),
		"status":                        "planned",
		"idempotency_key_kind":          "operation_scope_hash",
		"idempotency_key_included":      false,
		"idempotency_key_material":      "redacted_required_material_only",
		"replay_check":                  replayCheck,
		"conflict_policy":               conflictPolicy,
		"retry_policy":                  "retry_only_after_response_diagnostics",
		"requires_persisted_attempt":    true,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
		"external_call_made":            false,
		"provider_api_mutation":         "disabled",
		"provider_api_call_made":        false,
		"response_diagnostics_required": true,
	}
}

func providerReviewAdapterContractOperations(provider, reviewKind string) []map[string]any {
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	scope := "contents:write"
	reviewScope := "pull_requests:write"
	if provider == "gitea" {
		scope = "repository:write"
		reviewScope = "repository:write"
	}
	return []map[string]any{
		{
			"name":                  "create_branch_ref",
			"endpoint_key":          providerReviewEndpointKey(provider, "create_branch_ref"),
			"required_capability":   "branch_ref_write",
			"required_scope":        scope,
			"payload_shape":         "ref_from_target_branch",
			"adapter_status":        adapterStatus,
			"execution_status":      "blocked",
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		},
		{
			"name":                  "commit_starter_files",
			"endpoint_key":          providerReviewEndpointKey(provider, "commit_files"),
			"required_capability":   "file_content_write",
			"required_scope":        scope,
			"payload_shape":         "content_redacted_file_batch",
			"adapter_status":        adapterStatus,
			"execution_status":      "blocked",
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		},
		{
			"name":                  "open_review_request",
			"endpoint_key":          providerReviewEndpointKey(provider, "open_review"),
			"required_capability":   "review_request_write",
			"required_scope":        reviewScope,
			"payload_shape":         reviewKind,
			"adapter_status":        adapterStatus,
			"execution_status":      "blocked",
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		},
	}
}

func firstProviderReviewCredentialStrategy(items ...map[string]any) map[string]any {
	for _, item := range items {
		if len(item) > 0 {
			return item
		}
	}
	return map[string]any{
		"mode":                      "unknown",
		"provider_account_attached": false,
		"token_env_configured":      false,
		"token_env_present":         false,
		"token_stored":              false,
		"external_call_made":        false,
	}
}

func sanitizedProviderReviewCredentialStrategy(value map[string]any) map[string]any {
	if len(value) == 0 {
		return firstProviderReviewCredentialStrategy()
	}
	return map[string]any{
		"mode":                      cleanOptionalText(stringFromMap(value, "mode")),
		"provider_account_attached": boolOnlyFromAny(value["provider_account_attached"]),
		"token_env_configured":      boolOnlyFromAny(value["token_env_configured"]),
		"token_env_present":         boolOnlyFromAny(value["token_env_present"]),
		"token_stored":              false,
		"external_call_made":        false,
	}
}

func boolOnlyFromAny(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func providerReviewEndpointKey(provider, operation string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "github":
		return "github." + operation
	case "gitea":
		return "gitea." + operation
	default:
		return "provider." + operation
	}
}

func templateProviderReviewExecutionGuardrail(provider, reviewKind, sourceBranch, targetBranch string, enableRequested bool) map[string]any {
	return templateProviderReviewExecutionGuardrailWithStaging(provider, reviewKind, sourceBranch, targetBranch, enableRequested, false)
}

func templateProviderReviewExecutionGuardrailWithStaging(provider, reviewKind, sourceBranch, targetBranch string, enableRequested, starterFilePayloadStaged bool) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch = strings.TrimSpace(sourceBranch)
	targetBranch = strings.TrimSpace(targetBranch)
	branchReady := sourceBranch != "" && targetBranch != "" && isSafeGitRefPart(sourceBranch) && isSafeGitRefPart(targetBranch)
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	adapterReady := adapterStatus == "planned"
	mutationArmed := false
	configStatus := "blocked"
	configMessage := "Set ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION=true only after provider branch, commit, and review adapters are ready."
	if enableRequested {
		configStatus = "ready"
		configMessage = "Provider review execution was explicitly requested by configuration."
	}
	gates := []map[string]any{
		{
			"gate":              "provider_review_execution_enabled",
			"status":            configStatus,
			"required_config":   "ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION",
			"message":           configMessage,
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_api_adapter",
			"status":            map[bool]string{true: "ready", false: "blocked"}[adapterReady],
			"provider_type":     provider,
			"review_kind":       reviewKind,
			"adapter_status":    adapterStatus,
			"message":           "Provider branch creation, starter-file commit, and PR/MR API adapter contract is registered for supported providers.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_mutation_armed",
			"status":            map[bool]string{true: "ready", false: "blocked"}[mutationArmed],
			"provider_type":     provider,
			"review_kind":       reviewKind,
			"adapter_status":    adapterStatus,
			"message":           "Provider API mutation remains disabled until the execution adapter is explicitly armed after rehearsal.",
			"sensitive_payload": false,
		},
		{
			"gate":              "review_branches_valid",
			"status":            map[bool]string{true: "ready", false: "blocked"}[branchReady],
			"source_branch":     sourceBranch,
			"target_branch":     targetBranch,
			"message":           "Source and target branches must be present safe git refs before provider review execution.",
			"sensitive_payload": false,
		},
		{
			"gate":              "starter_file_payload_staged",
			"status":            map[bool]string{true: "ready", false: "blocked"}[starterFilePayloadStaged],
			"message":           "Starter-file payload must be staged as a content-redacted audit summary before external provider mutation.",
			"sensitive_payload": false,
		},
	}
	blocked := make([]string, 0, len(gates))
	for _, gate := range gates {
		if gate["status"] != "ready" {
			blocked = append(blocked, stringFromMap(gate, "gate"))
		}
	}
	mode := "disabled"
	if enableRequested {
		mode = "mutation_blocked"
	}
	return map[string]any{
		"execution_mode":           mode,
		"execution_enabled":        false,
		"execution_enabled_config": enableRequested,
		"provider_type":            provider,
		"review_kind":              reviewKind,
		"source_branch":            sourceBranch,
		"target_branch":            targetBranch,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"branch_creation_allowed":  false,
		"review_request_allowed":   false,
		"blocked_reasons":          blocked,
		"gates":                    gates,
		"next_step":                "Rehearse and arm provider branch, commit, and review adapters before enabling provider API mutation.",
	}
}

func templateProviderReviewExecutionRequest(provider, reviewKind, sourceBranch, targetBranch string) map[string]any {
	ready := sourceBranch != "" && targetBranch != ""
	request := map[string]any{
		"status":                   "blocked",
		"approval_action":          templateProviderReviewExecuteApprovalAction,
		"resource_type":            "project_template_run",
		"provider_type":            provider,
		"review_kind":              reviewKind,
		"source_branch":            sourceBranch,
		"target_branch":            targetBranch,
		"payload_redacted":         true,
		"contains_token":           false,
		"provider_api_mutation":    "disabled",
		"requires_operator_review": true,
	}
	if ready {
		request["status"] = "approval_ready"
		return request
	}
	request["blocked_reason"] = "source and target branches are required before requesting provider review execution"
	return request
}

func templateProviderReviewKind(provider, mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch mode {
	case "merge_request":
		return "merge_request"
	case "pull_request":
		return "pull_request"
	default:
		return "pull_request"
	}
}

func templateBranchStrategyActionRequired(strategy map[string]any, defaultBranch string) string {
	proposed := strings.TrimSpace(fmt.Sprint(strategy["proposed_branch"]))
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(strategy["mode"])))
	provider := strings.ToLower(strings.TrimSpace(fmt.Sprint(strategy["provider_type"])))
	switch mode {
	case "pull_request":
		if provider == "github" {
			return fmt.Sprintf("Create branch %s from starter files, then open a GitHub pull request into %s.", proposed, defaultBranch)
		}
		return fmt.Sprintf("Create branch %s from starter files, then open a provider pull request into %s.", proposed, defaultBranch)
	case "merge_request":
		if provider == "gitea" {
			return fmt.Sprintf("Create branch %s from starter files, then open a Gitea pull request into %s.", proposed, defaultBranch)
		}
		return fmt.Sprintf("Create branch %s from starter files, then open a merge request into %s.", proposed, defaultBranch)
	default:
		return fmt.Sprintf("Create branch %s from starter files, then open provider review before merging into %s.", proposed, defaultBranch)
	}
}

func templateProtectedBranchStrategy(repo, remote map[string]any, defaultBranch string) map[string]any {
	metadata := mapFromAny(remote["metadata"])
	mode := strings.ToLower(strings.TrimSpace(firstNonEmptyString(
		stringFromMap(metadata, "branch_strategy"),
		stringFromMap(metadata, "protected_branch_strategy"),
	)))
	if mode == "" || mode == "none" || mode == "direct" || mode == "allow_direct" {
		return nil
	}
	if mode != "proposed_branch" && mode != "pull_request" && mode != "merge_request" {
		return map[string]any{
			"mode":            mode,
			"strategy_status": "unsupported",
			"message":         "Unsupported protected branch strategy; use proposed_branch, pull_request, or merge_request.",
		}
	}
	prefix := strings.TrimSpace(firstNonEmptyString(stringFromMap(metadata, "branch_prefix"), "assops/template"))
	repoKey := strings.TrimSpace(firstNonEmptyString(stringFromMap(repo, "repo_key"), stringFromMap(repo, "name"), "project"))
	proposed := strings.TrimSpace(firstNonEmptyString(
		stringFromMap(metadata, "proposed_branch"),
		stringFromMap(metadata, "branch_name"),
	))
	if proposed == "" {
		proposed = safeTemplateBranchName(prefix, repoKey, defaultBranch)
	} else if !isSafeGitRefPart(proposed) {
		proposed = safeTemplateBranchName(prefix, repoKey, defaultBranch)
	}
	strategy := map[string]any{
		"mode":                 mode,
		"strategy_status":      "planned",
		"proposed_branch":      proposed,
		"target_branch":        defaultBranch,
		"provider_next_action": "open_review",
		"message":              "Starter files should be pushed to a reviewed branch before protected default branch changes.",
	}
	if provider := strings.TrimSpace(firstNonEmptyString(stringFromMap(remote, "provider_type"), stringFromMap(remote, "kind"))); provider != "" {
		strategy["provider_type"] = strings.ToLower(provider)
	}
	return strategy
}

func safeTemplateBranchName(prefix, repoKey, defaultBranch string) string {
	clean := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		var b strings.Builder
		prevDash := false
		for _, r := range value {
			allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
			if allowed {
				b.WriteRune(r)
				prevDash = false
				continue
			}
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
		return strings.Trim(b.String(), "-")
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	cleanPrefixParts := make([]string, 0)
	for _, part := range strings.Split(prefix, "/") {
		if cleaned := clean(part); cleaned != "" {
			cleanPrefixParts = append(cleanPrefixParts, cleaned)
		}
	}
	prefix = strings.Join(cleanPrefixParts, "/")
	if prefix == "" {
		prefix = "assops/template"
	}
	repoPart := clean(repoKey)
	if repoPart == "" {
		repoPart = "project"
	}
	targetPart := clean(defaultBranch)
	if targetPart == "" {
		targetPart = "main"
	}
	branch := prefix + "/" + repoPart + "-" + targetPart
	branch = strings.ReplaceAll(branch, "//", "/")
	branch = strings.Trim(branch, "/.")
	if !isSafeGitRefPart(branch) {
		return "assops/template/" + repoPart + "-" + targetPart
	}
	return branch
}

func templateProviderAlreadyExists(status int, body []byte) bool {
	if status == http.StatusConflict {
		return true
	}
	if status != http.StatusUnprocessableEntity {
		return false
	}
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	for _, message := range providerErrorMessages(payload) {
		if providerErrorMessageMeansAlreadyExists(message) {
			return true
		}
	}
	return false
}

func providerErrorMessages(value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		var out []string
		for _, key := range []string{"message", "error", "resource", "code"} {
			if message := strings.TrimSpace(fmt.Sprint(typed[key])); message != "" && message != "<nil>" {
				out = append(out, message)
			}
		}
		for _, key := range []string{"errors", "details"} {
			out = append(out, providerErrorMessages(typed[key])...)
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, providerErrorMessages(item)...)
		}
		return out
	default:
		return nil
	}
}

func providerErrorMessageMeansAlreadyExists(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return message == "already_exists" ||
		message == "already exists" ||
		message == "name already exists" ||
		message == "repository already exists"
}

type externalTemplateProviderConfig struct {
	Provider       string
	APIBase        string
	CreateURL      string
	Owner          string
	RepositoryName string
	Description    string
	TokenEnv       string
	Token          string
	Private        bool
}

func buildExternalTemplateProviderSpec(repo, remote map[string]any) (externalTemplateProviderConfig, bool) {
	provider := strings.ToLower(strings.TrimSpace(firstNonEmptyString(stringFromMap(remote, "provider_type"), stringFromMap(remote, "kind"))))
	if provider != "github" && provider != "gitea" {
		return externalTemplateProviderConfig{}, false
	}
	metadata := mapFromAny(remote["metadata"])
	repoName := firstNonEmptyString(stringFromMap(metadata, "repository_name"), stringFromMap(metadata, "name"), stringFromMap(repo, "repo_key"), stringFromMap(repo, "name"))
	if repoName == "" || !isSafeRepositoryName(repoName) {
		return externalTemplateProviderConfig{}, false
	}
	owner := firstNonEmptyString(stringFromMap(metadata, "owner"), stringFromMap(metadata, "org"), repositoryOwnerFromURL(remoteURLFromRow(remote)))
	tokenEnv := firstNonEmptyString(stringFromMap(metadata, "token_env"), defaultTemplateProviderTokenEnv(provider))
	if templateRemoteUsesProviderAccount(remote, metadata) {
		tokenEnv = firstNonEmptyString(stringFromMap(metadata, "token_env"), stringFromMap(metadata, "provider_account_env"))
	}
	if !safeTemplateProviderTokenEnv(provider, tokenEnv) {
		return externalTemplateProviderConfig{}, false
	}
	visibility := strings.ToLower(strings.TrimSpace(stringFromMap(metadata, "visibility")))
	spec := externalTemplateProviderConfig{
		Provider:       provider,
		APIBase:        firstNonEmptyString(stringFromMap(metadata, "api_base_url"), defaultTemplateProviderAPIBase(provider, remote)),
		Owner:          owner,
		RepositoryName: repoName,
		Description:    firstNonEmptyString(stringFromMap(metadata, "description"), stringFromMap(repo, "description")),
		TokenEnv:       tokenEnv,
		Token:          strings.TrimSpace(os.Getenv(tokenEnv)),
		Private:        templateProviderPrivate(metadata, visibility),
	}
	createURL, ok := templateProviderCreateURL(spec.Provider, spec.APIBase, spec.Owner)
	if !ok {
		return externalTemplateProviderConfig{}, false
	}
	spec.CreateURL = createURL
	return spec, true
}

func templateRemoteUsesProviderAccount(remote, metadata map[string]any) bool {
	return strings.TrimSpace(stringFromMap(remote, "source_account_id")) != "" ||
		strings.TrimSpace(stringFromMap(metadata, "provider_account_id")) != "" ||
		strings.TrimSpace(stringFromMap(metadata, "provider_account_name")) != ""
}

func templateProviderPrivate(metadata map[string]any, visibility string) bool {
	if _, ok := metadata["private"]; ok {
		return boolDefaultFromMap(metadata, "private", true)
	}
	switch visibility {
	case "public":
		return false
	case "internal", "private":
		return true
	default:
		return true
	}
}

func templateRemoteProtectsDefaultBranch(remote map[string]any) bool {
	if boolFromMap(remote, "protected") {
		return true
	}
	metadata := mapFromAny(remote["metadata"])
	return boolFromMap(metadata, "protected") || boolFromMap(metadata, "protected_branch")
}

func templateRemoteAllowsProtectedBranchPush(remote map[string]any) bool {
	metadata := mapFromAny(remote["metadata"])
	return boolFromMap(metadata, "allow_protected_branch_push")
}

func templateRemoteAllowsExistingRepositoryPush(remote map[string]any) bool {
	metadata := mapFromAny(remote["metadata"])
	return boolFromMap(metadata, "allow_existing_repository_push")
}

func safeTemplateProviderTokenEnv(provider, value string) bool {
	value = strings.TrimSpace(value)
	switch provider {
	case "github":
		return value == "ASSOPS_GITHUB_TEMPLATE_TOKEN" || safeTemplateProviderTokenEnvSuffix(value, "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_")
	case "gitea":
		return value == "ASSOPS_GITEA_TEMPLATE_TOKEN" || safeTemplateProviderTokenEnvSuffix(value, "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_")
	default:
		return false
	}
}

func safeTemplateProviderTokenEnvSuffix(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) > len(prefix)+64 {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func providerErrorSuffix(body []byte) string {
	message := providerErrorMessage(body)
	if message == "" {
		return ""
	}
	return ": " + truncateProviderError(message, providerDiagnosticErrorLimit)
}

func providerErrorMessage(body []byte) string {
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err == nil {
		for _, key := range []string{"message", "error"} {
			message := strings.TrimSpace(fmt.Sprint(payload[key]))
			if message != "" && message != "<nil>" {
				return message
			}
		}
	}
	return ""
}

func truncateProviderError(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func newTemplateProviderHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if err := validateTemplateProviderHost(ctx, host); err != nil {
				return nil, err
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return validateTemplateProviderURL(req.Context(), req.URL.String())
		},
	}
}

func validateTemplateProviderURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("scheme must be http or https")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("host is required")
	}
	return validateTemplateProviderHost(ctx, parsed.Hostname())
}

func validateTemplateProviderHost(ctx context.Context, host string) error {
	if allowLocalTemplateProviderAPI() && isLoopbackHost(host) {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("host resolved to no addresses")
	}
	for _, item := range ips {
		if !isPublicTemplateProviderIP(item.IP) {
			return fmt.Errorf("host resolves to non-public address")
		}
	}
	return nil
}

func isPublicTemplateProviderIP(ip net.IP) bool {
	return ip != nil &&
		ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified()
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func allowLocalTemplateProviderAPI() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API")), "true")
}

func templateProviderCreateURL(provider, apiBase, owner string) (string, bool) {
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if apiBase == "" {
		return "", false
	}
	parsed, err := url.Parse(apiBase)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", false
	}
	owner = url.PathEscape(strings.TrimSpace(owner))
	switch provider {
	case "github":
		if owner != "" {
			return apiBase + "/orgs/" + owner + "/repos", true
		}
		return apiBase + "/user/repos", true
	case "gitea":
		if owner != "" {
			return apiBase + "/orgs/" + owner + "/repos", true
		}
		return apiBase + "/user/repos", true
	default:
		return "", false
	}
}

func defaultTemplateProviderTokenEnv(provider string) string {
	switch provider {
	case "github":
		return "ASSOPS_GITHUB_TEMPLATE_TOKEN"
	case "gitea":
		return "ASSOPS_GITEA_TEMPLATE_TOKEN"
	default:
		return ""
	}
}

func defaultTemplateProviderAPIBase(provider string, remote map[string]any) string {
	switch provider {
	case "github":
		return "https://api.github.com"
	case "gitea":
		if origin := remoteOrigin(remote); origin != "" {
			return origin + "/api/v1"
		}
	}
	return ""
}

func remoteOrigin(remote map[string]any) string {
	for _, raw := range []string{stringFromMap(remote, "web_url"), remoteURLFromRow(remote)} {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err == nil && parsed.Scheme != "" && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") {
			return parsed.Scheme + "://" + parsed.Host
		}
	}
	return ""
}

func repositoryOwnerFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 {
			return parts[0]
		}
	}
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) == 2 {
			pathParts := strings.Split(strings.Trim(parts[1], "/"), "/")
			if len(pathParts) >= 2 {
				return pathParts[0]
			}
		}
	}
	return ""
}

func isSafeRepositoryName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 100 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func externalTemplateRemote(remotes []map[string]any) map[string]any {
	for _, remote := range remotes {
		provider := strings.ToLower(strings.TrimSpace(firstNonEmptyString(stringFromMap(remote, "provider_type"), stringFromMap(remote, "kind"))))
		if provider == "github" || provider == "gitea" {
			return remote
		}
	}
	return nil
}

func (e *GitExecutor) ensureBareRepository(ctx context.Context, result *gitExecutionResult, gitDir string) error {
	runner := e.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	stdout, stderr, err := runner.Run(ctx, "", "git", "--git-dir", gitDir, "rev-parse", "--is-bare-repository")
	result.Stdout += sanitizeGitOutput(stdout)
	result.Stderr += sanitizeGitOutput(stderr)
	if err != nil {
		return fmt.Errorf("checking bare repository failed: %w", err)
	}
	if strings.TrimSpace(stdout) != "true" {
		return fmt.Errorf("local_bare remote_url already exists but is not a bare repository")
	}
	return nil
}

func (e *GitExecutor) bareBranchSHA(ctx context.Context, result *gitExecutionResult, gitDir, branch string) (string, bool, error) {
	runner := e.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	ref := "refs/heads/" + branch
	stdout, stderr, err := runner.Run(ctx, "", "git", "--git-dir", gitDir, "rev-parse", "--verify", ref)
	result.Stdout += sanitizeGitOutput(stdout)
	result.Stderr += sanitizeGitOutput(stderr)
	if err != nil {
		return "", false, nil
	}
	sha := strings.TrimSpace(stdout)
	if sha == "" {
		return "", false, nil
	}
	return sha, true, nil
}

func localBareTemplateRemote(remotes []map[string]any) map[string]any {
	for _, remote := range remotes {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(remote["provider_type"])), "local_bare") ||
			strings.EqualFold(strings.TrimSpace(fmt.Sprint(remote["kind"])), "local_bare") {
			return remote
		}
	}
	return nil
}

func safeLocalBareRemotePath(path string, baseDirs []string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "\x00") {
		return false
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "git@") {
		return false
	}
	if !filepath.IsAbs(path) || len(baseDirs) == 0 {
		return false
	}
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	for _, base := range baseDirs {
		base = strings.TrimSpace(base)
		if base == "" || !filepath.IsAbs(base) {
			continue
		}
		cleanBase, err := filepath.Abs(filepath.Clean(base))
		if err != nil {
			continue
		}
		if cleanBase == string(os.PathSeparator) {
			continue
		}
		rel, err := filepath.Rel(cleanBase, cleanPath)
		if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
			return true
		}
	}
	return false
}

func safeResolvedLocalBareRemotePath(path string, baseDirs []string) bool {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return false
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return false
	}
	for _, base := range baseDirs {
		base = strings.TrimSpace(base)
		if base == "" || !filepath.IsAbs(base) {
			continue
		}
		resolvedBase, err := filepath.EvalSymlinks(base)
		if err != nil {
			continue
		}
		resolvedBase, err = filepath.Abs(resolvedBase)
		if err != nil || resolvedBase == string(os.PathSeparator) {
			continue
		}
		rel, err := filepath.Rel(resolvedBase, parent)
		if err == nil && (rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..")) {
			return true
		}
	}
	return false
}

func (e *GitExecutor) run(ctx context.Context, result *gitExecutionResult, dir, name string, args ...string) error {
	runner := e.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	stdout, stderr, err := runner.Run(ctx, dir, name, args...)
	result.Stdout += sanitizeGitOutput(stdout)
	result.Stderr += sanitizeGitOutput(stderr)
	if err != nil {
		return fmt.Errorf("%s %s failed: %w", name, strings.Join(redactGitArgs(args), " "), err)
	}
	return nil
}

func (e *GitExecutor) revParse(ctx context.Context, dir, ref string) (string, error) {
	runner := e.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	stdout, stderr, err := runner.Run(ctx, dir, "git", "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w: %s", err, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout), nil
}

func (e *GitExecutor) newWorkDir(pattern string) (string, func(), error) {
	base := e.WorkDir
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, fmt.Errorf("creating git work dir: %w", err)
	}
	dir, err := os.MkdirTemp(base, pattern)
	if err != nil {
		return "", nil, fmt.Errorf("creating git temp dir: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func remoteURLFromRow(row map[string]any) string {
	if value := strings.TrimSpace(fmt.Sprint(row["remote_url"])); value != "" && value != "<nil>" {
		return value
	}
	if urls, ok := row["urls"].([]any); ok {
		for _, url := range urls {
			if value := strings.TrimSpace(fmt.Sprint(url)); value != "" {
				return value
			}
		}
	}
	return ""
}

func defaultBranchFromRow(row map[string]any) string {
	branch := strings.TrimSpace(fmt.Sprint(row["default_branch"]))
	if branch == "" || branch == "<nil>" {
		return "main"
	}
	return branch
}

func gitRefsFromInput(input any, defaultBranch string) gitRefs {
	refsMap := mapFromAny(input)
	if nested, ok := refsMap["refs"]; ok {
		refsMap = mapFromAny(nested)
	}
	refs := gitRefs{
		Branches: stringSliceFromAny(refsMap["branches"]),
		Tags:     stringSliceFromAny(refsMap["tags"]),
	}
	if len(refs.Branches) == 0 && len(refs.Tags) == 0 && defaultBranch != "" {
		refs.Branches = []string{defaultBranch}
	}
	return refs
}

func mapFromAny(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func stringSliceFromAny(value any) []string {
	if typed, ok := value.([]string); ok {
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	items, ok := value.([]any)
	if !ok {
		if strings.TrimSpace(fmt.Sprint(value)) == "" || fmt.Sprint(value) == "<nil>" {
			return nil
		}
		return []string{strings.TrimSpace(fmt.Sprint(value))}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(fmt.Sprint(item))
		if value != "" && value != "<nil>" {
			out = append(out, value)
		}
	}
	return out
}

var safeGitRefPartPattern = regexp.MustCompile(`^[A-Za-z0-9._/\-]+$`)
var fullHexSHAPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)

func isSafeGitRefPart(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, "..") {
		return false
	}
	if strings.Contains(value, "//") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".lock") {
		return false
	}
	return safeGitRefPartPattern.MatchString(value)
}

func isFullHexSHA(value string) bool {
	return fullHexSHAPattern.MatchString(value)
}

func redactGitArgs(args []string) []string {
	redacted := make([]string, len(args))
	copy(redacted, args)
	for i, arg := range redacted {
		if strings.Contains(arg, "://") || strings.Contains(arg, "@") && strings.Contains(arg, ":") {
			redacted[i] = "<remote>"
		}
	}
	return redacted
}

var gitURLPattern = regexp.MustCompile(`(?i)((?:https?|ssh|git)://[^\s'"]+|[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+:[^\s'"]+)`)

func sanitizeGitOutput(output string) string {
	return gitURLPattern.ReplaceAllString(output, "<remote>")
}
