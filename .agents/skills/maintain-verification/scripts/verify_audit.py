#!/usr/bin/env python3
"""Audit a repository's verification contract and feature maps against the repository itself, without running anything.

Findings (each fails the audit until resolved):
  placeholder        a `TODO(verify)` left by the scaffold: the map or contract is still a draft
  stale-task         `mise run NAME` mentioned in VERIFY.md or a map, but this repository defines no such task
  stale-path         a backticked path in VERIFY.md or a map that no longer exists
  unlinked-map       a Markdown file beside the index that the index does not link
  missing-link       an index link to a map that does not exist
  coverage-claim     an `automated` scenario row that names no test/command path, or names one that is missing or contains no test
  duplicate-id       a scenario ID defined twice; bad-driver: a Driver cell that is neither `automated` nor `manual`
  unmapped-change    (with --base) a changed source file no feature map references and no --rationale explains
Notes (informational): features affected by the changes since --base, policy files changed since --base, and whether the newest run record
under the artifact directory still matches the current contract and maps (`proof: current|stale|none`).

The audit reads text and Git; it never edits a map, runs a check, or marks anything verified. Authored map changes and actual run results
stay separate: the record it writes lists both, side by side. Works in any clone with Git, mise, and Python 3.11+; no sum or Herdr.

Usage:
  verify_audit.py [--root DIR] [--base REF] [--rationale TEXT] [--json] [--no-record]
Exit codes: 0 clean, 1 findings, 2 blocked (no repository or no VERIFY.md), 3 usage.
"""
from __future__ import annotations

import sys

if sys.version_info < (3, 11):
    sys.stderr.write("verify_audit.py needs Python 3.11 or newer (tomllib).\n")
    sys.exit(2)

import argparse
import datetime as _dt
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import tomllib
from pathlib import Path

FENCE = re.compile(r"^```verify[ \t]*\n(.*?)^```[ \t]*$", re.S | re.M)
LINK = re.compile(r"\]\(([^)\s]+\.md)\)")
ROW = re.compile(r"^\|\s*`?([A-Za-z0-9][A-Za-z0-9._:/-]{0,79})`?\s*\|(.*)\|\s*$")
CODE = re.compile(r"`([^`\n]+)`")
TASK_REF = re.compile(r"\bmise run ([A-Za-z0-9_:.-]+)")
TODO = "TODO(verify)"
PATH_SUFFIXES = (".py", ".md", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".toml", ".json", ".sh", ".yaml", ".yml", ".html", ".go", ".rs", ".rb", ".txt", ".css")
CODE_SUFFIXES = (".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".go", ".rs", ".rb", ".sh", ".html", ".css", ".in", ".toml", ".json", ".yaml", ".yml")
NOT_CODE_PREFIXES = (".artifacts/", ".agents/", ".claude/", ".cursor/", ".github/", "docs/")
NOT_CODE_NAMES = ("VERIFY.md", "README.md", "AGENTS.md", "CLAUDE.md", ".gitignore", "LICENSE", "mise.toml", ".mise.toml", "package-lock.json")


class Blocked(Exception):
    pass


def utc_now():
    return _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def sha256_file(path: Path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def resolve_root(start: Path):
    result = subprocess.run(["git", "-C", str(start), "rev-parse", "--show-toplevel"], text=True, capture_output=True)
    if result.returncode:
        raise Blocked(f"{start} is not inside a Git work tree")
    return Path(result.stdout.strip()).resolve()


def own_tasks(root: Path):
    binary = shutil.which("mise")
    if binary is None:
        return None
    result = subprocess.run([binary, "tasks", "ls", "--json"], cwd=str(root), text=True, capture_output=True, env={**os.environ, "MISE_QUIET": "1"})
    try:
        rows = json.loads(result.stdout or "[]")
    except ValueError:
        return None
    names = set()
    for row in rows:
        source = row.get("source") or row.get("file") or ""
        if source and (Path(source).resolve() == root or root in Path(source).resolve().parents):
            names.add(row["name"])
    return names


def path_tokens(text):
    """Backticked tokens that look like repository paths; the first word of a command is taken so `hello.py NAME` checks `hello.py`."""
    for token in CODE.findall(text):
        word = token.strip().split()[0] if token.strip() else ""
        if not word or word.startswith(("-", "<", "$", "/", "http://", "https://", "mise ", "python", "npm", "node", "git", "curl")) or any(ch in word for ch in "*<>{}$|;&"):
            continue  # Flags, placeholders, URLs, commands, and absolute or route paths (`/health`) are not repository paths.
        if re.match(r"^[A-Z][A-Z0-9_]+/", word):
            continue  # `ITEMS_DATA_DIR/items.json`: an environment-variable-relative path, not a repository path.
        if word.endswith("/") or "/" in word or word.endswith(PATH_SUFFIXES):
            yield word.rstrip(":,")


def references(root: Path, path: Path):
    """Repository paths a map or contract mentions, resolved relative to the repository root or to the file itself."""
    found = set()
    text = path.read_text(encoding="utf-8")
    for word in list(path_tokens(text)) + LINK.findall(text):
        for base in (root, path.parent):
            candidate = (base / word).resolve()
            if candidate.exists():
                try:
                    found.add(str(candidate.relative_to(root)).rstrip("/"))
                except ValueError:
                    pass
                break
    return found


def git_ignored(root: Path, word: str):
    """Runtime paths the repository deliberately ignores (state, run records) are not stale when absent."""
    return any(subprocess.run(["git", "-C", str(root), "check-ignore", "-q", "--no-index", w], capture_output=True).returncode == 0 for w in (word, word.rstrip("/") + "/"))


def check_paths(root: Path, path: Path, findings):
    for word in dict.fromkeys(path_tokens(path.read_text(encoding="utf-8"))):
        if not any((base / word).exists() for base in (root, path.parent)) and not git_ignored(root, word):
            findings.append({"kind": "stale-path", "file": str(path.relative_to(root)), "detail": f"`{word}` does not exist in the repository"})


def check_tasks(root: Path, path: Path, tasks, findings):
    if tasks is None:
        return
    for name in sorted(set(TASK_REF.findall(path.read_text(encoding="utf-8")))):
        if name not in tasks:
            findings.append({"kind": "stale-task", "file": str(path.relative_to(root)), "detail": f"`mise run {name}` is mentioned but this repository defines no `{name}` task"})


def check_placeholders(root: Path, path: Path, findings):
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if TODO in line:
            findings.append({"kind": "placeholder", "file": str(path.relative_to(root)), "line": number, "detail": line.strip()[:160]})


def scenarios_of(root: Path, path: Path, seen, findings):
    rows = []
    driver_column = None
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.lstrip().startswith("|"):
            driver_column = None
            continue
        header = [c.strip().lower() for c in line.strip().strip("|").split("|")]
        if "driver" in header and driver_column is None:
            driver_column = header.index("driver") - 1
            continue
        row = ROW.match(line)
        if not row or driver_column is None:
            continue
        cells = [c.strip() for c in row.group(2).split("|")]
        if driver_column >= len(cells) or set(cells[driver_column]) <= {"-", ":"}:
            continue
        driver = cells[driver_column]
        scenario_id = row.group(1)
        relative = str(path.relative_to(root))
        if not re.match(r"^(automated|manual)\b", driver, re.I):
            findings.append({"kind": "bad-driver", "file": relative, "line": number, "detail": f"{scenario_id}: driver {driver!r} must start with `automated` or `manual`"})
            continue
        if scenario_id in seen:
            findings.append({"kind": "duplicate-id", "file": relative, "line": number, "detail": f"{scenario_id} is also defined in {seen[scenario_id]}"})
        seen[scenario_id] = relative
        automated = driver.lower().startswith("automated")
        if automated and TODO not in driver:  # A placeholder row is already reported as `placeholder`.
            named = [w for w in path_tokens(driver)] + re.findall(r"(?<![\w/])((?:[\w.-]+/)*[\w.-]+\.(?:py|js|mjs|ts|tsx|go|rs|rb|sh))\b", driver)
            if not named:
                findings.append({"kind": "coverage-claim", "file": relative, "line": number, "detail": f"{scenario_id} is `automated` but names no test or command path that covers it"})
            for word in dict.fromkeys(named):
                target = next((base / word for base in (root, path.parent) if (base / word).exists()), None)
                if target is None:
                    findings.append({"kind": "coverage-claim", "file": relative, "line": number, "detail": f"{scenario_id} claims coverage by `{word}`, which does not exist"})
                elif target.is_file() and target.suffix == ".py" and "def test" not in target.read_text(encoding="utf-8", errors="replace") and "unittest" not in target.read_text(encoding="utf-8", errors="replace") and "assert" not in target.read_text(encoding="utf-8", errors="replace"):
                    findings.append({"kind": "coverage-claim", "file": relative, "line": number, "detail": f"{scenario_id} claims coverage by `{word}`, which contains no test or assertion"})
        rows.append({"id": scenario_id, "map": relative, "driver": "automated" if automated else "manual", "driver_text": driver[:200], "line": number})
    return rows


def changed_since(root: Path, base: str):
    probe = subprocess.run(["git", "-C", str(root), "rev-parse", "--verify", "--quiet", base + "^{commit}"], capture_output=True)
    if probe.returncode:
        raise Blocked(f"--base {base!r} is not a commit in this repository")
    result = subprocess.run(["git", "-C", str(root), "diff", "--name-only", base], text=True, capture_output=True, check=True)
    untracked = subprocess.run(["git", "-C", str(root), "ls-files", "--others", "--exclude-standard"], text=True, capture_output=True, check=True)
    return sorted({l for l in (result.stdout + untracked.stdout).splitlines() if l.strip()})


def is_code(path: str):
    if path in NOT_CODE_NAMES or path.startswith(NOT_CODE_PREFIXES) or Path(path).name.startswith("."):
        return False
    return path.endswith(CODE_SUFFIXES)


def latest_run(root: Path, artifacts: str, contract_sha, map_shas):
    latest = root / artifacts / "latest.json"
    if not latest.is_file():
        return {"proof": "none", "detail": "no run record under the artifact directory; the maps have never been exercised here"}
    try:
        record = json.loads(latest.read_text(encoding="utf-8"))
    except ValueError:
        return {"proof": "none", "detail": "latest.json is not valid JSON"}
    recorded_maps = {m["path"]: m["sha256"] for m in record.get("feature_maps", [])}
    current = record.get("contract", {}).get("sha256") == contract_sha and recorded_maps == map_shas
    return {"proof": "current" if current else "stale", "run_id": record.get("run_id"), "outcome": record.get("outcome"), "candidate": record.get("candidate", {}).get("sha"),
            "certifies": record.get("certifies"), "not_exercised": record.get("not_exercised"), "record": record.get("artifacts", {}).get("run_dir"),
            "detail": "the newest run record was produced against the current contract and maps" if current else "the contract or a map changed after the newest run record; that record does not prove the current maps"}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=".")
    parser.add_argument("--base", help="Commit/ref; changed files since it are matched against the maps' references")
    parser.add_argument("--rationale", help="Why the changes since --base need no map change (recorded; turns unmapped-change findings into an acknowledged note)")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--no-record", action="store_true", help="Do not write the audit record under the artifact directory")
    args = parser.parse_args(argv)
    record = {"schema": 1, "audit_id": f"{_dt.datetime.now(_dt.timezone.utc):%Y%m%dT%H%M%SZ}-{secrets.token_hex(4)}", "at": utc_now(), "outcome": None,
              "findings": [], "notes": [], "authored": {}, "runs": None, "changes": None}
    findings = record["findings"]
    root = None
    try:
        root = resolve_root(Path(args.root).resolve())
        record["root"] = str(root)
        contract_path = root / "VERIFY.md"
        if not contract_path.is_file():
            raise Blocked("VERIFY.md is missing at the project root; run the create-verification skill first")
        text = contract_path.read_text(encoding="utf-8")
        match = FENCE.search(text)
        config = {}
        if match:
            try:
                config = tomllib.loads(match.group(1))
            except tomllib.TOMLDecodeError as exc:
                raise Blocked(f"VERIFY.md ```verify block is not valid TOML: {exc}")
        else:
            raise Blocked("VERIFY.md has no ```verify block")
        maps_index = config.get("feature_maps") or "docs/features/README.md"
        artifacts = config.get("artifacts") or ".artifacts/verification"
        tasks = own_tasks(root)
        if tasks is not None and "verify" not in tasks:
            findings.append({"kind": "stale-task", "file": "VERIFY.md", "detail": "this repository defines no `verify` task; the entrypoint `mise run verify` does not exist"})
        check_placeholders(root, contract_path, findings)
        check_paths(root, contract_path, findings)
        check_tasks(root, contract_path, tasks, findings)
        index = root / maps_index
        map_files = []
        if not index.is_file():
            findings.append({"kind": "missing-link", "file": "VERIFY.md", "detail": f"feature-map index `{maps_index}` does not exist"})
        else:
            map_files.append(index)
            linked = set()
            for link in LINK.findall(index.read_text(encoding="utf-8")):
                if link.startswith(("http://", "https://")):
                    continue
                target = (index.parent / link).resolve()
                if not target.is_file():
                    findings.append({"kind": "missing-link", "file": maps_index, "detail": f"links `{link}`, which does not exist"})
                    continue
                linked.add(target)
                map_files.append(target)
            for sibling in sorted(index.parent.glob("*.md")):
                if sibling.resolve() != index.resolve() and sibling.resolve() not in linked:
                    findings.append({"kind": "unlinked-map", "file": str(sibling.relative_to(root)), "detail": f"not linked from `{maps_index}`"})
        seen = {}
        scenarios = []
        refs = {}
        for path in map_files:
            check_placeholders(root, path, findings)
            check_paths(root, path, findings)
            check_tasks(root, path, tasks, findings)
            if path.resolve() != index.resolve():
                scenarios.extend(scenarios_of(root, path, seen, findings))
            refs[str(path.relative_to(root))] = sorted(references(root, path))
        map_shas = {str(p.relative_to(root)): sha256_file(p) for p in map_files}
        record["authored"] = {"contract": {"path": "VERIFY.md", "sha256": sha256_file(contract_path)}, "feature_maps": map_shas, "scenarios": scenarios, "references": refs,
                              "inventory_incomplete": bool(index.is_file() and re.search(r"inventory:\s*\*{0,2}incomplete", index.read_text(encoding="utf-8"), re.I))}
        record["runs"] = latest_run(root, artifacts, sha256_file(contract_path), map_shas)
        record["notes"].append({"kind": "proof", **record["runs"]})
        if args.base:
            changed = changed_since(root, args.base)
            policy_prefixes = ("VERIFY.md", "mise.toml", ".mise.toml", "mise-tasks/", ".agents/skills/verify/", *map_shas)
            policy = [c for c in changed if c.startswith(policy_prefixes)]
            affected, unmapped = {}, []
            for change in changed:
                owners = [m for m, r in refs.items() if any(change == ref or change.startswith(ref + "/") for ref in r)]
                for owner in owners:
                    affected.setdefault(owner, []).append(change)
                if not owners and is_code(change):
                    unmapped.append(change)
            record["changes"] = {"base": args.base, "files": changed, "policy": policy, "affected_maps": affected, "unmapped": unmapped, "rationale": args.rationale}
            if policy:
                record["notes"].append({"kind": "policy-changed", "detail": "verification policy files changed since base; the root's independent review must inspect them", "files": policy})
            for owner, files in affected.items():
                record["notes"].append({"kind": "affected-map", "file": owner, "detail": "references changed files; re-read its entry points, variants, expected states, and coverage", "files": files})
            if unmapped and args.rationale:
                record["notes"].append({"kind": "no-map-change", "detail": args.rationale, "files": unmapped})
            for change in (unmapped if not args.rationale else []):
                findings.append({"kind": "unmapped-change", "file": change, "detail": "changed since base but no feature map references it; update a map or record --rationale for a purely internal change"})
        record["outcome"] = "findings" if findings else "clean"
    except Blocked as exc:
        record["outcome"], record["blocked_reason"] = "blocked", str(exc)
    if root is not None and not args.no_record and record["outcome"] != "blocked":
        out_dir = root / artifacts / "audit"
        out_dir.mkdir(parents=True, exist_ok=True)
        path = out_dir / f"{record['audit_id']}.json"
        path.write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
        record["record"] = str(path.relative_to(root))
    if args.json:
        print(json.dumps(record, indent=2))
    else:
        print(f"audit: {record['outcome']}" + (f" ({record['blocked_reason']})" if record.get("blocked_reason") else ""))
        for f in findings:
            print(f"  {f['kind']:>16}  {f['file']}" + (f":{f['line']}" if f.get("line") else "") + f"  {f['detail']}")
        for n in record["notes"]:
            print(f"  note {n['kind']}{(' ' + n['file']) if n.get('file') else ''}: {n.get('detail')}" + (f" [{', '.join(n['files'])}]" if n.get("files") else ""))
        if record.get("record"):
            print(f"record: {record['record']}")
    return {"clean": 0, "findings": 1, "blocked": 2}[record["outcome"]]


if __name__ == "__main__":
    sys.exit(main())
