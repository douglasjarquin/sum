#!/usr/bin/env python3
"""A strict fake GitHub CLI for the publication path (issue #35): `--version`, `pr edit --help`, `auth status`, `repo view`, `pr view`,
`pr list`, `pr create`, and `pr edit --body-file --attach`. State lives in FAKE_GH_ROOT/github.json and is edited the way GitHub would: a referenced `./file`
becomes a stable attachment URL derived from the file's content hash, an unreferenced attachment is appended. Scenario knobs:
`version`, `visibility`, `viewer_permission`, `auth` (false = unauthenticated), `edit` (ok | exit | hang | hang-after-write | partial),
and `mutation` ({"after_views": N, "where": "outside" | "inside", "text": ...}) which changes the stored body once after the N-th
`pr view`, so a concurrent human edit between the publisher's read and write can be staged. `prs` is the full list this repository
answers `pr list` with (default: the single `pr`, if any); `head_sha` and `next_number` are what `pr create` gives a new one, and
`create` (ok | timeout | truncated | refuse) decides whether it reports the URL it just created. `mergeable` and `merge_state_status`
(on the state or one PR) are what `pr view` answers for GitHub's mergeability. Never touches the network."""
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


def listed_prs():
    """Every pull request this repository has, which is what `pr list` answers and `pr view`/`pr edit` resolve against."""
    if "prs" in state:
        return state["prs"]
    single = state.get("pr") or {}
    return [single] if single else []


def find_pr(number):
    for candidate in listed_prs():
        if candidate.get("number", 7) == number:
            return candidate
    return None


ROLLUP = {"pass": ("COMPLETED", "SUCCESS"), "fail": ("COMPLETED", "FAILURE"), "pending": ("IN_PROGRESS", ""),
          "skipping": ("COMPLETED", "SKIPPED"), "cancel": ("COMPLETED", "CANCELLED")}


def checks(required_only=False):
    declared = state.get("checks", [])
    return [c for c in declared if c.get("required")] if required_only else declared


def rollup():
    nodes = []
    for c in checks():
        status, conclusion = ROLLUP.get(c.get("bucket", "pending"), ("IN_PROGRESS", ""))
        node = {"__typename": "CheckRun", "name": c["name"], "status": status, "conclusion": conclusion,
                "detailsUrl": c.get("link", ""), "workflowName": c.get("workflow", "")}
        if state.get("rollup_required", True):
            node["isRequired"] = bool(c.get("required"))
        nodes.append(node)
    return nodes


def pr_json(p, fields):
    repo = state.get("repository", "douglasjarquin/project")
    owner, _, name = state.get("head_repository", repo).partition("/")
    full = {"number": p.get("number", 7), "url": f"https://github.com/{repo}/pull/{p.get('number', 7)}", "state": p.get("state", "OPEN"), "body": p.get("body", ""),
            "headRefName": p.get("head_branch", "sum/t-x"), "headRefOid": p.get("head_sha"), "baseRefName": p.get("base_branch", "main"),
            "headRepository": {"name": name}, "headRepositoryOwner": {"login": owner}, "isCrossRepository": state.get("head_repository", repo) != repo,
            "mergedAt": p.get("merged_at"), "mergeCommit": {"oid": p["merge_commit"]} if p.get("merge_commit") else None, "closed": p.get("state", "OPEN") != "OPEN",
            "mergeable": p.get("mergeable", state.get("mergeable", "MERGEABLE")),
            "mergeStateStatus": p.get("merge_state_status", state.get("merge_state_status", "CLEAN")),
            "statusCheckRollup": rollup()}
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
if args[:2] == ["pr", "checks"]:
    if "--json" not in args or not state.get("checks_json", True):
        fail("unknown flag: --json\nUsage:  gh pr checks [<number> | <url> | <branch>] [flags]", 1)
    number = int(args[2])
    repo = flag("--repo", state.get("repository", "douglasjarquin/project"))
    if find_pr(number) is None or repo != state.get("repository", "douglasjarquin/project"):
        fail("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)" % number)
    required_only = "--required" in args
    selected = checks(required_only)
    if not selected:
        branch = pr().get("head_branch", "sum/t-x")
        fail("no %schecks reported on the '%s' branch" % ("required " if required_only else "", branch), 1)
    fields = flag("--json").split(",")
    print(json.dumps([{k: c.get(k, "") for k in fields} for c in selected]))
    buckets = {c.get("bucket") for c in selected}
    sys.exit(1 if buckets & {"fail", "cancel"} else 8 if "pending" in buckets else 0)
if args[:2] == ["pr", "list"]:
    repo = flag("--repo", state.get("repository", "douglasjarquin/project"))
    if repo != state.get("repository", "douglasjarquin/project"):
        fail(f"GraphQL: Could not resolve to a Repository with the name '{repo}'. (repository)")
    head = flag("--head")
    wanted = flag("--state", "open").lower()
    fields = flag("--json", "number,state").split(",")
    rows = []
    for p in listed_prs():
        if head and p.get("head_branch", "sum/t-x") != head:
            continue
        if wanted != "all" and p.get("state", "OPEN").lower() != wanted:
            continue
        rows.append({k: pr_json(p, ",".join(fields))[k] for k in fields})
    print(json.dumps(rows))
    sys.exit(0)
if args[:2] == ["pr", "create"]:
    repo = flag("--repo", state.get("repository", "douglasjarquin/project"))
    head = flag("--head", "sum/t-x")
    base = flag("--base", "main")
    body_file = flag("--body-file")
    body = pathlib.Path(body_file).read_text() if body_file else (flag("--body") or "")
    with (root / "calls.jsonl").open("a") as out:
        out.write(json.dumps({"created": {"title": flag("--title"), "head": head, "base": base,
                                          "draft": "--draft" in args, "body": body}}) + "\n")
    mode = state.get("create", "ok")
    if mode == "refuse":
        fail(state.get("create_error", "pull request create failed: GraphQL: Resource not accessible by integration"))
    number = state.get("next_number", 7)
    created = {"number": number, "state": "OPEN", "head_sha": state.get("head_sha"), "head_branch": head,
               "base_branch": base, "body": body, "draft": "--draft" in args}
    state["pr"] = created
    if "prs" in state:
        state["prs"] = state["prs"] + [created]
    state["creates"] = state.get("creates", 0) + 1
    save()
    if mode == "truncated":
        print(f"Creating pull request for {head} into {base} in {repo}")
        sys.exit(0)
    if mode == "timeout":
        print(f"Creating pull request for {head} into {base} in {repo}")
        sys.exit(124)
    print(f"https://github.com/{repo}/pull/{number}")
    sys.exit(0)
if args[:2] == ["pr", "view"] and "--json" in args:
    if state.get("view_fail"):
        fail(state["view_fail"])
    number = int(args[2])
    repo = flag("--repo", state.get("repository", "douglasjarquin/project"))
    found = find_pr(number)
    if found is None or repo != state.get("repository", "douglasjarquin/project"):
        fail("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)" % number)
    apply_mutation()
    print(json.dumps(pr_json(found, flag("--json"))))
    sys.exit(0)
if args[:2] == ["pr", "edit"]:
    number = int(args[2])
    target = find_pr(number)
    if target is None:
        fail("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)" % number)
    mode = state.get("edit", "ok")
    if mode == "hang":
        time.sleep(120)
    if mode == "exit":
        fail("GraphQL: Resource not accessible by integration (updatePullRequest)")
    if state.get("viewer_permission", "WRITE") not in ("WRITE", "MAINTAIN", "ADMIN"):
        fail("GraphQL: Resource not accessible by personal access token (updatePullRequest)", 1)
    body_file = flag("--body-file")
    body = pathlib.Path(body_file).read_text() if body_file else target.get("body", "")
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
    target["body"] = body
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
