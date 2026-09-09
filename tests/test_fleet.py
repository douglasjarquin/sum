"""Fleet capacity and rolling updates against ten or more scripted workers.

Real Git, the strict fake Herdr, real sum helpers through the installation entrypoint; no model, network, or credentials.
Scripted workers prove the helper's bookkeeping and bounds, never a model's compliance with a refresh instruction.
"""
from __future__ import annotations
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import time
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_core
from test_core import sumctl


class FleetLab(test_core.UpdateLab):
    def setUp(self):
        super().setUp()
        self.measurements = []

    def tearDown(self):
        for row in self.measurements:  # Actual numbers from this run; the validation notes quote them, they are not a guarantee.
            print("FLEET-MEASURE " + json.dumps(row), file=sys.stderr)

    def ctl(self, root, store, *argv, env=None, ok=True):
        result = self.cli([root / "bin" / "sumctl", "--home", store.home, *argv], env=env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        self.assertEqual(result.returncode, 1, result.stdout)
        return json.loads(result.stderr)

    def project(self, name):
        repo = self.root / "projects" / name
        repo.mkdir(parents=True)
        self.git("init", "-b", "main", cwd=repo)
        self.git("config", "user.name", "sum test", cwd=repo)
        self.git("config", "user.email", "test@example.invalid", cwd=repo)
        (repo / "README.md").write_text(f"{name}\n")
        self.git("add", ".", cwd=repo)
        self.git("commit", "-q", "-m", "fixture", cwd=repo)
        return repo

    def brief(self, text="Do the approved thing."):
        path = self.root / "brief.md"
        if not path.exists():
            path.write_text(text)
        return path

    def fake_state(self):
        return json.loads((self.root / "fake/state.json").read_text())

    def pane_state(self, pane, **changes):
        path = self.root / "fake/state.json"
        state = json.loads(path.read_text())
        if changes.get("remove"):
            state["panes"].pop(pane, None)
        else:
            state["panes"][pane].update(changes)
        path.write_text(json.dumps(state))

    def calls(self):
        path = self.root / "fake/calls.jsonl"
        return [json.loads(line)["args"] for line in path.read_text().splitlines()] if path.exists() else []

    def checkouts(self, tasks):
        return {t["id"]: (self.git("rev-parse", "HEAD", cwd=t["worktree"]), self.git("branch", "--show-current", cwd=t["worktree"]),
                          self.git("status", "--porcelain", "--untracked-files=all", cwd=t["worktree"])) for t in tasks}

    def versions(self, store, task_id):
        return json.loads((store.path(task_id) / "versions.json").read_text())

    def measure(self, label, root, store, *argv, env=None):
        before = len(self.calls())
        started = time.monotonic()
        value = self.ctl(root, store, *argv, env=env)
        elapsed = round((time.monotonic() - started) * 1000)
        delta = self.calls()[before:]
        row = {"pass": label, "wall_ms": elapsed, "herdr_calls": len(delta), "agent_get_calls": sum(1 for c in delta if c[:2] == ["agent", "get"]),
               "agent_list_calls": sum(1 for c in delta if c[:2] == ["agent", "list"]), "prompts": sum(1 for c in delta if c[:2] == ["agent", "prompt"]),
               "reported_fanout": value.get("fanout")}
        self.measurements.append(row)
        return value, delta


class FleetTest(FleetLab):
    def test_twelve_workers_keep_working_through_updates_an_interrupted_refresh_and_a_rollback(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        settings = self.ctl(root, store, "settings", "set", "--global", "12", "--per-repository", "1", env=env)
        self.assertEqual((settings["previous"], settings["capacity"]), (None, {"global": 12, "per_repository": 1}))
        roles = ["busy-tool-call", "dirty-checkout", "open-question", "answered-unapplied", "pending-report", "old-mcp-client",
                 "closed-parent", "unknown-worker", "failed-refresh", "ignores-refresh", "cooperative-a", "cooperative-b"]
        tasks = {}
        for role in roles:
            repo = self.project(role)
            task = self.ctl(root, store, "dispatch", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
            self.assertEqual(task["status"], "running")
            self.assertEqual(task["admission"]["limits"], {"global": 12, "per_repository": 1})
            tasks[role] = task
        by_id = {t["id"]: role for role, t in tasks.items()}
        self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 12)
        thirteenth = self.ctl(root, store, "prepare", "--repo", self.project("thirteenth"), "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)
        self.assertIn("12 of 12 global execution slots", thirteenth["error"])
        # Every worker settled except the one inside a long tool call; then the recorded situations.
        for role, task in tasks.items():
            self.pane_state(task["pane"], agent_status="working" if role == "busy-tool-call" else "idle")
        (Path(tasks["dirty-checkout"]["worktree"]) / "wip.txt").write_text("uncommitted work\n")
        open_q = self.ctl(root, store, "ask", tasks["open-question"]["id"], "--key", "open", "--text", "Open question?", env=env)["question"]
        answered = self.ctl(root, store, "ask", tasks["answered-unapplied"]["id"], "--key", "answered", "--text", "Answered question?", env=env)["question"]
        self.ctl(root, store, "answer", tasks["answered-unapplied"]["id"], answered["id"], "--text", "Yes.", env=env)
        self.pane_state(tasks["answered-unapplied"]["pane"], agent_status="idle")  # The answer notice left the fake worker `working`.
        self.ctl(root, store, "report", tasks["pending-report"]["id"], "--text", "Candidate ready; not verified.", env=env)
        old_versions = self.versions(store, tasks["old-mcp-client"]["id"])
        old_versions["runtime"]["mcp"] = {**sumctl.MCP_CONTRACT, "tools": 9}  # A client that connected under an older tool surface.
        (store.path(tasks["old-mcp-client"]["id"]) / "versions.json").write_text(json.dumps(old_versions))
        closed = store.read(tasks["closed-parent"]["id"])
        closed["parent"]["pane"] = "w-gone:p1"
        store.save(closed)
        closed_q = self.ctl(root, store, "ask", tasks["closed-parent"]["id"], "--key", "orphan", "--text", "Parent gone?", env=env)
        self.assertEqual(closed_q["notice"]["status"], "pending")
        self.assertIn("agent_not_found", closed_q["notice"]["error"])
        self.pane_state(tasks["unknown-worker"]["pane"], remove=True)
        env["FAKE_FAIL_PROMPT_PANES"] = tasks["failed-refresh"]["pane"]
        records = {role: store.read(t["id"]) for role, t in tasks.items()}
        checkouts = self.checkouts(tasks.values())
        calls_before = len(self.calls())
        # Rundown over twelve workers: one agent snapshot, no per-worker observation call, no transcript read.
        inbox, delta = self.measure("status --live (12 workers)", root, store, "status", "--live", env=env)
        self.assertEqual((inbox["fanout"]["herdr_calls"], inbox["fanout"]["sessions"]), (1, 1))
        self.assertEqual([c[:2] for c in delta], [["agent", "list"]])
        observed = {by_id[row["id"]]: row.get("observed") for row in inbox["tasks"]}
        self.assertEqual(observed["busy-tool-call"], "working")
        self.assertIsNone(observed["unknown-worker"])
        self.assertIn("agent_not_found", next(r for r in inbox["tasks"] if r["id"] == tasks["unknown-worker"]["id"])["attention"])
        self.assertEqual(inbox["capacity"]["occupied"]["global"], 12)
        # Stage N+1 (a changed worker procedure), activate it, and request a rolling refresh from the new default.
        skill = (root / "skills/sum-worker/SKILL.md").read_text()
        sha1 = self.commit_upstream(root, "skills/sum-worker/SKILL.md", skill + "\nUpdate one: reread decisions before continuing.\n")
        applied = self.apply(store)
        self.assertEqual((applied["changed"], applied["default"]["sha"]), (True, sha1))
        first, delta = self.measure("refresh request after update one (12 workers)", root, store, "refresh", "request", env=env)
        rows = {by_id.get(r.get("task"), r["target"]): r for r in first["targets"]}
        self.assertEqual(first["runtime"]["sha"], sha1)
        self.assertEqual(rows["coordinator"]["state"], "pending-busy")
        expected = {"busy-tool-call": "pending-busy", "unknown-worker": "pending-unreachable", "failed-refresh": "pending-unreachable"}
        for role in roles:
            self.assertEqual(rows[role]["state"], expected.get(role, "submitted-unconfirmed"), role)
            self.assertEqual(rows[role]["revision"], "r2", role)
        self.assertIn("working", rows["busy-tool-call"]["reason"])
        self.assertIn("agent_not_found", rows["unknown-worker"]["reason"])
        self.assertIn("prompt was not accepted", rows["failed-refresh"]["reason"])
        self.assertEqual([d["what"] for d in rows["old-mcp-client"]["deferred"]], ["mcp"])
        self.assertEqual(first["counts"]["submitted-unconfirmed"], 9)
        self.assertEqual(first["fanout"]["herdr_calls"], 1 + 10)  # One snapshot plus one prompt per settled recipient.
        self.assertEqual([c[:2] for c in delta if c[:2] == ["agent", "get"]], [])
        self.assertEqual(sum(1 for c in delta if c[:2] == ["agent", "prompt"]), 10)
        self.assertLessEqual(len(delta), 1 + 1 + 10)  # `herdr --version` for the coordinator gate, the snapshot, the prompts.
        for prompt in (c[3] for c in delta if c[:2] == ["agent", "prompt"]):
            self.assertTrue(prompt.startswith("sum refresh t-"))
            for prose in ("Open question", "Answered question", "Yes.", "Candidate ready", "Parent gone"):
                self.assertNotIn(prose, prompt)
        # Ordinary work continues on the old absolute callbacks while the new default serves.
        self.ctl(root, store, "answer", tasks["open-question"]["id"], open_q["id"], "--text", "Answered after update one.", env=env)
        self.ctl(root, store, "resolve", tasks["answered-unapplied"]["id"], answered["id"], env=env)
        self.ctl(root, store, "report", tasks["cooperative-a"]["id"], "--text", "Reported during the update.", env=env)
        more = self.ctl(root, store, "ask", tasks["busy-tool-call"]["id"], "--key", "busy", "--text", "Asked while busy?", env=env)["question"]
        for role in ("dirty-checkout", "cooperative-a", "cooperative-b"):
            self.ctl(root, store, "brief", "adopt", tasks[role]["id"], "r2", env={**env, "HERDR_PANE_ID": tasks[role]["pane"]})
        self.assertEqual(self.ctl(root, store, "refresh", "status")["counts"]["confirmed"], 3)
        for role, task in tasks.items():
            self.pane_state(task["pane"], agent_status="working" if role == "busy-tool-call" else "idle") if role != "unknown-worker" else None
        # Update two, then the updater is interrupted in the middle of its fan-out: everything persisted so far stays, nothing is half-written.
        self.commit_upstream(root, "AGENTS.md", (root / "AGENTS.md").read_text() + "\nUpdate two.\n")
        sha2 = self.commit_upstream(root, "skills/sum-worker/SKILL.md", skill + "\nUpdate two: reread decisions before continuing.\n")
        self.assertEqual(self.apply(store)["default"]["sha"], sha2)
        release2 = root / ".local" / "releases" / sha2
        real_delivery = sumctl.attempt_delivery
        deliveries = []
        def interrupted(endpoint, expected_cwd, message, session, snapshots=None):
            if len(deliveries) == 5:
                raise KeyboardInterrupt  # The operator killed the updater mid-pass.
            deliveries.append(endpoint["pane"])
            return real_delivery(endpoint, expected_cwd, message, session, snapshots)
        with mock.patch.object(sumctl, "RUNTIME", release2), mock.patch.object(sumctl, "ROOT", root.resolve()), \
                mock.patch.object(sumctl, "attempt_delivery", interrupted), mock.patch.dict(os.environ, env):
            with self.assertRaises(KeyboardInterrupt):
                sumctl.refresh_request(store, argparse.Namespace(task=None, coordinator=False))
        status = self.ctl(root, store, "refresh", "status")
        states = {by_id[r["task"]]: r for r in status["targets"] if r["target"] == "task"}
        orphaned = [role for role, r in states.items() if r["requested"] == "r3" and r["reason"] == "requested; no delivery attempt recorded yet"]
        self.assertEqual(len(orphaned), 1, states)  # Exactly the interrupted target: requested and persisted, delivery never attempted.
        self.assertEqual(sum(1 for r in states.values() if r["requested"] == "r3"), 6)
        self.assertEqual(self.ctl(root, store, "show", tasks["open-question"]["id"])["questions"][0]["status"], "answered")
        for role, task in tasks.items():
            saved = store.read(task["id"])
            self.assertEqual([q["key"] for q in saved["questions"]], [q["key"] for q in records[role]["questions"]] + (["busy"] if role == "busy-tool-call" else []))
        # Recovery is an ordinary repeated request: idempotent, coalescing every target on r3.
        recovered, delta = self.measure("refresh request recovery after interruption (12 workers)", root, store, "refresh", "request", env=env)
        rows = {by_id.get(r.get("task"), r["target"]): r for r in recovered["targets"]}
        self.assertEqual({r["revision"] for role, r in rows.items() if role != "coordinator"}, {"r3"})
        self.assertEqual(rows["coordinator"]["revision"], "r2")
        self.assertIn("AGENTS.md changed", rows["coordinator"]["summary"])
        self.assertEqual(recovered["fanout"]["sessions"], 1)
        self.assertEqual([c[:2] for c in delta if c[:2] == ["agent", "get"]], [])
        for role in roles:
            versions = self.versions(store, tasks[role]["id"])
            self.assertEqual(versions["requested"], "r3", role)
            self.assertEqual(versions["revisions"][-1]["id"], "r3")
        stale = self.ctl(root, store, "brief", "adopt", tasks["cooperative-b"]["id"], "r2", env={**env, "HERDR_PANE_ID": tasks["cooperative-b"]["pane"]}, ok=False)
        self.assertIn("not the requested revision", stale["error"])
        self.ctl(root, store, "brief", "adopt", tasks["cooperative-b"]["id"], "r3", env={**env, "HERDR_PANE_ID": tasks["cooperative-b"]["pane"]})
        # Roll back the default: the rolled-back runtime stages the next revision; no worker is restarted; obligations are intact.
        rolled = self.ctl(root, store, "update", "rollback", env=env)
        self.assertEqual((rolled["changed"], rolled["default"]["sha"]), (True, sha1))
        for role, task in tasks.items():
            if role not in ("busy-tool-call", "unknown-worker"):
                self.pane_state(task["pane"], agent_status="idle")
        third, delta = self.measure("refresh request after rollback (12 workers)", root, store, "refresh", "request", env=env)
        rows = {by_id.get(r.get("task"), r["target"]): r for r in third["targets"]}
        self.assertEqual((third["runtime"]["sha"], rows["coordinator"]["revision"]), (sha1, "r3"))
        self.assertEqual({r["revision"] for role, r in rows.items() if role != "coordinator"}, {"r4"})
        self.assertNotIn("Update two", Path(rows["cooperative-a"]["path"]).read_text())
        self.assertIn("Update one", Path(rows["cooperative-a"]["path"]).read_text())
        self.ctl(root, store, "refresh", "adopt", "--coordinator", "r3", env=env)
        for role in ("cooperative-a", "cooperative-b", "dirty-checkout"):
            self.ctl(root, store, "brief", "adopt", tasks[role]["id"], "r4", env={**env, "HERDR_PANE_ID": tasks[role]["pane"]})
        final = self.ctl(root, store, "refresh", "status")
        self.assertEqual(final["counts"]["confirmed"], 4)
        self.assertEqual(final["counts"]["pending-busy"], 1)
        self.assertEqual(final["counts"]["pending-unreachable"], 2)
        # A failed update leaves the rolled-back default and does not disturb dispatch, ask, report, or rundown.
        (root / "local-only.txt").write_text("unmerged\n")
        self.git("add", "local-only.txt", cwd=root)
        self.git("commit", "-q", "-m", "unmerged local commit", cwd=root)
        refused = self.ctl(root, store, "update", "apply", "--ref", "HEAD", "--no-fetch", env=env, ok=False)
        self.assertIn("not merged on origin/main", refused["error"])
        self.assertEqual(self.ctl(root, store, "update", "status")["default"]["sha"], sha1)
        self.ctl(root, store, "answer", tasks["busy-tool-call"]["id"], more["id"], "--text", "Answered during the failed update.", env=env)
        self.ctl(root, store, "report", tasks["cooperative-b"]["id"], "--text", "Reported during the failed update.", env=env)
        live, delta = self.measure("inbox --live after failed update (12 workers)", root, store, "inbox", "--live", env=env)
        self.assertEqual(live["fanout"]["herdr_calls"], 1)
        # New work selects the rolled-back default after explicit stop inspection releases the reported task's slot.
        refused = self.ctl(root, store, "prepare", "--repo", self.project("late"), "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)
        self.assertIn("12 of 12 global execution slots", refused["error"])
        pending = store.read(tasks["pending-report"]["id"])
        self.pane_state(tasks["pending-report"]["pane"], agent=None, agent_status="done")
        self.ctl(root, store, "execution", "park", tasks["pending-report"]["id"], "--attempt", pending["execution"]["worker"]["id"],
                 env={**env, "SUM_LSOF_BIN": str(Path(__file__).parent / "fixtures/lsof.py"), "FAKE_LSOF_ROOT": str(self.root / "fake-lsof")})
        self.ctl(root, store, "archive", tasks["pending-report"]["id"], "--acknowledge", env=env)
        late = self.ctl(root, store, "prepare", "--repo", self.root / "projects" / "late", "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        self.assertEqual((self.versions(store, late["id"])["runtime"]["sha"], late["admission"]["occupied_before"]["global"]), (sha1, 11))
        self.assertIn(str(root.resolve() / "bin" / "sumctl"), Path(late["brief_path"]).read_text())
        # Nothing was relaunched, stopped, rewound, or lost.
        later = self.calls()[calls_before:]
        self.assertEqual([c for c in later if c[:2] in (["agent", "start"], ["worktree", "create"], ["agent", "read"])], [["worktree", "create"] + later[[i for i, c in enumerate(later) if c[:2] == ["worktree", "create"]][0]][2:]])
        self.assertEqual(sum(1 for c in self.calls() if c[:2] == ["agent", "start"]), 12)
        self.assertFalse(any(c[1] in {"stop", "kill", "restart", "remove"} for c in self.calls() if c[0] in {"agent", "pane", "worktree"}))
        self.assertEqual(self.checkouts(tasks.values()), checkouts)
        self.assertEqual((Path(tasks["dirty-checkout"]["worktree"]) / "wip.txt").read_text(), "uncommitted work\n")
        shown = {role: self.ctl(root, store, "show", t["id"]) for role, t in tasks.items()}
        self.assertEqual(shown["open-question"]["questions"][0]["status"], "answered")
        self.assertEqual(shown["answered-unapplied"]["questions"][0]["status"], "applied")
        self.assertEqual(shown["closed-parent"]["questions"][0]["status"], "open")
        self.assertEqual([q["status"] for q in shown["busy-tool-call"]["questions"]], ["answered"])
        self.assertEqual(shown["pending-report"]["report"]["brief_revision"], "r1")
        self.assertEqual(shown["cooperative-a"]["report"]["brief_revision"], "r1")
        self.assertEqual(shown["cooperative-b"]["report"]["brief_revision"], "r4")
        self.assertEqual(shown["pending-report"]["status"], "archived")
        self.assertTrue(all(len(shown[role]["versions"]["revisions"]) == 4 for role in roles if role != "pending-report"))
        self.assertEqual(shown["old-mcp-client"]["versions"]["revisions"][-1]["status"], "requested")

    def test_twelve_concurrent_admissions_respect_the_limits_without_lock_held_herdr_calls(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "6", env=env)
        repos = [self.project(f"repo-{i:02d}") for i in range(12)]
        def admit(repo):
            return self.cli([root / "bin" / "sumctl", "--home", store.home, "prepare", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved"], env=env)
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            results = list(pool.map(admit, repos))
        admitted = [json.loads(r.stdout) for r in results if r.returncode == 0]
        refused = [json.loads(r.stderr)["error"] for r in results if r.returncode != 0]
        self.assertEqual((len(admitted), len(refused)), (6, 6), refused)
        self.assertTrue(all("6 of 6 global execution slots" in e for e in refused), refused)
        self.assertEqual(sorted(t["admission"]["occupied_before"]["global"] for t in admitted), list(range(6)))  # Serialized under the store lock.
        self.assertEqual(sum(1 for c in self.calls() if c[:2] == ["worktree", "create"]), 6)  # A refused admission made no Herdr call at all.
        self.assertEqual(len({t["repository"] for t in admitted}), 6)
        self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 6)
        # Per-repository isolation: more global room never admits a second writer into one checkout.
        self.ctl(root, store, "settings", "set", "--global", "24", env=env)
        shared = self.project("shared")
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
            results = list(pool.map(admit, [shared] * 6))
        self.assertEqual(sum(1 for r in results if r.returncode == 0), 1)
        self.assertTrue(all("1 of 1 slots for" in json.loads(r.stderr)["error"] for r in results if r.returncode))
        self.assertEqual(sum(1 for c in self.calls() if c[:2] == ["worktree", "create"]), 7)
        with self.assertRaisesRegex(sumctl.SumError, "per_repository \\(3\\) exceeds capacity.global \\(2\\)"):
            sumctl.write_settings(store, {"global": 2, "per_repository": 3})

    def test_explicit_limits_invalid_settings_and_lowered_limits_never_touch_running_work(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "2", "--per-repository", "1", env=env)
        shown = self.ctl(root, store, "settings", "show")
        self.assertEqual((shown["limits"], shown["source"]), ({"global": 2, "per_repository": 1}, "settings.json"))
        first = self.ctl(root, store, "dispatch", "--repo", self.project("one"), "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        second = self.ctl(root, store, "dispatch", "--repo", self.project("two"), "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        third_repo = self.project("three")
        self.assertIn("2 of 2 global execution slots", self.ctl(root, store, "prepare", "--repo", third_repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)["error"])
        # A reported or idle worker keeps its slot until conclusive stop inspection.
        self.ctl(root, store, "report", first["id"], "--text", "Done, says the worker.", env=env)
        self.pane_state(first["pane"], agent_status="done")
        self.assertIn("2 of 2 global execution slots", self.ctl(root, store, "prepare", "--repo", third_repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)["error"])
        # Invalid settings fail before any side effect and leave every recorded task and callback usable.
        settings = store.home / "settings.json"
        records = self.snapshot(store.home)
        calls = len(self.calls())
        for content in ('{"schema": 2, "capacity": {"global": 3}}', '{"schema": 1, "capacity": {"global": 0}}', '{"schema": 1, "capacity": {"global": 1, "per_repository": 2}}',
                        '{"schema": 1, "capacity": {"global": true}}', '{"schema": 1, "capacity": {"globl": 3}}', '{"schema": 1, "capacity": 3}', '[1]', 'not json', '{"schema": 1, "capacity": {"global": 4}, "extra": 1}',
                        f'{{"schema": 1, "capacity": {{"global": {sumctl.CAPACITY_MAX + 1}}}}}'):
            settings.write_text(content)
            error = self.ctl(root, store, "prepare", "--repo", third_repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)["error"]
            self.assertIn("Invalid", error, content)
            self.assertIn("nothing was admitted", error)
            self.assertEqual(self.ctl(root, store, "settings", "show")["source"], "invalid")
            self.assertIn("Invalid", self.ctl(root, store, "settings", "set", "--global", "5", env=env, ok=False)["error"])  # A broken file is never silently replaced.
            self.assertEqual(settings.read_text(), content)
        self.assertEqual(len(store.all()), 2)
        self.assertEqual(len(self.calls()), calls)
        self.assertEqual(self.ctl(root, store, "ask", second["id"], "--key", "k", "--text", "Still saved?", env=env)["question"]["status"], "open")
        self.assertEqual(self.ctl(root, store, "show", first["id"])["report"]["text"], "Done, says the worker.")
        self.assertEqual({k for k in set(records) | set(self.snapshot(store.home)) if records.get(k) != self.snapshot(store.home).get(k)},
                         {"settings.json", f"tasks/{second['id']}/task.json", f"tasks/{second['id']}/returns.json"})  # The notice names the first task's submitted report too, but only the new question is stamped with this attempt (#14 repair: a submitted or uncertain sibling is never restamped).
        settings.symlink_to(settings.with_name("elsewhere.json")) if not settings.exists() else settings.unlink()
        settings.symlink_to(settings.with_name("elsewhere.json"))
        self.assertIn("must not be a symlink", self.ctl(root, store, "prepare", "--repo", third_repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)["error"])
        settings.unlink()
        # Explicitly raised capacity is the user's choice; a developer pane cannot set it; the value is validated before it is written.
        with mock.patch.dict(os.environ, {"HERDR_PANE_ID": "w-dev:p1"}):
            self.assertEqual(sumctl.init(store, argparse.Namespace(role=None, task=None, reclaim=False))["role"], "developer")
        self.assertIn("not the registered coordinator", self.ctl(root, store, "settings", "set", "--global", "9", env={**env, "HERDR_PANE_ID": "w-dev:p1"}, ok=False)["error"])
        self.assertIn("between 1 and", self.ctl(root, store, "settings", "set", "--global", "0", env=env, ok=False)["error"])
        self.assertIn("Give --global", self.ctl(root, store, "settings", "set", env=env, ok=False)["error"])
        self.assertFalse(settings.exists())
        raised = self.ctl(root, store, "settings", "set", "--global", "4", env=env)
        self.assertEqual((raised["capacity"], json.loads(settings.read_text())), ({"global": 4, "per_repository": 1}, {"schema": 1, "capacity": {"global": 4, "per_repository": 1}}))
        self.assertEqual(oct(settings.stat().st_mode & 0o777), "0o600")
        third = self.ctl(root, store, "dispatch", "--repo", third_repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        self.assertEqual(third["admission"]["source"], "settings.json")
        # Lowering the limit affects future admission only: nothing is evicted, stopped, or relaunched.
        lowered = self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        self.assertEqual((lowered["capacity"]["global"], lowered["occupied"]["global"]), (1, 3))
        self.assertEqual(sorted(t["status"] for t in store.all()), ["reported", "running", "waiting"])
        self.assertIn("3 of 1 global execution slots", self.ctl(root, store, "prepare", "--repo", self.project("four"), "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)["error"])
        self.assertFalse(any(c[1] in {"stop", "kill"} for c in self.calls()))
        self.assertEqual(sum(1 for c in self.calls() if c[:2] == ["agent", "start"]), 3)
        self.assertEqual(self.ctl(root, store, "inbox")["capacity"]["occupied"]["global"], 3)
        # Settings travel with the records-only backup; precedence is the file, then the defaults; preferences stay narrative.
        (store.home / "preferences.md").write_text("Narrative: I like three workers.\n")
        backup = sumctl.backup(store, self.root / "records.tar.gz")
        with tarfile.open(backup["backup"]) as archive:
            names = archive.getnames()
        self.assertIn("state/settings.json", names)
        self.assertTrue(backup["manifest"]["settings_included"])
        self.assertEqual(sumctl.load_settings(store)["capacity"]["global"], 1)
        # Candidate code in a development checkout may show but never set the installation's settings.
        candidate = Path(sumctl.dev_prepare(store, argparse.Namespace(name="candidate", base="HEAD", pane=False))["path"])
        with mock.patch.object(sumctl, "ROOT", candidate), mock.patch("sys.stderr"), mock.patch.object(sumctl, "emit"):
            self.assertEqual(sumctl.main(["--home", str(store.home), "settings", "show"]), 0)
            self.assertEqual(sumctl.main(["--home", str(store.home), "settings", "set", "--global", "8"]), 1)
        self.assertEqual(sumctl.load_settings(store)["capacity"]["global"], 1)

    def test_absent_capacity_is_unlimited_and_missing_capacity_key_is_unlimited(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        repo = self.project("unlimited")
        first = self.ctl(root, store, "dispatch", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        second = self.ctl(root, store, "dispatch", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        self.assertNotEqual(first["id"], second["id"])
        shown = self.ctl(root, store, "settings", "show")
        self.assertEqual((shown["limits"], shown["source"], shown["occupied"]["global"]), (None, "unlimited", 2))
        (store.home / "settings.json").write_text(json.dumps({"schema": 1}))
        third = self.ctl(root, store, "dispatch", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        self.assertNotEqual(second["id"], third["id"])
        shown = self.ctl(root, store, "settings", "show")
        self.assertEqual((shown["limits"], shown["source"], shown["occupied"]["global"]), (None, "settings.json", 3))

    def test_explicit_capacity_remains_enforced_and_clearable(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        repo = self.project("limited")
        self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        first = self.ctl(root, store, "dispatch", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        refused = self.ctl(root, store, "prepare", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)
        self.assertIn("1 of 1 global execution slots", refused["error"])
        self.assertEqual(self.ctl(root, store, "settings", "show")["limits"], {"global": 1, "per_repository": 1})
        self.ctl(root, store, "preset", "set", "deep", "--harness", "codex", env=env)
        self.ctl(root, store, "settings", "set", "--worker-preset", "deep", env=env)
        self.ctl(root, store, "settings", "set", "--clear-capacity", env=env)
        shown = self.ctl(root, store, "settings", "show")
        self.assertEqual((shown["limits"], shown["worker"], sorted(shown["presets"])), (None, {"preset": "deep"}, ["deep"]))
        second = self.ctl(root, store, "dispatch", "--repo", repo, "--brief", self.brief(), "--harness", "codex", "--approved", env=env)
        self.assertNotEqual(first["id"], second["id"])


if __name__ == "__main__":
    import unittest
    unittest.main()
