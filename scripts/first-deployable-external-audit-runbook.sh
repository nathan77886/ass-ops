#!/usr/bin/env bash
set -euo pipefail

audit_file="${1:-${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_OUTPUT:-.assops/release-notes/first-deployable-external-audit.json}}"
output="${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_RUNBOOK_OUTPUT:-.assops/release-notes/first-deployable-external-audit-runbook.md}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

bash scripts/first-deployable-external-audit-validate.sh "$audit_file" >/dev/null
mkdir -p "$(dirname "$output")"

python3 - "$audit_file" "$output" <<'PY'
import json
import re
import sys
from pathlib import Path

audit_path = Path(sys.argv[1])
output = Path(sys.argv[2])
audit = json.loads(audit_path.read_text(encoding="utf-8"))

def line_escape(value):
    return str(value).replace("\n", " ").strip()

def bullet(value):
    return f"- {line_escape(value)}"

def fenced(value):
    value = str(value).strip()
    if not value:
        return ""
    return f"```bash\n{value}\n```"

blockers = audit.get("completion", {}).get("blockers", [])
verified = audit.get("completion", {}).get("verified_external_items", [])
secret_policy = audit.get("secret_policy", {})

lines = [
    "# First Deployable External Audit Runbook",
    "",
    "Generated from `first-deployable-external-audit.json`.",
    "",
    "## Status",
    "",
    bullet(f"Repository: {audit.get('repository', '')}"),
    bullet(f"Version: {audit.get('version', '')}"),
    bullet(f"Verified external items: {len(verified)}"),
    bullet(f"Remaining blockers: {len(blockers)}"),
    bullet(f"Generated at: {audit.get('verified_at', '')}"),
    "",
    "## Safety Boundary",
    "",
    "- This runbook contains command templates and evidence expectations only.",
    "- It does not contain secret values, database passwords, kubeconfig content, private keys, provider payloads, or raw logs.",
    "- Commands that trigger workflows, tags, releases, or deploys require an external operator decision before execution.",
    "",
    "## Secret Policy",
    "",
]

for key in sorted(secret_policy):
    lines.append(bullet(f"{key}: {secret_policy[key]}"))

lines.extend(["", "## Blockers", ""])

for index, blocker in enumerate(blockers, 1):
    lines.extend([
        f"### {index}. {blocker.get('id', '')}",
        "",
        bullet(f"Reason: {blocker.get('reason', '')}"),
        "",
    ])
    for action_index, action in enumerate(blocker.get("next_actions", []), 1):
        lines.extend([
            f"Action {action_index}:",
            "",
            bullet(f"Description: {action.get('description', '')}"),
            bullet(f"Evidence: {action.get('evidence', '')}"),
        ])
        required_names = action.get("required_names") or []
        if required_names:
            lines.append("- Required names:")
            for name in required_names:
                lines.append(f"  - `{line_escape(name)}`")
        command = action.get("command_template", "")
        if command:
            lines.extend(["", fenced(command)])
        lines.append("")

text = "\n".join(lines).rstrip() + "\n"
secret_shape = re.compile(
    r"(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^\s]+[:][^\s@]+@)"
)
if secret_shape.search(text):
    raise SystemExit("external audit runbook contains secret-shaped material")
output.write_text(text, encoding="utf-8")
print(f"first-deployable external audit runbook written to {output}")
PY

ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_OUTPUT="$audit_file" \
  bash scripts/first-deployable-external-audit-runbook-validate.sh "$output" >/dev/null
