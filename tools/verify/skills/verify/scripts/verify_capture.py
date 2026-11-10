#!/usr/bin/env python3
"""Capture evidence for one mapped feature while you drive it: the exact command or HTTP request, its observable result, and the time.

Evidence lands under the run directory (`<artifacts>/<run-id>/evidence/NNN-<feature>.json`), beside `run.json`, so a teardown that removes
what the run started never removes the proof. Nothing is started, stopped, or cleaned here; you drive the feature through its real
entrypoint and this records what happened. Exit 0 when the observed result met every expectation you stated, 1 otherwise, 2 on usage.

Usage:
  verify_capture.py --feature ID [--scenario ID] [--run-dir DIR] [--note TEXT] run [--expect-exit N] [--expect-text T]... -- COMMAND [ARGS...]
  verify_capture.py --feature ID [--scenario ID] [--run-dir DIR] [--note TEXT] http [--data JSON] [--expect-status N] [--expect-text T]... METHOD URL
--run-dir defaults to the run directory named by `<artifacts>/latest.json`, or a new `<artifacts>/manual-<stamp>/` when no run record exists yet.
Recipes are limited to what this file implements: a subprocess (CLI) and plain HTTP (service). A browser or desktop surface without an
installed driver stays a manual scenario; report it with the runner's `--scenario` instead of pretending this captured it.
"""
from __future__ import annotations

import argparse
import datetime as _dt
import json
import re
import secrets
import subprocess
import sys
import time
import tomllib
import urllib.error
import urllib.request
from pathlib import Path

LIMIT = 20000
FENCE = re.compile(r"^```verify[ \t]*\n(.*?)^```[ \t]*$", re.S | re.M)


def utc_now():
    return _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def root_and_artifacts():
    result = subprocess.run(["git", "rev-parse", "--show-toplevel"], text=True, capture_output=True)
    if result.returncode:
        raise SystemExit("not inside a Git work tree")
    root = Path(result.stdout.strip()).resolve()
    artifacts = ".artifacts/verification"
    contract = root / "VERIFY.md"
    if contract.is_file():
        match = FENCE.search(contract.read_text(encoding="utf-8"))
        if match:
            try:
                artifacts = tomllib.loads(match.group(1)).get("artifacts") or artifacts
            except tomllib.TOMLDecodeError:
                pass
    return root, root / artifacts


def choose_run_dir(root: Path, artifacts: Path, explicit):
    if explicit:
        return Path(explicit).resolve()
    latest = artifacts / "latest.json"
    if latest.is_file():
        try:
            run_dir = json.loads(latest.read_text(encoding="utf-8")).get("artifacts", {}).get("run_dir")
            if run_dir and (root / run_dir).is_dir():
                return (root / run_dir).resolve()
        except ValueError:
            pass
    return artifacts / f"manual-{_dt.datetime.now(_dt.timezone.utc):%Y%m%dT%H%M%SZ}-{secrets.token_hex(3)}"


def clip(text):
    return text if len(text) <= LIMIT else text[:LIMIT] + f"\n... [{len(text) - LIMIT} more characters not recorded]"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--feature", required=True, help="Feature or scenario ID from the feature maps")
    parser.add_argument("--scenario", help="Scenario ID when it differs from --feature")
    parser.add_argument("--run-dir", help="Run directory to write into (default: the newest run record's directory)")
    parser.add_argument("--note", help="What this capture is meant to show")
    sub = parser.add_subparsers(dest="recipe", required=True)
    run = sub.add_parser("run", help="Run a command and record stdout, stderr, and the exit code")
    run.add_argument("--expect-exit", type=int, default=0)
    run.add_argument("--expect-text", action="append", default=[], help="Text that must appear in stdout")
    run.add_argument("--timeout", type=int, default=120)
    run.add_argument("command", nargs=argparse.REMAINDER, help="-- COMMAND [ARGS...]")
    http = sub.add_parser("http", help="Send one HTTP request and record status and body")
    http.add_argument("--data", help="Request body (sent as JSON)")
    http.add_argument("--expect-status", type=int)
    http.add_argument("--expect-text", action="append", default=[], help="Text that must appear in the body")
    http.add_argument("--timeout", type=int, default=10)
    http.add_argument("method")
    http.add_argument("url")
    args = parser.parse_args(argv)
    root, artifacts = root_and_artifacts()
    run_dir = choose_run_dir(root, artifacts, args.run_dir)
    evidence_dir = run_dir / "evidence"
    evidence_dir.mkdir(parents=True, exist_ok=True)
    started = utc_now()
    clock = time.monotonic()
    record = {"schema": 1, "feature": args.feature, "scenario": args.scenario or args.feature, "note": args.note, "recipe": args.recipe, "started_at": started, "cwd": str(Path.cwd()),
              "expectations": [], "unmet": []}
    if args.recipe == "run":
        command = args.command[1:] if args.command[:1] == ["--"] else args.command
        if not command:
            parser.error("run needs -- COMMAND [ARGS...]")
        record["command"] = command
        record["expectations"] = [f"exit == {args.expect_exit}", *[f"stdout contains {t!r}" for t in args.expect_text]]
        try:
            proc = subprocess.run(command, text=True, capture_output=True, timeout=args.timeout)
            record.update(exit=proc.returncode, stdout=clip(proc.stdout), stderr=clip(proc.stderr), timed_out=False)
            if proc.returncode != args.expect_exit:
                record["unmet"].append(f"exit was {proc.returncode}, expected {args.expect_exit}")
            record["unmet"] += [f"stdout lacks {t!r}" for t in args.expect_text if t not in proc.stdout]
        except FileNotFoundError as exc:
            record.update(exit=None, error=str(exc), timed_out=False)
            record["unmet"].append(f"command not found: {command[0]}")
        except subprocess.TimeoutExpired:
            record.update(exit=None, timed_out=True)
            record["unmet"].append(f"timed out after {args.timeout}s")
    else:
        if not re.match(r"^https?://", args.url):
            parser.error(f"http needs an http(s):// URL, got {args.url!r}; read the URL the service printed, not the runner's echo line")
        body = args.data.encode() if args.data is not None else None
        request = urllib.request.Request(args.url, data=body, method=args.method.upper(), headers={"Content-Type": "application/json"} if body else {})
        record["request"] = {"method": args.method.upper(), "url": args.url, "data": args.data}
        record["expectations"] = [*([f"status == {args.expect_status}"] if args.expect_status else []), *[f"body contains {t!r}" for t in args.expect_text]]
        try:
            with urllib.request.urlopen(request, timeout=args.timeout) as response:
                status, text = response.status, response.read().decode("utf-8", "replace")
                record["headers"] = {k: v for k, v in response.headers.items() if k.lower() in ("content-type", "content-length", "location")}
        except urllib.error.HTTPError as exc:
            status, text = exc.code, exc.read().decode("utf-8", "replace")
        except (urllib.error.URLError, OSError) as exc:
            status, text = None, ""
            record["error"] = str(exc)
            record["unmet"].append(f"request failed: {exc}")
        record.update(status=status, body=clip(text))
        if args.expect_status is not None and status != args.expect_status:
            record["unmet"].append(f"status was {status}, expected {args.expect_status}")
        record["unmet"] += [f"body lacks {t!r}" for t in args.expect_text if t not in text]
    record.update(ended_at=utc_now(), seconds=round(time.monotonic() - clock, 3), met=not record["unmet"])
    index = len(list(evidence_dir.glob("*.json"))) + 1
    safe = re.sub(r"[^A-Za-z0-9._-]+", "-", record["scenario"])[:60]
    path = evidence_dir / f"{index:03d}-{safe}.json"
    path.write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    try:
        shown = path.relative_to(root)
    except ValueError:
        shown = path
    print(f"evidence: {shown} ({'met' if record['met'] else 'UNMET: ' + '; '.join(record['unmet'])})")
    return 0 if record["met"] else 1


if __name__ == "__main__":
    sys.exit(main())
