#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
handoff_dir="${1:-${ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR:-.assops/release-notes/first-deployable}}"

cd "$repo_root"

bash scripts/first-deployable-handoff-manifest-validate.sh "$handoff_dir" >/dev/null

python3 - "$handoff_dir" <<'PY'
import json
import re
import sys
import hashlib
from pathlib import Path

handoff_dir = Path(sys.argv[1])

required_files = {
    "README.md",
    "branch-protection-plan.md",
    "production-backup-rehearsal-plan.md",
    "helm-values-v0.1.0.yaml",
    "helm-rollout-rehearsal-plan.md",
    "completion-audit.md",
    "completion-audit.schema.json",
    "completion-audit.json",
    "external-evidence-checklist.schema.json",
    "external-evidence-checklist.json",
    "external-evidence-status.schema.json",
    "external-evidence-status.example.json",
    "manifest.json",
}
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
secret_shape = re.compile(
    r"(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^\s]+[:][^\s@]+@)"
)

missing = sorted(name for name in required_files if not (handoff_dir / name).is_file())
if missing:
    raise SystemExit(f"handoff pack missing required files: {', '.join(missing)}")

unexpected = sorted(path.name for path in handoff_dir.iterdir() if path.is_file() and path.name not in required_files)
if unexpected:
    raise SystemExit(f"handoff pack has unexpected files: {', '.join(unexpected)}")

for path in handoff_dir.iterdir():
    if path.is_file() and secret_shape.search(path.read_text(encoding="utf-8", errors="ignore")):
        raise SystemExit(f"handoff pack file contains secret-shaped material: {path.name}")

manifest = json.loads((handoff_dir / "manifest.json").read_text(encoding="utf-8"))
manifest_paths = {entry.get("path") for entry in manifest.get("files", [])}
expected_manifest_paths = required_files - {"manifest.json"}
if manifest_paths != expected_manifest_paths:
    raise SystemExit(f"handoff manifest file set mismatch: {sorted(manifest_paths)}")

checklist = json.loads((handoff_dir / "external-evidence-checklist.json").read_text(encoding="utf-8"))
checklist_schema = json.loads((handoff_dir / "external-evidence-checklist.schema.json").read_text(encoding="utf-8"))
status = json.loads((handoff_dir / "external-evidence-status.example.json").read_text(encoding="utf-8"))
status_schema = json.loads((handoff_dir / "external-evidence-status.schema.json").read_text(encoding="utf-8"))
completion_audit_schema = json.loads((handoff_dir / "completion-audit.schema.json").read_text(encoding="utf-8"))
completion_audit = json.loads((handoff_dir / "completion-audit.json").read_text(encoding="utf-8"))
readme = (handoff_dir / "README.md").read_text(encoding="utf-8")
audit = (handoff_dir / "completion-audit.md").read_text(encoding="utf-8")

if checklist.get("schema") != "assops.first_deployable.external_evidence.v1":
    raise SystemExit("unexpected external evidence checklist schema")
if checklist_schema.get("$id") != "assops.first_deployable.external_evidence.schema.json":
    raise SystemExit("unexpected external evidence checklist JSON Schema id")
if checklist_schema.get("properties", {}).get("schema", {}).get("const") != "assops.first_deployable.external_evidence.v1":
    raise SystemExit("external evidence checklist JSON Schema does not pin checklist schema")
if checklist.get("generated_locally") is not True or checklist.get("no_external_calls_made") is not True:
    raise SystemExit("external evidence checklist safety flags must be true")
if status.get("schema") != "assops.first_deployable.external_evidence_status.v1":
    raise SystemExit("unexpected external evidence status schema")
if status_schema.get("$id") != "assops.first_deployable.external_evidence_status.schema.json":
    raise SystemExit("unexpected external evidence status JSON Schema id")
if status_schema.get("properties", {}).get("schema", {}).get("const") != "assops.first_deployable.external_evidence_status.v1":
    raise SystemExit("external evidence status JSON Schema does not pin status schema")
if status.get("source_checklist") != "external-evidence-checklist.json":
    raise SystemExit("external evidence status source checklist mismatch")
actual_checklist_sha256 = hashlib.sha256((handoff_dir / "external-evidence-checklist.json").read_bytes()).hexdigest()
if status.get("source_checklist_sha256") != actual_checklist_sha256:
    raise SystemExit("external evidence status source checklist sha256 mismatch")
if completion_audit.get("schema") != "assops.first_deployable.completion_audit.v1":
    raise SystemExit("unexpected completion audit schema")
if completion_audit_schema.get("$id") != "assops.first_deployable.completion_audit.schema.json":
    raise SystemExit("unexpected completion audit JSON Schema id")
if completion_audit_schema.get("properties", {}).get("schema", {}).get("const") != "assops.first_deployable.completion_audit.v1":
    raise SystemExit("completion audit JSON Schema does not pin audit schema")
if completion_audit.get("generated_locally") is not True or completion_audit.get("no_external_calls_made") is not True:
    raise SystemExit("completion audit safety flags must be true")
if completion_audit.get("completion_allowed_from_local_checks") is not False:
    raise SystemExit("completion audit must block local-only completion")
if completion_audit.get("external_evidence_required") is not True:
    raise SystemExit("completion audit must require external evidence")
if completion_audit.get("local_gate") != "make first-deployable-check":
    raise SystemExit("completion audit local gate mismatch")
if completion_audit.get("completion_evidence_validator") != "make first-deployable-external-evidence-complete-validate":
    raise SystemExit("completion audit completion validator mismatch")

items = checklist.get("items", [])
if not isinstance(items, list):
    raise SystemExit("external evidence checklist items must be a list")
item_ids = {item.get("id") for item in items if isinstance(item, dict)}
status_ids = {entry.get("id") for entry in status.get("entries", []) if isinstance(entry, dict)}
if item_ids != expected_ids:
    raise SystemExit(f"external evidence checklist ids mismatch: {sorted(item_ids)}")
if status_ids != expected_ids:
    raise SystemExit(f"external evidence status ids mismatch: {sorted(status_ids)}")
audit_ids = set(completion_audit.get("external_blockers", []))
if audit_ids != expected_ids:
    raise SystemExit(f"completion audit external blocker ids mismatch: {sorted(audit_ids)}")

checklist_by_id = {item.get("id"): item for item in items if isinstance(item, dict)}
status_by_id = {entry.get("id"): entry for entry in status.get("entries", []) if isinstance(entry, dict)}

for item in items:
    item_id = item.get("id")
    if item.get("completion_blocker") is not True:
        raise SystemExit(f"external evidence item is not a completion blocker: {item_id}")
    local_plan = item.get("local_plan")
    if local_plan not in expected_manifest_paths:
        raise SystemExit(f"external evidence item references unknown local plan: {item_id}")
    for key in ("owner", "required_evidence"):
        if not isinstance(item.get(key), str) or not item[key].strip():
            raise SystemExit(f"external evidence item missing {key}: {item_id}")
        if status_by_id[item_id].get(key) != item[key]:
            raise SystemExit(f"external evidence status {key} mismatch: {item_id}")
    if local_plan != "README.md" and local_plan not in readme and local_plan not in audit:
        raise SystemExit(f"external evidence item lacks local-plan trace: {item_id}")

if "Do not mark the first deployable goal complete from local checks alone" not in audit:
    raise SystemExit("completion audit missing local-only completion guardrail")
if "Local Validation Before Handoff" not in readme:
    raise SystemExit("handoff README missing local validation section")

print("first-deployable handoff pack validation passed")
PY
