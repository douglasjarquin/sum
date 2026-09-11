#!/usr/bin/env python3
"""A strict fake for quota-axi --provider NAME. Never touches the network.

Calls are appended to FAKE_QUOTA_AXI_ROOT/calls.jsonl when that directory is set.
"""
import json, os, pathlib, sys
args = sys.argv[1:]
root = os.environ.get("FAKE_QUOTA_AXI_ROOT")
if root:
    path = pathlib.Path(root)
    path.mkdir(parents=True, exist_ok=True)
    with (path / "calls.jsonl").open("a") as out:
        out.write(json.dumps({"args": args}) + "\n")
if os.environ.get("FAKE_QUOTA_AXI_MISSING"):
    print("fake quota-axi disabled", file=sys.stderr); sys.exit(127)
provider = None
i = 0
while i < len(args):
    if args[i] == "--provider" and i + 1 < len(args):
        provider = args[i + 1]; i += 2; continue
    if args[i] == "--format" and i + 1 < len(args):
        i += 2; continue
    print("unsupported fake quota-axi call: %r" % (args,), file=sys.stderr); sys.exit(2)
if not provider:
    print("quota-axi requires --provider", file=sys.stderr); sys.exit(2)
print("quota-axi provider=%s remaining=99" % provider)
sys.exit(0)
