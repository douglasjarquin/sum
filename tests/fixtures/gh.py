#!/usr/bin/env python3
"""A strict fake for the two documented gh calls sum makes. Scenario comes from FAKE_GH_ROOT/pr.json; never touches the network."""
import json, os, pathlib, sys, time
root = pathlib.Path(os.environ["FAKE_GH_ROOT"])
root.mkdir(parents=True, exist_ok=True)
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps({"args": args, "cwd": os.getcwd()}) + "\n")
scenario = json.loads((root / "pr.json").read_text()) if (root / "pr.json").exists() else {}
if scenario.get("hang"):
    time.sleep(120)
if args[:3] == ["repo", "view", "--json"] and args[3] == "nameWithOwner":
    print(json.dumps({"nameWithOwner": scenario.get("repository", "douglasjarquin/project")})); sys.exit(0)
if args[:2] == ["pr", "checks"]:
    declared = scenario.get("checks", [])
    selected = [c for c in declared if c.get("required")] if "--required" in args else declared
    if "--json" not in args:
        print("unknown flag: --json", file=sys.stderr); sys.exit(1)
    if not selected:
        print("no %schecks reported on the '%s' branch" % ("required " if "--required" in args else "", scenario.get("head_branch", "sum/t-x")),
              file=sys.stderr); sys.exit(1)
    fields = args[args.index("--json") + 1].split(",")
    print(json.dumps([{k: c.get(k, "") for k in fields} for c in selected]))
    buckets = {c.get("bucket") for c in selected}
    sys.exit(1 if buckets & {"fail", "cancel"} else 8 if "pending" in buckets else 0)
if args[:2] == ["pr", "view"] and "--repo" in args and "--json" in args:
    number = int(args[2]); repo = args[args.index("--repo") + 1]
    if scenario.get("fail"):
        print(json.dumps({"message": scenario["fail"]}), file=sys.stderr); sys.exit(1)
    if number != scenario.get("number") or repo != scenario.get("repository", "douglasjarquin/project"):
        print("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)" % number, file=sys.stderr); sys.exit(1)
    owner, _, name = scenario.get("head_repository", repo).partition("/")
    print(json.dumps({"number": number, "url": f"https://github.com/{repo}/pull/{number}", "state": scenario.get("state", "OPEN"),
                      "headRefName": scenario["head_branch"], "headRefOid": scenario["head_sha"], "baseRefName": scenario.get("base_branch", "main"),
                      "headRepository": {"name": name}, "headRepositoryOwner": {"login": owner}, "isCrossRepository": scenario.get("head_repository", repo) != repo,
                      "mergedAt": scenario.get("merged_at"), "mergeCommit": {"oid": scenario["merge_commit"]} if scenario.get("merge_commit") else None,
                      "closed": scenario.get("state", "OPEN") != "OPEN", "statusCheckRollup": []})); sys.exit(0)
print("unsupported fake gh call: %r" % (args,), file=sys.stderr); sys.exit(2)
