#!/usr/bin/env python3
"""Serve dist/ on an ephemeral loopback port; prints the URL, serves until interrupted."""
import functools, http.server, pathlib, sys
dist = pathlib.Path(__file__).resolve().parent / "dist"
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(dist))
server = http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 0), handler)
print(f"http://127.0.0.1:{server.server_address[1]}", flush=True)
try:
    server.serve_forever()
except KeyboardInterrupt:
    pass
