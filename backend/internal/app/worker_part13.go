package app

import (
	"fmt"
	"strings"
)

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func templateNameWithSuffix(base, suffix, fallbackSuffix string) string {
	suffix = firstNonEmptyString(suffix, fallbackSuffix)
	if base == "" {
		return suffix
	}
	if suffix == "" || strings.HasSuffix(base, "-"+suffix) {
		return base
	}
	return base + "-" + suffix
}

func templateDisplayName(base, suffix, fallbackSuffix string) string {
	suffix = firstNonEmptyString(suffix, fallbackSuffix)
	if base == "" {
		return suffix
	}
	if suffix == "" || strings.HasSuffix(base, " "+suffix) {
		return base
	}
	return base + " " + suffix
}

func mapRemoteIDs(remotes []map[string]any) []string {
	ids := make([]string, 0, len(remotes))
	for _, remote := range remotes {
		id := strings.TrimSpace(fmt.Sprint(remote["id"]))
		if id != "" && id != "<nil>" {
			ids = append(ids, id)
		}
	}
	return ids
}

func mapTemplateFileIDs(files []map[string]any) []string {
	ids := make([]string, 0, len(files))
	for _, file := range files {
		id := strings.TrimSpace(fmt.Sprint(file["id"]))
		if id != "" && id != "<nil>" {
			ids = append(ids, id)
		}
	}
	return ids
}

func templateFileSummaries(files []map[string]any) []map[string]any {
	summaries := make([]map[string]any, 0, len(files))
	for _, file := range files {
		summaries = append(summaries, map[string]any{
			"id":     file["id"],
			"path":   file["path"],
			"kind":   file["kind"],
			"status": file["status"],
		})
	}
	return summaries
}

func mapSliceFromAny(value any) []map[string]any {
	if typed, ok := value.([]map[string]any); ok {
		return typed
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		mapped := mapFromAny(item)
		if len(mapped) > 0 {
			out = append(out, mapped)
		}
	}
	return out
}

func boolFromMap(input map[string]any, key string) bool {
	return boolDefaultFromMap(input, key, false)
}

func boolDefaultFromMap(input map[string]any, key string, fallback bool) bool {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return fallback
}

func completeTemplateSteps(value any, project, repo map[string]any, remotes []map[string]any, syncAsset map[string]any, files []map[string]any) []map[string]any {
	stepsAny, _ := value.([]any)
	if len(stepsAny) == 0 {
		stepsAny = []any{
			map[string]any{"key": "project", "title": "Create project asset"},
			map[string]any{"key": "repository", "title": "Create repository metadata"},
			map[string]any{"key": "remotes", "title": "Bind repository remotes"},
			map[string]any{"key": "repo_sync", "title": "Configure repo sync asset"},
			map[string]any{"key": "files", "title": "Plan starter repository files"},
		}
	}
	steps := make([]map[string]any, 0, len(stepsAny))
	for _, item := range stepsAny {
		step := mapFromAny(item)
		key := stringFromMap(step, "key")
		switch key {
		case "project":
			step["status"] = "completed"
			step["project_id"] = project["id"]
		case "repository":
			if repo != nil {
				step["status"] = "completed"
				step["repository_id"] = repo["id"]
			} else {
				step["status"] = "planned"
			}
		case "remotes":
			if len(remotes) > 0 {
				step["status"] = "completed"
				step["remote_ids"] = mapRemoteIDs(remotes)
			} else {
				step["status"] = "planned"
				step["reason"] = "template parameters must include remotes before repo sync can be attached automatically"
			}
		case "repo_sync":
			if syncAsset != nil {
				step["status"] = "completed"
				step["repo_sync_asset_id"] = syncAsset["id"]
			} else {
				step["status"] = "planned"
				step["reason"] = "source_remote_id and target_remote_id are required after remotes are attached"
			}
		case "files":
			if len(files) > 0 {
				step["status"] = "completed"
				step["template_file_ids"] = mapTemplateFileIDs(files)
			} else {
				step["status"] = "planned"
				step["reason"] = "template defaults or parameters must include files"
			}
		default:
			step["status"] = "planned"
		}
		steps = append(steps, step)
	}
	return steps
}

func completeTemplateStepsWithRepositoryProvision(steps []map[string]any, provision *gitExecutionResult) []map[string]any {
	if provision == nil || provision.Details == nil {
		return steps
	}
	for _, step := range steps {
		switch stringFromMap(step, "key") {
		case "repository":
			step["status"] = "completed"
			step["repository_provisioned"] = true
			step["commit_sha"] = provision.AfterSHA
			step["remote_id"] = provision.Details["remote_id"]
		case "files":
			if count, ok := provision.Details["file_count"].(int); ok && count > 0 {
				step["status"] = "completed"
				step["pushed"] = true
				step["commit_sha"] = provision.AfterSHA
			}
		}
	}
	return steps
}

func templateStepsWithProvisionRetry(value any) []map[string]any {
	input := mapSliceFromAny(value)
	steps := make([]map[string]any, 0, len(input))
	for _, item := range input {
		step := mapFromAny(item)
		switch stringFromMap(step, "key") {
		case "repository", "files":
			step["status"] = "provisioning"
			delete(step, "error")
		}
		steps = append(steps, step)
	}
	return steps
}

func templateStepsWithProvisionFailure(value any) []map[string]any {
	input := mapSliceFromAny(value)
	steps := make([]map[string]any, 0, len(input))
	for _, item := range input {
		step := mapFromAny(item)
		switch stringFromMap(step, "key") {
		case "repository", "files":
			step["status"] = "failed"
		}
		steps = append(steps, step)
	}
	return steps
}

func templateStepsWithStatus(value any, status string) []map[string]any {
	stepsAny, _ := value.([]any)
	steps := make([]map[string]any, 0, len(stepsAny))
	for _, item := range stepsAny {
		step := mapFromAny(item)
		step["status"] = status
		steps = append(steps, step)
	}
	return steps
}

func hasTemplateSteps(value any) bool {
	steps, ok := value.([]any)
	return ok && len(steps) > 0
}

func mergeGitExecutionResult(result map[string]any, execution *gitExecutionResult) {
	if execution == nil {
		return
	}
	result["stdout"] = execution.Stdout
	result["stderr"] = execution.Stderr
	result["after_sha"] = execution.AfterSHA
	result["details"] = execution.Details
}

func (w *ControlWorker) newGitExecutor(workDir string) *GitExecutor {
	executor := NewGitExecutor(workDir)
	executor.LocalBareBaseDirs = w.cfg.LocalBareBaseDirs
	return executor
}

func mergeRepoTagLookupExecutionResult(result map[string]any, execution *gitExecutionResult) {
	if execution == nil {
		return
	}
	for key, value := range execution.Details {
		result[key] = value
	}
	result["matched_sha"] = execution.AfterSHA
	result["matched_sha_present"] = execution.AfterSHA != ""
	result["raw_git_output_recorded"] = false
	result["remote_url_recorded"] = false
	result["credentials_recorded"] = false
	result["contains_token"] = false
}

func gitExecutionOutputFromMap(result map[string]any) (string, string) {
	stdout, _ := result["stdout"].(string)
	stderr, _ := result["stderr"].(string)
	return stdout, stderr
}
