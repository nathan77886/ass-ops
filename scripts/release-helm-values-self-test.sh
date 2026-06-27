#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

values="$tmpdir/helm-values-v0.1.0.yaml"
stdout_values="/tmp/assops-release-helm-values-stdout.yaml"
metadata_values="$tmpdir/helm-values-metadata.yaml"

cd "$repo_root"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-values \
  GHCR_OWNER=Nathan77886 \
  VERSION=v0.1.0 \
  OUTPUT="$values" \
  >/tmp/assops-release-helm-values-self-test.log

test -s "$values"
grep -q 'registry: ghcr.io' "$values"
grep -q 'repository: nathan77886/assops-gateway' "$values"
grep -q 'repository: nathan77886/assops-worker' "$values"
grep -q 'repository: nathan77886/assops-node-worker' "$values"
grep -q 'repository: nathan77886/assops-web' "$values"
grep -q 'tag: v0.1.0' "$values"
grep -q 'version: "v0.1.0"' "$values"
grep -q 'commit: "release-commit-not-set"' "$values"
grep -q 'buildTime: "release-build-time-not-set"' "$values"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-values \
  GHCR_OWNER=Nathan77886 \
  VERSION=v0.1.0 \
  >"$stdout_values"
grep -q 'repository: nathan77886/assops-gateway' "$stdout_values"

ASSOPS_RELEASE_COMMIT=abc123def456 \
ASSOPS_RELEASE_BUILD_TIME=2026-06-26T12:34:56Z \
PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-values \
  GHCR_OWNER=nathan77886 \
  VERSION=v0.1.0 \
  OUTPUT="$metadata_values" \
  >/tmp/assops-release-helm-values-metadata-self-test.log
grep -q 'commit: "abc123def456"' "$metadata_values"
grep -q 'buildTime: "2026-06-26T12:34:56Z"' "$metadata_values"

if rg -n "(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|password:|token:)" "$values" "$stdout_values" "$metadata_values"; then
  echo "release-helm-values generated secret-shaped material" >&2
  exit 1
fi

if PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-values \
  GHCR_OWNER=owner/repo \
  VERSION=v0.1.0 \
  OUTPUT="$tmpdir/bad-owner.yaml" \
  >/tmp/assops-release-helm-values-bad-owner.log 2>&1; then
  echo "expected release-helm-values to reject owner/repo" >&2
  exit 1
fi
grep -q 'GHCR owner must be an owner or organization name' /tmp/assops-release-helm-values-bad-owner.log

if PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-values \
  GHCR_OWNER=nathan77886 \
  VERSION='v0.1.0 bad' \
  OUTPUT="$tmpdir/bad-version.yaml" \
  >/tmp/assops-release-helm-values-bad-version.log 2>&1; then
  echo "expected release-helm-values to reject unsafe version" >&2
  exit 1
fi
grep -q 'release version must not contain whitespace' /tmp/assops-release-helm-values-bad-version.log

echo "release-helm-values self-test passed"
