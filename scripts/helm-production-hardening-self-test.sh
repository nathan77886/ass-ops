#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

rendered="$tmpdir/assops-production-rendered.yaml"

helm template assops deploy/helm/assops \
  -f deploy/helm/assops/values.production.example.yaml \
  >"$rendered"

require_line() {
  local pattern="$1"
  local description="$2"
  if ! grep -Eq "$pattern" "$rendered"; then
    echo "missing production Helm hardening evidence: $description" >&2
    exit 1
  fi
}

require_absent() {
  local pattern="$1"
  local description="$2"
  if grep -Eq "$pattern" "$rendered"; then
    echo "unexpected production Helm render evidence: $description" >&2
    exit 1
  fi
}

require_count() {
  local pattern="$1"
  local expected="$2"
  local description="$3"
  local actual
  actual="$(grep -Ec "$pattern" "$rendered" || true)"
  if [[ "$actual" != "$expected" ]]; then
    echo "unexpected production Helm hardening count for $description: got $actual, want $expected" >&2
    exit 1
  fi
}

require_count '^kind: NetworkPolicy$' 2 'web and gateway NetworkPolicies when external PostgreSQL is used'
require_count '^kind: PodDisruptionBudget$' 4 'application PodDisruptionBudgets'
require_count '^kind: PersistentVolumeClaim$' 5 'retained application PVCs without chart-managed PostgreSQL'

require_line '^kind: Ingress$' 'Ingress enabled'
require_line '^[[:space:]]*ingressClassName: "nginx"$' 'reviewed ingress class'
require_line '^[[:space:]]*secretName: "assops-production-tls"$' 'TLS Secret reference'
require_line '^[[:space:]]*- host: "assops.example.com"$' 'production host rule'

require_line '^automountServiceAccountToken: false$' 'ServiceAccount token automount disabled'
require_line '^[[:space:]]*automountServiceAccountToken: false$' 'pod token automount disabled'
require_line '^[[:space:]]*seccompProfile:$' 'pod seccomp profile'
require_line '^[[:space:]]*type: RuntimeDefault$' 'RuntimeDefault seccomp profile'
require_line '^[[:space:]]*allowPrivilegeEscalation: false$' 'container privilege escalation disabled'
require_line '^[[:space:]]*readOnlyRootFilesystem: true$' 'read-only root filesystem for Go containers'
require_line '^[[:space:]]*runAsNonRoot: true$' 'non-root Go containers'
require_line '^[[:space:]]*runAsUser: 65532$' 'explicit non-root UID'
require_line '^[[:space:]]*drop:$' 'capability drop list'
require_line '^[[:space:]]*- ALL$' 'all Linux capabilities dropped'

require_line '^[[:space:]]*storageClassName: "assops-retain"$' 'reviewed retained StorageClass'
require_line 'ghcr.io/nathan77886/assops-gateway:v0.1.0' 'production gateway image reference'
require_line 'ghcr.io/nathan77886/assops-worker:v0.1.0' 'production worker image reference'
require_line 'ghcr.io/nathan77886/assops-node-worker:v0.1.0' 'production node-worker image reference'
require_line 'ghcr.io/nathan77886/assops-web:v0.1.0' 'production web image reference'

require_absent '^kind: Secret$' 'chart-managed application Secret in production overlay'
require_absent '^kind: StatefulSet$' 'chart-managed PostgreSQL in production overlay'
require_absent 'change-me-' 'placeholder secret material in production render'

echo "helm-production-hardening self-test passed"
