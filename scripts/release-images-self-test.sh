#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

cat > "$tmpdir/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
{
  printf 'docker'
  for arg in "$@"; do
    printf ' %s' "$arg"
  done
  printf '\n'
} >> "$ASSOPS_RELEASE_IMAGES_STUB_LOG"
SH
chmod +x "$tmpdir/docker"

if PATH="$tmpdir:$PATH" bash "$repo_root/scripts/release-images.sh" >/tmp/assops-release-images-missing.log 2>&1; then
  echo "expected release-images to fail without owner/version" >&2
  exit 1
fi
grep -q "ASSOPS_RELEASE_IMAGE_OWNER" /tmp/assops-release-images-missing.log
grep -q "ASSOPS_RELEASE_IMAGE_VERSION" /tmp/assops-release-images-missing.log

build_log="$tmpdir/build.log"
PATH="$tmpdir:$PATH" \
ASSOPS_RELEASE_IMAGES_STUB_LOG="$build_log" \
ASSOPS_RELEASE_IMAGE_OWNER=Nathan77886 \
ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 \
bash "$repo_root/scripts/release-images.sh" >/tmp/assops-release-images-build.log

for target in gateway worker node-worker web; do
  grep -q -- "--target ${target}" "$build_log"
  grep -q -- "-t ghcr.io/nathan77886/assops-${target}:v0.1.0" "$build_log"
done
if grep -q "docker push" "$build_log"; then
  echo "release-images pushed even though ASSOPS_RELEASE_IMAGE_PUSH was not true" >&2
  cat "$build_log" >&2
  exit 1
fi
grep -q "push=false" /tmp/assops-release-images-build.log

push_log="$tmpdir/push.log"
PATH="$tmpdir:$PATH" \
ASSOPS_RELEASE_IMAGES_STUB_LOG="$push_log" \
ASSOPS_RELEASE_IMAGE_OWNER=Nathan77886 \
ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 \
ASSOPS_RELEASE_IMAGE_PUSH=true \
bash "$repo_root/scripts/release-images.sh" >/tmp/assops-release-images-push.log

for target in gateway worker node-worker web; do
  grep -q -- "docker push ghcr.io/nathan77886/assops-${target}:v0.1.0" "$push_log"
done
grep -q "push=true" /tmp/assops-release-images-push.log

if PATH="$tmpdir:$PATH" \
  ASSOPS_RELEASE_IMAGES_STUB_LOG="$tmpdir/invalid.log" \
  ASSOPS_RELEASE_IMAGE_OWNER=nathan77886 \
  ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 \
  ASSOPS_RELEASE_IMAGE_PUSH=yes \
  bash "$repo_root/scripts/release-images.sh" >/tmp/assops-release-images-invalid.log 2>&1; then
  echo "expected release-images to reject invalid ASSOPS_RELEASE_IMAGE_PUSH" >&2
  exit 1
fi
grep -q "ASSOPS_RELEASE_IMAGE_PUSH must be true or false" /tmp/assops-release-images-invalid.log

echo "release-images self-test passed"
