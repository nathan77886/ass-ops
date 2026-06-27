#!/usr/bin/env bash
set -euo pipefail

audit_file="${1:-${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_OUTPUT:-.assops/release-notes/first-deployable-external-audit.json}}"

if [[ ! -s "$audit_file" ]]; then
  echo "external audit file not found: $audit_file" >&2
  exit 1
fi

python3 - "$audit_file" <<'PY'
import json
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
text = path.read_text(encoding="utf-8")
secret_shape = re.compile(
    r"(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^\s]+[:][^\s@]+@)"
)
if secret_shape.search(text):
    raise SystemExit("external audit contains secret-shaped material")

data = json.loads(text)
if data.get("schema") != "assops.first_deployable.external_audit.v1":
    raise SystemExit("unexpected external audit schema")
if data.get("completion", {}).get("complete") is not False:
    raise SystemExit("external audit must not mark completion true")

required_top = {
    "repository",
    "version",
    "ghcr_owner",
    "github_environments",
    "github_secrets",
    "github_artifacts",
    "ghcr_packages",
    "completion",
    "secret_policy",
}
missing_top = sorted(required_top - set(data))
if missing_top:
    raise SystemExit(f"external audit missing top-level fields: {', '.join(missing_top)}")

policy = data.get("secret_policy", {})
for key in ("contains_token_values", "contains_database_password", "contains_kubeconfig", "contains_private_key"):
    if policy.get(key) is not False:
        raise SystemExit(f"external audit secret policy must keep {key}=false")

secrets = data.get("github_secrets", {})
expected_names = secrets.get("expected_names")
if not isinstance(expected_names, list) or not expected_names:
    raise SystemExit("external audit github_secrets.expected_names must be non-empty")
if secrets.get("contains_secret_values") is not False:
    raise SystemExit("external audit must declare contains_secret_values=false")

completion = data.get("completion", {})
blockers = completion.get("blockers")
if not isinstance(blockers, list):
    raise SystemExit("external audit completion.blockers must be a list")
for blocker in blockers:
    if not isinstance(blocker, dict):
        raise SystemExit("external audit blocker must be an object")
    for field in ("id", "reason", "next_actions"):
        if field not in blocker:
            raise SystemExit(f"external audit blocker missing {field}")
    if not isinstance(blocker["id"], str) or not blocker["id"].strip():
        raise SystemExit("external audit blocker id must be non-empty")
    if not isinstance(blocker["reason"], str) or not blocker["reason"].strip():
        raise SystemExit(f"{blocker['id']} blocker reason must be non-empty")
    actions = blocker["next_actions"]
    if not isinstance(actions, list) or not actions:
        raise SystemExit(f"{blocker['id']} blocker next_actions must be non-empty")
    for action in actions:
        if not isinstance(action, dict):
            raise SystemExit(f"{blocker['id']} next_action must be an object")
        for field in ("description", "evidence"):
            if not isinstance(action.get(field), str) or not action[field].strip():
                raise SystemExit(f"{blocker['id']} next_action missing {field}")
        command = action.get("command_template", "")
        if command and not isinstance(command, str):
            raise SystemExit(f"{blocker['id']} command_template must be a string")
        command_lower = command.lower()
        disallowed = ("--password", "--body", "kubectl delete", "helm uninstall", "git reset --hard")
        if any(marker in command_lower for marker in disallowed):
            raise SystemExit(f"{blocker['id']} next_action command contains disallowed marker")

print("first-deployable external audit validation passed")
PY
