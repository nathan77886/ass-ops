#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

handoff_dir="$tmpdir/handoff"
ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-schema-self-test-plan.log

python3 - "$handoff_dir" <<'PY'
import json
import sys
from pathlib import Path

handoff_dir = Path(sys.argv[1])
schema_files = {
    "completion-audit.schema.json": "assops.first_deployable.completion_audit.schema.json",
    "external-evidence-checklist.schema.json": "assops.first_deployable.external_evidence.schema.json",
    "external-evidence-status.schema.json": "assops.first_deployable.external_evidence_status.schema.json",
}
data_files = {
    "completion-audit.schema.json": "completion-audit.json",
    "external-evidence-checklist.schema.json": "external-evidence-checklist.json",
    "external-evidence-status.schema.json": "external-evidence-status.example.json",
}
manifest = json.loads((handoff_dir / "manifest.json").read_text(encoding="utf-8"))
manifest_paths = {entry.get("path") for entry in manifest.get("files", [])}

seen_ids = set()
for schema_name, expected_id in schema_files.items():
    if schema_name not in manifest_paths:
        raise SystemExit(f"schema missing from manifest: {schema_name}")
    schema = json.loads((handoff_dir / schema_name).read_text(encoding="utf-8"))
    if schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
        raise SystemExit(f"schema draft mismatch: {schema_name}")
    if schema.get("$id") != expected_id:
        raise SystemExit(f"schema id mismatch: {schema_name}")
    if schema.get("$id") in seen_ids:
        raise SystemExit(f"duplicate schema id: {schema.get('$id')}")
    seen_ids.add(schema.get("$id"))
    if schema.get("additionalProperties") is not False:
        raise SystemExit(f"schema must reject extra top-level fields: {schema_name}")
    data = json.loads((handoff_dir / data_files[schema_name]).read_text(encoding="utf-8"))
    expected_data_schema = schema.get("properties", {}).get("schema", {}).get("const")
    if data.get("schema") != expected_data_schema:
        raise SystemExit(f"data schema does not match schema const: {schema_name}")

print("first-deployable handoff schema self-test passed")
PY

bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-schema-self-test-validate.log
