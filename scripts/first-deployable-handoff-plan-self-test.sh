#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

out_dir="$tmpdir/handoff"

cd "$repo_root"

ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$out_dir" \
ASSOPS_FIRST_DEPLOYABLE_REPO=nathan77886/ass-ops \
ASSOPS_FIRST_DEPLOYABLE_GHCR_OWNER=nathan77886 \
ASSOPS_FIRST_DEPLOYABLE_VERSION=v0.1.0 \
ASSOPS_FIRST_DEPLOYABLE_ENV=production \
ASSOPS_FIRST_DEPLOYABLE_NAMESPACE=assops \
ASSOPS_FIRST_DEPLOYABLE_RELEASE=assops \
ASSOPS_FIRST_DEPLOYABLE_ENV_VALUES=deploy/helm/assops/values.production.example.yaml \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-self-test.log

test -s "$out_dir/README.md"
test -s "$out_dir/branch-protection-plan.md"
test -s "$out_dir/production-backup-rehearsal-plan.md"
test -s "$out_dir/helm-values-v0.1.0.yaml"
test -s "$out_dir/helm-rollout-rehearsal-plan.md"
test -s "$out_dir/completion-audit.md"
test -s "$out_dir/completion-audit.schema.json"
test -s "$out_dir/completion-audit.json"
test -s "$out_dir/external-evidence-checklist.schema.json"
test -s "$out_dir/external-evidence-checklist.json"
test -s "$out_dir/external-evidence-status.schema.json"
test -s "$out_dir/external-evidence-status.example.json"
test -s "$out_dir/manifest.json"

grep -q "ASSOPS First Deployable Handoff" "$out_dir/README.md"
grep -q "No GitHub, registry, Kubernetes, Argo, database, MQ, or provider API mutation" "$out_dir/README.md"
grep -q "External Actions Still Required" "$out_dir/README.md"
grep -q "Repository administrator applies and verifies the GitHub ruleset" "$out_dir/README.md"
grep -q "Protected environment owner runs restore rehearsal against a disposable database" "$out_dir/README.md"
grep -q "Release operator verifies image publication, registry access, and image attestations" "$out_dir/README.md"
grep -Fq 'Promotion workflow runs with `deploy=false` first' "$out_dir/README.md"
grep -q "Private test operator runs provider-review live execution" "$out_dir/README.md"
grep -Fq 'Real environment Dashboard and `assops-tool project readiness` evidence' "$out_dir/README.md"
grep -q "Local Validation Before Handoff" "$out_dir/README.md"
grep -q "first-deployable-handoff-validate" "$out_dir/README.md"
grep -q "first-deployable-handoff-plan-manifest-validate" "$out_dir/README.md"
grep -q "first-deployable-external-evidence-validate" "$out_dir/README.md"
grep -q "first-deployable-external-evidence-complete-validate" "$out_dir/README.md"
grep -q "does not read token values, kubeconfig contents, database URLs" "$out_dir/README.md"
grep -q "Completion audit" "$out_dir/README.md"
grep -q "Completion audit schema" "$out_dir/README.md"
grep -q "Machine-readable completion audit" "$out_dir/README.md"
grep -q "External evidence checklist schema" "$out_dir/README.md"
grep -q "External evidence checklist" "$out_dir/README.md"
grep -q "External evidence status schema" "$out_dir/README.md"
grep -q "External evidence status template" "$out_dir/README.md"
grep -q "Handoff manifest" "$out_dir/README.md"
grep -q "first-deployable handoff pack validation passed" /tmp/assops-first-deployable-handoff-validate.log

grep -q "ASSOPS First Deployable Completion Audit" "$out_dir/completion-audit.md"
grep -q "External Evidence Still Required" "$out_dir/completion-audit.md"
grep -q "Do not mark the first deployable goal complete from local checks alone" "$out_dir/completion-audit.md"

python3 -m json.tool "$out_dir/external-evidence-checklist.json" >/dev/null
python3 -m json.tool "$out_dir/external-evidence-checklist.schema.json" >/dev/null
python3 -m json.tool "$out_dir/external-evidence-status.schema.json" >/dev/null
python3 -m json.tool "$out_dir/external-evidence-status.example.json" >/dev/null
python3 -m json.tool "$out_dir/completion-audit.json" >/dev/null
python3 -m json.tool "$out_dir/completion-audit.schema.json" >/dev/null
python3 -m json.tool "$out_dir/manifest.json" >/dev/null
python3 - "$out_dir/external-evidence-checklist.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)

expected_ids = {
    "github_ruleset_applied",
    "retained_backup_artifact_published",
    "restore_rehearsal_completed",
    "ghcr_images_and_attestations_verified",
    "promotion_preflight_completed",
    "protected_helm_rollout_completed",
    "provider_review_live_rehearsed",
    "graph_backed_readiness_verified",
}
items = data.get("items", [])
ids = {item.get("id") for item in items}
if data.get("schema") != "assops.first_deployable.external_evidence.v1":
    raise SystemExit("unexpected external evidence schema")
if data.get("no_external_calls_made") is not True:
    raise SystemExit("external evidence checklist must record no-call generation")
if ids != expected_ids:
    raise SystemExit(f"unexpected external evidence ids: {sorted(ids)}")
if len(items) != 8:
    raise SystemExit(f"unexpected external evidence count: {len(items)}")
for item in items:
    if item.get("completion_blocker") is not True:
        raise SystemExit(f"item is not marked as blocker: {item.get('id')}")
    for key in ("owner", "required_evidence", "local_plan"):
        if not item.get(key):
            raise SystemExit(f"item {item.get('id')} missing {key}")
PY

python3 - "$out_dir/manifest.json" "$out_dir" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

manifest_path = Path(sys.argv[1])
out_dir = Path(sys.argv[2])
data = json.loads(manifest_path.read_text(encoding="utf-8"))
expected_files = {
    "README.md",
    "branch-protection-plan.md",
    "production-backup-rehearsal-plan.md",
    "helm-values-v0.1.0.yaml",
    "helm-rollout-rehearsal-plan.md",
    "completion-audit.md",
    "completion-audit.schema.json",
    "completion-audit.json",
    "external-evidence-checklist.schema.json",
    "external-evidence-checklist.json",
    "external-evidence-status.schema.json",
    "external-evidence-status.example.json",
}
if data.get("schema") != "assops.first_deployable.handoff_manifest.v1":
    raise SystemExit("unexpected handoff manifest schema")
if data.get("no_external_calls_made") is not True:
    raise SystemExit("handoff manifest must record no-call generation")
files = data.get("files", [])
paths = {item.get("path") for item in files}
if paths != expected_files:
    raise SystemExit(f"unexpected manifest files: {sorted(paths)}")
for item in files:
    path = item.get("path")
    digest = item.get("sha256")
    actual = hashlib.sha256((out_dir / path).read_bytes()).hexdigest()
    if digest != actual:
        raise SystemExit(f"checksum mismatch for {path}")
PY

grep -q "ASSOPS Branch Protection Plan" "$out_dir/branch-protection-plan.md"
grep -q "does not call GitHub" "$out_dir/branch-protection-plan.md"
grep -q "Ruleset targets \`~DEFAULT_BRANCH\`" "$out_dir/branch-protection-plan.md"
grep -q "Required status checks are strict/fresh" "$out_dir/branch-protection-plan.md"
grep -q "\`Workflow Lint\`" "$out_dir/branch-protection-plan.md"
grep -q "\`Secret Scan\`" "$out_dir/branch-protection-plan.md"
grep -q "\`Go Vulnerability Check\`" "$out_dir/branch-protection-plan.md"
grep -q "/repos/nathan77886/ass-ops/rulesets" "$out_dir/branch-protection-plan.md"

grep -q "Production Backup Restore Rehearsal Plan" "$out_dir/production-backup-rehearsal-plan.md"
grep -q "ASSOPS_ACTIVE_DATABASE_URL" "$out_dir/production-backup-rehearsal-plan.md"
grep -q "ASSOPS_REHEARSAL_DATABASE_URL" "$out_dir/production-backup-rehearsal-plan.md"
grep -q "production-retained-backup.yml" "$out_dir/production-backup-rehearsal-plan.md"
grep -q "production-restore-rehearsal.yml" "$out_dir/production-backup-rehearsal-plan.md"
grep -Fq 'exactly one `assops-*.dump`' "$out_dir/production-backup-rehearsal-plan.md"

grep -q "Helm Rollout Rehearsal Plan" "$out_dir/helm-rollout-rehearsal-plan.md"
grep -q "Release artifacts and GHCR images have attestations verified" "$out_dir/helm-rollout-rehearsal-plan.md"
grep -q "Namespace-scoped kubeconfig and promotion RBAC are reviewed out of band" "$out_dir/helm-rollout-rehearsal-plan.md"
grep -q "deploy=false" "$out_dir/helm-rollout-rehearsal-plan.md"
grep -q "deploy=true" "$out_dir/helm-rollout-rehearsal-plan.md"
grep -q "does not read kubeconfig content" "$out_dir/helm-rollout-rehearsal-plan.md"

grep -q "image:" "$out_dir/helm-values-v0.1.0.yaml"
grep -q "repository: nathan77886/assops-gateway" "$out_dir/helm-values-v0.1.0.yaml"
grep -q "repository: nathan77886/assops-worker" "$out_dir/helm-values-v0.1.0.yaml"
grep -q "repository: nathan77886/assops-node-worker" "$out_dir/helm-values-v0.1.0.yaml"
grep -q "repository: nathan77886/assops-web" "$out_dir/helm-values-v0.1.0.yaml"
grep -q "tag: v0.1.0" "$out_dir/helm-values-v0.1.0.yaml"

if rg -n "(?i)(ghp_|github_pat_|BEGIN [A-Z ]*PRIVATE[ ]KEY|xox[baprs]-|AKIA[0-9A-Z]{16})" "$out_dir"; then
  echo "first-deployable handoff generated secret-shaped material" >&2
  exit 1
fi

if ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR="$tmpdir/bad" \
  ASSOPS_FIRST_DEPLOYABLE_ENV_VALUES="$tmpdir/missing-values.yaml" \
  bash scripts/first-deployable-handoff-plan.sh \
  >/tmp/assops-first-deployable-handoff-self-test-missing.log 2>&1; then
  echo "expected first-deployable handoff to reject missing environment values" >&2
  exit 1
fi
grep -q "environment values file not found" /tmp/assops-first-deployable-handoff-self-test-missing.log

echo "first-deployable-handoff-plan self-test passed"
