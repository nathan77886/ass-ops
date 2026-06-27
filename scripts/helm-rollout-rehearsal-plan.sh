#!/usr/bin/env bash
set -euo pipefail

repo="${ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO:-}"
ghcr_owner="${ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER:-}"
version="${ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION:-}"
namespace="${ASSOPS_HELM_ROLLOUT_REHEARSAL_NAMESPACE:-assops}"
release="${ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE:-assops}"
environment="${ASSOPS_HELM_ROLLOUT_REHEARSAL_ENVIRONMENT:-production}"
env_values="${ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES:-}"
release_values="${ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES:-}"
previous_values="${ASSOPS_HELM_ROLLOUT_REHEARSAL_PREVIOUS_VALUES:-}"
restore_report="${ASSOPS_HELM_ROLLOUT_REHEARSAL_RESTORE_REPORT:-}"
output="${ASSOPS_HELM_ROLLOUT_REHEARSAL_PLAN_OUTPUT:-}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for helm-rollout-rehearsal-plan" >&2
    exit 1
  }
}

need python3

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

plan_file="$tmpdir/helm-rollout-rehearsal-plan.md"
python3 - "$plan_file" "$repo" "$ghcr_owner" "$version" "$namespace" "$release" "$environment" "$env_values" "$release_values" "$previous_values" "$restore_report" <<'PY'
import os
import re
import sys

(
    plan_path,
    repo,
    ghcr_owner,
    version,
    namespace,
    release,
    environment,
    env_values,
    release_values,
    previous_values,
    restore_report,
) = sys.argv[1:12]

failures = []
warnings = []

danger_markers = [
    ".env",
    "kubeconfig",
    "kube-config",
    "secret",
    "password",
    "passwd",
    "token",
    "cookie",
    "session",
    ".pem",
    ".key",
    "id_rsa",
    "id_ed25519",
]


def safe_name(value, label, pattern):
    if not value:
        failures.append(f"{label} is required")
        return False
    if not re.match(pattern, value):
        failures.append(f"{label} contains unsupported characters")
        return False
    return True


def reject_secret_shaped(value, label):
    if not value:
        return
    lower = value.lower()
    for marker in danger_markers:
        if marker in lower:
            failures.append(f"{label} contains disallowed marker {marker}")


def validate_file(path, label, required=True, suffixes=(".yaml", ".yml")):
    if not path:
        if required:
            failures.append(f"{label} is required")
        return False
    reject_secret_shaped(path, label)
    if "\x00" in path:
        failures.append(f"{label} contains a NUL byte")
        return False
    if path.startswith("-"):
        failures.append(f"{label} must not start with '-'")
    normalized = os.path.normpath(path)
    parts = normalized.split(os.sep)
    if ".." in parts:
        failures.append(f"{label} must not contain '..'")
    if not path.endswith(suffixes):
        failures.append(f"{label} must end with one of {', '.join(suffixes)}")
    if path and not os.path.isfile(path):
        failures.append(f"{label} file does not exist")
    return True


safe_name(repo, "repository", r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
safe_name(ghcr_owner, "GHCR owner", r"^[A-Za-z0-9_.-]+$")
safe_name(namespace, "Kubernetes namespace", r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
safe_name(release, "Helm release", r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
safe_name(environment, "GitHub environment", r"^[A-Za-z0-9_.-]+$")

if not version:
    failures.append("release version is required")
elif not re.match(r"^v[0-9]+\.[0-9]+\.[0-9]+([-.+][A-Za-z0-9_.-]+)?$", version):
    failures.append("release version must look like vMAJOR.MINOR.PATCH")

for value, label in [
    (repo, "repository"),
    (ghcr_owner, "GHCR owner"),
    (version, "release version"),
    (namespace, "Kubernetes namespace"),
    (release, "Helm release"),
    (environment, "GitHub environment"),
]:
    reject_secret_shaped(value, label)

validate_file(env_values, "environment values")
validate_file(release_values, "release image values")
validate_file(previous_values, "previous rollback values", required=False)
validate_file(restore_report, "restore rehearsal report", required=False, suffixes=(".json",))

if env_values and release_values and os.path.abspath(env_values) == os.path.abspath(release_values):
    failures.append("environment values and release image values must be different files")

if previous_values and os.path.abspath(previous_values) in {os.path.abspath(env_values), os.path.abspath(release_values)}:
    warnings.append("previous rollback values points at a current rollout values file; verify rollback evidence is from the previous release")

if env_values and "production.example" in env_values:
    warnings.append("environment values points at the production example; use a reviewed environment overlay before real rollout")

if not previous_values:
    warnings.append("previous rollback values file not provided; capture current values before any Helm upgrade")

if not restore_report:
    warnings.append("restore rehearsal report not provided; keep a current private report with release notes before promotion")

lines = [
    "# Helm Rollout Rehearsal Plan",
    "",
    "## Offline Inputs",
    "",
    f"- Repository: `{repo or 'missing'}`",
    f"- GHCR owner: `{ghcr_owner or 'missing'}`",
    f"- Release version: `{version or 'missing'}`",
    f"- GitHub environment: `{environment or 'missing'}`",
    f"- Kubernetes namespace: `{namespace or 'missing'}`",
    f"- Helm release: `{release or 'missing'}`",
    f"- Environment values: `{env_values or 'missing'}`",
    f"- Release image values: `{release_values or 'missing'}`",
    f"- Previous rollback values: `{previous_values or 'missing'}`",
    f"- Restore rehearsal report: `{restore_report or 'missing'}`",
    "",
    "## Required Evidence Before Protected Rollout",
    "",
    "1. GitHub `production` environment requires reviewers before `promote-production.yml` can run with `deploy=true`.",
    "2. Release artifacts and GHCR images have attestations verified from the same repository and version.",
    "3. Environment values use external Secret, external PostgreSQL, TLS ingress, reviewed storage class, resource limits, NetworkPolicy, and PodDisruptionBudget.",
    "4. Release image values pin gateway, worker, node-worker, and web images to the reviewed GHCR owner and version.",
    "5. Current restore rehearsal report is retained privately with release notes before promotion.",
    "6. Current Helm values or previous release values are captured before any upgrade for rollback review.",
    "7. Namespace-scoped kubeconfig and promotion RBAC are reviewed out of band; this plan does not read kubeconfig content.",
    "8. Operator has dry-run rendered manifests and rollback command shape reviewed before allowing `helm upgrade --install`.",
    "",
    "## Commands To Review Manually",
    "",
    "```bash",
    f"gh workflow run promote-production.yml --repo {repo or '<owner>/<repo>'} \\",
    f"  -f github_environment={environment or 'production'} \\",
    f"  -f ghcr_owner={ghcr_owner or '<owner>'} \\",
    f"  -f version={version or 'v0.1.0'} \\",
    f"  -f namespace={namespace or 'assops'} \\",
    f"  -f release={release or 'assops'} \\",
    f"  -f environment_values={env_values or '<reviewed-environment-values.yaml>'} \\",
    "  -f deploy=false",
    "```",
    "",
    "Only after protected-environment reviewers approve the preflight artifact and rollback evidence, repeat with `deploy=true`.",
    "",
]

if warnings:
    lines.extend(["## Warnings", ""])
    lines.extend(f"- {item}" for item in warnings)
    lines.append("")

if failures:
    lines.extend(["## Blocking Findings", ""])
    lines.extend(f"- {item}" for item in failures)
else:
    lines.extend(["## Blocking Findings", "", "- none"])

open(plan_path, "w", encoding="utf-8").write("\n".join(lines) + "\n")
for line in lines:
    print(line)
if failures:
    sys.exit(1)
PY

if [[ -n "$output" ]]; then
  mkdir -p "$(dirname "$output")"
  cp "$plan_file" "$output"
fi

echo "helm-rollout-rehearsal-plan passed for ${release:-missing} in namespace ${namespace:-missing}"
