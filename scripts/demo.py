#!/usr/bin/env python3
"""Credential-free integration demo: strict fake Herdr + real Git + real sum helpers."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def main():
    with tempfile.TemporaryDirectory(prefix="sum-demo-") as tmp:
        base = Path(tmp).resolve()  # macOS: /var is a symlink to /private/var.
        repo = base / "project"
        repo.mkdir()
        def git(*args, cwd=repo):
            return subprocess.run(["git", "-C", str(cwd), *args], check=True, text=True, capture_output=True).stdout.strip()
        git("init", "-b", "main")
        git("config", "user.name", "sum demo")
        git("config", "user.email", "demo@example.invalid")
        (repo / "README.md").write_text("A disposable demo project.\n")
        git("add", ".")
        git("commit", "-m", "Initial fixture")
        main_sha = git("rev-parse", "HEAD")
        brief = base / "brief.md"
        brief.write_text("Add greeting.py with greet(name) returning 'Hello, <name>!' and verify it. Ask whether to preserve punctuation. Do not publish.")
        env = {k: v for k, v in os.environ.items() if not k.startswith(("SUM_", "HERDR_"))}  # Inherited installation context never steers the lab.
        env.update(SUM_HERDR_BIN=str(ROOT / "tests/fixtures/herdr.py"),
                   FAKE_HERDR_ROOT=str(base / "fake"), FAKE_PARENT_CWD=str(ROOT),
                   HERDR_ENV="1", HERDR_PANE_ID="w-parent:p1", HERDR_SESSION="sum-test",
                   FAKE_PARENT_STATUS="working")
        def ctl(*args, pane=None, check=True):
            pane_env = dict(env, HERDR_PANE_ID=pane) if pane else env
            result = subprocess.run([sys.executable, str(ROOT / "lib/sumctl.py"), "--home", str(base / "state"), *args],
                                    env=pane_env, check=check, text=True, capture_output=True)
            return json.loads(result.stdout or result.stderr)
        (base / "state").mkdir()
        (base / "state/state.json").write_text('{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-09-05T00:00:00+00:00"}\n')
        doctor = ctl("doctor", check=False)
        assert not (base / "state/context.json").exists(), "doctor must not bind"
        assert ctl("init")["role"] == "coordinator"
        assert ctl("init")["role"] == "coordinator"
        second = ctl("init", pane="w-second:p1")
        assert second["role"] == "developer" and second["coordinator"]["pane"] == "w-parent:p1"
        assert ctl("init", "--role", "coordinator", pane="w-second:p1", check=False)["error"].startswith("Coordinator is owned by pane w-parent:p1")
        print("PASS: doctor observed without binding; first pane claimed coordinator once; a second unbriefed pane became a developer.")
        task = ctl("dispatch", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved")
        print("PASS: delegated through sum to a strict fake Herdr; real isolated Git worktree created.")
        worker = ctl("init", pane=task["pane"])
        assert worker["role"] == "worker" and worker["task"] == task["id"]
        refused = ctl("dispatch", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved", pane="w-second:p1", check=False)
        assert "not the registered coordinator" in refused["error"]
        print("PASS: the dispatched worker pane kept its task role; the developer pane could not dispatch.")
        q = ctl("ask", task["id"], "--key", "punctuation", "--text", "Keep the exclamation mark?")
        assert q["notice"]["status"] == "pending"
        assert ctl("inbox")["tasks"][0]["questions"][0]["text"] == "Keep the exclamation mark?"
        print("PASS: question remained visible while the coordinator was busy.")
        ctl("answer", task["id"], q["question"]["id"], "--text", "Yes, keep it.")
        ctl("resolve", task["id"], q["question"]["id"])
        worktree = Path(task["worktree"])
        (worktree / "greeting.py").write_text('def greet(name):\n    return f"Hello, {name}!"\n')
        subprocess.run([sys.executable, "-c", "from greeting import greet; assert greet('Doug') == 'Hello, Doug!'"], cwd=worktree, check=True)
        git("add", "greeting.py", cwd=worktree)
        git("commit", "-m", "Add greeting", cwd=worktree)
        candidate = git("rev-parse", "HEAD", cwd=worktree)
        ctl("report", task["id"], "--text", f"Scripted worker added greeting.py. Candidate {candidate}. Python assertion passed. No independent LLM review or PR performed.")
        assert git("rev-parse", "HEAD") == main_sha
        assert not (repo / "greeting.py").exists()
        backup = ctl("backup", str(base / "records.tar.gz"))
        assert backup["manifest"]["scope"] == "records-only"
        print("PASS: answer applied, real code committed on task branch, primary checkout unchanged.")
        print("PASS: worker report and records-only backup created; no merge, deletion, or external publication.")
        # Self-development: an isolated checkout of a (fixture) installation while the task records above stay in service.
        installation = base / "installation"
        installation.mkdir()
        git("init", "-b", "main", cwd=installation)
        git("config", "user.name", "sum demo", cwd=installation)
        git("config", "user.email", "demo@example.invalid", cwd=installation)
        (installation / "AGENTS.md").write_text("fixture installation\n")
        (installation / ".gitignore").write_text(".sum/\n")
        git("add", ".", cwd=installation)
        git("commit", "-m", "Installation fixture", cwd=installation)
        (installation / ".sum").mkdir(mode=0o700)
        (installation / ".sum/state.json").write_text('{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-09-05T00:00:00+00:00", "instance": "demo"}\n')
        records = {p: p.read_bytes() for p in (base / "state").rglob("*") if p.is_file()}
        dev = ctl("--home", str(installation / ".sum"), "dev", "prepare", "--name", "demo", "--pane")
        assert Path(dev["path"]) == installation / ".sum/dev/demo" and dev["branch"] == "sum-dev/demo" and dev["role"] == "developer"
        assert git("branch", "--show-current", cwd=installation) == "main" and git("status", "--porcelain", cwd=installation) == ""
        (Path(dev["path"]) / "candidate.py").write_text("candidate = True\n")
        again = ctl("--home", str(installation / ".sum"), "dev", "prepare", "--name", "demo")
        assert again["reopened"] and again["dirty"] and (Path(dev["path"]) / "candidate.py").exists()
        refused = ctl("--home", str(installation / ".sum"), "dev", "remove", "--name", "demo", check=False)
        assert "uncommitted or untracked" in refused["error"] and (Path(dev["path"]) / "candidate.py").exists()
        assert {p: p.read_bytes() for p in (base / "state").rglob("*") if p.is_file()} == records  # Task records and roles above are untouched.
        assert ctl("init", pane="w-late:p1")["role"] == "developer" and ctl("init")["role"] == "coordinator"
        print("PASS: development checkout prepared on its own branch with an ordinary pane, reopened with dirty work intact, never force-removed; installation branch and task records unchanged.")
        print("This demo uses NO actual model, Herdr binary, GitHub account, or provider credentials. Temporary fixture cleaned up.")

if __name__ == "__main__": main()
