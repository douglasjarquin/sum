from __future__ import annotations

from copy import deepcopy
import os
import sys
import unittest
from unittest import mock

import test_repairs


class RepairLifecycleTest(unittest.TestCase):
    def setUp(self):
        self.lab = test_repairs.RepairTest()
        self.addCleanup(self.lab.doCleanups)
        self.lab.setUp()

    def test_uncertain_delivery_remains_charged_and_is_not_replayed(self):
        lab = self.lab
        lab.env["FAKE_FAIL_PROMPT"] = "1"
        lab.send("uncertain", ok=False)
        del lab.env["FAKE_FAIL_PROMPT"]
        prompts = lab.prompt_count()

        repeated = lab.send("uncertain")

        self.assertTrue(repeated["duplicate"])
        self.assertEqual(repeated["operation"]["state"], "uncertain")
        self.assertEqual(lab.store.read(lab.task["id"])["repairs"]["consumed"], 1)
        self.assertEqual(lab.prompt_count(), prompts)

    def test_process_exit_after_intent_never_replays_the_instruction(self):
        lab = self.lab
        attempt = lab.store.read(lab.task["id"])["execution"]["worker"]["id"]
        script = """import os, sys
sys.path.insert(0, sys.argv[1])
import sumctl
original = sumctl.herdr
def crash(args, **kwargs):
    if args[:2] == ["agent", "prompt"]:
        os._exit(73)
    return original(args, **kwargs)
sumctl.herdr = crash
sys.exit(sumctl.main(sys.argv[2:]))
"""
        result = lab.cli([sys.executable, "-c", script, lab.install / "lib", "--home", lab.store.home,
                          "repair", "send", lab.task["id"], "--attempt", attempt, "--key", "crashed",
                          "--text", "Apply the bounded correction."], env=lab.env)
        self.assertEqual(result.returncode, 73, result.stderr)
        prompts = lab.prompt_count()

        repeated = lab.send("crashed")

        self.assertTrue(repeated["duplicate"])
        self.assertEqual(repeated["operation"]["state"], "in-flight")
        self.assertEqual(lab.store.read(lab.task["id"])["repairs"]["consumed"], 1)
        self.assertEqual(lab.prompt_count(), prompts)
        lab.send("explicit-new-iteration")
        self.assertEqual(lab.store.read(lab.task["id"])["repairs"]["consumed"], 2)

    def test_candidate_callbacks_refresh_and_harness_change_preserve_exhaustion(self):
        lab = self.lab
        question = lab.exhausted_question()
        before = deepcopy(lab.store.read(lab.task["id"])["repairs"])
        lab.git("commit", "--allow-empty", "-q", "-m", "new candidate", cwd=lab.task["worktree"])
        candidate = lab.git("rev-parse", "HEAD", cwd=lab.task["worktree"])
        lab.ctl(lab.install, lab.store, "report", lab.task["id"], "--text", "New candidate ready.", env=lab.env)
        lab.ctl(lab.install, lab.store, "verify", lab.task["id"], "--candidate", candidate,
                "--result", "pass", "--text", "Fixture verification receipt.", env=lab.env)
        lab.ctl(lab.install, lab.store, "brief", "regenerate", lab.task["id"], env=lab.env)
        changed = lab.store.read(lab.task["id"])
        changed["harness"] = "claude"
        changed["launch"]["harness"] = "claude"
        lab.store.save(changed)

        lab.send("third", ok=False)

        saved = lab.store.read(lab.task["id"])
        self.assertEqual(saved["repairs"], before)
        self.assertEqual([q["id"] for q in saved["questions"]], [question])
        self.assertEqual(saved["report"]["text"], "New candidate ready.")

    def test_update_and_rollback_preserve_consumed_iterations(self):
        lab = self.lab
        question = lab.exhausted_question()
        before = deepcopy(lab.store.read(lab.task["id"])["repairs"])
        with mock.patch.dict(os.environ, lab.env):
            first = lab.commit_upstream(lab.install, "first-release.txt")
            lab.apply(lab.store)
            lab.commit_upstream(lab.install, "second-release.txt")
            lab.apply(lab.store)
        lab.ctl(lab.install, lab.store, "update", "rollback", "--to", first, env=lab.env)

        lab.send("after-rollback", ok=False)

        saved = lab.store.read(lab.task["id"])
        self.assertEqual(saved["repairs"], before)
        self.assertEqual([q["id"] for q in saved["questions"]], [question])
        self.assertEqual(lab.current(lab.install).name, first)
