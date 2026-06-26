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
    ASSOPS_GATEWAY_URL="http://127.0.0.1:$port" bash scripts/api-smoke.sh
    exit 0
  fi
  sleep 0.1
done

echo "api-smoke stub did not start" >&2
exit 1
