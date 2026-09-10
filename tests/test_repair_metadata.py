from __future__ import annotations

from copy import deepcopy
import unittest

import test_repairs


class RepairMetadataTest(unittest.TestCase):
    def setUp(self):
        self.lab = test_repairs.RepairTest()
        self.lab.setUp()
        self.addCleanup(self.lab.doCleanups)

    def test_malformed_operation_records_refuse_before_delivery_without_mutation(self):
        lab = self.lab
        lab.send("first")
        lab.pane_state(lab.task["pane"], agent_status="idle")
        baseline = lab.store.read(lab.task["id"])
        malformed = []
        null_record = deepcopy(baseline)
        null_record["repairs"] = None
        malformed.append(("null-ledger", null_record))
        boolean_schema = deepcopy(baseline)
        boolean_schema["repairs"]["schema"] = True
        malformed.append(("boolean-schema", boolean_schema))
        for field, value in (("created_at", "not-a-timestamp"), ("key", ""), ("state", []),
                             ("id", "r-invalid"), ("attempt", "x-invalid")):
            record = deepcopy(baseline)
            record["repairs"]["operations"][0][field] = value
            malformed.append((field, record))
        duplicate = deepcopy(baseline)
        extra = deepcopy(duplicate["repairs"]["operations"][0])
        extra["key"] = "different-key"
        duplicate["repairs"]["operations"].append(extra)
        duplicate["repairs"]["consumed"] = 2
        malformed.append(("duplicate-operation-id", duplicate))
        for name, record in malformed:
            with self.subTest(field=name):
                lab.pane_state(lab.task["pane"], agent_status="idle")
                lab.store.save(record)
                before = lab.store.read(lab.task["id"])
                prompts = lab.prompt_count()
                lab.send("next", ok=False)
                self.assertEqual(lab.store.read(lab.task["id"]), before)
                self.assertEqual(lab.prompt_count(), prompts)

    def test_malformed_grant_decision_returns_a_structured_refusal(self):
        lab = self.lab
        question = lab.exhausted_question()
        lab.ctl(lab.install, lab.store, "repair", "extend", lab.task["id"], "--question", question,
                "--additional", "1", "--approved", "--text", "One additional iteration approved.", env=lab.env)
        record = lab.store.read(lab.task["id"])
        record["questions"][0]["decision"] = []
        lab.store.save(record)
        before = lab.store.read(lab.task["id"])
        lab.send("next", ok=False)
        self.assertEqual(lab.store.read(lab.task["id"]), before)
