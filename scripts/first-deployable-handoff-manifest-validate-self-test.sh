#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

handoff_dir="$tmpdir/handoff"
ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-manifest-validate-self-test-plan.log

bash scripts/first-deployable-handoff-manifest-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-manifest-validate-ok.log

printf '\nmanifest checksum mutation\n' >>"$handoff_dir/README.md"
if bash scripts/first-deployable-handoff-manifest-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-manifest-validate-bad-checksum.log 2>&1; then
  echo "manifest validator accepted a mutated handoff file" >&2
  exit 1
fi
grep -q "checksum mismatch" /tmp/assops-first-deployable-handoff-manifest-validate-bad-checksum.log

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$handoff_dir" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-manifest-validate-self-test-plan-refresh.log

python3 - "$handoff_dir/manifest.json" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))
data["files"].append({"path": "../unsafe.md", "sha256": "0" * 64})
path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
PY

if bash scripts/first-deployable-handoff-manifest-validate.sh "$handoff_dir" \
  >/tmp/assops-first-deployable-handoff-manifest-validate-bad-path.log 2>&1; then
  echo "manifest validator accepted an unsafe path" >&2
  exit 1
fi
grep -q "unsafe file path" /tmp/assops-first-deployable-handoff-manifest-validate-bad-path.log

echo "first-deployable handoff manifest validate self-test passed"
