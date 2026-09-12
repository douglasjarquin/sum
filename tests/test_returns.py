"""Issue #13: pending returns are preserved, coalesced per recipient, and reported with honest delivery status."""
from __future__ import annotations
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import unittest
from unittest import mock

import test_core

sumctl = test_core.sumctl


class ReturnsTest(unittest.TestCase):
    for _name in ("setUp", "init", "pane", "cli", "git", "prepare", "question", "calls", "pane_state", "prompts", "started_task",
                  "versions", "legacy_helper", "legacy_cli", "refresh"):
        locals()[_name] = getattr(test_core.CoreTest, _name)
    del _name

    def answer(self, task, question, text="Yes."):
        return sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=question["id"], text=text, file=None))

    def returns(self, task):
        return sumctl.returns_view(self.store, self.store.read(task["id"]))

    def open_ids(self, task):
        return sorted(o["id"] for o in self.returns(task)["open"])

    def states(self, task):
        return {o["id"]: o["notification"]["state"] for o in self.returns(task)["open"]}

    def pump(self, **kw):
        return sumctl.pump(self.store, sumctl.context(), **kw)

    def remote_pump(self, **kw):
        return sumctl.pump(self.store, None, inline=False, **kw)

    def second_repo(self, name="second-repo"):
        second = self.root / name
        subprocess.run(["git", "clone", "--quiet", str(self.repo), str(second)], check=True)
        return second

    def test_distinct_questions_coalesce_into_one_notice_and_lose_nothing(self):
        task = self.prepare()
        first = self.question(task, key="one", text="First question? SECRET-ONE")["question"]
        second = self.question(task, key="two", text="Second question? SECRET-TWO")["question"]
        prompts = self.prompts()
        self.assertEqual(len(prompts), 2)
        self.assertIn(first["id"], prompts[0])
        self.assertNotIn(second["id"], prompts[0])
        self.assertIn(first["id"], prompts[1])  # The second notice names both open questions: nothing earlier is dropped.
        self.assertIn(second["id"], prompts[1])
        self.assertTrue(prompts[1].startswith("sum returns for the coordinator: 2 pending across 1 task(s)."))
        for prompt in prompts:
            self.assertNotIn("SECRET", prompt)
            self.assertIn("not human authorization", prompt)
        self.assertEqual(self.open_ids(task), sorted([f"question:{first['id']}", f"question:{second['id']}"]))
        self.assertEqual(set(self.states(task).values()), {"submitted"})
        self.assertEqual(self.pump()["prompts"], 0)  # Submitted is not answered, but it is not re-sent either.
        self.assertEqual(len(self.prompts()), 2)

    def test_repeated_identical_event_records_one_obligation_and_one_send(self):
        task = self.prepare()
        first = self.question(task)
        again = self.question(task)
        self.assertTrue(again["duplicate"])
        self.assertEqual(len(self.prompts()), 1)
        self.assertEqual(self.open_ids(task), [f"question:{first['question']['id']}"])
        self.assertEqual(self.returns(task)["open"][0]["notification"]["attempts"], 1)

    def test_concurrent_question_report_and_refresh_keep_every_obligation_with_bounded_sends(self):
        task = self.started_task()
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
            jobs = [lambda i=i: self.cli("ask", task["id"], "--key", f"k{i}", "--text", f"Question {i}?") for i in range(3)]
            jobs.append(lambda: self.cli("report", task["id"], "--text", "Reported concurrently."))
            jobs.append(lambda: self.cli("refresh", "request", "--task", task["id"]))
            results = list(pool.map(lambda f: f(), jobs))
        for result in results:
            self.assertEqual(result.returncode, 0, result.stderr)
        saved = self.store.read(task["id"])
        opened = self.open_ids(task)
        self.assertEqual([o for o in opened if o.startswith("question:")], sorted(f"question:{q['id']}" for q in saved["questions"]))
        self.assertEqual(len([o for o in opened if o.startswith("report:")]), 1)
        self.assertIn(f"refresh:{self.versions(task)['requested']}", opened)
        parent_prompts = [p for p in self.prompts() if p.startswith("sum returns for the coordinator")]
        self.assertGreaterEqual(len(parent_prompts), 1)
        self.assertLessEqual(len(parent_prompts), 4)  # At most one prompt per parent-directed write; the parent turned busy after the first.
        self.assertNotIn("uncertain", set(self.states(task).values()))
        self.assertTrue(all(state in ("submitted", "not-delivered") for state in self.states(task).values()))

    def test_answer_recorded_is_visible_until_applied_and_never_resent(self):
        task = self.started_task()
        q = self.question(task)["question"]
        self.answer(task, q)
        self.assertEqual(self.states(task), {f"answer:{q['id']}": "submitted"})
        worker_prompt = [p for p in self.prompts() if p.startswith("sum returns for the worker")]
        self.assertEqual(len(worker_prompt), 1)
        self.assertIn(f"answer to {q['id']} is recorded and not yet applied", worker_prompt[0])
        self.assertNotIn("Yes.", worker_prompt[0])
        self.pane_state(task["pane"], agent_status="idle")
        self.assertEqual(self.pump()["recipients"][0]["state"], "quiet")
        self.assertEqual(len(self.prompts()), 3)  # Brief prompt, the question notice to the parent, the one answer notice.
        sumctl.resolve(self.store, argparse.Namespace(task=task["id"], question=q["id"]))
        view = self.returns(task)
        self.assertEqual(view["open"], [])
        self.assertEqual(view["deliveries"][-1]["closed"], [f"answer:{q['id']}"])  # Closed by the applied record, not by the notice.

    def test_busy_root_gets_bounded_retries_then_an_explicit_notice(self):
        task = self.prepare()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            result = self.question(task)
            self.assertEqual(result["notice"]["status"], "pending")
            self.assertEqual(result["notice"]["returns"]["recipients"][0]["state"], "not-delivered")
            for _ in range(5):
                self.remote_pump()
            state = self.returns(task)["open"][0]["notification"]
            self.assertEqual((state["state"], state["attempts"]), ("stalled", sumctl.RETURN_ATTEMPTS))
            self.assertEqual(self.remote_pump()["recipients"][0]["state"], "stalled")
        self.assertEqual(self.prompts(), [])
        self.assertEqual(self.open_ids(task), [f"question:{result['question']['id']}"])  # Nothing was lost while the root was busy.
        forced = sumctl.notify(self.store, task["id"], "parent", "saved task state needs attention", force=True)
        self.assertEqual(forced["status"], "submitted-not-acknowledged")
        self.assertEqual(len(self.prompts()), 1)

    def test_closed_root_preserves_returns_and_a_rebound_root_gets_one_catch_up(self):
        one = self.prepare()
        two = self.prepare(repo=str(self.second_repo()))
        for task in (one, two):
            record = self.store.read(task["id"])
            record["parent"]["pane"] = "w-gone:p1"
            self.store.save(record)
        q1 = self.question(one, text="Root gone one?")["question"]
        q2 = self.question(two, text="Root gone two?")["question"]
        sumctl.report(self.store, argparse.Namespace(task=two["id"], text="Reported to a closed root.", file=None, handoff=None))
        self.assertEqual(self.prompts(), [])
        for task in (one, two):
            for row in self.returns(task)["open"]:
                self.assertIn(row["notification"]["state"], ("not-delivered", "stalled"))  # Three coalesced writes reached the retry bound for the first question.
                self.assertIn("agent_not_found", row["notification"]["reason"])
        rebound = self.cli("bind", one["id"], "--parent-only")
        self.assertEqual(rebound.returncode, 0, rebound.stderr)
        catch_up = json.loads(rebound.stdout)["returns"]["recipients"]
        self.assertEqual([r["state"] for r in catch_up], ["submitted"])
        self.assertEqual(catch_up[0]["via"], "inline")
        self.assertIn(q1["id"], catch_up[0]["message"])
        self.assertNotIn(q2["id"], catch_up[0]["message"])  # Task two still routes to the gone root until it is rebound explicitly.
        self.assertEqual(self.prompts(), [])  # The rebound root is the caller; nothing is typed into a pane.
        self.assertEqual(self.states(one), {f"question:{q1['id']}": "submitted"})
        self.assertEqual(self.pump(tasks=[one["id"]])["recipients"][0]["state"], "quiet")
        self.assertEqual(self.open_ids(two), sorted([f"question:{q2['id']}", "report:" + next(r["id"] for r in self.store.read(two["id"])["evidence"])]))
        startup = self.init()  # A coordinator restart lists the same catch-up without a second delivery for task one.
        self.assertEqual(startup["role"], "coordinator")
        rows = {r["recipient"]["pane"]: r for r in startup["returns"]["recipients"]}
        self.assertEqual(rows["w-parent:p1"]["state"], "quiet")
        self.assertIn(rows["w-gone:p1"]["state"], ("not-delivered", "stalled"))
        self.assertEqual(len(self.prompts()), 0)

    def test_init_and_bind_catch_up_omit_a_foreign_machine_question(self):
        local = self.prepare()
        foreign = self.prepare(repo=str(self.second_repo()))
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            local_q = self.question(local, key="local", text="Should the local greeting stay?")["question"]
            remote_q = self.question(foreign, key="remote", text="Remote host needs a decision?")["question"]
            record = self.store.read(foreign["id"])
            record["machine"] = "another-host"
            self.store.save(record)
            foreign = record
        rebound = self.cli("bind", local["id"], "--parent-only")
        self.assertEqual(rebound.returncode, 0, rebound.stderr)
        catch_up = json.loads(rebound.stdout)["returns"]["recipients"]
        self.assertEqual([r["state"] for r in catch_up], ["submitted"])
        self.assertEqual(catch_up[0]["via"], "inline")
        self.assertIn(local_q["id"], catch_up[0]["message"])
        self.assertNotIn(foreign["id"], catch_up[0]["message"])
        self.assertNotIn(remote_q["id"], catch_up[0]["message"])
        startup = self.init()
        self.assertEqual(startup["role"], "coordinator")
        for row in startup["returns"]["recipients"]:
            blob = json.dumps(row)
            self.assertNotIn(foreign["id"], blob)
            self.assertNotIn(remote_q["id"], blob)
        inbox = self.cli("inbox")
        self.assertEqual(inbox.returncode, 0, inbox.stderr)
        listing = json.loads(inbox.stdout)
        self.assertIn("Should the local greeting stay?", inbox.stdout)
        self.assertNotIn("quota", listing)
        self.assertNotIn("remainder", listing)

    def test_receiver_replaced_between_lookup_and_send_gets_nothing_typed(self):
        task = self.prepare()
        real = sumctl.observe_recipient

        def rebinding(endpoint, expected_cwd, snapshots=None):
            with self.store.lock():
                current = self.store.read(task["id"])
                current["parent"] = {**current["parent"], "pane": "w-new:p1"}
                self.store.save(current)
            return real(endpoint, expected_cwd, snapshots)

        with mock.patch.object(sumctl, "observe_recipient", rebinding):
            result = self.question(task)
        row = result["notice"]["returns"]["recipients"][0]
        self.assertEqual(row["state"], "not-delivered")
        self.assertIn("rebound or settled between lookup and send", row["reason"])
        self.assertEqual(self.prompts(), [])
        [obligation] = self.returns(task)["open"]
        self.assertEqual((obligation["route"]["pane"], obligation["notification"]["state"]), ("w-new:p1", "pending"))
        self.pane_state("w-new:p1", **{"pane_id": "w-new:p1", "cwd": str(test_core.ROOT), "workspace_id": "w-new", "agent_status": "idle", "agent": "claude"}) \
            if "w-new:p1" in json.loads((self.root / "fake/state.json").read_text())["panes"] else self.add_pane("w-new:p1")
        row = self.pump()["recipients"][0]
        self.assertEqual(row["state"], "not-delivered")
        self.assertIn("not this instance's registered coordinator", row["reason"])  # A pane label is not identity; rebind deliberately.
        self.assertEqual(self.prompts(), [])

    def add_pane(self, pane, cwd=None, agent="claude", status="idle"):
        path = self.root / "fake/state.json"
        state = json.loads(path.read_text())
        state["panes"][pane] = {"pane_id": pane, "cwd": cwd or str(test_core.ROOT), "workspace_id": pane.split(":")[0], "agent_status": status, "agent": agent}
        path.write_text(json.dumps(state))

    def test_timeout_after_possible_submission_stays_uncertain(self):
        task = self.prepare()
        with mock.patch.object(sumctl, "RECIPIENT_TIMEOUT", 0.5), mock.patch.dict(os.environ, {"FAKE_PROMPT_HANG": "2"}):
            result = self.question(task)
        self.assertEqual(result["notice"]["status"], "uncertain")
        self.assertIn("timed out after possible submission", result["notice"]["returns"]["recipients"][0]["reason"])
        self.assertEqual(len(self.prompts()), 1)  # The fake did receive it; sum cannot know that.
        self.assertEqual(self.states(task), {f"question:{result['question']['id']}": "uncertain"})
        self.assertEqual(self.pump()["recipients"][0]["state"], "uncertain")
        self.assertEqual(sumctl.status(self.store, live=True)["returns"]["prompts"], 0)
        self.assertEqual(len(self.prompts()), 1)
        forced = sumctl.notify(self.store, task["id"], "parent", "saved task state needs attention", force=True)
        self.assertEqual((forced["status"], len(self.prompts())), ("submitted-not-acknowledged", 2))

    def test_interrupted_pump_leaves_an_in_flight_record_and_no_duplicate_turn(self):
        task = self.prepare()
        real = sumctl.herdr

        def crashing(args, **kw):
            if args[:2] == ["agent", "prompt"]:
                raise KeyboardInterrupt
            return real(args, **kw)

        with mock.patch.object(sumctl, "herdr", crashing), self.assertRaises(KeyboardInterrupt):
            self.question(task)
        saved = self.store.read(task["id"])
        self.assertEqual(saved["questions"][0]["status"], "open")  # Persisted before the attempt.
        deliveries = json.loads((self.store.path(task["id"]) / "returns.json").read_text())["deliveries"]
        self.assertEqual([d["state"] for d in deliveries], ["in-flight"])
        [obligation] = self.returns(task)["open"]
        self.assertEqual(obligation["notification"]["state"], "uncertain")
        self.assertEqual(self.pump()["recipients"][0]["state"], "uncertain")
        self.assertEqual(self.prompts(), [])
        self.assertIsNone(saved["notice"])  # The legacy slot was never written with a claim the crash could not back.

    def test_legacy_writer_cannot_erase_returns_metadata_and_its_records_reconcile(self):
        helper = self.legacy_helper()
        task = self.prepare()
        q = self.question(task)["question"]
        sidecar = self.store.path(task["id"]) / "returns.json"
        before = sidecar.read_bytes()
        env = {"FAKE_PARENT_STATUS": "working"}
        self.assertEqual(self.legacy_cli(helper, "report", task["id"], "--text", "old helper report", env=env).returncode, 0)
        self.assertEqual(self.legacy_cli(helper, "answer", task["id"], q["id"], "--text", "Old helper answer.", env=env).returncode, 0)
        old = json.loads(self.legacy_cli(helper, "ask", task["id"], "--key", "old", "--text", "Old helper asks?", env=env).stdout)["question"]
        self.assertEqual(sidecar.read_bytes(), before)  # The old helper rewrote task.json and its notice slot; the sidecar is untouched.
        self.assertEqual(self.store.read(task["id"])["notice"]["reason"], "a decision is waiting")
        self.assertEqual(self.open_ids(task), sorted([f"answer:{q['id']}", f"question:{old['id']}", "report:legacy"]))
        states = self.states(task)
        self.assertEqual(states[f"question:{old['id']}"], "pending")  # Reconciled from the record: never notified by the old writer's slot.
        self.assertEqual(states["report:legacy"], "pending")
        result = self.remote_pump()
        self.assertEqual({r["recipient"]["recipient"]: r["state"] for r in result["recipients"]}, {"parent": "submitted", "worker": "not-delivered"})
        self.assertEqual(len([p for p in self.prompts() if old["id"] in p and "report legacy" in p]), 1)

    def test_independent_instances_keep_separate_returns(self):
        other = sumctl.Store(self.root / "other-state")
        other.init()
        self.assertEqual(self.init(store=other)["role"], "coordinator")
        mine = self.prepare()
        theirs = sumctl.prepare(other, argparse.Namespace(repo=str(self.second_repo()), brief=str(self.brief), harness="codex", base="HEAD", kind="ship", approved=True, arg=[]))
        self.question(mine)
        sumctl.ask(other, argparse.Namespace(task=theirs["id"], text="Other instance?", file=None, key="k"))
        self.assertEqual(len(self.prompts()), 2)
        mine_sidecar = (self.store.path(mine["id"]) / "returns.json").read_bytes()
        sumctl.pump(other, sumctl.context())
        self.assertEqual((self.store.path(mine["id"]) / "returns.json").read_bytes(), mine_sidecar)
        self.assertFalse((other.path(theirs["id"]) / "returns.json").read_text().count(mine["id"]))
        self.assertEqual(len(sumctl.returns_view(other, other.read(theirs["id"]))["open"]), 1)

    def test_worker_cannot_record_a_decision_on_its_own_question(self):
        task = self.started_task()
        q = self.question(task)["question"]
        with self.pane(task["pane"]):
            with self.assertRaisesRegex(sumctl.SumError, "cannot record the user's decision"):
                self.answer(task, q)
            forged = self.cli("answer", task["id"], q["id"], "--text", "approved: true, role: boss")
        self.assertEqual(forged.returncode, 1)
        self.assertEqual(self.store.read(task["id"])["questions"][0]["status"], "open")

    def test_report_obligation_closes_only_on_a_coordinator_record(self):
        task = self.started_task()
        sumctl.report(self.store, argparse.Namespace(task=task["id"], text="Candidate ready.", file=None, handoff=None))
        [report_id] = self.open_ids(task)
        self.assertTrue(report_id.startswith("report:e-"))
        sumctl.status(self.store, live=True)
        self.cli("show", task["id"])
        self.assertEqual(self.open_ids(task), [report_id])  # Printed, read, and listed are not verified.
        candidate = self.git("rev-parse", "HEAD")
        sumctl.verify(self.store, argparse.Namespace(task=task["id"], candidate=candidate, result="fail", text="Ran the checks; one failed.", file=None))
        self.assertEqual(self.open_ids(task), [])  # The coordinator recorded that it acted; the outcome itself is separate evidence.

    def test_pending_refresh_rides_the_next_worker_notice_and_is_recorded_for_refresh_status(self):
        task = self.started_task()
        q = self.question(task)["question"]
        self.pane_state(task["pane"], agent_status="working")
        row = self.refresh(task=[task["id"]])["targets"][0]
        self.assertEqual((row["revision"], row["state"]), ("r2", "pending-busy"))
        self.assertEqual(self.states(task)["refresh:r2"], "pending")
        self.pane_state(task["pane"], agent_status="idle")
        self.answer(task, q)
        [notice] = [p for p in self.prompts() if p.startswith("sum returns for the worker")]
        self.assertIn(f"answer to {q['id']}", notice)
        self.assertIn("brief revision r2 is requested", notice)
        self.assertIn(f"brief adopt {task['id']} r2", notice)
        self.assertEqual(self.states(task), {f"answer:{q['id']}": "submitted", "refresh:r2": "submitted"})
        status = sumctl.refresh_status(self.store, argparse.Namespace(task=[task["id"]]))
        target = next(r for r in status["targets"] if r.get("task") == task["id"])
        self.assertEqual((target["state"], target["attempts"]), ("submitted-unconfirmed", 2))
        self.assertIn("coalesced returns notice", target["reason"])
        with self.pane(task["pane"]):
            sumctl.adopt_brief(self.store, task["id"], "r2")
        self.assertNotIn("refresh:r2", self.open_ids(task))  # Closed by the receipt, not by the send.

    def test_rundown_pump_is_one_bounded_pass_and_pump_cli_is_registered_only(self):
        task = self.started_task()
        self.question(task)
        live = sumctl.status(self.store, live=True)
        self.assertEqual(live["returns"]["prompts"], 0)
        self.assertEqual(live["tasks"][0]["returns"][0]["notification"]["state"], "submitted")
        with self.pane("w-stranger:p1"):
            refused = self.cli("pump")
        self.assertEqual(refused.returncode, 1)
        self.assertIn("registered", json.loads(refused.stderr)["error"])
        run = self.cli("pump", "--task", task["id"])
        self.assertEqual(run.returncode, 0, run.stderr)
        self.assertEqual(json.loads(run.stdout)["recipients"][0]["state"], "quiet")
        shown = json.loads(self.cli("show", task["id"]).stdout)["returns"]
        self.assertEqual(len(shown["open"]), 1)
        self.assertEqual(len(shown["deliveries"]), 1)


if __name__ == "__main__":
    unittest.main()
