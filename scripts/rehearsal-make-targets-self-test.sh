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

make --no-print-directory -C "$repo_root" production-backup-rehearsal-plan \
  REPO=nathan77886/ass-ops \
  ENV=production \
  RUNNER=ubuntu-latest \
  CRON='17 3 * * 1' \
  BACKUP_SOURCE=artifact:retained-assops-backup \
  RETENTION_DAYS=14 \
  OUTPUT="$tmpdir/production-backup-rehearsal-plan.md" \
  >/tmp/assops-rehearsal-make-production-backup.log

grep -q "Production Backup Restore Rehearsal Plan" "$tmpdir/production-backup-rehearsal-plan.md"
grep -q -- "- none" "$tmpdir/production-backup-rehearsal-plan.md"

make --no-print-directory -C "$repo_root" helm-rollout-rehearsal-plan \
  REPO=nathan77886/ass-ops \
  GHCR_OWNER=nathan77886 \
  VERSION=v0.1.0 \
  NAMESPACE=assops \
  RELEASE=assops \
  ENV=production \
  ENV_VALUES="$env_values" \
  RELEASE_VALUES="$release_values" \
  PREVIOUS_VALUES="$previous_values" \
  RESTORE_REPORT="$restore_report" \
  OUTPUT="$tmpdir/helm-rollout-rehearsal-plan.md" \
  >/tmp/assops-rehearsal-make-helm-rollout.log

grep -q "Helm Rollout Rehearsal Plan" "$tmpdir/helm-rollout-rehearsal-plan.md"
grep -q -- "- none" "$tmpdir/helm-rollout-rehearsal-plan.md"

echo "rehearsal make targets self-test passed"
