#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

handoff_dir="$tmpdir/handoff"
ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-plan.log

bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-ok.log

touch "$handoff_dir/stale-artifact.txt"
if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-extra-file.log 2>&1; then
  echo "handoff pack validator accepted an unexpected file" >&2
  exit 1
fi
grep -q "unexpected files" /tmp/assops-first-deployable-handoff-validate-extra-file.log
rm "$handoff_dir/stale-artifact.txt"

python3 - "$handoff_dir/external-evidence-checklist.json" "$handoff_dir/external-evidence-status.example.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
status_path = Path(sys.argv[2])
manifest_path = Path(sys.argv[3])
data = json.loads(path.read_text(encoding="utf-8"))
data["items"][0]["completion_blocker"] = False
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
checklist_sha = hashlib.sha256(path.read_bytes()).hexdigest()
status = json.loads(status_path.read_text(encoding="utf-8"))
status["source_checklist_sha256"] = checklist_sha
status_path.write_text(json.dumps(status, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "external-evidence-checklist.json":
        item["sha256"] = checklist_sha
    if item.get("path") == "external-evidence-status.example.json":
        item["sha256"] = hashlib.sha256(status_path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-blocker.log 2>&1; then
  echo "handoff pack validator accepted a non-blocking external evidence item" >&2
  exit 1
fi
grep -q "not a completion blocker" /tmp/assops-first-deployable-handoff-validate-bad-blocker.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-refresh.log

python3 - "$handoff_dir/external-evidence-status.example.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
data = json.loads(path.read_text(encoding="utf-8"))
data["entries"][0]["id"] = "unexpected_external_evidence"
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "external-evidence-status.example.json":
        item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-status.log 2>&1; then
  echo "handoff pack validator accepted a drifted status template id" >&2
  exit 1
fi
grep -q "external evidence status ids mismatch" /tmp/assops-first-deployable-handoff-validate-bad-status.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-refresh-owner.log

python3 - "$handoff_dir/external-evidence-status.example.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
data = json.loads(path.read_text(encoding="utf-8"))
data["entries"][0]["owner"] = "wrong_owner"
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "external-evidence-status.example.json":
        item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-owner.log 2>&1; then
  echo "handoff pack validator accepted a status owner mismatch" >&2
  exit 1
fi
grep -q "external evidence status owner mismatch" /tmp/assops-first-deployable-handoff-validate-bad-owner.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-refresh-sha.log

python3 - "$handoff_dir/external-evidence-status.example.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
data = json.loads(path.read_text(encoding="utf-8"))
data["source_checklist_sha256"] = "0" * 64
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "external-evidence-status.example.json":
        item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-source-sha.log 2>&1; then
  echo "handoff pack validator accepted a status source checklist checksum mismatch" >&2
  exit 1
fi
grep -q "source checklist sha256 mismatch" /tmp/assops-first-deployable-handoff-validate-bad-source-sha.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-refresh-schema.log

python3 - "$handoff_dir/external-evidence-status.schema.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
data = json.loads(path.read_text(encoding="utf-8"))
data["$id"] = "wrong.schema"
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "external-evidence-status.schema.json":
        item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-schema.log 2>&1; then
  echo "handoff pack validator accepted a drifted evidence status schema" >&2
  exit 1
fi
grep -q "unexpected external evidence status JSON Schema id" /tmp/assops-first-deployable-handoff-validate-bad-schema.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-refresh-checklist-schema.log

python3 - "$handoff_dir/external-evidence-checklist.schema.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
data = json.loads(path.read_text(encoding="utf-8"))
data["$id"] = "wrong.checklist.schema"
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "external-evidence-checklist.schema.json":
        item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-checklist-schema.log 2>&1; then
  echo "handoff pack validator accepted a drifted evidence checklist schema" >&2
  exit 1
fi
grep -q "unexpected external evidence checklist JSON Schema id" /tmp/assops-first-deployable-handoff-validate-bad-checklist-schema.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-refresh-audit-schema.log

python3 - "$handoff_dir/completion-audit.schema.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
data = json.loads(path.read_text(encoding="utf-8"))
data["$id"] = "wrong.audit.schema"
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "completion-audit.schema.json":
        item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-audit-schema.log 2>&1; then
  echo "handoff pack validator accepted a drifted completion audit schema" >&2
  exit 1
fi
grep -q "unexpected completion audit JSON Schema id" /tmp/assops-first-deployable-handoff-validate-bad-audit-schema.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-validate-self-test-refresh-audit.log

python3 - "$handoff_dir/completion-audit.json" "$handoff_dir/manifest.json" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
data = json.loads(path.read_text(encoding="utf-8"))
data["completion_allowed_from_local_checks"] = True
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
for item in manifest["files"]:
    if item.get("path") == "completion-audit.json":
        item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-validate-bad-audit.log 2>&1; then
  echo "handoff pack validator accepted local-only completion" >&2
  exit 1
fi
grep -q "must block local-only completion" /tmp/assops-first-deployable-handoff-validate-bad-audit.log

echo "first-deployable handoff pack validate self-test passed"
