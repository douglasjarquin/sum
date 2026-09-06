#!/usr/bin/env python3
"""Tiny JSON item service. `app.py [PORT]` serves on 127.0.0.1 (ephemeral port when omitted) and prints its URL.

Routes: GET /health -> {"ok": true}; GET /items -> the stored list; POST /items {"name": ...} -> 201 with the stored item.
Items persist in `$ITEMS_DATA_DIR/items.json` (default ./data) so a restart keeps them. Anything else is 404.
"""
import http.server, json, os, pathlib, sys


def data_file():
    directory = pathlib.Path(os.environ.get("ITEMS_DATA_DIR", pathlib.Path(__file__).resolve().parent / "data"))
    directory.mkdir(parents=True, exist_ok=True)
    return directory / "items.json"


def load():
    path = data_file()
    return json.loads(path.read_text()) if path.is_file() else []


class Handler(http.server.BaseHTTPRequestHandler):
    def _send(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/health":
            return self._send(200, {"ok": True})
        if self.path == "/items":
            return self._send(200, load())
        self._send(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/items":
            return self._send(404, {"error": "not found"})
        length = int(self.headers.get("Content-Length") or 0)
        try:
            payload = json.loads(self.rfile.read(length) or b"{}")
            name = payload["name"]
        except (ValueError, KeyError, TypeError):
            return self._send(400, {"error": "name is required"})
        items = load()
        item = {"id": len(items) + 1, "name": name}
        items.append(item)
        data_file().write_text(json.dumps(items))
        self._send(201, item)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    server = http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 0), Handler)
    print(f"http://127.0.0.1:{server.server_address[1]}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
