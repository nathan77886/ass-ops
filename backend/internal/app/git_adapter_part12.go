package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

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
	appendGitExecutionOutput(result, sanitizeGitOutputForLocalPath(stdout, gitDir), sanitizeGitOutputForLocalPath(stderr, gitDir))
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
	appendGitExecutionOutput(result, sanitizeGitOutputForLocalPath(stdout, gitDir), sanitizeGitOutputForLocalPath(stderr, gitDir))
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
		if isLocalBareRemote(remote) {
			return remote
		}
	}
	return nil
}

func isLocalBareRemote(remote map[string]any) bool {
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(remote["provider_type"])), "local_bare") ||
		strings.EqualFold(strings.TrimSpace(fmt.Sprint(remote["kind"])), "local_bare")
}

func (e *GitExecutor) validateExistingLocalBareRemote(ctx context.Context, result *gitExecutionResult, remote map[string]any, remoteURL, label string) error {
	if !isLocalBareRemote(remote) {
		return nil
	}
	if result != nil && result.Details != nil {
		result.Details[label+"_local_bare_remote"] = true
	}
	if !safeLocalBareRemotePath(remoteURL, e.LocalBareBaseDirs) {
		return fmt.Errorf("%s local_bare remote_url must be under an allowed absolute base directory", label)
	}
	if !safeResolvedLocalBareRemotePath(remoteURL, e.LocalBareBaseDirs) {
		return fmt.Errorf("%s local_bare remote_url resolves outside allowed base directories", label)
	}
	if err := e.ensureBareRepository(ctx, result, remoteURL); err != nil {
		return fmt.Errorf("%s local_bare remote is invalid: %w", label, err)
	}
	if result != nil && result.Details != nil {
		result.Details[label+"_local_bare_path_allowed"] = true
		result.Details[label+"_local_bare_bare_repository"] = true
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
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		target, err = filepath.EvalSymlinks(filepath.Dir(path))
	}
	if err != nil {
		return false
	}
	target, err = filepath.Abs(target)
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
		rel, err := filepath.Rel(resolvedBase, target)
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
	appendGitExecutionOutput(result, sanitizeGitOutput(stdout), sanitizeGitOutput(stderr))
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
