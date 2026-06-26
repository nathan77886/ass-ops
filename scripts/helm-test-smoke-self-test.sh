#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

cat > "$tmpdir/kubectl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

if [[ "$1" == "-n" ]]; then
  shift 2
fi

case "$1" in
  rollout)
    exit 0
    ;;
  get)
    printf '127.0.0.1'
    exit 0
    ;;
  port-forward)
    target="$2"
    mapping="$3"
    local_port="${mapping%%:*}"
    component="gateway"
    case "$target" in
      *node-worker-health) component="node-worker" ;;
      *worker-health) component="control-worker" ;;
    esac

    COMPONENT="$component" PORT="$local_port" python3 - <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os

component = os.environ["COMPONENT"]
port = int(os.environ["PORT"])


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
            self.send_json({"ok": True, "component": component})
            return
        if self.path == "/api/projects":
            if self.headers.get("authorization") != "Bearer smoke-token":
                self.send_json({"error": "unauthorized"}, 401)
                return
            self.send_json({"items": []})
            return
        if self.path == "/api/worker-queue/summary":
            if self.headers.get("authorization") != "Bearer smoke-token":
                self.send_json({"error": "unauthorized"}, 401)
                return
            self.send_json({"queued": 0, "running": 0})
            return
        self.send_json({"error": "not found"}, 404)

    def do_POST(self):
        if self.path == "/api/auth/login":
            self.send_json({"token": "smoke-token"})
            return
        self.send_json({"error": "not found"}, 404)


HTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
    ;;
  *)
    echo "unexpected kubectl args: $*" >&2
    exit 1
    ;;
esac
SH
chmod +x "$tmpdir/kubectl"

PATH="$tmpdir:$PATH" \
ASSOPS_HELM_SMOKE_NAMESPACE=assops-test \
ASSOPS_HELM_SMOKE_RELEASE=test \
ASSOPS_HELM_SMOKE_WEB_PORT=19380 \
ASSOPS_HELM_SMOKE_WORKER_PORT=19381 \
ASSOPS_HELM_SMOKE_NODE_WORKER_PORT=19382 \
bash scripts/helm-test-smoke.sh
