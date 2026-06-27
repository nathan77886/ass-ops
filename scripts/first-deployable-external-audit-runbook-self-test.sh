#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

cd "$repo_root"

audit="$tmpdir/audit.json"
runbook="$tmpdir/runbook.md"
cat >"$audit" <<'JSON'
{
  "schema": "assops.first_deployable.external_audit.v1",
  "verified_at": "2026-06-27T00:00:00Z",
  "repository": "nathan77886/ass-ops",
  "version": "v0.1.0",
  "ghcr_owner": "nathan77886",
  "github_environments": {"production_exists": true},
  "github_secrets": {
    "expected_names": ["ASSOPS_ACTIVE_DATABASE_URL", "KUBE_CONFIG_B64"],
    "contains_secret_values": false
  },
  "github_artifacts": {"total_count": 0, "returned_count": 0},
  "ghcr_packages": {"query_status": 1, "expected_packages": ["assops-gateway"], "returned_names": []},
  "completion": {
    "verified_external_items": ["github_ruleset_applied"],
    "complete": false,
    "blockers": [
      {
        "id": "protected_helm_rollout_completed",
        "reason": "missing required GitHub secret names: ASSOPS_ACTIVE_DATABASE_URL, KUBE_CONFIG_B64",
        "next_actions": [
          {
            "description": "Create the GitHub production environment secrets from an approved secret source.",
            "command_template": "gh secret set <NAME> --repo nathan77886/ass-ops --env production",
            "required_names": ["ASSOPS_ACTIVE_DATABASE_URL", "KUBE_CONFIG_B64"],
            "evidence": "GitHub environment secret list returns every expected secret name"
          }
        ]
      }
    ]
  },
  "secret_policy": {
    "contains_token_values": false,
    "contains_database_password": false,
    "contains_kubeconfig": false,
    "contains_private_key": false
  }
}
JSON

ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_RUNBOOK_OUTPUT="$runbook" \
  bash scripts/first-deployable-external-audit-runbook.sh "$audit" \
  >/tmp/assops-first-deployable-external-audit-runbook-self-test.log

test -s "$runbook"
grep -q "First Deployable External Audit Runbook" "$runbook"
grep -q "Remaining blockers: 1" "$runbook"
grep -q "protected_helm_rollout_completed" "$runbook"
grep -q "gh secret set <NAME>" "$runbook"
grep -q "ASSOPS_ACTIVE_DATABASE_URL" "$runbook"
grep -q "contains_token_values: False\|contains_token_values: false" "$runbook"
ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_OUTPUT="$audit" \
  bash scripts/first-deployable-external-audit-runbook-validate.sh "$runbook" \
  >/tmp/assops-first-deployable-external-audit-runbook-validate-ok.log

if grep -Eiq 'ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^[:space:]]+[:][^[:space:]@]+@' "$runbook"; then
  echo "runbook contains secret-shaped material" >&2
  exit 1
fi

bad="$tmpdir/bad.json"
python3 - "$audit" "$bad" <<'PY'
import json
import sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
data["completion"]["blockers"][0]["next_actions"] = []
json.dump(data, open(sys.argv[2], "w", encoding="utf-8"), indent=2)
PY
if ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_RUNBOOK_OUTPUT="$tmpdir/bad.md" \
  bash scripts/first-deployable-external-audit-runbook.sh "$bad" \
  >/tmp/assops-first-deployable-external-audit-runbook-bad.log 2>&1; then
  echo "expected invalid audit runbook generation to fail" >&2
  exit 1
fi
grep -q "next_actions must be non-empty" /tmp/assops-first-deployable-external-audit-runbook-bad.log

echo "first-deployable external audit runbook self-test passed"
