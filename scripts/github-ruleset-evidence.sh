#!/usr/bin/env bash
set -euo pipefail

repo="${ASSOPS_GITHUB_RULESET_REPO:-nathan77886/ass-ops}"
branch="${ASSOPS_GITHUB_RULESET_BRANCH:-master}"
template="${ASSOPS_GITHUB_RULESET_TEMPLATE:-.github/rulesets/main-required-checks.json}"
status_template="${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_EVIDENCE_STATUS_TEMPLATE:-.assops/release-notes/first-deployable/external-evidence-status.example.json}"
evidence_output="${ASSOPS_GITHUB_RULESET_EVIDENCE_OUTPUT:-.assops/release-notes/github-ruleset-evidence.json}"
status_output="${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_EVIDENCE_STATUS_OUTPUT:-.assops/release-notes/external-evidence-status.local.json}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for github-ruleset-evidence" >&2
    exit 1
  }
}

need gh
need python3

if [[ ! -s "$template" ]]; then
  echo "ruleset template not found: $template" >&2
  exit 1
fi
if [[ ! -s "$status_template" ]]; then
  echo "external evidence status template not found: $status_template" >&2
  exit 1
fi

gh auth status --hostname github.com >/dev/null

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/rulesets" >"$tmpdir/rulesets.json"
ruleset_id="$(python3 - "$tmpdir/rulesets.json" "$template" <<'PY'
import json
import sys
from pathlib import Path

rulesets = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
template = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
matches = [item for item in rulesets if item.get("name") == template["name"]]
if len(matches) != 1:
    raise SystemExit(f"expected exactly one ruleset named {template['name']!r}, found {len(matches)}")
print(matches[0]["id"])
PY
)"
gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/rulesets/${ruleset_id}" >"$tmpdir/ruleset.json"
gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/rules/branches/${branch}" >"$tmpdir/branch-rules.json"

mkdir -p "$(dirname "$evidence_output")" "$(dirname "$status_output")"

python3 - \
  "$repo" \
  "$branch" \
  "$template" \
  "$status_template" \
  "$evidence_output" \
  "$status_output" \
  "$tmpdir/ruleset.json" \
  "$tmpdir/branch-rules.json" <<'PY'
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

repo = sys.argv[1]
branch = sys.argv[2]
template_path = Path(sys.argv[3])
status_template_path = Path(sys.argv[4])
evidence_output = Path(sys.argv[5])
status_output = Path(sys.argv[6])
ruleset_path = Path(sys.argv[7])
branch_rules_path = Path(sys.argv[8])

template = json.loads(template_path.read_text(encoding="utf-8"))
ruleset = json.loads(ruleset_path.read_text(encoding="utf-8"))
branch_rules = json.loads(branch_rules_path.read_text(encoding="utf-8"))

expected_name = template["name"]
if ruleset.get("name") != expected_name:
    raise SystemExit(f"ruleset name mismatch: {ruleset.get('name')!r}")

if ruleset.get("target") != template.get("target"):
    raise SystemExit("ruleset target mismatch")
if ruleset.get("enforcement") != "active":
    raise SystemExit("ruleset enforcement is not active")
if ruleset.get("conditions", {}).get("ref_name", {}).get("include") != ["~DEFAULT_BRANCH"]:
    raise SystemExit("ruleset does not target default branch")

expected_types = [rule["type"] for rule in template["rules"]]
actual_types = [rule.get("type") for rule in ruleset.get("rules", [])]
for rule_type in expected_types:
    if rule_type not in actual_types:
        raise SystemExit(f"ruleset missing rule type: {rule_type}")

template_required_checks = []
for rule in template["rules"]:
    if rule["type"] == "required_status_checks":
        template_required_checks = [item["context"] for item in rule["parameters"]["required_status_checks"]]
        break
actual_required_checks = []
for rule in ruleset.get("rules", []):
    if rule.get("type") == "required_status_checks":
        actual_required_checks = [item["context"] for item in rule.get("parameters", {}).get("required_status_checks", [])]
        if not rule.get("parameters", {}).get("strict_required_status_checks_policy"):
            raise SystemExit("required status checks are not strict")
        break
if actual_required_checks != template_required_checks:
    raise SystemExit("required status check contexts mismatch")

ruleset_id = ruleset["id"]
branch_rule_types = {
    item.get("type")
    for item in branch_rules
    if item.get("ruleset_id") == ruleset_id
}
for rule_type in expected_types:
    if rule_type not in branch_rule_types:
        raise SystemExit(f"default branch missing applied rule type: {rule_type}")

verified_at = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
evidence = {
    "schema": "assops.github_ruleset_evidence.v1",
    "verified_at": verified_at,
    "repository": repo,
    "default_branch": branch,
    "ruleset": {
        "id": ruleset_id,
        "name": ruleset["name"],
        "target": ruleset["target"],
        "enforcement": ruleset["enforcement"],
        "conditions": ruleset.get("conditions", {}),
        "rule_types": actual_types,
        "required_status_checks": actual_required_checks,
    },
    "branch_rules": [
        {
            "type": item.get("type"),
            "ruleset_id": item.get("ruleset_id"),
            "ruleset_source": item.get("ruleset_source"),
            "ruleset_source_type": item.get("ruleset_source_type"),
        }
        for item in branch_rules
        if item.get("ruleset_id") == ruleset_id
    ],
    "secret_policy": {
        "contains_token_values": False,
        "contains_private_keys": False,
    },
}
evidence_output.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n", encoding="utf-8")

status = json.loads(status_template_path.read_text(encoding="utf-8"))
for entry in status["entries"]:
    if entry["id"] == "github_ruleset_applied":
        entry["status"] = "verified"
        entry["evidence_reference"] = f"repo:{evidence_output}"
        entry["verified_by"] = "gh-authenticated:nathan77886"
        entry["verified_at"] = verified_at
        entry["evidence_summary"] = (
            f"Repository ruleset {ruleset_id} is active on {repo} default branch {branch} "
            "with deletion, non_fast_forward, pull_request, and required_status_checks rules applied."
        )
        break
else:
    raise SystemExit("external status template missing github_ruleset_applied entry")
status_output.write_text(json.dumps(status, indent=2, sort_keys=True) + "\n", encoding="utf-8")

print(f"github ruleset evidence written to {evidence_output}")
print(f"external evidence status written to {status_output}")
PY
