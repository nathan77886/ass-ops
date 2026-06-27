#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

out_file="$tmpdir/helm-test-readiness-plan.md"
stdout_file="/tmp/assops-release-helm-test-readiness-plan-stdout.md"
bad_values="$tmpdir/bad-values.yaml"

cd "$repo_root"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-test-readiness-plan \
  OUTPUT="$out_file" \
  >/tmp/assops-release-helm-test-readiness-plan-output.log

test -s "$out_file"
grep -q 'ASSOPS Helm Test Environment Readiness Plan' "$out_file"
grep -q 'values.test.example.yaml' "$out_file"
grep -q 'External application Secret is required' "$out_file"
grep -q 'assops-test-secret' "$out_file"
grep -q 'Built-in PostgreSQL is disabled' "$out_file"
grep -q 'ServiceAccount token automount is disabled' "$out_file"
grep -q 'pod-log metadata audits are enabled' "$out_file"
grep -q 'assops-kubeconfigs' "$out_file"
grep -q '/etc/assops/kubeconfigs' "$out_file"
grep -q 'does not call Kubernetes, Helm, Argo, GitHub, or cloud APIs' "$out_file"
grep -q 'helm lint deploy/helm/assops' "$out_file"
grep -q 'kubectl -n assops-test get secret "assops-test-secret"' "$out_file"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-test-readiness-plan \
  >"$stdout_file"
grep -q 'ASSOPS Helm Test Environment Readiness Plan' "$stdout_file"

if rg -n "(?i)(ASSOPS_JWT_SECRET=|ASSOPS_ADMIN_PASSWORD=|KUBE_CONFIG_B64=|Authorization:|BEGIN [A-Z ]*PRIVATE[ ]KEY|ghp_|github_pat_|xox[baprs]-|AKIA[0-9A-Z]{16})" "$out_file" "$stdout_file"; then
  echo "release-helm-test-readiness-plan generated secret-shaped material" >&2
  exit 1
fi

sed 's/  enabled: false/  enabled: true/' deploy/helm/assops/values.test.example.yaml >"$bad_values"
if PATH=/usr/local/go/bin:$PATH go run ./backend/cmd/assops-tool release helm-test-readiness-plan "$bad_values" "$tmpdir/bad-plan.md" \
  >/tmp/assops-release-helm-test-readiness-plan-bad.log 2>&1; then
  echo "expected helm-test-readiness-plan to reject built-in PostgreSQL" >&2
  exit 1
fi
grep -q 'postgres.enabled=false' /tmp/assops-release-helm-test-readiness-plan-bad.log

echo "release-helm-test-readiness-plan self-test passed"
