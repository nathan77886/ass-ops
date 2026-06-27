#!/usr/bin/env bash
set -euo pipefail

owner="${ASSOPS_RELEASE_IMAGE_OWNER:-}"
version="${ASSOPS_RELEASE_IMAGE_VERSION:-}"
registry="${ASSOPS_RELEASE_IMAGE_REGISTRY:-ghcr.io}"
push="${ASSOPS_RELEASE_IMAGE_PUSH:-false}"
platform="${ASSOPS_RELEASE_IMAGE_PLATFORM:-linux/amd64}"
build_args="${ASSOPS_RELEASE_IMAGE_BUILD_ARGS:-}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for release-images" >&2
    exit 1
  }
}

usage() {
  cat >&2 <<'TXT'
release-images builds ASSOPS runtime images with the same GHCR naming shape as the release workflow.

Required:
  ASSOPS_RELEASE_IMAGE_OWNER      GHCR owner or organization, for example nathan77886
  ASSOPS_RELEASE_IMAGE_VERSION    Version tag, for example v0.1.0

Optional:
  ASSOPS_RELEASE_IMAGE_REGISTRY   Registry host, default ghcr.io
  ASSOPS_RELEASE_IMAGE_PUSH       Set true to push after build; default false
  ASSOPS_RELEASE_IMAGE_PLATFORM   Docker build platform, default linux/amd64
  ASSOPS_RELEASE_IMAGE_BUILD_ARGS Extra docker build args, for example "--pull"

Examples:
  ASSOPS_RELEASE_IMAGE_OWNER=nathan77886 ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 make release-images
  docker login ghcr.io
  ASSOPS_RELEASE_IMAGE_OWNER=nathan77886 ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 ASSOPS_RELEASE_IMAGE_PUSH=true make release-images
TXT
}

need docker

if [[ -z "$owner" || -z "$version" ]]; then
  usage
  exit 1
fi

case "$push" in
  true|false) ;;
  *)
    echo "ASSOPS_RELEASE_IMAGE_PUSH must be true or false" >&2
    exit 1
    ;;
esac

owner_lower="$(printf '%s' "$owner" | tr '[:upper:]' '[:lower:]')"
targets=(gateway worker node-worker web)

for target in "${targets[@]}"; do
  image="${registry}/${owner_lower}/assops-${target}:${version}"
  echo "building ${image}"
  # shellcheck disable=SC2086
  docker build \
    --platform "$platform" \
    --target "$target" \
    -t "$image" \
    $build_args \
    .

  if [[ "$push" == "true" ]]; then
    echo "pushing ${image}"
    docker push "$image"
  fi
done

cat <<TXT
release-images completed for ${registry}/${owner_lower}/assops-*:${version}
push=${push}
TXT
