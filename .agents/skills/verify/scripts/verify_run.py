#!/usr/bin/env python3
"""Portable verification runner for the project-root VERIFY.md contract.

Runs in an ordinary clone with nothing but Git, mise, and Python 3.11+: no sum installation, no Herdr, no absolute path
to anything outside this repository. It reads VERIFY.md, checks that the canonical `mise run verify` task belongs to this
project (not a parent directory's configuration), runs it, and writes one evidence record per run under the Git-ignored
artifact directory. Every outcome is one of pass / fail / blocked / not-applicable; a missing or malformed contract is
`blocked`, never a pass, and a dirty tree makes the run provisional so it cannot certify a commit SHA.

Usage:
  verify_run.py [--root DIR] [--check] [--base REF] [--scenario ID=STATUS[:reason]]... [--timeout SECONDS] [--json]
Exit codes: 0 pass, 1 fail, 2 blocked (configuration, dependency, or freshness prevented a trustworthy run), 3 usage.
"""
from __future__ import annotations

import sys

if sys.version_info < (3, 11):
    sys.stderr.write(f"verify_run.py needs Python 3.11 or newer (tomllib); this is {sys.version.split()[0]}. Blocked, not passed.\n")
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
import time
import tomllib
from pathlib import Path

SCHEMA = 1
CONTRACT_FILE = "VERIFY.md"
REQUIRED_HEADINGS = ("Setup", "Readiness", "Teardown", "Automated checks", "Scenarios", "Isolation", "Artifacts")
SCENARIO_STATUSES = ("pass", "fail", "blocked", "not-run", "not-applicable")
POLICY_FILES_DEFAULT = ("VERIFY.md", "mise.toml", ".mise.toml", "mise-tasks/", ".agents/skills/verify/")
FENCE = re.compile(r"^```verify[ \t]*\n(.*?)^```[ \t]*$", re.S | re.M)
LINK = re.compile(r"\]\(([^)\s]+\.md)\)")
ROW = re.compile(r"^\|\s*`?([A-Za-z0-9][A-Za-z0-9._:/-]{0,79})`?\s*\|(.*)\|\s*$")


class Blocked(Exception):
    """The run cannot produce a trustworthy verdict; the reason is recorded, never converted into a pass."""


def utc_now():
    return _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def sha256_file(path: Path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(root: Path, *args, check=True):
    result = subprocess.run(["git", "-C", str(root), *args], text=True, capture_output=True)
    if check and result.returncode:
        raise Blocked(f"git {' '.join(args)} failed: {(result.stderr or result.stdout).strip()[:300]}")
    return result


def resolve_root(start: Path):
    """The real Git top level. A linked worktree's `.git` is a file, a managed clone is nested under another repository; both resolve here."""
    result = subprocess.run(["git", "-C", str(start), "rev-parse", "--show-toplevel"], text=True, capture_output=True)
    if result.returncode:
        raise Blocked(f"{start} is not inside a Git work tree: {(result.stderr or '').strip()[:200]}")
    return Path(result.stdout.strip()).resolve()


def load_contract(root: Path):
    path = root / CONTRACT_FILE
    if not path.is_file():
        raise Blocked(f"{CONTRACT_FILE} is missing at the project root {root}; this project is not yet standardized.")
    text = path.read_text(encoding="utf-8")
    match = FENCE.search(text)
    if not match:
        raise Blocked(f"{CONTRACT_FILE} has no ```verify configuration block.")
    try:
        config = tomllib.loads(match.group(1))
    except tomllib.TOMLDecodeError as exc:
        raise Blocked(f"{CONTRACT_FILE} ```verify block is not valid TOML: {exc}")
    headings = {h.strip().lower() for h in re.findall(r"^#{2,3}\s+(.+?)\s*$", text, re.M)}
    missing = [h for h in REQUIRED_HEADINGS if h.lower() not in headings]
    if missing:
        raise Blocked(f"{CONTRACT_FILE} lacks required section(s): {', '.join(missing)}.")
    entrypoint = config.get("entrypoint")
    if entrypoint != "mise run verify":
        raise Blocked(f"{CONTRACT_FILE} entrypoint must be the literal `mise run verify`, found {entrypoint!r}.")
    for key in ("feature_maps", "artifacts"):
        value = config.get(key)
        if not isinstance(value, str) or not value or Path(value).is_absolute() or ".." in Path(value).parts:
            raise Blocked(f"{CONTRACT_FILE} `{key}` must be a relative path inside the repository, found {value!r}.")
    owner = config.get("task_owner", ".")
    if not isinstance(owner, str) or Path(owner).is_absolute() or ".." in Path(owner).parts:
        raise Blocked(f"{CONTRACT_FILE} `task_owner` must be a relative directory inside the repository, found {owner!r}.")
    requires = config.get("requires", {})
    freshness = config.get("freshness", {})
    for name, value in (("requires", requires), ("freshness", freshness)):
        if not isinstance(value, dict):
            raise Blocked(f"{CONTRACT_FILE} `{name}` must be a table.")
    for key in ("commands",):
        if not all(isinstance(v, str) for v in requires.get(key, [])):
            raise Blocked(f"{CONTRACT_FILE} `requires.{key}` must be a list of strings.")
    for key in ("inputs", "outputs"):
        if not all(isinstance(v, str) and not Path(v).is_absolute() for v in freshness.get(key, [])):
            raise Blocked(f"{CONTRACT_FILE} `freshness.{key}` must be a list of relative paths.")
    timeout = config.get("timeout_seconds", 3600)
    if not isinstance(timeout, int) or timeout <= 0:
        raise Blocked(f"{CONTRACT_FILE} `timeout_seconds` must be a positive integer.")
    return {"path": path, "sha256": sha256_file(path), "entrypoint": entrypoint, "feature_maps": config["feature_maps"], "artifacts": config["artifacts"],
            "task_owner": owner, "requires": requires, "freshness": freshness, "timeout": timeout,
            "policy_files": list(config.get("policy_files", POLICY_FILES_DEFAULT))}


def load_feature_maps(root: Path, index_relative: str):
    """The index links every feature map; each map lists scenarios as table rows `| id | ... | automated|manual ... |`."""
    index = root / index_relative
    if not index.is_file():
        raise Blocked(f"Feature-map index {index_relative} is missing.")
    maps = [{"path": index_relative, "sha256": sha256_file(index)}]
    scenarios = []
    seen = set()
    for link in LINK.findall(index.read_text(encoding="utf-8")):
        if link.startswith(("http://", "https://")):
            continue
        target = (index.parent / link).resolve()
        try:
            relative = target.relative_to(root)
        except ValueError:
            raise Blocked(f"Feature map link {link} in {index_relative} leaves the repository.")
        if not target.is_file():
            raise Blocked(f"Feature map {relative} linked from {index_relative} is missing.")
        maps.append({"path": str(relative), "sha256": sha256_file(target)})
        driver_column = None
        for line in target.read_text(encoding="utf-8").splitlines():
            if not line.lstrip().startswith("|"):
                driver_column = None  # A table ended; the next one declares its own columns.
                continue
            header = [c.strip().lower() for c in line.strip().strip("|").split("|")]
            if "driver" in header and driver_column is None:
                driver_column = header.index("driver") - 1  # Cells after the id column.
                continue
            row = ROW.match(line)
            if not row or driver_column is None:
                continue
            cells = [c.strip() for c in row.group(2).split("|")]
            if driver_column >= len(cells) or set(cells[driver_column]) <= {"-", ":"}:
                continue  # The `| --- |` separator or a short row.
            driver = cells[driver_column]
            if not re.match(r"^(automated|manual)\b", driver, re.I):
                raise Blocked(f"Scenario {row.group(1)} in {relative} has driver {driver!r}; it must start with `automated` or `manual`.")
            scenario_id = row.group(1)
            if scenario_id in seen:
                raise Blocked(f"Scenario id {scenario_id} is defined twice across the feature maps.")
            seen.add(scenario_id)
            scenarios.append({"id": scenario_id, "map": str(relative), "driver": "automated" if driver.lower().startswith("automated") else "manual",
                              "driver_text": driver[:200], "description": (cells[0] if cells else "")[:200]})
    return maps, scenarios


def check_requirements(requires):
    missing = [c for c in requires.get("commands", []) if shutil.which(c) is None]
    if missing:
        raise Blocked(f"Required command(s) not found on PATH: {', '.join(missing)}. The run is blocked, not failed.")
    return {"commands": requires.get("commands", [])}


def mise_task(root: Path, owner_relative: str, name="verify"):
    """`mise tasks ls --json` from the root; the selected task's source must live under this repository (or its documented monorepo owner)."""
    binary = shutil.which("mise")
    if binary is None:
        raise Blocked("mise is not on PATH; the canonical entrypoint cannot run.")
    result = subprocess.run([binary, "tasks", "ls", "--json"], cwd=str(root), text=True, capture_output=True, env={**os.environ, "MISE_QUIET": "1"})
    if result.returncode and not result.stdout.strip():
        raise Blocked(f"mise tasks ls exited {result.returncode}: {(result.stderr or '').strip()[-300:]}")
    try:
        rows = json.loads(result.stdout or "[]")
    except ValueError:
        raise Blocked("mise tasks ls did not return JSON.")
    owner = (root / owner_relative).resolve()
    if not owner.is_dir() or (owner != root and root not in owner.parents and owner not in root.parents):
        raise Blocked(f"task_owner {owner_relative!r} is not a directory of this repository.")
    candidates = [r for r in rows if isinstance(r, dict) and r.get("name") == name]
    if not candidates:
        raise Blocked(f"mise defines no `{name}` task for this repository; VERIFY.md names an entrypoint that does not exist.")
    task = candidates[0]
    source = task.get("source") or task.get("file") or ""
    source_path = Path(source).resolve() if source else None
    inside = source_path is not None and (source_path == owner or owner in source_path.parents or source_path == root or root in source_path.parents)
    if not inside:
        raise Blocked(f"`mise run {name}` here would execute a task defined outside this repository ({source or 'unknown source'}); "
                      "it is another project's command, not this one's verification.")
    return {"name": name, "source": str(source_path), "owner": str(owner)}


def freshness_state(root: Path, freshness):
    inputs, outputs = freshness.get("inputs", []), freshness.get("outputs", [])
    if not outputs:
        return None

    def newest(paths):
        stamp = None
        for relative in paths:
            path = root / relative
            files = [p for p in path.rglob("*") if p.is_file()] if path.is_dir() else ([path] if path.is_file() else [])
            for f in files:
                stamp = max(stamp or 0, f.stat().st_mtime)
        return stamp

    out, inp = newest(outputs), newest(inputs)
    if out is None:
        return {"fresh": False, "reason": f"build output(s) {outputs} are missing"}
    if inp is not None and inp > out:
        return {"fresh": False, "reason": f"build output(s) {outputs} are older than input(s) {inputs}; the checked application is stale"}
    return {"fresh": True, "reason": None}


def policy_change(root: Path, base: str | None, files):
    if not base:
        return {"checked": False, "base": None, "changed": []}
    if git(root, "rev-parse", "--verify", "--quiet", base + "^{commit}", check=False).returncode:
        raise Blocked(f"--base {base!r} is not a commit in this repository.")
    result = git(root, "diff", "--name-only", f"{base}...HEAD", "--", *files)
    changed = [line for line in result.stdout.splitlines() if line.strip()]
    return {"checked": True, "base": base, "changed": changed}


def parse_scenario_args(values):
    outcomes = {}
    for value in values or []:
        if "=" not in value:
            raise SystemExit(f"--scenario expects ID=STATUS[:reason], got {value!r}")
        scenario_id, rest = value.split("=", 1)
        status, _, reason = rest.partition(":")
        if status not in SCENARIO_STATUSES or status == "not-run":
            raise SystemExit(f"--scenario status must be one of pass, fail, blocked, not-applicable; got {status!r}")
        if status in ("not-applicable", "blocked") and not reason.strip():
            raise SystemExit(f"--scenario {scenario_id}={status} needs a reason after ':'")
        outcomes[scenario_id] = {"status": status, "reason": reason.strip() or None}
    return outcomes


def run_entrypoint(root: Path, run_dir: Path, timeout: int):
    log = run_dir / "verify.log"
    started = utc_now()
    clock = time.monotonic()
    with log.open("w", encoding="utf-8") as handle:
        try:
            proc = subprocess.run(["mise", "run", "verify"], cwd=str(root), stdout=handle, stderr=subprocess.STDOUT, timeout=timeout,
                                  env={**os.environ, "SUM_VERIFY_RUN_DIR": str(run_dir)})
            exit_code, timed_out = proc.returncode, False
        except subprocess.TimeoutExpired:
            exit_code, timed_out = None, True
    return {"command": "mise run verify", "cwd": str(root), "started_at": started, "ended_at": utc_now(), "seconds": round(time.monotonic() - clock, 3),
            "exit": exit_code, "timed_out": timed_out, "log": str(log.relative_to(root))}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=".", help="Any directory inside the repository; the Git top level is resolved from it")
    parser.add_argument("--check", action="store_true", help="Validate the contract, maps, task ownership, and requirements without running anything")
    parser.add_argument("--base", help="Commit/ref to compare policy files against; changes to VERIFY.md, maps, or tasks are flagged for root review")
    parser.add_argument("--scenario", action="append", metavar="ID=STATUS[:reason]", help="Outcome of a manual/interactive scenario you actually exercised")
    parser.add_argument("--timeout", type=int, help="Override the contract's timeout_seconds for the entrypoint")
    parser.add_argument("--json", action="store_true", help="Print the full run record instead of a summary")
    args = parser.parse_args(argv)
    outcomes = parse_scenario_args(args.scenario)
    run_id = f"{_dt.datetime.now(_dt.timezone.utc):%Y%m%dT%H%M%SZ}-{secrets.token_hex(4)}"
    record = {"schema": SCHEMA, "run_id": run_id, "started_at": utc_now(), "runner": {"path": str(Path(__file__).resolve()), "mode": "check" if args.check else "run"},
              "outcome": None, "blocked_reason": None, "provisional": None, "certifies": None, "requires_root_review": False}
    root = None
    try:
        root = resolve_root(Path(args.root).resolve())
        record["root"] = str(root)
        head = git(root, "rev-parse", "HEAD").stdout.strip()
        dirty = bool(git(root, "status", "--porcelain", "--untracked-files=normal").stdout.strip())
        record["candidate"] = {"sha": head, "dirty": dirty, "branch": git(root, "rev-parse", "--abbrev-ref", "HEAD", check=False).stdout.strip() or None,
                               "git_dir_is_file": (root / ".git").is_file()}
        record["provisional"] = dirty
        contract = load_contract(root)
        record["contract"] = {"path": CONTRACT_FILE, "sha256": contract["sha256"], "entrypoint": contract["entrypoint"], "task_owner": contract["task_owner"]}
        maps, scenarios = load_feature_maps(root, contract["feature_maps"])
        record["feature_maps"] = maps
        unknown = sorted(set(outcomes) - {s["id"] for s in scenarios})
        if unknown:
            raise SystemExit(f"--scenario names id(s) not present in any feature map: {', '.join(unknown)}")
        record["requirements"] = check_requirements(contract["requires"])
        record["task"] = mise_task(root, contract["task_owner"])
        record["policy"] = policy_change(root, args.base, [*contract["policy_files"], *(m["path"] for m in maps)])  # Maps are policy too.
        artifacts = root / contract["artifacts"]
        ignored = subprocess.run(["git", "-C", str(root), "check-ignore", "-q", str(artifacts)], capture_output=True).returncode == 0
        record["artifacts"] = {"dir": contract["artifacts"], "git_ignored": ignored}
        if args.check:
            record["outcome"] = "checked"
            record["scenarios"] = scenarios
        else:
            run_dir = artifacts / run_id
            run_dir.mkdir(parents=True, exist_ok=False)
            record["artifacts"]["run_dir"] = str(run_dir.relative_to(root))
            execution = run_entrypoint(root, run_dir, args.timeout or contract["timeout"])
            record["execution"] = execution
            record["freshness"] = freshness_state(root, contract["freshness"])
            automated_status = "pass" if execution["exit"] == 0 else ("blocked" if execution["timed_out"] else "fail")
            rows = []
            for scenario in scenarios:
                if scenario["driver"] == "automated":
                    status, reason = automated_status, f"covered by `mise run verify` (exit {execution['exit']})"
                elif scenario["id"] in outcomes:
                    status, reason = outcomes[scenario["id"]]["status"], outcomes[scenario["id"]]["reason"] or "reported by the operator for this run"
                else:
                    status, reason = "not-run", "manual/interactive scenario was not exercised in this run"
                rows.append({**scenario, "status": status, "reason": reason})
            record["scenarios"] = rows
            if execution["timed_out"]:
                record["outcome"], record["blocked_reason"] = "blocked", f"`mise run verify` exceeded {args.timeout or contract['timeout']}s and was stopped"
            elif execution["exit"] != 0:
                record["outcome"] = "fail"
            elif record["freshness"] and not record["freshness"]["fresh"]:
                record["outcome"], record["blocked_reason"] = "fail", record["freshness"]["reason"]
            elif any(r["status"] == "fail" for r in rows):
                record["outcome"] = "fail"
            else:
                record["outcome"] = "pass"
            record["requires_root_review"] = bool(record["policy"]["changed"])
            record["certifies"] = head if (record["outcome"] == "pass" and not dirty and not record["requires_root_review"]) else None
            record["not_exercised"] = [r["id"] for r in rows if r["status"] in ("not-run", "blocked", "not-applicable")]
    except Blocked as exc:
        record["outcome"], record["blocked_reason"] = "blocked", str(exc)
    record["ended_at"] = utc_now()
    if root is not None and record.get("artifacts", {}).get("run_dir"):
        (root / record["artifacts"]["run_dir"] / "run.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
        latest = root / record["artifacts"]["dir"] / "latest.json"
        latest.write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    if args.json:
        print(json.dumps(record, indent=2))
    else:
        candidate = record.get("candidate", {})
        print(f"verify: {record['outcome']}" + (f" ({record['blocked_reason']})" if record["blocked_reason"] else ""))
        if candidate:
            print(f"candidate: {candidate.get('sha')} dirty={candidate.get('dirty')} provisional={record['provisional']} certifies={record['certifies']}")
        if record.get("scenarios") is not None and not args.check:
            counts = {}
            for row in record["scenarios"]:
                counts[row["status"]] = counts.get(row["status"], 0) + 1
            print("scenarios: " + ", ".join(f"{k}={v}" for k, v in sorted(counts.items())) if counts else "scenarios: none mapped")
        if record.get("requires_root_review"):
            print("policy files changed since base; this run cannot certify its own new standard: " + ", ".join(record["policy"]["changed"]))
        if record.get("artifacts", {}).get("run_dir"):
            print(f"record: {record['artifacts']['run_dir']}/run.json")
    return {"pass": 0, "checked": 0, "fail": 1, "blocked": 2}[record["outcome"]]


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit as exc:
        if isinstance(exc.code, str):
            print(exc.code, file=sys.stderr)
            sys.exit(3)
        raise
