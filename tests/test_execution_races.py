from __future__ import annotations

import subprocess
import sys
import unittest
from unittest import mock

import test_root_verification as fixture


class ExecutionRaceTest(unittest.TestCase):
    def setUp(self):
        self.lab = fixture.RootVerificationTest("runTest")
        self.addCleanup(self.lab.doCleanups)
        self.lab.setUp()

    def test_verifier_cannot_be_parked_between_reservation_and_checkout_creation(self):
        task = self.lab.prepare()
        original_run = fixture.sumctl.run
        observed = []

        def interleave(argv, **kwargs):
            if list(argv[:2]) == ["git", "-C"] and list(argv[3:5]) == ["worktree", "add"]:
                attempt = self.lab.store.read(task["id"])["execution"]["verifiers"][-1]
                result = subprocess.run(
                    [sys.executable, str(fixture.ROOT / "lib/sumctl.py"), "--home", str(self.lab.store.home),
                     "execution", "park", task["id"], "--attempt", attempt["id"]],
                    text=True, capture_output=True, timeout=30,
                )
                observed.append(result.returncode)
                self.assertNotEqual(result.returncode, 0, f"In-flight verifier was released: {result.stdout}")
            return original_run(argv, **kwargs)

        with mock.patch.object(fixture.sumctl, "run", side_effect=interleave):
            self.lab.verify(task, task["base_sha"], execute=True)
        self.assertEqual(len(observed), 1)

    def test_park_recovers_after_its_observer_process_exits(self):
        task = self.lab.prepare()
        attempt = self.lab.store.read(task["id"])["execution"]["worker"]["id"]
        child = subprocess.run(
            [sys.executable, "-c",
             "import os, sys, sumctl; sumctl._park_observation = lambda *args: os._exit(73); "
             "sys.exit(sumctl.main(sys.argv[1:]))",
             "--home", str(self.lab.store.home), "execution", "park", task["id"], "--attempt", attempt],
            cwd=fixture.ROOT / "lib", text=True, capture_output=True, timeout=30,
        )
        self.assertEqual(child.returncode, 73, child.stderr)
        self.assertEqual(self.lab.store.read(task["id"])["execution"]["worker"]["state"], "observing")
        result = subprocess.run(
            [sys.executable, str(fixture.ROOT / "lib/sumctl.py"), "--home", str(self.lab.store.home),
             "execution", "park", task["id"], "--attempt", attempt],
            text=True, capture_output=True, timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.lab.store.read(task["id"])["execution"]["worker"]["state"], "released")

    def test_live_observer_keeps_exclusive_ownership_of_park(self):
        task = self.lab.prepare()
        attempt = self.lab.store.read(task["id"])["execution"]["worker"]["id"]
        original_observe = fixture.sumctl._park_observation
        observed = []

        def interleave(store, current):
            result = subprocess.run(
                [sys.executable, str(fixture.ROOT / "lib/sumctl.py"), "--home", str(store.home),
                 "execution", "park", task["id"], "--attempt", attempt],
                text=True, capture_output=True, timeout=30,
            )
            observed.append(result.returncode)
            self.assertEqual(result.returncode, 1, result.stdout)
            self.assertEqual(store.read(task["id"])["execution"]["worker"]["state"], "observing")
            return original_observe(store, current)

        with mock.patch.object(fixture.sumctl, "_park_observation", side_effect=interleave):
            result = fixture.sumctl.execution_park(self.lab.store, task["id"], attempt)
        self.assertEqual(observed, [1])
        self.assertTrue(result["released"])
