package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasePromotionPlanAcceptsReleaseMetadataOverlay(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-v0.1.0-linux-amd64.tar.gz": "binary",
		"assops-web-v0.1.0.tar.gz":         "web",
		"assops-0.1.0.tgz":                 "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")
	valuesPath := filepath.Join(artifactDir, "helm-values.yaml")
	t.Setenv("ASSOPS_RELEASE_COMMIT", "abc123def456")
	t.Setenv("ASSOPS_RELEASE_BUILD_TIME", "2026-06-26T12:34:56Z")
	values, err := releaseHelmValues("nathan77886", "v0.1.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	if err := writeTextFile(valuesPath, values); err != nil {
		t.Fatalf("write values: %v", err)
	}
	t.Setenv("ASSOPS_RELEASE_COMMIT", "")
	t.Setenv("ASSOPS_RELEASE_BUILD_TIME", "")

	if _, err := releasePromotionPlan("nathan77886/ass-ops", "nathan77886", "v0.1.0", artifactDir, reportPath, valuesPath); err != nil {
		t.Fatalf("releasePromotionPlan should accept metadata-only overlay drift: %v", err)
	}
}

func TestReleasePromotionPlanRejectsMismatchedHelmValues(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-v0.1.0-linux-amd64.tar.gz": "binary",
		"assops-web-v0.1.0.tar.gz":         "web",
		"assops-0.1.0.tgz":                 "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")
	valuesPath := filepath.Join(artifactDir, "helm-values.yaml")
	values, err := releaseHelmValues("nathan77886", "v0.2.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	if err := writeTextFile(valuesPath, values); err != nil {
		t.Fatalf("write values: %v", err)
	}

	_, err = releasePromotionPlan("nathan77886/ass-ops", "nathan77886", "v0.1.0", artifactDir, reportPath, valuesPath)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatched helm values error, got %v", err)
	}
}

func TestReleaseHelmReadinessPlanForProductionExample(t *testing.T) {
	plan, err := releaseHelmReadinessPlan("../../../deploy/helm/assops/values.production.example.yaml")
	if err != nil {
		t.Fatalf("releaseHelmReadinessPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Helm Environment Readiness Plan",
		"values.production.example.yaml",
		"Values sha256:",
		"External Secret is required",
		"assops-production-secret",
		"Built-in PostgreSQL is disabled",
		"TLS ingress",
		"assops.example.com",
		"assops-production-tls",
		"ServiceAccount token automount is disabled",
		"NetworkPolicy and PodDisruptionBudget are enabled",
		"`DATABASE_URL`",
		"`ASSOPS_JWT_SECRET`",
		"`ASSOPS_ARGO_READ_TOKEN`",
		"`context`: `5Gi`",
		"storageClass `assops-retain`",
		"`backups`: `20Gi`",
		"`postgres`: external managed PostgreSQL; no chart-managed PostgreSQL PVC is rendered.",
		"does not call Kubernetes, Helm, Argo, GitHub, or cloud APIs",
		"does not render manifests, bind kubeconfigs, read external Secret values, or write deployment records",
		"helm lint deploy/helm/assops",
		"helm template assops deploy/helm/assops",
		"kubectl -n assops get secret \"assops-production-secret\"",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("helm readiness plan missing %q in:\n%s", want, plan)
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
			t.Fatalf("helm readiness plan should not contain %q:\n%s", forbidden, plan)
		}
	}
}

func TestReleaseHelmReadinessPlanRejectsUnsafeProductionOverlay(t *testing.T) {
	dir := t.TempDir()
	valuesPath := filepath.Join(dir, "values.yaml")
	values := strings.Join([]string{
		"gatewayURL: http://assops.example.com",
		"secret:",
		"  create: true",
		"postgres:",
		"  enabled: true",
		"ingress:",
		"  enabled: true",
		"  className: nginx",
		"  host: assops.example.com",
		"  tlsSecretName: \"\"",
		"serviceAccount:",
		"  automountServiceAccountToken: true",
		"networkPolicy:",
		"  enabled: false",
		"podDisruptionBudget:",
		"  enabled: false",
	}, "\n")
	if err := os.WriteFile(valuesPath, []byte(values), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}

	_, err := releaseHelmReadinessPlan(valuesPath)
	if err == nil || !strings.Contains(err.Error(), "secret.create=false") {
		t.Fatalf("releaseHelmReadinessPlan error = %v, want secret.create=false", err)
	}
}

func TestReleaseHelmReadinessPlanRejectsSpecificUnsafeFields(t *testing.T) {
	fixture, err := os.ReadFile("../../../deploy/helm/assops/values.production.example.yaml")
	if err != nil {
		t.Fatalf("read production values fixture: %v", err)
	}
	cases := []struct {
		name    string
		oldText string
		newText string
		want    string
	}{
		{
			name:    "gateway url must be https",
			oldText: "gatewayURL: https://assops.example.com",
			newText: "gatewayURL: http://assops.example.com",
			want:    "gatewayURL to use https",
		},
		{
			name:    "gateway url must not contain credentials",
			oldText: "gatewayURL: https://assops.example.com",
			newText: "gatewayURL: https://user:pass@assops.example.com",
			want:    "gatewayURL without embedded credentials",
		},
		{
			name:    "web service stays internal",
			oldText: "  service:\n    type: ClusterIP",
			newText: "  service:\n    type: LoadBalancer",
			want:    "web.service.type=ClusterIP",
		},
		{
			name:    "persistent volumes enabled",
			oldText: "  context:\n    enabled: true",
			newText: "  context:\n    enabled: false",
			want:    "persistence.context.enabled=true",
		},
		{
			name:    "storage class must be explicit",
			oldText: "    storageClassName: assops-retain",
			newText: "    storageClassName: \"\"",
			want:    "persistence.context.storageClassName",
		},
		{
			name:    "storage class key must exist",
			oldText: "    storageClassName: assops-retain\n  bareRepos:",
			newText: "  bareRepos:",
			want:    "persistence.context.storageClassName",
		},
		{
			name:    "resource requests required",
			oldText: "    cpu: 100m",
			newText: "    cpu: \"\"",
			want:    "resources.requests.cpu",
		},
		{
			name:    "default capabilities drop all",
			oldText: "          - ALL",
			newText: "          - NET_BIND_SERVICE",
			want:    "securityContext.containers.default.capabilities.drop to include ALL",
		},
		{
			name:    "web privilege escalation disabled",
			oldText: "    web:\n      allowPrivilegeEscalation: false",
			newText: "    web:\n      allowPrivilegeEscalation: true",
			want:    "securityContext.containers.web.allowPrivilegeEscalation=false",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			valuesPath := filepath.Join(dir, "values.yaml")
			content := strings.Replace(string(fixture), tc.oldText, tc.newText, 1)
			if content == string(fixture) {
				t.Fatalf("fixture replacement did not change content for %s", tc.name)
			}
			if err := os.WriteFile(valuesPath, []byte(content), 0o600); err != nil {
				t.Fatalf("write values: %v", err)
			}
			_, err := releaseHelmReadinessPlan(valuesPath)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseHelmReadinessPlan error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReadSimpleHelmValues(t *testing.T) {
	dir := t.TempDir()
	valuesPath := filepath.Join(dir, "values.yaml")
	content := strings.Join([]string{
		"# comment",
		"root:",
		"  child:",
		"    name: \"value # not comment\" # real comment",
		"    list:",
		"      - ALL",
		"      - NET_BIND_SERVICE",
		"    quoted: ' spaced value '",
	}, "\n")
	if err := os.WriteFile(valuesPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	values, err := readSimpleHelmValues(valuesPath)
	if err != nil {
		t.Fatalf("readSimpleHelmValues: %v", err)
	}
	if values["root.child.name"] != "value # not comment" {
		t.Fatalf("quoted comment value = %q", values["root.child.name"])
	}
	if values["root.child.list"] != "ALL,NET_BIND_SERVICE" {
		t.Fatalf("list value = %q", values["root.child.list"])
	}
	if values["root.child.quoted"] != "spaced value" {
		t.Fatalf("quoted value = %q", values["root.child.quoted"])
	}

	tabPath := filepath.Join(dir, "tabs.yaml")
	if err := os.WriteFile(tabPath, []byte("root:\n\tchild: value\n"), 0o600); err != nil {
		t.Fatalf("write tabs: %v", err)
	}
	if _, err := readSimpleHelmValues(tabPath); err == nil || !strings.Contains(err.Error(), "uses tabs") {
		t.Fatalf("readSimpleHelmValues tab error = %v", err)
	}
}
