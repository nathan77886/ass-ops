#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

out_dir="$tmpdir/handoff"
ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$out_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-external-evidence-complete-self-test-plan.log

template="$out_dir/external-evidence-status.example.json"
if ASSOPS_FIRST_DEPLOYABLE_REQUIRE_ALL_VERIFIED=true \
  bash scripts/first-deployable-external-evidence-validate.sh "$template" \
  >/tmp/assops-first-deployable-external-evidence-complete-pending.log 2>&1; then
  echo "complete evidence validator accepted pending evidence" >&2
  exit 1
fi
grep -q "is not verified" /tmp/assops-first-deployable-external-evidence-complete-pending.log

complete="$tmpdir/external-evidence-status.complete.json"
python3 - "$template" "$complete" <<'PY'
import json
import sys
from pathlib import Path

source = Path(sys.argv[1])
target = Path(sys.argv[2])
data = json.loads(source.read_text(encoding="utf-8"))
for entry in data["entries"]:
    item_id = entry["id"]
    entry["status"] = "verified"
    entry["evidence_reference"] = f"private-release-notes/{item_id}.md"
    entry["verified_by"] = f"{item_id}_owner"
    entry["verified_at"] = "2026-06-27T00:00:00Z"
    entry["evidence_summary"] = f"{item_id} evidence verified in private release notes"
target.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
PY

ASSOPS_FIRST_DEPLOYABLE_REQUIRE_ALL_VERIFIED=true \
  bash scripts/first-deployable-external-evidence-validate.sh "$complete" \
  >/tmp/assops-first-deployable-external-evidence-complete-ok.log
grep -q "completion validation passed" /tmp/assops-first-deployable-external-evidence-complete-ok.log

bad="$tmpdir/external-evidence-status.missing-verified-field.json"
python3 - "$complete" "$bad" <<'PY'
import json
import sys
from pathlib import Path

source = Path(sys.argv[1])
target = Path(sys.argv[2])
data = json.loads(source.read_text(encoding="utf-8"))
data["entries"][0]["verified_at"] = ""
target.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
PY

if ASSOPS_FIRST_DEPLOYABLE_REQUIRE_ALL_VERIFIED=true \
  bash scripts/first-deployable-external-evidence-validate.sh "$bad" \
  >/tmp/assops-first-deployable-external-evidence-complete-bad.log 2>&1; then
  echo "complete evidence validator accepted missing verified field" >&2
  exit 1
fi
grep -q "verified status missing fields" /tmp/assops-first-deployable-external-evidence-complete-bad.log

echo "first-deployable external evidence complete validate self-test passed"
