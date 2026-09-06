#!/usr/bin/env python3
"""A strict fake of the codegraph 1.5.0 CLI surface sum uses, with the behavior verified against the real release in an isolated lab:

`init [path]` writes `.codegraph/.gitignore` and the index inside that exact checkout (never a sibling worktree's), honors Git's ignore
rules including `info/exclude`, and re-running it on an existing index is a cheap no-op; `status --json [path]` reports `initialized`,
`indexPath`, `worktreeMismatch`, `pendingChanges` for edits made since the index, and the `index` version/extraction block; `sync` is
incremental; `index` rebuilds; `query NAME -p PATH --json` returns the symbols of that checkout's own index with their source lines;
`install --print-config ID` prints a snippet and writes nothing. Anything else exits 2 so an unverified call is never silently accepted.

Knobs: FAKE_CODEGRAPH_ROOT (call log, concurrency accounting), FAKE_CODEGRAPH_VERSION, FAKE_CODEGRAPH_FAIL=1 (init/index/sync exit 1),
FAKE_CODEGRAPH_SLEEP=SECONDS (slow build, for timeouts and slot bounds), FAKE_CODEGRAPH_EXTRACTION (schema version of new indexes)."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

VERSION = os.environ.get("FAKE_CODEGRAPH_VERSION", "1.5.0")
EXTRACTION = int(os.environ.get("FAKE_CODEGRAPH_EXTRACTION", "24"))
ROOT = Path(os.environ.get("FAKE_CODEGRAPH_ROOT", "/nonexistent-fake-codegraph"))
GITIGNORE = "# CodeGraph data files — local to each machine, not for committing.\n*\n!.gitignore\n"


def log(args):
    if ROOT.parent.exists():
        ROOT.mkdir(parents=True, exist_ok=True)
        with (ROOT / "calls.jsonl").open("a") as handle:
            handle.write(json.dumps({"args": args, "cwd": os.getcwd(), "env": {k: v for k, v in os.environ.items() if k.startswith("CODEGRAPH_")}}) + "\n")


def project_path(args, flag=True):
    if flag and "-p" in args:
        return Path(args[args.index("-p") + 1]).resolve()
    if flag and "--path" in args:
        return Path(args[args.index("--path") + 1]).resolve()
    rest = [a for a in args[1:] if not a.startswith("-")]
    return Path(rest[0]).resolve() if rest else Path.cwd()


def tracked_files(project):
    result = subprocess.run(["git", "-C", str(project), "ls-files", "-z", "--cached", "--others", "--exclude-standard"], capture_output=True)
    if result.returncode:
        return sorted(str(p.relative_to(project)) for p in project.rglob("*.py") if ".codegraph" not in p.parts)
    return sorted(name for name in result.stdout.decode().split("\0") if name and (project / name).is_file() and not name.startswith(".codegraph/"))


def scan(project):
    files, symbols = {}, []
    for name in tracked_files(project):
        data = (project / name).read_bytes()
        files[name] = hashlib.sha256(data).hexdigest()
        if name.endswith(".py"):
            for number, line in enumerate(data.decode(errors="replace").splitlines(), 1):
                match = re.match(r"\s*(?:def|class)\s+([A-Za-z_][A-Za-z0-9_]*)", line)
                if match:
                    symbols.append({"name": match.group(1), "filePath": name, "startLine": number, "kind": "class" if line.lstrip().startswith("class") else "function", "source": line.strip()})
    return files, symbols


def concurrency_enter():
    if not ROOT.parent.exists():
        return None
    active = ROOT / "active"
    active.mkdir(parents=True, exist_ok=True)
    marker = active / str(os.getpid())
    marker.write_text("1")
    count = len(list(active.iterdir()))
    peak = ROOT / "peak.txt"
    previous = int(peak.read_text()) if peak.is_file() else 0
    if count > previous:
        peak.write_text(str(count))
    return marker


def build(project, index_dir, meta_path, full):
    if os.environ.get("FAKE_CODEGRAPH_FAIL"):
        print("✗ fake codegraph: indexing failed as instructed", file=sys.stderr)
        sys.exit(1)
    marker = concurrency_enter()
    try:
        time.sleep(float(os.environ.get("FAKE_CODEGRAPH_SLEEP", "0")))
        index_dir.mkdir(exist_ok=True)
        (index_dir / ".gitignore").write_text(GITIGNORE)
        files, symbols = scan(project)
        (index_dir / "codegraph.db").write_text(json.dumps({"files": files, "symbols": symbols}))
        meta_path.write_text(json.dumps({"version": VERSION, "extraction": EXTRACTION, "projectPath": str(project), "lastIndexed": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                                         "fileCount": len(files), "nodeCount": len(files) + len(symbols), "edgeCount": len(symbols), "full": full}))
    finally:
        if marker is not None:
            marker.unlink(missing_ok=True)
    print(f"◆  Indexed {len(files)} files")


def pending_changes(project, indexed):
    """Only uncommitted edits whose content differs from the indexed content count, as in the real 1.5.0 (lab-verified): a committed change
    leaves every counter at zero while the index is behind, and a sync of a dirty file clears it."""
    result = subprocess.run(["git", "-C", str(project), "status", "--porcelain", "--untracked-files=all"], capture_output=True, text=True)
    pending = {"added": 0, "modified": 0, "removed": 0}
    for line in result.stdout.splitlines() if result.returncode == 0 else []:
        code, name = line[:2], line[3:]
        if name.startswith(".codegraph/"):
            continue
        path = project / name
        if not path.is_file():
            pending["removed"] += name in indexed
            continue
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if name not in indexed:
            pending["added"] += 1
        elif indexed[name] != digest:
            pending["modified"] += 1
    return pending


def status(project):
    index_dir = project / ".codegraph"
    meta_path = index_dir / "meta.json"
    if not meta_path.is_file():
        return {"initialized": False, "version": VERSION, "projectPath": str(project), "indexPath": str(index_dir), "lastIndexed": None}
    meta = json.loads(meta_path.read_text())
    db = json.loads((index_dir / "codegraph.db").read_text())
    pending = pending_changes(project, db["files"])
    return {"initialized": True, "version": VERSION, "projectPath": str(project), "indexPath": str(index_dir), "lastIndexed": meta["lastIndexed"],
            "fileCount": meta["fileCount"], "nodeCount": meta["nodeCount"], "edgeCount": meta["edgeCount"], "dbSizeBytes": (index_dir / "codegraph.db").stat().st_size,
            "backend": "fake", "journalMode": "wal", "languages": ["python"], "pendingChanges": pending,
            "worktreeMismatch": None if meta["projectPath"] == str(project) else {"indexed": meta["projectPath"], "current": str(project)},
            "index": {"builtWithVersion": meta["version"], "builtWithExtractionVersion": meta["extraction"], "currentExtractionVersion": EXTRACTION,
                      "reindexRecommended": meta["extraction"] != EXTRACTION, "state": "complete", "pendingRefs": 0}}


def main(args):
    log(args)
    if not args or args[0] in ("--version", "-V", "version"):
        print(VERSION)
        return 0
    command = args[0]
    if command in ("init", "index", "sync"):
        project = project_path(args)
        if not project.is_dir():
            print(f"✗ {project} is not a directory", file=sys.stderr)
            return 1
        index_dir = project / ".codegraph"
        meta_path = index_dir / "meta.json"
        if command == "init" and meta_path.is_file():
            print("◆  Already initialized")  # The real 1.5.0 re-init on an existing index returns in well under a second without a rebuild.
            return 0
        if command == "sync" and not meta_path.is_file():
            print("✗ CodeGraph isn't available here — no .codegraph/ index exists", file=sys.stderr)
            return 1
        build(project, index_dir, meta_path, full=command != "sync")
        return 0
    if command == "status":
        project = project_path(args)
        value = status(project)
        print(json.dumps(value) if "--json" in args or "-j" in args else f"CodeGraph Status\n{value}")
        return 0
    if command == "query":
        project = project_path(args)
        name = args[1]
        value = status(project)
        if not value["initialized"]:
            print("✗ CodeGraph isn't available here — no .codegraph/ index exists. If you are an AI agent: continue with your usual tools.")
            return 0
        db = json.loads((project / ".codegraph" / "codegraph.db").read_text())
        rows = [{"node": {**s, "language": "python", "qualifiedName": s["name"]}} for s in db["symbols"] if s["name"] == name]
        print(json.dumps(rows, indent=2) if "--json" in args or "-j" in args else "\n".join(f"{r['node']['name']} {r['node']['filePath']}:{r['node']['startLine']}" for r in rows))
        return 0
    if command == "install" and "--print-config" in args:
        print(f"# Add to a config file\n{json.dumps({'mcpServers': {'codegraph': {'type': 'stdio', 'command': 'codegraph', 'args': ['serve', '--mcp']}}}, indent=2)}")
        return 0
    print(f"fake codegraph: unsupported call {args!r}; only the verified 1.5.0 surface is scripted", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
