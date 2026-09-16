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
import stat
import subprocess
import time
import tomllib
from pathlib import Path
from typing import Any

SCHEMA = 1
CONTRACT_FILE = "VERIFY.md"
REQUIRED_HEADINGS = ("Setup", "Readiness", "Teardown", "Automated checks", "Scenarios", "Isolation", "Artifacts")
SCENARIO_STATUSES = ("pass", "fail", "blocked", "not-run", "not-applicable")
POLICY_FILES_DEFAULT = ("VERIFY.md", "mise.toml", ".mise.toml", "mise-tasks/", ".agents/skills/verify/", ".agents/skills/evidence/", ".agents/skills/create-verification/", ".agents/skills/maintain-verification/")
MAX_VERIFICATION_FILE_BYTES = 256 * 1024
FENCE = re.compile(r"^```verify[ \t]*\n(.*?)^```[ \t]*$", re.S | re.M)
LINK = re.compile(r"\]\(([^)\s]+\.md)\)")
ROW = re.compile(r"^\|\s*`?([A-Za-z0-9][A-Za-z0-9._:/-]{0,79})`?\s*\|(.*)\|\s*$")
EVIDENCE_REQUIRED = re.compile(r"\b(screenshot|screencast|red/green|before/after)\b", re.I)  # An Evidence cell naming visual proof needs a comparison manifest.


class Blocked(Exception):
    """The run cannot produce a trustworthy verdict; the reason is recorded, never converted into a pass."""


def safe_relative_path(value):
    return isinstance(value, str) and bool(value) and not Path(value).is_absolute() and "\\" not in value and not any(character in value for character in ":*?[]") and all(part != ".." for part in value.split("/"))


def read_bounded(path: Path):
    try:
        info = path.lstat()
    except OSError as exc:
        raise Blocked(f"verification file {path} is missing: {exc}")
    if path.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_size > MAX_VERIFICATION_FILE_BYTES:
        raise Blocked(f"verification file {path} exceeds {MAX_VERIFICATION_FILE_BYTES} bytes or is not regular")
    try:
        return path.read_bytes()
    except OSError as exc:
        raise Blocked(f"verification file {path} is unreadable: {exc}")


def utc_now():
    return _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def sha256_file(path: Path):
    return hashlib.sha256(read_bounded(path)).hexdigest()


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
    text = read_bounded(path).decode("utf-8")
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
        if not safe_relative_path(value):
            raise Blocked(f"{CONTRACT_FILE} `{key}` must be a relative path inside the repository, found {value!r}.")
    evidence = config.get("evidence", ".artifacts/evidence")
    if not safe_relative_path(evidence):
        raise Blocked(f"{CONTRACT_FILE} `evidence` must be a relative path inside the repository, found {evidence!r}.")
    owner = config.get("task_owner", ".")
    if not safe_relative_path(owner):
        raise Blocked(f"{CONTRACT_FILE} `task_owner` must be a relative directory inside the repository, found {owner!r}.")
    requires = config.get("requires", {})
    freshness = config.get("freshness", {})
    for name, value in (("requires", requires), ("freshness", freshness)):
        if not isinstance(value, dict):
            raise Blocked(f"{CONTRACT_FILE} `{name}` must be a table.")
    for key in ("commands",):
        if not isinstance(requires.get(key, []), list) or not all(isinstance(v, str) for v in requires.get(key, [])):
            raise Blocked(f"{CONTRACT_FILE} `requires.{key}` must be a list of strings.")
    for key in ("inputs", "outputs"):
        if not isinstance(freshness.get(key, []), list) or not all(safe_relative_path(v) for v in freshness.get(key, [])):
            raise Blocked(f"{CONTRACT_FILE} `freshness.{key}` must be a list of relative paths.")
    timeout = config.get("timeout_seconds", 3600)
    if not isinstance(timeout, int) or timeout <= 0:
        raise Blocked(f"{CONTRACT_FILE} `timeout_seconds` must be a positive integer.")
    return {"path": path, "sha256": sha256_file(path), "entrypoint": entrypoint, "feature_maps": config["feature_maps"], "artifacts": config["artifacts"], "evidence": evidence,
            "task_owner": owner, "requires": requires, "freshness": freshness, "timeout": timeout,
            "policy_files": policy_file_set(config.get("policy_files", []))}


def policy_file_set(extra):
    """The default policy files always count; a candidate's VERIFY.md may add paths but can never remove or shrink the set that governs it."""
    if not isinstance(extra, list) or not all(safe_relative_path(v) for v in extra):
        raise Blocked(f"{CONTRACT_FILE} `policy_files` must be a list of relative paths to add to the defaults.")
    return sorted(set(POLICY_FILES_DEFAULT) | set(extra))


def load_feature_maps(root: Path, index_relative: str):
    """The index links every feature map; each map lists scenarios as table rows `| id | ... | automated|manual ... |`."""
    index = root / index_relative
    try:
        index_text = read_bounded(index).decode("utf-8")
    except Blocked:
        raise Blocked(f"Feature-map index {index_relative} is missing.")
    maps = [{"path": index_relative, "sha256": sha256_file(index)}]
    scenarios = []
    seen = set()
    for link in LINK.findall(index_text):
        if link.startswith(("http://", "https://")):
            continue
        if not safe_relative_path(link):
            raise Blocked(f"Feature map link {link} in {index_relative} is not a safe repository path.")
        target = (index.parent / link).resolve()
        try:
            relative = target.relative_to(root)
        except ValueError:
            raise Blocked(f"Feature map link {link} in {index_relative} leaves the repository.")
        try:
            map_text = read_bounded(target).decode("utf-8")
        except Blocked:
            raise Blocked(f"Feature map {relative} linked from {index_relative} is missing.")
        maps.append({"path": str(relative), "sha256": sha256_file(target)})
        driver_column = None
        for line in map_text.splitlines():
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
            evidence_text = cells[driver_column + 1].strip() if driver_column + 1 < len(cells) else ""
            scenarios.append({"id": scenario_id, "map": str(relative), "feature": Path(relative).stem, "driver": "automated" if driver.lower().startswith("automated") else "manual",
                              "driver_text": driver[:200], "description": (cells[0] if cells else "")[:200],
                              "requires_evidence": bool(EVIDENCE_REQUIRED.search(evidence_text))})
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
    return {"name": name, "source": str(source_path), "owner": str(owner), "binary": binary}


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


def evidence_root(root: Path, evidence_relative: str):
    """evidence_capture.py honours VERIFY_EVIDENCE_ROOT, so the runner reads the same override or it looks where nothing was written."""
    override = os.environ.get("VERIFY_EVIDENCE_ROOT")
    if override:
        return Path(override).expanduser().resolve(), "VERIFY_EVIDENCE_ROOT"
    return root / evidence_relative, "contract"


def evidence_state(root: Path, evidence_relative: str, head: str, scenarios):
    """Comparison manifests written by .agents/skills/evidence for this candidate SHA, and the mapped scenarios that name visual evidence but have none.
    Missing evidence is reported, never invented and never turned into a pass for that scenario."""
    required = [s["id"] for s in scenarios if s.get("requires_evidence")]
    by_id = {s["id"]: s for s in scenarios}
    present = {}
    evidence_dir, root_source = evidence_root(root, evidence_relative)
    if evidence_dir.is_dir():
        for manifest in evidence_dir.glob("*/*/comparison.json"):
            try:
                comparison = json.loads(manifest.read_text(encoding="utf-8"))
            except ValueError:
                continue
            after_sha = (comparison.get("after") or {}).get("sha")
            declared = (comparison.get("candidate") or {}).get("sha")
            usable = comparison.get("verdict") not in (None, "mismatch", "capture-failed") and after_sha and head.startswith(after_sha[:12]) and (not declared or head.startswith(declared[:12]))
            if comparison.get("scenario") in required and usable:
                present.setdefault(comparison["scenario"], []).append({"path": manifest_path(manifest, root), "verdict": comparison.get("verdict"), "visual_proof": comparison.get("visual_proof")})
    missing = [i for i in required if i not in present]
    details = [{"scenario": i, "feature": by_id.get(i, {}).get("feature"), "map": by_id.get(i, {}).get("map")} for i in missing]
    return {"dir": evidence_relative, "root": str(evidence_dir), "root_source": root_source,
            "required": required, "present": present, "missing": missing, "missing_details": details}


def manifest_path(manifest: Path, root: Path):
    try:
        return str(manifest.relative_to(root))
    except ValueError:
        return str(manifest)


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


def run_entrypoint(root: Path, run_dir: Path, timeout: int, binary: str):
    log = run_dir / "verify.log"
    started = utc_now()
    clock = time.monotonic()
    with log.open("w", encoding="utf-8") as handle:
        try:
            proc = subprocess.run([binary, "run", "verify"], cwd=str(root), stdout=handle, stderr=subprocess.STDOUT, timeout=timeout,
                                  env={**os.environ, "VERIFY_RUN_DIR": str(run_dir)})
            exit_code, timed_out = proc.returncode, False
        except subprocess.TimeoutExpired:
            exit_code, timed_out = None, True
    return {"command": "mise run verify", "cwd": str(root), "started_at": started, "ended_at": utc_now(), "seconds": round(time.monotonic() - clock, 3),
            "exit": exit_code, "timed_out": timed_out, "log": str(log.relative_to(root))}


def main(argv=None):
    description = (__doc__ or "").splitlines()[0]
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument("--root", default=".", help="Any directory inside the repository; the Git top level is resolved from it")
    parser.add_argument("--check", action="store_true", help="Validate the contract, maps, task ownership, and requirements without running anything")
    parser.add_argument("--base", help="Commit/ref to compare policy files against; changes to VERIFY.md, maps, or tasks are flagged for root review")
    parser.add_argument("--scenario", action="append", metavar="ID=STATUS[:reason]", help="Outcome of a manual/interactive scenario you actually exercised")
    parser.add_argument("--timeout", type=int, help="Override the contract's timeout_seconds for the entrypoint")
    parser.add_argument("--json", action="store_true", help="Print the full run record instead of a summary")
    args = parser.parse_args(argv)
    outcomes = parse_scenario_args(args.scenario)
    run_id = f"{_dt.datetime.now(_dt.timezone.utc):%Y%m%dT%H%M%SZ}-{secrets.token_hex(4)}"
    record: dict[str, Any] = {"schema": SCHEMA, "run_id": run_id, "started_at": utc_now(), "runner": {"path": str(Path(__file__).resolve()), "mode": "check" if args.check else "run"},
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
        contract: dict[str, Any] = load_contract(root)
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
            execution = run_entrypoint(root, run_dir, args.timeout or contract["timeout"], record["task"]["binary"])
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
            record["evidence"] = evidence_state(root, contract["evidence"], head, scenarios)
            for row in rows:
                if row["id"] in record["evidence"]["missing"]:
                    row["reason"] += "; required visual evidence (screenshot/screencast comparison for this candidate) is missing"
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
            record["requires_root_review"] = bool(record["policy"]["changed"]) or not record["policy"]["checked"]  # Unchecked policy is unreviewed, never green.
            record["certifies"] = head if (record["outcome"] == "pass" and not dirty and record["policy"]["checked"] and not record["policy"]["changed"]) else None
            record["not_exercised"] = [r["id"] for r in rows if r["status"] in ("not-run", "blocked", "not-applicable")]
    except Blocked as exc:
        record["outcome"], record["blocked_reason"] = "blocked", str(exc)
    record["ended_at"] = utc_now()
    artifacts_record = record.get("artifacts")
    if root is not None and isinstance(artifacts_record, dict) and isinstance(artifacts_record.get("run_dir"), str):
        (root / artifacts_record["run_dir"] / "run.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
        latest = root / str(artifacts_record["dir"]) / "latest.json"
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
        policy_record = record.get("policy")
        if record.get("requires_root_review") and isinstance(policy_record, dict) and policy_record.get("checked"):
            changed = policy_record.get("changed", [])
            print("policy files changed since base; this run cannot certify its own new standard: " + ", ".join(str(path) for path in changed))
        elif record.get("requires_root_review"):
            print("policy not compared (no --base); the run cannot certify a SHA until VERIFY.md, tasks, and maps are reviewed against a base")
        evidence_record = record.get("evidence")
        if isinstance(evidence_record, dict) and evidence_record.get("missing"):
            missing = evidence_record["missing"]
            print("required evidence missing for: " + ", ".join(str(scenario) for scenario in missing) + f" (no comparison for this candidate under {evidence_record['dir']})")
        if isinstance(artifacts_record, dict) and artifacts_record.get("run_dir"):
            print(f"record: {artifacts_record['run_dir']}/run.json")
    return {"pass": 0, "checked": 0, "fail": 1, "blocked": 2}[record["outcome"]]


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit as exc:
        if isinstance(exc.code, str):
            print(exc.code, file=sys.stderr)
            sys.exit(3)
        raise
