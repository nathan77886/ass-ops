#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
cleanup() {
  if [[ -n "${stub_pid:-}" ]]; then
    kill "$stub_pid" 2>/dev/null || true
  fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

cat > "$tmpdir/api_smoke_stub.py" <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import json
import os


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        return

    def send_json(self, data, status=200):
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            self.send_json({"ok": True, "component": "gateway"})
            return
        if self.path == "/api/projects":
            if self.headers.get("authorization") != "Bearer smoke-token":
                self.send_json({"error": "unauthorized"}, 401)
                return
            self.send_json({"items": [{"id": "project-1", "name": "Smoke Project"}]})
            return
        if self.path == "/api/worker-queue/summary":
            if self.headers.get("authorization") != "Bearer smoke-token":
                self.send_json({"error": "unauthorized"}, 401)
                return
            self.send_json({"queued_jobs": 0, "running_jobs": 0})
            return
        if self.headers.get("authorization") != "Bearer smoke-token":
            self.send_json({"error": "unauthorized"}, 401)
            return
        if self.path == "/api/assets/graph":
            self.send_json({"nodes": [], "edges": []})
            return
        if self.path == "/api/projects/project-1/git-repositories":
            self.send_json({"items": [{"id": "repo-1", "name": "demo-service"}]})
            return
        if self.path == "/api/git-repositories/repo-1/remotes":
            self.send_json({"items": [{"id": "remote-1", "name": "github"}]})
            return
        if self.path == "/api/projects/project-1/kubernetes/environments":
            self.send_json({"items": [{
                "id": "k8s-env-1",
                "name": "Demo Kubernetes Environment",
                "environment": "staging",
                "cluster_name": "demo-cluster",
                "namespace": "demo",
                "kubeconfig_secret_ref_present": True,
                "token_subject_review_ready": True,
                "rbac_read_logs_ready": True,
                "rbac_restart_pods_ready": True,
                "log_access_metadata_ready": True,
                "pod_restart_metadata_ready": True,
            }]})
            return
        if self.path == "/api/projects/project-1/deployment-targets":
            self.send_json({"items": [{
                "id": "target-1",
                "name": "demo-cluster/demo",
                "environment": "staging",
                "cluster_name": "demo-cluster",
                "namespace": "demo",
                "kubernetes_environment_id": "k8s-env-1",
                "kubeconfig_secret_ref_present": True,
                "token_subject_review_status": "reviewed",
                "rbac_read_logs_status": "reviewed",
                "rbac_restart_pods_status": "reviewed",
                "kubernetes_environment_status": "ready",
            }]})
            return
        if self.path == "/api/git-remotes/remote-1":
            self.send_json({"id": "remote-1", "name": "github"})
            return
        if self.path == "/api/repo-tag-runs?repo_id=repo-1":
            self.send_json({"items": [{"id": "tag-run-1", "tag_name": "v0.1.0", "status": "completed", "target_sha": "0123456789abcdef0123456789abcdef01234567"}]})
            return
        if self.path == "/api/git-remotes/remote-1/github-actions":
            self.send_json({"items": [{
                "id": "action-1",
                "workflow_name": "CI",
                "status": "completed",
                "conclusion": "success",
                "artifact_count": 1,
                "active_artifact_count": 1,
                "total_artifact_size_in_bytes": 1024,
                "artifacts": [{"name": "build.tar.gz"}],
            }]})
            return
        if self.path == "/api/git-remotes/remote-1/github-labels":
            self.send_json({"items": [{
                "id": "label-1",
                "name": "bug",
                "color": "d73a4a",
                "is_default": True,
                "result_scope": "github_repository_label_read_model",
                "provider_response_included": False,
                "credential_included": False,
            }]})
            return
        item_paths = {
            "/api/assets",
            "/api/operations",
            "/api/repo-sync-runs",
            "/api/repo-tag-runs",
            "/api/operation-approvals",
            "/api/ai-runtimes",
            "/api/git-repositories/repo-1/repo-sync-assets",
            "/api/projects/project-1/argo/connections",
            "/api/projects/project-1/argo/apps",
            "/api/projects/project-1/deployment-records",
            "/api/projects/project-1/rollback-points",
            "/api/projects/project-1/ssh-machines",
            "/api/projects/project-1/webhook-connections",
            "/api/projects/project-1/agent/tasks",
        }
        if self.path in item_paths:
            self.send_json({"items": []})
            return
        self.send_json({"error": "not found"}, 404)

    def do_POST(self):
        if self.path == "/api/deployment-targets/target-1/pods":
            if self.headers.get("authorization") != "Bearer smoke-token":
                self.send_json({"error": "unauthorized"}, 401)
                return
            self.send_json({
                "mode": "deployment_target_pod_metadata",
                "backend_state": "blocked",
                "result_scope": "sanitized_pod_metadata",
                "items": [],
                "kubernetes_api_call": False,
                "raw_response_included": False,
                "secret_included": False,
                "log_body_included": False,
            })
            return
        if self.path == "/api/projects/project-1/argo/pod-log-query-preview":
            if self.headers.get("authorization") != "Bearer smoke-token":
                self.send_json({"error": "unauthorized"}, 401)
                return
            self.send_json({
                "mode": "read_only_preview",
                "query_state": "ready_for_approval",
                "retrieval_plan": {"approval_status": "planned"},
                "deployment_target": {"id": "target-1"},
                "external_call_made": False,
                "kubernetes_api_call": False,
                "log_body_included": False,
                "contains_secret": False,
                "contains_token": False,
            })
            return
        if self.path != "/api/auth/login":
            self.send_json({"error": "not found"}, 404)
            return
        length = int(self.headers.get("content-length", "0"))
        body = json.loads(self.rfile.read(length) or b"{}")
        if not body.get("email") or not body.get("password"):
            self.send_json({"error": "missing credentials"}, 400)
            return
        self.send_json({"token": "smoke-token"})


server = HTTPServer(("127.0.0.1", 0), Handler)
Path(os.environ["PORT_FILE"]).write_text(str(server.server_port), encoding="utf-8")
server.serve_forever()
PY

PORT_FILE="$tmpdir/port" python3 "$tmpdir/api_smoke_stub.py" &
stub_pid="$!"

for _ in $(seq 1 50); do
  if [[ -s "$tmpdir/port" ]]; then
    port="$(cat "$tmpdir/port")"
    ASSOPS_GATEWAY_URL="http://127.0.0.1:$port" \
    ASSOPS_API_SMOKE_REQUIRE_PROJECT=true \
      bash scripts/api-smoke.sh
    exit 0
  fi
  sleep 0.1
done

echo "api-smoke stub did not start" >&2
exit 1
