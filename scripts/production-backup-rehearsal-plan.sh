#!/usr/bin/env bash
set -euo pipefail

repo="${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO:-}"
environment="${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_ENV:-production}"
runner="${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RUNNER:-ubuntu-latest}"
cron="${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_CRON:-17 3 * * 1}"
backup_source="${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_BACKUP_SOURCE:-artifact:retained-assops-backup}"
retention_days="${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RETENTION_DAYS:-14}"
retained_artifact="${ASSOPS_PRODUCTION_RETAINED_BACKUP_ARTIFACT:-retained-assops-backup}"
restore_report="${ASSOPS_PRODUCTION_RESTORE_REHEARSAL_REPORT_NAME:-production-restore-rehearsal}"
output="${ASSOPS_PRODUCTION_BACKUP_REHEARSAL_PLAN_OUTPUT:-}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for production-backup-rehearsal-plan" >&2
    exit 1
  }
}

need python3

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

plan_file="$tmpdir/production-backup-rehearsal-plan.md"
python3 - "$plan_file" "$repo" "$environment" "$runner" "$cron" "$backup_source" "$retention_days" "$retained_artifact" "$restore_report" <<'PY'
import re
import sys

plan_path, repo, environment, runner, cron, backup_source, retention_days, retained_artifact, restore_report = sys.argv[1:10]

failures = []
warnings = []

def safe_name(value, label, pattern=r"^[A-Za-z0-9_.@/-]+$"):
    if not value:
        failures.append(f"{label} is required")
        return False
    if not re.match(pattern, value):
        failures.append(f"{label} contains unsupported characters")
        return False
    return True

safe_name(repo, "repository", r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
safe_name(environment, "GitHub environment", r"^[A-Za-z0-9_.-]+$")
safe_name(runner, "runner label", r"^[A-Za-z0-9_.@/-]+$")
safe_name(retained_artifact, "retained backup artifact name", r"^[A-Za-z0-9_.-]+$")
safe_name(restore_report, "restore rehearsal report artifact name", r"^[A-Za-z0-9_.-]+$")

try:
    retention = int(retention_days)
except ValueError:
    retention = 0
    failures.append("retention days must be an integer")
if retention < 1 or retention > 90:
    failures.append("retention days must be between 1 and 90")

cron_fields = cron.split()
if len(cron_fields) != 5:
    failures.append("cron expression must contain exactly five fields")

danger_markers = [".env", "kubeconfig", "kube-config", "secret", "password", "token", "cookie", "session", ".pem", ".key", ".log", "id_rsa", "id_ed25519"]
source_kind = ""
source_value = ""
if backup_source.startswith("artifact:"):
    source_kind = "artifact"
    source_value = backup_source[len("artifact:"):]
    if not safe_name(source_value, "backup artifact source", r"^[A-Za-z0-9_.-]+$"):
        pass
elif backup_source.startswith("path:"):
    source_kind = "path"
    source_value = backup_source[len("path:"):]
    if not source_value.startswith("/"):
        failures.append("backup path source must be absolute")
    if not re.search(r"(^|/)assops-[^/]+\.dump$", source_value):
        failures.append("backup path source should point to one retained assops-*.dump file")
else:
    failures.append("backup source must use artifact:name or path:/absolute/assops-*.dump")

lower_source = backup_source.lower()
for marker in danger_markers:
    if marker in lower_source and not lower_source.endswith(".dump"):
        failures.append(f"backup source contains disallowed marker {marker}")

if source_kind == "artifact" and source_value != retained_artifact:
    warnings.append("backup source artifact differs from retained backup artifact name; verify this is intentional")
if source_kind == "path" and runner == "ubuntu-latest":
    warnings.append("path backup source usually requires a self-hosted runner with the retained backup store mounted")

lines = [
    "# Production Backup Restore Rehearsal Plan",
    "",
    "## Offline Inputs",
    "",
    f"- Repository: `{repo or 'missing'}`",
    f"- GitHub environment: `{environment or 'missing'}`",
    f"- Runner: `{runner or 'missing'}`",
    f"- Schedule cron: `{cron or 'missing'}`",
    f"- Backup source: `{backup_source or 'missing'}`",
    f"- Retention days: `{retention_days or 'missing'}`",
    f"- Retained backup artifact name: `{retained_artifact or 'missing'}`",
    f"- Restore rehearsal report artifact name: `{restore_report or 'missing'}`",
    "",
    "## Required Protected Environment Secrets",
    "",
    "- `ASSOPS_ACTIVE_DATABASE_URL` for `production-retained-backup.yml`",
    "- `ASSOPS_ACTIVE_DATABASE_PASSWORD` only when the URL omits a password",
    "- `ASSOPS_REHEARSAL_DATABASE_URL` for `production-restore-rehearsal.yml`; target database name must be disposable",
    "- `ASSOPS_REHEARSAL_DATABASE_PASSWORD` only when the rehearsal URL omits a password",
    "",
    "## Manual Dispatch Checks",
    "",
    "1. Run `production-retained-backup.yml` manually with the protected environment before enabling its schedule.",
    "2. Confirm the retained backup artifact contains exactly one `assops-*.dump` file and no `.env`, kubeconfig, log, key, or PEM-like files.",
    "3. Run `production-restore-rehearsal.yml` manually against the selected retained backup source and a disposable restore database.",
    "4. Keep the private JSON restore rehearsal report with release notes before any production promotion.",
    "",
]

if warnings:
    lines.extend(["## Warnings", ""])
    lines.extend(f"- {item}" for item in warnings)
    lines.append("")

if failures:
    lines.extend(["## Blocking Findings", ""])
    lines.extend(f"- {item}" for item in failures)
else:
    lines.extend(["## Blocking Findings", "", "- none"])

open(plan_path, "w", encoding="utf-8").write("\n".join(lines) + "\n")
for line in lines:
    print(line)
if failures:
    sys.exit(1)
PY

if [[ -n "$output" ]]; then
  mkdir -p "$(dirname "$output")"
  cp "$plan_file" "$output"
fi

echo "production-backup-rehearsal-plan passed for ${repo:-missing} in ${environment:-missing}"
