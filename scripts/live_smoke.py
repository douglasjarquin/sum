#!/usr/bin/env python3
"""Opt-in real Herdr contract smoke test in an isolated HOME and named session.

Tests native worktree/layout/input APIs, not an authenticated coding harness.
Never attaches to, stops, or changes the user's default session.
"""
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]

def main():
    binary = ROOT / ".local/bin/herdr"
    if not binary.is_file():
        raise RuntimeError("Run mise run setup first.")
    version = subprocess.check_output([str(binary), "--version"], text=True)
    if "0.8.2" not in version:
        raise RuntimeError(f"Expected pinned Herdr 0.8.2, got {version.strip()}")
    name = "sum-test-" + uuid.uuid4().hex[:10]
    assert name.startswith("sum-test-") and name != "default"
    with tempfile.TemporaryDirectory(prefix=name + "-") as tmp:
        base = Path(tmp)
        env = os.environ.copy()
        for key in ("HERDR_SOCKET_PATH", "HERDR_PANE_ID", "HERDR_ENV", "SUM_SESSION", "SUM_HOME"):
            env.pop(key, None)
        env.update(HOME=str(base / "home"), XDG_CONFIG_HOME=str(base / "config"), HERDR_SESSION=name, SHELL="/bin/bash")
        Path(env["HOME"]).mkdir()
        Path(env["XDG_CONFIG_HOME"]).mkdir()
        def cli(*args, timeout=10):
            result = subprocess.run([str(binary), "--session", name, *args], env=env, check=True,
                                    text=True, capture_output=True, timeout=timeout)
            return json.loads(result.stdout).get("result")
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
            result = subprocess.run([sys.executable, str(ROOT / "lib/sumctl.py"), "--home", str(base / "state"),
                                    "prepare", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved"],
                                    env=env, text=True, capture_output=True, check=True)
            task = json.loads(result.stdout)
            marker = "SUM_SMOKE_" + uuid.uuid4().hex
            cli("pane", "run", task["pane"], "printf '%s\\n' " + shlex.quote(marker))
            cli("pane", "wait-output", task["pane"], marker, "--timeout", "5000")
            assert Path(task["worktree"]).resolve() != repo.resolve()
            print("PASS: real Herdr 0.8.2 worktree response, pane IDs, command input, and bounded output wait.")
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
