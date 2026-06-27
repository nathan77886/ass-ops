#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

repo="${ASSOPS_FIRST_DEPLOYABLE_REPO:-nathan77886/ass-ops}"
ghcr_owner="${ASSOPS_FIRST_DEPLOYABLE_GHCR_OWNER:-nathan77886}"
version="${ASSOPS_FIRST_DEPLOYABLE_VERSION:-v0.1.0}"
environment="${ASSOPS_FIRST_DEPLOYABLE_ENV:-production}"
runner="${ASSOPS_FIRST_DEPLOYABLE_RUNNER:-ubuntu-latest}"
cron="${ASSOPS_FIRST_DEPLOYABLE_BACKUP_CRON:-17 3 * * 1}"
backup_source="${ASSOPS_FIRST_DEPLOYABLE_BACKUP_SOURCE:-artifact:retained-assops-backup}"
retention_days="${ASSOPS_FIRST_DEPLOYABLE_RETENTION_DAYS:-14}"
namespace="${ASSOPS_FIRST_DEPLOYABLE_NAMESPACE:-assops}"
release="${ASSOPS_FIRST_DEPLOYABLE_RELEASE:-assops}"
env_values="${ASSOPS_FIRST_DEPLOYABLE_ENV_VALUES:-deploy/helm/assops/values.production.example.yaml}"
previous_values="${ASSOPS_FIRST_DEPLOYABLE_PREVIOUS_VALUES:-}"
restore_report="${ASSOPS_FIRST_DEPLOYABLE_RESTORE_REPORT:-}"
out_dir="${ASSOPS_FIRST_DEPLOYABLE_HANDOFF_DIR:-.assops/release-notes/first-deployable}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for first-deployable-handoff-plan" >&2
    exit 1
  }
}

need go
need make
need python3
need sha256sum

cd "$repo_root"

if [[ ! -f "$env_values" ]]; then
  echo "environment values file not found: $env_values" >&2
  exit 1
fi
if [[ -n "$previous_values" && ! -f "$previous_values" ]]; then
  echo "previous values file not found: $previous_values" >&2
  exit 1
fi
if [[ -n "$restore_report" && ! -f "$restore_report" ]]; then
  echo "restore report file not found: $restore_report" >&2
  exit 1
fi

mkdir -p "$out_dir"

branch_plan="$out_dir/branch-protection-plan.md"
backup_plan="$out_dir/production-backup-rehearsal-plan.md"
release_values="$out_dir/helm-values-${version}.yaml"
rollout_plan="$out_dir/helm-rollout-rehearsal-plan.md"
completion_audit="$out_dir/completion-audit.md"
completion_audit_schema="$out_dir/completion-audit.schema.json"
completion_audit_json="$out_dir/completion-audit.json"
evidence_checklist_schema="$out_dir/external-evidence-checklist.schema.json"
evidence_checklist="$out_dir/external-evidence-checklist.json"
evidence_status_schema="$out_dir/external-evidence-status.schema.json"
evidence_status_template="$out_dir/external-evidence-status.example.json"
manifest="$out_dir/manifest.json"
index_file="$out_dir/README.md"

go run ./backend/cmd/assops-tool release branch-protection-plan \
  "$repo" \
  .github/rulesets/main-required-checks.json \
  .github/CODEOWNERS \
  "$branch_plan" \
  >/tmp/assops-first-deployable-branch-protection-plan.log

make --no-print-directory production-backup-rehearsal-plan \
  REPO="$repo" \
  ENV="$environment" \
  RUNNER="$runner" \
  CRON="$cron" \
  BACKUP_SOURCE="$backup_source" \
  RETENTION_DAYS="$retention_days" \
  OUTPUT="$backup_plan" \
  >/tmp/assops-first-deployable-production-backup-plan.log

go run ./backend/cmd/assops-tool release helm-values \
  "$ghcr_owner" \
  "$version" \
  "$release_values" \
  >/tmp/assops-first-deployable-helm-values.log

rollout_make_args=(
  helm-rollout-rehearsal-plan
  "REPO=$repo"
  "GHCR_OWNER=$ghcr_owner"
  "VERSION=$version"
  "NAMESPACE=$namespace"
  "RELEASE=$release"
  "ENV=$environment"
  "ENV_VALUES=$env_values"
  "RELEASE_VALUES=$release_values"
  "OUTPUT=$rollout_plan"
)
if [[ -n "$previous_values" ]]; then
  rollout_make_args+=("PREVIOUS_VALUES=$previous_values")
fi
if [[ -n "$restore_report" ]]; then
  rollout_make_args+=("RESTORE_REPORT=$restore_report")
fi
make --no-print-directory "${rollout_make_args[@]}" \
  >/tmp/assops-first-deployable-helm-rollout-plan.log

ASSOPS_FIRST_DEPLOYABLE_COMPLETION_AUDIT_OUTPUT="$completion_audit" \
  bash scripts/first-deployable-completion-audit.sh \
  >/tmp/assops-first-deployable-completion-audit.log

cat >"$evidence_checklist" <<EOF
{
  "schema": "assops.first_deployable.external_evidence.v1",
  "generated_locally": true,
  "no_external_calls_made": true,
  "repository": "$repo",
  "version": "$version",
  "environment": "$environment",
  "items": [
    {
      "id": "github_ruleset_applied",
      "owner": "repository_administrator",
      "required_evidence": "GitHub ruleset id, target repository, applied default-branch rules, and verify command output from an administrator account",
      "local_plan": "branch-protection-plan.md",
      "completion_blocker": true
    },
    {
      "id": "retained_backup_artifact_published",
      "owner": "protected_environment_owner",
      "required_evidence": "successful production-retained-backup workflow run id, artifact name, retention days, and one assops-*.dump artifact confirmation",
      "local_plan": "production-backup-rehearsal-plan.md",
      "completion_blocker": true
    },
    {
      "id": "restore_rehearsal_completed",
      "owner": "protected_environment_owner",
      "required_evidence": "successful production-restore-rehearsal workflow run id, disposable database target confirmation, and private restore rehearsal report path",
      "local_plan": "production-backup-rehearsal-plan.md",
      "completion_blocker": true
    },
    {
      "id": "ghcr_images_and_attestations_verified",
      "owner": "release_operator",
      "required_evidence": "published GHCR gateway, worker, node-worker, and web image digests plus attestation verification output for $version",
      "local_plan": "helm-values-$version.yaml",
      "completion_blocker": true
    },
    {
      "id": "promotion_preflight_completed",
      "owner": "release_operator",
      "required_evidence": "promote-production workflow run with deploy=false, rendered promotion artifact, and reviewed environment values path",
      "local_plan": "helm-rollout-rehearsal-plan.md",
      "completion_blocker": true
    },
    {
      "id": "protected_helm_rollout_completed",
      "owner": "cluster_operator",
      "required_evidence": "protected deploy=true workflow approval, Helm rollout status for gateway worker node-worker web, rollback values, and smoke result",
      "local_plan": "helm-rollout-rehearsal-plan.md",
      "completion_blocker": true
    },
    {
      "id": "provider_review_live_rehearsed",
      "owner": "private_test_operator",
      "required_evidence": "reviewed target repository, enabled provider-review execution switches, approved attempt id, provider review URL, and sanitized execution result",
      "local_plan": "README.md",
      "completion_blocker": true
    },
    {
      "id": "graph_backed_readiness_verified",
      "owner": "environment_operator",
      "required_evidence": "Dashboard readiness export and assops-tool project readiness output from the real environment, not local seed data",
      "local_plan": "completion-audit.md",
      "completion_blocker": true
    }
  ]
}
EOF

cat >"$evidence_checklist_schema" <<EOF
{
  "\$schema": "https://json-schema.org/draft/2020-12/schema",
  "\$id": "assops.first_deployable.external_evidence.schema.json",
  "title": "ASSOPS first deployable external evidence checklist",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema", "generated_locally", "no_external_calls_made", "repository", "version", "environment", "items"],
  "properties": {
    "schema": {"const": "assops.first_deployable.external_evidence.v1"},
    "generated_locally": {"type": "boolean"},
    "no_external_calls_made": {"type": "boolean"},
    "repository": {"type": "string", "minLength": 1},
    "version": {"type": "string", "minLength": 1},
    "environment": {"type": "string", "minLength": 1},
    "items": {
      "type": "array",
      "minItems": 8,
      "maxItems": 8,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "owner", "required_evidence", "local_plan", "completion_blocker"],
        "properties": {
          "id": {"enum": ["github_ruleset_applied", "retained_backup_artifact_published", "restore_rehearsal_completed", "ghcr_images_and_attestations_verified", "promotion_preflight_completed", "protected_helm_rollout_completed", "provider_review_live_rehearsed", "graph_backed_readiness_verified"]},
          "owner": {"type": "string", "minLength": 1},
          "required_evidence": {"type": "string", "minLength": 1},
          "local_plan": {"type": "string", "minLength": 1},
          "completion_blocker": {"const": true}
        }
      }
    }
  }
}
EOF

evidence_checklist_sha256="$(sha256sum "$evidence_checklist" | awk '{print $1}')"

cat >"$evidence_status_schema" <<EOF
{
  "\$schema": "https://json-schema.org/draft/2020-12/schema",
  "\$id": "assops.first_deployable.external_evidence_status.schema.json",
  "title": "ASSOPS first deployable external evidence status",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema", "generated_locally", "no_external_calls_made", "repository", "version", "environment", "source_checklist", "source_checklist_sha256", "entries"],
  "properties": {
    "schema": {"const": "assops.first_deployable.external_evidence_status.v1"},
    "generated_locally": {"type": "boolean"},
    "no_external_calls_made": {"type": "boolean"},
    "repository": {"type": "string", "minLength": 1},
    "version": {"type": "string", "minLength": 1},
    "environment": {"type": "string", "minLength": 1},
    "source_checklist": {"const": "external-evidence-checklist.json"},
    "source_checklist_sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
    "entries": {
      "type": "array",
      "minItems": 8,
      "maxItems": 8,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "owner", "required_evidence", "status", "evidence_reference", "verified_by", "verified_at", "evidence_summary"],
        "properties": {
          "id": {"enum": ["github_ruleset_applied", "retained_backup_artifact_published", "restore_rehearsal_completed", "ghcr_images_and_attestations_verified", "promotion_preflight_completed", "protected_helm_rollout_completed", "provider_review_live_rehearsed", "graph_backed_readiness_verified"]},
          "owner": {"type": "string", "minLength": 1},
          "required_evidence": {"type": "string", "minLength": 1},
          "status": {"enum": ["pending", "verified", "rejected"]},
          "evidence_reference": {"type": "string"},
          "verified_by": {"type": "string"},
          "verified_at": {"type": "string"},
          "evidence_summary": {"type": "string"}
        }
      }
    }
  }
}
EOF

cat >"$evidence_status_template" <<EOF
{
  "schema": "assops.first_deployable.external_evidence_status.v1",
  "generated_locally": true,
  "no_external_calls_made": true,
  "repository": "$repo",
  "version": "$version",
  "environment": "$environment",
  "source_checklist": "external-evidence-checklist.json",
  "source_checklist_sha256": "$evidence_checklist_sha256",
  "entries": [
    {"id": "github_ruleset_applied", "owner": "repository_administrator", "required_evidence": "GitHub ruleset id, target repository, applied default-branch rules, and verify command output from an administrator account", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""},
    {"id": "retained_backup_artifact_published", "owner": "protected_environment_owner", "required_evidence": "successful production-retained-backup workflow run id, artifact name, retention days, and one assops-*.dump artifact confirmation", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""},
    {"id": "restore_rehearsal_completed", "owner": "protected_environment_owner", "required_evidence": "successful production-restore-rehearsal workflow run id, disposable database target confirmation, and private restore rehearsal report path", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""},
    {"id": "ghcr_images_and_attestations_verified", "owner": "release_operator", "required_evidence": "published GHCR gateway, worker, node-worker, and web image digests plus attestation verification output for $version", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""},
    {"id": "promotion_preflight_completed", "owner": "release_operator", "required_evidence": "promote-production workflow run with deploy=false, rendered promotion artifact, and reviewed environment values path", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""},
    {"id": "protected_helm_rollout_completed", "owner": "cluster_operator", "required_evidence": "protected deploy=true workflow approval, Helm rollout status for gateway worker node-worker web, rollback values, and smoke result", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""},
    {"id": "provider_review_live_rehearsed", "owner": "private_test_operator", "required_evidence": "reviewed target repository, enabled provider-review execution switches, approved attempt id, provider review URL, and sanitized execution result", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""},
    {"id": "graph_backed_readiness_verified", "owner": "environment_operator", "required_evidence": "Dashboard readiness export and assops-tool project readiness output from the real environment, not local seed data", "status": "pending", "evidence_reference": "", "verified_by": "", "verified_at": "", "evidence_summary": ""}
  ]
}
EOF

cat >"$completion_audit_json" <<EOF
{
  "schema": "assops.first_deployable.completion_audit.v1",
  "generated_locally": true,
  "no_external_calls_made": true,
  "repository": "$repo",
  "version": "$version",
  "environment": "$environment",
  "local_gate": "make first-deployable-check",
  "handoff_pack_validator": "make first-deployable-handoff-validate",
  "external_evidence_status_validator": "make first-deployable-external-evidence-validate",
  "completion_evidence_validator": "make first-deployable-external-evidence-complete-validate",
  "completion_allowed_from_local_checks": false,
  "external_evidence_required": true,
  "external_blockers": [
    "github_ruleset_applied",
    "retained_backup_artifact_published",
    "restore_rehearsal_completed",
    "ghcr_images_and_attestations_verified",
    "promotion_preflight_completed",
    "protected_helm_rollout_completed",
    "provider_review_live_rehearsed",
    "graph_backed_readiness_verified"
  ]
}
EOF

cat >"$completion_audit_schema" <<EOF
{
  "\$schema": "https://json-schema.org/draft/2020-12/schema",
  "\$id": "assops.first_deployable.completion_audit.schema.json",
  "title": "ASSOPS first deployable completion audit",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema", "generated_locally", "no_external_calls_made", "repository", "version", "environment", "local_gate", "handoff_pack_validator", "external_evidence_status_validator", "completion_evidence_validator", "completion_allowed_from_local_checks", "external_evidence_required", "external_blockers"],
  "properties": {
    "schema": {"const": "assops.first_deployable.completion_audit.v1"},
    "generated_locally": {"const": true},
    "no_external_calls_made": {"const": true},
    "repository": {"type": "string", "minLength": 1},
    "version": {"type": "string", "minLength": 1},
    "environment": {"type": "string", "minLength": 1},
    "local_gate": {"const": "make first-deployable-check"},
    "handoff_pack_validator": {"const": "make first-deployable-handoff-validate"},
    "external_evidence_status_validator": {"const": "make first-deployable-external-evidence-validate"},
    "completion_evidence_validator": {"const": "make first-deployable-external-evidence-complete-validate"},
    "completion_allowed_from_local_checks": {"const": false},
    "external_evidence_required": {"const": true},
    "external_blockers": {
      "type": "array",
      "minItems": 8,
      "maxItems": 8,
      "items": {"enum": ["github_ruleset_applied", "retained_backup_artifact_published", "restore_rehearsal_completed", "ghcr_images_and_attestations_verified", "promotion_preflight_completed", "protected_helm_rollout_completed", "provider_review_live_rehearsed", "graph_backed_readiness_verified"]}
    }
  }
}
EOF

cat >"$index_file" <<EOF
# ASSOPS First Deployable Handoff

Generated locally. No GitHub, registry, Kubernetes, Argo, database, Redis, MQ, or provider API mutation was performed by this handoff generator.

## Inputs

- Repository: \`$repo\`
- GHCR owner: \`$ghcr_owner\`
- Version: \`$version\`
- Environment: \`$environment\`
- Namespace: \`$namespace\`
- Helm release: \`$release\`
- Environment values: \`$env_values\`
- Release values: \`$release_values\`
- Previous values: \`${previous_values:-not provided}\`
- Restore report: \`${restore_report:-not provided}\`

## Generated Plans

- [Branch protection plan](branch-protection-plan.md)
- [Production backup rehearsal plan](production-backup-rehearsal-plan.md)
- [Release Helm values](helm-values-${version}.yaml)
- [Helm rollout rehearsal plan](helm-rollout-rehearsal-plan.md)
- [Completion audit](completion-audit.md)
- [Completion audit schema](completion-audit.schema.json)
- [Machine-readable completion audit](completion-audit.json)
- [External evidence checklist schema](external-evidence-checklist.schema.json)
- [External evidence checklist](external-evidence-checklist.json)
- [External evidence status schema](external-evidence-status.schema.json)
- [External evidence status template](external-evidence-status.example.json)
- [Handoff manifest](manifest.json)

## External Actions Still Required

1. Repository administrator applies and verifies the GitHub ruleset from the branch protection plan.
2. Protected environment owner enables and runs the retained-backup artifact workflow with reviewed secrets and storage policy.
3. Protected environment owner runs restore rehearsal against a disposable database and preserves the private report.
4. Release operator verifies image publication, registry access, and image attestations for \`$version\`.
5. Cluster operator reviews Secret, TLS, storage class, namespace-scoped kubeconfig, rollback values, and ingress policy before any Helm rollout.
6. Promotion workflow runs with \`deploy=false\` first, then \`deploy=true\` only after protected-environment review.
7. Private test operator runs provider-review live execution only after both execution gates are enabled in an environment-owned overlay and the target repository policy is reviewed.
8. Real environment Dashboard and \`assops-tool project readiness\` evidence proves graph-backed first-version readiness outside local seed data.

## Local Validation Before Handoff

1. Run \`make first-deployable-handoff-validate\` to verify required files, manifest safety flags, relative file paths, SHA256 checksums, checklist/status ids, completion-blocker flags, and local-plan references. Run \`make first-deployable-handoff-plan-manifest-validate\` when only the checksum manifest needs to be checked.
2. Copy \`external-evidence-status.example.json\` to a private evidence status file after environment owners complete external work; keep the owner and required-evidence fields aligned with the checklist.
3. Fill only evidence references and short summaries; keep screenshots, run logs, secret values, database URLs, kubeconfigs, and raw provider payloads outside the repository.
4. Run \`make first-deployable-external-evidence-validate EVIDENCE_FILE=/path/to/external-evidence-status.json\` to validate status schema, ids, required verified fields, and secret-shaped text. This does not prove external system truth.
5. Run \`make first-deployable-external-evidence-complete-validate EVIDENCE_FILE=/path/to/external-evidence-status.json\` before declaring completion; it fails until every external evidence entry is \`verified\` with a reference, verifier, and timestamp.

## Safety Boundary

- This generator writes local Markdown/YAML files only.
- It does not read token values, kubeconfig contents, database URLs, SSH keys, provider responses, Git output, or raw application Secrets.
- It does not call GitHub, contact registries, run Helm against a cluster, invoke Argo, connect to PostgreSQL, run Git provider mutations, or push images.
EOF

manifest_files=(
  README.md
  branch-protection-plan.md
  production-backup-rehearsal-plan.md
  "helm-values-${version}.yaml"
  helm-rollout-rehearsal-plan.md
  completion-audit.md
  completion-audit.schema.json
  completion-audit.json
  external-evidence-checklist.schema.json
  external-evidence-checklist.json
  external-evidence-status.schema.json
  external-evidence-status.example.json
)

{
  printf '{\n'
  printf '  "schema": "assops.first_deployable.handoff_manifest.v1",\n'
  printf '  "generated_locally": true,\n'
  printf '  "no_external_calls_made": true,\n'
  printf '  "repository": "%s",\n' "$repo"
  printf '  "version": "%s",\n' "$version"
  printf '  "environment": "%s",\n' "$environment"
  printf '  "files": [\n'
  for i in "${!manifest_files[@]}"; do
    file="${manifest_files[$i]}"
    checksum="$(sha256sum "$out_dir/$file" | awk '{print $1}')"
    comma=","
    if [[ "$i" -eq "$((${#manifest_files[@]} - 1))" ]]; then
      comma=""
    fi
    printf '    {"path": "%s", "sha256": "%s"}%s\n' "$file" "$checksum" "$comma"
  done
  printf '  ]\n'
  printf '}\n'
} >"$manifest"

bash scripts/first-deployable-handoff-validate.sh "$out_dir" \
  >/tmp/assops-first-deployable-handoff-validate.log

echo "first-deployable handoff plan written to $out_dir"
