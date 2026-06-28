package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func templateProviderReviewExecutionGuardrailWithStaging(provider, reviewKind, sourceBranch, targetBranch string, enableRequested, mutationArmingRequested, starterFilePayloadStaged bool) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch = strings.TrimSpace(sourceBranch)
	targetBranch = strings.TrimSpace(targetBranch)
	branchReady := sourceBranch != "" && targetBranch != "" && isSafeGitRefPart(sourceBranch) && isSafeGitRefPart(targetBranch)
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	adapterReady := adapterStatus == "planned"
	configStatus := "blocked"
	configMessage := "Set ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION=true only after provider branch, commit, and review adapters are ready."
	if enableRequested {
		configStatus = "ready"
		configMessage = "Provider review execution was explicitly requested by configuration."
	}
	mutationReady := enableRequested && mutationArmingRequested
	mutationStatus := "blocked"
	mutationMessage := "Set ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION=true and ASSOPS_ARM_PROVIDER_REVIEW_MUTATION=true only after provider review rehearsal evidence is reviewed."
	if mutationReady {
		mutationStatus = "ready"
		mutationMessage = "Provider review mutation arming was explicitly requested by configuration; live provider API calls remain disabled."
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
			"status":            mutationStatus,
			"required_config":   "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
			"provider_type":     provider,
			"review_kind":       reviewKind,
			"adapter_status":    adapterStatus,
			"message":           mutationMessage,
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
	if mutationReady {
		mode = "mutation_armed_audit_only"
	}
	return map[string]any{
		"execution_mode":           mode,
		"execution_enabled":        false,
		"execution_enabled_config": enableRequested,
		"mutation_armed_config":    mutationReady,
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
