from __future__ import annotations

from pathlib import Path

import test_fleet


class RepairTest(test_fleet.FleetLab):
    def setUp(self):
        super().setUp()
        self.install, self.store = self.installation()
        self.env = {"FAKE_PARENT_CWD": str(self.install.resolve())}
        self.ctl(self.install, self.store, "settings", "set", "--global", "4", env=self.env)
        self.task = self.ctl(
            self.install,
            self.store,
            "dispatch",
            "--repo",
            self.project("repair-target"),
            "--brief",
            self.brief(),
            "--harness",
            "codex",
            "--approved",
            env=self.env,
        )
        self.pane_state(self.task["pane"], agent_status="idle")

    def send(self, key, *, ok=True):
        attempt = self.store.read(self.task["id"])["execution"]["worker"]["id"]
        return self.ctl(
            self.install,
            self.store,
            "repair",
            "send",
            self.task["id"],
            "--attempt",
            attempt,
            "--key",
            key,
            "--text",
            "Apply the bounded correction.",
            env=self.env,
            ok=ok,
        )

    def prompt_count(self):
        return sum(1 for call in self.calls() if call[:2] == ["agent", "prompt"])

    def test_resume_cannot_launch_after_the_repair_allowance_is_consumed(self):
        self.send("repair-1")
        self.pane_state(self.task["pane"], agent_status="idle")
        self.send("repair-2")
        attempt = self.store.read(self.task["id"])["execution"]["worker"]["id"]
        self.pane_state(self.task["pane"], agent=None, agent_status="done")
        env = {**self.env, "SUM_LSOF_BIN": str(Path(__file__).parent / "fixtures/lsof.py"),
               "FAKE_LSOF_ROOT": str(self.root / "fake-lsof")}
        self.ctl(self.install, self.store, "execution", "park", self.task["id"], "--attempt", attempt, env=env)
        before = len(self.calls())

        refused = self.ctl(self.install, self.store, "execution", "resume", self.task["id"],
                           "--attempt", attempt, env=env, ok=False)

        self.assertIn("allowance is exhausted", refused["error"])
        self.assertFalse(any(call[:2] == ["agent", "start"] for call in self.calls()[before:]))
        self.assertEqual(self.store.read(self.task["id"])["execution"]["worker"]["id"], attempt)

    def test_each_new_resume_consumes_one_iteration(self):
        env = {**self.env, "SUM_LSOF_BIN": str(Path(__file__).parent / "fixtures/lsof.py"),
               "FAKE_LSOF_ROOT": str(self.root / "fake-lsof")}
        for _ in range(2):
            attempt = self.store.read(self.task["id"])["execution"]["worker"]["id"]
            self.pane_state(self.task["pane"], agent=None, agent_status="done")
            self.ctl(self.install, self.store, "execution", "park", self.task["id"], "--attempt", attempt, env=env)
            self.ctl(self.install, self.store, "execution", "resume", self.task["id"], "--attempt", attempt, env=env)
        self.assertEqual(self.store.read(self.task["id"]).get("repairs", {}).get("consumed"), 2)

    def exhausted_question(self):
        self.send("repair-1")
        self.pane_state(self.task["pane"], agent_status="idle")
        self.send("repair-2")
        self.pane_state(self.task["pane"], agent_status="idle")
        self.send("repair-3", ok=False)
        return self.store.read(self.task["id"])["questions"][-1]["id"]

    def test_explicit_grant_allows_only_the_added_iteration_and_is_idempotent(self):
        question = self.exhausted_question()
        args = ("repair", "extend", self.task["id"], "--question", question, "--additional", "1",
                "--approved", "--text", "The user approved one additional correction.")
        granted = self.ctl(self.install, self.store, *args, env=self.env)
        repeated = self.ctl(self.install, self.store, *args, env=self.env)
        self.send("repair-3")
        self.pane_state(self.task["pane"], agent_status="idle")
        self.send("repair-4", ok=False)

        self.assertFalse(granted["duplicate"])
        self.assertTrue(repeated["duplicate"])
        saved = self.store.read(self.task["id"])
        self.assertEqual(saved["repairs"]["consumed"], 3)
        self.assertEqual(len(saved["repairs"]["grants"]), 1)
        self.assertEqual(saved["questions"][0]["status"], "answered")
        self.assertEqual(saved["questions"][-1]["status"], "open")

    def test_worker_and_unapproved_calls_cannot_extend_allowance(self):
        question = self.exhausted_question()
        args = ("repair", "extend", self.task["id"], "--question", question, "--additional", "1",
                "--text", "An answer is not authorization.")
        before = self.store.read(self.task["id"])
        self.ctl(self.install, self.store, *args, env=self.env, ok=False)
        self.ctl(self.install, self.store, *args, "--approved",
                 env={**self.env, "HERDR_PANE_ID": self.task["pane"]}, ok=False)
        self.assertEqual(self.store.read(self.task["id"]), before)

    def test_answer_alone_grants_nothing_but_explicit_confirmation_can_apply_it(self):
        question = self.exhausted_question()
        decision = "The user approved one additional correction."
        self.ctl(self.install, self.store, "answer", self.task["id"], question, "--text", decision, env=self.env)
        self.send("repair-3", ok=False)
        self.assertEqual(self.store.read(self.task["id"])["repairs"]["grants"], [])

        self.ctl(self.install, self.store, "repair", "extend", self.task["id"], "--question", question,
                 "--additional", "1", "--approved", "--text", decision, env=self.env)

        self.pane_state(self.task["pane"], agent_status="idle")
        self.send("repair-3")
        self.assertEqual(self.store.read(self.task["id"])["repairs"]["consumed"], 3)

    def test_budget_decision_does_not_reuse_an_ordinary_question_key(self):
        ordinary = self.ctl(self.install, self.store, "ask", self.task["id"], "--key", "repair-allowance-2",
                            "--text", "Keep this unrelated question?", env=self.env)["question"]
        self.exhausted_question()

        questions = self.store.read(self.task["id"])["questions"]

        self.assertEqual(questions[0], ordinary)
        self.assertEqual(len(questions), 2)
        self.assertEqual(questions[1]["decision"], {"kind": "repair-allowance", "allowance": 2})

    def test_instruction_key_cannot_collide_with_a_generated_resume_operation(self):
        attempt = self.store.read(self.task["id"])["execution"]["worker"]["id"]
        self.send("resume-" + attempt)
        self.pane_state(self.task["pane"], agent=None, agent_status="done")
        env = {**self.env, "SUM_LSOF_BIN": str(Path(__file__).parent / "fixtures/lsof.py"),
               "FAKE_LSOF_ROOT": str(self.root / "fake-lsof")}
        self.ctl(self.install, self.store, "execution", "park", self.task["id"], "--attempt", attempt, env=env)

        resumed = self.ctl(self.install, self.store, "execution", "resume", self.task["id"], "--attempt", attempt, env=env)

        self.assertEqual(resumed["status"], "running")
        self.assertEqual(resumed["repairs"]["consumed"], 2)
        self.assertEqual(len(resumed["repairs"]["operations"]), 2)

    def test_two_corrections_deliver_once_each_and_third_creates_one_exhaustion_question(self):
        before = self.prompt_count()
        first = self.send("repair-1")
        self.pane_state(self.task["pane"], agent_status="idle")
        second = self.send("repair-2")
        self.pane_state(self.task["pane"], agent_status="idle")
        refused = self.send("repair-3", ok=False)
        repeated = self.send("repair-3", ok=False)

        self.assertEqual((first["operation"]["state"], second["operation"]["state"]), ("submitted", "submitted"))
        self.assertEqual(self.prompt_count(), before + 2)
        self.assertIn("allowance is exhausted", refused["error"])
        self.assertEqual(repeated["error"], refused["error"])
        saved = self.store.read(self.task["id"])
        self.assertEqual((saved["repairs"]["default_allowance"], saved["repairs"]["consumed"]), (2, 2))
        budget_questions = [q for q in saved["questions"] if q.get("decision", {}).get("kind") == "repair-allowance"]
        self.assertEqual(len(budget_questions), 1)


if __name__ == "__main__":
    import unittest

    unittest.main()
