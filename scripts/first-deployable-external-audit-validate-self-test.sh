#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

cd "$repo_root"

good="$tmpdir/good-audit.json"
cat >"$good" <<'JSON'
{
  "schema": "assops.first_deployable.external_audit.v1",
  "verified_at": "2026-06-27T00:00:00Z",
  "repository": "nathan77886/ass-ops",
  "version": "v0.1.0",
  "ghcr_owner": "nathan77886",
  "github_environments": {
    "production_exists": true,
    "production_has_protection_rules": true,
    "returned_names": ["production"]
  },
  "github_secrets": {
    "expected_names": ["ASSOPS_ACTIVE_DATABASE_URL"],
    "repo_secret_names": [],
    "production_environment_secret_names": [],
    "missing_expected_names": ["ASSOPS_ACTIVE_DATABASE_URL"],
    "contains_secret_values": false
  },
  "github_artifacts": {
    "total_count": 0,
    "returned_count": 0,
    "returned_names": [],
    "unexpired_retained_backup_artifact_found": false
  },
  "ghcr_packages": {
    "query_status": 1,
    "expected_packages": ["assops-gateway"],
    "returned_names": []
  },
  "completion": {
    "verified_external_items": [],
    "complete": false,
    "blockers": [
      {
        "id": "protected_helm_rollout_completed",
        "reason": "missing required GitHub secret names: ASSOPS_ACTIVE_DATABASE_URL",
        "next_actions": [
          {
            "description": "Create GitHub environment secret names from an approved source.",
            "command_template": "gh secret set <NAME> --repo nathan77886/ass-ops --env production",
            "evidence": "GitHub environment secret list returns the expected name"
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

bash scripts/first-deployable-external-audit-validate.sh "$good" \
  >/tmp/assops-first-deployable-external-audit-validate-ok.log

bad_no_actions="$tmpdir/bad-no-actions.json"
python3 - "$good" "$bad_no_actions" <<'PY'
import json
import sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
data["completion"]["blockers"][0]["next_actions"] = []
json.dump(data, open(sys.argv[2], "w", encoding="utf-8"), indent=2)
PY
if bash scripts/first-deployable-external-audit-validate.sh "$bad_no_actions" \
  >/tmp/assops-first-deployable-external-audit-validate-bad-no-actions.log 2>&1; then
  echo "expected missing next_actions audit to fail" >&2
  exit 1
fi
grep -q "next_actions must be non-empty" /tmp/assops-first-deployable-external-audit-validate-bad-no-actions.log

bad_secret="$tmpdir/bad-secret.json"
python3 - "$good" "$bad_secret" <<'PY'
import json
import sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
data["completion"]["blockers"][0]["next_actions"][0]["command_template"] = "psql " + "postgres://" + "user:secret@" + "example.invalid/assops"
json.dump(data, open(sys.argv[2], "w", encoding="utf-8"), indent=2)
PY
if bash scripts/first-deployable-external-audit-validate.sh "$bad_secret" \
  >/tmp/assops-first-deployable-external-audit-validate-bad-secret.log 2>&1; then
  echo "expected secret-shaped audit to fail" >&2
  exit 1
fi
grep -q "secret-shaped material" /tmp/assops-first-deployable-external-audit-validate-bad-secret.log

bad_command="$tmpdir/bad-command.json"
python3 - "$good" "$bad_command" <<'PY'
import json
import sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
data["completion"]["blockers"][0]["next_actions"][0]["command_template"] = "git reset --hard"
json.dump(data, open(sys.argv[2], "w", encoding="utf-8"), indent=2)
PY
if bash scripts/first-deployable-external-audit-validate.sh "$bad_command" \
  >/tmp/assops-first-deployable-external-audit-validate-bad-command.log 2>&1; then
  echo "expected disallowed command audit to fail" >&2
  exit 1
fi
grep -q "disallowed marker" /tmp/assops-first-deployable-external-audit-validate-bad-command.log

echo "first-deployable external audit validate self-test passed"
