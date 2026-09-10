from __future__ import annotations

import concurrent.futures
import json
import os
from pathlib import Path
import sys
from unittest import mock

import test_fleet


class ExecutionTest(test_fleet.FleetLab):
    def setUp(self):
        super().setUp()
        self.lsof_root = self.root / "fake-lsof"
        patch = mock.patch.dict(os.environ, {"SUM_LSOF_BIN": str(Path(__file__).parent / "fixtures/lsof.py"), "FAKE_LSOF_ROOT": str(self.lsof_root)})
        patch.start()
        self.addCleanup(patch.stop)

    def lsof(self, *processes):
        self.lsof_root.mkdir(exist_ok=True)
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": list(processes)}))

    def ctl(self, root, store, *argv, env=None, ok=True):
        result = self.cli([sys.executable, root / "lib/sumctl.py", "--home", store.home, *argv], env=env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        self.assertEqual(result.returncode, 1, result.stdout)
        return json.loads(result.stderr)

    def dispatch(self, root, store, name):
        return self.ctl(root, store, "dispatch", "--repo", self.project(name), "--brief", self.brief(), "--harness", "codex", "--approved",
                        env={"FAKE_PARENT_CWD": str(root.resolve())})

    def show_execution(self, root, store, task):
        shown = self.ctl(root, store, "execution", "show", task["id"])
        saved = store.read(task["id"])
        self.assertEqual(shown["task"], task["id"])
        self.assertEqual(shown["execution"]["worker"]["id"], saved["execution"]["worker"]["id"])
        return shown

    def stop_worker(self, task):
        self.pane_state(task["pane"], agent=None, agent_status="done")

    def test_park_releases_a_verified_exited_worker_without_losing_returns(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        task = self.dispatch(root, store, "parked")
        attempt = self.show_execution(root, store, task)["execution"]["worker"]["id"]
        question = self.ctl(root, store, "ask", task["id"], "--key", "decision", "--text", "Need a decision?", env=env)["question"]
        self.ctl(root, store, "report", task["id"], "--text", "Work is parked.", env=env)
        self.stop_worker(task)
        before = len(self.calls())
        parked = self.ctl(root, store, "execution", "park", task["id"], "--attempt", attempt, env=env)
        self.assertEqual(parked["task"], task["id"])
        self.assertFalse(any(call[:2] == ["agent", "start"] for call in self.calls()[before:]))
        self.assertEqual(self.show_execution(root, store, task)["execution"]["worker"]["id"], attempt)
        inbox = self.ctl(root, store, "inbox")
        row = next(row for row in inbox["tasks"] if row["id"] == task["id"])
        self.assertEqual([item["id"] for item in row["questions"]], [question["id"]])
        self.assertTrue(row["report_available"])
        self.dispatch(root, store, "admitted-after-park")
        self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 1)

    def test_park_never_releases_idle_missing_uncertain_or_background_workers(self):
        for name in ("idle", "missing", "uncertain", "background"):
            with self.subTest(worker=name):
                root, store = self.installation(f"execution {name}")
                env = {"FAKE_PARENT_CWD": str(root.resolve())}
                self.ctl(root, store, "settings", "set", "--global", "1", env=env)
                task = self.dispatch(root, store, name)
                attempt = self.show_execution(root, store, task)["execution"]["worker"]["id"]
                match name:
                    case "idle":
                        self.pane_state(task["pane"], agent_status="idle")
                    case "missing":
                        self.pane_state(task["pane"], remove=True)
                    case "background":
                        self.stop_worker(task)
                        self.lsof({"pid": 7777, "cwd": task["worktree"]})
                    case "uncertain":
                        pass
                failed_env = {**env, **({"FAKE_SESSION": "elsewhere"} if name == "uncertain" else {})}
                self.ctl(root, store, "execution", "park", task["id"], "--attempt", attempt, env=failed_env, ok=False)
                self.assertEqual(store.read(task["id"])["execution"]["worker"]["id"], attempt)
                self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 1)
                self.ctl(root, store, "prepare", "--repo", self.project(f"blocked-{name}"), "--brief", self.brief(), "--harness", "codex", "--approved", env=env, ok=False)

    def test_stale_attempt_cannot_park_or_resume_a_new_worker(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        task = self.dispatch(root, store, "stale")
        attempt = self.show_execution(root, store, task)["execution"]["worker"]["id"]
        self.stop_worker(task)
        self.ctl(root, store, "execution", "park", task["id"], "--attempt", attempt, env=env)
        stopped = store.read(task["id"])["execution"]["worker"]
        resumed = self.ctl(root, store, "execution", "resume", task["id"], "--attempt", attempt, env=env)
        self.assertEqual(resumed["task"], task["id"])
        history = [row["attempt"] for row in store.read(task["id"]).get("evidence", []) if row["kind"] == "execution"]
        self.assertEqual(history, [stopped])
        current = self.show_execution(root, store, task)["execution"]["worker"]["id"]
        self.assertNotEqual(current, attempt)
        before = len(self.calls())
        self.ctl(root, store, "execution", "park", task["id"], "--attempt", attempt, env=env, ok=False)
        self.ctl(root, store, "execution", "resume", task["id"], "--attempt", attempt, env=env, ok=False)
        self.assertFalse(any(call[:2] == ["agent", "start"] for call in self.calls()[before:]))
        self.assertEqual(store.read(task["id"])["execution"]["worker"]["id"], current)

    def test_resume_refuses_before_launch_when_another_task_holds_capacity(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        task = self.dispatch(root, store, "resume")
        attempt = self.show_execution(root, store, task)["execution"]["worker"]["id"]
        self.stop_worker(task)
        self.ctl(root, store, "execution", "park", task["id"], "--attempt", attempt, env=env)
        self.dispatch(root, store, "occupant")
        before = len(self.calls())
        self.ctl(root, store, "execution", "resume", task["id"], "--attempt", attempt, env=env, ok=False)
        self.assertFalse(any(call[:2] == ["agent", "start"] for call in self.calls()[before:]))
        self.assertEqual(store.read(task["id"])["execution"]["worker"]["id"], attempt)

    def test_archive_refuses_a_held_reservation_and_old_status_writes_do_not_erase_occupancy(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        task = self.dispatch(root, store, "archive-held")

        refused = self.ctl(root, store, "archive", task["id"], "--acknowledge", env=env, ok=False)
        self.assertIn("execution reservation", refused["error"])

        saved = store.read(task["id"])
        saved["status"] = "archived"
        store.save(saved)
        self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 1)
        blocked = self.ctl(root, store, "prepare", "--repo", self.project("blocked-by-old-write"), "--brief", self.brief(),
                           "--harness", "codex", "--approved", env=env, ok=False)
        self.assertIn("1 of 1 global execution slots", blocked["error"])

    def test_legacy_and_malformed_records_are_conservative(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        task = self.dispatch(root, store, "legacy")
        saved = store.read(task["id"])
        saved.pop("execution")
        store.save(saved)
        self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 1)
        legacy_attempt = self.ctl(root, store, "execution", "show", task["id"])["execution"]["worker"]["id"]
        self.assertEqual(legacy_attempt, f"legacy:{task['id']}")
        self.stop_worker(task)
        self.ctl(root, store, "execution", "park", task["id"], "--attempt", legacy_attempt, env=env)
        self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 0)

        saved = store.read(task["id"])
        saved["execution"] = {"schema": 1, "worker": {"state": "released"}, "verifiers": []}
        store.save(saved)
        refused = self.ctl(root, store, "prepare", "--repo", self.project("malformed-blocked"), "--brief", self.brief(),
                           "--harness", "codex", "--approved", env=env, ok=False)
        self.assertIn("Malformed execution reservation", refused["error"])

    def test_concurrent_resume_launches_exactly_one_successor(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(root.resolve())}
        self.ctl(root, store, "settings", "set", "--global", "1", env=env)
        task = self.dispatch(root, store, "concurrent-resume")
        attempt = self.show_execution(root, store, task)["execution"]["worker"]["id"]
        self.stop_worker(task)
        self.ctl(root, store, "execution", "park", task["id"], "--attempt", attempt, env=env)
        before = sum(1 for call in self.calls() if call[:2] == ["agent", "start"])

        def resume():
            return self.cli([sys.executable, root / "lib/sumctl.py", "--home", store.home, "execution", "resume", task["id"], "--attempt", attempt], env=env)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(lambda _: resume(), range(2)))
        self.assertEqual(sorted(result.returncode for result in results), [0, 1])
        self.assertEqual(sum(1 for call in self.calls() if call[:2] == ["agent", "start"]), before + 1)
        self.assertEqual(self.ctl(root, store, "settings", "show")["occupied"]["global"], 1)
