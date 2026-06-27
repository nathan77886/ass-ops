#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO=nathan77886/ass-ops \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_ENV=production \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RUNNER=ubuntu-latest \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_CRON='17 3 * * 1' \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_BACKUP_SOURCE=artifact:retained-assops-backup \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RETENTION_DAYS=14 \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_PLAN_OUTPUT="$tmpdir/plan.md" \
bash "$repo_root/scripts/production-backup-rehearsal-plan.sh" >/tmp/assops-production-backup-rehearsal-plan-ok.log

grep -q "Production Backup Restore Rehearsal Plan" "$tmpdir/plan.md"
grep -q -- "- none" "$tmpdir/plan.md"

if bash "$repo_root/scripts/production-backup-rehearsal-plan.sh" >/tmp/assops-production-backup-rehearsal-plan-missing.log 2>&1; then
  echo "expected production-backup-rehearsal-plan to fail without repo" >&2
  exit 1
fi
grep -q "repository is required" /tmp/assops-production-backup-rehearsal-plan-missing.log

if ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO=nathan77886/ass-ops \
  ASSOPS_PRODUCTION_BACKUP_REHEARSAL_BACKUP_SOURCE=path:/backups/prod.env \
  bash "$repo_root/scripts/production-backup-rehearsal-plan.sh" >/tmp/assops-production-backup-rehearsal-plan-danger.log 2>&1; then
  echo "expected production-backup-rehearsal-plan to reject dangerous backup source" >&2
  exit 1
fi
grep -Fq "backup path source should point to one retained assops-*.dump file" /tmp/assops-production-backup-rehearsal-plan-danger.log
grep -Fq "backup source contains disallowed marker .env" /tmp/assops-production-backup-rehearsal-plan-danger.log

ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO=nathan77886/ass-ops \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RUNNER=ubuntu-latest \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_BACKUP_SOURCE=path:/backups/assops-20260627.dump \
bash "$repo_root/scripts/production-backup-rehearsal-plan.sh" >/tmp/assops-production-backup-rehearsal-plan-path.log
grep -q "path backup source usually requires a self-hosted runner" /tmp/assops-production-backup-rehearsal-plan-path.log

if ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO=nathan77886/ass-ops \
  ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RETENTION_DAYS=0 \
  bash "$repo_root/scripts/production-backup-rehearsal-plan.sh" >/tmp/assops-production-backup-rehearsal-plan-retention.log 2>&1; then
  echo "expected production-backup-rehearsal-plan to reject invalid retention" >&2
  exit 1
fi
grep -q "retention days must be between 1 and 90" /tmp/assops-production-backup-rehearsal-plan-retention.log

echo "production-backup-rehearsal-plan self-test passed"
