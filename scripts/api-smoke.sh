#!/usr/bin/env bash
set -euo pipefail

base_url="${ASSOPS_GATEWAY_URL:-http://localhost:8080}"
email="${ASSOPS_ADMIN_EMAIL:-admin@assops.local}"
password="${ASSOPS_ADMIN_PASSWORD:-admin1234}"
require_project="${ASSOPS_API_SMOKE_REQUIRE_PROJECT:-false}"
project_slug="${ASSOPS_API_SMOKE_PROJECT_SLUG:-}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for api-smoke" >&2
    exit 1
  }
}

json_field() {
  python3 -c '
import json
import sys

key = sys.argv[1]
try:
    data = json.load(sys.stdin)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid JSON response: {exc}")
value = data
for part in key.split("."):
    if not isinstance(value, dict) or part not in value:
        raise SystemExit(f"missing JSON field: {key}")
    value = value[part]
if isinstance(value, bool):
    print("true" if value else "false")
else:
    print(value)
' "$1"
}

first_item_field() {
  python3 -c '
import json
import sys

key = sys.argv[1]
try:
    data = json.load(sys.stdin)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid JSON response: {exc}")
items = data.get("items")
if not isinstance(items, list) or not items:
    print("")
    raise SystemExit(0)
first = items[0]
if not isinstance(first, dict):
    print("")
    raise SystemExit(0)
value = first.get(key, "")
print("" if value is None else value)
' "$1"
}

item_count() {
  python3 -c '
import json
import sys

try:
    data = json.load(sys.stdin)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid JSON response: {exc}")
items = data.get("items")
if not isinstance(items, list):
    raise SystemExit("missing JSON array field: items")
print(len(items))
'
}

first_item_fields() {
  python3 -c '
import json
import sys

fields = sys.argv[1:]
try:
    data = json.load(sys.stdin)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid JSON response: {exc}")
items = data.get("items")
if not isinstance(items, list) or not items:
    raise SystemExit("expected at least one item")
first = items[0]
if not isinstance(first, dict):
    raise SystemExit("first item is not an object")
missing = [field for field in fields if field not in first]
if missing:
    raise SystemExit(f"missing first item fields: {missing}")
' "$@"
}

first_item_field_matching() {
  python3 -c '
import json
import sys

match_field, match_value, output_field = sys.argv[1:4]
try:
    data = json.load(sys.stdin)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid JSON response: {exc}")
items = data.get("items")
if not isinstance(items, list):
    print("")
    raise SystemExit(0)
for item in items:
    if not isinstance(item, dict):
        continue
    if str(item.get(match_field, "")).lower() == match_value.lower():
        value = item.get(output_field, "")
        print("" if value is None else value)
        raise SystemExit(0)
print("")
' "$@"
}

project_id_for_slug() {
  python3 -c '
import json
import sys

slug = sys.argv[1]
try:
    data = json.load(sys.stdin)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid JSON response: {exc}")
items = data.get("items")
if not isinstance(items, list):
    print("")
    raise SystemExit(0)
for item in items:
    if isinstance(item, dict) and str(item.get("slug", "")) == slug:
        print(item.get("id", "") or "")
        raise SystemExit(0)
print("")
' "$1"
}

auth_get() {
  local path="$1"
  curl -fsS "$base_url$path" -H "authorization: Bearer $token"
}

auth_post_json() {
  local path="$1"
  local payload="$2"
  curl -fsS "$base_url$path" \
    -H "authorization: Bearer $token" \
    -H 'content-type: application/json' \
    -d "$payload"
}

assert_items_response() {
  local path="$1"
  local response
  response="$(auth_get "$path")"
  printf '%s' "$response" | json_field items >/dev/null
  printf '%s' "$response"
}

require_non_empty_items() {
  local path="$1"
  local label="$2"
  local response
  response="$(assert_items_response "$path")"
  if [[ "$(printf '%s' "$response" | item_count)" -eq 0 ]]; then
    echo "api-smoke expected ${label} from ${path}" >&2
    exit 1
  fi
  printf '%s' "$response"
}

need curl
need python3

health="$(curl -fsS "$base_url/healthz")"
component="$(printf '%s' "$health" | json_field component)"
if [[ "$component" != "gateway" ]]; then
  echo "unexpected health component: $component" >&2
  exit 1
fi

login_payload="$(
  EMAIL="$email" PASSWORD="$password" python3 - <<'PY'
import json
import os

print(json.dumps({"email": os.environ["EMAIL"], "password": os.environ["PASSWORD"]}))
PY
)"
login="$(curl -fsS "$base_url/api/auth/login" \
  -H 'content-type: application/json' \
  -d "$login_payload")"
token="$(printf '%s' "$login" | json_field token)"
if [[ -z "$token" || "$token" == "null" ]]; then
  echo "login did not return a token" >&2
  exit 1
fi

projects="$(curl -fsS "$base_url/api/projects" \
  -H "authorization: Bearer $token")"
printf '%s' "$projects" | json_field items >/dev/null
project_id="$(printf '%s' "$projects" | first_item_field id)"
if [[ -n "$project_slug" ]]; then
  slug_project_id="$(printf '%s' "$projects" | project_id_for_slug "$project_slug")"
  if [[ -n "$slug_project_id" && "$slug_project_id" != "null" ]]; then
    project_id="$slug_project_id"
  fi
fi
if [[ "$require_project" == "true" && ( -z "$project_id" || "$project_id" == "null" ) ]]; then
  echo "api-smoke expected at least one project but /api/projects returned none" >&2
  exit 1
fi

queue_summary="$(curl -fsS "$base_url/api/worker-queue/summary" \
  -H "authorization: Bearer $token")"
printf '%s' "$queue_summary" | json_field queued_jobs >/dev/null
printf '%s' "$queue_summary" | json_field running_jobs >/dev/null

assert_items_response "/api/assets" >/dev/null
asset_graph="$(auth_get "/api/assets/graph")"
printf '%s' "$asset_graph" | json_field nodes >/dev/null
printf '%s' "$asset_graph" | json_field edges >/dev/null
assert_items_response "/api/operations" >/dev/null
assert_items_response "/api/repo-sync-runs" >/dev/null
repo_tag_runs="$(assert_items_response "/api/repo-tag-runs")"
assert_items_response "/api/operation-approvals" >/dev/null
assert_items_response "/api/ai-runtimes" >/dev/null

if [[ -n "$project_id" && "$project_id" != "null" ]]; then
  git_repositories="$(assert_items_response "/api/projects/${project_id}/git-repositories")"
  repository_id="$(printf '%s' "$git_repositories" | first_item_field id)"
  assert_items_response "/api/projects/${project_id}/argo/connections" >/dev/null
  assert_items_response "/api/projects/${project_id}/argo/apps" >/dev/null
  kubernetes_environments="$(assert_items_response "/api/projects/${project_id}/kubernetes/environments")"
  deployment_targets="$(assert_items_response "/api/projects/${project_id}/deployment-targets")"
  assert_items_response "/api/projects/${project_id}/deployment-records" >/dev/null
  assert_items_response "/api/projects/${project_id}/rollback-points" >/dev/null
  assert_items_response "/api/projects/${project_id}/ssh-machines" >/dev/null
  assert_items_response "/api/projects/${project_id}/webhook-connections" >/dev/null
  assert_items_response "/api/projects/${project_id}/agent/tasks" >/dev/null

  if [[ "$require_project" == "true" ]]; then
    kubernetes_environments="$(require_non_empty_items "/api/projects/${project_id}/kubernetes/environments" "Kubernetes environments")"
    printf '%s' "$kubernetes_environments" | first_item_fields \
      id name environment cluster_name namespace kubeconfig_secret_ref_present \
      token_subject_review_ready rbac_read_logs_ready rbac_restart_pods_ready \
      log_access_metadata_ready pod_restart_metadata_ready >/dev/null
    deployment_targets="$(require_non_empty_items "/api/projects/${project_id}/deployment-targets" "deployment targets")"
    printf '%s' "$deployment_targets" | first_item_fields \
      id name environment cluster_name namespace kubernetes_environment_id \
      kubeconfig_secret_ref_present token_subject_review_status \
      rbac_read_logs_status rbac_restart_pods_status kubernetes_environment_status >/dev/null
    deployment_target_id="$(printf '%s' "$deployment_targets" | first_item_field id)"
    pod_list="$(auth_post_json "/api/deployment-targets/${deployment_target_id}/pods" '{}')"
    printf '%s' "$pod_list" | json_field mode >/dev/null
    printf '%s' "$pod_list" | json_field backend_state >/dev/null
    printf '%s' "$pod_list" | json_field result_scope >/dev/null
    printf '%s' "$pod_list" | json_field items >/dev/null
    printf '%s' "$pod_list" | json_field kubernetes_api_call >/dev/null
    printf '%s' "$pod_list" | json_field raw_response_included >/dev/null
    printf '%s' "$pod_list" | json_field secret_included >/dev/null
    printf '%s' "$pod_list" | json_field log_body_included >/dev/null
    pod_log_preview_payload="$(
      DEPLOYMENT_TARGET_ID="$deployment_target_id" python3 - <<'PY'
import json
import os

print(json.dumps({
    "deployment_target_id": os.environ["DEPLOYMENT_TARGET_ID"],
    "pod_name": "demo-service-abc123",
    "container_name": "demo-service",
    "tail_lines": 100,
}))
PY
)"
    pod_log_preview="$(auth_post_json "/api/projects/${project_id}/argo/pod-log-query-preview" "$pod_log_preview_payload")"
    printf '%s' "$pod_log_preview" | json_field mode >/dev/null
    printf '%s' "$pod_log_preview" | json_field query_state >/dev/null
    printf '%s' "$pod_log_preview" | json_field retrieval_plan >/dev/null
    printf '%s' "$pod_log_preview" | json_field deployment_target >/dev/null
    printf '%s' "$pod_log_preview" | json_field external_call_made >/dev/null
    printf '%s' "$pod_log_preview" | json_field kubernetes_api_call >/dev/null
    printf '%s' "$pod_log_preview" | json_field log_body_included >/dev/null
    printf '%s' "$pod_log_preview" | json_field contains_secret >/dev/null
    printf '%s' "$pod_log_preview" | json_field contains_token >/dev/null
  fi

  if [[ -n "$repository_id" && "$repository_id" != "null" ]]; then
    assert_items_response "/api/git-repositories/${repository_id}/repo-sync-assets" >/dev/null
    git_remotes="$(assert_items_response "/api/git-repositories/${repository_id}/remotes")"
    remote_id="$(printf '%s' "$git_remotes" | first_item_field_matching provider_type github id)"
    if [[ -z "$remote_id" || "$remote_id" == "null" ]]; then
      remote_id="$(printf '%s' "$git_remotes" | first_item_field_matching kind github id)"
    fi
    if [[ -z "$remote_id" || "$remote_id" == "null" ]]; then
      remote_id="$(printf '%s' "$git_remotes" | first_item_field id)"
    fi
    if [[ "$require_project" == "true" && ( -z "$remote_id" || "$remote_id" == "null" ) ]]; then
      echo "api-smoke expected at least one git remote for repository ${repository_id}" >&2
      exit 1
    fi
    if [[ "$require_project" == "true" ]]; then
      repo_tag_runs="$(require_non_empty_items "/api/repo-tag-runs?repo_id=${repository_id}" "repo tag runs")"
      printf '%s' "$repo_tag_runs" | first_item_fields id tag_name status target_sha >/dev/null
    fi
    if [[ -n "$remote_id" && "$remote_id" != "null" ]]; then
      remote="$(auth_get "/api/git-remotes/${remote_id}")"
      printf '%s' "$remote" | json_field id >/dev/null
      github_actions="$(assert_items_response "/api/git-remotes/${remote_id}/github-actions")"
      github_labels="$(assert_items_response "/api/git-remotes/${remote_id}/github-labels")"
      if [[ "$require_project" == "true" ]]; then
        github_actions="$(require_non_empty_items "/api/git-remotes/${remote_id}/github-actions" "GitHub Actions")"
        printf '%s' "$github_actions" | first_item_fields id workflow_name status conclusion artifact_count active_artifact_count total_artifact_size_in_bytes artifacts >/dev/null
        github_labels="$(require_non_empty_items "/api/git-remotes/${remote_id}/github-labels" "GitHub labels")"
        printf '%s' "$github_labels" | first_item_fields id name color is_default result_scope provider_response_included credential_included >/dev/null
      fi
    fi
  fi
fi

echo "api-smoke passed for $base_url"
