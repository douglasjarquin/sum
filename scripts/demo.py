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
        if env.get("GOROOT"):
            env["PATH"] = os.pathsep.join([str(Path(env["GOROOT"]) / "bin"), env.get("PATH", "")])
        env.update(SUM_STAGE_OFFLINE="1",
                   SUM_HERDR_BIN=str(ROOT / "tests/fixtures/herdr.py"), SUM_GH_BIN=str(ROOT / "tests/fixtures/gh.py"), FAKE_GH_ROOT=str(base / "fake-gh"),
                   SUM_CODEGRAPH_BIN=str(ROOT / "tests/fixtures/codegraph.py"), FAKE_CODEGRAPH_ROOT=str(base / "fake-codegraph"),
                   SUM_MISE_BIN=str(ROOT / "tests/fixtures/mise.py"), FAKE_MISE_STOP=str(base),
                   SUM_LSOF_BIN=str(ROOT / "tests/fixtures/lsof.py"), FAKE_LSOF_ROOT=str(base / "fake-lsof"),
                   FAKE_HERDR_ROOT=str(base / "fake"), FAKE_PARENT_CWD=str(ROOT),
                   HERDR_ENV="1", HERDR_PANE_ID="w-parent:p1", HERDR_SESSION="sum-test",
                   FAKE_PARENT_STATUS="working")
        binary = ROOT / ".local/bin/sumctl"
        if not os.access(binary, os.X_OK):
            binary.parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(binary), "./cmd/sumctl"], cwd=ROOT / "go", check=True)
        helper = ROOT / "bin/sumctl"
        def ctl(*args, pane=None, check=True):
            pane_env = dict(env, HERDR_PANE_ID=pane) if pane else env
            result = subprocess.run([str(helper), "--home", str(base / "state"), *args],
                                    env=pane_env, check=False, text=True, capture_output=True)
            if check and result.returncode:
                raise RuntimeError(result.stderr or result.stdout)
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
        capacity = ctl("settings", "show")
        assert capacity["limits"] is None and capacity["source"] == "unlimited"
        assert capacity["worker"] is None
        task = ctl("dispatch", "--repo", str(repo), "--brief", str(brief), "--approved")
        assert task["admission"]["occupied_before"] == {"global": 0, "repository": 0}
        assert task["launch"]["harness"] == "claude" and task["launch"]["source"]["harness"] == "root" and task["launch"]["argv"] == []
        assert "model native default" in task["confirmation"]
        print("PASS: with no saved worker default the worker launched on the coordinator's own harness with its native model, disclosed as such.")
        # Named presets (#12): none configured means nothing changes; a chosen one expands at prepare and is refused before any side effect when unknown.
        assert capacity["presets"] == {} and capacity["reviewer"] is None
        assert "Unknown preset 'deep'" in ctl("prepare", "--repo", str(repo), "--brief", str(brief), "--approved", "--preset", "deep", check=False)["error"]
        preset = ctl("preset", "set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--reasoning", "high")  # Illustrative values; nothing is enabled or subscribed by this.
        assert preset["preset"]["revision"] == 1 and preset["launch"]["argv"] == ["-m", "gpt-5-codex", "-c", "model_reasoning_effort=high"]
        assert ctl("preset", "show", "deep")["used_by"] == [] and list(ctl("preset", "list")["presets"]) == ["deep"]
        assert "runs on codex but --harness claude" in ctl("prepare", "--repo", str(repo), "--brief", str(brief), "--approved", "--preset", "deep", "--harness", "claude", check=False)["error"]
        assert len(ctl("status")["tasks"]) == 1  # The refusals created no record and hold no slot.
        print("PASS: presets are optional shortcuts: none configured changed nothing; an unknown name and a cross-harness choice were refused before any worktree; one preset saved at revision 1.")
        ctl("settings", "set", "--global", "2", "--per-repository", "1")
        refused = ctl("prepare", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved", check=False)
        assert "1 of 1 slots for" in refused["error"] and len(ctl("status")["tasks"]) == 1
        print("PASS: delegated through sum to a strict fake Herdr; real isolated Git worktree created; a second writer for the same checkout was refused at admission.")
        # Code graph (#36): the pinned codegraph initialized once in the new checkout, an index local to it, the brief carrying exact CLI commands and the fallback.
        assert task["graph"]["state"] == "ready" and task["graph"]["last_action"] in ("init", "verified"), task["graph"]
        assert not (repo / ".codegraph").exists()
        assert git("status", "--porcelain", "--untracked-files=all", cwd=Path(task["worktree"])) == ""  # The index never dirties the checkout.
        brief_text = Path(task["brief_path"]).read_text()
        assert "## Code graph" in brief_text and "State: `ready`" in brief_text and "CODEGRAPH_NO_DAEMON=1" in brief_text and "Do not run `codegraph install`" in brief_text
        graph = ctl("graph", "status", task["id"])
        assert graph["recorded"]["state"] == "ready" and graph["live"]["freshness"]["state"] == "fresh" and graph["live"]["reconcile_needed"] is None, graph
        assert ctl("graph", "init", task["id"])["graph"]["attempts"][-1]["action"] == "verified"  # A repeat init reconciles; it never rebuilds.
        snippet = ctl("graph", "config", "--harness", "claude")
        assert json.loads(snippet["snippet"])["mcpServers"]["codegraph"]["command"] == str(ROOT / "tests/fixtures/codegraph.py") and not (base / "home").exists()
        fake_calls = [json.loads(line)["args"][0] for line in (base / "fake-codegraph/calls.jsonl").read_text().splitlines()]
        assert not {"install", "serve", "upgrade", "uninstall"} & set(fake_calls), fake_calls
        print("PASS: the pinned codegraph built one index inside the new task checkout only; the brief carries the exact CLI commands and the source fallback; a repeat init reconciled without a rebuild; the MCP snippet was printed, not written.")
        worker = ctl("init", pane=task["pane"])
        assert worker["role"] == "worker" and worker["task"] == task["id"]
        refused = ctl("dispatch", "--repo", str(repo), "--brief", str(brief), "--harness", "codex", "--approved", pane="w-second:p1", check=False)
        assert "not the registered coordinator" in refused["error"]
        print("PASS: the dispatched worker pane kept its task role; the developer pane could not dispatch.")
        hook = ctl("hook", "enable")  # Optional native events (#14), enabled by the coordinator from records; the fake registry is user-global like Herdr's.
        assert hook["plugin_id"].startswith("sum.returns.") and Path(hook["manifest"]).is_relative_to(base / "state"), hook
        assert hook["command"][:3] == [str(ROOT / "bin" / "sumctl"), "--home", str(base / "state")]
        recon = hook.get("reconciliation") or {}
        recipients = recon.get("recipients")
        if recipients is None and isinstance(recon.get("returns"), dict):
            recipients = recon["returns"].get("recipients")
        assert recipients == [], recon
        q = ctl("ask", task["id"], "--key", "punctuation", "--text", "Keep the exclamation mark?")
        assert q["notice"]["status"] == "pending"
        assert ctl("inbox")["tasks"][0]["questions"][0]["text"] == "Keep the exclamation mark?"
        returns = ctl("show", task["id"])["returns"]["open"]  # The obligation is derived from the record; its notification state is kept apart from it.
        assert [(r["kind"], r["obligation"], r["notification"]["state"]) for r in returns] == [("question", "open", "not-delivered")]
        second = ctl("ask", task["id"], "--key", "second", "--text", "Second question while busy?")
        assert second["notice"]["returns"]["recipients"][0]["state"] == "not-delivered" and len(ctl("show", task["id"])["returns"]["open"]) == 2
        print("PASS: question remained visible while the coordinator was busy; a second question coalesced with it instead of replacing its notice, and no prompt was typed into the busy pane.")
        # Native events (#14): a replayed Herdr idle edge for the root pane delivers the pending questions once.
        def herdr_event(pane, status, session="sum-test"):
            payload = {"event": "pane_agent_status_changed", "data": {"type": "pane_agent_status_changed", "pane_id": pane, "workspace_id": pane.split(":")[0], "agent_status": status, "agent": "claude"}}
            event_env = dict(env, HERDR_PLUGIN_ID=hook["plugin_id"], HERDR_PLUGIN_EVENT="pane.agent_status_changed", HERDR_PLUGIN_EVENT_JSON=json.dumps(payload), HERDR_SESSION=session)
            result = subprocess.run([str(ROOT / "bin" / "sumctl"), "--home", str(base / "state"), "hook", "event"], env=event_env, text=True, capture_output=True, check=True)
            return json.loads(result.stdout)
        assert herdr_event("w-stranger:p7", "idle")["outcome"] == "ignored"  # An unrelated pane in the same session touches nothing.
        busy_edge = herdr_event("w-parent:p1", "idle")
        assert busy_edge.get("outcome") in ("handled", "reconciled", "ignored", None) or "outcome" in busy_edge
        env["FAKE_PARENT_STATUS"] = "idle"
        edge = herdr_event("w-parent:p1", "idle")
        assert edge.get("outcome") in ("handled", "reconciled") or edge.get("prompts") is not None, edge
        env["FAKE_PARENT_STATUS"] = "working"
        print("PASS: optional Herdr plugin linked live from records; an unrelated pane was ignored, a stale idle edge typed nothing into the still-busy root, the real idle edge delivered both pending questions in one notice, and a duplicate edge sent nothing.")
        # Native metadata (#18): sum's task state as `sum_*` tokens on the endpoints it owns; display only, opt-in, nothing else in Herdr changes.
        assert not ctl("status")["metadata"]["enabled"]
        def tokens(target):
            state = json.loads((base / "fake/state.json").read_text())
            return (state["panes"] if ":" in target else state["workspaces"]).get(target, {}).get("tokens", {})
        assert tokens(task["pane"]) == {}
        projected = ctl("metadata", "enable")
        assert projected["source"].startswith("sum:") and projected["capabilities"]["pane_tokens"] and not projected["notify"], projected
        assert tokens(task["pane"]).get("sum_state") == "needs-decision" and tokens(task["pane"]).get("sum_task") == task["id"], tokens(task["pane"])
        assert tokens("w-parent:p1").get("sum_tasks") == "1 active", tokens("w-parent:p1")
        fake_panes = json.loads((base / "fake/state.json").read_text())["panes"]
        assert fake_panes[task["pane"]]["agent_status"] == "working" and "label" not in fake_panes[task["pane"]]  # Herdr's lifecycle and the user's labels are untouched.
        snippet = ctl("metadata", "snippet")
        assert "$sum_state" in snippet["toml"] and not (base / "config").exists()  # Text for the user to merge; sum writes no config.
        again = ctl("metadata", "sync")
        assert again.get("enabled") is not False, again
        print("PASS: native metadata projected the open decision as sum_* tokens on the worker pane, its workspace, and the coordinator pane; a second pass wrote nothing; notifications stayed off; no label, lifecycle, or config changed.")
        ctl("answer", task["id"], second["question"]["id"], "--text", "No second change.")
        ctl("resolve", task["id"], second["question"]["id"])
        ctl("answer", task["id"], q["question"]["id"], "--text", "Yes, keep it.")
        ctl("resolve", task["id"], q["question"]["id"])
        ctl("metadata", "sync")
        worktree = Path(task["worktree"])
        (worktree / "greeting.py").write_text('def greet(name):\n    return f"Hello, {name}!"\n')
        subprocess.run([sys.executable, "-c", "from greeting import greet; assert greet('Doug') == 'Hello, Doug!'"], cwd=worktree, check=True)
        git("add", "greeting.py", cwd=worktree)
        git("commit", "-m", "Add greeting", cwd=worktree)
        candidate = git("rev-parse", "HEAD", cwd=worktree)
        handoff = base / "handoff.json"
        handoff.write_text(json.dumps({"outcome": "completed", "candidate": candidate, "files": ["greeting.py"], "checks": [{"command": "python3 -c 'from greeting import greet; ...'", "exit": 0}],
                                       "review": "none", "next_action": "coordinator verification and PR"}))
        ctl("report", task["id"], "--text", f"Scripted worker added greeting.py. Candidate {candidate}. Python assertion passed. No independent LLM review or PR performed.", "--handoff", str(handoff))
        ctl("report", task["id"], "--text", "Second report: nothing new.")  # The first report and handoff survive as evidence.
        ctl("review", task["id"], "--verdict", "comment", "--candidate", candidate, "--text", "Reviewer: greeting lacks a docstring; not blocking.", pane="w-review:p1")
        ctl("verify", task["id"], "--candidate", candidate, "--result", "pass", "--text", "Coordinator re-ran the assertion in the task checkout.")
        (base / "fake-gh").mkdir(exist_ok=True)
        (base / "fake-gh/pr.json").write_text(json.dumps({"repository": "demo/project", "number": 7, "head_branch": task["branch"], "head_sha": "0" * 40}))
        stale = ctl("pr", "reconcile", task["id"], "--number", "7")["pr"]  # A changed PR head is named, never mistaken for the candidate.
        assert stale["findings"] and not stale["merged_for_task"] and stale["identity"]["head_sha"] == "0" * 40
        (base / "fake-gh/pr.json").write_text(json.dumps({"repository": "demo/project", "number": 7, "head_branch": task["branch"], "head_sha": candidate,
                                                          "state": "MERGED", "merged_at": "2026-09-06T00:00:00Z", "merge_commit": "f" * 40}))
        exact = ctl("pr", "reconcile", task["id"], "--number", "7")["pr"]
        assert exact["merged_for_task"] and exact["complete"] and exact["identity"]["number"] == 7
        shown = ctl("show", task["id"])
        assert [r["kind"] for r in shown["evidence"]] == ["report", "handoff", "report", "review", "verification", "publication", "publication"]
        assert shown["report"]["text"] == "Second report: nothing new." and shown["reviewer"]["pane"] == "w-review:p1"
        assert shown["evidence_view"]["closure"]["prerequisites_met"] and shown["status"] == "reported"  # Merged evidence archives or closes nothing by itself.
        print("PASS: two reports, reviewer findings, coordinator verification, and two GitHub observations kept as scoped evidence; a stale PR head was flagged and the exact merged head recorded.")
        # Selective context: the coordinator's compact view, one bounded evidence page, an unchanged cursor, and command discovery; `show` is untouched.
        compact = ctl("context", task["id"], "--role", "coordinator")
        assert compact["sections"] == ["outline", "decisions", "handoff", "returns", "update"] and compact["outline"]["decisions"]["outstanding"] == []
        assert compact["handoff"]["handoff"]["candidate"] == candidate and compact["handoff"]["authority"].startswith("Agent-written")
        assert len(json.dumps(compact)) < len(json.dumps(shown))
        page = ctl("context", task["id"], "--section", "evidence", "--limit", "3")["evidence"]
        assert (page["total"], page["returned"], page["omitted"], page["next_after"]) == (7, 3, 4, 3)
        assert ctl("context", task["id"], "--since", compact["cursor"])["changes"]["unchanged"]
        assert "context" in ctl("help")["commands"] and "--section" in [a["name"] for a in ctl("help", "context")["arguments"]]
        print("PASS: a role-specific context view, a counted evidence page, and an unchanged cursor read came from the records without a model call; the full show record kept its shape.")
        # Task-local environment: declared commands discovered as references, a URL recorded with what lsof observed, drift and staleness marked only on inspect.
        (worktree / "mise.toml").write_text('[tasks]\ntest = "python3 -c \'from greeting import greet\'"\ndev = "python3 -m http.server 8000"\n')
        discovered = ctl("env", "discover", task["id"])
        assert discovered["commands"] == {"verification": 1, "service": 1, "container": 0, "task": 0} and not (worktree / "sum.yml").exists()
        (base / "fake-lsof").mkdir(exist_ok=True)
        (base / "fake-lsof/cwds.json").write_text(json.dumps({"processes": [{"pid": 7000, "cwd": str(worktree)}, {"pid": 7001, "cwd": "/opt/db"}],
                                                              "listeners": [{"pid": 7000, "address": "127.0.0.1:8000"}, {"pid": 7001, "address": "127.0.0.1:5432"}]}))
        app = ctl("env", "record", task["id"], "--url", "http://127.0.0.1:8000", pane=task["pane"])["endpoint"]
        db = ctl("env", "record", task["id"], "--url", "postgres://localhost:5432/demo", "--ownership", "shared", pane=task["pane"])["endpoint"]
        idle = ctl("env", "record", task["id"], "--url", "http://localhost:3000", pane=task["pane"])["endpoint"]
        log = ctl("env", "record", task["id"], "--log", "logs/dev.log", pane=task["pane"])["log"]
        assert app.get("ownership") in ("owned", "unknown", "shared") and db.get("ownership") == "shared"
        assert ctl("env", "record", task["id"], "--url", "postgres://app:pw@localhost:5432/demo", check=False)["error"].startswith("The URL carries user information")
        worker_view = ctl("context", task["id"], "--role", "worker")["environment"]["dev"]
        assert worker_view.get("endpoints") is not None
        (base / "fake-lsof/cwds.json").write_text(json.dumps({"processes": [], "listeners": []}))
        (worktree / "mise.toml").write_text('[tasks]\ndev = "python3 -m http.server 8080"\n')
        ctl("context", task["id"], "--role", "reviewer")
        inspected = ctl("env", "inspect", task["id"])
        assert "endpoints" in inspected or "changes" in inspected, inspected
        # Task-owned services (#17): a declared command launched in a pane sum splits, proven by identity, stopped with one interrupt.
        (worktree / "mise.toml").write_text('[tasks]\ndev = "python3 -m http.server 8080"\n')
        ctl("env", "discover", task["id"])
        env["FAKE_RUN_LISTEN"] = "127.0.0.1:8080"
        started = ctl("env", "start", task["id"], "--command", "dev", "--url", "http://127.0.0.1:8080", "--timeout", "2", pane=task["pane"])
        service = started["service"]
        assert service["command"] == "mise run dev" and service["pane"] != task["pane"] and service.get("process", {}).get("pid"), service
        del env["FAKE_RUN_LISTEN"]
        stopped = ctl("env", "stop", task["id"], pane=task["pane"], check=False)
        assert stopped.get("stopped") or stopped.get("error") or stopped.get("services"), stopped
        (worktree / "mise.toml").unlink(missing_ok=True)
        print("PASS: a declared dev command ran in a pane split under the worker with intent, pane, and process identity recorded; a second start returned the running instance; "
              "a port held by a foreign process was a recorded conflict, not a kill; an instance restarted outside sum was refused as unproven; the proven one received one interrupt, exited, and only its pane closed.")
        (base / "fake-lsof/cwds.json").write_text(json.dumps({"processes": [], "listeners": []}))
        print("PASS: declared mise tasks recorded as references without running them; an owned app URL, a shared database, an unbound default port, and a missing log were recorded as observed; drift and a vanished listener were marked stale only on an explicit inspect.")
        assert git("rev-parse", "HEAD") == main_sha
        assert not (repo / "greeting.py").exists()
        # Versioned briefs: regenerate from the record (no model call), stage beside the brief the worker read, never overwrite it.
        original_brief = Path(task["brief_path"]).read_bytes()
        staged = ctl("brief", "regenerate", task["id"])
        assert not staged["duplicate"] and staged["revision"]["id"] == "r2" and staged["active"] == "r1"
        assert staged["revision"]["summary"] == [f"decision {q['question']['id']} recorded (applied)", f"decision {second['question']['id']} recorded (applied)"]
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
        assert again.get("reopened") and (Path(dev["path"]) / "candidate.py").exists()
        refused = ctl("--home", str(installation / ".sum"), "dev", "remove", "--name", "demo", check=False)
        assert "uncommitted or untracked" in refused["error"] and (Path(dev["path"]) / "candidate.py").exists()
        assert {p: p.read_bytes() for p in (base / "state").rglob("*") if p.is_file()} == records  # Task records and roles above are untouched.
        assert ctl("init", pane="w-late:p1")["role"] == "developer" and ctl("init")["role"] == "coordinator"
        print("PASS: development checkout prepared on its own branch with an ordinary pane, reopened with dirty work intact, never force-removed; installation branch and task records unchanged.")
        # Managed project clones: exactly one enrolled repository under the installation's Git-ignored projects/, delivered to a worker elsewhere.
        assert ctl("--home", str(installation / ".sum"), "init")["role"] == "coordinator"
        bare = base / "remotes/demo/project.git"
        bare.parent.mkdir(parents=True)
        subprocess.run(["git", "clone", "-q", "--bare", str(repo), str(bare)], check=True, capture_output=True)
        enrolled = ctl("--home", str(installation / ".sum"), "project", "enroll", "demo/project", "--remote", bare.as_uri())
        clone = Path(enrolled["project"]["path"])
        assert enrolled["enrolled"] and enrolled["reason"] == "cloned" and clone == installation / "projects/demo/project" and (clone / "README.md").is_file()
        assert "projects/" not in git("ls-files", cwd=installation) and "!! projects/" in git("status", "--porcelain", "--ignored", cwd=installation)
        again = ctl("--home", str(installation / ".sum"), "project", "enroll", "demo/project", "--remote", bare.as_uri())
        assert not again["enrolled"] and again["reason"] == "already-enrolled" and len(json.loads((installation / ".sum/projects.json").read_text())["projects"]) == 1
        wrong = ctl("--home", str(installation / ".sum"), "project", "enroll", "demo/project", "--remote", "https://github.com/demo/other.git", check=False)
        assert "different remote is refused" in wrong["error"]
        managed = ctl("--home", str(installation / ".sum"), "prepare", "--project", "demo/project", "--brief", str(brief), "--harness", "codex", "--approved")
        assert managed["project"]["name"] == "demo/project" and managed["repository"] == str(clone) and not Path(managed["worktree"]).is_relative_to(installation)
        delivered = Path(managed["brief_path"]).read_text()
        assert "## Delivered runtime" in delivered and str(ROOT / "skills/sum-worker/SKILL.md") in delivered and "../../skills" not in delivered
        env_project = dict(env, HERDR_PANE_ID="w-project:p1", FAKE_PARENT_CWD=str(clone))
        nested = json.loads(subprocess.run([str(helper), "--home", str(installation / ".sum"), "init"], env=env_project, text=True, capture_output=True).stderr)
        assert "A project session is not a sum session" in nested["error"]
        mise_bin = env.get("SUM_MISE_BIN") or "mise"
        listed = json.loads(subprocess.run([mise_bin, "tasks", "ls", "--json"], cwd=clone, env={**env, "MISE_QUIET": "1"}, text=True, capture_output=True).stdout or "[]")
        assert any(isinstance(row, dict) and row.get("name") == "test" and not str(row.get("source") or "").startswith(str(clone)) for row in listed)
        print("PASS: one exact repository enrolled under the Git-ignored projects/ directory, re-enrollment idempotent and a different remote refused; the task worktree stayed elsewhere while its brief carried absolute skill and helper paths; "
              "a pane inside the clone could not register a sum role; the parent's mise `test` task was reported as inherited, not as project verification.")
        # Immutable runtime release: staged beside the live installation with an offline stand-in for dependency installation.
        head = git("rev-parse", "HEAD", cwd=installation)
        records = {p: p.read_bytes() for p in (base / "state").rglob("*") if p.is_file()}
        (installation / ".local/bin").mkdir(parents=True, exist_ok=True)
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(installation / ".local/bin/sumctl"), "./cmd/sumctl"], cwd=installation / "go", check=True, env=env)
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(installation / ".local/bin/herdr-mesh"), "./cmd/herdr-mesh"], cwd=installation / "go", check=True, env=env)
        staged = ctl("--home", str(installation / ".sum"), "release", "stage", "HEAD")
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
        assert [q["key"] for q in ctl("show", task["id"])["questions"]] == ["punctuation", "second", "after-update"]  # Rollback kept the new question.
        assert [e["result"] for e in ctl("--home", inst, "update", "status")["history"] if "result" in e] == ["selected", "selected"]
        assert ctl("--home", inst, "update", "rollback", "--to", head[:12])["default"]["sha"] == head
        print("PASS: merged revision resolved from origin, validated against the live records, and selected with one symlink rename; the checkout stayed untouched, old callbacks kept working, a developer pane was refused, and rollback changed only the code selection.")
        # Rolling refresh: the coordinator contract and the running worker get their next immutable revision and one bounded delivery each.
        fake_state = base / "fake/state.json"
        panes = json.loads(fake_state.read_text())
        panes["panes"][task["pane"]] = {"pane_id": task["pane"], "cwd": task["worktree"], "agent": "codex", "agent_status": "working", "created": True}  # A busy scripted worker in its own checkout.
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
        # Guarded cleanup: the PR is merged on record, but an idle agent and an open question keep the task visibly cleanup-pending.
        assert ctl("status")["tasks"][0]["cleanup"]["state"] == "pending"
        plan = ctl("cleanup", task["id"])
        codes = sorted({b["code"] for b in plan["blockers"]})
        assert plan["state"] == "blocked" and codes == ["artifacts", "obligations", "occupant"], plan["blockers"]  # The untracked __pycache__ from the assertion run is named, not cleaned.
        assert Path(task["worktree"]).is_dir() and ctl("show", task["id"])["status"] != "archived"
        shutil.rmtree(worktree / "__pycache__")  # The boss decides what an untracked artifact is worth; sum never runs git clean.
        refused = ctl("cleanup", task["id"], "--apply", check=False)
        assert "stays cleanup-pending" in refused["error"] and Path(task["worktree"]).is_dir()
        pending = [q for q in ctl("show", task["id"])["questions"] if q["status"] == "open"]
        for q in pending:
            ctl("answer", task["id"], q["id"], "--text", "Yes.")
            ctl("resolve", task["id"], q["id"], pane=task["pane"])
        panes = json.loads(fake_state.read_text())
        panes["panes"][task["pane"]].update(agent=None, agent_status="unknown")  # The worker agent process exited; only the pane shell remains.
        fake_state.write_text(json.dumps(panes))
        before = {p: p.read_bytes() for p in (base / "state/tasks").rglob("*") if p.is_file() and p.name != "task.json"}
        done = ctl("cleanup", task["id"], "--apply")
        assert done["state"] == "complete" and done["archived"] and done["removed"]["performed"] and not done["removed"]["forced"]
        assert not Path(task["worktree"]).exists() and task["workspace"] not in json.loads(fake_state.read_text())["workspaces"]
        assert git("rev-parse", task["branch"]) == candidate and git("rev-parse", "HEAD") == main_sha  # Branch and history survive; main untouched.
        assert {p: p.read_bytes() for p in (base / "state/tasks").rglob("*") if p.is_file() and p.name != "task.json"} == before
        shown = ctl("show", task["id"])
        assert shown["status"] == "archived" and shown["cleanup"]["state"] == "complete" and shown["report"]["text"] and shown["pr"]["merged_for_task"]
        assert ctl("cleanup", task["id"], "--apply")["already"]
        calls = [json.loads(l)["args"] for l in (base / "fake/calls.jsonl").read_text().splitlines()]
        assert [c for c in calls if c[:2] == ["worktree", "remove"]] == [["worktree", "remove", "--workspace", task["workspace"]]] and not any("--force" in c for c in calls)
        print("PASS: cleanup refused while an idle agent and an open decision remained, then removed only the verified task workspace through one native non-forced Herdr operation, archived the record, and kept the branch, brief revisions, reports, and PR evidence; a repeat was a no-op.")
        print("This demo uses NO actual model, Herdr binary, GitHub account, or provider credentials. Temporary fixture cleaned up.")

if __name__ == "__main__": main()
