#!/usr/bin/env python3
"""Credential-free integration demo: strict fake Herdr + real Git + real sum helpers."""
import json
import os
from pathlib import Path
import shutil
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
        # Versioned briefs: regenerate from the record (no model call), stage beside the brief the worker read, never overwrite it.
        original_brief = Path(task["brief_path"]).read_bytes()
        staged = ctl("brief", "regenerate", task["id"])
        assert not staged["duplicate"] and staged["revision"]["id"] == "r2" and staged["active"] == "r1"
        assert staged["revision"]["summary"] == [f"decision {q['question']['id']} recorded (applied)"]
        assert ctl("brief", "regenerate", task["id"])["duplicate"]
        assert Path(task["brief_path"]).read_bytes() == original_brief and ctl("show", task["id"])["brief"] == brief.read_text()
        assert "Yes, keep it." in Path(staged["revision"]["path"]).read_text()
        assert ctl("brief", "request", task["id"], "r2")["requested"] == "r2"
        assert ctl("show", task["id"])["notice"]["reason"] == "a worker report is available"  # Refresh bookkeeping left the notice slot alone.
        assert ctl("brief", "adopt", task["id"], "r2", pane=task["pane"])["active"] == "r2"
        assert ctl("show", task["id"])["report"]["brief_revision"] == "r1"
        backup = ctl("backup", str(base / "records.tar.gz"))
        assert backup["manifest"]["scope"] == "records-only" and backup["manifest"]["brief_revisions_included"]
        print("PASS: answer applied, real code committed on task branch, primary checkout unchanged.")
        print("PASS: brief revision r2 staged from the record with a machine-generated summary; the worker's original brief and the approved task stayed byte-identical; refresh was explicit.")
        print("PASS: worker report and records-only backup (with every brief revision) created; no merge, deletion, or external publication.")
        # Self-development: an isolated checkout of a (fixture) installation while the task records above stay in service.
        installation = base / "installation"
        installation.mkdir()
        git("init", "-b", "main", cwd=installation)
        git("config", "user.name", "sum demo", cwd=installation)
        git("config", "user.email", "demo@example.invalid", cwd=installation)
        listing = subprocess.run(["git", "-C", str(ROOT), "ls-files", "-z"], capture_output=True, check=True).stdout
        for relative in filter(None, listing.decode().split("\0")):  # The fixture installation holds this checkout's committed sum sources.
            (installation / relative).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, installation / relative, follow_symlinks=False)
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
        # Immutable runtime release: staged beside the live installation with an offline stand-in for dependency installation.
        head = git("rev-parse", "HEAD", cwd=installation)
        records = {p: p.read_bytes() for p in (base / "state").rglob("*") if p.is_file()}
        stage_script = ("import importlib.util, sys\n"
                        f"spec = importlib.util.spec_from_file_location('sumctl', {str(ROOT / 'lib/sumctl.py')!r}); sumctl = importlib.util.module_from_spec(spec); spec.loader.exec_module(sumctl)\n"
                        f"tspec = importlib.util.spec_from_file_location('test_core', {str(ROOT / 'tests/test_core.py')!r}); tests = importlib.util.module_from_spec(tspec); tspec.loader.exec_module(tests)\n"
                        f"print(json.dumps(sumctl.stage(sumctl.Store({str(installation / '.sum')!r}), 'HEAD', installer=tests.fake_installer)))".replace("json.dumps", "__import__('json').dumps"))
        staged = json.loads(subprocess.run([sys.executable, "-c", stage_script], env=env, check=True, text=True, capture_output=True).stdout)
        release = Path(staged["release"])
        assert staged["staged"] and not staged["activated"] and release == installation / ".local/releases" / head
        assert not (release / ".sum").exists() and staged["manifest"]["source"]["sha"] == head
        assert not (installation / ".deps").exists()  # The installation's own runtime and state stayed as they were.
        assert ctl("--home", str(installation / ".sum"), "release", "list")["releases"] == [{"sha": head, "path": str(release), "ok": True, "sum_version": "0.1.0", "staged_at": staged["manifest"]["staged_at"]}]
        direct = subprocess.run([str(release / "bin/sumctl"), "--home", str(base / "state"), "status"], env=env, text=True, capture_output=True)
        assert direct.returncode == 1 and "immutable release tree" in direct.stderr  # A release never owns state.
        assert subprocess.run([str(installation / "bin/sumctl"), "--home", str(base / "state"), "status"], env=env, text=True, capture_output=True).returncode == 0
        assert {p: p.read_bytes() for p in (base / "state").rglob("*") if p.is_file()} == records
        print("PASS: immutable release staged under .local/releases/<sha> with a validated manifest; nothing activated, the release owns no state, the installation entrypoint still serves old callbacks.")
        # Atomic update and code-only rollback: a bare repository stands in for origin; the staged bundle above is the merged candidate.
        origin = base / "origin.git"
        subprocess.run(["git", "init", "-q", "--bare", "-b", "main", str(origin)], check=True)
        git("remote", "add", "origin", str(origin), cwd=installation)
        git("push", "-q", "-u", "origin", "main", cwd=installation)
        git("remote", "set-head", "origin", "main", cwd=installation)
        inst = str(installation / ".sum")
        assert ctl("--home", inst, "init")["role"] == "coordinator"
        check = ctl("--home", inst, "update", "check")
        assert check["source"]["sha"] == head and check["default"]["kind"] == "checkout" and check["staged"] and check["compatibility"]["ok"]
        applied = ctl("--home", inst, "update", "apply", "--no-fetch")
        assert applied["changed"] and applied["previous"]["kind"] == "checkout" and applied["default"]["sha"] == head and applied["post_check"]["ok"]
        assert Path(os.readlink(installation / ".local/current")) == Path("releases") / head
        served = json.loads(subprocess.run([str(installation / "bin/sumctl"), "--home", str(base / "state"), "doctor"], env=env, text=True, capture_output=True).stdout)
        assert served["runtime"] == str(release) and served["installation"] == str(installation)  # New entrypoint calls run the new default.
        ctl("ask", task["id"], "--key", "after-update", "--text", "Saved while the new default serves?")  # Old absolute callback, same records.
        assert git("status", "--porcelain", cwd=installation) == "" and git("rev-parse", "HEAD", cwd=installation) == head  # Checkout untouched.
        refused = ctl("--home", inst, "update", "apply", "--no-fetch", pane="w-second:p1", check=False)
        assert "not the registered coordinator" in refused["error"]
        rolled = ctl("--home", inst, "update", "rollback")
        assert rolled["changed"] and rolled["default"]["kind"] == "checkout" and not (installation / ".local/current").exists()
        assert [q["key"] for q in ctl("show", task["id"])["questions"]] == ["punctuation", "after-update"]  # Rollback kept the new question.
        assert [e["result"] for e in ctl("--home", inst, "update", "status")["history"] if "result" in e] == ["selected", "selected"]
        assert ctl("--home", inst, "update", "rollback", "--to", head[:12])["default"]["sha"] == head
        print("PASS: merged revision resolved from origin, validated against the live records, and selected with one symlink rename; the checkout stayed untouched, old callbacks kept working, a developer pane was refused, and rollback changed only the code selection.")
        # Rolling refresh: the coordinator contract and the running worker get their next immutable revision and one bounded delivery each.
        fake_state = base / "fake/state.json"
        panes = json.loads(fake_state.read_text())
        panes["panes"][task["pane"]] = {"pane_id": task["pane"], "cwd": task["worktree"], "agent": "codex", "agent_status": "working"}  # The fake rewrote this pane when it acted as the caller above.
        fake_state.write_text(json.dumps(panes))
        refresh = ctl("refresh", "request")
        rows = {r["target"]: r for r in refresh["targets"]}
        assert rows["task"]["state"] == "pending-busy" and rows["task"]["revision"] == "r3", rows  # A busy worker keeps its current brief; the request is persisted.
        assert rows["coordinator"]["state"] == "pending-busy" and rows["coordinator"]["revision"] == "r1"  # The coordinator is its own target.
        panes["panes"][task["pane"]]["agent_status"] = "idle"
        fake_state.write_text(json.dumps(panes))
        refresh = ctl("refresh", "request", "--task", task["id"])
        [row] = refresh["targets"]
        assert row["state"] == "submitted-unconfirmed" and row["revision"] == "r3"
        instruction = json.loads(fake_state.read_text())["panes"][task["pane"]]["last_prompt"]
        assert instruction.startswith(f"sum refresh {task['id']}: brief revision r3 is requested") and "Saved while" not in instruction
        assert ctl("show", task["id"])["notice"]["reason"] == "a decision is waiting"  # The notice slot still holds the after-update question; refresh never used it.
        assert ctl("refresh", "status")["counts"]["submitted-unconfirmed"] == 1
        assert ctl("brief", "adopt", task["id"], "r3", pane=task["pane"])["active"] == "r3"
        assert ctl("refresh", "adopt", "--coordinator", "r1")["active"] == "r1"
        assert ctl("refresh", "status")["counts"]["confirmed"] == 2
        print("PASS: rolling refresh staged r3 and a coordinator contract snapshot, left the busy worker on its brief, delivered one fixed instruction once it was idle, and confirmed both only through explicit receipts.")
        print("This demo uses NO actual model, Herdr binary, GitHub account, or provider credentials. Temporary fixture cleaned up.")

if __name__ == "__main__": main()
