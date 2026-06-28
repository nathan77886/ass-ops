#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out_file="${ASSOPS_FIRST_DEPLOYABLE_COMPLETION_AUDIT_OUTPUT:-}"

cd "$repo_root"

required_files=(
  Makefile
  docs/deploy-helm.md
  docs/deploy-production.md
  docs/first-version-gap.md
  .github/workflows/ci.yml
  .github/workflows/release.yml
  .github/workflows/production-retained-backup.yml
  .github/workflows/production-restore-rehearsal.yml
  .github/rulesets/main-required-checks.json
  .github/CODEOWNERS
  deploy/helm/assops/values.production.example.yaml
  deploy/helm/assops/values.test.example.yaml
  deploy/helm/assops/values.test.private.example.yaml
  scripts/first-deployable-handoff-plan.sh
  scripts/first-deployable-handoff-manifest-validate.sh
  scripts/first-deployable-handoff-validate.sh
  scripts/production-backup-rehearsal-plan.sh
  scripts/helm-rollout-rehearsal-plan.sh
  scripts/provider-review-live-test-plan.sh
  scripts/release-images.sh
)

self_test_targets=(
  api-smoke-self-test
  compose-smoke-local-images-self-test
  first-deployable-completion-audit-self-test
  first-deployable-coverage-audit-self-test
  first-deployable-external-audit-runbook-self-test
  first-deployable-external-audit-validate-self-test
  first-deployable-external-evidence-complete-validate-self-test
  first-deployable-external-evidence-self-test
  first-deployable-external-evidence-validate-self-test
  first-deployable-handoff-manifest-validate-self-test
  first-deployable-handoff-plan-self-test
  first-deployable-handoff-schema-self-test
  first-deployable-handoff-validate-self-test
  helm-production-hardening-self-test
  helm-rollout-rehearsal-plan-self-test
  helm-test-image-preflight-self-test
  helm-test-preflight-self-test
  helm-test-smoke-self-test
  production-backup-rehearsal-plan-self-test
  provider-review-live-test-plan-self-test
  rehearsal-make-targets-self-test
  release-backup-schedule-plan-self-test
  release-branch-protection-plan-self-test
  release-helm-readiness-plan-self-test
  release-helm-test-readiness-plan-self-test
  release-helm-values-self-test
  release-images-self-test
  release-promotion-plan-self-test
  release-rehearsal-plans-self-test
  release-validate-bundle-self-test
  workflow-safety-self-test
)

failures=()
for file in "${required_files[@]}"; do
  if [[ ! -s "$file" ]]; then
    failures+=("missing required deployable evidence file: $file")
  fi
done

require_text() {
  local file="$1"
  local pattern="$2"
  local label="$3"
  if ! grep -Eq "$pattern" "$file"; then
    failures+=("missing $label in $file")
  fi
}

require_text Makefile 'first-deployable-check:' 'first-deployable gate target'
require_text Makefile 'first-deployable-handoff-plan-self-test' 'handoff self-test in deployable gate'
require_text Makefile 'release-promotion-plan-self-test' 'promotion self-test in deployable gate'
require_text Makefile 'release-backup-schedule-plan-self-test' 'backup schedule self-test in deployable gate'
require_text Makefile 'release-helm-values-self-test' 'release Helm values self-test in deployable gate'
require_text Makefile 'release-helm-readiness-plan-self-test' 'release Helm readiness self-test in deployable gate'
require_text Makefile 'release-helm-test-readiness-plan-self-test' 'Helm test readiness self-test in deployable gate'
require_text Makefile 'release-branch-protection-plan-self-test' 'branch protection self-test in deployable gate'
require_text docs/deploy-production.md 'make first-deployable-handoff-plan' 'handoff instructions'
require_text docs/deploy-helm.md 'make first-deployable-check' 'local deployable gate instructions'
require_text docs/first-version-gap.md 'ruleset still needs to be applied' 'branch-protection external blocker'
require_text docs/first-version-gap.md 'real environment rollout still require' 'environment rollout external blocker'
require_text scripts/first-deployable-handoff-plan.sh 'External Actions Still Required' 'handoff external action index'
require_text scripts/first-deployable-handoff-plan.sh 'completion-audit.md' 'handoff completion audit artifact'
require_text scripts/first-deployable-handoff-plan.sh 'completion-audit.json' 'handoff machine-readable completion audit artifact'
require_text scripts/first-deployable-handoff-plan.sh 'completion-audit.schema.json' 'handoff completion audit schema artifact'
require_text scripts/first-deployable-handoff-plan.sh 'external-evidence-checklist.schema.json' 'handoff external evidence checklist schema artifact'
require_text scripts/first-deployable-handoff-plan.sh 'external-evidence-checklist.json' 'handoff external evidence checklist artifact'
require_text scripts/first-deployable-handoff-plan.sh 'external-evidence-status.schema.json' 'handoff external evidence status schema artifact'
require_text scripts/first-deployable-handoff-plan.sh 'external-evidence-status.example.json' 'handoff external evidence status template artifact'
require_text scripts/first-deployable-handoff-plan.sh 'source_checklist_sha256' 'handoff status source checklist checksum artifact'
require_text scripts/first-deployable-handoff-plan.sh 'manifest.json' 'handoff checksum manifest artifact'
require_text scripts/first-deployable-handoff-plan.sh 'assops-tool project readiness' 'handoff graph-backed readiness blocker'
require_text scripts/first-deployable-handoff-plan.sh 'first-deployable-handoff-validate.sh' 'handoff generator post-generation validation'
require_text scripts/first-deployable-handoff-manifest-validate.sh 'checksum mismatch' 'handoff manifest checksum validation'
require_text scripts/first-deployable-handoff-validate.sh 'completion_blocker' 'handoff pack cross-file completion-blocker validation'
require_text scripts/first-deployable-handoff-validate.sh 'unexpected files' 'handoff pack stale artifact rejection'
require_text scripts/first-deployable-handoff-validate.sh 'external evidence status .* mismatch' 'handoff pack owner traceability validation'
require_text scripts/first-deployable-handoff-validate.sh 'source checklist sha256 mismatch' 'handoff status source checksum validation'
require_text scripts/first-deployable-handoff-validate.sh 'external evidence status JSON Schema' 'handoff status schema validation'
require_text scripts/first-deployable-handoff-validate.sh 'external evidence checklist JSON Schema' 'handoff checklist schema validation'
require_text scripts/first-deployable-handoff-validate.sh 'completion audit JSON Schema' 'handoff completion audit schema validation'
require_text scripts/first-deployable-external-evidence-validate.sh 'ASSOPS_FIRST_DEPLOYABLE_REQUIRE_ALL_VERIFIED' 'external evidence completion validation mode'
require_text scripts/first-deployable-external-evidence-validate.sh 'verified_at must be UTC RFC3339 seconds' 'external evidence timestamp validation'
require_text scripts/first-deployable-external-evidence-validate.sh 'rejected status missing evidence_summary' 'external evidence rejected-summary validation'
require_text scripts/provider-review-live-test-plan.sh 'Secret-shaped literal detected in render' 'provider-review no-secret render boundary'
require_text scripts/production-backup-rehearsal-plan.sh 'manual.*protected environment|Manual Dispatch Checks' 'backup rehearsal protected dispatch boundary'
require_text scripts/helm-rollout-rehearsal-plan.sh 'does not read kubeconfig' 'rollout rehearsal no-call boundary'
require_text .github/workflows/release.yml 'ghcr.io' 'GHCR image publication path'
require_text .github/workflows/production-retained-backup.yml 'ASSOPS_ACTIVE_DATABASE_URL' 'protected retained-backup source secret'
require_text .github/workflows/production-restore-rehearsal.yml 'ASSOPS_REHEARSAL_DATABASE_URL' 'protected restore rehearsal target secret'

for target in "${self_test_targets[@]}"; do
  require_text Makefile "$target" "$target Make target/gate coverage"
  require_text docs/deploy-helm.md "$target" "$target deploy-helm coverage"
done

if ((${#failures[@]} > 0)); then
  printf 'first-deployable completion audit failed\n' >&2
  printf -- '- %s\n' "${failures[@]}" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

report="$tmpdir/first-deployable-completion-audit.md"
cat >"$report" <<'EOF'
# ASSOPS First Deployable Completion Audit

Generated locally. This audit only checks repository evidence and known external blockers. It does not call GitHub, registries, Kubernetes, Argo, PostgreSQL, MQ, provider APIs, SSH, or Codex CLI.

## Local Evidence Present

- `make first-deployable-check` is wired as the offline deployable gate.
- Compose config, web build, API smoke, Helm preflight, Helm render, production Helm hardening, workflow safety, release image, release bundle, promotion, handoff, and rehearsal-plan self-tests are referenced by the gate.
- First-deployable handoff generator can produce branch-protection, backup rehearsal, release values, and Helm rollout plans without environment calls.
- First-deployable handoff generator also writes JSON Schemas for the machine-readable completion audit and external evidence checklist/status files, plus a checksum manifest for the environment-owner proof still required; the handoff validators check required files, reject stale unexpected files, verify file paths, safety flags, checksums, schema identities, schema id uniqueness, status-to-checklist checksum binding, checklist/status ids, owner/required-evidence traceability, completion-blocker flags, and local-plan references locally, and completion-mode evidence validation fails until every external evidence entry is verified with required proof fields, UTC timestamps, acceptable reference shape, and rejected-entry summaries.
- GitHub workflow files include release image publication, protected retained-backup, protected restore rehearsal, and promotion paths.
- Helm test, private-test, and production example overlays exist for environment-owned review.
- Self-test target coverage is pinned for `api-smoke-self-test`, `compose-smoke-local-images-self-test`, `first-deployable-completion-audit-self-test`, `first-deployable-coverage-audit-self-test`, `first-deployable-external-audit-runbook-self-test`, `first-deployable-external-audit-validate-self-test`, `first-deployable-external-evidence-complete-validate-self-test`, `first-deployable-external-evidence-self-test`, `first-deployable-external-evidence-validate-self-test`, `first-deployable-handoff-manifest-validate-self-test`, `first-deployable-handoff-plan-self-test`, `first-deployable-handoff-schema-self-test`, `first-deployable-handoff-validate-self-test`, `helm-production-hardening-self-test`, `helm-rollout-rehearsal-plan-self-test`, `helm-test-image-preflight-self-test`, `helm-test-preflight-self-test`, `helm-test-smoke-self-test`, `production-backup-rehearsal-plan-self-test`, `provider-review-live-test-plan-self-test`, `rehearsal-make-targets-self-test`, `release-backup-schedule-plan-self-test`, `release-branch-protection-plan-self-test`, `release-helm-readiness-plan-self-test`, `release-helm-test-readiness-plan-self-test`, `release-helm-values-self-test`, `release-images-self-test`, `release-promotion-plan-self-test`, `release-rehearsal-plans-self-test`, `release-validate-bundle-self-test`, and `workflow-safety-self-test`.

## External Evidence Still Required

1. Repository administrator applies and verifies the GitHub ruleset on the real repository.
2. Protected environment owner runs retained-backup artifact publication with reviewed storage and secrets.
3. Protected environment owner runs restore rehearsal against an explicit disposable database and preserves the private report.
4. Release operator publishes and verifies GHCR images plus attestations for the selected version.
5. Cluster operator reviews namespace, external Secret, TLS ingress, storage class, image pull access, promotion RBAC, previous values, and rollback evidence before any Helm rollout.
6. Promotion workflow runs with `deploy=false` first, then `deploy=true` only after protected-environment review.
7. Private test operator rehearses provider-review live execution against a reviewed repository with both execution switches enabled.
8. Real environment Dashboard and `assops-tool project readiness` evidence proves graph-backed first-version readiness outside local seed data.

## Completion Rule

Do not mark the first deployable goal complete from local checks alone. Local gates prove packaging, render, script, and no-call safety contracts; external owners must provide the environment evidence above.
EOF

if [[ -n "$out_file" ]]; then
  mkdir -p "$(dirname "$out_file")"
  cp "$report" "$out_file"
  echo "first-deployable completion audit written to $out_file"
else
  cat "$report"
fi
