"""Issue #14: native Herdr events drive the bounded pump and record attention; the synchronous path stays whole without them.

Events are replayed exactly as Herdr 0.9.0 delivers them to a plugin command (environment variables verified in a named lab
session), against the strict fake Herdr. No model, network, or credentials.
"""
from __future__ import annotations
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_core

sumctl = test_core.sumctl


class HookTest(unittest.TestCase):
    for _name in ("setUp", "init", "pane", "cli", "git", "prepare", "question", "calls", "pane_state", "prompts", "started_task", "versions", "refresh"):
        locals()[_name] = getattr(test_core.CoreTest, _name)
    del _name

    def ctx(self):
        return sumctl.context()

    def started_task(self):
        task = self.prepare()
        sumctl.start(self.store, task["id"])
        return self.store.read(task["id"])  # The brief prompt left the scripted worker `working`, as a real one is after dispatch.

    def enable(self):
        return sumctl.hook_enable(self.store, self.ctx())

    def plugin_id(self):
        return sumctl.hook_plugin_id(self.store)

    def event(self, name, pane=None, status=None, session="sum-test", plugin_id=None, extra=None, home=None):
        """Replay one plugin invocation with the exact variables Herdr 0.9.0 injects."""
        data = {"type": name.replace(".", "_"), "workspace_id": (pane or "w0:p0").split(":")[0]}
        if pane:
            data["pane_id"] = pane
        if status:
            data["agent_status"] = status
            data["agent"] = "claude"
        data.update(extra or {})
        env = {"HERDR_ENV": "1", "HERDR_PLUGIN_ID": plugin_id or self.plugin_id(), "HERDR_PLUGIN_EVENT": name,
               "HERDR_PLUGIN_EVENT_JSON": json.dumps({"event": name.replace(".", "_"), "data": data}), "HERDR_SESSION": session,
               "HERDR_SOCKET_PATH": f"/tmp/x/sessions/{session}/herdr.sock", "HERDR_PANE_ID": pane or "", "HERDR_WORKSPACE_ID": data["workspace_id"],
               "HERDR_PLUGIN_STATE_DIR": str(self.root / "plugin-state"), "HERDR_PLUGIN_ROOT": str(sumctl.hook_plugin_dir(self.store))}
        return sumctl.hook_event(home or self.store, {**os.environ, **env})

    def cli_event(self, name, pane, status=None, session="sum-test"):
        data = {"type": name.replace(".", "_"), "pane_id": pane, "workspace_id": pane.split(":")[0]}
        if status:
            data["agent_status"] = status
        env = {"HERDR_PLUGIN_ID": self.plugin_id(), "HERDR_PLUGIN_EVENT": name, "HERDR_SESSION": session,
               "HERDR_PLUGIN_EVENT_JSON": json.dumps({"event": name.replace(".", "_"), "data": data}), "HERDR_PANE_ID": pane}
        return self.cli("hook", "event", env=env)

    def health(self):
        return sumctl.read_health(self.store)

    def open_returns(self, task):
        return {o["id"]: o["notification"]["state"] for o in sumctl.returns_view(self.store, self.store.read(task["id"]))["open"]}

    def attention(self, task):
        return sumctl.open_attention(self.store.read(task["id"]))

    def parent_prompts(self):
        return [p for p in self.prompts() if p.startswith("sum returns for the coordinator")]

    def worker_prompts(self):
        return [p for p in self.prompts() if p.startswith("sum returns for the worker")]

    # --- registration -------------------------------------------------------------------------------------------------

    def test_enable_writes_manifest_links_live_and_reconciles_once(self):
        task = self.started_task()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            self.question(task)  # Parent busy: the question stays pending.
        self.assertEqual(set(self.open_returns(task).values()), {"not-delivered"})
        result = self.enable()
        manifest = Path(result["manifest"]).read_text()
        self.assertEqual(result["plugin_id"], self.plugin_id())
        self.assertTrue(result["plugin_id"].startswith("sum.returns."))
        self.assertIn(f'id = "{result["plugin_id"]}"', manifest)
        self.assertIn(f'min_herdr_version = "{sumctl.HERDR_VERSION}"', manifest)
        for event in sumctl.HOOK_EVENTS:
            self.assertIn(f'on = "{event}"', manifest)
        self.assertEqual(manifest.count("[[events]]"), len(sumctl.HOOK_EVENTS))
        self.assertEqual(manifest.count("[[startup]]"), 1)
        self.assertEqual(result["command"], [str(test_core.ROOT / "bin" / "sumctl"), "--home", str(self.store.home), "hook", "event"])
        self.assertNotIn(str(Path.cwd()), manifest.replace(str(test_core.ROOT), ""))  # The home is the recorded one, never the cwd.
        self.assertTrue(Path(result["manifest"]).is_relative_to(self.store.home))
        self.assertEqual(result["warnings"], [])
        links = [c for c in self.calls() if c[:2] == ["plugin", "link"]]
        self.assertEqual(len(links), 1)
        self.assertFalse(any(c[:2] == ["server", "stop"] for c in self.calls()))
        # The parent had become idle meanwhile: the explicit initial reconciliation presented the pending question at once, inline to the caller.
        [recipient] = result["reconciliation"]["returns"]["recipients"]
        self.assertEqual((recipient["state"], recipient["via"]), ("submitted", "inline"))
        self.assertEqual(len(self.parent_prompts()), 0)
        self.assertEqual(set(self.open_returns(task).values()), {"submitted"})
        self.assertTrue(self.health()["enabled"])
        registry = json.loads((self.root / "fake/plugins.json").read_text())
        self.assertEqual(list(registry), [result["plugin_id"]])
        again = self.enable()
        self.assertFalse(again["manifest_changed"])
        self.assertEqual(len([c for c in self.calls() if c[:2] == ["plugin", "link"]]), 2)  # Relinking is the documented idempotent refresh.

    def test_enable_requires_the_coordinator_and_disable_keeps_the_synchronous_path(self):
        task = self.started_task()
        with self.pane("w-other:p1"):
            self.init()
            with self.assertRaisesRegex(sumctl.SumError, "not the registered coordinator"):
                sumctl.hook_enable(self.store, sumctl.context())
        self.assertFalse((self.store.home / "hook").exists())
        self.enable()
        result = sumctl.hook_disable(self.store, self.ctx())
        self.assertEqual(result["action"], "disabled")
        self.assertFalse(self.health()["enabled"])
        registry = json.loads((self.root / "fake/plugins.json").read_text())
        self.assertFalse(registry[self.plugin_id()]["enabled"])
        # A late event to a disabled instance is bookkeeping only; ask/report still save and notify synchronously.
        row = self.event("pane.agent_status_changed", task["pane"], "idle")
        self.assertEqual(row["outcome"], "disabled")
        q = self.question(task)
        self.assertEqual(q["notice"]["status"], "submitted-not-acknowledged")
        status = sumctl.hook_status(self.store, self.ctx())
        self.assertTrue(status["degraded"])
        self.assertIn("inbox --live", status["reason"])
        self.assertEqual(status["registry"]["enabled"], False)
        self.assertEqual(sumctl.hook_enable(self.store, self.ctx())["plugin_id"], self.plugin_id())
        self.assertTrue(json.loads((self.root / "fake/plugins.json").read_text())[self.plugin_id()]["enabled"])
        unlinked = sumctl.hook_disable(self.store, self.ctx(), unlink=True)
        self.assertEqual(unlinked["action"], "unlinked")
        self.assertEqual(json.loads((self.root / "fake/plugins.json").read_text()), {})

    def test_hook_status_is_read_only_and_reports_pending_age(self):
        task = self.started_task()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            self.question(task)
        before = {p: p.read_bytes() for p in self.store.home.rglob("*") if p.is_file()}
        status = sumctl.hook_status(self.store, None)
        self.assertEqual(status["pending"]["count"], 1)
        self.assertGreaterEqual(status["pending"]["oldest_age_s"], 0)
        self.assertFalse(status["enabled"])
        self.assertEqual({p: p.read_bytes() for p in self.store.home.rglob("*") if p.is_file()}, before)
        result = self.cli("hook", "status")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("pending", json.loads(result.stdout))

    # --- delivery on edges ----------------------------------------------------------------------------------------------

    def test_question_while_root_busy_is_delivered_on_the_idle_edge_without_polling(self):
        task = self.started_task()
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            q = self.question(task)["question"]
            self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "not-delivered"})
            busy = self.event("pane.agent_status_changed", "w-parent:p1", "working")
            self.assertEqual(busy["outcomes"][0]["action"], "none")
            self.assertEqual(len(self.parent_prompts()), 0)
            # Herdr reported idle but the fresh observation says working: nothing is typed into a busy pane.
            stale = self.event("pane.agent_status_changed", "w-parent:p1", "idle")
            self.assertEqual(stale["outcomes"][0]["recipients"][0][0], "not-delivered")
            self.assertEqual(len(self.parent_prompts()), 0)
        calls_before = len(self.calls())
        started = time.monotonic()
        row = self.event("pane.agent_status_changed", "w-parent:p1", "idle")
        elapsed_ms = round((time.monotonic() - started) * 1000)
        self.assertEqual(row["outcome"], "handled")
        self.assertEqual(row["outcomes"][0]["prompts"], 1)
        self.assertEqual(len(self.parent_prompts()), 1)
        self.assertIn(q["id"], self.parent_prompts()[0])
        self.assertNotIn("Keep compatibility", self.parent_prompts()[0])
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "submitted"})
        self.assertLessEqual(len(self.calls()) - calls_before, 3)  # One agent list, one cwd lookup, one prompt: no polling loop.
        self.assertEqual(self.health()["last_event"]["event"], "pane.agent_status_changed")
        print(f"HOOK-MEASURE {json.dumps({'case': 'question-idle-edge', 'handler_ms': row['handler_ms'], 'wall_ms': elapsed_ms, 'herdr_calls': row['herdr_calls']})}", file=sys.stderr)
        again = self.event("pane.agent_status_changed", "w-parent:p1", "idle")
        self.assertEqual(again["outcomes"][0]["prompts"], 0)  # Duplicate edge: submitted is not re-sent.
        self.assertEqual(len(self.parent_prompts()), 1)

    def test_already_satisfied_condition_is_handled_by_enable_and_startup_reconciliation(self):
        task = self.started_task()
        q = self.question(task)["question"]
        self.pane_state(task["pane"], agent_status="idle")
        sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q["id"], text="Yes.", file=None))
        self.assertEqual(len(self.worker_prompts()), 1)  # The answer's own synchronous notice reached the idle worker.
        result = self.enable()
        self.assertEqual(result["reconciliation"]["returns"]["prompts"], 0)  # Already satisfied: nothing sent twice.
        self.assertEqual(len(self.worker_prompts()), 1)
        startup = self.event("startup")
        self.assertEqual((startup["outcome"], startup["prompts"]), ("reconciled", 0))
        # A request written while the worker was busy is picked up by the startup reconciliation, not by a timer.
        self.pane_state(task["pane"], agent_status="working")
        q2 = self.question(task, key="second", text="Second?")["question"]
        sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q2["id"], text="No.", file=None))
        self.assertEqual(len(self.worker_prompts()), 1)
        self.pane_state(task["pane"], agent_status="idle")
        startup = self.event("startup")
        self.assertEqual(startup["prompts"], 1)
        self.assertEqual(len(self.worker_prompts()), 2)
        self.assertIn(q2["id"], self.worker_prompts()[1])

    def test_stalled_return_is_retried_on_a_real_edge_but_uncertain_is_not(self):
        task = self.started_task()
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            q = self.question(task)["question"]
            for _ in range(4):
                sumctl.pump(self.store, None, inline=False)
            self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "stalled"})
        row = self.event("pane.agent_status_changed", "w-parent:p1", "idle")
        self.assertEqual(row["outcomes"][0]["prompts"], 1)
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "submitted"})
        with mock.patch.dict(os.environ, {"FAKE_PROMPT_HANG": "0.2"}), mock.patch.object(sumctl, "RECIPIENT_TIMEOUT", 0.05):
            q2 = self.question(task, key="two", text="Two?")["question"]
        self.assertEqual(self.open_returns(task)[f"question:{q2['id']}"], "uncertain")
        prompts = len(self.prompts())
        row = self.event("pane.agent_status_changed", "w-parent:p1", "idle")
        self.assertEqual(row["outcomes"][0]["recipients"][0][0], "uncertain")
        self.assertEqual(len(self.prompts()), prompts)  # A possible duplicate turn is never risked by an edge.

    def test_refresh_requested_while_busy_is_delivered_to_the_worker_on_its_idle_edge(self):
        task = self.started_task()
        self.enable()
        q = self.question(task)["question"]
        sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q["id"], text="Yes.", file=None))
        self.pane_state(task["pane"], agent_status="working")
        rows = {r["target"]: r for r in self.refresh(task=[task["id"]])["targets"]}
        self.assertEqual(rows["task"]["state"], "pending-busy")
        requested = self.versions(task)["requested"]
        before = len(self.worker_prompts())
        self.pane_state(task["pane"], agent_status="idle")
        row = self.event("pane.agent_status_changed", task["pane"], "idle")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["prompts"], 1)
        self.assertEqual(len(self.worker_prompts()), before + 1)
        self.assertIn(f"brief revision {requested} is requested", self.worker_prompts()[-1])
        self.assertIn(f"brief adopt {task['id']} {requested}", self.worker_prompts()[-1])
        self.assertEqual(sumctl.refresh_state(self.versions(task))["state"], "submitted-unconfirmed")
        self.assertEqual(self.attention(task), [])  # An answered question and a requested revision are records; idle here is ordinary.
        sumctl.adopt_brief(self.store, task["id"], requested)
        self.assertEqual(sumctl.refresh_state(self.versions(task))["state"], "confirmed")

    # --- attention -------------------------------------------------------------------------------------------------------

    def test_blocked_permission_ui_becomes_bounded_attention_not_an_approval(self):
        task = self.started_task()
        self.enable()
        self.pane_state(task["pane"], agent_status="blocked", screen="Allow Bash(rm -rf build)? SECRET-TOKEN-XYZ\n> (y/n)")
        row = self.event("pane.agent_status_changed", task["pane"], "blocked")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["kind"], "blocked")
        [record] = self.attention(task)
        self.assertEqual((record["kind"], record["observed"], record["status"]), ("blocked", "blocked", "open"))
        self.assertIn("rm -rf build", record["excerpt"])
        self.assertIn("agent read", record["source"]["pointer"])
        self.assertIn("not proof", record["note"])
        self.assertEqual(self.open_returns(task), {f"attention:{record['id']}": "submitted"})
        [prompt] = self.parent_prompts()
        self.assertIn(f"attention {record['id']}", prompt)
        self.assertIn("blocked", prompt)
        self.assertNotIn("SECRET-TOKEN", prompt)  # Excerpts stay in the record; the notice carries IDs only.
        duplicate = self.event("pane.agent_status_changed", task["pane"], "blocked")
        self.assertTrue([o for o in duplicate["outcomes"] if o.get("task") == task["id"]][0]["duplicate"])
        self.assertEqual(len(self.attention(task)), 1)
        self.assertEqual(len(self.parent_prompts()), 1)
        self.assertEqual(self.store.read(task["id"])["questions"], [])  # No question, no answer, no approval was invented.
        self.pane_state(task["pane"], agent_status="working")
        resumed = self.event("pane.agent_status_changed", task["pane"], "working")
        self.assertEqual([o for o in resumed["outcomes"] if o.get("task") == task["id"]][0]["closed"], [record["id"]])
        self.assertEqual(self.attention(task), [])
        self.assertEqual(self.open_returns(task), {})

    def test_prose_only_question_then_routine_output_is_attention_that_a_saved_record_supersedes(self):
        task = self.started_task()
        self.enable()
        self.pane_state(task["pane"], agent_status="idle", screen="Should I keep the exclamation mark? I will wait for your answer.")
        row = self.event("pane.agent_status_changed", task["pane"], "idle")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["kind"], "idle-without-report")
        [record] = self.attention(task)
        self.assertIn("exclamation mark", record["excerpt"])
        self.assertIn(f"attention {record['id']}", self.parent_prompts()[-1])
        self.assertNotIn("exclamation", self.parent_prompts()[-1])
        # Newer routine output and another idle edge do not replace or duplicate the record.
        self.pane_state(task["pane"], screen="Formatting done. 3 files changed.")
        again = self.event("pane.agent_status_changed", task["pane"], "idle")
        self.assertTrue([o for o in again["outcomes"] if o.get("task") == task["id"]][0]["duplicate"])
        self.assertEqual(self.attention(task)[0]["excerpt"], record["excerpt"])
        # The coordinator captures the real question as a record; the attention is superseded by it, not deleted.
        q = self.question(task, key="captured", text="Keep the exclamation mark?")["question"]
        self.assertEqual(self.attention(task), [])
        self.assertEqual(list(self.open_returns(task)), [f"question:{q['id']}"])
        saved = self.store.read(task["id"])["attention"]
        self.assertEqual([a["id"] for a in saved], [record["id"]])

    def test_prose_only_question_is_listed_in_records_inbox_with_excerpt_and_source(self):
        task = self.started_task()
        self.enable()
        question = "Should I keep the exclamation mark?"
        self.pane_state(task["pane"], agent_status="idle", screen=f"{question} I will wait for your answer.")
        row = self.event("pane.agent_status_changed", task["pane"], "idle")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["kind"], "idle-without-report")
        [record] = self.attention(task)
        self.assertIn(question, record["excerpt"])
        [prompt] = self.parent_prompts()
        self.assertIn(f"attention {record['id']}", prompt)
        self.assertNotIn("exclamation", prompt)
        result = self.cli("inbox")
        self.assertEqual(result.returncode, 0, result.stderr)
        inbox = json.loads(result.stdout)
        listed = [r for r in inbox["tasks"] if r["id"] == task["id"]]
        self.assertEqual(len(listed), 1, result.stdout)
        [inbox_row] = listed
        self.assertEqual(inbox_row["questions"], [])
        self.assertEqual(len(inbox_row["attention_records"]), 1)
        shown = inbox_row["attention_records"][0]
        self.assertEqual(shown["id"], record["id"])
        self.assertIn(question, shown["excerpt"])
        self.assertIn("agent read", shown["source"]["pointer"])
        self.assertNotIn("require a rundown", inbox["guarantee"])
        self.pane_state(task["pane"], screen="Formatting done. 3 files changed.")
        duplicate = self.event("pane.agent_status_changed", task["pane"], "idle")
        self.assertTrue([o for o in duplicate["outcomes"] if o.get("task") == task["id"]][0]["duplicate"])
        again = json.loads(self.cli("inbox").stdout)
        [again_row] = [r for r in again["tasks"] if r["id"] == task["id"]]
        self.assertEqual(len(again_row["attention_records"]), 1)
        self.assertEqual(again_row["attention_records"][0]["excerpt"], shown["excerpt"])
        self.assertIn(question, again_row["attention_records"][0]["excerpt"])
        self.assertEqual(again_row["questions"], [])

    def test_open_question_is_preserved_when_newer_output_and_edges_arrive(self):
        task = self.started_task()
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            q = self.question(task, text="Real saved question?")["question"]
        self.pane_state(task["pane"], agent_status="idle", screen="unrelated newer output")
        row = self.event("pane.agent_status_changed", task["pane"], "idle")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertNotIn("kind", worker)  # An open saved question means idle is the expected state, not attention.
        self.assertEqual(self.attention(task), [])
        self.assertEqual(list(self.open_returns(task)), [f"question:{q['id']}"])
        self.assertEqual(self.store.read(task["id"])["questions"][0]["status"], "open")

    def test_worker_exit_without_report_is_attention_with_a_pointer(self):
        task = self.started_task()
        self.enable()
        row = self.event("pane.agent_detected", task["pane"], extra={"agent": "claude", "released": True, "final_status": "unknown"})
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["kind"], "exited")
        [record] = self.attention(task)
        self.assertEqual(record["source"]["event"], "pane.agent_detected")
        self.assertEqual(len(self.parent_prompts()), 1)
        self.pane_state(task["pane"], remove=True)
        exited = self.event("pane.exited", task["pane"])
        self.assertTrue([o for o in exited["outcomes"] if o.get("task") == task["id"]][0]["duplicate"])
        closed = self.event("pane.closed", task["pane"])
        self.assertEqual([o for o in closed["outcomes"] if o.get("task") == task["id"]][0]["kind"], "closed")
        self.assertEqual(sorted(a["kind"] for a in self.attention(task)), ["closed", "exited"])
        self.assertEqual(len(self.parent_prompts()), 2)
        seen = sumctl.attention_seen(self.store, self.ctx(), task["id"], record["id"])
        self.assertEqual(seen["status"], "seen")
        self.assertEqual([a["kind"] for a in self.attention(task)], ["closed"])
        saved = self.store.read(task["id"])
        self.assertEqual(saved["status"], "running")  # Attention never changes the task status or archives anything.
        self.assertIsNone(saved["report"])

    def test_launch_handshake_idle_is_not_attention(self):
        task = self.prepare()
        self.enable()
        row = self.event("pane.agent_status_changed", task["pane"], "idle")
        self.assertEqual(self.attention(task), [])
        self.assertEqual(len(self.parent_prompts()), 0)
        sumctl.start(self.store, task["id"])
        row = self.event("pane.agent_status_changed", task["pane"], "idle")  # The payload says idle; the fresh observation says working.
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["observed"], "working")
        self.assertEqual(self.attention(task), [])

    # --- binding and isolation -----------------------------------------------------------------------------------------

    def test_unrelated_panes_other_sessions_and_other_homes_are_ignored(self):
        task = self.started_task()
        self.enable()
        for name, pane, session in (("pane.agent_status_changed", "w-stranger:p9", "sum-test"), ("pane.exited", "w-stranger:p9", "sum-test"),
                                    ("pane.agent_status_changed", task["pane"], "someone-else"), ("pane.agent_status_changed", "w-parent:p1", "default")):
            row = self.event(name, pane, "idle" if "status" in name else None, session=session)
            self.assertEqual(row["outcome"], "ignored", row)
        self.assertEqual(self.health()["ignored"], 4)
        self.assertEqual(self.prompts(), [self.prompts()[0]])  # Only the dispatch brief prompt was ever sent.
        with self.assertRaisesRegex(sumctl.SumError, "answers only"):
            self.event("pane.agent_status_changed", task["pane"], "idle", plugin_id="sum.returns.000000000000")
        self.assertEqual(self.health()["errors"], [])  # hook_event raised before any handling; the CLI wrapper records it.
        result = self.cli_event("pane.agent_status_changed", task["pane"], "idle", session="")
        self.assertEqual(result.returncode, 1)
        self.assertIn("no identifiable Herdr session", result.stderr)
        self.assertEqual(self.health()["errors"][-1]["stage"], "event")
        with self.assertRaisesRegex(sumctl.SumError, "Malformed pane id"):
            self.event("pane.agent_status_changed", "../../etc/passwd:p1", "idle")

    def test_two_sum_homes_have_distinct_plugins_and_never_cross(self):
        task = self.started_task()
        self.enable()
        other_root, other = test_core.CoreTest.installation(self, "other-install")
        with mock.patch.dict(os.environ, {"HERDR_PANE_ID": "w-other-root:p1"}):
            self.pane_state("w-parent:p1")  # Ensure the fake knows the first root; the fake registers the caller pane itself.
            sumctl.init(other, argparse.Namespace(role=None, task=None, reclaim=False))
            other_result = sumctl.hook_enable(other, sumctl.context())
        self.assertNotEqual(other_result["plugin_id"], self.plugin_id())
        registry = json.loads((self.root / "fake/plugins.json").read_text())
        self.assertEqual(sorted(registry), sorted([self.plugin_id(), other_result["plugin_id"]]))
        self.assertIn(str(other.home), registry[other_result["plugin_id"]]["manifest_path"])
        # The other home's plugin receives an event for this home's worker: not its pane, so nothing happens there.
        row = self.event("pane.agent_status_changed", task["pane"], "idle", plugin_id=other_result["plugin_id"], home=other)
        self.assertEqual(row["outcome"], "ignored")
        self.assertEqual(sumctl.read_health(self.store)["events"], 0)
        self.assertEqual(sumctl.read_health(other)["ignored"], 1)

    def test_closed_or_rebound_root_leaves_returns_pending_until_a_pane_binds(self):
        task = self.started_task()
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            q = self.question(task)["question"]
        row = self.event("pane.closed", "w-parent:p1")
        self.assertEqual(row["outcomes"][0]["action"], "noted")
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "not-delivered"})
        self.assertEqual(len(self.parent_prompts()), 0)
        with self.pane("w-newroot:p1"):
            with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "idle"}):
                self.pane_state("w-parent:p1", remove=True)
                claimed = self.init(role="coordinator", reclaim=True)
                self.assertEqual(claimed["role"], "coordinator")
                self.assertIn("enabled", claimed["hook"])
                bound = self.cli("bind", task["id"], "--parent-only")
                self.assertEqual(bound.returncode, 0, bound.stderr)
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "submitted"})  # Presented inline to the new root by bind's own pass.
        with self.pane("w-newroot:p1"), mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            self.question(task, key="later", text="Later?")
            self.assertEqual(len(self.parent_prompts()), 0)
        with self.pane("w-newroot:p1"):
            row = self.event("pane.agent_status_changed", "w-newroot:p1", "idle")
        self.assertEqual(row["outcomes"][0]["prompts"], 1)  # The rebound root is now the recorded parent; the old pane id is nobody.

    # --- repair 1: sibling isolation, fresh boundary, out-of-order working ------------------------------------------------

    def test_new_attention_never_restamps_or_reprompts_an_uncertain_sibling(self):
        task = self.started_task()
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PROMPT_HANG": "0.2"}), mock.patch.object(sumctl, "RECIPIENT_TIMEOUT", 0.05):
            q = self.question(task, text="Ambiguous? SECRET-Q")["question"]
        time.sleep(0.3)  # Let the hung fake prompt finish its late save before the pane states change.
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "uncertain"})
        prompts_before = len(self.prompts())
        self.pane_state(task["pane"], agent_status="blocked", screen="Allow write?")
        row = self.event("pane.agent_status_changed", task["pane"], "blocked")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual((worker["kind"], worker["parent_prompts"]), ("blocked", 1))
        [record] = self.attention(task)
        self.assertEqual(len(self.prompts()), prompts_before + 1)
        prompt = self.prompts()[-1]
        self.assertIn(f"attention {record['id']}", prompt)
        self.assertNotIn(q["id"], prompt)  # The possibly-landed question is not re-injected.
        self.assertIn("1 earlier return(s) to you are uncertain or stalled and are not repeated here", prompt)
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "uncertain", f"attention:{record['id']}": "submitted"})
        deliveries = sumctl.read_returns(self.store, task["id"])["deliveries"]
        self.assertEqual([d["obligations"] for d in deliveries if d["state"] == "submitted"], [[f"attention:{record['id']}"]])  # The sibling was not restamped.
        again = self.event("pane.agent_status_changed", "w-parent:p1", "idle")
        self.assertEqual(again["outcomes"][0]["prompts"], 0)
        self.assertEqual(len(self.prompts()), prompts_before + 1)

    def test_recipient_that_turns_busy_during_the_excerpt_read_is_not_prompted(self):
        task = self.started_task()
        self.enable()
        real_excerpt = sumctl.excerpt
        def slow_excerpt(session, pane):
            text = real_excerpt(session, pane)
            self.pane_state("w-parent:p1", agent_status="working")  # The root started a turn while `agent read` was running.
            return text
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}), mock.patch.object(sumctl, "excerpt", slow_excerpt):
            self.pane_state(task["pane"], agent_status="blocked", screen="Approve?")
            row = self.event("pane.agent_status_changed", task["pane"], "blocked")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual((worker["kind"], worker["parent_prompts"]), ("blocked", 0))
        self.assertEqual(len(self.parent_prompts()), 0)
        [record] = self.attention(task)
        self.assertEqual(self.open_returns(task)[f"attention:{record['id']}"], "not-delivered")
        self.assertGreaterEqual(row["herdr_calls"], 2)  # The pre-read snapshot was dropped; the pump observed again and saw `working`.
        with mock.patch.object(sumctl, "excerpt", slow_excerpt):
            live = sumctl.status(self.store, live=True)
        self.assertEqual(live["returns"]["prompts"], 0)
        self.assertEqual(len(self.parent_prompts()), 0)

    def test_out_of_order_working_does_not_close_a_live_blocked_attention(self):
        task = self.started_task()
        self.enable()
        self.pane_state(task["pane"], agent_status="blocked", screen="Approve?")
        self.event("pane.agent_status_changed", task["pane"], "blocked")
        [record] = self.attention(task)
        row = self.event("pane.agent_status_changed", task["pane"], "working")  # Stale payload: the pane is still at the dialog.
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual((worker["action"], worker["observed"], worker["closed"]), ("kept", "blocked", []))
        self.assertEqual([a["id"] for a in self.attention(task)], [record["id"]])
        self.pane_state(task["pane"], agent_status="idle")
        row = self.event("pane.agent_status_changed", task["pane"], "idle")  # The dialog cleared: any non-blocked fresh status closes it.
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["closed"], [record["id"]])
        self.assertEqual([a["kind"] for a in self.attention(task)], ["idle-without-report"])
        self.pane_state(task["pane"], remove=True)
        row = self.event("pane.agent_status_changed", task["pane"], "idle")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["action"], "unobservable")  # No snapshot row: the payload is not used as truth and nothing new is recorded.
        self.assertEqual(len(self.attention(task)), 1)

    def test_plain_pump_with_pending_attention_and_a_filtered_sibling_stamps_only_the_attention(self):
        task = self.started_task()
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PROMPT_HANG": "0.2"}), mock.patch.object(sumctl, "RECIPIENT_TIMEOUT", 0.05):
            q = self.question(task, text="Ambiguous?")["question"]
        time.sleep(0.3)  # The hung fake prompt still holds the store for FAKE_PROMPT_HANG seconds; its late save must not clobber the pane states below.
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            self.pane_state(task["pane"], agent_status="blocked", screen="Approve?")
            self.event("pane.agent_status_changed", task["pane"], "blocked")  # Parent busy: the attention stays pending.
        [record] = self.attention(task)
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "uncertain", f"attention:{record['id']}": "not-delivered"})
        prompts_before = len(self.prompts())
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            result = self.cli("pump")  # reason=None from the coordinator's own pane: no KeyError, presented inline before any observation.
        self.assertEqual(result.returncode, 0, result.stderr)
        [recipient] = json.loads(result.stdout)["recipients"]
        self.assertEqual((recipient["state"], recipient["via"]), ("submitted", "inline"))
        self.assertIn(f"attention {record['id']}", recipient["message"])
        self.assertNotIn(q["id"], recipient["message"])
        self.assertEqual(self.open_returns(task), {f"question:{q['id']}": "uncertain", f"attention:{record['id']}": "submitted"})
        self.assertEqual(len(self.prompts()), prompts_before)
        self.assertEqual(self.store.read(task["id"])["notice"]["reason"], "saved task state needs attention")
        # The same pass from outside the recipient pane takes the prompt path; a second pending attention exercises it with reason=None.
        self.pane_state(task["pane"], remove=True)
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            self.event("pane.exited", task["pane"])  # Busy root: the exit attention stays pending.
            [exited] = [a for a in self.attention(task) if a["kind"] == "exited"]
            self.assertEqual(sumctl.pump(self.store, None, inline=False)["prompts"], 0)  # Busy root, honest not-delivered, still no KeyError.
        result = sumctl.pump(self.store, None, inline=False)
        [recipient] = result["recipients"]
        self.assertEqual((recipient["state"], recipient["via"]), ("submitted", "prompt"), recipient)
        self.assertEqual(recipient["sent_obligations"], [f"attention:{exited['id']}"])
        self.assertEqual(len(self.prompts()), prompts_before + 1)
        self.assertNotIn(q["id"], self.prompts()[-1])
        deliveries = sumctl.read_returns(self.store, task["id"])["deliveries"]
        self.assertEqual(deliveries[-1]["obligations"], [f"attention:{exited['id']}"])  # Each attempt stamped exactly one attention; the uncertain question was never restamped.
        self.assertFalse(any(f"question:{q['id']}" in d["obligations"] for d in deliveries[1:]))

    # --- robustness -------------------------------------------------------------------------------------------------------

    def test_reordered_concurrent_and_duplicate_events_send_at_most_one_notice(self):
        task = self.started_task()
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            q = self.question(task)["question"]
        events = [("pane.agent_status_changed", "w-parent:p1", "idle")] * 4 + [("pane.agent_status_changed", "w-parent:p1", "working")] * 2
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
            results = list(pool.map(lambda e: self.cli_event(*e), events))
        for result in results:
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.parent_prompts()), 1)
        self.assertIn(q["id"], self.parent_prompts()[0])
        state = self.open_returns(task)[f"question:{q['id']}"]
        self.assertEqual(state, "submitted")
        self.assertEqual(self.health()["events"], 6)

    def test_handler_crash_is_recorded_and_never_blocks_task_writes(self):
        task = self.started_task()
        self.enable()
        with mock.patch.object(sumctl, "excerpt", side_effect=RuntimeError("boom")):
            self.pane_state(task["pane"], agent_status="blocked")
            with self.assertRaises(RuntimeError):
                sumctl.hook_event_main(self.store, {**os.environ, "HERDR_PLUGIN_ID": self.plugin_id(), "HERDR_PLUGIN_EVENT": "pane.agent_status_changed", "HERDR_SESSION": "sum-test",
                                                    "HERDR_PLUGIN_EVENT_JSON": json.dumps({"event": "pane_agent_status_changed", "data": {"type": "pane_agent_status_changed", "pane_id": task["pane"], "workspace_id": "w", "agent_status": "blocked"}})})
        self.assertEqual(self.health()["last_error"]["error"], "boom")  # Any exception is recorded, not only sum's own.
        self.assertEqual(self.attention(task), [])
        q = self.question(task, text="Still saved?")["question"]  # Task writes are independent of the handler.
        self.assertEqual(self.store.read(task["id"])["questions"][0]["id"], q["id"])
        (self.store.home / "hook" / "health.json").write_text("{not json")
        result = self.cli_event("pane.agent_status_changed", task["pane"], "idle")
        self.assertEqual(result.returncode, 1)
        self.assertIn("Cannot read", result.stderr)
        report = self.cli("report", task["id"], "--text", "Report while the hook is broken.", env={"HERDR_PANE_ID": task["pane"]})
        self.assertEqual(report.returncode, 0, report.stderr)
        self.assertIsNotNone(self.store.read(task["id"])["report"])
        summary = sumctl.hook_summary(self.store)
        self.assertTrue(summary["degraded"])
        self.assertIn("health unreadable", summary["reason"])
        live = sumctl.status(self.store, live=True)
        self.assertTrue(live["hook"]["degraded"])  # The rundown still works and says so.

    def test_rundown_reconciles_missed_events_only_when_enabled(self):
        task = self.started_task()
        self.pane_state(task["pane"], agent_status="idle", screen="Waiting for guidance...")
        live = sumctl.status(self.store, live=True)
        self.assertFalse(live["hook"]["enabled"])
        self.assertNotIn("attention_sweep", live)
        self.assertEqual(self.attention(task), [])
        self.assertTrue(live["tasks"][0]["attention"].startswith("No report."))
        self.enable()
        self.assertEqual(len(self.attention(task)), 1)  # Enable's own reconciliation recorded the already-idle worker.
        [record] = self.attention(task)
        sumctl.attention_seen(self.store, self.ctx(), task["id"], record["id"])
        self.pane_state(task["pane"], agent_status="blocked", screen="Approve?")
        live = sumctl.status(self.store, live=True)
        self.assertEqual([r["outcome"] for r in live["attention_sweep"] if r["task"] == task["id"]], ["recorded"])
        self.assertEqual([a["kind"] for a in self.attention(task)], ["blocked"])
        self.assertEqual(live["tasks"][0]["attention_records"][0]["kind"], "blocked")
        self.assertEqual(live["fanout"]["herdr_calls"], 2)  # One agent list for the sweep; the excerpt read invalidated it, so the pump observed again before prompting.
        self.assertEqual(len([c for c in self.calls() if c[:2] == ["agent", "read"]]), 2)  # One bounded excerpt per attention record.

    def test_fleet_of_twelve_workers_survives_update_disable_and_re_enable(self):
        settings = sumctl.write_settings(self.store, {"global": 13, "per_repository": 1})
        self.assertEqual(settings["capacity"], {"global": 13, "per_repository": 1})
        tasks = []
        for i in range(12):
            repo = self.root / f"fleet-{i:02d}"
            subprocess.run(["git", "clone", "--quiet", str(self.repo), str(repo)], check=True)
            task = self.prepare(repo=str(repo))
            sumctl.start(self.store, task["id"])
            tasks.append(self.store.read(task["id"]))
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            for task in tasks:
                self.question(task, key="fleet", text=f"Fleet question for {task['id']}?")
        self.assertEqual(len(self.parent_prompts()), 0)
        started = time.monotonic()
        row = self.event("pane.agent_status_changed", "w-parent:p1", "idle")
        wall_ms = round((time.monotonic() - started) * 1000)
        self.assertEqual(row["outcomes"][0]["prompts"], 1)
        [prompt] = self.parent_prompts()
        self.assertTrue(prompt.startswith("sum returns for the coordinator: 12 pending across 12 task(s)."))
        self.assertLessEqual(row["herdr_calls"], 3)
        print(f"HOOK-MEASURE {json.dumps({'case': 'twelve-workers-one-edge', 'handler_ms': row['handler_ms'], 'wall_ms': wall_ms, 'herdr_calls': row['herdr_calls']})}", file=sys.stderr)
        # Runtime change: the manifest command points at the installation entrypoint, so a runtime switch needs no relink.
        self.assertEqual(Path(sumctl.read_health(self.store)["command"][0]), test_core.ROOT / "bin" / "sumctl")
        sumctl.hook_disable(self.store, self.ctx())
        for task in tasks[:3]:
            self.pane_state(task["pane"], agent_status="blocked")
            self.assertEqual(self.event("pane.agent_status_changed", task["pane"], "blocked")["outcome"], "disabled")
        self.assertTrue(all(self.attention(t) == [] for t in tasks))
        result = self.enable()
        recorded = [r for r in result["reconciliation"]["attention"] if r["outcome"] == "recorded"]
        self.assertEqual(sorted(r["task"] for r in recorded), sorted(t["id"] for t in tasks[:3]))  # Already-blocked workers were caught up on re-enable.
        self.assertEqual(result["fanout"]["herdr_calls"], 1)  # One agent list served twelve workers; the pump presented inline to the caller, so no second observation was needed.
        self.assertEqual(len([c for c in self.calls() if c[:2] == ["agent", "read"]]), 3)
        [recipient] = result["reconciliation"]["returns"]["recipients"]
        self.assertEqual((recipient["state"], recipient["via"]), ("submitted", "inline"))
        self.assertTrue(recipient["message"].startswith("sum returns for the coordinator: 15 pending across 12 task(s)."))  # Three new attention items coalesced with the twelve open questions.


if __name__ == "__main__":
    unittest.main()
