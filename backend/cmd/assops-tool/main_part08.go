package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

func releaseAgentToolRehearsalPlan(projectSlug, runtimeKey string) (string, error) {
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	if !isSafeProjectSlug(projectSlug) {
		return "", fmt.Errorf("project slug must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}
	runtimeKey = strings.ToLower(strings.TrimSpace(runtimeKey))
	if !isSafeProjectSlug(runtimeKey) {
		return "", fmt.Errorf("runtime key must contain letters or numbers and may include only internal dot, underscore, or hyphen")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Agent Tool Rehearsal Plan\n\n")
	fmt.Fprintf(&b, "Project slug: `%s`\n\n", projectSlug)
	fmt.Fprintf(&b, "Runtime key: `%s`\n\n", runtimeKey)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Project slug and runtime key are safe local identifiers for matching ASSOPS agent tool execution evidence.\n")
	fmt.Fprintf(&b, "- This plan intentionally does not accept prompts, runtime config, environment variables, tool input/output, raw tool input/output, workspace paths, repository URLs, patch content, diff content, provider URLs, token names, credentials, or worker secret material as inputs.\n")
	fmt.Fprintf(&b, "- This plan does not invoke tools, materialize runtime config, materialize tool input, record tool output, start Codex CLI, apply patches, mutate repositories, call providers, update agent tasks, write operation logs, sync assets, or record snapshots.\n\n")
	fmt.Fprintf(&b, "## Live Rehearsal Sequence\n\n")
	for index, step := range []string{
		"Confirm the ASSOPS project has a project-owned agent_task asset and selected AI runtime metadata for the runtime key.",
		"Review the graph-backed context snapshot and verify the agent task is linked to the selected runtime.",
		"Approve the audit-only `agent.execute` operation and wait for the worker claim plan to create a claimable audit job.",
		"Confirm the allowlisted tool set remains limited to `context.generate`, `runtime.check`, `codex.execution.plan`, and `patch.prepare`.",
		"Wait for terminal sanitized agent_tool_calls audit evidence before treating result callback recording as observed.",
		"Review the result callback plan and verify it records sanitized audit status only, with raw tool input/output still suppressed.",
		"Review the tool execution arming plan and keep worker tool invocation, tool input materialization, tool output recording, Codex CLI process start, patch apply, repository mutation, and provider calls disabled.",
		"Review the allowlisted tool-invocation review plan only after runtime metadata, allowlist, terminal successful audit evidence, and result callback observation are ready.",
		"Record the sanitized tool-call audit snapshot from terminal agent_tool_calls evidence only.",
		"Record the sanitized tool-arming snapshot only when allowlist and successful audit evidence make the operator review preflight ready.",
	} {
		fmt.Fprintf(&b, "%d. %s\n", index+1, step)
	}
	fmt.Fprintf(&b, "\n## Required Evidence\n\n")
	for _, item := range []string{
		"project-owned agent_task asset",
		"selected AI runtime metadata for the runtime key",
		"agent_task to ai_runtime graph relation",
		"approval request for agent.execute",
		"worker claim plan audit job evidence",
		"allowlisted tool names review",
		"terminal sanitized agent_tool_calls evidence",
		"sanitized result callback observation",
		"tool execution arming plan state",
		"allowlisted tool-invocation review plan state",
		"sanitized tool-call audit snapshot status",
		"sanitized tool-arming snapshot status",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## Suppressed Material\n\n")
	for _, item := range []string{
		"runtime config, environment variables, authorization headers, worker secrets, API keys, tokens, and credentials",
		"prompt bodies, tool input, raw tool input, tool output, raw tool output, and runtime provider responses",
		"workspace paths, repository URLs, source remote URLs, branch names, patch content, diff content, and file contents",
		"provider URLs, provider request/response bodies, response headers, workflow logs, and command output",
		"operation IDs, tool-call IDs, worker claim fields, idempotency keys, and operator notes containing sensitive execution details",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local documentation only; it does not invoke tools, materialize runtime config, materialize tool input, record tool output, start Codex CLI, apply patches, mutate repositories, call providers, update agent tasks, write operation logs, sync assets, or record snapshots.\n")
	fmt.Fprintf(&b, "- Real allowlisted tool invocation remains disabled until a separate operator-reviewed execution backend, raw I/O redaction review, result callback review, and provider/repository mutation boundary are implemented and armed.\n\n")
	fmt.Fprintf(&b, "## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool project readiness\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Agent task -> worker dispatch and tool guardrails\n")
	fmt.Fprintf(&b, "# In ASSOPS UI: Agent task -> Record tool-call audit / tool-arming snapshots\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run real allowlisted tool invocation only after runtime verification, tool allowlist review, terminal audit review, result callback review, operator execution review, and raw I/O redaction review are confirmed out of band.\n")
	return b.String(), nil
}

func readBranchProtectionRuleset(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading ruleset JSON: %w", err)
	}
	var ruleset map[string]any
	if err := json.Unmarshal(raw, &ruleset); err != nil {
		return nil, fmt.Errorf("parsing ruleset JSON: %w", err)
	}
	name, _ := ruleset["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("ruleset name is required")
	}
	if ruleset["target"] != "branch" {
		return nil, fmt.Errorf("ruleset target must be branch")
	}
	if ruleset["enforcement"] != "active" {
		return nil, fmt.Errorf("ruleset enforcement must be active")
	}
	conditions := toolMapFromAny(ruleset["conditions"])
	refName := toolMapFromAny(conditions["ref_name"])
	if !toolContainsString(stringSliceFromAny(refName["include"]), "~DEFAULT_BRANCH") {
		return nil, fmt.Errorf("ruleset must include ~DEFAULT_BRANCH")
	}
	rules, ok := ruleset["rules"].([]any)
	if !ok || len(rules) == 0 {
		return nil, fmt.Errorf("ruleset rules must be a non-empty array")
	}
	ruleByType := map[string]map[string]any{}
	for _, rawRule := range rules {
		rule := toolMapFromAny(rawRule)
		ruleType := strings.TrimSpace(fmt.Sprint(rule["type"]))
		if ruleType != "" {
			ruleByType[ruleType] = rule
		}
	}
	for _, required := range []string{"deletion", "non_fast_forward", "pull_request", "required_status_checks"} {
		if _, ok := ruleByType[required]; !ok {
			return nil, fmt.Errorf("ruleset missing %s rule", required)
		}
	}
	prParams := toolMapFromAny(ruleByType["pull_request"]["parameters"])
	for _, field := range []string{"dismiss_stale_reviews_on_push", "require_code_owner_review", "require_last_push_approval", "required_review_thread_resolution"} {
		if prParams[field] != true {
			return nil, fmt.Errorf("pull request rule must set %s", field)
		}
	}
	if intFromAny(prParams["required_approving_review_count"]) < 1 {
		return nil, fmt.Errorf("pull request rule must require at least one approval")
	}
	statusParams := toolMapFromAny(ruleByType["required_status_checks"]["parameters"])
	if statusParams["strict_required_status_checks_policy"] != true {
		return nil, fmt.Errorf("required status checks must be strict")
	}
	checks := statusCheckContexts(statusParams["required_status_checks"])
	for _, want := range firstVersionRequiredStatusChecks() {
		if !toolContainsString(checks, want) {
			return nil, fmt.Errorf("ruleset missing required status check %q", want)
		}
	}
	ruleset["required_status_checks"] = checks
	return ruleset, nil
}

func readCodeownersSummary(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading CODEOWNERS: %w", err)
	}
	ownersByPattern := map[string][]string{}
	defaultOwnerLine := 0
	for lineNo, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("CODEOWNERS line %d must include a pattern and owner", lineNo+1)
		}
		pattern := fields[0]
		if strings.Contains(pattern, "..") {
			return nil, fmt.Errorf("CODEOWNERS line %d contains unsafe pattern", lineNo+1)
		}
		for _, owner := range fields[1:] {
			if !strings.HasPrefix(owner, "@") || len(owner) < 2 || strings.ContainsAny(owner, " \t\r\n") {
				return nil, fmt.Errorf("CODEOWNERS line %d contains invalid owner", lineNo+1)
			}
			ownersByPattern[pattern] = append(ownersByPattern[pattern], owner)
		}
		if pattern == "*" {
			defaultOwnerLine = lineNo + 1
		}
	}
	for _, pattern := range requiredCodeownerPatterns() {
		if len(ownersByPattern[pattern]) == 0 {
			return nil, fmt.Errorf("CODEOWNERS missing required pattern %s", pattern)
		}
	}
	return map[string]any{
		"required_patterns":  requiredCodeownerPatterns(),
		"pattern_count":      len(ownersByPattern),
		"default_owner_line": defaultOwnerLine,
	}, nil
}

func statusCheckContexts(value any) []string {
	rawItems, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item := toolMapFromAny(rawItem)
		context, ok := item["context"].(string)
		context = strings.TrimSpace(context)
		if ok && context != "" {
			out = append(out, context)
		}
	}
	sort.Strings(out)
	return out
}

// Keep this list synchronized with `.github/workflows/ci.yml` job names and
// `.github/rulesets/main-required-checks.json`.
func firstVersionRequiredStatusChecks() []string {
	return []string{
		"Workflow Lint",
		"Secret Scan",
		"Go",
		"Web",
		"Compose Config",
		"DB Rehearsal",
		"Helm Chart",
		"Helm Smoke",
		"Docker Build (gateway)",
		"Docker Build (worker)",
		"Docker Build (node-worker)",
		"Docker Build (web)",
		"Go Vulnerability Check",
	}
}

func requiredCodeownerPatterns() []string {
	return []string{"*", "/backend/", "/web/", "/.github/", "/deploy/", "/docs/deploy-production.md", "/docs/deploy-helm.md", "/docs/github-branch-protection.md", "/Dockerfile", "/Makefile"}
}

func parseBackupScheduleSource(value string) (string, string, error) {
	kind, raw, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return "", "", fmt.Errorf("backup source must use artifact:NAME or path:/mounted/assops-*.dump")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("backup source value is required")
	}
	switch kind {
	case "artifact":
		if !isSafeWorkflowInput(raw) {
			return "", "", fmt.Errorf("backup artifact name must contain only letters, numbers, dot, underscore, or hyphen")
		}
	case "path":
		if !isSafeBackupPath(raw) {
			return "", "", fmt.Errorf("backup path contains unsupported characters")
		}
	default:
		return "", "", fmt.Errorf("backup source must use artifact:NAME or path:/mounted/assops-*.dump")
	}
	return kind, raw, nil
}

func isSafeWorkflowInput(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
