#!/usr/bin/env bash
set -euo pipefail

repo="${ASSOPS_FIRST_DEPLOYABLE_REPO:-nathan77886/ass-ops}"
version="${ASSOPS_FIRST_DEPLOYABLE_VERSION:-v0.1.0}"
ghcr_owner="${ASSOPS_FIRST_DEPLOYABLE_GHCR_OWNER:-nathan77886}"
deployment_mode="${ASSOPS_FIRST_DEPLOYABLE_DEPLOYMENT_MODE:-docker-local}"
status_file="${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_EVIDENCE_FILE:-.assops/release-notes/external-evidence-status.local.json}"
output="${ASSOPS_FIRST_DEPLOYABLE_EXTERNAL_AUDIT_OUTPUT:-.assops/release-notes/first-deployable-external-audit.json}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for first-deployable-external-audit" >&2
    exit 1
  }
}

need gh
need python3

if [[ ! -s "$status_file" ]]; then
  echo "external evidence status file not found: $status_file" >&2
  exit 1
fi

gh auth status --hostname github.com >/dev/null

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

gh repo view "$repo" --json nameWithOwner,defaultBranchRef,visibility,url >"$tmpdir/repo.json"
gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/tags?per_page=30" >"$tmpdir/tags.json"
gh release list --repo "$repo" --limit 30 \
  --json tagName,name,isDraft,isPrerelease,publishedAt,createdAt \
  >"$tmpdir/releases.json"
gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/environments?per_page=100" \
  >"$tmpdir/environments.json"
if gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/environments/production" \
  >"$tmpdir/production-environment.json" 2>"$tmpdir/production-environment.err"; then
  printf '0' >"$tmpdir/production-environment.status"
else
  printf '%s' "$?" >"$tmpdir/production-environment.status"
  : >"$tmpdir/production-environment.json"
fi
gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/actions/secrets?per_page=100" \
  >"$tmpdir/repo-secrets.json"
if gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/environments/production/secrets?per_page=100" \
  >"$tmpdir/production-environment-secrets.json" 2>"$tmpdir/production-environment-secrets.err"; then
  printf '0' >"$tmpdir/production-environment-secrets.status"
else
  printf '%s' "$?" >"$tmpdir/production-environment-secrets.status"
  : >"$tmpdir/production-environment-secrets.json"
fi
gh api -H 'Accept: application/vnd.github+json' \
  "/repos/${repo}/actions/artifacts?per_page=100" \
  >"$tmpdir/artifacts.json"

for workflow in \
  production-retained-backup.yml \
  production-restore-rehearsal.yml \
  promote-production.yml \
  release.yml; do
  safe_name="${workflow//[^A-Za-z0-9]/_}"
  if gh run list --repo "$repo" --workflow "$workflow" --limit 20 \
    --json databaseId,workflowName,displayTitle,status,conclusion,event,createdAt,updatedAt,headBranch,headSha \
    >"$tmpdir/run-${safe_name}.json" 2>"$tmpdir/run-${safe_name}.err"; then
    printf '0' >"$tmpdir/run-${safe_name}.status"
  else
    printf '%s' "$?" >"$tmpdir/run-${safe_name}.status"
    : >"$tmpdir/run-${safe_name}.json"
  fi
done

package_status=0
if gh api -H 'Accept: application/vnd.github+json' \
  "/users/${ghcr_owner}/packages?package_type=container" \
  >"$tmpdir/packages.json" 2>"$tmpdir/packages.err"; then
  package_status=0
else
  package_status="$?"
  : >"$tmpdir/packages.json"
fi
printf '%s' "$package_status" >"$tmpdir/packages.status"

mkdir -p "$(dirname "$output")"
python3 - \
  "$repo" \
  "$version" \
  "$ghcr_owner" \
  "$deployment_mode" \
  "$status_file" \
  "$output" \
  "$tmpdir" <<'PY'
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

repo = sys.argv[1]
version = sys.argv[2]
ghcr_owner = sys.argv[3]
deployment_mode = sys.argv[4]
status_file = Path(sys.argv[5])
output = Path(sys.argv[6])
tmpdir = Path(sys.argv[7])

def load_json(path, default):
    if not path.is_file() or not path.read_text(encoding="utf-8").strip():
        return default
    return json.loads(path.read_text(encoding="utf-8"))

def safe_err(path):
    if not path.is_file():
        return ""
    text = path.read_text(encoding="utf-8", errors="replace")
    redacted = []
    for line in text.splitlines():
        if any(marker in line.lower() for marker in ("token", "authorization", "password", "secret")):
            continue
        redacted.append(line[:500])
    return "\n".join(redacted)[:1000]

def workflow_runs(workflow):
    safe = "".join(ch if ch.isalnum() else "_" for ch in workflow)
    status = int((tmpdir / f"run-{safe}.status").read_text(encoding="utf-8") or "0")
    runs = load_json(tmpdir / f"run-{safe}.json", [])
    return {
        "query_status": status,
        "query_error": safe_err(tmpdir / f"run-{safe}.err") if status else "",
        "total_returned": len(runs),
        "successful_run_ids": [run.get("databaseId") for run in runs if run.get("conclusion") == "success"],
        "latest_runs": runs[:5],
    }

repo_info = load_json(tmpdir / "repo.json", {})
tags = load_json(tmpdir / "tags.json", [])
releases = load_json(tmpdir / "releases.json", [])
environments_payload = load_json(tmpdir / "environments.json", {})
production_environment_status = int((tmpdir / "production-environment.status").read_text(encoding="utf-8") or "0")
production_environment_payload = load_json(tmpdir / "production-environment.json", {})
repo_secrets_payload = load_json(tmpdir / "repo-secrets.json", {})
production_environment_secrets_status = int((tmpdir / "production-environment-secrets.status").read_text(encoding="utf-8") or "0")
production_environment_secrets_payload = load_json(tmpdir / "production-environment-secrets.json", {})
artifacts_payload = load_json(tmpdir / "artifacts.json", {})
status = json.loads(status_file.read_text(encoding="utf-8"))

package_query_status = int((tmpdir / "packages.status").read_text(encoding="utf-8") or "0")
packages = load_json(tmpdir / "packages.json", [])
package_names = [pkg.get("name") for pkg in packages]
expected_packages = [f"assops-{name}" for name in ("gateway", "worker", "node-worker", "web")]
expected_secret_names = []
if deployment_mode == "helm-kubernetes":
    expected_secret_names = [
        "ASSOPS_ACTIVE_DATABASE_URL",
        "ASSOPS_ACTIVE_DATABASE_PASSWORD",
        "ASSOPS_REHEARSAL_DATABASE_URL",
        "ASSOPS_REHEARSAL_DATABASE_PASSWORD",
        "KUBE_CONFIG_B64",
        "ASSOPS_ADMIN_EMAIL",
        "ASSOPS_ADMIN_PASSWORD",
    ]

tag_names = [tag.get("name") for tag in tags]
release_tags = [release.get("tagName") for release in releases]
environments = environments_payload.get("environments", []) if isinstance(environments_payload, dict) else []
environment_names = [env.get("name") for env in environments if isinstance(env, dict)]
production_environment = production_environment_payload if production_environment_status == 0 else next(
    (env for env in environments if isinstance(env, dict) and env.get("name") == "production"),
    {},
)
production_protection_rules = production_environment.get("protection_rules", []) if isinstance(production_environment, dict) else []
repo_secret_names = [secret.get("name") for secret in repo_secrets_payload.get("secrets", [])]
production_environment_secret_names = [
    secret.get("name")
    for secret in production_environment_secrets_payload.get("secrets", [])
]
available_secret_names = sorted(set(repo_secret_names + production_environment_secret_names))
missing_secret_names = [name for name in expected_secret_names if name not in available_secret_names]
artifacts = artifacts_payload.get("artifacts", []) if isinstance(artifacts_payload, dict) else []
artifact_summaries = [
    {
        "id": artifact.get("id"),
        "name": artifact.get("name"),
        "expired": artifact.get("expired"),
        "created_at": artifact.get("created_at"),
        "expires_at": artifact.get("expires_at"),
        "workflow_run": artifact.get("workflow_run", {}),
    }
    for artifact in artifacts[:100]
]
retained_backup_artifact_names = [
    artifact.get("name")
    for artifact in artifacts
    if artifact.get("name") == "retained-assops-backup" and artifact.get("expired") is False
]
workflow = {
    "production_retained_backup": workflow_runs("production-retained-backup.yml"),
    "production_restore_rehearsal": workflow_runs("production-restore-rehearsal.yml"),
    "promote_production": workflow_runs("promote-production.yml"),
    "release_candidate": workflow_runs("release.yml"),
}

external_status = {
    entry["id"]: {
        "status": entry.get("status"),
        "evidence_reference": entry.get("evidence_reference", ""),
        "verified_at": entry.get("verified_at", ""),
    }
    for entry in status.get("entries", [])
}

blockers = []

def add_blocker(item_id, reason, next_actions):
    blockers.append({
        "id": item_id,
        "reason": reason,
        "next_actions": next_actions,
    })

configure_secret_actions = [
    {
        "description": "Create the GitHub production environment secrets from an approved secret source; this audit records names only and never reads values.",
        "command_template": f"gh secret set <NAME> --repo {repo} --env production",
        "required_names": expected_secret_names,
        "evidence": "gh api /repos/{repo}/environments/production/secrets returns every expected secret name",
    }
]
if deployment_mode == "helm-kubernetes" and "production" not in environment_names:
    add_blocker("protected_helm_rollout_completed", "GitHub production environment is absent", [
        {
            "description": "Create the protected GitHub production environment before deploy=true promotion runs.",
            "command_template": f"gh api -X PUT /repos/{repo}/environments/production --input production-environment.json",
            "evidence": "GitHub environment list contains production",
        }
    ])
if missing_secret_names:
    add_blocker("protected_helm_rollout_completed", f"missing required GitHub secret names: {', '.join(missing_secret_names)}", configure_secret_actions)
if deployment_mode == "helm-kubernetes" and not workflow["production_retained_backup"]["successful_run_ids"]:
    add_blocker("retained_backup_artifact_published", "no successful production-retained-backup workflow run returned", [
        {
            "description": "After active database secrets are present, manually publish the retained backup artifact from the protected environment.",
            "command_template": f"gh workflow run production-retained-backup.yml --repo {repo} -f github_environment=production -f artifact_name=retained-assops-backup -f retention_days=14 -f keep_count=3",
            "evidence": "a successful production-retained-backup.yml workflow run id",
        }
    ])
if deployment_mode == "helm-kubernetes" and not retained_backup_artifact_names:
    add_blocker("retained_backup_artifact_published", "no unexpired retained-assops-backup artifact returned", [
        {
            "description": "Keep one private unexpired retained backup artifact for restore rehearsal input.",
            "command_template": f"gh api /repos/{repo}/actions/artifacts --jq '.artifacts[] | select(.name == \"retained-assops-backup\" and .expired == false) | .id'",
            "evidence": "one unexpired retained-assops-backup artifact id",
        }
    ])
if deployment_mode == "helm-kubernetes" and not workflow["production_restore_rehearsal"]["successful_run_ids"]:
    add_blocker("restore_rehearsal_completed", "no successful production-restore-rehearsal workflow run returned", [
        {
            "description": "After retained backup and rehearsal database secrets are present, restore into the disposable rehearsal database.",
            "command_template": f"gh workflow run production-restore-rehearsal.yml --repo {repo} -f github_environment=production -f backup_artifact_name=retained-assops-backup -f report_name=production-restore-rehearsal -f artifact_retention_days=30",
            "evidence": "a successful production-restore-rehearsal.yml workflow run id and uploaded rehearsal report artifact",
        }
    ])
if deployment_mode == "helm-kubernetes" and version not in tag_names and version not in release_tags:
    add_blocker("ghcr_images_and_attestations_verified", f"no tag or release named {version} returned", [
        {
            "description": "Create the reviewed release tag/release only after the current deployable changes are intentionally committed and pushed.",
            "command_template": f"git tag {version} && git push origin {version}",
            "evidence": f"GitHub tag or release named {version}",
        }
    ])
if deployment_mode == "helm-kubernetes" and package_query_status != 0:
    add_blocker("ghcr_images_and_attestations_verified", "GHCR package API query failed; current gh token likely lacks read:packages", [
        {
            "description": "Use a GitHub token with read:packages or make the release packages visible to the verifier.",
            "command_template": f"gh auth refresh -h github.com -s read:packages && gh api /users/{ghcr_owner}/packages?package_type=container",
            "evidence": "GHCR package API returns assops-gateway, assops-worker, assops-node-worker, and assops-web",
        }
    ])
elif deployment_mode == "helm-kubernetes" and (missing := [name for name in expected_packages if name not in package_names]):
    add_blocker("ghcr_images_and_attestations_verified", f"missing GHCR packages: {', '.join(missing)}", [
        {
            "description": "Publish all release images and attestations from the tag-gated release workflow.",
            "command_template": f"gh run list --repo {repo} --workflow release.yml --limit 20",
            "evidence": "successful release.yml run plus GHCR packages for all four ASSOPS images",
        }
    ])
if deployment_mode == "helm-kubernetes" and not workflow["promote_production"]["successful_run_ids"]:
    add_blocker("promotion_preflight_completed", "no successful promote-production workflow run returned", [
        {
            "description": "Run protected promotion preflight with deploy=false after release images and attestations are visible.",
            "command_template": f"gh workflow run promote-production.yml --repo {repo} -f version={version} -f ghcr_owner={ghcr_owner} -f deploy=false",
            "evidence": "successful promote-production.yml preflight run and promotion-preflight artifact",
        }
    ])
    add_blocker("protected_helm_rollout_completed", "no successful promote-production deploy evidence returned", [
        {
            "description": "After protected review, secrets, rollback point, and preflight are complete, run deploy=true rollout.",
            "command_template": f"gh workflow run promote-production.yml --repo {repo} -f version={version} -f ghcr_owner={ghcr_owner} -f deploy=true -f smoke_url=https://ass-ops.4nathan.com",
            "evidence": "successful Production Helm Rollout job plus post-deploy smoke evidence",
        }
    ])
add_blocker("provider_review_live_rehearsed", "no provider-review live rehearsal evidence source is available to this audit", [
    {
        "description": "Run a private GitHub provider-review live rehearsal through the approval audit path and record sanitized execute/cleanup status.",
        "command_template": "ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION=true ASSOPS_ARM_PROVIDER_REVIEW_MUTATION=true <run private test rehearsal>",
        "evidence": "sanitized provider-review execute-live evidence with no token, request body, response body, headers, refs, or file content",
    }
])
add_blocker("graph_backed_readiness_verified", "no real-environment dashboard/readiness export source is available to this audit", [
    {
        "description": "Export the real-environment readiness dashboard or graph-backed asset evidence after production smoke succeeds.",
        "command_template": "<export ASSOPS readiness dashboard evidence from production environment>",
        "evidence": "dashboard/readiness export tied to the production release version and smoke timestamp",
    }
])

audit = {
    "schema": "assops.first_deployable.external_audit.v1",
    "verified_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "repository": repo,
    "version": version,
    "ghcr_owner": ghcr_owner,
    "deployment_mode": deployment_mode,
    "repo": repo_info,
    "external_evidence_status": external_status,
    "tags": {"contains_version": version in tag_names, "returned_names": tag_names[:30]},
    "releases": {"contains_version": version in release_tags, "returned_tags": release_tags[:30]},
    "workflows": workflow,
    "github_environments": {
        "returned_names": environment_names,
        "production_exists": "production" in environment_names,
        "production_query_status": production_environment_status,
        "production_query_error": safe_err(tmpdir / "production-environment.err") if production_environment_status else "",
        "production_can_admins_bypass": production_environment.get("can_admins_bypass") if production_environment else None,
        "production_protection_rules": production_protection_rules,
        "production_has_protection_rules": bool(production_protection_rules),
    },
    "github_secrets": {
        "expected_names": expected_secret_names,
        "repo_secret_names": repo_secret_names,
        "production_environment_secret_query_status": production_environment_secrets_status,
        "production_environment_secret_query_error": safe_err(tmpdir / "production-environment-secrets.err") if production_environment_secrets_status else "",
        "production_environment_secret_names": production_environment_secret_names,
        "missing_expected_names": missing_secret_names,
        "contains_secret_values": False,
    },
    "github_artifacts": {
        "total_count": artifacts_payload.get("total_count", len(artifacts)) if isinstance(artifacts_payload, dict) else len(artifacts),
        "returned_count": len(artifacts),
        "returned_names": [artifact.get("name") for artifact in artifacts[:100]],
        "unexpired_retained_backup_artifact_found": bool(retained_backup_artifact_names),
        "latest": artifact_summaries[:20],
    },
    "ghcr_packages": {
        "query_status": package_query_status,
        "query_error": safe_err(tmpdir / "packages.err") if package_query_status else "",
        "expected_packages": expected_packages,
        "returned_names": package_names,
    },
    "completion": {
        "verified_external_items": [key for key, value in external_status.items() if value.get("status") == "verified"],
        "blockers": blockers,
        "complete": False,
    },
    "secret_policy": {
        "contains_token_values": False,
        "contains_database_password": False,
        "contains_kubeconfig": False,
        "contains_private_key": False,
    },
}
output.write_text(json.dumps(audit, indent=2, sort_keys=True) + "\n", encoding="utf-8")
print(f"first-deployable external audit written to {output}")
print(f"verified external items: {len(audit['completion']['verified_external_items'])}")
print(f"remaining blockers: {len(blockers)}")
PY
