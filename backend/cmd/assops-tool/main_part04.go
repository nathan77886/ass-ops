package main

import (
	"fmt"
	"strings"
)

func releaseHelmReadinessPlan(valuesPath string) (string, error) {
	valuesPath = strings.TrimSpace(valuesPath)
	if valuesPath == "" {
		return "", fmt.Errorf("Helm values path is required")
	}
	values, err := readSimpleHelmValues(valuesPath)
	if err != nil {
		return "", err
	}
	requiredBooleans := []struct {
		key  string
		want string
	}{
		{key: "secret.create", want: "false"},
		{key: "postgres.enabled", want: "false"},
		{key: "ingress.enabled", want: "true"},
		{key: "serviceAccount.automountServiceAccountToken", want: "false"},
		{key: "networkPolicy.enabled", want: "true"},
		{key: "podDisruptionBudget.enabled", want: "true"},
	}
	for _, required := range requiredBooleans {
		if got := values[required.key]; got != required.want {
			return "", fmt.Errorf("Helm readiness requires %s=%s", required.key, required.want)
		}
	}
	for _, key := range []string{"secret.name", "gatewayURL", "ingress.className", "ingress.host", "ingress.tlsSecretName"} {
		if strings.TrimSpace(values[key]) == "" {
			return "", fmt.Errorf("Helm readiness requires %s", key)
		}
	}
	if !strings.HasPrefix(values["gatewayURL"], "https://") {
		return "", fmt.Errorf("Helm readiness requires gatewayURL to use https")
	}
	if strings.Contains(values["gatewayURL"], "@") {
		return "", fmt.Errorf("Helm readiness requires gatewayURL without embedded credentials")
	}
	if values["web.service.type"] != "ClusterIP" {
		return "", fmt.Errorf("Helm readiness requires web.service.type=ClusterIP before ingress rollout")
	}
	for _, key := range []string{"persistence.context.enabled", "persistence.bareRepos.enabled", "persistence.ssh.enabled", "persistence.backups.enabled"} {
		if values[key] != "true" {
			return "", fmt.Errorf("Helm readiness requires %s=true", key)
		}
	}
	for _, key := range []string{"persistence.context.size", "persistence.bareRepos.size", "persistence.ssh.size", "persistence.backups.size"} {
		if strings.TrimSpace(values[key]) == "" {
			return "", fmt.Errorf("Helm readiness requires %s", key)
		}
	}
	for _, key := range []string{"persistence.context.storageClassName", "persistence.bareRepos.storageClassName", "persistence.ssh.storageClassName", "persistence.backups.storageClassName"} {
		if strings.TrimSpace(values[key]) == "" {
			return "", fmt.Errorf("Helm readiness requires %s to make storage class selection explicit", key)
		}
	}
	for _, key := range []string{"resources.requests.cpu", "resources.requests.memory", "resources.limits.cpu", "resources.limits.memory"} {
		if strings.TrimSpace(values[key]) == "" {
			return "", fmt.Errorf("Helm readiness requires %s", key)
		}
	}
	for _, key := range []string{
		"securityContext.containers.default.allowPrivilegeEscalation",
		"securityContext.containers.default.readOnlyRootFilesystem",
		"securityContext.containers.default.runAsNonRoot",
		"securityContext.containers.migrate.allowPrivilegeEscalation",
		"securityContext.containers.migrate.readOnlyRootFilesystem",
		"securityContext.containers.migrate.runAsNonRoot",
		"securityContext.containers.web.allowPrivilegeEscalation",
	} {
		want := "true"
		if strings.Contains(key, "allowPrivilegeEscalation") {
			want = "false"
		}
		if got := values[key]; got != want {
			return "", fmt.Errorf("Helm readiness requires %s=%s", key, want)
		}
	}
	for _, key := range []string{"securityContext.containers.default.capabilities.drop", "securityContext.containers.migrate.capabilities.drop"} {
		if !toolContainsString(strings.Split(values[key], ","), "ALL") {
			return "", fmt.Errorf("Helm readiness requires %s to include ALL", key)
		}
	}
	sum, err := sha256File(valuesPath)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Helm Environment Readiness Plan\n\n")
	fmt.Fprintf(&b, "Values overlay: `%s`\n\n", valuesPath)
	fmt.Fprintf(&b, "Values sha256: `%s`\n\n", sum)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- External Secret is required via `%s`; chart-managed production secrets are disabled.\n", values["secret.name"])
	fmt.Fprintf(&b, "- Built-in PostgreSQL is disabled; `DATABASE_URL` must point at managed PostgreSQL from the external Secret.\n")
	fmt.Fprintf(&b, "- HTTPS gateway and TLS ingress are configured for `%s` with ingress class `%s` and TLS Secret `%s`.\n", values["ingress.host"], values["ingress.className"], values["ingress.tlsSecretName"])
	fmt.Fprintf(&b, "- Application ServiceAccount token automount is disabled.\n")
	fmt.Fprintf(&b, "- NetworkPolicy and PodDisruptionBudget are enabled for first-version rollout review.\n")
	fmt.Fprintf(&b, "- Persistent volumes include explicit reviewed storage classes, resource requests/limits, and non-root/drop-capability runtime posture.\n\n")
	fmt.Fprintf(&b, "## Required External Secret Keys\n\n")
	for _, key := range requiredExternalSecretKeys() {
		fmt.Fprintf(&b, "- `%s`\n", key)
	}
	fmt.Fprintf(&b, "\n## Storage Review\n\n")
	for _, key := range []string{"context", "bareRepos", "ssh", "backups"} {
		fmt.Fprintf(&b, "- `%s`: `%s`, storageClass `%s`\n", key, values["persistence."+key+".size"], values["persistence."+key+".storageClassName"])
	}
	fmt.Fprintf(&b, "- `postgres`: external managed PostgreSQL; no chart-managed PostgreSQL PVC is rendered.\n")
	fmt.Fprintf(&b, "\n## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan reads only the local values file; it does not call Kubernetes, Helm, Argo, GitHub, or cloud APIs.\n")
	fmt.Fprintf(&b, "- It does not render manifests, bind kubeconfigs, read external Secret values, or write deployment records.\n\n")
	fmt.Fprintf(&b, "## Preflight Commands\n\n```bash\n")
	fmt.Fprintf(&b, "helm lint deploy/helm/assops -f %q\n", valuesPath)
	fmt.Fprintf(&b, "helm template assops deploy/helm/assops -f deploy/helm/assops/values.yaml -f %q >/tmp/assops-rendered.yaml\n", valuesPath)
	fmt.Fprintf(&b, "kubectl -n assops get secret %q\n", values["secret.name"])
	fmt.Fprintf(&b, "kubectl -n assops get secret %q\n", values["ingress.tlsSecretName"])
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run the `kubectl` checks only after confirming the target cluster, namespace, and kubeconfig out of band. Keep promotion in preflight-only mode until those checks, restore rehearsal, and operator approval are recorded.\n")
	return b.String(), nil
}

func releaseHelmTestReadinessPlan(valuesPath string) (string, error) {
	valuesPath = strings.TrimSpace(valuesPath)
	if valuesPath == "" {
		return "", fmt.Errorf("Helm test values path is required")
	}
	values, err := readSimpleHelmValues(valuesPath)
	if err != nil {
		return "", err
	}
	required := []struct {
		key  string
		want string
	}{
		{key: "secret.create", want: "false"},
		{key: "postgres.enabled", want: "false"},
		{key: "serviceAccount.automountServiceAccountToken", want: "false"},
		{key: "env.kubernetesLogsEnabled", want: "true"},
		{key: "web.service.type", want: "ClusterIP"},
	}
	for _, item := range required {
		if got := values[item.key]; got != item.want {
			return "", fmt.Errorf("Helm test readiness requires %s=%s", item.key, item.want)
		}
	}
	for _, key := range []string{
		"secret.name",
		"gatewayURL",
		"env.version",
		"env.commit",
		"env.buildTime",
		"env.kubeconfigSecretDir",
		"env.kubectlPath",
		"persistence.kubeconfigs.existingSecretName",
	} {
		if strings.TrimSpace(values[key]) == "" {
			return "", fmt.Errorf("Helm test readiness requires %s", key)
		}
	}
	if strings.Contains(values["gatewayURL"], "@") {
		return "", fmt.Errorf("Helm test readiness requires gatewayURL without embedded credentials")
	}
	if !strings.HasPrefix(values["env.kubeconfigSecretDir"], "/") {
		return "", fmt.Errorf("Helm test readiness requires env.kubeconfigSecretDir to be an absolute pod path")
	}
	sum, err := sha256File(valuesPath)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Helm Test Environment Readiness Plan\n\n")
	fmt.Fprintf(&b, "Values overlay: `%s`\n\n", valuesPath)
	fmt.Fprintf(&b, "Values sha256: `%s`\n\n", sum)
	fmt.Fprintf(&b, "## Local Validation\n\n")
	fmt.Fprintf(&b, "- External application Secret is required via `%s`; chart-managed test secrets are disabled.\n", values["secret.name"])
	fmt.Fprintf(&b, "- Built-in PostgreSQL is disabled; `DATABASE_URL` must point at the reviewed test database from the external Secret.\n")
	fmt.Fprintf(&b, "- Application ServiceAccount token automount is disabled.\n")
	fmt.Fprintf(&b, "- Kubernetes pod-log metadata audits are enabled and use kubeconfig files mounted from Secret `%s` at `%s`.\n", values["persistence.kubeconfigs.existingSecretName"], values["env.kubeconfigSecretDir"])
	fmt.Fprintf(&b, "- Web stays internal as `ClusterIP`; verify access through port-forward, ingress, or another reviewed test entrypoint.\n")
	fmt.Fprintf(&b, "- Health metadata should report version `%s`, commit `%s`, and build time `%s` unless a private release overlay overrides them.\n\n", values["env.version"], values["env.commit"], values["env.buildTime"])
	fmt.Fprintf(&b, "## Required External Secret Keys\n\n")
	for _, key := range requiredExternalSecretKeys() {
		fmt.Fprintf(&b, "- `%s`\n", key)
	}
	fmt.Fprintf(&b, "\n## Required Kubeconfig Secret\n\n")
	fmt.Fprintf(&b, "- Secret: `%s`\n", values["persistence.kubeconfigs.existingSecretName"])
	fmt.Fprintf(&b, "- Mount path: `%s`\n", values["env.kubeconfigSecretDir"])
	fmt.Fprintf(&b, "- UI `kubeconfig_secret_ref` should be a Secret key or safe relative path below that mount.\n")
	fmt.Fprintf(&b, "- Store only reviewed namespace-scoped kubeconfig files; do not paste kubeconfig content into ASSOPS.\n\n")
	fmt.Fprintf(&b, "## No-Call Boundary\n\n")
	fmt.Fprintf(&b, "- This plan reads only the local values file; it does not call Kubernetes, Helm, Argo, GitHub, or cloud APIs.\n")
	fmt.Fprintf(&b, "- It does not render manifests, bind kubeconfigs, read external Secret values, fetch pod logs, or write deployment records.\n\n")
	fmt.Fprintf(&b, "## Preflight Commands\n\n```bash\n")
	fmt.Fprintf(&b, "helm lint deploy/helm/assops -f %q\n", valuesPath)
	fmt.Fprintf(&b, "helm template assops deploy/helm/assops -n assops-test -f deploy/helm/assops/values.yaml -f %q >/tmp/assops-test-rendered.yaml\n", valuesPath)
	fmt.Fprintf(&b, "kubectl -n assops-test get secret %q\n", values["secret.name"])
	fmt.Fprintf(&b, "kubectl -n assops-test get secret %q\n", values["persistence.kubeconfigs.existingSecretName"])
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Run the `kubectl` checks only after confirming the test cluster, namespace, and kubeconfig out of band. After install, verify gateway, worker, node-worker, web rollouts, then query `/healthz` through the web Service.\n")
	return b.String(), nil
}
