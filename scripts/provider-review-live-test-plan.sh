#!/usr/bin/env bash
set -euo pipefail

namespace="${ASSOPS_PROVIDER_REVIEW_LIVE_TEST_NAMESPACE:-assops-test}"
release="${ASSOPS_PROVIDER_REVIEW_LIVE_TEST_RELEASE:-assops}"
values_file="${ASSOPS_PROVIDER_REVIEW_LIVE_TEST_VALUES:-deploy/helm/assops/values.test.example.yaml}"
extra_values="${ASSOPS_PROVIDER_REVIEW_LIVE_TEST_EXTRA_VALUES:-}"
output="${ASSOPS_PROVIDER_REVIEW_LIVE_TEST_OUTPUT:-}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for provider-review-live-test-plan" >&2
    exit 1
  }
}

need helm
need python3

if [[ ! -f "$values_file" ]]; then
  echo "values file not found: $values_file" >&2
  exit 1
fi

args=(template "$release" deploy/helm/assops -n "$namespace" -f "$values_file")
if [[ -n "$extra_values" ]]; then
  IFS=':' read -r -a extra_files <<< "$extra_values"
  for file in "${extra_files[@]}"; do
    if [[ -z "$file" ]]; then
      continue
    fi
    if [[ ! -f "$file" ]]; then
      echo "extra values file not found: $file" >&2
      exit 1
    fi
    args+=(-f "$file")
  done
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

rendered_file="$tmpdir/rendered.yaml"
helm "${args[@]}" > "$rendered_file"

plan_file="$tmpdir/provider-review-live-test-plan.md"
python3 - "$rendered_file" "$plan_file" "$namespace" "$release" <<'PY'
import re
import sys

rendered_path, plan_path, namespace, release = sys.argv[1:5]
text = open(rendered_path, encoding="utf-8").read()

def config_value(name):
    pattern = re.compile(r"^\s*" + re.escape(name) + r":\s*['\"]?([^'\"\s#]+)['\"]?\s*$", re.MULTILINE)
    match = pattern.search(text)
    return match.group(1).strip() if match else ""

execution_enabled = config_value("ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION")
mutation_armed = config_value("ASSOPS_ARM_PROVIDER_REVIEW_MUTATION")
has_secret_ref = "secretRef:" in text
has_github_token_key = re.search(r"^\s*ASSOPS_GITHUB_TEMPLATE_TOKEN:\s*", text, re.MULTILINE) is not None
has_gitea_token_key = re.search(r"^\s*ASSOPS_GITEA_TEMPLATE_TOKEN:\s*", text, re.MULTILINE) is not None
contains_secret_value = any(marker in text for marker in ["ghp_", "github_pat_", "BEGIN " + "PRIVATE KEY", "xoxb-"])

failures = []
if execution_enabled != "true":
    failures.append("ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION must render as true")
if mutation_armed != "true":
    failures.append("ASSOPS_ARM_PROVIDER_REVIEW_MUTATION must render as true")
if not has_secret_ref:
    failures.append("gateway/worker pods must reference the ASSOPS application Secret")
if contains_secret_value:
    failures.append("rendered manifest contains a value shaped like a secret")

lines = [
    "# Provider Review Live Private Test Plan",
    "",
    f"Namespace: `{namespace}`",
    f"Release: `{release}`",
    "",
    "## Rendered Gate Evidence",
    "",
    f"- `ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION`: `{execution_enabled or 'missing'}`",
    f"- `ASSOPS_ARM_PROVIDER_REVIEW_MUTATION`: `{mutation_armed or 'missing'}`",
    f"- Application Secret reference rendered: `{str(has_secret_ref).lower()}`",
    f"- Chart-managed GitHub token key rendered: `{str(has_github_token_key).lower()}`",
    f"- Chart-managed Gitea token key rendered: `{str(has_gitea_token_key).lower()}`",
    f"- Secret-shaped literal detected in render: `{str(contains_secret_value).lower()}`",
    "",
    "## Manual Private Test Sequence",
    "",
    "1. Use a reviewed private GitHub repository and confirm default/protected branch rules.",
    "2. Store `ASSOPS_GITHUB_TEMPLATE_TOKEN` only in the environment-owned application Secret; do not commit token values.",
    "3. Install or upgrade Helm only after image preflight and normal Helm preflight pass for the same overlay.",
    "4. Create a project from a template with protected/default-branch starter-file review enabled.",
    "5. Approve the provider-review execution request, claim the current attempt, record live-readiness evidence, and record mutation-arming evidence.",
    "6. Use Approval audit `Execute live review` once, then verify the sanitized ledger contains phase, retryability, cleanup flags, and review URL evidence without raw refs or file content.",
    "7. If the attempt records `review_branch_delete_required`, use Approval audit `Cleanup live review` and verify only sanitized cleanup status is recorded.",
    "",
]
if failures:
    lines.extend(["## Blocking Findings", ""])
    lines.extend(f"- {item}" for item in failures)
else:
    lines.extend(["## Blocking Findings", "", "- none"])

open(plan_path, "w", encoding="utf-8").write("\n".join(lines) + "\n")
for line in lines:
    print(line)
if failures:
    sys.exit(1)
PY

if [[ -n "$output" ]]; then
  mkdir -p "$(dirname "$output")"
  cp "$plan_file" "$output"
fi

echo "provider-review-live-test-plan passed for ${release} in namespace ${namespace}"
