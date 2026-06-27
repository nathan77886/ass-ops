#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
handoff_dir="${1:-${ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR:-.assops/release-notes/first-deployable}}"
manifest_file="$handoff_dir/manifest.json"

cd "$repo_root"

python3 - "$handoff_dir" "$manifest_file" <<'PY'
import hashlib
import json
import re
import sys
from pathlib import Path, PurePosixPath

handoff_dir = Path(sys.argv[1])
manifest_file = Path(sys.argv[2])

if not manifest_file.is_file():
    raise SystemExit(f"handoff manifest not found: {manifest_file}")

text = manifest_file.read_text(encoding="utf-8")
secret_shape = re.compile(
    r"(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16}|postgres[:]//[^\s]+[:][^\s@]+@)"
)
if secret_shape.search(text):
    raise SystemExit("handoff manifest contains secret-shaped material")

data = json.loads(text)
if data.get("schema") != "assops.first_deployable.handoff_manifest.v1":
    raise SystemExit("unexpected handoff manifest schema")
if data.get("generated_locally") is not True or data.get("no_external_calls_made") is not True:
    raise SystemExit("handoff manifest safety flags must be true")

files = data.get("files")
if not isinstance(files, list) or not files:
    raise SystemExit("handoff manifest files must be a non-empty list")

seen = set()
for entry in files:
    if not isinstance(entry, dict):
        raise SystemExit("handoff manifest file entry must be an object")
    rel = entry.get("path")
    expected = entry.get("sha256")
    if not isinstance(rel, str) or not rel.strip():
        raise SystemExit("handoff manifest file path must be a string")
    if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-f]{64}", expected):
        raise SystemExit(f"{rel} has invalid sha256")
    posix = PurePosixPath(rel)
    if posix.is_absolute() or ".." in posix.parts or rel == "manifest.json":
        raise SystemExit(f"handoff manifest has unsafe file path: {rel}")
    if rel in seen:
        raise SystemExit(f"handoff manifest duplicates file path: {rel}")
    seen.add(rel)

    path = handoff_dir / rel
    if not path.is_file():
        raise SystemExit(f"handoff manifest file missing: {rel}")
    body = path.read_bytes()
    if secret_shape.search(body.decode("utf-8", errors="ignore")):
        raise SystemExit(f"handoff manifest file contains secret-shaped material: {rel}")
    actual = hashlib.sha256(body).hexdigest()
    if actual != expected:
        raise SystemExit(f"handoff manifest checksum mismatch: {rel}")

print("first-deployable handoff manifest validation passed")
PY
