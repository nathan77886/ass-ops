package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmStorageClassContract(t *testing.T) {
	files := map[string][]string{
		"../../../deploy/helm/assops/values.yaml": {
			"storageClassName: \"\"",
		},
		"../../../deploy/helm/assops/values.production.example.yaml": {
			"storageClassName: assops-retain",
		},
		"../../../deploy/helm/assops/values.schema.json": {
			"\"storageClassName\": { \"type\": \"string\" }",
		},
		"../../../deploy/helm/assops/templates/pvc.yaml": {
			".Values.persistence.context.storageClassName",
			"storageClassName: {{ .Values.persistence.context.storageClassName | quote }}",
			".Values.persistence.bareRepos.storageClassName",
			".Values.persistence.ssh.storageClassName",
			".Values.persistence.backups.storageClassName",
		},
		"../../../deploy/helm/assops/templates/postgres.yaml": {
			".Values.postgres.storageClassName",
			"storageClassName: {{ .Values.postgres.storageClassName | quote }}",
		},
	}
	for path, wants := range files {
		t.Run(path, func(t *testing.T) {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			source := string(content)
			for _, want := range wants {
				if !strings.Contains(source, want) {
					t.Fatalf("%s missing %q", path, want)
				}
			}
		})
	}
}

func TestReleaseHelmTestReadinessPlanForTestExample(t *testing.T) {
	plan, err := releaseHelmTestReadinessPlan("../../../deploy/helm/assops/values.test.example.yaml")
	if err != nil {
		t.Fatalf("releaseHelmTestReadinessPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Helm Test Environment Readiness Plan",
		"values.test.example.yaml",
		"Values sha256:",
		"External application Secret is required",
		"assops-test-secret",
		"Built-in PostgreSQL is disabled",
		"ServiceAccount token automount is disabled",
		"pod-log metadata audits are enabled",
		"encrypted kubeconfig secrets stored in the database",
		"`DATABASE_URL`",
		"`ASSOPS_JWT_SECRET`",
		"`ASSOPS_ARGO_READ_TOKEN`",
		"`kubeconfig_secret_ref`",
		"`kubeconfig_secret`",
		"does not call Kubernetes, Helm, Argo, GitHub, or cloud APIs",
		"does not render manifests, bind kubeconfigs, read external Secret values, fetch pod logs, or write deployment records",
		"helm lint deploy/helm/assops",
		"helm template assops deploy/helm/assops -n assops-test",
		"kubectl -n assops-test get secret \"assops-test-secret\"",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("helm test readiness plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		"ASSOPS_JWT_SECRET=",
		"ASSOPS_ADMIN_PASSWORD=",
		"KUBE_CONFIG_B64=",
		"Authorization:",
		"PRIVATE" + " KEY",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("helm test readiness plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseHelmTestReadinessPlanRejectsUnsafeFields(t *testing.T) {
	fixture, err := os.ReadFile("../../../deploy/helm/assops/values.test.example.yaml")
	if err != nil {
		t.Fatalf("read test values fixture: %v", err)
	}
	cases := []struct {
		name    string
		oldText string
		newText string
		want    string
	}{
		{
			name:    "external secret required",
			oldText: "  create: false",
			newText: "  create: true",
			want:    "secret.create=false",
		},
		{
			name:    "external postgres required",
			oldText: "  enabled: false",
			newText: "  enabled: true",
			want:    "postgres.enabled=false",
		},
		{
			name:    "pod logs enabled",
			oldText: "  kubernetesLogsEnabled: \"true\"",
			newText: "  kubernetesLogsEnabled: \"false\"",
			want:    "env.kubernetesLogsEnabled=true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := strings.Replace(string(fixture), tc.oldText, tc.newText, 1)
			valuesPath := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(valuesPath, []byte(values), 0o600); err != nil {
				t.Fatalf("write values: %v", err)
			}
			_, err := releaseHelmTestReadinessPlan(valuesPath)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseHelmTestReadinessPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleaseCallbackRehearsalPlan(t *testing.T) {
	plan, err := releaseCallbackRehearsalPlan("https://assops-staging.example.com/")
	if err != nil {
		t.Fatalf("releaseCallbackRehearsalPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Provider Callback Rehearsal Plan",
		"Public origin: `https://assops-staging.example.com`",
		"Public origin is HTTPS",
		"/api/webhooks/gitea/<webhook-connection-id>",
		"/api/webhooks/github/<webhook-connection-id>",
		"ASSOPS_GATEWAY_URL=https://assops-staging.example.com",
		"provider test delivery",
		"provider metrics comparison",
		"threshold decision audit",
		"provider callback rehearsal snapshot",
		"does not call providers, send test deliveries, fetch metrics, open tunnels, configure DNS, or write ASSOPS rows",
		"Provider test delivery, provider metrics comparison, DNS/TLS setup, and public ingress verification remain operator-owned",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("callback rehearsal plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		"Authorization:",
		"token=",
		"secret=",
		"payload:",
		"X-GitHub-Delivery:",
		"X-Gitea-Delivery:",
		"PRIVATE KEY",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("callback rehearsal plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseCallbackRehearsalPlanRejectsUnsafeOrigins(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		want   string
	}{
		{name: "empty", origin: "", want: "public origin is required"},
		{name: "http", origin: "http://assops.example.com", want: "must use https"},
		{name: "path", origin: "https://assops.example.com/callback", want: "must not include a path"},
		{name: "query", origin: "https://assops.example.com?token=bad", want: "must not include query or fragment"},
		{name: "fragment", origin: "https://assops.example.com#callback", want: "must not include query or fragment"},
		{name: "userinfo", origin: "https://user:pass@assops.example.com", want: "must not include userinfo"},
		{name: "localhost", origin: "https://localhost", want: "public staging hostname"},
		{name: "local domain", origin: "https://assops.local", want: "public staging hostname"},
		{name: "local domain with port", origin: "https://assops.local:443", want: "public staging hostname"},
		{name: "private ip", origin: "https://10.0.0.5", want: "must not use localhost, private, link-local, or unspecified IPs"},
		{name: "loopback ip", origin: "https://127.0.0.1", want: "must not use localhost, private, link-local, or unspecified IPs"},
		{name: "ipv6 loopback", origin: "https://[::1]", want: "must not use localhost, private, link-local, or unspecified IPs"},
		{name: "ipv6 link local", origin: "https://[fe80::1]", want: "must not use localhost, private, link-local, or unspecified IPs"},
		{name: "ipv6 multicast", origin: "https://[ff02::1]", want: "must not use localhost, private, link-local, or unspecified IPs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseCallbackRehearsalPlan(tc.origin)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseCallbackRehearsalPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReleaseDemoImportPlan(t *testing.T) {
	plan, err := releaseDemoImportPlan("ASSOPS-Demo", "https://assops-staging.example.com")
	if err != nil {
		t.Fatalf("releaseDemoImportPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Live Demo Import Plan",
		"Project slug: `assops-demo`",
		"Public origin: `https://assops-staging.example.com`",
		"does not call providers, run Git, create repositories, write ASSOPS rows, or read credentials",
		"Create or select the real Gitea source repository and GitHub mirror repository",
		"Define the RepoSyncAsset from the Gitea remote to the GitHub remote",
		"record-demo-readiness-snapshot --project-slug assops-demo --dry-run",
		"project asset and project graph node",
		"at least two project-owned Git remote assets",
		"provider callback event linked to RepoSyncAsset or sync operation",
		"remote clone URLs",
		"provider tokens or webhook secrets",
		"Git stdout/stderr",
		"does not create/import rows, call Gitea/GitHub, run Git, replay webhooks, or record snapshots",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("demo import plan missing %q in:\n%s", want, plan)
		}
	}
	for _, forbidden := range []string{
		// Config plans must never include concrete ref, file, provider, or command-output examples.
		// Descriptive suppressed-material labels such as "token names" remain allowed above.
		"Authorization:",
		"token=",
		"password=",
		"PRIVATE KEY",
		"provider response:",
		"https://user:",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("demo import plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}
