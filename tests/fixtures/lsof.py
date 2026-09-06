#!/usr/bin/env python3
"""A strict fake for the two lsof calls sum makes: the cwd table (`-a -d cwd -Fpn -w`) and the TCP listener table
(`-nP -iTCP -sTCP:LISTEN -Fpn -w`). Scenario data comes from FAKE_LSOF_ROOT/cwds.json: `processes` [{pid, cwd}],
`listeners` [{pid, address}], and `fail` (message) to make the cwd pass fail, `fail_listeners` for the listener pass."""
import json, os, pathlib, sys
root = pathlib.Path(os.environ["FAKE_LSOF_ROOT"])
root.mkdir(parents=True, exist_ok=True)
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps({"args": args}) + "\n")
scenario = json.loads((root / "cwds.json").read_text()) if (root / "cwds.json").exists() else {"processes": []}
if args == ["-a", "-d", "cwd", "-Fpn", "-w"]:
    if scenario.get("fail"):
        print(scenario["fail"], file=sys.stderr); sys.exit(1)
    rows = [{"pid": os.getpid(), "cwd": os.getcwd()}, *scenario.get("processes", [])]
    for row in rows:
        print(f"p{row['pid']}\nn{row['cwd']}")
    sys.exit(0)
if args == ["-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn", "-w"]:
    if scenario.get("fail_listeners"):
        print(scenario["fail_listeners"], file=sys.stderr); sys.exit(1)
    rows = scenario.get("listeners", [])
    for row in rows:
        print(f"p{row['pid']}\nn{row['address']}")
    sys.exit(0 if rows else 1)  # Real lsof exits 1 when nothing matched.
print("unsupported fake lsof call: %r" % (args,), file=sys.stderr); sys.exit(2)
