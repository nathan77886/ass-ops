#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

log="$tmpdir/commands.log"

cat > "$tmpdir/helm" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'helm %s\n' "$*" >> "$ASSOPS_PREFLIGHT_STUB_LOG"
case "$1" in
  lint)
    exit 0
    ;;
  template)
    printf 'kind: List\nitems: []\n'
    exit 0
    ;;
  *)
    echo "unexpected helm args: $*" >&2
    exit 1
    ;;
esac
SH
chmod +x "$tmpdir/helm"

cat > "$tmpdir/kubectl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "$ASSOPS_PREFLIGHT_STUB_LOG"
if [[ "$1" == "get" && "$2" == "namespace" ]]; then
  exit 0
fi
if [[ "$1" == "-n" && "$3" == "get" && "$4" == "secret" ]]; then
  python3 - <<'PY'
import json

keys = [
    "DATABASE_URL",
    "ASSOPS_JWT_SECRET",
    "ASSOPS_WEBHOOK_SECRET_KEY",
    "ASSOPS_ADMIN_EMAIL",
    "ASSOPS_ADMIN_PASSWORD",
    "ASSOPS_APPROVAL_WEBHOOK_TOKEN",
    "ASSOPS_GITHUB_ACTIONS_READ_TOKEN",
    "ASSOPS_ARGO_READ_TOKEN",
    "test-assops-reader.yaml",
]
print(json.dumps({"data": {key: "cHJlc2VudA==" for key in keys}}))
PY
  exit 0
fi
echo "unexpected kubectl args: $*" >&2
exit 1
SH
chmod +x "$tmpdir/kubectl"

PATH="$tmpdir:$PATH" \
ASSOPS_PREFLIGHT_STUB_LOG="$log" \
ASSOPS_HELM_PREFLIGHT_NAMESPACE=assops-test \
ASSOPS_HELM_PREFLIGHT_RELEASE=test \
ASSOPS_HELM_PREFLIGHT_VALUES=deploy/helm/assops/values.test.example.yaml \
ASSOPS_HELM_PREFLIGHT_APP_SECRET=assops-test-secret \
ASSOPS_HELM_PREFLIGHT_KUBECONFIG_SECRET=assops-kubeconfigs \
ASSOPS_HELM_PREFLIGHT_KUBECONFIG_KEY=test-assops-reader.yaml \
bash scripts/helm-test-preflight.sh

if grep -E '\bkubectl (apply|delete|patch|rollout|port-forward)\b|\bhelm (upgrade|install|uninstall|rollback)\b' "$log" >/dev/null; then
  echo "helm-test-preflight used a mutating command" >&2
  cat "$log" >&2
  exit 1
fi

grep -q 'kubectl get namespace assops-test' "$log"
grep -q 'kubectl -n assops-test get secret assops-test-secret' "$log"
grep -q 'kubectl -n assops-test get secret assops-kubeconfigs' "$log"
