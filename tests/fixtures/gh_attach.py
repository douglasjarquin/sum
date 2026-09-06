#!/usr/bin/env python3
"""A strict fake GitHub CLI for the publication path (issue #35): `--version`, `pr edit --help`, `auth status`, `repo view`, `pr view`,
and `pr edit --body-file --attach`. State lives in FAKE_GH_ROOT/github.json and is edited the way GitHub would: a referenced `./file`
becomes a stable attachment URL derived from the file's content hash, an unreferenced attachment is appended. Scenario knobs:
`version`, `visibility`, `viewer_permission`, `auth` (false = unauthenticated), `edit` (ok | exit | hang | hang-after-write | partial),
and `mutation` ({"after_views": N, "where": "outside" | "inside", "text": ...}) which changes the stored body once after the N-th
`pr view`, so a concurrent human edit between the publisher's read and write can be staged. Never touches the network."""
import hashlib
import json
import os
import pathlib
import re
import sys
import time
import uuid

root = pathlib.Path(os.environ["FAKE_GH_ROOT"])
root.mkdir(parents=True, exist_ok=True)
state_path = root / "github.json"
state = json.loads(state_path.read_text()) if state_path.exists() else {}
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps({"args": args, "cwd": os.getcwd()}) + "\n")


def save():
    state_path.write_text(json.dumps(state, indent=2))


def version():
    return tuple(int(p) for p in state.get("version", "2.100.0").split("."))


def flag(name, default=None):
    return args[args.index(name) + 1] if name in args else default


def attachment_url(digest):
    return f"https://github.com/user-attachments/assets/{uuid.uuid5(uuid.NAMESPACE_URL, digest)}"


def pr():
    return state.setdefault("pr", {})


def pr_json(fields):
    p = pr()
    repo = state.get("repository", "douglasjarquin/project")
    owner, _, name = state.get("head_repository", repo).partition("/")
    full = {"number": p.get("number", 7), "url": f"https://github.com/{repo}/pull/{p.get('number', 7)}", "state": p.get("state", "OPEN"), "body": p.get("body", ""),
            "headRefName": p.get("head_branch", "sum/t-x"), "headRefOid": p.get("head_sha"), "baseRefName": p.get("base_branch", "main"),
            "headRepository": {"name": name}, "headRepositoryOwner": {"login": owner}, "isCrossRepository": state.get("head_repository", repo) != repo,
            "mergedAt": p.get("merged_at"), "mergeCommit": {"oid": p["merge_commit"]} if p.get("merge_commit") else None, "closed": p.get("state", "OPEN") != "OPEN"}
    return {k: full.get(k) for k in fields.split(",")}


def apply_mutation():
    mutation = state.get("mutation")
    if not mutation or mutation.get("applied"):
        return
    state["views"] = state.get("views", 0) + 1
    if state["views"] < mutation.get("after_views", 1):
        return
    body = pr().get("body", "")
    if mutation.get("where") == "inside":
        start = body.find("<!-- before-and-after:start -->")
        insert = start + len("<!-- before-and-after:start -->") if start != -1 else len(body)
        body = body[:insert] + "\n" + mutation.get("text", "human note inside the block") + body[insert:]
    else:
        body = body.rstrip("\n") + "\n\n" + mutation.get("text", "A reviewer added this paragraph meanwhile.") + "\n"
    pr()["body"] = body
    mutation["applied"] = True
    save()


def fail(message, code=1):
    print(message, file=sys.stderr)
    sys.exit(code)


if args == ["--version"]:
    print(f"gh version {state.get('version', '2.100.0')} (2026-09-03)\nhttps://github.com/cli/cli/releases/tag/v{state.get('version', '2.100.0')}")
    sys.exit(0)
if args[:3] == ["pr", "edit", "--help"]:
    print("Edit a pull request.\n\nFLAGS\n" + ("      --attach file             Attach an image or video file, in '<file>#<image alt text>' format\n" if version() >= (2, 99, 0) else "")
          + "  -b, --body string             Set the new body.\n  -F, --body-file file          Read body text from file\n")
    sys.exit(0)
if args[:2] == ["auth", "status"]:
    if state.get("auth", True):
        print("github.com\n  ✓ Logged in to github.com account fake (keyring)")
        sys.exit(0)
    fail("You are not logged into any GitHub hosts. To log in, run: gh auth login", 1)
if args[:2] == ["repo", "view"] and "--json" in args:
    fields = flag("--json")
    repo = state.get("repository", "douglasjarquin/project")
    if len(args) > 2 and not args[2].startswith("--") and args[2] != repo:
        fail(f"GraphQL: Could not resolve to a Repository with the name '{args[2]}'. (repository)")
    full = {"nameWithOwner": repo, "visibility": state.get("visibility", "PUBLIC"), "viewerPermission": state.get("viewer_permission", "WRITE")}
    print(json.dumps({k: full.get(k) for k in fields.split(",")}))
    sys.exit(0)
if args[:2] == ["pr", "view"] and "--json" in args:
    if state.get("view_fail"):
        fail(state["view_fail"])
    number = int(args[2])
    repo = flag("--repo", state.get("repository", "douglasjarquin/project"))
    if number != pr().get("number", 7) or repo != state.get("repository", "douglasjarquin/project"):
        fail("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)" % number)
    apply_mutation()
    print(json.dumps(pr_json(flag("--json"))))
    sys.exit(0)
if args[:2] == ["pr", "edit"]:
    number = int(args[2])
    if number != pr().get("number", 7):
        fail("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)" % number)
    mode = state.get("edit", "ok")
    if mode == "hang":
        time.sleep(120)
    if mode == "exit":
        fail("GraphQL: Resource not accessible by integration (updatePullRequest)")
    if state.get("viewer_permission", "WRITE") not in ("WRITE", "MAINTAIN", "ADMIN"):
        fail("GraphQL: Resource not accessible by personal access token (updatePullRequest)", 1)
    body_file = flag("--body-file")
    body = pathlib.Path(body_file).read_text() if body_file else pr().get("body", "")
    attachments = [args[i + 1] for i, a in enumerate(args) if a == "--attach"]
    if len(attachments) > 50:
        fail("too many attachments: at most 50 files per command")
    failed = []
    for index, spec in enumerate(attachments):
        path_text, _, alt = spec.partition("#")
        path = pathlib.Path(path_text)
        if not path.is_file():
            fail(f"attachment {path_text}: no such file")
        ext = path.suffix.lower()
        if ext not in (".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp4", ".mov", ".webm"):
            fail(f"attachment {path_text}: unsupported file type {ext}")
        data = path.read_bytes()
        if len(data) > (100 if ext in (".mp4", ".mov", ".webm") else 10) * 1024 * 1024:
            fail(f"attachment {path_text}: file too large")
        if mode == "partial" and index > 0:
            failed.append(path_text)
            continue
        url = attachment_url(hashlib.sha256(data).hexdigest())
        ref = "(" + (path_text if path_text.startswith("./") else "./" + path_text) + ")"
        if ref in body:
            body = body.replace(ref, f"({url})")
        else:
            body = body.rstrip("\n") + "\n\n" + (f"![{alt or path.name}]({url})" if ext not in (".mp4", ".mov", ".webm") else url) + "\n"
    pr()["body"] = body
    state["edits"] = state.get("edits", 0) + 1
    save()
    repo = state.get("repository", "douglasjarquin/project")
    print(f"https://github.com/{repo}/pull/{number}")
    if mode == "hang-after-write":
        time.sleep(120)
    if failed:
        fail("failed to upload " + ", ".join(failed), 1)
    sys.exit(0)
if args[:2] == ["repo", "clone"]:
    fail("unsupported in the attach fake", 2)
fail("unsupported fake gh call: %r" % (args,), 2)
