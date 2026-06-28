package main

import (
	"fmt"
	"strconv"
	"strings"
)

func releaseBackupSchedulePlan(repo, environment, runner, cronExpr, backupSource, retentionDays string) (string, error) {
	repo = strings.TrimSpace(repo)
	environment = strings.TrimSpace(environment)
	runner = strings.TrimSpace(runner)
	cronExpr = strings.TrimSpace(cronExpr)
	backupSource = strings.TrimSpace(backupSource)
	retentionDays = strings.TrimSpace(retentionDays)
	if !isOwnerRepo(repo) {
		return "", fmt.Errorf("repository must be owner/repo")
	}
	if !isSafeWorkflowInput(environment) {
		return "", fmt.Errorf("environment must contain only letters, numbers, dot, underscore, or hyphen")
	}
	if !isSafeWorkflowInput(runner) {
		return "", fmt.Errorf("runner must contain only letters, numbers, dot, underscore, or hyphen")
	}
	if !isSafeCronExpression(cronExpr) {
		return "", fmt.Errorf("cron must be a five-field GitHub Actions cron expression without quotes or shell metacharacters")
	}
	retention, err := strconv.Atoi(retentionDays)
	if err != nil || retention < 1 || retention > 90 {
		return "", fmt.Errorf("artifact retention days must be between 1 and 90")
	}
	sourceKind, sourceValue, err := parseBackupScheduleSource(backupSource)
	if err != nil {
		return "", err
	}
	var artifactInput, pathInput, runnerNote string
	switch sourceKind {
	case "artifact":
		artifactInput = sourceValue
		runnerNote = "Any runner with repository Actions artifact read access can download the latest unexpired retained backup artifact by name."
	case "path":
		pathInput = sourceValue
		if runner == "ubuntu-latest" {
			return "", fmt.Errorf("backup path sources require a self-hosted runner that mounts the retained backup store")
		}
		runnerNote = "The selected runner must be self-hosted and mount the retained backup store read-only."
	default:
		return "", fmt.Errorf("unsupported backup source kind %q", sourceKind)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Production Backup Schedule Plan\n\n")
	fmt.Fprintf(&b, "Repository: `%s`\n\n", repo)
	fmt.Fprintf(&b, "Workflow: `.github/workflows/production-restore-rehearsal.yml`\n\n")
	fmt.Fprintf(&b, "GitHub environment: `%s`\n\n", environment)
	fmt.Fprintf(&b, "Runner: `%s`\n\n", runner)
	fmt.Fprintf(&b, "Cron: `%s`\n\n", cronExpr)
	fmt.Fprintf(&b, "Artifact retention: `%d days`\n\n", retention)
	fmt.Fprintf(&b, "## Backup Source\n\n")
	if sourceKind == "artifact" {
		fmt.Fprintf(&b, "- Use workflow artifact `%s` containing exactly one retained `assops-*.dump` file.\n", sourceValue)
	} else {
		fmt.Fprintf(&b, "- Use runner-local backup path `%s`.\n", sourceValue)
	}
	fmt.Fprintf(&b, "- %s\n\n", runnerNote)
	fmt.Fprintf(&b, "## Retained Backup Publication Contract\n\n")
	fmt.Fprintf(&b, "- Publication must be produced by the environment-owned retained backup job after `assops-tool db backup-retain` succeeds.\n")
	fmt.Fprintf(&b, "- The published source must contain exactly one `assops-*.dump` backup for the rehearsal run.\n")
	fmt.Fprintf(&b, "- Publication metadata must record the backup timestamp, source environment, retention window, and checksum location without embedding database URLs or credentials.\n")
	fmt.Fprintf(&b, "- The rehearsal workflow is a consumer only; it must not create, rotate, delete, or overwrite retained backups.\n")
	if sourceKind == "artifact" {
		fmt.Fprintf(&b, "- The checked-in `.github/workflows/production-retained-backup.yml` workflow can publish artifact `%s`, but it is disabled until the protected environment secrets, runner, artifact retention, and repository variable gate are configured.\n", sourceValue)
		fmt.Fprintf(&b, "- GitHub artifact publication uploads a raw `pg_dump` custom-format file to private repository Actions storage; external storage, additional encryption, and large-database handling remain environment-owned.\n")
		fmt.Fprintf(&b, "- Artifact `%s` must stay unexpired for at least `%d days` and remain private to repository Actions readers until the rehearsal report is attached to release notes.\n", sourceValue, retention)
		fmt.Fprintf(&b, "- The artifact producer should upload only the dump and non-secret manifest/checksum metadata; do not include `.env`, database URLs, kubeconfigs, or raw logs.\n\n")
	} else {
		fmt.Fprintf(&b, "- Path `%s` must be mounted read-only on runner `%s`; the workflow only reads this file path and never publishes the backup itself.\n", sourceValue, runner)
		fmt.Fprintf(&b, "- The mounted backup store owner must handle backup retention, checksum publication, and deletion outside this workflow.\n\n")
	}
	fmt.Fprintf(&b, "## Required GitHub Environment Secrets\n\n")
	fmt.Fprintf(&b, "- `ASSOPS_REHEARSAL_DATABASE_URL`: disposable restore database URL; the database name must include rehearsal, restore, test, tmp, scratch, or disposable.\n")
	fmt.Fprintf(&b, "- `ASSOPS_REHEARSAL_DATABASE_PASSWORD`: optional password passed through `PGPASSWORD` when the URL omits a password.\n")
	fmt.Fprintf(&b, "- `ASSOPS_ACTIVE_DATABASE_URL`: optional guard value so `assops-tool` can reject a rehearsal target that equals the active database.\n\n")
	fmt.Fprintf(&b, "## Manual Dispatch Check\n\n")
	fmt.Fprintf(&b, "Run this once before enabling the scheduled workflow gate:\n\n```bash\n")
	fmt.Fprintf(&b, "gh workflow run production-restore-rehearsal.yml --repo %s \\\n", repo)
	fmt.Fprintf(&b, "  -f github_environment=%q \\\n", environment)
	fmt.Fprintf(&b, "  -f runner=%q \\\n", runner)
	if artifactInput != "" {
		fmt.Fprintf(&b, "  -f backup_artifact_name=%q \\\n", artifactInput)
		fmt.Fprintf(&b, "  -f backup_path='' \\\n")
	} else {
		fmt.Fprintf(&b, "  -f backup_artifact_name='' \\\n")
		fmt.Fprintf(&b, "  -f backup_path=%q \\\n", pathInput)
	}
	fmt.Fprintf(&b, "  -f artifact_retention_days=%q\n", strconv.Itoa(retention))
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Scheduled Workflow Variables\n\n")
	fmt.Fprintf(&b, "The workflow already contains a scheduled trigger. After the manual dispatch succeeds, set these repository variables to enable it:\n\n")
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENABLED=true`\n")
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENVIRONMENT=%s`\n", environment)
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_RUNNER=%s`\n", runner)
	if artifactInput != "" {
		fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_ARTIFACT=%s`\n", artifactInput)
		fmt.Fprintf(&b, "- Leave `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_PATH` unset.\n")
	} else {
		fmt.Fprintf(&b, "- Leave `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_ARTIFACT` unset.\n")
		fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_PATH=%s`\n", pathInput)
	}
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_REPORT_NAME=production-restore-rehearsal-scheduled`\n")
	fmt.Fprintf(&b, "- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_RETENTION_DAYS=%d`\n\n", retention)
	fmt.Fprintf(&b, "The checked-in schedule is `%s`; update the workflow cron separately only if the environment needs a different window.\n\n", cronExpr)
	fmt.Fprintf(&b, "Keep the workflow protected by the `%s` environment and preserve the one-source-only guard between the artifact and path variables.\n", environment)
	return b.String(), nil
}

func releaseBranchProtectionPlan(repo, rulesetPath, codeownersPath string) (string, error) {
	repo = strings.TrimSpace(repo)
	rulesetPath = strings.TrimSpace(rulesetPath)
	codeownersPath = strings.TrimSpace(codeownersPath)
	if !isOwnerRepo(repo) {
		return "", fmt.Errorf("repository must be owner/repo")
	}
	if rulesetPath == "" {
		return "", fmt.Errorf("ruleset JSON path is required")
	}
	if codeownersPath == "" {
		return "", fmt.Errorf("CODEOWNERS path is required")
	}
	ruleset, err := readBranchProtectionRuleset(rulesetPath)
	if err != nil {
		return "", err
	}
	codeowners, err := readCodeownersSummary(codeownersPath)
	if err != nil {
		return "", err
	}
	statusChecks := stringSliceFromAny(ruleset["required_status_checks"])
	if len(statusChecks) == 0 {
		return "", fmt.Errorf("ruleset required status checks are empty")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Branch Protection Plan\n\n")
	fmt.Fprintf(&b, "Repository: `%s`\n\n", repo)
	fmt.Fprintf(&b, "Ruleset template: `%s`\n\n", rulesetPath)
	fmt.Fprintf(&b, "CODEOWNERS: `%s`\n\n", codeownersPath)
	fmt.Fprintf(&b, "Ruleset name: `%s`\n\n", ruleset["name"])
	fmt.Fprintf(&b, "Target: `%s`\n\n", ruleset["target"])
	fmt.Fprintf(&b, "Enforcement: `%s`\n\n", ruleset["enforcement"])
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Ruleset targets `~DEFAULT_BRANCH` and keeps enforcement active.\n")
	fmt.Fprintf(&b, "- Branch deletion and non-fast-forward pushes are blocked.\n")
	fmt.Fprintf(&b, "- Pull requests require one approval, code owner review, last-pusher separation, stale-review dismissal, and resolved review threads.\n")
	fmt.Fprintf(&b, "- Required status checks are strict/fresh and match the first-version CI contract.\n")
	fmt.Fprintf(&b, "- CODEOWNERS has a default owner and path-specific owners for backend, web, GitHub governance, deploy, Dockerfile, and Makefile.\n\n")
	fmt.Fprintf(&b, "## Required Checks\n\n")
	for _, check := range statusChecks {
		fmt.Fprintf(&b, "- `%s`\n", check)
	}
	fmt.Fprintf(&b, "\n## CODEOWNERS Coverage\n\n")
	for _, pattern := range stringSliceFromAny(codeowners["required_patterns"]) {
		fmt.Fprintf(&b, "- `%s`\n", pattern)
	}
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local validation only; it does not call GitHub, create rulesets, mutate branch protection, or read secrets.\n")
	fmt.Fprintf(&b, "- Applying the ruleset requires a repository administrator with `Administration: write` permission.\n")
	fmt.Fprintf(&b, "- Review the target repository and default branch in GitHub before running the commands below.\n\n")
	fmt.Fprintf(&b, "## Apply Commands\n\n```bash\n")
	fmt.Fprintf(&b, "jq . %q\n", rulesetPath)
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  --method POST \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rulesets \\\n", repo)
	fmt.Fprintf(&b, "  --input %q\n", rulesetPath)
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Verify Commands\n\n```bash\n")
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rulesets\n", repo)
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rules/branches/<default-branch>\n", repo)
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Update Existing Ruleset\n\n")
	fmt.Fprintf(&b, "If the repository already has this ruleset, replace it only after comparing the active ruleset ID from the verification output:\n\n```bash\n")
	fmt.Fprintf(&b, "gh api \\\n")
	fmt.Fprintf(&b, "  --method PUT \\\n")
	fmt.Fprintf(&b, "  -H \"Accept: application/vnd.github+json\" \\\n")
	fmt.Fprintf(&b, "  -H \"X-GitHub-Api-Version: 2026-03-10\" \\\n")
	fmt.Fprintf(&b, "  /repos/%s/rulesets/<ruleset-id> \\\n", repo)
	fmt.Fprintf(&b, "  --input %q\n", rulesetPath)
	fmt.Fprintf(&b, "```\n")
	return b.String(), nil
}

func releaseCallbackRehearsalPlan(publicOrigin string) (string, error) {
	origin, err := normalizePublicCallbackOrigin(publicOrigin)
	if err != nil {
		return "", err
	}
	giteaEndpoint := origin + "/api/webhooks/gitea/<webhook-connection-id>"
	githubEndpoint := origin + "/api/webhooks/github/<webhook-connection-id>"
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Provider Callback Rehearsal Plan\n\n")
	fmt.Fprintf(&b, "Public origin: `%s`\n\n", origin)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- Public origin is HTTPS, has no path/query/fragment/userinfo, and is not localhost or a private IP literal.\n")
	fmt.Fprintf(&b, "- ASSOPS should expose provider callback routes under the same origin used by `ASSOPS_GATEWAY_URL`.\n")
	fmt.Fprintf(&b, "- This plan uses placeholder connection IDs and never embeds delivery IDs, provider URLs, payloads, tokens, headers, request bodies, provider responses, or operator notes.\n\n")
	fmt.Fprintf(&b, "## Provider Callback Endpoints\n\n")
	fmt.Fprintf(&b, "- Gitea push callback: `%s`\n", giteaEndpoint)
	fmt.Fprintf(&b, "- GitHub workflow callback: `%s`\n\n", githubEndpoint)
	fmt.Fprintf(&b, "## Operator Rehearsal Sequence\n\n")
	fmt.Fprintf(&b, "1. Set `ASSOPS_GATEWAY_URL=%s` in the staging environment and restart only after confirming the target environment.\n", origin)
	fmt.Fprintf(&b, "2. Create or rotate webhook connections in ASSOPS so the UI shows provider-specific callback URLs with connection IDs.\n")
	fmt.Fprintf(&b, "3. In the provider UI, configure the Gitea push webhook and GitHub workflow webhook with the ASSOPS callback URLs and provider-owned secret material.\n")
	fmt.Fprintf(&b, "4. Send one provider test delivery for each provider, then trigger one real low-risk repository event per provider.\n")
	fmt.Fprintf(&b, "5. Confirm ASSOPS records sanitized webhook event status, signature result, replay eligibility, and repo-sync binding evidence.\n")
	fmt.Fprintf(&b, "6. Record the local threshold decision audit, apply local threshold configuration, and then record the provider callback rehearsal snapshot from already-observed local evidence.\n")
	fmt.Fprintf(&b, "7. Compare provider-side delivery/limit metrics manually and store only safe counts/status in release notes; do not paste provider payloads or headers.\n\n")
	fmt.Fprintf(&b, "## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan is local validation only; it does not call providers, send test deliveries, fetch metrics, open tunnels, configure DNS, or write ASSOPS rows.\n")
	fmt.Fprintf(&b, "- Provider test delivery, provider metrics comparison, DNS/TLS setup, and public ingress verification remain operator-owned staging tasks.\n\n")
	fmt.Fprintf(&b, "## Evidence To Attach To Release Notes\n\n")
	for _, item := range []string{
		"public origin and TLS owner",
		"Gitea and GitHub callback routes observed in ASSOPS UI",
		"sanitized callback event counts by provider",
		"operator replay-proof state",
		"threshold decision audit id/count only",
		"applied threshold configuration keys only",
		"provider metrics comparison status without provider payloads",
		"provider callback rehearsal snapshot status",
	} {
		fmt.Fprintf(&b, "- `%s`\n", item)
	}
	return b.String(), nil
}
