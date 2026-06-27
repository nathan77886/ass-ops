#!/usr/bin/env bash
set -euo pipefail

runbook="${1:-${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_RUNBOOK_OUTPUT:-.assops/release-notes/first-deployable-external-audit-runbook.md}}"
audit="${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_OUTPUT:-.assops/release-notes/first-deployable-external-audit.json}"

if [[ ! -s "$runbook" ]]; then
  echo "external audit runbook not found: $runbook" >&2
  exit 1
fi
if [[ ! -s "$audit" ]]; then
  echo "external audit file not found: $audit" >&2
  exit 1
fi

python3 - "$runbook" "$audit" <<'PY'
import json
import re
import sys
from pathlib import Path

runbook_path = Path(sys.argv[1])
audit_path = Path(sys.argv[2])
text = runbook_path.read_text(encoding="utf-8")
audit = json.loads(audit_path.read_text(encoding="utf-8"))

secret_shape = re.compile(
    r"(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^\s]+[:][^\s@]+@)"
)
if secret_shape.search(text):
    raise SystemExit("external audit runbook contains secret-shaped material")

required_phrases = [
    "# First Deployable External Audit Runbook",
    "## Status",
    "## Safety Boundary",
    "## Secret Policy",
    "## Blockers",
    "command templates and evidence expectations only",
]
for phrase in required_phrases:
    if phrase not in text:
        raise SystemExit(f"external audit runbook missing phrase: {phrase}")

blockers = audit.get("completion", {}).get("blockers", [])
if f"Remaining blockers: {len(blockers)}" not in text:
    raise SystemExit("external audit runbook blocker count mismatch")
for blocker in blockers:
    item_id = blocker.get("id", "")
    reason = blocker.get("reason", "")
    if item_id not in text:
        raise SystemExit(f"external audit runbook missing blocker id: {item_id}")
    if reason not in text:
        raise SystemExit(f"external audit runbook missing blocker reason: {item_id}")
    for action in blocker.get("next_actions", []):
        for field in ("description", "evidence"):
            value = action.get(field, "")
            if value and value not in text:
                raise SystemExit(f"external audit runbook missing {field}: {item_id}")
        command = action.get("command_template", "")
        if command and command not in text:
            raise SystemExit(f"external audit runbook missing command template: {item_id}")
        for name in action.get("required_names", []) or []:
            if name not in text:
                raise SystemExit(f"external audit runbook missing required name: {name}")

for marker in ("git reset --hard", "kubectl delete", "helm uninstall", "--password", "--body"):
    if marker in text.lower():
        raise SystemExit(f"external audit runbook contains disallowed marker: {marker}")

print("first-deployable external audit runbook validation passed")
PY
