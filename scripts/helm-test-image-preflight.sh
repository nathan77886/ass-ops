#!/usr/bin/env bash
set -euo pipefail

namespace="${ASSOPS_HELM_IMAGE_PREFLIGHT_NAMESPACE:-assops-test}"
release="${ASSOPS_HELM_IMAGE_PREFLIGHT_RELEASE:-assops}"
values_file="${ASSOPS_HELM_IMAGE_PREFLIGHT_VALUES:-deploy/helm/assops/values.test.example.yaml}"
extra_values="${ASSOPS_HELM_IMAGE_PREFLIGHT_EXTRA_VALUES:-}"
manifest_timeout="${ASSOPS_HELM_IMAGE_PREFLIGHT_TIMEOUT_SECONDS:-20}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for helm-test-image-preflight" >&2
    exit 1
  }
}

need helm
need docker
need python3
need timeout

if [[ ! -f "$values_file" ]]; then
  echo "values file not found: $values_file" >&2
  exit 1
fi

args=(template "$release" deploy/helm/assops -n "$namespace" -f "$values_file")
if [[ -n "$extra_values" ]]; then
  IFS=':' read -r -a extra_files <<< "$extra_values"
  for file in "${extra_files[@]}"; do
    if [[ -z "$file" ]]; then
      continue
    fi
    if [[ ! -f "$file" ]]; then
      echo "extra values file not found: $file" >&2
      exit 1
    fi
    args+=(-f "$file")
  done
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

rendered_file="$tmpdir/rendered.yaml"
helm "${args[@]}" > "$rendered_file"
images="$(
  python3 - "$rendered_file" <<'PY'
import re
import sys

path = sys.argv[1]
images = []
seen = set()
with open(path, encoding="utf-8") as handle:
    for line in handle:
        match = re.match(r"\s*image:\s*['\"]?([^'\"\s]+)['\"]?\s*$", line)
        if not match:
            continue
        image = match.group(1)
        if image not in seen:
            seen.add(image)
            images.append(image)
for image in images:
    print(image)
PY
)"

if [[ -z "$images" ]]; then
  echo "no container images found in Helm render" >&2
  exit 1
fi

failed=0
while IFS= read -r image; do
  if [[ -z "$image" ]]; then
    continue
  fi
  if timeout -k 5s "${manifest_timeout}s" docker manifest inspect "$image" >/dev/null 2>&1; then
    echo "image ok: $image"
  else
    echo "image not accessible from registry metadata within ${manifest_timeout}s: $image" >&2
    failed=1
  fi
done <<< "$images"

if [[ "$failed" -ne 0 ]]; then
  echo "one or more rendered images are not registry-accessible; push images or docker login/use imagePullSecrets before Helm install" >&2
  exit 1
fi

echo "helm-test-image-preflight passed for ${release} in namespace ${namespace}"
