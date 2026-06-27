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
  >/tmp/assops-first-deployable-external-evidence-self-test.log

checklist="$out_dir/external-evidence-checklist.json"
checklist_schema="$out_dir/external-evidence-checklist.schema.json"
status_template="$out_dir/external-evidence-status.example.json"
status_schema="$out_dir/external-evidence-status.schema.json"
audit="$out_dir/completion-audit.md"
audit_json="$out_dir/completion-audit.json"
audit_schema="$out_dir/completion-audit.schema.json"
readme="$out_dir/README.md"
manifest="$out_dir/manifest.json"

test -s "$checklist"
test -s "$checklist_schema"
test -s "$status_template"
test -s "$status_schema"
test -s "$audit"
test -s "$audit_json"
test -s "$audit_schema"
test -s "$readme"
test -s "$manifest"

python3 - "$checklist" "$status_template" "$audit" "$readme" "$manifest" "$audit_json" "$status_schema" "$checklist_schema" "$audit_schema" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

checklist_path = Path(sys.argv[1])
status_template_path = Path(sys.argv[2])
audit = Path(sys.argv[3]).read_text(encoding="utf-8")
readme = Path(sys.argv[4]).read_text(encoding="utf-8")
manifest_path = Path(sys.argv[5])
audit_json_path = Path(sys.argv[6])
status_schema_path = Path(sys.argv[7])
checklist_schema_path = Path(sys.argv[8])
audit_schema_path = Path(sys.argv[9])
data = json.loads(checklist_path.read_text(encoding="utf-8"))
status_template = json.loads(status_template_path.read_text(encoding="utf-8"))
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
audit_json = json.loads(audit_json_path.read_text(encoding="utf-8"))
status_schema = json.loads(status_schema_path.read_text(encoding="utf-8"))
checklist_schema = json.loads(checklist_schema_path.read_text(encoding="utf-8"))
audit_schema = json.loads(audit_schema_path.read_text(encoding="utf-8"))

expected = [
    ("github_ruleset_applied", "Repository administrator applies and verifies the GitHub ruleset", "branch-protection-plan.md"),
    ("retained_backup_artifact_published", "retained-backup artifact publication", "production-backup-rehearsal-plan.md"),
    ("restore_rehearsal_completed", "restore rehearsal against an explicit disposable database", "production-backup-rehearsal-plan.md"),
    ("ghcr_images_and_attestations_verified", "GHCR images plus attestations", "helm-values-v0.1.0.yaml"),
    ("promotion_preflight_completed", "deploy=false", "helm-rollout-rehearsal-plan.md"),
    ("protected_helm_rollout_completed", "deploy=true", "helm-rollout-rehearsal-plan.md"),
    ("provider_review_live_rehearsed", "provider-review live execution", "README.md"),
    ("graph_backed_readiness_verified", "assops-tool project readiness", "completion-audit.md"),
]

if data.get("schema") != "assops.first_deployable.external_evidence.v1":
    raise SystemExit("unexpected checklist schema")
if data.get("generated_locally") is not True or data.get("no_external_calls_made") is not True:
    raise SystemExit("checklist must preserve local no-call metadata")

items = {item.get("id"): item for item in data.get("items", [])}
expected_ids = {item[0] for item in expected}
if set(items) != expected_ids:
    raise SystemExit(f"checklist ids drifted: {sorted(items)}")

for item_id, audit_phrase, local_plan in expected:
    item = items[item_id]
    if item.get("completion_blocker") is not True:
        raise SystemExit(f"{item_id} is not marked as a completion blocker")
    if item.get("local_plan") != local_plan:
        raise SystemExit(f"{item_id} local_plan mismatch: {item.get('local_plan')}")
    if audit_phrase not in audit and audit_phrase not in readme:
        raise SystemExit(f"{item_id} missing matching audit/readme phrase: {audit_phrase}")
    for field in ("owner", "required_evidence"):
        value = item.get(field)
        if not isinstance(value, str) or not value.strip():
            raise SystemExit(f"{item_id} missing {field}")

for forbidden in ("token", "password", "private key", "kubeconfig content", "database URL"):
    if forbidden in checklist_path.read_text(encoding="utf-8").lower():
        raise SystemExit(f"checklist should describe evidence without requesting secret material: {forbidden}")

manifest_files = {item.get("path"): item.get("sha256") for item in manifest.get("files", [])}
if manifest.get("schema") != "assops.first_deployable.handoff_manifest.v1":
    raise SystemExit("unexpected manifest schema")
if "external-evidence-checklist.json" not in manifest_files:
    raise SystemExit("manifest must checksum external evidence checklist")
if "external-evidence-checklist.schema.json" not in manifest_files:
    raise SystemExit("manifest must checksum external evidence checklist schema")
if "external-evidence-status.example.json" not in manifest_files:
    raise SystemExit("manifest must checksum external evidence status template")
if "external-evidence-status.schema.json" not in manifest_files:
    raise SystemExit("manifest must checksum external evidence status schema")
if "completion-audit.json" not in manifest_files:
    raise SystemExit("manifest must checksum completion audit json")
if "completion-audit.schema.json" not in manifest_files:
    raise SystemExit("manifest must checksum completion audit schema")
actual = hashlib.sha256(checklist_path.read_bytes()).hexdigest()
if manifest_files["external-evidence-checklist.json"] != actual:
    raise SystemExit("external evidence checklist checksum mismatch")
checklist_schema_actual = hashlib.sha256(checklist_schema_path.read_bytes()).hexdigest()
if manifest_files["external-evidence-checklist.schema.json"] != checklist_schema_actual:
    raise SystemExit("external evidence checklist schema checksum mismatch")
status_actual = hashlib.sha256(status_template_path.read_bytes()).hexdigest()
if manifest_files["external-evidence-status.example.json"] != status_actual:
    raise SystemExit("external evidence status template checksum mismatch")
status_schema_actual = hashlib.sha256(status_schema_path.read_bytes()).hexdigest()
if manifest_files["external-evidence-status.schema.json"] != status_schema_actual:
    raise SystemExit("external evidence status schema checksum mismatch")
audit_actual = hashlib.sha256(audit_json_path.read_bytes()).hexdigest()
if manifest_files["completion-audit.json"] != audit_actual:
    raise SystemExit("completion audit json checksum mismatch")
audit_schema_actual = hashlib.sha256(audit_schema_path.read_bytes()).hexdigest()
if manifest_files["completion-audit.schema.json"] != audit_schema_actual:
    raise SystemExit("completion audit schema checksum mismatch")
status_ids = {entry.get("id") for entry in status_template.get("entries", [])}
if status_template.get("schema") != "assops.first_deployable.external_evidence_status.v1" or status_ids != expected_ids:
    raise SystemExit("external evidence status template ids drifted")
if status_template.get("source_checklist_sha256") != actual:
    raise SystemExit("external evidence status source checklist checksum drifted")
if status_schema.get("$id") != "assops.first_deployable.external_evidence_status.schema.json":
    raise SystemExit("unexpected external evidence status schema id")
if status_schema.get("properties", {}).get("entries", {}).get("minItems") != 8:
    raise SystemExit("external evidence status schema must pin item count")
if checklist_schema.get("$id") != "assops.first_deployable.external_evidence.schema.json":
    raise SystemExit("unexpected external evidence checklist schema id")
if checklist_schema.get("properties", {}).get("items", {}).get("minItems") != 8:
    raise SystemExit("external evidence checklist schema must pin item count")
if audit_json.get("schema") != "assops.first_deployable.completion_audit.v1":
    raise SystemExit("unexpected completion audit json schema")
if audit_schema.get("$id") != "assops.first_deployable.completion_audit.schema.json":
    raise SystemExit("unexpected completion audit schema id")
if set(audit_json.get("external_blockers", [])) != expected_ids:
    raise SystemExit("completion audit json blocker ids drifted")
if audit_json.get("completion_allowed_from_local_checks") is not False:
    raise SystemExit("completion audit json must block local-only completion")
PY

echo "first-deployable external evidence self-test passed"
