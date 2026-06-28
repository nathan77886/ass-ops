package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func writeJSONReport(path string, value any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("report path is required")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating report directory: %w", err)
		}
	}
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	return nil
}

func writeTextFile(path, value string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("output path is required")
	}
	if hasParentDirTraversal(path) {
		return fmt.Errorf("output path must not contain parent directory traversal")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

func hasParentDirTraversal(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}

func releaseHelmValues(owner, version string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	version = strings.TrimSpace(version)
	if owner == "" {
		return "", fmt.Errorf("GHCR owner is required")
	}
	if strings.Contains(owner, "/") {
		return "", fmt.Errorf("GHCR owner must be an owner or organization name, not owner/repo")
	}
	if !isContainerPathSegment(owner) {
		return "", fmt.Errorf("GHCR owner contains unsupported characters")
	}
	if version == "" {
		return "", fmt.Errorf("release version is required")
	}
	if strings.ContainsAny(version, " \t\r\n") {
		return "", fmt.Errorf("release version must not contain whitespace")
	}
	commit := safeReleaseMetadataValue(os.Getenv("ASSOPS_RELEASE_COMMIT"), "release-commit-not-set")
	buildTime := safeReleaseMetadataValue(os.Getenv("ASSOPS_RELEASE_BUILD_TIME"), "release-build-time-not-set")
	return fmt.Sprintf(`image:
  registry: ghcr.io
  pullPolicy: IfNotPresent
  gateway:
    repository: %s/assops-gateway
    tag: %s
  worker:
    repository: %s/assops-worker
    tag: %s
  nodeWorker:
    repository: %s/assops-node-worker
    tag: %s
  web:
    repository: %s/assops-web
    tag: %s
env:
  version: %s
  commit: %s
  buildTime: %s
`, owner, version, owner, version, owner, version, owner, version, quoteYAMLString(version), quoteYAMLString(commit), quoteYAMLString(buildTime)), nil
}

func safeReleaseMetadataValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n\t") {
		return fallback
	}
	return value
}

func quoteYAMLString(value string) string {
	return strconv.Quote(value)
}

func releasePromotionPlan(repo, owner, version, artifactDir, rehearsalReport, helmValuesPath string) (string, error) {
	repo = strings.TrimSpace(repo)
	owner = strings.ToLower(strings.TrimSpace(owner))
	version = strings.TrimSpace(version)
	artifactDir = strings.TrimSpace(artifactDir)
	rehearsalReport = strings.TrimSpace(rehearsalReport)
	helmValuesPath = strings.TrimSpace(helmValuesPath)
	if !isOwnerRepo(repo) {
		return "", fmt.Errorf("repository must be owner/repo")
	}
	if _, err := releaseHelmValues(owner, version); err != nil {
		return "", err
	}
	if helmValuesPath == "" {
		return "", fmt.Errorf("Helm values path is required")
	}
	helmValuesDigest, err := validateReleaseHelmValuesFile(helmValuesPath, owner, version)
	if err != nil {
		return "", err
	}
	bundle, err := validateReleaseBundle(artifactDir, rehearsalReport)
	if err != nil {
		return "", err
	}
	artifacts, _ := bundle["artifacts"].(map[string]any)
	binaries := stringSliceFromAny(artifacts["binaries"])
	web := stringSliceFromAny(artifacts["web"])
	helm := stringSliceFromAny(artifacts["helm"])
	if len(binaries) == 0 || len(web) == 0 || len(helm) == 0 {
		return "", fmt.Errorf("release bundle artifact summary is incomplete")
	}
	images := []string{
		fmt.Sprintf("ghcr.io/%s/assops-gateway:%s", owner, version),
		fmt.Sprintf("ghcr.io/%s/assops-worker:%s", owner, version),
		fmt.Sprintf("ghcr.io/%s/assops-node-worker:%s", owner, version),
		fmt.Sprintf("ghcr.io/%s/assops-web:%s", owner, version),
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ASSOPS Promotion Plan %s\n\n", version)
	fmt.Fprintf(&b, "Repository: `%s`\n\n", repo)
	fmt.Fprintf(&b, "Artifact directory: `%s`\n\n", artifactDir)
	fmt.Fprintf(&b, "Restore rehearsal report: `%s`\n\n", rehearsalReport)
	fmt.Fprintf(&b, "Helm values overlay: `%s`\n\n", helmValuesPath)
	fmt.Fprintf(&b, "Helm values sha256: `%s`\n\n", helmValuesDigest)
	fmt.Fprintf(&b, "## Required Gates\n\n")
	fmt.Fprintf(&b, "- Release bundle checksum validation passed locally.\n")
	fmt.Fprintf(&b, "- Restore rehearsal report is present, redacted, and includes migrations/object counts.\n")
	fmt.Fprintf(&b, "- GitHub artifact attestations must be verified before promotion.\n")
	fmt.Fprintf(&b, "- GHCR image attestations must be verified before rollout.\n")
	fmt.Fprintf(&b, "- Helm values overlay pins all workloads to `%s`.\n\n", version)
	fmt.Fprintf(&b, "- A reviewed environment values overlay must be applied between `deploy/helm/assops/values.yaml` and the generated release overlay.\n\n")
	fmt.Fprintf(&b, "## Release Artifacts\n\n")
	for _, name := range append(append(binaries, web...), helm...) {
		fmt.Fprintf(&b, "- `%s`\n", filepath.Join(artifactDir, name))
	}
	fmt.Fprintf(&b, "- `%s`\n\n", filepath.Join(artifactDir, "SHA256SUMS"))
	fmt.Fprintf(&b, "## Images\n\n")
	for _, image := range images {
		fmt.Fprintf(&b, "- `%s`\n", image)
	}
	fmt.Fprintf(&b, "\n## Verification Commands\n\n```bash\n")
	fmt.Fprintf(&b, "assops-tool release validate-bundle %q %q\n", artifactDir, rehearsalReport)
	for _, name := range append(append([]string{"SHA256SUMS"}, binaries...), append(web, helm...)...) {
		fmt.Fprintf(&b, "gh attestation verify %q --repo %s\n", filepath.Join(artifactDir, name), repo)
	}
	for _, image := range images {
		fmt.Fprintf(&b, "gh attestation verify %q --repo %s\n", "oci://"+image, repo)
	}
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Rollout Guardrails\n\n")
	fmt.Fprintf(&b, "- The production promotion workflow defaults to preflight-only; do not set `deploy=true` until the protected environment, namespace-scoped kubeconfig, previous values overlay, rollback point, and operator approval have been reviewed.\n")
	fmt.Fprintf(&b, "- When the deployed ASSOPS URL is reachable from the runner, set the workflow `smoke_url` input and protected `ASSOPS_ADMIN_EMAIL` / `ASSOPS_ADMIN_PASSWORD` environment secrets so `scripts/api-smoke.sh` verifies health, login, project listing, and worker queue summary after rollout.\n")
	fmt.Fprintf(&b, "- For private clusters without a public route, leave `smoke_url` empty and set `smoke_via_port_forward=true` so the workflow runs the same API smoke through `kubectl port-forward svc/<release>-web 18080:80`.\n")
	fmt.Fprintf(&b, "- Application pods should not need Kubernetes API credentials; keep rollout credentials isolated to the protected promotion workflow.\n\n")
	fmt.Fprintf(&b, "## Rollout Commands\n\n```bash\n")
	fmt.Fprintf(&b, "ENVIRONMENT_VALUES=<reviewed-environment-values.yaml>\n")
	fmt.Fprintf(&b, "helm template assops deploy/helm/assops -f deploy/helm/assops/values.yaml -f \"$ENVIRONMENT_VALUES\" -f %q >/tmp/assops-rendered.yaml\n", helmValuesPath)
	fmt.Fprintf(&b, "helm upgrade --install assops deploy/helm/assops --wait --wait-for-jobs --timeout 10m -f deploy/helm/assops/values.yaml -f \"$ENVIRONMENT_VALUES\" -f %q\n", helmValuesPath)
	fmt.Fprintf(&b, "ASSOPS_GATEWAY_URL=https://assops.example.com scripts/api-smoke.sh\n")
	fmt.Fprintf(&b, "kubectl -n assops port-forward svc/assops-web 18080:80\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Rollback Note\n\n")
	fmt.Fprintf(&b, "Keep the previous Helm values overlay and database backup path with the release notes before rollout.\n")
	return b.String(), nil
}
