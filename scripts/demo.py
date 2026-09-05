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
        base = Path(tmp)
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
        env = os.environ.copy()
        env.update(SUM_HERDR_BIN=str(ROOT / "tests/fixtures/herdr.py"),
                   FAKE_HERDR_ROOT=str(base / "fake"), FAKE_PARENT_CWD=str(ROOT),
                   HERDR_ENV="1", HERDR_PANE_ID="w-parent:p1", HERDR_SESSION="sum-test",
                   FAKE_PARENT_STATUS="working")
        def ctl(*args):
            result = subprocess.run([sys.executable, str(ROOT / "lib/sumctl.py"), "--home", str(base / "state"), *args],
                                    env=env, check=True, text=True, capture_output=True)
            return json.loads(result.stdout)
        task = ctl("dispatch", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved")
        print("PASS: delegated through sum to a strict fake Herdr; real isolated Git worktree created.")
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
        print("This demo uses NO actual model, Herdr binary, GitHub account, or provider credentials. Temporary fixture cleaned up.")

if __name__ == "__main__": main()
