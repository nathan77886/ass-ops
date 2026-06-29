package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseBackupSchedulePlanForMountedPathSource(t *testing.T) {
	plan, err := releaseBackupSchedulePlan("nathan77886/ass-ops", "production", "self-hosted-prod", "23 2 * * 0", "path:/mnt/backups/assops-20260622-120000.dump", "30")
	if err != nil {
		t.Fatalf("releaseBackupSchedulePlan path source: %v", err)
	}
	for _, want := range []string{
		"runner-local backup path `/mnt/backups/assops-20260622-120000.dump`",
		"must be self-hosted",
		"ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_PATH=/mnt/backups/assops-20260622-120000.dump",
		"backup_artifact_name=''",
		"backup_path=\"/mnt/backups/assops-20260622-120000.dump\"",
		"Retained Backup Publication Contract",
		"must be mounted read-only on runner `self-hosted-prod`",
		"must handle backup retention, checksum publication, and deletion outside this workflow",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("path schedule plan missing %q in:\n%s", want, plan)
		}
	}
	if strings.Contains(plan, "ASSOPS_REHEARSAL_DATABASE_PASSWORD=") {
		t.Fatalf("path schedule plan should not include secret values:\n%s", plan)
	}
}

func TestReleaseBranchProtectionPlan(t *testing.T) {
	plan, err := releaseBranchProtectionPlan("nathan77886/ass-ops", "../../../.github/rulesets/main-required-checks.json", "../../../.github/CODEOWNERS")
	if err != nil {
		t.Fatalf("releaseBranchProtectionPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Branch Protection Plan",
		"Repository: `nathan77886/ass-ops`",
		"Ruleset template: `../../../.github/rulesets/main-required-checks.json`",
		"Ruleset name: `ASSOPS default branch required checks`",
		"Enforcement: `active`",
		"Ruleset targets `~DEFAULT_BRANCH`",
		"Branch deletion and non-fast-forward pushes are blocked",
		"code owner review",
		"Required status checks are strict/fresh",
		"`Workflow Lint`",
		"`Secret Scan`",
		"`Go Vulnerability Check`",
		"`/backend/`",
		"`/web/`",
		"`/.github/`",
		"`/deploy/`",
		"local validation only; it does not call GitHub",
		"Administration: write",
		"gh api",
		"/repos/nathan77886/ass-ops/rulesets",
		"--input \"../../../.github/rulesets/main-required-checks.json\"",
		"/repos/nathan77886/ass-ops/rules/branches/<default-branch>",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("branch protection plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		"Authorization:",
		"token",
		"password",
		"PRIVATE KEY",
	} {
		if strings.Contains(strings.ToLower(plan), strings.ToLower(forbidden)) {
			t.Fatalf("branch protection plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseBranchProtectionPlanRejectsMissingRequiredCheck(t *testing.T) {
	dir := t.TempDir()
	rulesetPath := filepath.Join(dir, "ruleset.json")
	codeownersPath := filepath.Join(dir, "CODEOWNERS")
	ruleset := `{
		"name": "ASSOPS default branch required checks",
		"target": "branch",
		"enforcement": "active",
		"conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
		"rules": [
			{"type": "deletion"},
			{"type": "non_fast_forward"},
			{"type": "pull_request", "parameters": {
				"dismiss_stale_reviews_on_push": true,
				"require_code_owner_review": true,
				"require_last_push_approval": true,
				"required_approving_review_count": 1,
				"required_review_thread_resolution": true
			}},
			{"type": "required_status_checks", "parameters": {
				"strict_required_status_checks_policy": true,
				"required_status_checks": [{"context": "Go"}]
			}}
		]
	}`
	if err := os.WriteFile(rulesetPath, []byte(ruleset), 0o600); err != nil {
		t.Fatalf("write ruleset: %v", err)
	}
	if err := os.WriteFile(codeownersPath, []byte(strings.Join([]string{
		"* @nathan77886",
		"/backend/ @nathan77886",
		"/web/ @nathan77886",
		"/.github/ @nathan77886",
		"/deploy/ @nathan77886",
		"/docs/deploy-production.md @nathan77886",
		"/docs/deploy-helm.md @nathan77886",
		"/docs/github-branch-protection.md @nathan77886",
		"/Dockerfile @nathan77886",
		"/Makefile @nathan77886",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write codeowners: %v", err)
	}

	_, err := releaseBranchProtectionPlan("nathan77886/ass-ops", rulesetPath, codeownersPath)
	if err == nil || !strings.Contains(err.Error(), "Workflow Lint") {
		t.Fatalf("releaseBranchProtectionPlan error = %v, want missing Workflow Lint", err)
	}
}

func TestReleaseBranchProtectionPlanRejectsIncompleteCodeowners(t *testing.T) {
	dir := t.TempDir()
	rulesetPath := filepath.Join(dir, "ruleset.json")
	codeownersPath := filepath.Join(dir, "CODEOWNERS")
	rulesetBytes, err := os.ReadFile("../../../.github/rulesets/main-required-checks.json")
	if err != nil {
		t.Fatalf("read ruleset fixture: %v", err)
	}
	if err := os.WriteFile(rulesetPath, rulesetBytes, 0o600); err != nil {
		t.Fatalf("write ruleset: %v", err)
	}
	if err := os.WriteFile(codeownersPath, []byte("* @nathan77886\n/backend/ @nathan77886\n"), 0o600); err != nil {
		t.Fatalf("write codeowners: %v", err)
	}

	_, err = releaseBranchProtectionPlan("nathan77886/ass-ops", rulesetPath, codeownersPath)
	if err == nil || !strings.Contains(err.Error(), "/web/") {
		t.Fatalf("releaseBranchProtectionPlan error = %v, want missing /web/", err)
	}
}

func TestReleaseBranchProtectionPlanRejectsUnsafeInputs(t *testing.T) {
	dir := t.TempDir()
	badJSONPath := filepath.Join(dir, "bad.json")
	badOwnersPath := filepath.Join(dir, "CODEOWNERS")
	if err := os.WriteFile(badJSONPath, []byte(`not-json`), 0o600); err != nil {
		t.Fatalf("write bad json: %v", err)
	}
	if err := os.WriteFile(badOwnersPath, []byte("* nathan77886\n"), 0o600); err != nil {
		t.Fatalf("write bad codeowners: %v", err)
	}
	cases := []struct {
		name string
		repo string
		rule string
		own  string
		want string
	}{
		{
			name: "invalid repo",
			repo: "nathan77886",
			rule: "../../../.github/rulesets/main-required-checks.json",
			own:  "../../../.github/CODEOWNERS",
			want: "owner/repo",
		},
		{
			name: "empty ruleset",
			repo: "nathan77886/ass-ops",
			rule: "",
			own:  "../../../.github/CODEOWNERS",
			want: "ruleset JSON path",
		},
		{
			name: "bad json",
			repo: "nathan77886/ass-ops",
			rule: badJSONPath,
			own:  "../../../.github/CODEOWNERS",
			want: "parsing ruleset JSON",
		},
		{
			name: "invalid owner",
			repo: "nathan77886/ass-ops",
			rule: "../../../.github/rulesets/main-required-checks.json",
			own:  badOwnersPath,
			want: "invalid owner",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseBranchProtectionPlan(tc.repo, tc.rule, tc.own)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseBranchProtectionPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestProductionRestoreRehearsalWorkflowValidatesArtifactContents(t *testing.T) {
	content, err := os.ReadFile("../../../.github/workflows/production-restore-rehearsal.yml")
	if err != nil {
		t.Fatalf("read production restore rehearsal workflow: %v", err)
	}
	source := string(content)
	for _, want := range []string{
		"Retained backup artifact must contain exactly one assops-*.dump file",
		"-iname '.env*'",
		"-iname '*kubeconfig*'",
		"-iname '*.log'",
		"-iname '*.key'",
		"-iname '*.pem'",
		"Retained backup artifact contains disallowed secret/log-like files",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("production restore rehearsal workflow missing %q", want)
		}
	}
}

func TestProductionRetainedBackupWorkflowGuardrails(t *testing.T) {
	content, err := os.ReadFile("../../../.github/workflows/production-retained-backup.yml")
	if err != nil {
		t.Fatalf("read production retained backup workflow: %v", err)
	}
	source := string(content)
	for _, want := range []string{
		"ASSOPS_PRODUCTION_RETAINED_BACKUP_ENABLED == 'true'",
		"production-retained-backup-${{",
		"cancel-in-progress: false",
		"name: ${{ github.event_name == 'workflow_dispatch' && inputs.github_environment || vars.ASSOPS_PRODUCTION_RETAINED_BACKUP_ENVIRONMENT || 'production' }}",
		"DATABASE_URL: ${{ secrets.ASSOPS_ACTIVE_DATABASE_URL }}",
		"PGPASSWORD: ${{ secrets.ASSOPS_ACTIVE_DATABASE_PASSWORD }}",
		"ASSOPS_ACTIVE_DATABASE_URL environment secret is required",
		"set +x",
		"bin/assops-tool db backup-retain .assops/retained-backups \"$INPUT_KEEP_COUNT\"",
		"Retained backup artifact must contain exactly one assops-*.dump file",
		"-iname '.env*'",
		"-iname '*kubeconfig*'",
		"-iname '*.log'",
		"-iname '*.key'",
		"-iname '*.pem'",
		"No database URL, password, kubeconfig, or raw log files are written to the artifact staging directory.",
		"actions/upload-artifact@v7",
		"retention-days: ${{ env.INPUT_RETENTION_DAYS }}",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("production retained backup workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"pg_dump -Fc",
		"pg_dump --file",
		"/tmp/assops-backup-retain.json",
		"cat /tmp/assops-backup-retain.json",
		"echo \"$DATABASE_URL\"",
		"echo $DATABASE_URL",
		"ASSOPS_ACTIVE_DATABASE_URL=",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("production retained backup workflow contains forbidden pattern %q", forbidden)
		}
	}
}
