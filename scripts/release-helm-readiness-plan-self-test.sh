#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

out_file="$tmpdir/helm-readiness-plan.md"
stdout_file="/tmp/assops-release-helm-readiness-plan-stdout.md"
bad_values="$tmpdir/bad-values.yaml"

cd "$repo_root"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-readiness-plan \
  OUTPUT="$out_file" \
  >/tmp/assops-release-helm-readiness-plan-output.log

test -s "$out_file"
grep -q 'ASSOPS Helm Environment Readiness Plan' "$out_file"
grep -q 'values.production.example.yaml' "$out_file"
grep -q 'External Secret is required' "$out_file"
grep -q 'assops-production-secret' "$out_file"
grep -q 'Built-in PostgreSQL is disabled' "$out_file"
grep -q 'TLS ingress' "$out_file"
grep -q 'assops.example.com' "$out_file"
grep -q 'assops-production-tls' "$out_file"
grep -q 'ServiceAccount token automount is disabled' "$out_file"
grep -q 'NetworkPolicy and PodDisruptionBudget are enabled' "$out_file"
grep -q 'storageClass `assops-retain`' "$out_file"
grep -q 'does not call Kubernetes, Helm, Argo, GitHub, or cloud APIs' "$out_file"
grep -q 'helm lint deploy/helm/assops' "$out_file"
grep -q 'kubectl -n assops get secret "assops-production-secret"' "$out_file"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-readiness-plan \
  >"$stdout_file"
grep -q 'ASSOPS Helm Environment Readiness Plan' "$stdout_file"

if rg -n "(?i)(ASSOPS_JWT_SECRET=|ASSOPS_ADMIN_PASSWORD=|KUBE_CONFIG_B64=|Authorization:|BEGIN [A-Z ]*PRIVATE[ ]KEY|ghp_|github_pat_|xox[baprs]-|AKIA[0-9A-Z]{16})" "$out_file" "$stdout_file"; then
  echo "release-helm-readiness-plan generated secret-shaped material" >&2
  exit 1
fi

sed 's/gatewayURL: https:\/\/assops.example.com/gatewayURL: http:\/\/assops.example.com/' \
  deploy/helm/assops/values.production.example.yaml >"$bad_values"
if PATH=/usr/local/go/bin:$PATH go run ./backend/cmd/assops-tool release helm-readiness-plan "$bad_values" "$tmpdir/bad-plan.md" \
  >/tmp/assops-release-helm-readiness-plan-bad.log 2>&1; then
  echo "expected helm-readiness-plan to reject non-HTTPS gateway" >&2
  exit 1
fi
grep -q 'gatewayURL to use https' /tmp/assops-release-helm-readiness-plan-bad.log

echo "release-helm-readiness-plan self-test passed"
