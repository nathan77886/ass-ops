#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

mapfile -t self_test_targets < <(
  awk '/^[A-Za-z0-9_.-]+-self-test:/ { sub(":.*", "", $1); print $1 }' Makefile | sort -u
)

if ((${#self_test_targets[@]} == 0)); then
  echo "no Make self-test targets found" >&2
  exit 1
fi

gate_block="$(awk '
  /^first-deployable-check:/ { in_gate=1; next }
  in_gate && /^[A-Za-z0-9_.-]+:/ { exit }
  in_gate { print }
' Makefile)"

missing=()
for target in "${self_test_targets[@]}"; do
  script="scripts/${target}.sh"
  if [[ ! -s "$script" ]]; then
    missing+=("missing script for Make target: $script")
  fi
  if ! grep -Fq "$target" <<<"$gate_block"; then
    missing+=("$target is not wired into first-deployable-check")
  fi
  if ! grep -Fq "$target" docs/deploy-helm.md; then
    missing+=("$target is not listed in docs/deploy-helm.md")
  fi
  if ! grep -Fq "$target" scripts/first-deployable-completion-audit.sh; then
    missing+=("$target is not listed in first-deployable completion audit")
  fi
done

if ((${#missing[@]} > 0)); then
  printf 'first-deployable coverage audit failed\n' >&2
  printf -- '- %s\n' "${missing[@]}" >&2
  exit 1
fi

echo "first-deployable coverage audit self-test passed"
