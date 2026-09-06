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
            # Bounded fleet lab: twelve prepared tasks (shell panes, no agent) in this real session; one snapshot serves the whole pass.
            fleet = sumctl("--home", str(state), "settings", "set", "--global", "13", "--per-repository", "1")
            assert fleet["capacity"] == {"global": 13, "per_repository": 1}, fleet
            for i in range(12):
                project = base / f"fleet-{i:02d}"
                project.mkdir()
                subprocess.run(["git", "-C", str(project), "init", "-b", "main"], check=True, capture_output=True, env=env)
                subprocess.run(["git", "-C", str(project), "-c", "user.name=sum smoke", "-c", "user.email=smoke@example.invalid", "commit", "--allow-empty", "-m", "fixture"], check=True, capture_output=True, env=env)
                sumctl("--home", str(state), "prepare", "--repo", str(project), "--brief", str(brief), "--harness", "codex", "--approved")
            started = time.monotonic()
            live = sumctl("--home", str(state), "status", "--live")
            wall = round((time.monotonic() - started) * 1000)
            assert len(live["tasks"]) == 13 and live["capacity"]["occupied"]["global"] == 13, live["capacity"]
            assert live["fanout"]["herdr_calls"] == 1, live["fanout"]  # One real `agent list`; no per-pane observation call.
            assert all(row.get("attention", "").startswith("Cannot observe worker") for row in live["tasks"]), [r.get("attention") for r in live["tasks"]]
            for row in live["tasks"]:  # A recorded decision per task gives the refresh a real revision to deliver.
                sumctl("--home", str(state), "ask", row["id"], "--key", "lab", "--text", "Lab question?")
            started = time.monotonic()
            refresh = sumctl("--home", str(state), "refresh", "request")
            refresh_wall = round((time.monotonic() - started) * 1000)
            rows = [r for r in refresh["targets"] if r["target"] == "task"]
            assert len(rows) == 13 and all(r["state"] == "pending-unreachable" for r in rows), refresh["counts"]
            assert refresh["fanout"]["herdr_calls"] == 1, refresh["fanout"]
            print(f"PASS: 13 prepared tasks in one real Herdr session; status --live took {wall} ms and refresh request {refresh_wall} ms on this host, "
                  f"each with exactly one Herdr observation call; no agent was started, so every worker row is honestly unreachable/pending.")
            # Native events lab (#14): link this lab state's plugin into the isolated Herdr registry, drive real status edges with
            # `pane report-agent`, and check that the real server ran the handler with the documented environment.
            hook = sumctl("--home", str(state), "hook", "enable")
            listed = cli("plugin", "list", "--plugin", hook["plugin_id"], "--json")["plugins"]
            assert len(listed) == 1 and listed[0]["enabled"] and listed[0]["manifest_path"] == hook["manifest"], listed
            assert listed[0].get("warnings", []) == [], listed[0]  # Every declared event name is known to the pinned build; 0.8.2 omits the field when empty.
            assert hook["fanout"]["herdr_calls"] == 1, hook["fanout"]
            marker = "SUM_EXCERPT_" + uuid.uuid4().hex[:8]
            cli("pane", "run", task["pane"], "printf '%s\\n' " + shlex.quote(marker))
            cli("pane", "wait-output", task["pane"], "--match", marker, "--timeout", "5000")
            record = json.loads((state / "tasks" / task["id"] / "task.json").read_text())
            record["status"] = "running"  # Lab only: a scripted occupant stands in for a launched harness so idle is a finished turn, not the launch handshake.
            (state / "tasks" / task["id"] / "task.json").write_text(json.dumps(record))
            health_path = state / "hook" / "health.json"
            def edge(status, expect_kind):
                before = json.loads(health_path.read_text()).get("events", 0)
                started = time.monotonic()
                cli("pane", "report-agent", task["pane"], "--source", "lab", "--agent", "claude", "--state", status)
                while time.monotonic() - started < 10:
                    health = json.loads(health_path.read_text())
                    if health.get("events", 0) > before and health["last_event"].get("pane") == task["pane"] and health["last_event"].get("status") == status:
                        break
                    time.sleep(0.02)
                else:
                    raise RuntimeError(f"handler did not record the {status} edge: {json.loads(health_path.read_text())}")
                wall = round((time.monotonic() - started) * 1000)
                last = health["last_event"]
                assert last["outcome"] == "handled" and last["event"] == "pane.agent_status_changed", last
                worker = [o for o in last["outcomes"] if o.get("task") == task["id"]][0]
                assert worker.get("kind") == expect_kind, worker
                return wall, last["handler_ms"], worker
            swept = [r for r in hook["reconciliation"]["attention"] if r["outcome"] == "recorded"]
            assert len(swept) == 13 and {r["kind"] for r in swept} == {"exited"}, swept  # Every waiting task's pane has no agent: recorded once each, from one snapshot, no prompt.
            wall_idle, handler_idle, worker = edge("idle", None)  # This task holds an open saved question: idle is expected and the question is preserved.
            saved = json.loads((state / "tasks" / task["id"] / "task.json").read_text())
            assert saved["questions"][0]["status"] == "open" and [a["kind"] for a in saved["attention"] if a["status"] == "open"] == ["exited"], saved["attention"]
            assert worker["action"] == "pump" and worker["prompts"] == 0, worker  # Nothing was typed into the scripted occupant.
            wall_blocked, handler_blocked, worker = edge("blocked", "blocked")
            saved = json.loads((state / "tasks" / task["id"] / "task.json").read_text())
            [attention] = [a for a in saved["attention"] if a["status"] == "open" and a["kind"] == "blocked"]
            assert marker in attention["excerpt"], attention
            assert "agent read" in attention["source"]["pointer"] and attention["source"]["session"] == name
            returns = sumctl("--home", str(state), "show", task["id"])["returns"]["open"]
            assert sorted(r["kind"] for r in returns) == ["attention", "attention", "question", "refresh"], [(r["kind"], r["notification"]["state"]) for r in returns]
            assert {r["notification"]["state"] for r in returns} <= {"not-delivered", "stalled"}, returns  # The root is a shell pane, the worker pane an unregistered scripted occupant: nothing was typed anywhere.
            wall_working, handler_working, worker = edge("working", None)
            assert worker["action"] == "resumed" and worker["closed"] == [attention["id"]], worker  # Resuming closes the blocked record; the exit record waits for the coordinator.
            health = json.loads(health_path.read_text())
            assert health["errors"] == [] and health["handled"] >= 3, health
            ignored_before = health["ignored"]
            stranger = cli("workspace", "create", "--cwd", str(base), "--label", "stranger", "--no-focus")["root_pane"]["pane_id"]
            cli("pane", "report-agent", stranger, "--source", "lab", "--agent", "claude", "--state", "idle")
            deadline = time.monotonic() + 10
            while json.loads(health_path.read_text())["ignored"] == ignored_before and time.monotonic() < deadline:
                time.sleep(0.02)
            health = json.loads(health_path.read_text())
            assert health["ignored"] >= ignored_before + 1 and health["last_event"]["outcome"] == "ignored", health["last_event"]  # Detection plus status edge: both ignored, neither touched a record.
            logs = cli("plugin", "log", "list", "--plugin", hook["plugin_id"], "--limit", "10")["logs"]
            assert logs and all(l["status"] == "succeeded" for l in logs), [(l["event"], l["status"], l["stderr"]) for l in logs]
            status = sumctl("--home", str(state), "hook", "status")
            assert status["enabled"] and status["registry"]["enabled"] and not status["degraded"], status
            disabled = sumctl("--home", str(state), "hook", "disable", "--unlink")
            assert disabled["action"] == "unlinked" and cli("plugin", "list", "--plugin", hook["plugin_id"], "--json")["plugins"] == []
            print(f"PASS: real Herdr 0.8.2 linked the lab plugin live, ran the handler for idle/blocked/working edges with the documented environment, "
                  f"recorded bounded attention with a real output excerpt, ignored an unrelated pane, and unlinked cleanly. "
                  f"Event-to-attention wall time on this host: idle {wall_idle} ms (handler {handler_idle} ms), blocked {wall_blocked} ms (handler {handler_blocked} ms), "
                  f"working {wall_working} ms (handler {handler_working} ms). No model was involved; a shell reported as an agent is not semantic question detection.")
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
