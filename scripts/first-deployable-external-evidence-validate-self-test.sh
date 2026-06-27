#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

out_dir="$tmpdir/handoff"
cd "$repo_root"

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$out_dir" \
ASSOPS_FIRST_DEPLOYABLE_REPO=nathan77886/ass-ops \
ASSOPS_FIRST_DEPLOYABLE_GHCR_OWNER=nathan77886 \
ASSOPS_FIRST_DEPLOYABLE_VERSION=v0.1.0 \
ASSOPS_FIRST_DEPLOYABLE_ENV=production \
ASSOPS_FIRST_DEPLOYABLE_NAMESPACE=assops \
ASSOPS_FIRST_DEPLOYABLE_RELEASE=assops \
ASSOPS_FIRST_DEPLOYABLE_ENV_VALUES=deploy/helm/assops/values.production.example.yaml \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-external-evidence-validate-self-test.log

template="$out_dir/external-evidence-status.example.json"
test -s "$template"
bash scripts/first-deployable-external-evidence-validate.sh "$template" \
  >/tmp/assops-first-deployable-external-evidence-validate-ok.log

bad_verified="$tmpdir/bad-verified.json"
python3 - "$template" "$bad_verified" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
data["entries"][0]["status"] = "verified"
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
if bash scripts/first-deployable-external-evidence-validate.sh "$bad_verified" \
  >/tmp/assops-first-deployable-external-evidence-validate-bad-verified.log 2>&1; then
  echo "expected verified evidence without reference to fail" >&2
  exit 1
fi
grep -q "verified status missing fields" /tmp/assops-first-deployable-external-evidence-validate-bad-verified.log

bad_secret="$tmpdir/bad-secret.json"
python3 - "$template" "$bad_secret" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
data["entries"][0]["evidence_reference"] = "ghp_exampletoken"
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
if bash scripts/first-deployable-external-evidence-validate.sh "$bad_secret" \
  >/tmp/assops-first-deployable-external-evidence-validate-bad-secret.log 2>&1; then
  echo "expected secret-shaped evidence to fail" >&2
  exit 1
fi
grep -q "secret-shaped material" /tmp/assops-first-deployable-external-evidence-validate-bad-secret.log

bad_timestamp="$tmpdir/bad-timestamp.json"
python3 - "$template" "$bad_timestamp" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
entry = data["entries"][0]
entry["status"] = "verified"
entry["evidence_reference"] = "private-release-notes/github-ruleset.md"
entry["verified_by"] = "repository_admin"
entry["verified_at"] = "2026-06-27 00:00:00"
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
if bash scripts/first-deployable-external-evidence-validate.sh "$bad_timestamp" \
  >/tmp/assops-first-deployable-external-evidence-validate-bad-timestamp.log 2>&1; then
  echo "expected invalid verified_at shape to fail" >&2
  exit 1
fi
grep -q "verified_at must be UTC RFC3339 seconds" /tmp/assops-first-deployable-external-evidence-validate-bad-timestamp.log

bad_reference="$tmpdir/bad-reference.json"
python3 - "$template" "$bad_reference" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
entry = data["entries"][0]
entry["status"] = "verified"
entry["evidence_reference"] = "../private/github-ruleset.md"
entry["verified_by"] = "repository_admin"
entry["verified_at"] = "2026-06-27T00:00:00Z"
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
if bash scripts/first-deployable-external-evidence-validate.sh "$bad_reference" \
  >/tmp/assops-first-deployable-external-evidence-validate-bad-reference.log 2>&1; then
  echo "expected invalid evidence_reference shape to fail" >&2
  exit 1
fi
grep -q "evidence_reference has invalid shape" /tmp/assops-first-deployable-external-evidence-validate-bad-reference.log

bad_rejected="$tmpdir/bad-rejected.json"
python3 - "$template" "$bad_rejected" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
data["entries"][0]["status"] = "rejected"
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
if bash scripts/first-deployable-external-evidence-validate.sh "$bad_rejected" \
  >/tmp/assops-first-deployable-external-evidence-validate-bad-rejected.log 2>&1; then
  echo "expected rejected evidence without summary to fail" >&2
  exit 1
fi
grep -q "rejected status missing evidence_summary" /tmp/assops-first-deployable-external-evidence-validate-bad-rejected.log

bad_owner="$tmpdir/bad-owner.json"
python3 - "$template" "$bad_owner" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
data["entries"][0]["owner"] = ""
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
if bash scripts/first-deployable-external-evidence-validate.sh "$bad_owner" \
  >/tmp/assops-first-deployable-external-evidence-validate-bad-owner.log 2>&1; then
  echo "expected empty owner to fail" >&2
  exit 1
fi
grep -q "field must not be empty: owner" /tmp/assops-first-deployable-external-evidence-validate-bad-owner.log

bad_source_sha="$tmpdir/bad-source-sha.json"
python3 - "$template" "$bad_source_sha" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
data["source_checklist_sha256"] = "not-a-sha"
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
if bash scripts/first-deployable-external-evidence-validate.sh "$bad_source_sha" \
  >/tmp/assops-first-deployable-external-evidence-validate-bad-source-sha.log 2>&1; then
  echo "expected invalid source checklist sha256 to fail" >&2
  exit 1
fi
grep -q "source checklist sha256 missing or invalid" /tmp/assops-first-deployable-external-evidence-validate-bad-source-sha.log

echo "first-deployable external evidence validate self-test passed"
