"""Issue #18: sum task state projected into namespaced native Herdr tokens and optional notifications.

Everything runs against the strict fake Herdr whose metadata surface mirrors the lab-verified 0.9.0 CLI (token patches print
nothing, tokens ride `pane get`/`agent list`/`workspace get`, any source may clear a key, `notification show` reports the
user's toast delivery). No model, network, credentials, live installation state, or user `default` session.
"""
from __future__ import annotations
import argparse
import json
import os
from pathlib import Path
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_core
import test_cleanup
import test_hook

sumctl = test_core.sumctl


class MetadataTest(test_cleanup.CleanupTest):
    """Cleanup fixture (fake gh and lsof beside the fake Herdr) so merged-PR cleanup can be exercised; inherited cases run in their own files."""
    for _name in [n for n in dir(test_cleanup.CleanupTest) if n.startswith("test_")]:
        locals()[_name] = None  # Inherited cases stay where they belong; this file runs only the issue #18 cases.
    del _name

    def ctx(self):
        return sumctl.context()

    def enable(self, notify=False):
        return sumctl.metadata_enable(self.store, self.ctx(), notify=notify)

    def meta(self):
        return sumctl.read_metadata(self.store)

    def tokens(self, target):
        state = self.fake_state()
        table = state["panes"] if ":" in target else state["workspaces"]
        return (table.get(target) or {}).get("tokens", {})

    def sources(self, target):
        state = self.fake_state()
        table = state["panes"] if ":" in target else state["workspaces"]
        return (table.get(target) or {}).get("token_sources", {})

    def metadata_calls(self):
        return [c for c in self.calls() if c[1:2] == ["report-metadata"]]

    def notifications(self):
        return self.fake_state().get("notifications", [])

    def sync(self, tasks=None, reason="test"):
        return sumctl.metadata_sync(self.store, tasks=tasks, reason=reason)

    def started_task(self):
        task = self.prepare()
        sumctl.start(self.store, task["id"])
        self.pane_state(task["pane"], agent_status="idle")
        return self.store.read(task["id"])

    def pane_state(self, pane, **changes):
        test_core.CoreTest.pane_state(self, pane, **changes)

    def plugin_id(self):
        return sumctl.hook_plugin_id(self.store)

    # --- default off, enable, capabilities --------------------------------------------------------------------------

    def test_disabled_by_default_and_every_write_path_is_unchanged(self):
        task = self.started_task()
        q = self.question(task)
        self.assertEqual(q["notice"]["status"], "submitted-not-acknowledged")
        self.assertEqual(self.metadata_calls(), [])
        self.assertEqual(self.tokens(task["pane"]), {})
        summary = sumctl.status(self.store)["metadata"]
        self.assertFalse(summary["enabled"])
        self.assertTrue(summary["degraded"])
        self.assertIn("not enabled", summary["reason"])
        self.assertEqual(self.sync(), {"enabled": False, "skipped": True, "reason": "native metadata projection is not enabled"})
        self.assertFalse((self.store.home / "metadata").exists())

    def test_enable_probes_the_binary_records_a_namespaced_source_and_projects_once(self):
        task = self.started_task()
        self.question(task)
        result = self.enable()
        self.assertTrue(result["enabled"] and not result["notify"])
        instance = json.loads((self.store.home / "state.json").read_text())["instance"]
        self.assertEqual(result["source"], f"sum:{instance[:12]}")
        self.assertEqual({k: result["capabilities"][k] for k in ("pane_tokens", "workspace_tokens", "notification")}, {"pane_tokens": True, "workspace_tokens": True, "notification": True})
        self.assertTrue(any(c[:2] == ["api", "schema"] for c in self.calls()))
        self.assertEqual(self.tokens(task["pane"]), {"sum_state": "needs-decision", "sum_task": task["id"], "sum_repo": "repo with spaces"})
        self.assertEqual(self.tokens(task["workspace"]), {"sum_state": "needs-decision", "sum_task": task["id"], "sum_repo": "repo with spaces"})
        self.assertEqual(set(self.sources(task["pane"]).values()), {result["source"]})
        self.assertEqual(self.tokens("w-parent:p1"), {"sum_inbox": "1 decision", "sum_tasks": "1 active"})
        # The agent lifecycle, labels, titles, and state labels were never touched.
        self.assertFalse(any(c[:2] in (["pane", "report-agent"], ["pane", "rename"], ["workspace", "rename"], ["agent", "rename"]) for c in self.calls()))
        self.assertFalse(any("--title" in c or "--display-agent" in c or "--state-label" in c for c in self.metadata_calls()))
        self.assertTrue(all(k.startswith("sum_") for c in self.metadata_calls() for k in [a.split("=")[0] for a in c if "=" in a]))
        self.assertEqual(self.fake_state()["panes"][task["pane"]]["agent_status"], "idle")
        self.assertEqual(self.notifications(), [])  # Notifications stay off unless asked for.
        summary = sumctl.status(self.store)["metadata"]
        self.assertTrue(summary["enabled"] and not summary["degraded"])
        self.assertEqual(summary["resources"], 1)

    def test_enable_requires_the_coordinator(self):
        with self.pane("w-other:p1"):
            self.init()
            with self.assertRaisesRegex(sumctl.SumError, "not the registered coordinator"):
                sumctl.metadata_enable(self.store, sumctl.context())
        self.assertFalse((self.store.home / "metadata").exists())

    def test_missing_metadata_support_degrades_without_blocking_any_command(self):
        task = self.started_task()
        with mock.patch.dict(os.environ, {"FAKE_NO_METADATA": "1"}):
            with self.assertRaisesRegex(sumctl.SumError, "does not expose report-metadata"):
                self.enable()
            self.assertFalse(self.meta()["enabled"])
            q = self.question(task)  # ask keeps working: saved and notified synchronously.
            self.assertEqual(q["question"]["status"], "open")
            self.assertEqual(q["notice"]["status"], "submitted-not-acknowledged")
        # A build that passes the probe but later loses the command: the first refused write marks the capability off and the task is unaffected.
        self.enable()
        with mock.patch.dict(os.environ, {"FAKE_NO_METADATA": "1"}):
            answered = sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q["question"]["id"], text="Yes.", file=None))
            self.assertEqual(answered["question"]["status"], "answered")
            result = self.sync([task["id"]])
        self.assertTrue(result["enabled"])
        endpoints = {e["kind"]: e for e in result["tasks"][0]["endpoints"]}
        self.assertEqual(endpoints["workspace"]["outcome"], "failed")
        meta = self.meta()
        self.assertFalse(meta["capabilities"]["workspace_tokens"])
        self.assertIn("report-metadata failed", meta["degraded"])
        status = sumctl.status(self.store)["metadata"]
        self.assertTrue(status["degraded"] and status["enabled"])
        self.assertEqual(self.store.read(task["id"])["status"], "waiting")  # Task semantics untouched by presentation failures.
        resolved = sumctl.resolve(self.store, argparse.Namespace(task=task["id"], question=q["question"]["id"]))
        self.assertEqual(resolved["status"], "applied")

    # --- state derivation: sum state versus agent lifecycle ------------------------------------------------------------

    def test_task_state_disagrees_with_agent_state_without_touching_it(self):
        task = self.started_task()
        self.enable()
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "running")
        self.pane_state(task["pane"], agent_status="working")
        self.question(task)
        self.sync([task["id"]])
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "needs-decision")
        self.assertEqual(self.fake_state()["panes"][task["pane"]]["agent_status"], "working")  # Herdr's lifecycle is Herdr's.
        self.assertFalse(any(c[:2] == ["pane", "report-agent"] for c in self.calls()))
        # A report while a question is still open keeps the decision first; the coordinator inbox counts both kinds.
        sumctl.report(self.store, argparse.Namespace(task=task["id"], text="done", file=None, handoff=None))
        self.sync([task["id"]])
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "needs-decision")
        self.assertEqual(self.tokens("w-parent:p1")["sum_inbox"], "1 decision")

    def test_states_follow_the_records_in_the_order_the_boss_acts(self):
        task, sha = self.merged_task(reconcile=False)
        self.enable()
        self.assertEqual(self.tokens(task["workspace"])["sum_state"], "review-ready")
        self.assertEqual(self.tokens("w-parent:p1")["sum_inbox"], "1 review")
        record = sumctl.verify(self.store, argparse.Namespace(task=task["id"], candidate=sha, result="pass", text="checked", file=None))
        self.sync([task["id"]])
        self.assertEqual(self.tokens(task["workspace"])["sum_state"], "verified")
        self.reconcile(task)
        self.sync([task["id"]])
        tokens = self.tokens(task["workspace"])
        self.assertEqual(tokens["sum_state"], "merged-cleanup-pending")
        self.assertEqual(tokens["sum_pr"], "https://github.com/douglasjarquin/project/pull/7")
        self.assertEqual(self.tokens("w-parent:p1")["sum_inbox"], "1 cleanup")
        self.assertEqual(sumctl.metadata_status(self.store)["resources_detail"][task["id"]]["state"], "merged-cleanup-pending")

    def test_refresh_pending_shows_the_revision_mismatch_and_clears_on_adopt(self):
        task = self.started_task()
        self.enable()
        self.pane_state(task["pane"], agent_status="working")  # Busy worker: the refresh stays pending, the token says so.
        with self.altered_runtime():
            self.refresh(task=[task["id"]])
        self.sync([task["id"]])
        tokens = self.tokens(task["pane"])
        self.assertEqual((tokens["sum_state"], tokens["sum_rev"]), ("instruction-refresh-pending", "r1>r2"))
        self.assertEqual(self.tokens("w-parent:p1")["sum_inbox"], "1 refresh")
        sumctl.adopt_brief(self.store, task["id"], "r2")
        self.sync([task["id"]])
        tokens = self.tokens(task["pane"])
        self.assertEqual(tokens["sum_state"], "running")
        self.assertNotIn("sum_rev", tokens)
        self.assertEqual(self.tokens("w-parent:p1")["sum_inbox"], "clear")

    # --- coalescing and no-change writes ----------------------------------------------------------------------------

    def test_duplicate_events_and_unchanged_state_write_nothing(self):
        hook = test_hook.HookTest
        task = self.started_task()
        self.enable()
        sumctl.hook_enable(self.store, self.ctx())
        self.question(task)
        before = len(self.metadata_calls())
        event = hook.event.__get__(self)
        row = event("pane.agent_status_changed", task["pane"], "idle")
        self.assertEqual(row["outcome"], "handled")
        self.assertEqual(row["metadata"]["written"], 2)  # Pane and workspace moved to needs-decision; the root inbox line changed too.
        after_first = len(self.metadata_calls())
        self.assertGreater(after_first, before)
        for _ in range(3):
            row = event("pane.agent_status_changed", task["pane"], "idle")
            self.assertEqual(row["metadata"]["written"], 0)
        self.assertEqual(len(self.metadata_calls()), after_first)
        self.assertEqual(self.sync()["herdr_calls"], 0)  # Nothing differs: no snapshot, no `pane get`, no write.
        self.assertEqual(len(self.metadata_calls()), after_first)
        meta = self.meta()
        self.assertEqual(meta["stats"]["writes"], after_first)

    def test_notifications_are_opt_in_coalesced_and_never_carry_question_text(self):
        secret = "Rotate the token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123 now?"
        a = self.started_task()
        (self.root / "second").mkdir()
        self.git("init", "-b", "main", cwd=self.root / "second")
        self.git("config", "user.name", "sum test", cwd=self.root / "second")
        self.git("config", "user.email", "test@example.invalid", cwd=self.root / "second")
        (self.root / "second" / "README.md").write_text("x\n")
        self.git("add", ".", cwd=self.root / "second")
        self.git("commit", "-m", "fixture", cwd=self.root / "second")
        b = self.prepare(repo=str(self.root / "second"))
        self.enable(notify=True)
        self.assertEqual(self.notifications(), [])  # Nothing needs the boss yet.
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            self.question(a, text=secret, key="secret")
            sumctl.report(self.store, argparse.Namespace(task=b["id"], text="report body with " + secret, file=None, handoff=None))
        result = self.sync()
        [shown] = self.notifications()
        self.assertEqual(shown["title"], "sum: 2 tasks need you")
        self.assertEqual(shown["body"], f"{a['id']} needs-decision (repo with spaces) · {b['id']} review-ready (second)")
        self.assertEqual(shown["sound"], "request")
        self.assertNotIn("ghp_", json.dumps(shown))
        self.assertNotIn("Rotate", json.dumps(self.fake_state()))  # No token, title, or notification carries the question.
        self.assertEqual(result["notification"]["outcome"], "sent")
        self.assertFalse(result["notification"]["shown"])  # The user's toast delivery is off: reported, not hidden.
        self.assertEqual(result["notification"]["reason"], "disabled")
        self.sync()
        self.sync([a["id"]])
        self.assertEqual(len(self.notifications()), 1)  # Repeated passes with the same states notify nothing.
        with mock.patch.dict(os.environ, {"FAKE_TOAST": "herdr"}):
            q2 = self.question(a, text="Another decision?", key="second")
            self.sync([a["id"]])
        self.assertEqual(len(self.notifications()), 1)  # Still needs-decision: no new transition, no repeat.
        answer = sumctl.answer(self.store, argparse.Namespace(task=a["id"], question=q2["question"]["id"], text="ok", file=None))
        for q in self.store.read(a["id"])["questions"]:
            if q["status"] == "open":
                sumctl.answer(self.store, argparse.Namespace(task=a["id"], question=q["id"], text="ok", file=None))
        for q in self.store.read(a["id"])["questions"]:
            sumctl.resolve(self.store, argparse.Namespace(task=a["id"], question=q["id"]))
        self.sync([a["id"]])
        self.assertEqual(self.tokens(a["pane"])["sum_state"], "running")
        with mock.patch.dict(os.environ, {"FAKE_TOAST": "herdr"}):
            self.question(a, text="Third?", key="third")
            result = self.sync([a["id"]])
        self.assertEqual(len(self.notifications()), 2)  # A fresh transition after leaving the state notifies again, once.
        self.assertTrue(result["notification"]["shown"])
        self.assertEqual(self.meta()["stats"]["notifications"], 2)

    def test_notifications_off_by_default_remember_transitions_without_replaying_them(self):
        task = self.started_task()
        self.enable()
        self.question(task)
        self.sync([task["id"]])
        self.assertEqual(self.notifications(), [])
        self.enable(notify=True)  # Turning notifications on later does not replay the old needs-decision.
        self.assertEqual(self.notifications(), [])

    # --- identity: custom labels, stale panes, rebinding, two homes ---------------------------------------------------

    def test_custom_user_labels_and_foreign_tokens_are_preserved(self):
        task = self.started_task()
        state = self.fake_state()  # The user's own labels and another reporter's tokens, as Herdr would hold them.
        state["panes"][task["pane"]]["label"] = "my worker"
        state["panes"][task["pane"]]["tokens"] = {"jj_status": "2 changes", "sum_state": "foreign value"}
        state["panes"][task["pane"]]["token_sources"] = {"jj_status": "user:jj", "sum_state": "user:other"}
        state["workspaces"][task["workspace"]]["label"] = "my space"
        self.write_fake_state(state)
        self.enable()
        pane = self.fake_state()["panes"][task["pane"]]
        self.assertEqual(pane["label"], "my worker")
        self.assertEqual(pane["tokens"]["jj_status"], "2 changes")  # Another reporter's key is untouched.
        self.assertEqual(pane["tokens"]["sum_state"], "running")  # sum's own key names are sum's.
        self.assertEqual(self.fake_state()["workspaces"][task["workspace"]]["label"], "my space")
        sumctl.metadata_disable(self.store, self.ctx())
        pane = self.fake_state()["panes"][task["pane"]]
        self.assertEqual(pane["tokens"], {"jj_status": "2 changes"})
        self.assertEqual(pane["label"], "my worker")
        self.assertFalse(any(c[:2] in (["pane", "rename"], ["workspace", "rename"], ["agent", "rename"]) for c in self.calls()))

    def test_stale_pane_identity_gets_no_tokens_and_loses_the_old_ones(self):
        task = self.started_task()
        self.enable()
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "running")
        self.pane_state(task["pane"], cwd="/somewhere/else", agent=None)  # The pane now hosts something else in another checkout.
        self.question(task)
        result = self.sync([task["id"]])
        pane_row = next(e for e in result["tasks"][0]["endpoints"] if e["kind"] == "pane")
        self.assertEqual((pane_row["outcome"], pane_row["identity"]), ("skipped", "stale"))
        self.assertEqual(self.tokens(task["pane"]), {})  # Our stale keys were cleared; nothing new was written there.
        self.assertEqual(self.tokens(task["workspace"])["sum_state"], "needs-decision")  # The workspace is still the task's.
        self.assertNotIn("pane", self.meta()["resources"][task["id"]])
        self.pane_state(task["pane"], remove=True)
        self.answer_all(task)
        result = self.sync([task["id"]])
        pane_row = next(e for e in result["tasks"][0]["endpoints"] if e["kind"] == "pane")
        self.assertEqual(pane_row["outcome"], "absent")
        self.assertEqual(self.tokens(task["workspace"])["sum_state"], "running")

    def answer_all(self, task):
        for q in self.store.read(task["id"])["questions"]:
            if q["status"] == "open":
                sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q["id"], text="ok", file=None))
        for q in self.store.read(task["id"])["questions"]:
            if q["status"] == "answered":
                sumctl.resolve(self.store, argparse.Namespace(task=task["id"], question=q["id"]))

    def test_rebinding_clears_only_the_old_pane_and_projects_the_new_one(self):
        task = self.started_task()
        self.enable()
        old = task["pane"]
        new = "w-new:p7"
        self.set_pane(new, cwd=task["worktree"], agent="codex", agent_status="idle")
        state = self.fake_state()
        state["panes"][old]["tokens"]["other"] = "keep"
        self.write_fake_state(state)
        self.cli("bind", task["id"], "--worker-pane", new)
        self.assertEqual(self.store.read(task["id"])["pane"], new)
        self.assertEqual(self.tokens(old), {"other": "keep"})  # Only sum's keys left the old pane.
        self.assertEqual(self.tokens(new)["sum_task"], task["id"])
        self.assertEqual(self.meta()["resources"][task["id"]]["pane"]["id"], new)

    def test_two_sum_homes_project_only_their_own_endpoints(self):
        task = self.started_task()
        self.enable()
        other_root, other_store = self.installation("other")
        with self.pane("w-other:p1"):
            self.init(store=other_store)
            other_task = sumctl.prepare(other_store, argparse.Namespace(repo=str(other_root), brief=str(self.brief), harness="codex", base="HEAD", kind="ship", approved=True, arg=[]))
            result = sumctl.metadata_enable(other_store, sumctl.context())
        self.assertNotEqual(result["source"], self.meta()["source"])
        self.assertEqual(self.tokens(task["pane"])["sum_task"], task["id"])
        self.assertEqual(self.tokens(other_task["workspace"])["sum_task"], other_task["id"])
        self.assertEqual(self.tokens("w-other:p1")["sum_tasks"], "1 active")
        self.assertEqual(self.tokens("w-parent:p1")["sum_tasks"], "1 active")
        self.assertEqual(set(self.sources(task["pane"]).values()), {self.meta()["source"]})
        self.assertEqual(set(self.sources(other_task["workspace"]).values()), {result["source"]})
        with self.pane("w-other:p1"):
            sumctl.metadata_disable(other_store, sumctl.context())
        self.assertEqual(self.tokens(other_task["workspace"]), {})
        self.assertEqual(self.tokens("w-other:p1"), {})
        self.assertEqual(self.tokens(task["pane"])["sum_task"], task["id"])  # The first home's tokens survive the other home's disable.
        self.assertEqual(self.tokens("w-parent:p1")["sum_tasks"], "1 active")

    # --- blocked worker with a private question, cleanup, archive --------------------------------------------------------

    def test_blocked_worker_with_private_question_is_visible_without_its_text(self):
        hook = test_hook.HookTest
        task = self.started_task()
        self.enable(notify=True)
        sumctl.hook_enable(self.store, self.ctx())
        self.pane_state(task["pane"], agent_status="blocked", screen="May I delete the production database credentials file? (y/n)")
        row = hook.event.__get__(self)("pane.agent_status_changed", task["pane"], "blocked")
        worker = [o for o in row["outcomes"] if o.get("task") == task["id"]][0]
        self.assertEqual(worker["kind"], "blocked")
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "attention-blocked")
        self.assertEqual(self.tokens("w-parent:p1")["sum_inbox"], "1 blocked")
        [shown] = self.notifications()
        self.assertEqual(shown["body"], f"{task['id']} attention-blocked (repo with spaces)")
        self.assertNotIn("database", json.dumps(self.fake_state()["panes"][task["pane"]]["tokens"]) + json.dumps(shown))
        # The worker saves the real question: the record supersedes native attention and the token follows the record.
        with mock.patch.dict(os.environ, {"FAKE_PARENT_STATUS": "working"}):
            self.question(task, text="Delete the production credentials file?", key="creds")
        self.sync([task["id"]])
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "needs-decision")
        state = self.fake_state()
        self.assertNotIn("credentials", json.dumps([state["panes"][task["pane"]]["tokens"], state["panes"]["w-parent:p1"]["tokens"], state["notifications"]]))
        self.assertEqual(len(self.notifications()), 2)
        self.assertEqual(self.notifications()[1]["body"], f"{task['id']} needs-decision (repo with spaces)")

    def test_cleanup_and_archive_release_only_the_tokens_sum_owns(self):
        task, sha = self.merged_task()
        self.enable()
        self.assertEqual(self.tokens(task["workspace"])["sum_state"], "merged-cleanup-pending")
        state = self.fake_state()
        state["workspaces"][task["workspace"]]["tokens"]["jj_status"] = "clean"
        state["panes"]["w-parent:p1"]["tokens"]["other"] = "user"
        self.write_fake_state(state)
        self.set_pane(task["pane"], agent=None, processes=[])
        self.lsof()
        self.cleanup(task)
        self.cleanup(task, apply=True)
        self.assertEqual(self.store.read(task["id"])["status"], "archived")
        result = self.cli("metadata", "sync")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn(task["workspace"], self.fake_state()["workspaces"])  # Removed by the native worktree operation, not by metadata.
        self.assertNotIn(task["id"], self.meta()["resources"])
        self.assertEqual(self.tokens("w-parent:p1"), {"other": "user", "sum_inbox": "clear", "sum_tasks": "0 active"})
        # Archive without cleanup: the pane and workspace remain and lose exactly sum's keys.
        second = self.started_task()
        self.sync()
        state = self.fake_state()
        state["panes"][second["pane"]]["tokens"]["mine"] = "kept"
        self.write_fake_state(state)
        self.set_pane(second["pane"], agent=None, processes=[])
        attempt = self.store.read(second["id"])["execution"]["worker"]["id"]
        parked = self.cli("execution", "park", second["id"], "--attempt", attempt)
        self.assertEqual(parked.returncode, 0, parked.stderr)
        archived = self.cli("archive", second["id"], "--acknowledge")
        self.assertEqual(archived.returncode, 0, archived.stderr)
        self.assertEqual(self.tokens(second["pane"]), {"mine": "kept"})
        self.assertEqual(self.tokens(second["workspace"]), {})
        self.assertNotIn(second["id"], self.meta()["resources"])
        clears = [c for c in self.metadata_calls() if "--clear-token" in c]
        self.assertTrue(all(a.startswith("sum_") for c in clears for a in [c[i + 1] for i, x in enumerate(c) if x == "--clear-token"]))

    # --- helper path, hook path, snippet, inbox, disable -----------------------------------------------------------------

    def test_cli_write_paths_project_after_the_record_and_never_change_the_result(self):
        task = self.started_task()
        self.enable()
        result = self.cli("ask", task["id"], "--key", "cli", "--text", "From the CLI?")
        self.assertEqual(result.returncode, 0, result.stderr)
        value = json.loads(result.stdout)
        self.assertEqual(set(value), {"question", "notice"})  # The output shape every existing reader knows.
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "needs-decision")
        with mock.patch.dict(os.environ, {"FAKE_NO_METADATA": "1"}):
            result = self.cli("report", task["id"], "--text", "done")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["status"], "reported-not-verified")
        self.assertEqual(self.store.read(task["id"])["status"], "reported")
        live = self.cli("inbox", "--live")
        self.assertEqual(live.returncode, 0, live.stderr)
        self.assertTrue(json.loads(live.stdout)["metadata"]["enabled"])
        self.assertEqual(self.cli("metadata", "status").returncode, 0)

    def test_snippet_is_text_only_and_the_inbox_entrypoint_rides_the_hook_plugin(self):
        snippet = sumctl.metadata_snippet(self.store)
        self.assertIn("$sum_state", snippet["toml"])
        self.assertIn("$sum_inbox", snippet["toml"])
        self.assertIn("[ui.sidebar.spaces]", snippet["toml"])
        raw = self.cli("metadata", "snippet", "--raw")
        self.assertEqual(raw.stdout, snippet["toml"])
        config = Path(os.environ.get("XDG_CONFIG_HOME", self.root / "config"))
        self.assertFalse((config / "herdr" / "config.toml").exists())  # Never written by sum.
        with self.assertRaisesRegex(sumctl.SumError, "hook enable"):
            sumctl.metadata_inbox(self.store, self.ctx())
        hook = sumctl.hook_enable(self.store, self.ctx())
        manifest = Path(hook["manifest"]).read_text()
        self.assertIn('[[panes]]', manifest)
        self.assertIn('id = "inbox"', manifest)
        self.assertIn('placement = "popup"', manifest)
        self.assertIn(str(self.store.home), manifest)
        opened = sumctl.metadata_inbox(self.store, self.ctx())
        self.assertEqual(opened["placement"], "popup")
        self.assertIsNone(opened["pane"])  # A popup has no pane id.
        command = opened["result"]["plugin_pane"]["command"]
        self.assertEqual(command[-3:], ["--home", str(self.store.home), "inbox"])  # Records-only listing, no --live, no prompt.
        self.assertEqual(command[-4], str(test_core.ROOT / "bin" / "sumctl"))
        split = sumctl.metadata_inbox(self.store, self.ctx(), placement="split")
        self.assertEqual(split["pane"].split(":")[0], "w-parent")
        call = [c for c in self.calls() if c[:3] == ["plugin", "pane", "open"]][-1]
        self.assertIn("--no-focus", call)
        self.assertIn("--target-pane", call)
        with self.assertRaisesRegex(sumctl.SumError, "placement"):
            sumctl.metadata_inbox(self.store, self.ctx(), placement="fullscreen")

    def test_server_restart_loses_tokens_and_a_rundown_or_startup_writes_them_again(self):
        hook = test_hook.HookTest
        task = self.started_task()
        self.enable()
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "running")
        state = self.fake_state()  # Herdr keeps token metadata in memory only: a restart empties every pane and workspace.
        for pane in state["panes"].values():
            pane.pop("tokens", None); pane.pop("token_sources", None)
        for workspace in state["workspaces"].values():
            workspace.pop("tokens", None); workspace.pop("token_sources", None)
        self.write_fake_state(state)
        self.assertEqual(self.sync([task["id"]])["herdr_calls"], 0)  # An ordinary task write still believes the record.
        self.assertEqual(self.tokens(task["pane"]), {})
        live = sumctl.status(self.store, live=True)
        self.assertTrue(live["metadata"]["enabled"])
        result = sumctl.metadata_sync(self.store, reason="rundown", reconcile=True)
        self.assertEqual(len(result["forgotten"]), 3, result["forgotten"])  # Worker pane, workspace, coordinator pane.
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "running")
        self.assertEqual(self.tokens(task["workspace"])["sum_task"], task["id"])
        self.assertEqual(self.tokens("w-parent:p1")["sum_tasks"], "1 active")
        sumctl.hook_enable(self.store, self.ctx())
        state = self.fake_state()
        state["workspaces"][task["workspace"]].pop("tokens", None)
        self.write_fake_state(state)
        row = hook.event.__get__(self)("startup")  # The startup hook after a restart rewrites what Herdr dropped.
        self.assertEqual(row["outcome"], "reconciled")
        self.assertEqual(row["metadata"]["forgotten"], 1)
        self.assertEqual(self.tokens(task["workspace"])["sum_task"], task["id"])
        self.assertEqual(self.cli("inbox", "--live").returncode, 0)

    def test_disable_clears_recorded_tokens_and_leaves_the_synchronous_path(self):
        task = self.started_task()
        self.enable()
        self.question(task)
        self.sync()
        self.assertEqual(self.tokens(task["pane"])["sum_state"], "needs-decision")
        result = sumctl.metadata_disable(self.store, self.ctx())
        self.assertFalse(result["enabled"])
        self.assertEqual({(c.get("task"), c["kind"]) for c in result["cleared"]}, {(task["id"], "pane"), (task["id"], "workspace"), (None, "pane")})
        self.assertEqual(self.tokens(task["pane"]), {})
        self.assertEqual(self.tokens("w-parent:p1"), {})
        before = len(self.metadata_calls())
        sumctl.report(self.store, argparse.Namespace(task=task["id"], text="done", file=None, handoff=None))
        self.assertEqual(self.sync()["skipped"], True)
        self.assertEqual(len(self.metadata_calls()), before)
        self.assertEqual(self.store.read(task["id"])["status"], "reported")


class MetadataFleetTest(test_core.UpdateLab):
    """Twelve dispatched workers through a live update and rolling refresh with projection enabled: bounded calls, exact states."""

    def ctl(self, root, store, *argv, env=None):
        result = self.cli([root / "bin" / "sumctl", "--home", store.home, *argv], env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def cli(self, argv, env=None):
        import subprocess
        merged = os.environ.copy()
        merged.update(env or {})
        return subprocess.run([str(a) for a in argv], env=merged, capture_output=True, text=True)

    def fake_state(self):
        return json.loads((self.root / "fake/state.json").read_text())

    def calls(self):
        path = self.root / "fake/calls.jsonl"
        return [json.loads(line)["args"] for line in path.read_text().splitlines()] if path.exists() else []

    def project(self, name):
        repo = self.root / "projects" / name
        repo.mkdir(parents=True)
        self.git("init", "-b", "main", cwd=repo)
        self.git("config", "user.name", "sum test", cwd=repo)
        self.git("config", "user.email", "test@example.invalid", cwd=repo)
        (repo / "README.md").write_text(f"{name}\n")
        self.git("add", ".", cwd=repo)
        self.git("commit", "-q", "-m", "fixture", cwd=repo)
        return repo

    def test_live_update_with_twelve_workers_projects_every_refresh_once_with_one_snapshot(self):
        root, store = self.installation()
        env = {"FAKE_PARENT_CWD": str(test_core.ROOT)}  # The fake coordinator pane runs where this lab's helper records its own cwd, as a real coordinator pane does.
        self.ctl(root, store, "settings", "set", "--global", "12", "--per-repository", "1", env=env)
        brief = self.root / "brief.md"
        brief.write_text("Do the approved thing.")
        tasks = [self.ctl(root, store, "dispatch", "--repo", self.project(f"repo-{i:02d}"), "--brief", brief, "--harness", "codex", "--approved", env=env) for i in range(12)]
        enabled = self.ctl(root, store, "metadata", "enable", env=env)
        self.assertTrue(enabled["enabled"])
        self.assertEqual(enabled["fanout"]["herdr_calls"], 1)  # One `agent list` verified all twelve worker panes before their first write.
        state = self.fake_state()
        for task in tasks:
            self.assertEqual(state["panes"][task["pane"]]["tokens"]["sum_state"], "running")
            self.assertEqual(state["workspaces"][task["workspace"]]["tokens"]["sum_task"], task["id"])
        self.assertEqual(state["panes"]["w-parent:p1"]["tokens"], {"sum_inbox": "clear", "sum_tasks": "12 active"})
        writes = [c for c in self.calls() if c[1:2] == ["report-metadata"]]
        self.assertEqual(len(writes), 25)  # 12 panes + 12 workspaces + the coordinator pane; nothing else.
        for task in tasks:
            state = self.fake_state()
            state["panes"][task["pane"]]["agent_status"] = "working"  # Every worker is mid-turn: the refresh cannot be delivered yet.
            (self.root / "fake/state.json").write_text(json.dumps(state))
        sha = self.commit_upstream(root, "skills/sum-worker/SKILL.md", (root / "skills/sum-worker/SKILL.md").read_text() + "\nUpdate: reread decisions.\n")
        staged = sumctl.stage(store, sha, installer=test_core.fake_installer)
        self.assertTrue(staged["staged"])
        applied = self.ctl(root, store, "update", "apply", env=env)
        self.assertEqual(applied["default"]["sha"], sha)
        before = len(self.calls())
        refresh = self.ctl(root, store, "refresh", "request", env=env)
        delta = self.calls()[before:]
        rows = [r for r in refresh["targets"] if r["target"] == "task"]
        self.assertEqual({r["state"] for r in rows}, {"pending-busy"})
        state = self.fake_state()
        for task in tasks:
            self.assertEqual(state["panes"][task["pane"]]["tokens"]["sum_state"], "instruction-refresh-pending")
            self.assertEqual(state["panes"][task["pane"]]["tokens"]["sum_rev"], "r1>r2")
            self.assertEqual(state["panes"][task["pane"]]["agent_status"], "working")  # Still Herdr's lifecycle; untouched.
            self.assertEqual(state["workspaces"][task["workspace"]]["tokens"]["sum_state"], "instruction-refresh-pending")
        self.assertEqual(state["panes"]["w-parent:p1"]["tokens"]["sum_inbox"], "12 refresh · contract r1")  # The coordinator's own contract revision is requested too.
        self.assertEqual(sum(1 for c in delta if c[:2] == ["agent", "list"]), 2)  # One snapshot for the refresh pass, one for the projection pass.
        self.assertEqual(sum(1 for c in delta if c[1:2] == ["report-metadata"]), 25)
        self.assertEqual(sum(1 for c in delta if c[:2] == ["agent", "get"]), 0)
        again = self.ctl(root, store, "metadata", "sync", env=env)
        self.assertEqual((again["herdr_calls"], again["forgotten"]), (2, []))  # An explicit sync compares Herdr's held tokens (one snapshot, one workspace list) and writes nothing.
        self.assertFalse(any(e["outcome"] == "written" for r in again["tasks"] for e in r["endpoints"]))
        for task in tasks[:3]:
            self.ctl(root, store, "brief", "adopt", task["id"], "r2", env=env)
        state = self.fake_state()
        self.assertEqual([state["panes"][t["pane"]]["tokens"]["sum_state"] for t in tasks[:3]], ["running"] * 3)
        self.assertEqual(state["panes"]["w-parent:p1"]["tokens"]["sum_inbox"], "9 refresh · contract r1")
        checkouts = {t["id"]: self.git("status", "--porcelain", cwd=t["worktree"]) for t in tasks}
        self.assertEqual(set(checkouts.values()), {""})  # No worker checkout was touched by any of this.


if __name__ == "__main__":
    unittest.main()
