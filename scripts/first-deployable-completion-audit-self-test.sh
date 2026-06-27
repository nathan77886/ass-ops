#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

out_file="$tmpdir/first-deployable-completion-audit.md"

cd "$repo_root"

ASSOPS_FIRST_DEPLOYABLE_COMPLETION_AUDIT_OUTPUT="$out_file" \
  bash scripts/first-deployable-completion-audit.sh \
  >/tmp/assops-first-deployable-completion-audit-self-test.log

test -s "$out_file"
grep -q 'ASSOPS First Deployable Completion Audit' "$out_file"
grep -q 'Local Evidence Present' "$out_file"
grep -q 'External Evidence Still Required' "$out_file"
grep -q 'Do not mark the first deployable goal complete from local checks alone' "$out_file"
grep -q 'does not call GitHub, registries, Kubernetes, Argo, PostgreSQL, Redis, MQ, provider APIs, SSH, or Codex CLI' "$out_file"
grep -q 'machine-readable completion audit' "$out_file"
grep -q 'first-deployable-coverage-audit-self-test' "$out_file"
grep -q 'first-deployable-external-evidence-complete-validate-self-test' "$out_file"
grep -q 'release-helm-test-readiness-plan-self-test' "$out_file"
grep -q 'release-branch-protection-plan-self-test' "$out_file"
grep -q 'Repository administrator applies and verifies the GitHub ruleset' "$out_file"
grep -q 'Protected environment owner runs restore rehearsal' "$out_file"
grep -q 'Release operator publishes and verifies GHCR images' "$out_file"
grep -q 'Private test operator rehearses provider-review live execution' "$out_file"

if rg -n "(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^[:space:]]+[:][^[:space:]@]+@)" "$out_file"; then
  echo "first-deployable completion audit generated secret-shaped material" >&2
  exit 1
fi

bash scripts/first-deployable-completion-audit.sh \
  >/tmp/assops-first-deployable-completion-audit-stdout.md
grep -q 'External Evidence Still Required' /tmp/assops-first-deployable-completion-audit-stdout.md

echo "first-deployable-completion-audit self-test passed"
