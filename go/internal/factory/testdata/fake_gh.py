#!/usr/bin/env python3
"""Strict fake gh for factory tests. Scenario files live under FAKE_GH_ROOT."""
import json, os, pathlib, sys

root = pathlib.Path(os.environ["FAKE_GH_ROOT"])
root.mkdir(parents=True, exist_ok=True)
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps({"args": args}) + "\n")

def load(name, default):
    path = root / name
    if not path.exists():
        return default
    return json.loads(path.read_text())

def fail(message, code=1):
    print(message, file=sys.stderr)
    sys.exit(code)

if args[:2] == ["issue", "list"]:
    issues = load("issues.json", [])
    label = None
    if "--label" in args:
        label = args[args.index("--label") + 1]
    if label:
        filtered = []
        for issue in issues:
            names = []
            for item in issue.get("labels", []):
                names.append(item if isinstance(item, str) else item.get("name"))
            if label in names:
                filtered.append(issue)
        issues = filtered
    print(json.dumps(issues))
    sys.exit(0)

if args[:2] == ["issue", "view"]:
    number = int(args[2])
    issues = {int(i["number"]): i for i in load("issues.json", [])}
    bodies = load("bodies.json", {})
    issue = issues.get(number, {"number": number, "title": "unknown", "state": "OPEN", "labels": []})
    issue = dict(issue)
    issue["body"] = bodies.get(str(number), bodies.get(number, ""))
    print(json.dumps(issue))
    sys.exit(0)

if args[:2] == ["issue", "edit"] and ("--add-label" in args or "--remove-label" in args):
    print(json.dumps({"ok": True}))
    sys.exit(0)

if args[:2] == ["issue", "comment"]:
    if load("comment_error.json", None):
        fail(load("comment_error.json", {}).get("message", "comment failed"))
    print(json.dumps({"ok": True}))
    sys.exit(0)

if args[:2] == ["project", "item-list"]:
    if load("project_error.json", None):
        fail(load("project_error.json", {}).get("message", "insufficient_scopes"))
    # project_items.json is returned as-is. Each item may include
    # "repository": "https://github.com/owner/repo" or "owner/repo".
    print(json.dumps(load("project_items.json", [])))
    sys.exit(0)

if args[:2] == ["pr", "ready"]:
    print("ready")
    sys.exit(0)

if args[:2] == ["pr", "merge"]:
    print("merged")
    sys.exit(0)

fail("unsupported fake gh call: %r" % (args,), 2)
