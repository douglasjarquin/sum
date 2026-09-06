#!/usr/bin/env python3
"""Functional check: build (unless --no-build), start the service on an ephemeral port, fetch /, assert the page, stop the service."""
import pathlib, subprocess, sys, urllib.request
root = pathlib.Path(__file__).resolve().parent
if "--no-build" not in sys.argv:
    subprocess.run([sys.executable, str(root / "build.py")], check=True)
if not (root / "dist/index.html").is_file():
    print("dist/index.html missing", file=sys.stderr); sys.exit(1)
server = subprocess.Popen([sys.executable, str(root / "serve.py")], stdout=subprocess.PIPE, text=True)
try:
    url = server.stdout.readline().strip()
    body = urllib.request.urlopen(url + "/", timeout=5).read().decode()
    assert "<h1>Fixture service</h1>" in body, body
    assert "@BUILD@" not in body, "template was served unrendered"
    print("served", url, "ok")
finally:
    server.terminate(); server.wait(timeout=5)
