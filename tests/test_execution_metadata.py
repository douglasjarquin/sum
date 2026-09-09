from __future__ import annotations

from copy import deepcopy
import json
import sys
import unittest

import test_execution as fixture
from test_core import sumctl


class ExecutionMetadataTest(unittest.TestCase):
    def setUp(self):
        self.lab = fixture.ExecutionTest("runTest")
        self.addCleanup(self.lab.doCleanups)
        self.lab.setUp()
        self.root, self.store = self.lab.installation()
        self.env = {"FAKE_PARENT_CWD": str(self.root.resolve())}
        self.task = self.lab.dispatch(self.root, self.store, "metadata")

    def malformed_verifier(self, field, value, *, state="uncertain"):
        saved = self.store.read(self.task["id"])
        verifier = sumctl.reservations.new_attempt(
            "verifier",
            saved["execution"]["worker"]["owner"],
            None,
            sumctl.now(),
            state=state,
        )
        verifier[field] = value
        saved["execution"]["verifiers"] = [verifier]
        self.store.save(saved)
        return verifier

    def test_malformed_metadata_refuses_prepare_before_worktree_creation(self):
        for index, (field, value) in enumerate((
            ("occupant", "not-an-object"),
            ("candidate", ["not", "a", "sha"]),
            ("observations", ["not-an-object"]),
            ("created_at", {"bad": True}),
            ("updated_at", []),
            ("created_at", "not-a-timestamp"),
            ("owner", {"machine": "", "session": "", "pane": None}),
            ("owner", {"machine": "main", "session": "sum-test", "pane": ""}),
            ("checkout", ""),
        )):
            with self.subTest(field=field):
                verifier = self.malformed_verifier(field, value, state="released")
                before = self.lab.calls()

                result = self.lab.cli(
                    [
                        sys.executable,
                        self.root / "lib/sumctl.py",
                        "--home",
                        self.store.home,
                        "prepare",
                        "--repo",
                        self.lab.project(f"blocked-{index}"),
                        "--brief",
                        self.lab.brief(),
                        "--harness",
                        "codex",
                        "--approved",
                    ],
                    env=self.env,
                )

                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertIn("Malformed execution reservation", result.stderr)
                self.assertEqual(self.lab.calls(), before)
                self.assertEqual(self.store.read(self.task["id"])["execution"]["verifiers"], [verifier])

    def test_malformed_metadata_refuses_verifier_park_before_release(self):
        for field, value in (
            ("occupant", "not-an-object"),
            ("candidate", ["not", "a", "sha"]),
            ("observations", ["not-an-object"]),
            ("created_at", {"bad": True}),
            ("updated_at", []),
            ("created_at", "not-a-timestamp"),
            ("owner", {"machine": "", "session": "", "pane": None}),
            ("owner", {"machine": "main", "session": "sum-test", "pane": ""}),
            ("checkout", ""),
        ):
            with self.subTest(field=field):
                verifier = self.malformed_verifier(field, value)
                before = deepcopy(self.store.read(self.task["id"])["execution"])

                result = self.lab.cli(
                    [
                        sys.executable,
                        self.root / "lib/sumctl.py",
                        "--home",
                        self.store.home,
                        "execution",
                        "park",
                        self.task["id"],
                        "--attempt",
                        verifier["id"],
                    ],
                    env=self.env,
                )

                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertIn("Malformed execution reservation", result.stderr)
                self.assertEqual(self.store.read(self.task["id"])["execution"], before)

    def test_valid_worker_and_verifier_writer_shapes_remain_supported(self):
        saved = self.store.read(self.task["id"])
        worker = saved["execution"]["worker"]
        worker["occupant"] = {
            "machine": "host",
            "session": "session",
            "pane": "w-worker:p1",
            "checkout": "/tmp/worker",
            "harness": "codex",
            "name": None,
            "shell_pid": None,
            "pid": None,
            "argv": None,
        }
        worker["observations"] = [{"at": sumctl.now(), "outcome": "occupant-uncertain", "reason": "probe"}]
        verifier = sumctl.reservations.new_attempt("verifier", worker["owner"], "/tmp/verifier", sumctl.now(), state="uncertain", candidate="a" * 40)
        verifier["occupant"] = {"machine": "host", "pid": 999999, "argv": [sys.executable, "runner.py"], "checkout": "/tmp/verifier"}
        verifier["observations"] = [{"at": sumctl.now(), "outcome": "stopped", "pid": 999999, "checkout": "/tmp/verifier", "checkout_present": False}]
        saved["execution"]["verifiers"] = [verifier]
        self.store.save(saved)

        result = self.lab.cli(
            [sys.executable, self.root / "lib/sumctl.py", "--home", self.store.home, "execution", "park", self.task["id"], "--attempt", verifier["id"]],
            env=self.env,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)["released"])
        self.assertEqual(self.store.read(self.task["id"])["execution"]["verifiers"][0]["state"], "released")
