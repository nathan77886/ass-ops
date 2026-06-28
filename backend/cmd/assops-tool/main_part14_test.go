package main

import (
	"strings"
	"testing"
)

func TestReleaseConfigRehearsalPlan(t *testing.T) {
	plan, err := releaseConfigRehearsalPlan("ASSOPS-Demo", "GitHub-Config")
	if err != nil {
		t.Fatalf("releaseConfigRehearsalPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Config Repository Rehearsal Plan",
		"Project slug: `assops-demo`",
		"Remote key: `github-config`",
		"does not accept branch names, commit SHAs, refs, remote URLs, file contents, provider URLs, token names, Git output, provider responses, or operator notes as inputs",
		"does not run Git, create files, commit, push, call providers, update ProjectVersion rows, enqueue workers, write operation logs, sync assets, pin config commits, or record snapshots",
		"`repo_role=config`",
		"approval-gated `config.git_commit` audit workflow",
		"read-only config refs refresh (`git.refs.refresh`)",
		"config ref-refresh snapshot status",
		"config promotion snapshot status",
		"dry-run pin-config-commit result",
		"branch names, commit SHAs, refs, and remote URLs",
		"Git stdout/stderr",
		"does not run Git, create files, commit, push refs, call providers, update ProjectVersion rows, enqueue workers, write operation logs, sync assets, pin config commits, or record snapshots",
		"assops-tool db pin-config-commit --project-version-id <project-version-id> --repository-id <config-repository-id> --remote-id <config-remote-id> --dry-run",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("config rehearsal plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		"Authorization:",
		"token=",
		"password=",
		"PRIVATE KEY",
		"refs/heads/",
		"abcdef0123456789abcdef0123456789abcdef01",
		"https://github.com/",
		"values.yaml:",
		"application.yaml:",
		"provider response:",
		"git push",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("config rehearsal plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseConfigRehearsalPlanRejectsUnsafeInput(t *testing.T) {
	cases := []struct {
		name      string
		slug      string
		remoteKey string
		want      string
	}{
		{name: "empty slug", slug: "", remoteKey: "github-config", want: "project slug"},
		{name: "slash slug", slug: "owner/repo", remoteKey: "github-config", want: "project slug"},
		{name: "leading dot slug", slug: ".assops", remoteKey: "github-config", want: "project slug"},
		{name: "trailing dot slug", slug: "assops.", remoteKey: "github-config", want: "project slug"},
		{name: "empty remote key", slug: "assops-demo", remoteKey: "", want: "remote key"},
		{name: "remote key slash", slug: "assops-demo", remoteKey: "github/config", want: "remote key"},
		{name: "remote key leading dot", slug: "assops-demo", remoteKey: ".github", want: "remote key"},
		{name: "remote key trailing dot", slug: "assops-demo", remoteKey: "github.", want: "remote key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseConfigRehearsalPlan(tc.slug, tc.remoteKey)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseConfigRehearsalPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleaseAgentCodeRehearsalPlan(t *testing.T) {
	plan, err := releaseAgentCodeRehearsalPlan("ASSOPS-Demo", "Codex-CLI")
	if err != nil {
		t.Fatalf("releaseAgentCodeRehearsalPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Agent Code Rehearsal Plan",
		"Project slug: `assops-demo`",
		"Runtime key: `codex-cli`",
		"does not accept repository URLs, workspace paths, branch names, prompts, tool input/output, patch content, diff content, file contents, test commands, command output, provider URLs, token names, or credentials as inputs",
		"does not start Codex CLI, materialize runtime config, checkout source, bind workspaces, create branches, prepare or apply patches, run tests, invoke commit_push_spark, commit, push, create provider reviews, update agent tasks, write operation logs, sync assets, or record snapshots",
		"`context.generate` evidence",
		"audit-only `agent.execute` operation",
		"Codex execution plan",
		"patch preparation audit rows",
		"source checkout and branch-policy preflight",
		"code modification execution arming plan",
		"sanitized tool-call audit snapshot status",
		"sanitized tool-arming snapshot status",
		"sanitized code-audit snapshot status",
		"source checkout, patch application, test execution, commit/push, and provider review creation remain disabled",
		"assops-tool project readiness",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("agent code rehearsal plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		// Agent code plans must never include concrete source, patch, command, provider, or credential examples.
		// Descriptive suppressed-material labels such as "tokens" remain allowed above.
		"Authorization:",
		"token=",
		"password=",
		"PRIVATE KEY",
		"https://github.com/",
		"git@github.com:",
		"/mnt/ssd/",
		"refs/heads/",
		"abcdef0123456789abcdef0123456789abcdef01",
		"diff --git",
		"@@ -",
		"provider response:",
		"git push",
		"go test ./...",
		"OPENAI_API_KEY",
		"GITHUB_TOKEN",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("agent code rehearsal plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseAgentCodeRehearsalPlanRejectsUnsafeInput(t *testing.T) {
	cases := []struct {
		name       string
		slug       string
		runtimeKey string
		want       string
	}{
		{name: "empty slug", slug: "", runtimeKey: "codex-cli", want: "project slug"},
		{name: "slash slug", slug: "owner/repo", runtimeKey: "codex-cli", want: "project slug"},
		{name: "leading dot slug", slug: ".assops", runtimeKey: "codex-cli", want: "project slug"},
		{name: "trailing dot slug", slug: "assops.", runtimeKey: "codex-cli", want: "project slug"},
		{name: "empty runtime key", slug: "assops-demo", runtimeKey: "", want: "runtime key"},
		{name: "runtime key slash", slug: "assops-demo", runtimeKey: "codex/cli", want: "runtime key"},
		{name: "runtime key leading dot", slug: "assops-demo", runtimeKey: ".codex", want: "runtime key"},
		{name: "runtime key trailing dot", slug: "assops-demo", runtimeKey: "codex.", want: "runtime key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseAgentCodeRehearsalPlan(tc.slug, tc.runtimeKey)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseAgentCodeRehearsalPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleaseAgentToolRehearsalPlan(t *testing.T) {
	plan, err := releaseAgentToolRehearsalPlan("ASSOPS-Demo", "Codex-CLI")
	if err != nil {
		t.Fatalf("releaseAgentToolRehearsalPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Agent Tool Rehearsal Plan",
		"Project slug: `assops-demo`",
		"Runtime key: `codex-cli`",
		"does not accept prompts, runtime config, environment variables, tool input/output, raw tool input/output, workspace paths, repository URLs, patch content, diff content, provider URLs, token names, credentials, or worker secret material as inputs",
		"does not invoke tools, materialize runtime config, materialize tool input, record tool output, start Codex CLI, apply patches, mutate repositories, call providers, update agent tasks, write operation logs, sync assets, or record snapshots",
		"audit-only `agent.execute` operation",
		"`context.generate`, `runtime.check`, `codex.execution.plan`, and `patch.prepare`",
		"terminal sanitized agent_tool_calls audit evidence",
		"sanitized result callback observation",
		"tool execution arming plan state",
		"allowlisted tool-invocation review plan state",
		"sanitized tool-call audit snapshot status",
		"sanitized tool-arming snapshot status",
		"worker tool invocation, tool input materialization, tool output recording, Codex CLI process start, patch apply, repository mutation, and provider calls disabled",
		"assops-tool project readiness",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("agent tool rehearsal plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		// Agent tool plans must never include concrete prompt, tool I/O, provider, repository, or credential examples.
		// Descriptive suppressed-material labels such as "tokens" remain allowed above.
		"Authorization:",
		"token=",
		"password=",
		"PRIVATE KEY",
		"https://github.com/",
		"git@github.com:",
		"/mnt/ssd/",
		"refs/heads/",
		"abcdef0123456789abcdef0123456789abcdef01",
		"actual tool output",
		"do-not-serialize",
		"provider response:",
		"diff --git",
		"OPENAI_API_KEY",
		"GITHUB_TOKEN",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("agent tool rehearsal plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseAgentToolRehearsalPlanRejectsUnsafeInput(t *testing.T) {
	cases := []struct {
		name       string
		slug       string
		runtimeKey string
		want       string
	}{
		{name: "empty slug", slug: "", runtimeKey: "codex-cli", want: "project slug"},
		{name: "slash slug", slug: "owner/repo", runtimeKey: "codex-cli", want: "project slug"},
		{name: "leading dot slug", slug: ".assops", runtimeKey: "codex-cli", want: "project slug"},
		{name: "trailing dot slug", slug: "assops.", runtimeKey: "codex-cli", want: "project slug"},
		{name: "empty runtime key", slug: "assops-demo", runtimeKey: "", want: "runtime key"},
		{name: "runtime key slash", slug: "assops-demo", runtimeKey: "codex/cli", want: "runtime key"},
		{name: "runtime key leading dot", slug: "assops-demo", runtimeKey: ".codex", want: "runtime key"},
		{name: "runtime key trailing dot", slug: "assops-demo", runtimeKey: "codex.", want: "runtime key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseAgentToolRehearsalPlan(tc.slug, tc.runtimeKey)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseAgentToolRehearsalPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleaseBackupSchedulePlanForArtifactSource(t *testing.T) {
	plan, err := releaseBackupSchedulePlan("nathan77886/ass-ops", "production", "ubuntu-latest", "17 3 * * 1", "artifact:retained-assops-backup", "14")
	if err != nil {
		t.Fatalf("releaseBackupSchedulePlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Production Backup Schedule Plan",
		"production-restore-rehearsal.yml",
		"ASSOPS_REHEARSAL_DATABASE_URL",
		"ASSOPS_ACTIVE_DATABASE_URL",
		"ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENABLED=true",
		"ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_ARTIFACT=retained-assops-backup",
		"backup_artifact_name=\"retained-assops-backup\"",
		"backup_path=''",
		"Retained Backup Publication Contract",
		"Publication must be produced by the environment-owned retained backup job",
		"must contain exactly one `assops-*.dump` backup",
		"production-retained-backup.yml",
		"raw `pg_dump` custom-format file",
		"external storage, additional encryption, and large-database handling remain environment-owned",
		"must stay unexpired for at least `14 days`",
		"do not include `.env`, database URLs, kubeconfigs, or raw logs",
		"The checked-in schedule is `17 3 * * 1`",
		"external",
	} {
		if want == "external" {
			if strings.Contains(plan, "ASSOPS_REHEARSAL_DATABASE_PASSWORD=") {
				t.Fatalf("schedule plan should not include secret values:\n%s", plan)
			}
			continue
		}
		if !strings.Contains(plan, want) {
			t.Fatalf("schedule plan missing %q in:\n%s", want, plan)
		}
	}
}
