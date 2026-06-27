#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

out_file="$tmpdir/branch-protection-plan.md"
stdout_file="/tmp/assops-release-branch-protection-plan-stdout.md"

cd "$repo_root"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-branch-protection-plan \
  REPO=nathan77886/ass-ops \
  OUTPUT="$out_file" \
  >/tmp/assops-release-branch-protection-plan-output.log

test -s "$out_file"
grep -q 'ASSOPS Branch Protection Plan' "$out_file"
grep -q 'Repository: `nathan77886/ass-ops`' "$out_file"
grep -q 'Ruleset name: `ASSOPS default branch required checks`' "$out_file"
grep -q 'Enforcement: `active`' "$out_file"
grep -q 'Ruleset targets `~DEFAULT_BRANCH`' "$out_file"
grep -q 'Branch deletion and non-fast-forward pushes are blocked' "$out_file"
grep -q 'code owner review' "$out_file"
grep -q 'Required status checks are strict/fresh' "$out_file"
grep -q '`Workflow Lint`' "$out_file"
grep -q '`Secret Scan`' "$out_file"
grep -q '`Go Vulnerability Check`' "$out_file"
grep -q 'local validation only; it does not call GitHub' "$out_file"
grep -q 'Administration: write' "$out_file"
grep -q '/repos/nathan77886/ass-ops/rulesets' "$out_file"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-branch-protection-plan \
  REPO=nathan77886/ass-ops \
  >"$stdout_file"
grep -q 'ASSOPS Branch Protection Plan' "$stdout_file"

if rg -n "(?i)(Authorization:|ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|password|token)" "$out_file" "$stdout_file"; then
  echo "release-branch-protection-plan generated secret-shaped material" >&2
  exit 1
fi

if PATH=/usr/local/go/bin:$PATH make --no-print-directory release-branch-protection-plan \
  REPO=nathan77886 \
  OUTPUT="$tmpdir/bad-repo.md" \
  >/tmp/assops-release-branch-protection-plan-bad-repo.log 2>&1; then
  echo "expected release-branch-protection-plan to reject repository without owner/repo" >&2
  exit 1
fi
grep -q 'repository must be owner/repo' /tmp/assops-release-branch-protection-plan-bad-repo.log

bad_codeowners="$tmpdir/CODEOWNERS"
cp .github/CODEOWNERS "$bad_codeowners"
sed -i '/\/web\//d' "$bad_codeowners"
if PATH=/usr/local/go/bin:$PATH go run ./backend/cmd/assops-tool release branch-protection-plan \
  nathan77886/ass-ops \
  .github/rulesets/main-required-checks.json \
  "$bad_codeowners" \
  "$tmpdir/bad-codeowners.md" \
  >/tmp/assops-release-branch-protection-plan-bad-codeowners.log 2>&1; then
  echo "expected branch-protection-plan to reject incomplete CODEOWNERS" >&2
  exit 1
fi
grep -q '/web/' /tmp/assops-release-branch-protection-plan-bad-codeowners.log

echo "release-branch-protection-plan self-test passed"
