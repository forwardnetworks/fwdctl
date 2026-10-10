#!/usr/bin/env python3
"""A tiny fake Forward for the standalone-help eval: canned JSON per route, every request logged.
Unrouted requests answer 599 and are logged, so a task that needs a route the fake lacks shows up as a gap in the fixture,
not as a model failure. Synthetic data only. Usage: fake_forward.py PORT ROUTES.json CALLS.log"""
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port, routes_path, log_path = int(sys.argv[1]), sys.argv[2], sys.argv[3]
routes = json.load(open(routes_path))

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _serve(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n).decode() if n else ""
        path = self.path.split("?")[0]
        key = f"{self.command} {path}"
        hit = routes.get(key)
        with open(log_path, "a") as f:
            f.write(json.dumps({"method": self.command, "path": self.path, "body": body, "routed": hit is not None}) + "\n")
        status, payload = (hit.get("status", 200), hit.get("body")) if hit else (599, {"error": "no fixture for " + key})
        data = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _serve

HTTPServer(("127.0.0.1", port), H).serve_forever()
