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

PATH=/usr/local/go/bin:$PATH go run ./backend/cmd/assops-tool release validate-bundle "$artifact_dir" "$report" \
  >/tmp/assops-release-validate-bundle-cli-self-test.json
grep -q '"valid": true' /tmp/assops-release-validate-bundle-cli-self-test.json
grep -q '"checksum_verified": 3' /tmp/assops-release-validate-bundle-cli-self-test.json
grep -q '"migration_count": 1' /tmp/assops-release-validate-bundle-cli-self-test.json

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-validate-bundle \
  ARTIFACT_DIR="$artifact_dir" \
  REHEARSAL_REPORT="$report" \
  >/tmp/assops-release-validate-bundle-make-self-test.json
grep -q '"valid": true' /tmp/assops-release-validate-bundle-make-self-test.json

printf '%s' 'changed' >"$artifact_dir/assops-web-v1.0.0.tar.gz"
if PATH=/usr/local/go/bin:$PATH go run ./backend/cmd/assops-tool release validate-bundle "$artifact_dir" "$report" \
  >/tmp/assops-release-validate-bundle-bad-checksum.log 2>&1; then
  echo "expected validate-bundle to reject checksum mismatch" >&2
  exit 1
fi
grep -q 'checksum mismatch for assops-web-v1.0.0.tar.gz' /tmp/assops-release-validate-bundle-bad-checksum.log

echo "release-validate-bundle self-test passed"
