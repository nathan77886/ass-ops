#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

cat >"$tmpdir/helm" <<'SH'
#!/usr/bin/env bash
cat <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: assops-gateway
spec:
  template:
    spec:
      containers:
        - name: gateway
          image: registry.example.local/assops/gateway:test
        - name: worker
          image: "registry.example.local/assops/worker:test"
YAML
SH

cat >"$tmpdir/docker" <<'SH'
#!/usr/bin/env bash
if [[ "$1" != "manifest" || "$2" != "inspect" ]]; then
  exit 2
fi
case "$3" in
  registry.example.local/assops/gateway:test|registry.example.local/assops/worker:test)
    exit 0
    ;;
esac
exit 1
SH

chmod +x "$tmpdir/helm" "$tmpdir/docker"
touch "$tmpdir/values.yaml"

PATH="$tmpdir:$PATH" \
  ASSOPS_HELM_IMAGE_PREFLIGHT_VALUES="$tmpdir/values.yaml" \
  ASSOPS_HELM_IMAGE_PREFLIGHT_TIMEOUT_SECONDS=2 \
  bash "$repo_root/scripts/helm-test-image-preflight.sh" >/tmp/assops-helm-image-preflight-self-test-ok.log

cat >"$tmpdir/docker" <<'SH'
#!/usr/bin/env bash
if [[ "$1" != "manifest" || "$2" != "inspect" ]]; then
  exit 2
fi
case "$3" in
  registry.example.local/assops/gateway:test)
    exit 0
    ;;
esac
exit 1
SH
chmod +x "$tmpdir/docker"

if PATH="$tmpdir:$PATH" \
  ASSOPS_HELM_IMAGE_PREFLIGHT_VALUES="$tmpdir/values.yaml" \
  ASSOPS_HELM_IMAGE_PREFLIGHT_TIMEOUT_SECONDS=2 \
  bash "$repo_root/scripts/helm-test-image-preflight.sh" >/tmp/assops-helm-image-preflight-self-test-fail.log 2>&1; then
  echo "expected helm-test-image-preflight to fail when a rendered image is not accessible" >&2
  exit 1
fi

grep -q "registry.example.local/assops/worker:test" /tmp/assops-helm-image-preflight-self-test-fail.log

cat >"$tmpdir/docker" <<'SH'
#!/usr/bin/env bash
if [[ "$1" != "manifest" || "$2" != "inspect" ]]; then
  exit 2
fi
sleep 30
SH
chmod +x "$tmpdir/docker"

if PATH="$tmpdir:$PATH" \
  ASSOPS_HELM_IMAGE_PREFLIGHT_VALUES="$tmpdir/values.yaml" \
  ASSOPS_HELM_IMAGE_PREFLIGHT_TIMEOUT_SECONDS=1 \
  bash "$repo_root/scripts/helm-test-image-preflight.sh" >/tmp/assops-helm-image-preflight-self-test-timeout.log 2>&1; then
  echo "expected helm-test-image-preflight to fail when docker manifest inspect hangs" >&2
  exit 1
fi
grep -q "image not accessible from registry metadata within 1s" /tmp/assops-helm-image-preflight-self-test-timeout.log
echo "helm-test-image-preflight self-test passed"
