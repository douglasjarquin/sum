#!/usr/bin/env python3
"""Strict fake gh for factory tests. Scenario files live under FAKE_GH_ROOT."""
import json, os, pathlib, sys
import time

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

def next_value(name, counter, default):
    values = load(name, default)
    if not isinstance(values, list) or not values:
        return default
    path = root / counter
    index = int(path.read_text()) if path.exists() else 0
    path.write_text(str(index + 1))
    return values[min(index, len(values) - 1)]

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
    # project_items.json is returned as-is, truncated to --limit like gh.
    # Each item may include "repository": "https://github.com/owner/repo"
    # or "owner/repo".
    items = load("project_items.json", [])
    if "--limit" in args:
        try:
            limit = int(args[args.index("--limit") + 1])
        except (ValueError, IndexError):
            limit = None
        else:
            if isinstance(items, list):
                items = items[:limit]
            elif isinstance(items, dict):
                for key in ("items", "nodes"):
                    inner = items.get(key)
                    if isinstance(inner, list):
                        items = dict(items)
                        items[key] = inner[:limit]
                        break
    print(json.dumps(items))
    sys.exit(0)

if args[:2] == ["pr", "view"]:
    delay = load("pr_view_delay.json", 0)
    if delay:
        time.sleep(float(delay))
    observation = next_value("pr_views.json", "pr_view_count.txt", {})
    if load("pr_view_pause.json", False):
        (root / "pr_view_paused").write_text("ready")
        while not (root / "pr_view_continue").exists():
            time.sleep(0.01)
    print(json.dumps(observation))
    sys.exit(0)

if args[:2] == ["pr", "checks"]:
    checks = next_value("pr_checks.json", "pr_checks_count.txt", [])
    print(json.dumps(checks))
    sys.exit(0)

if args[:2] == ["pr", "ready"]:
    print("ready")
    sys.exit(0)

if args[:2] == ["pr", "merge"]:
    delay = load("pr_merge_delay.json", 0)
    if delay:
        time.sleep(float(delay))
    if load("merge_error.json", None):
        error = load("merge_error.json", {})
        fail(error.get("message", "merge result unknown"), error.get("code", 1))
    print("merged")
    sys.exit(0)

fail("unsupported fake gh call: %r" % (args,), 2)
