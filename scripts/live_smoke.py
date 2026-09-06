#!/usr/bin/env python3
"""Opt-in real Herdr contract smoke test in an isolated HOME and named session.

Tests native worktree/layout/input APIs, not an authenticated coding harness.
Never attaches to, stops, or changes the user's default session.
"""
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]

def main():
    binary = ROOT / ".local/bin/herdr"
    if not binary.is_file():
        found = shutil.which("herdr")  # A development checkout may reuse mise's read-only tool install without running setup.
        if not found:
            raise RuntimeError("Run mise run setup first, or make the pinned herdr available on PATH.")
        binary = Path(found)
    version = subprocess.check_output([str(binary), "--version"], text=True)
    if "0.8.2" not in version:
        raise RuntimeError(f"Expected pinned Herdr 0.8.2, got {version.strip()}")
    name = "sum-test-" + uuid.uuid4().hex[:10]
    assert name.startswith("sum-test-") and name != "default"
    # Herdr's socket lives under XDG_CONFIG_HOME; macOS caps sun_path at 104 bytes, so the lab needs a short root, not $TMPDIR.
    with tempfile.TemporaryDirectory(prefix="sum-lab-", dir="/tmp") as tmp:
        base = Path(tmp).resolve()
        # Sanitize every inherited sum/Herdr/MCP variable so the lab session and state are the only destinations.
        env = {k: v for k, v in os.environ.items() if not k.startswith(("SUM_", "HERDR_"))}
        env.update(HOME=str(base / "home"), XDG_CONFIG_HOME=str(base / "config"), HERDR_SESSION=name, SHELL="/bin/bash")
        Path(env["HOME"]).mkdir()
        Path(env["XDG_CONFIG_HOME"]).mkdir()
        def cli(*args, timeout=10):
            result = subprocess.run([str(binary), "--session", name, *args], env=env, check=True,
                                    text=True, capture_output=True, timeout=timeout)
            return json.loads(result.stdout).get("result") if result.stdout.strip() else None  # `pane run` prints nothing.
        repo = base / "repo"
        repo.mkdir()
        def git(*args):
            subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, env=env)
        git("init", "-b", "main")
        git("-c", "user.name=sum smoke", "-c", "user.email=smoke@example.invalid", "commit", "--allow-empty", "-m", "fixture")
        log = (base / "server.log").open("w")
        server = subprocess.Popen([str(binary), "--session", name, "server"], env=env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 15
            while True:
                try:
                    cli("workspace", "list")
                    break
                except (subprocess.SubprocessError, ValueError):
                    if server.poll() is not None or time.monotonic() > deadline:
                        log.flush()
                        raise RuntimeError((base / "server.log").read_text()[-3000:])
                    time.sleep(0.2)
            parent = cli("workspace", "create", "--cwd", str(ROOT), "--label", "sum-smoke-parent", "--no-focus")
            env.update(HERDR_ENV="1", HERDR_PANE_ID=parent["root_pane"]["pane_id"], SUM_HERDR_BIN=str(binary))
            brief = base / "brief.md"
            brief.write_text("Smoke test only. Do not launch any model or publish anything.")
            def sumctl(*args):
                result = subprocess.run([sys.executable, str(ROOT / "lib/sumctl.py"), *args], env=env, text=True, capture_output=True)
                if result.returncode:
                    raise RuntimeError(f"sumctl {' '.join(args)} failed: {result.stderr.strip()[-1500:]}")
                return json.loads(result.stdout)
            state = base / "state"
            state.mkdir(mode=0o700)
            (state / "state.json").write_text('{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-09-05T00:00:00+00:00"}\n')
            assert sumctl("--home", str(state), "init")["role"] == "coordinator"  # Lab state only; the installation's records are untouched.
            task = sumctl("--home", str(state), "prepare", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved")
            marker = "SUM_SMOKE_" + uuid.uuid4().hex
            cli("pane", "run", task["pane"], "printf '%s\\n' " + shlex.quote(marker))
            cli("pane", "wait-output", task["pane"], "--match", marker, "--timeout", "5000")
            assert Path(task["worktree"]).resolve() != repo.resolve()
            print("PASS: real Herdr 0.8.2 worktree response, pane IDs, command input, and bounded output wait.")
            installation = base / "installation"
            installation.mkdir()
            subprocess.run(["git", "-C", str(installation), "init", "-b", "main"], check=True, capture_output=True, env=env)
            (installation / "AGENTS.md").write_text("fixture\n")
            subprocess.run(["git", "-C", str(installation), "add", "."], check=True, capture_output=True, env=env)
            subprocess.run(["git", "-C", str(installation), "-c", "user.name=sum smoke", "-c", "user.email=smoke@example.invalid", "commit", "-m", "fixture"], check=True, capture_output=True, env=env)
            (installation / ".sum").mkdir(mode=0o700)
            (installation / ".sum/state.json").write_text('{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-09-05T00:00:00+00:00"}\n')
            dev = sumctl("--home", str(installation / ".sum"), "dev", "prepare", "--name", "smoke", "--pane")
            pane = cli("pane", "get", dev["pane"]["pane"])
            pane = pane.get("pane", pane)
            cwd = pane.get("cwd") or pane.get("working_directory")
            assert cwd and Path(cwd).resolve() == Path(dev["path"]).resolve(), pane
            print("PASS: real Herdr workspace opened with its root pane in the development checkout; no agent started.")
            print("No coding harness was launched; this is not a real-model acceptance test.")
        finally:
            try:
                cli("server", "stop", timeout=5)
            except Exception:
                server.terminate()  # Only the exact process created by this test.
            try: server.wait(timeout=5)
            except subprocess.TimeoutExpired:
                server.kill(); server.wait()
            log.close()

if __name__ == "__main__":
    try: main()
    except Exception as exc:
        print(f"live smoke failed: {exc}", file=sys.stderr)
        sys.exit(1)
