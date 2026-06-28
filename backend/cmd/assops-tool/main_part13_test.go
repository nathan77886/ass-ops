package main

import (
	"strings"
	"testing"
)

func TestReleaseDemoImportPlanRejectsUnsafeInput(t *testing.T) {
	cases := []struct {
		name   string
		slug   string
		origin string
		want   string
	}{
		{name: "empty slug", slug: "", origin: "https://assops.example.com", want: "project slug"},
		{name: "slash slug", slug: "owner/repo", origin: "https://assops.example.com", want: "project slug"},
		{name: "space slug", slug: "assops demo", origin: "https://assops.example.com", want: "project slug"},
		{name: "dot slug", slug: ".", origin: "https://assops.example.com", want: "project slug"},
		{name: "parent dot slug", slug: "..", origin: "https://assops.example.com", want: "project slug"},
		{name: "trailing dot slug", slug: "assops.", origin: "https://assops.example.com", want: "project slug"},
		{name: "unsafe origin", slug: "assops-demo", origin: "http://assops.example.com", want: "must use https"},
		{name: "origin path", slug: "assops-demo", origin: "https://assops.example.com/import", want: "must not include a path"},
		{name: "origin userinfo", slug: "assops-demo", origin: "https://user:pass@assops.example.com", want: "must not include userinfo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseDemoImportPlan(tc.slug, tc.origin)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseDemoImportPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleasePodLogRehearsalPlan(t *testing.T) {
	plan, err := releasePodLogRehearsalPlan("ASSOPS-Demo", "https://assops-staging.example.com/", "Prod", "billing-api")
	if err != nil {
		t.Fatalf("releasePodLogRehearsalPlan: %v", err)
	}
	if _, err := releasePodLogRehearsalPlan("assops-demo", "https://assops-staging.example.com", "prod", strings.Repeat("a", 63)); err != nil {
		t.Fatalf("releasePodLogRehearsalPlan should accept 63-character namespace: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Argo Pod Log Rehearsal Plan",
		"Project slug: `assops-demo`",
		"Public origin: `https://assops-staging.example.com`",
		"Environment: `prod`",
		"Namespace: `billing-api`",
		"does not read kubeconfig, create Kubernetes clients, call Argo/Kubernetes, open log streams, or write ASSOPS rows",
		"Bind a namespace-scoped kubeconfig secret out of band",
		"Create the ASSOPS `argo.pod_logs` approval request",
		"Record only sanitized result metadata",
		"deployment target linked to project and Argo app",
		"RBAC read pods/logs review completed",
		"log redaction review completed",
		"kubeconfig bodies",
		"raw Kubernetes or Argo responses",
		"raw log bodies and redacted log bodies",
		"does not read kubeconfig, call Kubernetes or Argo, open a log stream, create approvals, enqueue workers, record snapshots, or store log bodies",
		"assops-tool project readiness",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("pod log rehearsal plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		"Authorization:",
		"token=",
		"password=",
		"PRIVATE KEY",
		"BEGIN CERTIFICATE",
		"kubeconfig:",
		"log body:",
		"https://user:",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("pod log rehearsal plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleasePodLogRehearsalPlanRejectsUnsafeInput(t *testing.T) {
	cases := []struct {
		name        string
		slug        string
		origin      string
		environment string
		namespace   string
		want        string
	}{
		{name: "empty slug", slug: "", origin: "https://assops.example.com", environment: "prod", namespace: "billing", want: "project slug"},
		{name: "leading dot slug", slug: ".assops", origin: "https://assops.example.com", environment: "prod", namespace: "billing", want: "project slug"},
		{name: "trailing dot slug", slug: "assops.", origin: "https://assops.example.com", environment: "prod", namespace: "billing", want: "project slug"},
		{name: "unsafe origin", slug: "assops-demo", origin: "http://assops.example.com", environment: "prod", namespace: "billing", want: "must use https"},
		{name: "origin path", slug: "assops-demo", origin: "https://assops.example.com/logs", environment: "prod", namespace: "billing", want: "must not include a path"},
		{name: "origin query", slug: "assops-demo", origin: "https://assops.example.com?token=bad", environment: "prod", namespace: "billing", want: "must not include query or fragment"},
		{name: "origin fragment", slug: "assops-demo", origin: "https://assops.example.com#logs", environment: "prod", namespace: "billing", want: "must not include query or fragment"},
		{name: "origin userinfo", slug: "assops-demo", origin: "https://user:pass@assops.example.com", environment: "prod", namespace: "billing", want: "must not include userinfo"},
		{name: "empty environment", slug: "assops-demo", origin: "https://assops.example.com", environment: "", namespace: "billing", want: "environment"},
		{name: "environment slash", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod/us", namespace: "billing", want: "environment"},
		{name: "environment leading dot", slug: "assops-demo", origin: "https://assops.example.com", environment: ".prod", namespace: "billing", want: "environment"},
		{name: "environment trailing dot", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod.", namespace: "billing", want: "environment"},
		{name: "empty namespace", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod", namespace: "", want: "namespace"},
		{name: "namespace uppercase", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod", namespace: "Billing", want: "lowercase"},
		{name: "namespace underscore", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod", namespace: "billing_api", want: "namespace"},
		{name: "namespace leading hyphen", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod", namespace: "-billing", want: "namespace"},
		{name: "namespace trailing hyphen", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod", namespace: "billing-", want: "namespace"},
		{name: "namespace too long", slug: "assops-demo", origin: "https://assops.example.com", environment: "prod", namespace: strings.Repeat("a", 64), want: "namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releasePodLogRehearsalPlan(tc.slug, tc.origin, tc.environment, tc.namespace)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releasePodLogRehearsalPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleaseSSHRehearsalPlan(t *testing.T) {
	plan, err := releaseSSHRehearsalPlan("ASSOPS-Demo", "Prod")
	if err != nil {
		t.Fatalf("releaseSSHRehearsalPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS SSH Target Rehearsal Plan",
		"Project slug: `assops-demo`",
		"Environment: `prod`",
		"does not accept hostnames, usernames, SSH key paths, runbook URLs, fixture IDs, operator names, or command text as inputs",
		"does not read SSH keys, read known_hosts, open sockets, start SSH processes, enqueue workers, create approvals, write operation logs, or record snapshots",
		"approval-gated `ssh.verify` rehearsal",
		"low-risk `ssh.exec` rehearsal command",
		"operation-to-command-to-machine graph chains exist for both verify and exec",
		"target-environment proof snapshot status",
		"ssh rehearsal attestation snapshot status",
		"hostnames, IP addresses, usernames, and ports",
		"SSH private keys, public keys, known_hosts bodies, and key paths",
		"commands, arguments, stdout, stderr, exit errors, and raw adapter output",
		"does not probe environments, read key material, start SSH, create approvals, enqueue workers, write operation logs, sync assets, or record snapshots",
		"assops-tool project readiness",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("ssh rehearsal plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		"Authorization:",
		"token=",
		"password=",
		"PRIVATE KEY",
		"BEGIN OPENSSH",
		"known_hosts:",
		"stdout:",
		"stderr:",
		"10.0.0.",
		"ssh://",
		"deploy@",
		"/etc/assops/ssh",
		"runbooks.example.com",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("ssh rehearsal plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseSSHRehearsalPlanRejectsUnsafeInput(t *testing.T) {
	cases := []struct {
		name        string
		slug        string
		environment string
		want        string
	}{
		{name: "empty slug", slug: "", environment: "prod", want: "project slug"},
		{name: "slash slug", slug: "owner/repo", environment: "prod", want: "project slug"},
		{name: "leading dot slug", slug: ".assops", environment: "prod", want: "project slug"},
		{name: "trailing dot slug", slug: "assops.", environment: "prod", want: "project slug"},
		{name: "empty environment", slug: "assops-demo", environment: "", want: "environment"},
		{name: "environment slash", slug: "assops-demo", environment: "prod/us", want: "environment"},
		{name: "environment leading dot", slug: "assops-demo", environment: ".prod", want: "environment"},
		{name: "environment trailing dot", slug: "assops-demo", environment: "prod.", want: "environment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseSSHRehearsalPlan(tc.slug, tc.environment)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseSSHRehearsalPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleaseTagRehearsalPlan(t *testing.T) {
	plan, err := releaseTagRehearsalPlan("ASSOPS-Demo", "GitHub-Main")
	if err != nil {
		t.Fatalf("releaseTagRehearsalPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS GitHub Tag Rehearsal Plan",
		"Project slug: `assops-demo`",
		"Remote key: `github-main`",
		"does not accept tag names, commit SHAs, branches, remote URLs, workflow URLs, provider run IDs, token names, messages, or Git output as inputs",
		"does not call GitHub, run Git, create or push tags, refresh Actions, write operation logs, update repo_tag_runs, sync assets, or record snapshots",
		"approval-gated remote-specific tag operation",
		"read-only live tag lookup",
		"GitHub Actions refresh",
		"repo_tag_run to github_action_run canonical graph edge",
		"sanitized tag-result snapshot status",
		"sanitized Actions refresh snapshot status",
		"tag names, branch names, commit SHAs, and target refs",
		"provider tokens, authorization headers, credentials, and token env names",
		"Git stdout/stderr",
		"does not call providers, run Git, create tags, push refs, refresh Actions, enqueue workers, write operation logs, sync assets, or record snapshots",
		"assops-tool project readiness",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("tag rehearsal plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		"Authorization:",
		"token=",
		"password=",
		"PRIVATE KEY",
		"refs/tags/",
		"refs/heads/",
		"abcdef0123456789abcdef0123456789abcdef01",
		"https://github.com/",
		"workflow run:",
		"provider response:",
		"git push",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("tag rehearsal plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseTagRehearsalPlanRejectsUnsafeInput(t *testing.T) {
	cases := []struct {
		name      string
		slug      string
		remoteKey string
		want      string
	}{
		{name: "empty slug", slug: "", remoteKey: "github-main", want: "project slug"},
		{name: "slash slug", slug: "owner/repo", remoteKey: "github-main", want: "project slug"},
		{name: "leading dot slug", slug: ".assops", remoteKey: "github-main", want: "project slug"},
		{name: "trailing dot slug", slug: "assops.", remoteKey: "github-main", want: "project slug"},
		{name: "empty remote key", slug: "assops-demo", remoteKey: "", want: "remote key"},
		{name: "remote key slash", slug: "assops-demo", remoteKey: "github/main", want: "remote key"},
		{name: "remote key leading dot", slug: "assops-demo", remoteKey: ".github", want: "remote key"},
		{name: "remote key trailing dot", slug: "assops-demo", remoteKey: "github.", want: "remote key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseTagRehearsalPlan(tc.slug, tc.remoteKey)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseTagRehearsalPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
