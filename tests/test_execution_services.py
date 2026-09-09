from __future__ import annotations

import argparse
import unittest
from unittest import mock

import test_cleanup as cleanup_fixture
import test_services as service_fixture


class ExecutionServiceTest(unittest.TestCase):
    def test_park_during_service_inspection_prevents_launch(self):
        lab = service_fixture.ServiceTest("runTest")
        self.addCleanup(lab.doCleanups)
        lab.setUp()
        task = lab.discovered(lab.prepare())
        attempt = lab.store.read(task["id"])["execution"]["worker"]["id"]
        original = service_fixture.sumctl.unrecorded_panes
        observed = []

        def interleave(current, environment):
            result = lab.cli("execution", "park", task["id"], "--attempt", attempt)
            self.assertEqual(result.returncode, 0, result.stderr)
            observed.append(result.returncode)
            return original(current, environment)

        args = argparse.Namespace(task=task["id"], declared="dev", source=None, timeout=1, url=None, match=None, label=None)
        before = len(lab.calls_of("pane", "split"))
        with mock.patch.object(service_fixture.sumctl, "unrecorded_panes", side_effect=interleave):
            with self.assertRaises(service_fixture.sumctl.SumError):
                service_fixture.sumctl.env_start(lab.store, args)
        self.assertEqual(observed, [0])
        self.assertEqual(len(lab.calls_of("pane", "split")), before)
        self.assertEqual(lab.store.read(task["id"])["execution"]["worker"]["state"], "released")

    def test_cleanup_records_release_for_the_exact_worker_attempt(self):
        lab = cleanup_fixture.CleanupTest("runTest")
        self.addCleanup(lab.doCleanups)
        lab.setUp()
        task, _ = lab.merged_task()
        attempt = lab.store.read(task["id"])["execution"]["worker"]["id"]
        lab.cleanup(task, apply=True)
        saved = lab.store.read(task["id"])
        worker = saved["execution"]["worker"]
        self.assertEqual((saved["status"], worker["id"], worker["state"]), ("archived", attempt, "released"))
        self.assertEqual(worker["observations"][-1]["outcome"], "cleanup-stopped")
