#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

artifact_plan="$tmpdir/backup-schedule-artifact.md"
path_plan="$tmpdir/backup-schedule-path.md"

cd "$repo_root"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-backup-schedule-plan \
  REPO=nathan77886/ass-ops \
  ENV=production \
  RUNNER=ubuntu-latest \
  CRON='17 3 * * 1' \
  BACKUP_SOURCE=artifact:retained-assops-backup \
  RETENTION_DAYS=14 \
  OUTPUT="$artifact_plan" \
  >/tmp/assops-release-backup-schedule-artifact-self-test.log

test -s "$artifact_plan"
grep -q 'ASSOPS Production Backup Schedule Plan' "$artifact_plan"
grep -q 'workflow artifact `retained-assops-backup`' "$artifact_plan"
grep -q 'ASSOPS_ACTIVE_DATABASE_URL' "$artifact_plan"
grep -q 'ASSOPS_REHEARSAL_DATABASE_URL' "$artifact_plan"
grep -q 'must not create, rotate, delete, or overwrite retained backups' "$artifact_plan"
grep -q 'gh workflow run production-restore-rehearsal.yml' "$artifact_plan"

PATH=/usr/local/go/bin:$PATH make --no-print-directory release-backup-schedule-plan \
  REPO=nathan77886/ass-ops \
  ENV=production \
  RUNNER=self-hosted-prod \
  CRON='23 2 * * 0' \
  BACKUP_SOURCE=path:/mnt/assops-backups/assops-20260622-120000.dump \
  RETENTION_DAYS=30 \
  OUTPUT="$path_plan" \
  >/tmp/assops-release-backup-schedule-path-self-test.log

test -s "$path_plan"
grep -q '/mnt/assops-backups/assops-20260622-120000.dump' "$path_plan"
grep -q 'self-hosted-prod' "$path_plan"
grep -q 'mount the retained backup store read-only' "$path_plan"

if rg -n "(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^[:space:]]+[:][^[:space:]@]+@)" "$artifact_plan" "$path_plan"; then
  echo "release-backup-schedule-plan generated secret-shaped material" >&2
  exit 1
fi

if PATH=/usr/local/go/bin:$PATH make --no-print-directory release-backup-schedule-plan \
  REPO=nathan77886/ass-ops \
  ENV=production \
  RUNNER=ubuntu-latest \
  CRON='17 3 * * 1' \
  BACKUP_SOURCE=path:/mnt/assops-backups/prod.env \
  RETENTION_DAYS=14 \
  OUTPUT="$tmpdir/bad-path.md" \
  >/tmp/assops-release-backup-schedule-bad-path.log 2>&1; then
  echo "expected release-backup-schedule-plan to reject dangerous path source" >&2
  exit 1
fi
grep -Eq 'backup source|dump|disallowed|\.env' /tmp/assops-release-backup-schedule-bad-path.log

if PATH=/usr/local/go/bin:$PATH make --no-print-directory release-backup-schedule-plan \
  REPO=nathan77886 \
  ENV=production \
  RUNNER=ubuntu-latest \
  CRON='17 3 * * 1' \
  BACKUP_SOURCE=artifact:retained-assops-backup \
  RETENTION_DAYS=14 \
  OUTPUT="$tmpdir/bad-repo.md" \
  >/tmp/assops-release-backup-schedule-bad-repo.log 2>&1; then
  echo "expected release-backup-schedule-plan to reject repository without owner/repo" >&2
  exit 1
fi
grep -q 'repository must be owner/repo' /tmp/assops-release-backup-schedule-bad-repo.log

echo "release-backup-schedule-plan self-test passed"
