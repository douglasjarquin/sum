#!/usr/bin/env python3
import json
import os
from pathlib import Path
import socket
import shutil
import subprocess
import sys
import termios
import time
import tty

if "--version" in sys.argv:
    print("codex-cli 0.166.0")
    raise SystemExit

output = Path(os.environ["SUM_CANARY_OUTPUT"])
output.mkdir(parents=True, exist_ok=True)
os.environ["PATH"] = os.pathsep.join((str(output.parent / "fixture-bin"), str(Path(sys.executable).parent), os.environ.get("PATH", "")))
role = os.environ["SUM_ROLE"]
address = os.environ["HC_TERMINAL_ID"]
helper = Path(os.environ["SUM_HOME"]).parent / "bin/sumctl"


def status(state):
    os.write(1, ("\x1b]7501;state=" + state + ":app=sum-canary-fixture\x1b\\").encode())


def record(tag, value):
    path = output / (address + "-" + tag + ".json")
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    os.replace(temporary, path)


def call(args):
    with (output / (address + "-helper-calls.jsonl")).open("a") as stream:
        stream.write(json.dumps({"args": args}) + "\n")
    inspection = None
    if args and args[0] == "review":
        if role != "reviewer":
            raise RuntimeError("candidate inspection requires the independent scripted reviewer")
        candidate = args[args.index("--candidate") + 1]
        observed = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
        note = subprocess.check_output(["git", "show", candidate + ":canary-note.txt"], text=True)
        if observed != candidate or note != "Application-local HC composition verified.\n":
            raise RuntimeError("reviewer inspection did not match the assigned candidate")
        inspection = {"candidate": candidate, "note": note, "read_only": True}
    result = subprocess.run([str(helper), "--format", "json", *args], text=True, capture_output=True, timeout=90)
    value = {"args": args, "code": result.returncode, "stdout": result.stdout, "stderr": result.stderr,
             "fixture_python": sys.executable, "resolved_python3": shutil.which("python3")}
    if inspection:
        value["inspection"] = inspection
    try:
        value["result"] = json.loads(result.stdout)
    except ValueError:
        pass
    return value


def foreground_ready():
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(2)
            connection.connect(os.environ["HC_SOCKET"])
            connection.sendall((json.dumps(dict(jsonrpc="2.0", id=1, method="terminal.get", params={"terminal": address})) + "\n").encode())
            with connection.makefile("rb") as stream:
                terminal = json.loads(stream.readline())["result"]
        foreground = terminal.get("foreground")
        if foreground and foreground["pid"] == os.getpid():
            return
        time.sleep(0.02)
    raise RuntimeError("fixture foreground was not observed")


def process(text):
    status("working")
    try:
        if text.startswith("SUM_CANARY_CMD:"):
            request = json.loads(text.split(":", 1)[1])
            if request.get("action") == "candidate":
                if role != "worker":
                    raise RuntimeError("only the scripted worker can author its candidate")
                Path("canary-note.txt").write_text("Application-local HC composition verified.\n")
                subprocess.run(["git", "add", "canary-note.txt"], check=True, capture_output=True)
                subprocess.run(["git", "commit", "-m", "Add canary note"], check=True, capture_output=True)
                candidate = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
                handoff = output / "handoff.json"
                verified = subprocess.run([sys.executable, ".agents/skills/verify/scripts/verify_run.py", "--json", "--base", os.environ["SUM_CANARY_BASE"]], text=True, capture_output=True, timeout=60)
                if verified.returncode:
                    raise RuntimeError("worker verification failed: " + verified.stdout[-4000:] + verified.stderr[-1000:])
                verification = json.loads(verified.stdout)
                if verification["outcome"] != "pass" or verification["certifies"] != candidate:
                    raise RuntimeError("worker verification did not certify its candidate")
                summary = {"run_id": verification["run_id"], "outcome": verification["outcome"],
                           "record": str(Path(verification["root"]) / verification["artifacts"]["run_dir"] / "run.json"),
                           "candidate": verification["candidate"]["sha"], "certifies": verification["certifies"],
                           "requires_root_review": verification["requires_root_review"],
                           "contract_sha256": verification["contract"]["sha256"], "policy_changed": verification["policy"]["changed"]}
                handoff.write_text(json.dumps({"outcome": "completed", "candidate": candidate, "files": ["canary-note.txt"], "verification": summary}))
                result = {"code": 0, "candidate": candidate, "handoff": str(handoff)}
            elif request.get("action") in ("hold-working", "ready"):
                if role != "worker":
                    raise RuntimeError("native readiness controls require the scripted worker")
                state = "working" if request["action"] == "hold-working" else "done"
                result = {"code": 0, "state": state, "helper_invoked": False}
                if state == "working":
                    record(request["tag"], result)
                    return
            elif request.get("action") == "exit":
                record(request["tag"], {"code": 0, "exited": True})
                raise SystemExit
            else:
                result = call(request["args"])
            record(request["tag"], result)
        elif not (output / (address + "-init.json")).exists():
            foreground_ready()
            record("init", call(["init"]))
        else:
            with (output / (address + "-notices.jsonl")).open("a") as stream:
                stream.write(json.dumps({"text": text}) + "\n")
    except Exception as error:
        record("error", {"error": str(error), "text": text})
        status("error")
        return
    status("done")


fd = sys.stdin.fileno()
previous = termios.tcgetattr(fd)
tty.setraw(fd)
os.write(1, b"\x1b[?2004h")
status("idle")
pending = bytearray()
try:
    while True:
        data = os.read(fd, 4096)
        if not data:
            break
        for byte in data:
            if byte == 9:
                text = bytes(pending).replace(b"\x1b[200~", b"").replace(b"\x1b[201~", b"").decode()
                pending.clear()
                process(text)
            else:
                pending.append(byte)
finally:
    termios.tcsetattr(fd, termios.TCSADRAIN, previous)
