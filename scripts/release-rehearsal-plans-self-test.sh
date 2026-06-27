#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

tool=(go run ./backend/cmd/assops-tool)

run_plan() {
  local name="$1"
  shift
  "${tool[@]}" release "$@" "$tmpdir/$name.md" >/tmp/assops-release-rehearsal-$name.log
  test -s "$tmpdir/$name.md"
  grep -Eq "No-Call Boundary|Suppressed Material|Local Validation" "$tmpdir/$name.md"
}

cd "$repo_root"

run_plan callback callback-rehearsal-plan https://assops.example.com
grep -q "ASSOPS Provider Callback Rehearsal Plan" "$tmpdir/callback.md"
grep -q "does not call providers" "$tmpdir/callback.md"

run_plan demo demo-import-plan assops-demo https://assops.example.com
grep -q "ASSOPS Live Demo Import Plan" "$tmpdir/demo.md"
grep -q "does not call providers" "$tmpdir/demo.md"

run_plan pod-log pod-log-rehearsal-plan assops-demo https://assops.example.com production assops
grep -q "ASSOPS Argo Pod Log Rehearsal Plan" "$tmpdir/pod-log.md"
grep -q "does not read kubeconfig" "$tmpdir/pod-log.md"

run_plan ssh ssh-rehearsal-plan assops-demo production
grep -q "ASSOPS SSH Target Rehearsal Plan" "$tmpdir/ssh.md"
grep -q "does not read SSH keys" "$tmpdir/ssh.md"

run_plan tag tag-rehearsal-plan assops-demo github-main
grep -q "ASSOPS GitHub Tag Rehearsal Plan" "$tmpdir/tag.md"
grep -q "run Git" "$tmpdir/tag.md"

run_plan config config-rehearsal-plan assops-demo github-config
grep -q "ASSOPS Config Repository Rehearsal Plan" "$tmpdir/config.md"
grep -q "run Git" "$tmpdir/config.md"

run_plan agent-code agent-code-rehearsal-plan assops-demo codex-cli
grep -q "ASSOPS Agent Code Rehearsal Plan" "$tmpdir/agent-code.md"
grep -q "does not start Codex CLI" "$tmpdir/agent-code.md"

run_plan agent-tool agent-tool-rehearsal-plan assops-demo codex-cli
grep -q "ASSOPS Agent Tool Rehearsal Plan" "$tmpdir/agent-tool.md"
grep -q "does not invoke tools" "$tmpdir/agent-tool.md"

run_plan branch-protection branch-protection-plan nathan77886/ass-ops .github/rulesets/main-required-checks.json .github/CODEOWNERS
grep -q "ASSOPS Branch Protection Plan" "$tmpdir/branch-protection.md"
grep -q "does not call GitHub" "$tmpdir/branch-protection.md"

if "${tool[@]}" release callback-rehearsal-plan http://127.0.0.1:8080 "$tmpdir/bad-callback.md" >/tmp/assops-release-rehearsal-bad-callback.log 2>&1; then
  echo "expected callback rehearsal plan to reject loopback HTTP origin" >&2
  exit 1
fi
grep -q "public origin must use https" /tmp/assops-release-rehearsal-bad-callback.log

if "${tool[@]}" release tag-rehearsal-plan assops-demo 'bad remote' "$tmpdir/bad-tag.md" >/tmp/assops-release-rehearsal-bad-tag.log 2>&1; then
  echo "expected tag rehearsal plan to reject unsafe remote key" >&2
  exit 1
fi
grep -q "remote key must contain letters or numbers" /tmp/assops-release-rehearsal-bad-tag.log

echo "release-rehearsal-plans self-test passed"
