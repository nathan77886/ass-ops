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
set -euo pipefail
case "${ASSOPS_PROVIDER_REVIEW_LIVE_TEST_STUB_MODE:-ready}" in
  ready)
    cat <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: assops-config
data:
  ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION: "true"
  ASSOPS_ARM_PROVIDER_REVIEW_MUTATION: "true"
---
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
      envFrom:
        - secretRef:
            name: assops-test-secret
---
apiVersion: v1
kind: Secret
metadata:
  name: assops-test-secret
stringData:
  ASSOPS_GITHUB_TEMPLATE_TOKEN: ""
  ASSOPS_GITEA_TEMPLATE_TOKEN: ""
YAML
    ;;
  blocked)
    cat <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: assops-config
data:
  ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION: "false"
  ASSOPS_ARM_PROVIDER_REVIEW_MUTATION: "false"
---
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
      envFrom:
        - secretRef:
            name: assops-test-secret
YAML
    ;;
  leaked)
    cat <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: assops-config
data:
  ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION: "true"
  ASSOPS_ARM_PROVIDER_REVIEW_MUTATION: "true"
  EXAMPLE_BAD_TOKEN: "github_pat_secret_test"
---
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
      envFrom:
        - secretRef:
            name: assops-test-secret
YAML
    ;;
esac
SH
chmod +x "$tmpdir/helm"
touch "$tmpdir/values.yaml"

PATH="$tmpdir:$PATH" \
  ASSOPS_PROVIDER_REVIEW_LIVE_TEST_VALUES="$tmpdir/values.yaml" \
  ASSOPS_PROVIDER_REVIEW_LIVE_TEST_OUTPUT="$tmpdir/plan.md" \
  bash "$repo_root/scripts/provider-review-live-test-plan.sh" >/tmp/assops-provider-review-live-test-plan-ok.log

grep -q "Blocking Findings" "$tmpdir/plan.md"
grep -q -- "- none" "$tmpdir/plan.md"

if PATH="$tmpdir:$PATH" \
  ASSOPS_PROVIDER_REVIEW_LIVE_TEST_STUB_MODE=blocked \
  ASSOPS_PROVIDER_REVIEW_LIVE_TEST_VALUES="$tmpdir/values.yaml" \
  bash "$repo_root/scripts/provider-review-live-test-plan.sh" >/tmp/assops-provider-review-live-test-plan-blocked.log 2>&1; then
  echo "expected provider-review-live-test-plan to fail when live execution gates are false" >&2
  exit 1
fi
grep -q "ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION must render as true" /tmp/assops-provider-review-live-test-plan-blocked.log
grep -q "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION must render as true" /tmp/assops-provider-review-live-test-plan-blocked.log

if PATH="$tmpdir:$PATH" \
  ASSOPS_PROVIDER_REVIEW_LIVE_TEST_STUB_MODE=leaked \
  ASSOPS_PROVIDER_REVIEW_LIVE_TEST_VALUES="$tmpdir/values.yaml" \
  bash "$repo_root/scripts/provider-review-live-test-plan.sh" >/tmp/assops-provider-review-live-test-plan-leaked.log 2>&1; then
  echo "expected provider-review-live-test-plan to fail when rendered manifest contains secret-shaped data" >&2
  exit 1
fi
grep -q "rendered manifest contains a value shaped like a secret" /tmp/assops-provider-review-live-test-plan-leaked.log

echo "provider-review-live-test-plan self-test passed"
