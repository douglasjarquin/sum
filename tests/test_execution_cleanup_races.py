from __future__ import annotations

import os
from pathlib import Path
import shutil
import sys
import unittest
from unittest import mock

import test_cleanup as fixture
import test_services as service_fixture


ROOT = Path(__file__).resolve().parents[1]


def standardized_lab(test: unittest.TestCase) -> fixture.CleanupTest:
    lab = fixture.CleanupTest("runTest")
    test.addCleanup(lab.doCleanups)
    lab.setUp()
    for item in (ROOT / "tests/fixtures/verify/cli").iterdir():
        (shutil.copytree if item.is_dir() else shutil.copy2)(item, lab.repo / item.name)
    shutil.copytree(ROOT / ".agents/skills/verify", lab.repo / ".agents/skills/verify")
    lab.git("add", "-A")
    lab.git("commit", "-q", "-m", "standardized fixture")
    bin_dir = lab.root / "bin"
    bin_dir.mkdir()
    (bin_dir / "mise").symlink_to(ROOT / "tests/fixtures/mise.py")
    patch = mock.patch.dict(os.environ, {
        "PATH": os.pathsep.join([str(bin_dir), str(Path(sys.executable).resolve().parent), "/usr/bin", "/bin"]),
        "FAKE_MISE_STOP": str(lab.root),
    })
    patch.start()
    test.addCleanup(patch.stop)
    return lab


class ExecutionCleanupRaceTest(unittest.TestCase):
    def setUp(self):
        self.lab = standardized_lab(self)

    def test_cleanup_cannot_remove_checkout_before_reserved_worker_launch(self):
        task, _ = self.lab.merged_task()
        attempt = task["execution"]["worker"]["id"]
        parked = self.lab.cli("execution", "park", task["id"], "--attempt", attempt)
        self.assertEqual(parked.returncode, 0, parked.stderr)
        original = fixture.sumctl.herdr
        observed = []

        def cleanup_before_launch(argv, **kwargs):
            if argv[:2] == ["agent", "start"]:
                result = self.lab.cli("cleanup", task["id"], "--apply")
                observed.append(result.returncode)
                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertTrue(Path(task["worktree"]).is_dir())
            return original(argv, **kwargs)

        with mock.patch.object(fixture.sumctl, "herdr", side_effect=cleanup_before_launch):
            fixture.sumctl.execution_resume(self.lab.store, task["id"], attempt)
        self.assertEqual(observed, [1])
        self.assertEqual(self.lab.herdr_calls("worktree", "remove"), [])

    def test_competing_cleanup_refuses_without_overwriting_active_intent(self):
        task, _ = self.lab.merged_task()
        original = fixture.sumctl.recheck
        observed = []

        def competing_cleanup(*args, **kwargs):
            intent = self.lab.store.read(task["id"])["cleanup"]["intent"]
            result = self.lab.cli("cleanup", task["id"], "--apply")
            observed.append(result.returncode)
            self.assertEqual(result.returncode, 1, result.stdout)
            self.assertEqual(self.lab.store.read(task["id"])["cleanup"]["intent"], intent)
            return original(*args, **kwargs)

        with mock.patch.object(fixture.sumctl, "recheck", side_effect=competing_cleanup):
            result = self.lab.cleanup(task, apply=True)
        self.assertEqual(observed, [1])
        self.assertEqual(result["state"], "complete")
        self.assertEqual(len(self.lab.herdr_calls("worktree", "remove")), 1)

    def test_live_inbox_does_not_reconcile_an_active_removal(self):
        task, _ = self.lab.merged_task()
        original = fixture.sumctl.herdr_observe
        observed = []

        def inbox_before_removal(argv, **kwargs):
            if argv[:2] == ["worktree", "remove"]:
                record = self.lab.store.read(task["id"])["cleanup"]
                result = self.lab.cli("inbox", "--live")
                self.assertEqual(result.returncode, 0, result.stderr)
                observed.append(result.returncode)
                self.assertEqual(self.lab.store.read(task["id"])["cleanup"], record)
            return original(argv, **kwargs)

        with mock.patch.object(fixture.sumctl, "herdr_observe", side_effect=inbox_before_removal):
            result = self.lab.cleanup(task, apply=True)
        self.assertEqual(observed, [0])
        self.assertEqual(result["state"], "complete")

    def test_cleanup_intent_excludes_resume_before_agent_start(self):
        # Given a merged task whose exact stopped worker attempt was released.
        task, _ = self.lab.merged_task()
        attempt = task["execution"]["worker"]["id"]
        parked = self.lab.cli("execution", "park", task["id"], "--attempt", attempt)
        self.assertEqual(parked.returncode, 0, parked.stderr)
        starts_before = len(self.lab.herdr_calls("agent", "start"))
        original_recheck = fixture.sumctl.recheck
        observed = []

        def resume_after_intent(*args, **kwargs):
            # When a supported resume command races after cleanup persisted its intent.
            result = self.lab.cli("execution", "resume", task["id"], "--attempt", attempt)
            observed.append(result)
            return original_recheck(*args, **kwargs)

        with mock.patch.object(fixture.sumctl, "recheck", side_effect=resume_after_intent):
            result = self.lab.cleanup(task, apply=True)

        # Then resume loses before its native agent-start side effect and cleanup completes.
        self.assertEqual(result["state"], "complete")
        self.assertEqual(len(observed), 1)
        self.assertEqual(observed[0].returncode, 1, observed[0].stdout)
        self.assertIn("cleanup intent", observed[0].stderr)
        self.assertEqual(len(self.lab.herdr_calls("agent", "start")), starts_before)

    def test_cleanup_intent_excludes_root_verification_before_checkout_creation(self):
        # Given a standardized merged task ready for guarded cleanup.
        task, candidate = self.lab.merged_task()
        worktree_adds_before = self.lab.git("worktree", "list", "--porcelain").count("worktree ")
        original_recheck = fixture.sumctl.recheck
        observed = []

        def verify_after_intent(*args, **kwargs):
            # When a supported root verification command races after cleanup persisted its intent.
            result = self.lab.cli("verify", task["id"], "--candidate", candidate, "--execute")
            observed.append(result)
            return original_recheck(*args, **kwargs)

        with mock.patch.object(fixture.sumctl, "recheck", side_effect=verify_after_intent):
            result = self.lab.cleanup(task, apply=True)

        # Then verification loses before creating its detached checkout and cleanup completes.
        self.assertEqual(result["state"], "complete")
        self.assertEqual(len(observed), 1)
        self.assertEqual(observed[0].returncode, 1, observed[0].stdout)
        self.assertIn("cleanup intent", observed[0].stderr)
        self.assertEqual(self.lab.git("worktree", "list", "--porcelain").count("worktree "), worktree_adds_before - 1)
        self.assertEqual(self.lab.store.read(task["id"])["execution"]["verifiers"], [])

    def test_cleanup_release_refuses_changed_attempt_generation(self):
        # Given cleanup intent bound to one exact worker attempt and generation.
        task, _ = self.lab.merged_task()
        expected = fixture.sumctl.execution_stop_proof(task)
        with self.lab.store.lock():
            current = self.lab.store.read(task["id"])
            current["cleanup"] = {"schema": fixture.sumctl.CLEANUP_SCHEMA, "state": "removing", "history": [],
                                  "intent": {"attempts": expected}}
            worker = fixture.sumctl.reservations.worker(current)
            fixture.sumctl.reservations.transition(current, worker["id"], "uncertain", fixture.sumctl.now())
            self.lab.store.save(current)

        # When interrupted-cleanup reconciliation sees a later generation.
        with self.assertRaisesRegex(fixture.sumctl.SumError, "exact execution attempts and generations"):
            fixture.sumctl.release_cleanup_reservations(self.lab.store, task["id"], {"workspace": "absent"})

        # Then it does not release the changed attempt as cleanup's stopped process.
        worker = self.lab.store.read(task["id"])["execution"]["worker"]
        self.assertEqual((worker["state"], worker["generation"]), ("uncertain", expected[0]["generation"] + 1))

    def test_new_execution_wins_before_cleanup_intent_without_native_removal(self):
        # Given cleanup inspected a released worker but has not persisted destructive intent.
        task, _ = self.lab.merged_task()
        attempt = task["execution"]["worker"]["id"]
        parked = self.lab.cli("execution", "park", task["id"], "--attempt", attempt)
        self.assertEqual(parked.returncode, 0, parked.stderr)
        original = fixture.sumctl.save_cleanup_intent
        observed = []

        def resume_before_intent(*args, **kwargs):
            # When the supported resume command reserves and launches first.
            observed.append(self.lab.cli("execution", "resume", task["id"], "--attempt", attempt))
            return original(*args, **kwargs)

        with mock.patch.object(fixture.sumctl, "save_cleanup_intent", side_effect=resume_before_intent):
            with self.assertRaisesRegex(fixture.sumctl.SumError, "execution attempts changed after inspection"):
                self.lab.cleanup(task, apply=True)

        # Then cleanup loses before native removal and leaves no active cleanup intent.
        self.assertEqual(observed[0].returncode, 0, observed[0].stderr)
        self.assertTrue(Path(task["worktree"]).is_dir())
        self.assertEqual(self.lab.herdr_calls("worktree", "remove"), [])
        saved = self.lab.store.read(task["id"])
        self.assertEqual((saved["cleanup"]["state"], saved["cleanup"].get("intent")), ("blocked", None))

    def test_recheck_refusal_releases_cleanup_exclusion(self):
        # Given cleanup owns intent for a released worker but its final recheck finds a blocker.
        task, _ = self.lab.merged_task()
        attempt = task["execution"]["worker"]["id"]
        parked = self.lab.cli("execution", "park", task["id"], "--attempt", attempt)
        self.assertEqual(parked.returncode, 0, parked.stderr)
        blocked = {"blockers": [{"code": "occupant", "detail": "a writer appeared"}], "resources": {"workspace": "present"}}

        # When cleanup returns the conclusively pre-removal refusal.
        with mock.patch.object(fixture.sumctl, "recheck", return_value=blocked):
            with self.assertRaisesRegex(fixture.sumctl.SumError, "refused at the recheck"):
                self.lab.cleanup(task, apply=True)

        # Then a later approved resume is not permanently excluded by stale cleanup intent.
        resumed = self.lab.cli("execution", "resume", task["id"], "--attempt", attempt)
        self.assertEqual(resumed.returncode, 0, resumed.stderr)
        self.assertEqual(self.lab.store.read(task["id"])["cleanup"].get("intent"), None)

    def test_cleanup_intent_excludes_service_before_pane_split(self):
        # Given a discovered service command and active cleanup intent bound to its worker generation.
        lab = service_fixture.ServiceTest("runTest")
        self.addCleanup(lab.doCleanups)
        lab.setUp()
        task = lab.discovered(lab.prepare())
        proof = service_fixture.sumctl.execution_stop_proof(task)
        service_fixture.sumctl.save_cleanup_intent(lab.store, task["id"], {"workspace": task["workspace"]}, proof, {})
        splits_before = len(lab.calls_of("pane", "split"))

        # When the supported env start command races after cleanup intent.
        error = lab.start(task, ok=False)

        # Then it loses before creating or running a service pane.
        self.assertIn("cleanup intent", error["error"])
        self.assertEqual(len(lab.calls_of("pane", "split")), splits_before)


if __name__ == "__main__":
    unittest.main()
