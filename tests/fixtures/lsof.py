#!/usr/bin/env python3
"""A strict fake for the one lsof call sum makes: `lsof -a -d cwd -Fpn -w`. Processes come from FAKE_LSOF_ROOT/cwds.json."""
import json, os, pathlib, sys
root = pathlib.Path(os.environ["FAKE_LSOF_ROOT"])
root.mkdir(parents=True, exist_ok=True)
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps({"args": args}) + "\n")
if args != ["-a", "-d", "cwd", "-Fpn", "-w"]:
    print("unsupported fake lsof call: %r" % (args,), file=sys.stderr); sys.exit(2)
scenario = json.loads((root / "cwds.json").read_text()) if (root / "cwds.json").exists() else {"processes": []}
if scenario.get("fail"):
    print(scenario["fail"], file=sys.stderr); sys.exit(1)
rows = [{"pid": os.getpid(), "cwd": os.getcwd()}, *scenario["processes"]]
for row in rows:
    print(f"p{row['pid']}\nn{row['cwd']}")
