#!/usr/bin/env python3
"""A strict fake Remainder CLI for Codex quota tests. Never touches the network.

FAKE_REMAINDER_STATUS selects the observation:
  healthy      exit 0, remaining 40
  exhausted    exit 0, remaining 0 (usable zero, not missing)
  unavailable  exit 1, empty stdout, stderr names unavailable
  invalid      exit 2
  partial      exit 3
  timeout      exit 1, stderr names unavailable (this fake does not hang past the helper timeout)

Calls are appended to FAKE_REMAINDER_ROOT/calls.jsonl when that directory is set.
Diagnostics go to stderr only.
"""
import json, os, pathlib, sys
args = sys.argv[1:]
root = os.environ.get("FAKE_REMAINDER_ROOT")
if root:
    path = pathlib.Path(root)
    path.mkdir(parents=True, exist_ok=True)
    with (path / "calls.jsonl").open("a") as out:
        out.write(json.dumps({"args": args}) + "\n")
provider = profile = None
fmt = "compact"
i = 0
while i < len(args):
    if args[i] == "--provider" and i + 1 < len(args):
        provider = args[i + 1]; i += 2; continue
    if args[i] == "--profile" and i + 1 < len(args):
        profile = args[i + 1]; i += 2; continue
    if args[i] == "--format" and i + 1 < len(args):
        fmt = args[i + 1]; i += 2; continue
    print("unsupported fake remainder call: %r" % (args,), file=sys.stderr); sys.exit(2)
status = os.environ.get("FAKE_REMAINDER_STATUS", "healthy")
if status == "invalid" or not provider or not profile:
    print("invalid provider selection", file=sys.stderr); sys.exit(2)
if status == "unavailable" or status == "timeout":
    print("unavailable: no observation for %s" % provider, file=sys.stderr); sys.exit(1)
remaining = 0 if status == "exhausted" else 40
if fmt == "json":
    print(json.dumps({"provider": provider, "profile": profile, "remaining": remaining, "status": status}))
else:
    print("%s remaining=%s" % (provider, remaining))
if status == "partial":
    print("partial observation", file=sys.stderr); sys.exit(3)
sys.exit(0)
