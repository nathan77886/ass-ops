package main

import (
	"fmt"
	"strings"
)

func releaseTagRehearsalPlan(projectSlug, remoteKey string) (string, error) {
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	if !isSafeProjectSlug(projectSlug) {
		return "", fmt.Errorf("project slug must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}
	remoteKey = strings.ToLower(strings.TrimSpace(remoteKey))
	if !isSafeProjectSlug(remoteKey) {
		return "", fmt.Errorf("remote key must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS GitHub Tag Rehearsal Plan\n\n")
	fmt.Fprintf(&b, "Project slug: `%s`\n\n", projectSlug)
	fmt.Fprintf(&b, "Remote key: `%s`\n\n", remoteKey)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Project slug and remote key are safe local identifiers for matching ASSOPS release evidence.\n")
	fmt.Fprintf(&b, "- This plan intentionally does not accept tag names, commit SHAs, branches, remote URLs, workflow URLs, provider run IDs, token names, messages, or Git output as inputs.\n")
	fmt.Fprintf(&b, "- This plan does not call GitHub, run Git, create or push tags, refresh Actions, write operation logs, update repo_tag_runs, sync assets, or record snapshots.\n\n")
	fmt.Fprintf(&b, "## Live Rehearsal Sequence\n\n")
	for index, step := range []string{
		"Confirm the selected remote is a project-owned GitHub remote for the intended logical repository.",
		"Request the approval-gated remote-specific tag operation in ASSOPS with the real tag target reviewed outside this document.",
		"Execute the tag operation only after approval, protected-branch/tag policy review, and credential scope review are complete.",
		"Run the read-only live tag lookup for the recorded tag run and wait for sanitized terminal lookup evidence.",
		"Run the GitHub Actions refresh for the tag target remote and wait for synced local Actions evidence.",
		"Record the sanitized local tag-result snapshot from repo_tag_runs evidence only.",
		"Record the sanitized Actions refresh snapshot from locally synced github_action_runs evidence only.",
		"Run `assops-tool project readiness` after `assops-tool db sync-assets` confirms the tag run to Actions graph evidence.",
	} {
		fmt.Fprintf(&b, "%d. %s\n", index+1, step)
	}
	fmt.Fprintf(&b, "\n## Required Evidence\n\n")
	for _, item := range []string{
		"project-owned GitHub remote asset for the selected remote key",
		"approval request for the tag operation",
		"completed repo_tag_run asset linked to the project-owned remote",
		"read-only live lookup operation terminal evidence",
		"GitHub Actions refresh operation terminal evidence",
		"repo_tag_run to github_action_run canonical graph edge",
		"sanitized tag-result snapshot status",
		"sanitized Actions refresh snapshot status",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## Suppressed Material\n\n")
	for _, item := range []string{
		"tag names, branch names, commit SHAs, and target refs",
		"remote clone URLs, workflow URLs, run IDs, and provider request IDs",
		"provider tokens, authorization headers, credentials, and token env names",
		"tag messages, release notes, Git stdout/stderr, and command output",
		"provider request/response bodies, workflow logs, and raw error details",
		"operator notes containing repository, ref, credential, or provider details",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local documentation only; it does not call providers, run Git, create tags, push refs, refresh Actions, enqueue workers, write operation logs, sync assets, or record snapshots.\n")
	fmt.Fprintf(&b, "- Tag creation, live lookup, Actions refresh, graph sync, and snapshot recording remain operator-owned staging tasks.\n\n")
	fmt.Fprintf(&b, "## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool project readiness\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Repo Sync -> selected tag run -> Live lookup\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Repo Sync -> selected tag run -> Actions refresh\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Repo Sync -> selected tag run -> Record tag and Actions snapshots\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run the real tag rehearsal only after the target remote, approval policy, credential scope, tag protection posture, and Actions refresh expectations are confirmed out of band.\n")
	return b.String(), nil
}

func releaseConfigRehearsalPlan(projectSlug, remoteKey string) (string, error) {
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	if !isSafeProjectSlug(projectSlug) {
		return "", fmt.Errorf("project slug must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}
	remoteKey = strings.ToLower(strings.TrimSpace(remoteKey))
	if !isSafeProjectSlug(remoteKey) {
		return "", fmt.Errorf("remote key must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Config Repository Rehearsal Plan\n\n")
	fmt.Fprintf(&b, "Project slug: `%s`\n\n", projectSlug)
	fmt.Fprintf(&b, "Remote key: `%s`\n\n", remoteKey)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Project slug and remote key are safe local identifiers for matching ASSOPS config repository evidence.\n")
	fmt.Fprintf(&b, "- This plan intentionally does not accept branch names, commit SHAs, refs, remote URLs, file contents, provider URLs, token names, Git output, provider responses, or operator notes as inputs.\n")
	fmt.Fprintf(&b, "- This plan does not run Git, create files, commit, push, call providers, update ProjectVersion rows, enqueue workers, write operation logs, sync assets, pin config commits, or record snapshots.\n\n")
	fmt.Fprintf(&b, "## Live Rehearsal Sequence\n\n")
	for index, step := range []string{
		"Confirm the logical repository is project-owned, has `repo_role=config`, and uses the selected config remote.",
		"Review the read-only scaffold preview for `envs/dev`, `envs/test`, `envs/prod`, values examples, secrets examples, and README paths without copying file contents into release notes.",
		"Run local workspace review and secret scanning before requesting the approval-gated `config.git_commit` audit workflow.",
		"Request the `config.git_commit` operation only after approval, credential scope review, and protected-branch/review policy review are complete.",
		"Wait for terminal sanitized config Git commit operation/log evidence before treating the audit workflow as recorded.",
		"Run the read-only config refs refresh (`git.refs.refresh`) after provider review and wait for terminal sanitized ref-refresh evidence.",
		"Record the config ref-refresh snapshot from terminal sanitized evidence only.",
		"Record the config promotion snapshot only after audit evidence and provider-reviewed live workflow evidence are promotion-review-ready.",
		"Run `assops-tool db pin-config-commit --project-version-id <project-version-id> --repository-id <config-repository-id> --remote-id <config-remote-id> --dry-run` before any non-dry-run ProjectVersion pin.",
	} {
		fmt.Fprintf(&b, "%d. %s\n", index+1, step)
	}
	fmt.Fprintf(&b, "\n## Required Evidence\n\n")
	for _, item := range []string{
		"project-owned ProjectGitRepository asset with repo_role=config",
		"project-owned config Git remote asset for the selected remote key",
		"config scaffold preview review status",
		"approval request for config.git_commit",
		"terminal sanitized config.git_commit operation/log evidence",
		"terminal sanitized git.refs.refresh evidence",
		"config ref-refresh snapshot status",
		"config promotion snapshot status",
		"dry-run pin-config-commit result",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## Suppressed Material\n\n")
	for _, item := range []string{
		"branch names, commit SHAs, refs, and remote URLs",
		"file contents, config values, secrets examples, and generated manifest bodies",
		"provider URLs, provider request IDs, PR or MR URLs, workflow URLs, and run IDs",
		"provider tokens, authorization headers, credentials, and token env names",
		"Git stdout/stderr, command output, commit messages, and diff bodies",
		"provider request/response bodies, workflow logs, raw error details, and operator notes",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local documentation only; it does not run Git, create files, commit, push refs, call providers, update ProjectVersion rows, enqueue workers, write operation logs, sync assets, pin config commits, or record snapshots.\n")
	fmt.Fprintf(&b, "- File creation, commit/push execution, provider review, refs refresh, promotion snapshot, and ProjectVersion pinning remain operator-owned staging tasks.\n\n")
	fmt.Fprintf(&b, "## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool project readiness\n")
	fmt.Fprintf(&b, "assops-tool db pin-config-commit --project-version-id <project-version-id> --repository-id <config-repository-id> --remote-id <config-remote-id> --dry-run\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Project -> config repository -> Refresh config refs\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Project -> config repository -> Record refs snapshot / Record promotion snapshot\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run the real config Git workflow only after scaffold review, secret scanning, approval policy, credential scope, branch protection, provider review, and ProjectVersion pin ownership are confirmed out of band.\n")
	return b.String(), nil
}

func releaseAgentCodeRehearsalPlan(projectSlug, runtimeKey string) (string, error) {
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	if !isSafeProjectSlug(projectSlug) {
		return "", fmt.Errorf("project slug must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}
	runtimeKey = strings.ToLower(strings.TrimSpace(runtimeKey))
	if !isSafeProjectSlug(runtimeKey) {
		return "", fmt.Errorf("runtime key must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Agent Code Rehearsal Plan\n\n")
	fmt.Fprintf(&b, "Project slug: `%s`\n\n", projectSlug)
	fmt.Fprintf(&b, "Runtime key: `%s`\n\n", runtimeKey)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Project slug and runtime key are safe local identifiers for matching ASSOPS agent execution evidence.\n")
	fmt.Fprintf(&b, "- This plan intentionally does not accept repository URLs, workspace paths, branch names, prompts, tool input/output, patch content, diff content, file contents, test commands, command output, provider URLs, token names, or credentials as inputs.\n")
	fmt.Fprintf(&b, "- This plan does not start Codex CLI, materialize runtime config, checkout source, bind workspaces, create branches, prepare or apply patches, run tests, invoke commit_push_spark, commit, push, create provider reviews, update agent tasks, write operation logs, sync assets, or record snapshots.\n\n")
	fmt.Fprintf(&b, "## Live Rehearsal Sequence\n\n")
	for index, step := range []string{
		"Confirm the ASSOPS project has a project-owned agent_task asset and selected AI runtime metadata for the runtime key.",
		"Review graph-backed context readiness for the target project and confirm `context.generate` evidence is terminal and sanitized.",
		"Approve the audit-only `agent.execute` operation and wait for worker dispatch, Codex execution plan, and patch preparation audit rows to reach terminal status.",
		"Review the source checkout and branch-policy preflight before any future source remote checkout is armed.",
		"Review the code modification execution arming plan and keep source checkout, branch creation, patch application, test execution, git-commit backend, push-ref backend, and provider review creation disabled.",
		"Record the sanitized tool-call audit snapshot from terminal agent_tool_calls evidence only.",
		"Record the sanitized tool-arming snapshot only when runtime metadata, allowlist, successful audit evidence, and result callback observation are ready.",
		"Record the sanitized code-audit snapshot only after worker dispatch, Codex-plan, and patch-prepare audit rows are all successfully recorded.",
		"Treat any commit_push_spark or provider-review action as a separate future operator-reviewed workflow; this plan only checks that it remains uninvoked.",
	} {
		fmt.Fprintf(&b, "%d. %s\n", index+1, step)
	}
	fmt.Fprintf(&b, "\n## Required Evidence\n\n")
	for _, item := range []string{
		"project-owned agent_task asset",
		"selected AI runtime metadata for the runtime key",
		"completed context.generate tool-call evidence",
		"approval request for agent.execute",
		"terminal worker dispatch plan audit",
		"terminal codex.execution.plan audit",
		"terminal patch.prepare audit",
		"source checkout and branch-policy review preflight",
		"code modification execution arming plan",
		"sanitized tool-call audit snapshot status",
		"sanitized tool-arming snapshot status",
		"sanitized code-audit snapshot status",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## Suppressed Material\n\n")
	for _, item := range []string{
		"runtime config, environment variables, authorization headers, API keys, tokens, and credentials",
		"source remote URLs, repository URLs, workspace paths, branch names, and default branch names",
		"prompt bodies, tool input/output, raw tool input/output, and runtime provider responses",
		"patch content, diff content, file contents, generated files, and command output",
		"test commands, test output, Git stdout/stderr, commit messages, and provider review URLs",
		"operator notes containing repository, workspace, branch, credential, patch, or provider details",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local documentation only; it does not start Codex CLI, materialize runtime config, checkout source, bind workspaces, create branches, prepare or apply patches, run tests, invoke commit_push_spark, commit, push refs, create provider reviews, update agent tasks, write operation logs, sync assets, or record snapshots.\n")
	fmt.Fprintf(&b, "- Real source checkout, patch application, test execution, commit/push, and provider review creation remain disabled until a separate operator-reviewed execution backend is implemented and armed.\n\n")
	fmt.Fprintf(&b, "## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool project readiness\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Agent task -> execution guardrails\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Agent task -> Record tool-call audit / tool-arming / code-audit snapshots\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run real agent code execution only after runtime verification, source remote review, workspace binding review, branch policy review, structured patch review, test-plan review, commit-push agent review, provider-review reconciliation, and raw I/O redaction review are confirmed out of band.\n")
	return b.String(), nil
}
