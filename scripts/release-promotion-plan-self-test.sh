#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

artifact_dir="$tmpdir/artifacts"
report="$tmpdir/restore-rehearsal.json"
values="$tmpdir/helm-values-v0.1.0.yaml"
plan="$tmpdir/promotion-plan-v0.1.0.md"
mkdir -p "$artifact_dir"

printf '%s' 'binary' >"$artifact_dir/assops-tool-v1.0.0-linux-amd64.tar.gz"
printf '%s' 'web' >"$artifact_dir/assops-web-v1.0.0.tar.gz"
printf '%s' 'helm' >"$artifact_dir/assops-0.1.0.tgz"
(cd "$artifact_dir" && sha256sum assops-tool-v1.0.0-linux-amd64.tar.gz assops-web-v1.0.0.tar.gz assops-0.1.0.tgz > SHA256SUMS)

cat >"$report" <<'JSON'
{
  "backup": "/backups/assops-20260622-120000.dump",
  "target_database": "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable",
  "backup_object_counts": {
    "TABLE": 2
  },
  "migrations": [
    {
      "filename": "001_init.sql"
    }
  ],
  "rehearsed_at": "2026-06-22T12:00:00Z"
}
JSON

cd "$repo_root"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-helm-values \
  GHCR_OWNER=Nathan77886 \
  VERSION=v0.1.0 \
  OUTPUT="$values" \
  >/tmp/assops-release-promotion-helm-values-self-test.log

grep -q 'registry: ghcr.io' "$values"
grep -q 'repository: nathan77886/assops-gateway' "$values"
grep -q 'tag: v0.1.0' "$values"
grep -q 'commit:' "$values"
grep -q 'buildTime:' "$values"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-promotion-plan \
  REPO=nathan77886/ass-ops \
  GHCR_OWNER=Nathan77886 \
  VERSION=v0.1.0 \
  ARTIFACT_DIR="$artifact_dir" \
  REHEARSAL_REPORT="$report" \
  HELM_VALUES="$values" \
  OUTPUT="$plan" \
  >/tmp/assops-release-promotion-plan-self-test.log

test -s "$plan"
grep -q 'ASSOPS Promotion Plan v0.1.0' "$plan"
grep -q 'Release bundle checksum validation passed locally' "$plan"
grep -q 'gh attestation verify' "$plan"
grep -q 'ghcr.io/nathan77886/assops-gateway:v0.1.0' "$plan"
grep -q 'helm upgrade --install assops' "$plan"
grep -q 'deploy=true' "$plan"
grep -q 'rollback point' "$plan"

bad_values="$tmpdir/helm-values-v0.2.0.yaml"
PATH=/usr/local/go/bin:$PATH go run ./backend/cmd/assops-tool release helm-values nathan77886 v0.2.0 "$bad_values" \
  >/tmp/assops-release-promotion-bad-values-generate.log
if PATH=/usr/local/go/bin:$PATH make --no-print-directory release-promotion-plan \
  REPO=nathan77886/ass-ops \
  GHCR_OWNER=nathan77886 \
  VERSION=v0.1.0 \
  ARTIFACT_DIR="$artifact_dir" \
  REHEARSAL_REPORT="$report" \
  HELM_VALUES="$bad_values" \
  OUTPUT="$tmpdir/bad-promotion-plan.md" \
  >/tmp/assops-release-promotion-bad-values.log 2>&1; then
  echo "expected release-promotion-plan to reject mismatched Helm values" >&2
  exit 1
fi
grep -q 'Helm values overlay does not match GHCR owner/version' /tmp/assops-release-promotion-bad-values.log

if PATH=/usr/local/go/bin:$PATH make --no-print-directory release-promotion-plan \
  REPO=nathan77886 \
  GHCR_OWNER=nathan77886 \
  VERSION=v0.1.0 \
  ARTIFACT_DIR="$artifact_dir" \
  REHEARSAL_REPORT="$report" \
  HELM_VALUES="$values" \
  OUTPUT="$tmpdir/bad-repo-promotion-plan.md" \
  >/tmp/assops-release-promotion-bad-repo.log 2>&1; then
  echo "expected release-promotion-plan to reject repository without owner/repo" >&2
  exit 1
fi
grep -q 'repository must be owner/repo' /tmp/assops-release-promotion-bad-repo.log

echo "release-promotion-plan self-test passed"
