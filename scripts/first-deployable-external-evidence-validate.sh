#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_file="${1:-${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_EVIDENCE_FILE:-.assops/release-notes/first-deployable/external-evidence-status.example.json}}"
require_all_verified="${ASSOPS_FIRST_DEPLOYABLE_REQUIRE_ALL_VERIFIED:-false}"

cd "$repo_root"

python3 - "$evidence_file" "$require_all_verified" <<'PY'
import json
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
require_all_verified = sys.argv[2].lower() == "true"
if not path.is_file():
    raise SystemExit(f"external evidence file not found: {path}")

text = path.read_text(encoding="utf-8")
secret_shape = re.compile(
    r"(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^\s]+[:][^\s@]+@)"
)
if secret_shape.search(text):
    raise SystemExit("external evidence file contains secret-shaped material")

data = json.loads(text)
expected_ids = {
    "github_ruleset_applied",
    "retained_backup_artifact_published",
    "restore_rehearsal_completed",
    "ghcr_images_and_attestations_verified",
    "promotion_preflight_completed",
    "protected_helm_rollout_completed",
    "provider_review_live_rehearsed",
    "graph_backed_readiness_verified",
}
allowed_status = {"pending", "verified", "rejected"}
sha256_shape = re.compile(r"^[0-9a-f]{64}$")
verified_at_shape = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
evidence_reference_shape = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/@:+-]{2,255}$")

if data.get("schema") != "assops.first_deployable.external_evidence_status.v1":
    raise SystemExit("unexpected external evidence status schema")
if data.get("source_checklist") != "external-evidence-checklist.json":
    raise SystemExit("external evidence status source checklist mismatch")
if not sha256_shape.fullmatch(str(data.get("source_checklist_sha256", ""))):
    raise SystemExit("external evidence status source checklist sha256 missing or invalid")
entries = data.get("entries")
if not isinstance(entries, list):
    raise SystemExit("external evidence status entries must be a list")

ids = [entry.get("id") for entry in entries if isinstance(entry, dict)]
if set(ids) != expected_ids or len(ids) != len(expected_ids):
    raise SystemExit(f"external evidence status ids mismatch: {sorted(ids)}")

for entry in entries:
    if not isinstance(entry, dict):
        raise SystemExit("external evidence status entry must be an object")
    item_id = entry.get("id")
    status = entry.get("status")
    if status not in allowed_status:
        raise SystemExit(f"{item_id} has invalid status: {status}")
    for key in ("owner", "required_evidence", "evidence_reference", "verified_by", "verified_at", "evidence_summary"):
        if not isinstance(entry.get(key, ""), str):
            raise SystemExit(f"{item_id} field must be a string: {key}")
    for key in ("owner", "required_evidence"):
        if not entry.get(key, "").strip():
            raise SystemExit(f"{item_id} field must not be empty: {key}")
    if status == "verified":
        missing = [key for key in ("evidence_reference", "verified_by", "verified_at") if not entry.get(key, "").strip()]
        if missing:
            raise SystemExit(f"{item_id} verified status missing fields: {', '.join(missing)}")
        if not verified_at_shape.fullmatch(entry["verified_at"]):
            raise SystemExit(f"{item_id} verified_at must be UTC RFC3339 seconds: YYYY-MM-DDTHH:MM:SSZ")
        if not evidence_reference_shape.fullmatch(entry["evidence_reference"]):
            raise SystemExit(f"{item_id} evidence_reference has invalid shape")
    if status == "rejected" and not entry.get("evidence_summary", "").strip():
        raise SystemExit(f"{item_id} rejected status missing evidence_summary")
    if require_all_verified and status != "verified":
        raise SystemExit(f"{item_id} is not verified")

if require_all_verified:
    print("first-deployable external evidence completion validation passed")
else:
    print("first-deployable external evidence status validation passed")
PY
