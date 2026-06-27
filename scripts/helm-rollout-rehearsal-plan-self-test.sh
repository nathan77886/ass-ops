#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

env_values="$tmpdir/values.production.reviewed.yaml"
release_values="$tmpdir/helm-values-v0.1.0.yaml"
previous_values="$tmpdir/helm-values-v0.0.9.yaml"
restore_report="$tmpdir/restore-rehearsal.json"

touch "$env_values" "$release_values" "$previous_values" "$restore_report"

ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=nathan77886/ass-ops \
ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=nathan77886 \
ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 \
ASSOPS_HELM_ROLLOUT_REHEARSAL_NAMESPACE=assops \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE=assops \
ASSOPS_HELM_ROLLOUT_REHEARSAL_ENVIRONMENT=production \
ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES="$env_values" \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES="$release_values" \
ASSOPS_HELM_ROLLOUT_REHEARSAL_PREVIOUS_VALUES="$previous_values" \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RESTORE_REPORT="$restore_report" \
ASSOPS_HELM_ROLLOUT_REHEARSAL_PLAN_OUTPUT="$tmpdir/plan.md" \
bash "$repo_root/scripts/helm-rollout-rehearsal-plan.sh" >/tmp/assops-helm-rollout-rehearsal-plan-ok.log

grep -q "Helm Rollout Rehearsal Plan" "$tmpdir/plan.md"
grep -q -- "- none" "$tmpdir/plan.md"

if ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=nathan77886 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES="$env_values" \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES="$release_values" \
  bash "$repo_root/scripts/helm-rollout-rehearsal-plan.sh" >/tmp/assops-helm-rollout-rehearsal-plan-missing.log 2>&1; then
  echo "expected helm-rollout-rehearsal-plan to fail without repository" >&2
  exit 1
fi
grep -q "repository is required" /tmp/assops-helm-rollout-rehearsal-plan-missing.log

if ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=nathan77886/ass-ops \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=nathan77886 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES="$env_values" \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES="$release_values" \
  bash "$repo_root/scripts/helm-rollout-rehearsal-plan.sh" >/tmp/assops-helm-rollout-rehearsal-plan-version.log 2>&1; then
  echo "expected helm-rollout-rehearsal-plan to reject invalid version" >&2
  exit 1
fi
grep -q "release version must look like vMAJOR.MINOR.PATCH" /tmp/assops-helm-rollout-rehearsal-plan-version.log

danger_values="$tmpdir/values.secret.yaml"
touch "$danger_values"
if ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=nathan77886/ass-ops \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=nathan77886 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES="$danger_values" \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES="$release_values" \
  bash "$repo_root/scripts/helm-rollout-rehearsal-plan.sh" >/tmp/assops-helm-rollout-rehearsal-plan-danger.log 2>&1; then
  echo "expected helm-rollout-rehearsal-plan to reject secret-shaped values path" >&2
  exit 1
fi
grep -q "environment values contains disallowed marker secret" /tmp/assops-helm-rollout-rehearsal-plan-danger.log

if ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=nathan77886/ass-ops \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=nathan77886 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES="$env_values" \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES="$env_values" \
  bash "$repo_root/scripts/helm-rollout-rehearsal-plan.sh" >/tmp/assops-helm-rollout-rehearsal-plan-same.log 2>&1; then
  echo "expected helm-rollout-rehearsal-plan to reject identical values files" >&2
  exit 1
fi
grep -q "environment values and release image values must be different files" /tmp/assops-helm-rollout-rehearsal-plan-same.log

if ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=nathan77886/ass-ops \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=nathan77886 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES="$tmpdir/missing.yaml" \
  ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES="$release_values" \
  bash "$repo_root/scripts/helm-rollout-rehearsal-plan.sh" >/tmp/assops-helm-rollout-rehearsal-plan-missing-file.log 2>&1; then
  echo "expected helm-rollout-rehearsal-plan to reject missing values file" >&2
  exit 1
fi
grep -q "environment values file does not exist" /tmp/assops-helm-rollout-rehearsal-plan-missing-file.log

echo "helm-rollout-rehearsal-plan self-test passed"
