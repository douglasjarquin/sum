#!/usr/bin/env python3
"""Opt-in real Herdr lab for pane incarnation (#207) in an isolated HOME and a uniquely named session.

It checks, against the pinned Herdr, the identity fields sum records and the outcomes it derives from them: the same
occupant across repeated inits, a live handoff (new terminal_id, same shell: handoff), and a restart of this lab's own
server (same pane ID, new terminal_id and shell: replaced, then a deliberate reclaim). It never attaches to, stops, or
sends input to any session but the one it creates, and it stops only the server it started. No coding harness runs, so
the native-session `restored` outcome is not exercised here.
"""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    binary = ROOT / ".local/bin/herdr"
    if not binary.is_file():
        found = shutil.which("herdr")
        if not found:
            raise RuntimeError("Run mise run setup first, or make the pinned herdr available on PATH.")
        binary = Path(found)
    version = subprocess.check_output([str(binary), "--version"], text=True)
    if "0.9.0" not in version:
        raise RuntimeError(f"Expected pinned Herdr 0.9.0, got {version.strip()}")
    name = "sum-test-207-" + uuid.uuid4().hex[:8]
    assert name.startswith("sum-test-") and name != "default"
    # Herdr's socket lives under XDG_CONFIG_HOME; macOS caps sun_path at 104 bytes, so the lab needs a short root.
    with tempfile.TemporaryDirectory(prefix="sum-lab-", dir="/tmp") as tmp:
        base = Path(tmp).resolve()
        env = {k: v for k, v in os.environ.items() if not k.startswith(("SUM_", "HERDR_"))}
        env.update(HOME=str(base / "home"), XDG_CONFIG_HOME=str(base / "config"), HERDR_SESSION=name, SHELL="/bin/bash")
        Path(env["HOME"]).mkdir()
        Path(env["XDG_CONFIG_HOME"]).mkdir()
        sock = Path(env["XDG_CONFIG_HOME"]) / "herdr" / "sessions" / name / "herdr.sock"  # This lab's socket only.

        def cli(*args, timeout=10):
            result = subprocess.run([str(binary), "--session", name, *args], env=env, check=True, text=True, capture_output=True, timeout=timeout)
            return json.loads(result.stdout).get("result") if result.stdout.strip() else None

        servers = []

        def start_server():
            log = (base / f"server-{len(servers)}.log").open("w")
            proc = subprocess.Popen([str(binary), "--session", name, "server"], env=env, stdout=log, stderr=log)
            servers.append((proc, log))
            deadline = time.monotonic() + 15
            while True:
                try:
                    cli("workspace", "list")
                    return
                except (subprocess.SubprocessError, ValueError):
                    if proc.poll() is not None or time.monotonic() > deadline:
                        log.flush()
                        raise RuntimeError(Path(log.name).read_text()[-3000:])
                    time.sleep(0.2)

        def pane_now(pane):
            deadline = time.monotonic() + 15
            while True:
                try:
                    return cli("pane", "get", pane)["pane"]
                except (subprocess.SubprocessError, ValueError, TypeError):
                    if time.monotonic() > deadline:
                        raise
                    time.sleep(0.2)

        start_server()
        try:
            root = cli("workspace", "create", "--cwd", str(ROOT), "--label", "sum-incarnation", "--no-focus")["root_pane"]["pane_id"]
            env.update(HERDR_ENV="1", HERDR_PANE_ID=root, SUM_HERDR_BIN=str(binary))
            state = base / "state"
            state.mkdir(mode=0o700)
            (state / "state.json").write_text('{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-09-05T00:00:00+00:00"}\n')

            def init(*args):
                result = subprocess.run([str(ROOT / "bin/sumctl"), "--format", "json", "--home", str(state), "init", *args], env=env, text=True, capture_output=True)
                if result.returncode:
                    raise RuntimeError(f"sumctl init {' '.join(args)} failed: {result.stderr.strip()[-1500:]}")
                view = json.loads(result.stdout)
                return view["role"], (view.get("incarnation") or {}).get("outcome"), view

            first = pane_now(root)
            role, _, view = init()
            assert role == "coordinator", view
            recorded = json.loads((state / "context.json").read_text())["incarnation"]
            assert recorded["terminal"] == first["terminal_id"] and recorded["shell"]["pid"] > 0, recorded
            assert init()[:2] == ("coordinator", "same")
            print(f"PASS: real Herdr 0.9.0 reports terminal_id {first['terminal_id']} and shell pid {recorded['shell']['pid']} for {root}; "
                  "repeated init is the same occupant.")

            assert name.startswith("sum-test-") and sock.is_socket(), sock
            conn = socket.socket(socket.AF_UNIX)
            conn.settimeout(20)
            conn.connect(str(sock))
            conn.sendall((json.dumps({"id": "handoff", "method": "server.live_handoff", "params": {"import_exe": str(binary)}}) + "\n").encode())
            reply = conn.recv(65536).decode()
            conn.close()
            assert '"ok"' in reply, reply
            time.sleep(1)
            handed = pane_now(root)
            assert handed["pane_id"] == root and handed["terminal_id"] != first["terminal_id"], (first, handed)
            role, outcome, view = init()
            assert (role, outcome) == ("coordinator", "handoff"), view["incarnation"]
            print(f"PASS: a live handoff kept pane {root} and its shell process and issued terminal_id {handed['terminal_id']}: judged handoff, still the coordinator.")

            cli("server", "stop", timeout=10)
            for proc, _ in servers:
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    pass  # The handed-off server is another process; `server stop` above ended it.
            start_server()
            restored = pane_now(root)
            assert restored["pane_id"] == root and restored["terminal_id"] not in (first["terminal_id"], handed["terminal_id"]), restored
            role, outcome, view = init()
            assert (role, outcome) == ("developer", "replaced"), view["incarnation"]
            assert json.loads((state / "context.json").read_text())["incarnation"]["terminal"] == handed["terminal_id"], "a refused init changed the owner"
            role, outcome, view = init("--role", "coordinator", "--reclaim")
            owner = json.loads((state / "context.json").read_text())
            assert role == "coordinator" and owner["previous_observed"] == "replaced" and owner["incarnation"]["terminal"] == restored["terminal_id"], owner
            assert init()[:2] == ("coordinator", "same")
            print(f"PASS: after a restart of this lab server, pane {root} came back with terminal_id {restored['terminal_id']} and a new shell: "
                  "judged replaced, no inherited coordinator role, and the deliberate reclaim rebound it.")
            print("Not exercised: restored (no authenticated harness reports a native agent session in this lab).")
        finally:
            try:
                cli("server", "stop", timeout=5)
            except Exception:
                pass
            for proc, log in servers:
                if proc.poll() is None:
                    proc.terminate()  # Only processes this lab started.
                    try:
                        proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()
                log.close()


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"live incarnation lab failed: {exc!r}", file=sys.stderr)
        sys.exit(1)
