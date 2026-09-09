"""Issue #10: guarded, idempotent cleanup of merged task panes and checkouts without losing work."""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl


class CleanupTest(core.CoreTest):
    """Fake Herdr with the cleanup surface, fake gh, fake lsof, real Git worktrees. Inherited core cases run in test_core only."""
    def setUp(self):
        super().setUp()
        self.gh_root = self.root / "fake-gh"
        self.lsof_root = self.root / "fake-lsof"
        patch = mock.patch.dict(os.environ, {"SUM_GH_BIN": str(ROOT / "tests/fixtures/gh.py"), "FAKE_GH_ROOT": str(self.gh_root),
                                             "SUM_LSOF_BIN": str(ROOT / "tests/fixtures/lsof.py"), "FAKE_LSOF_ROOT": str(self.lsof_root)})
        patch.start()
        self.addCleanup(patch.stop)

    # --- fixture helpers -------------------------------------------------------------------------------------------

    def fake_state(self):
        return json.loads((self.root / "fake/state.json").read_text())

    def write_fake_state(self, state):
        (self.root / "fake/state.json").write_text(json.dumps(state))

    def set_pane(self, pane_id, **changes):
        state = self.fake_state()
        state["panes"].setdefault(pane_id, {"pane_id": pane_id, "cwd": "/tmp", "workspace_id": pane_id.split(":")[0], "agent_status": "unknown", "agent": None})
        state["panes"][pane_id].update(changes)
        state["workspaces"].setdefault(pane_id.split(":")[0], {"workspace_id": pane_id.split(":")[0], "label": "", "worktree": None})
        self.write_fake_state(state)

    def lsof(self, *processes, fail=None):
        self.lsof_root.mkdir(exist_ok=True)
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": list(processes), "fail": fail}))

    def scenario(self, **value):
        self.gh_root.mkdir(exist_ok=True)
        (self.gh_root / "pr.json").write_text(json.dumps(value))

    def commit(self, task, name, content=None, cwd=None):
        cwd = cwd or task["worktree"]
        (Path(cwd) / name).write_text(content if content is not None else f"{name}\n")
        self.git("add", name, cwd=cwd)
        self.git("commit", "-m", f"add {name}", cwd=cwd)
        return self.git("rev-parse", "HEAD", cwd=cwd)

    def report(self, task, sha):
        handoff = self.root / "handoff.json"
        handoff.write_text(json.dumps({"outcome": "completed", "candidate": sha, "next_action": "coordinator verification and PR", "review": "none"}))
        return sumctl.report(self.store, argparse.Namespace(task=task["id"], text="done", file=None, handoff=str(handoff)))

    def merged(self, task, sha, **changes):
        value = {"repository": "douglasjarquin/project", "number": 7, "head_branch": task["branch"], "head_sha": sha, "state": "MERGED",
                 "merged_at": "2026-09-06T00:00:00Z", "merge_commit": "f" * 40}
        value.update(changes)
        self.scenario(**value)

    def reconcile(self, task, number=7):
        return sumctl.pr_reconcile(self.store, argparse.Namespace(task=task["id"], number=number, repo=None, replace=False))

    def merged_task(self, reconcile=True):
        """A dispatched task whose worker committed, handed off, and whose exact PR head is merged; the agent has exited."""
        task = self.prepare()
        sha = self.commit(task, "greeting.py")
        self.report(task, sha)
        self.merged(task, sha)
        if reconcile:
            self.reconcile(task)
        return self.store.read(task["id"]), sha

    def cleanup(self, task, apply=False, reviewer_only=False, number=None):
        return sumctl.cleanup(self.store, argparse.Namespace(task=task["id"], apply=apply, reviewer_only=reviewer_only, number=number))

    def refused(self, task, pattern, **kwargs):
        with self.assertRaisesRegex(sumctl.SumError, pattern):
            self.cleanup(task, apply=True, **kwargs)
        saved = self.store.read(task["id"])
        self.assertTrue(Path(task["worktree"]).is_dir(), "the checkout must survive a refused cleanup")
        self.assertIn(task["workspace"], self.fake_state()["workspaces"])
        self.assertNotEqual(saved["status"], "archived")
        self.assertEqual(saved["cleanup"]["state"], "blocked")
        return saved

    def herdr_calls(self, *prefix):
        return [c for c in self.calls() if c[:len(prefix)] == list(prefix)]

    def records(self, task):
        return {str(p.relative_to(self.store.home)): p.read_bytes() for p in self.store.path(task["id"]).rglob("*") if p.is_file() and p.name != "task.json"}

    # --- merged clean PR -------------------------------------------------------------------------------------------

    def test_merged_clean_pr_removes_only_the_workspace_and_keeps_branch_and_records(self):
        task, sha = self.merged_task()
        records = self.records(task)
        records[str((self.store.path(task["id"]) / ".cleanup.lock").relative_to(self.store.home))] = b""
        plan = self.cleanup(task)
        self.assertEqual((plan["state"], plan["blockers"], plan["apply"]), ("ready", [], False))
        self.assertEqual(plan["resources"], {"workspace": "present", "pane": "present", "worktree": "present", "branch": "present", "reviewer_pane": None})
        self.assertTrue(Path(task["worktree"]).is_dir())  # Inspection removed nothing.
        self.assertEqual(self.store.read(task["id"])["cleanup"]["state"], "ready")
        result = self.cleanup(task, apply=True)
        self.assertEqual((result["state"], result["archived"], result["removed"]["performed"], result["removed"]["forced"]), ("complete", True, True, False))
        self.assertFalse(Path(task["worktree"]).exists())
        self.assertNotIn(task["workspace"], self.fake_state()["workspaces"])
        self.assertNotIn(task["pane"], self.fake_state()["panes"])
        self.assertEqual(self.git("rev-parse", task["branch"]), sha)  # Branch and history survive.
        self.assertEqual(self.git("rev-parse", "HEAD"), self.base)  # The primary checkout is untouched.
        self.assertEqual(self.git("status", "--porcelain"), "")
        saved = self.store.read(task["id"])
        self.assertEqual((saved["status"], saved["cleanup"]["state"]), ("archived", "complete"))
        self.assertEqual(self.records(task), records)  # Brief, revisions, and sidecars are byte-identical.
        self.assertEqual([r["kind"] for r in saved["evidence"]][:3], ["report", "handoff", "publication"])
        self.assertEqual(saved["report"]["text"], "done")
        removal = self.herdr_calls("worktree", "remove")
        self.assertEqual(removal, [["worktree", "remove", "--workspace", task["workspace"]]])  # Native, once, without --force.
        self.assertFalse(any("--force" in c for c in self.calls()))
        self.assertFalse(any(c[:1] == ["git"] for c in self.calls()))
        self.assertFalse(sumctl.holds_slot(saved))

    def test_squash_and_rebase_merges_need_no_ancestry_of_the_default_branch(self):
        for style in ("squash", "rebase"):
            with self.subTest(style=style):
                task = self.prepare()
                sha = self.commit(task, f"{style}.py")
                self.report(task, sha)
                if style == "squash":
                    merged = self.commit(task, f"{style}.py", content=f"{style}\n", cwd=self.repo)  # A different commit with the same content lands on main.
                else:
                    (self.repo / "other.txt").write_text("unrelated\n")
                    self.git("add", "other.txt"); self.git("commit", "-m", "unrelated")
                    merged = self.commit(task, f"{style}.py", content=f"{style}\n", cwd=self.repo)
                self.assertNotEqual(subprocess.run(["git", "-C", str(self.repo), "merge-base", "--is-ancestor", sha, "main"]).returncode, 0)
                self.merged(task, sha, merge_commit=merged)
                self.reconcile(task)
                result = self.cleanup(task, apply=True)
                self.assertEqual(result["state"], "complete")
                self.assertEqual(self.git("rev-parse", task["branch"]), sha)
                sumctl.main(["--home", str(self.store.home), "archive", task["id"], "--acknowledge"])  # Still records-only and harmless afterwards.
                self.git("checkout", "-q", self.base); self.git("checkout", "-q", "main")
                self.git("reset", "-q", "--hard", self.base)  # Reset the fixture's main only (a lab repo), for the next style.

    def test_duplicate_cleanup_is_idempotent_and_observes_nothing(self):
        task, _ = self.merged_task()
        self.cleanup(task, apply=True)
        before = len(self.calls())
        again = self.cleanup(task, apply=True)
        self.assertEqual((again["state"], again["already"]), ("complete", True))
        self.assertEqual(len(self.calls()), before)  # No Herdr call for an already-complete cleanup.
        self.assertEqual(self.cleanup(task)["already"], True)

    # --- GitHub says no ----------------------------------------------------------------------------------------------

    def test_closed_unmerged_pr_blocks(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        self.report(task, sha)
        self.merged(task, sha, state="CLOSED", merged_at=None, merge_commit=None)
        self.reconcile(task)
        saved = self.refused(task, "not merged with a merge commit")
        self.assertEqual([b["code"] for b in saved["cleanup"]["blockers"]], ["pr"])

    def test_network_or_auth_failure_is_uncertain_and_blocks(self):
        task, sha = self.merged_task()
        self.scenario(fail="HTTP 401: Bad credentials", number=7, head_branch=task["branch"], head_sha=sha)
        saved = self.refused(task, "recorded as uncertain")
        self.assertEqual([b["code"] for b in saved["cleanup"]["blockers"]], ["pr-uncertain"])
        self.assertEqual(saved["evidence"][-1]["outcome"], "uncertain")
        self.assertTrue(saved["pr"]["merged_for_task"])  # The last exact observation stays in place.

    def test_pr_head_changed_since_reconcile_blocks(self):
        task, sha = self.merged_task()
        self.commit(task, "b.py")  # A later push moved the branch; GitHub now reports a head that is not the recorded merged head.
        new_head = self.git("rev-parse", "HEAD", cwd=task["worktree"])
        self.merged(task, new_head)
        saved = self.refused(task, "PR head moved")
        self.assertIn("pr", [b["code"] for b in saved["cleanup"]["blockers"]])
        self.merged(task, "0" * 40)  # A head sum never saw is not a candidate of this task.
        saved = self.refused(task, "not a recorded candidate")

    def test_missing_pr_identity_blocks_unless_number_given(self):
        task, sha = self.merged_task(reconcile=False)
        saved = self.refused(task, "no complete PR identity recorded")
        self.assertIsNone(saved["pr"])
        result = self.cleanup(task, apply=True, number=7)
        self.assertEqual(result["state"], "complete")
        self.assertEqual(self.store.read(task["id"])["pr"]["identity"]["number"], 7)

    # --- the checkout holds work the merge does not -----------------------------------------------------------------

    def test_extra_local_commit_blocks(self):
        task, sha = self.merged_task()
        extra = self.commit(task, "later.py")
        saved = self.refused(task, "1 commit\\(s\\) in the checkout are not in the merged PR head")
        self.assertEqual(self.git("rev-parse", task["branch"]), extra)

    def test_dirty_untracked_and_ignored_files_block_but_known_caches_do_not(self):
        task = self.prepare()
        (Path(task["worktree"]) / ".gitignore").write_text("*.log\n__pycache__/\n")
        self.git("add", ".gitignore", cwd=task["worktree"])
        self.git("commit", "-m", "ignore", cwd=task["worktree"])
        sha = self.git("rev-parse", "HEAD", cwd=task["worktree"])
        self.report(task, sha)
        self.merged(task, sha)
        self.reconcile(task)
        worktree = Path(task["worktree"])
        (worktree / "README.md").write_text("changed\n")
        saved = self.refused(task, "modified tracked files")
        self.git("checkout", "--", "README.md", cwd=task["worktree"])
        (worktree / "notes.txt").write_text("keep me\n")
        saved = self.refused(task, "untracked files")
        (worktree / "notes.txt").unlink()
        (worktree / "run.log").write_text("valuable trace\n")
        saved = self.refused(task, "ignored files that are not known disposable caches")
        self.assertEqual(saved["cleanup"]["blockers"][0]["code"], "artifacts")
        self.assertTrue((worktree / "run.log").exists())
        (worktree / "run.log").unlink()
        (worktree / "__pycache__").mkdir()
        (worktree / "__pycache__/m.cpython-312.pyc").write_bytes(b"\x00")
        plan = self.cleanup(task)
        self.assertEqual(plan["state"], "ready")
        self.assertEqual(plan["artifacts"]["ignored_disposable"], ["__pycache__/"])  # Git reports a fully ignored directory as one entry.
        self.assertEqual(self.cleanup(task, apply=True)["state"], "complete")

    def test_worktree_artifacts_classification(self):
        task = self.prepare()
        worktree = Path(task["worktree"])
        (worktree / ".gitignore").write_text("*.log\nnode_modules/\n")
        self.git("add", ".gitignore", cwd=task["worktree"]); self.git("commit", "-m", "ignore", cwd=task["worktree"])
        (worktree / "README.md").write_text("m\n")
        (worktree / "staged.txt").write_text("s\n"); self.git("add", "staged.txt", cwd=task["worktree"])
        (worktree / "new.txt").write_text("n\n")
        (worktree / "a.log").write_text("l\n")
        (worktree / "node_modules").mkdir(); (worktree / "node_modules/x.js").write_text("x\n")
        self.assertEqual(sumctl.worktree_artifacts(task["worktree"]), {"tracked_modified": ["README.md"], "staged": ["staged.txt"], "untracked": ["new.txt"],
                                                                        "ignored_preserved": ["a.log"], "ignored_disposable": ["node_modules/"]})

    # --- occupants ---------------------------------------------------------------------------------------------------

    def test_busy_agent_blocks_even_when_idle(self):
        task, sha = self.merged_task()
        self.set_pane(task["pane"], agent="codex", agent_status="idle")
        saved = self.refused(task, "Herdr idle/done is not exit")
        self.assertEqual({b["code"] for b in saved["cleanup"]["blockers"]}, {"occupant"})
        self.set_pane(task["pane"], agent=None, agent_status="unknown")
        self.assertEqual(self.cleanup(task, apply=True)["state"], "complete")

    def test_foreground_process_and_detached_child_block(self):
        task, sha = self.merged_task()
        self.set_pane(task["pane"], processes=[{"pid": 4242, "name": "bash", "argv0": "bash", "cwd": task["worktree"]},
                                               {"pid": 5150, "name": "pytest", "argv0": "pytest", "cmdline": "pytest -x", "cwd": task["worktree"]}])
        self.refused(task, "foreground processes besides its shell: pytest \\(pid 5150\\)")
        self.set_pane(task["pane"], processes=[{"pid": 4242, "name": "bash", "argv0": "bash", "cwd": task["worktree"]}])
        self.lsof({"pid": 4242, "cwd": task["worktree"]}, {"pid": 7777, "cwd": task["worktree"] + "/sub"})  # The pane shell is excluded; a detached writer is not.
        saved = self.refused(task, "processes still run inside the checkout .*pid 7777")
        self.lsof(fail="lsof: permission denied")
        saved = self.refused(task, "cannot be established")
        self.lsof()
        self.assertEqual(self.cleanup(task, apply=True)["state"], "complete")

    @unittest.skipUnless(shutil.which("lsof"), "real lsof unavailable")
    def test_real_lsof_sees_a_detached_process_in_the_checkout(self):
        task, sha = self.merged_task()
        child = subprocess.Popen(["sleep", "30"], cwd=task["worktree"], start_new_session=True)
        self.addCleanup(child.kill)
        with mock.patch.dict(os.environ, {"SUM_LSOF_BIN": ""}):
            inside, error = sumctl.processes_in(task["worktree"])
            self.assertIsNone(error)
            self.assertIn(child.pid, [p["pid"] for p in inside])
            child.kill(); child.wait()
            inside, error = sumctl.processes_in(task["worktree"])
            self.assertEqual((inside, error), ([], None))

    def test_never_kills_processes_or_forces(self):
        task, sha = self.merged_task()
        self.set_pane(task["pane"], agent="codex", agent_status="working")
        self.refused(task, "still hosts agent")
        self.assertFalse(any(c[:2] in (["agent", "send-keys"], ["pane", "send-keys"], ["pane", "close"], ["workspace", "close"]) for c in self.calls()))

    # --- identity ------------------------------------------------------------------------------------------------------

    def test_reused_or_moved_pane_blocks(self):
        task, sha = self.merged_task()
        self.set_pane(task["pane"], cwd=str(self.repo))
        saved = self.refused(task, "refusing a possibly reused pane")
        self.set_pane(task["pane"], cwd=task["worktree"], workspace_id="w-elsewhere")
        saved = self.refused(task, "now sits in workspace w-elsewhere")

    def test_extra_service_pane_in_task_workspace_blocks(self):
        task, sha = self.merged_task()
        self.set_pane(task["workspace"] + ":p2", cwd=task["worktree"], agent=None)
        saved = self.refused(task, "unknown pane .*:p2 .* blocks removal")
        self.assertEqual([b["code"] for b in saved["cleanup"]["blockers"]], ["panes"])

    def test_coordinator_workspace_and_labels_are_never_targets(self):
        task, sha = self.merged_task()
        with self.store.lock():
            saved = self.store.read(task["id"])
            saved["workspace"] = "w-parent"
            self.store.save(saved)
        self.refused(self.store.read(task["id"]), "coordinator's own workspace")
        self.assertFalse(any(c[:2] == ["workspace", "get"] for c in self.calls()))  # Identity failed before any resource lookup.
        with self.store.lock():
            saved = self.store.read(task["id"])
            saved["workspace"], saved["pane"] = task["workspace"], "w-parent:p1"
            self.store.save(saved)
        self.refused(self.store.read(task["id"]), "calling coordinator pane")

    def test_checkout_identity_mismatch_blocks(self):
        task, sha = self.merged_task()
        state = self.fake_state()
        state["workspaces"][task["workspace"]]["worktree"]["checkout_path"] = str(self.repo)
        self.write_fake_state(state)
        self.refused(task, "is not the task checkout")
        state["workspaces"][task["workspace"]]["worktree"]["checkout_path"] = task["worktree"]
        self.write_fake_state(state)
        self.git("checkout", "-q", "-b", "elsewhere", cwd=task["worktree"])
        self.refused(task, "not the task branch")

    def test_developer_pane_cannot_clean_up(self):
        task, sha = self.merged_task()
        with self.pane("w-second:p1"):
            self.init()
            with self.assertRaisesRegex(sumctl.SumError, "not the registered coordinator"):
                self.cleanup(task, apply=True)
        self.assertTrue(Path(task["worktree"]).is_dir())

    # --- obligations -----------------------------------------------------------------------------------------------------

    def test_open_question_and_missing_handoff_block(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        sumctl.report(self.store, argparse.Namespace(task=task["id"], text="prose only", file=None, handoff=None))
        self.merged(task, sha)
        self.reconcile(task)
        q = self.question(task)
        saved = self.refused(task, "questions not yet answered")
        self.assertEqual(sorted(b["code"] for b in saved["cleanup"]["blockers"]), ["handoff", "obligations"])
        sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q["question"]["id"], text="yes", file=None))
        sumctl.resolve(self.store, argparse.Namespace(task=task["id"], question=q["question"]["id"]))
        self.refused(task, "no structured worker handoff")
        self.report(task, sha)
        self.assertEqual(self.cleanup(task, apply=True)["state"], "complete")

    # --- interruption ------------------------------------------------------------------------------------------------------

    def test_crash_after_native_removal_reconciles_from_records_and_observation(self):
        task, sha = self.merged_task()
        with mock.patch.dict(os.environ, {"FAKE_REMOVE_CRASH": "1"}):
            with self.assertRaisesRegex(sumctl.SumError, "exited 137"):
                self.cleanup(task, apply=True)
        saved = self.store.read(task["id"])
        self.assertEqual((saved["status"], saved["cleanup"]["state"]), ("reported", "removing"))
        self.assertEqual(saved["cleanup"]["intent"]["merged_head"], sha)
        self.assertFalse(Path(task["worktree"]).exists())  # Herdr did remove before the crash.
        self.assertEqual(self.git("rev-parse", task["branch"]), sha)
        rows = {r["id"]: r for r in sumctl.status(self.store, live=True, inbox=True)["tasks"]}  # The next bounded rundown reconciles.
        self.assertEqual(rows[task["id"]]["cleanup_reconciled"]["state"], "complete")
        saved = self.store.read(task["id"])
        self.assertEqual((saved["status"], saved["cleanup"]["state"], saved["cleanup"]["step"]), ("archived", "complete", "reconciled-after-interruption"))
        self.assertEqual(self.cleanup(task)["already"], True)

    def test_interrupted_before_removal_reverts_to_pending(self):
        task, sha = self.merged_task()
        with self.store.lock():
            saved = self.store.read(task["id"])
            saved["cleanup"] = {"schema": 1, "state": "removing", "history": [], "at": sumctl.now()}
            self.store.save(saved)
        result = self.cleanup(task, apply=True)  # Reconcile finds everything present, then proceeds through the full guarded path.
        self.assertEqual(result["state"], "complete")

    def test_already_absent_resources_are_accepted_only_after_inspection(self):
        task, sha = self.merged_task()
        self.git("worktree", "remove", task["worktree"])  # The boss removed it by hand; the branch stays.
        state = self.fake_state()
        del state["workspaces"][task["workspace"]]; del state["panes"][task["pane"]]
        self.write_fake_state(state)
        result = self.cleanup(task, apply=True)
        self.assertEqual((result["state"], result["removed"]["performed"]), ("complete", False))
        self.assertEqual(self.git("rev-parse", task["branch"]), sha)
        self.assertFalse(Path(task["worktree"]).exists())  # Nothing recreated.

    def test_checkout_without_workspace_is_not_removed_by_git(self):
        task, sha = self.merged_task()
        state = self.fake_state()
        del state["workspaces"][task["workspace"]]; del state["panes"][task["pane"]]
        self.write_fake_state(state)
        self.assertRaises(sumctl.SumError, lambda: self.cleanup(task, apply=True))
        self.assertTrue(Path(task["worktree"]).is_dir())
        self.assertEqual(self.store.read(task["id"])["cleanup"]["blockers"][0]["code"], "workspace")

    # --- reviewer pane ------------------------------------------------------------------------------------------------------

    def test_reviewer_pane_closes_early_only_with_saved_findings_and_never_the_checkout(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        self.report(task, sha)
        self.set_pane("w-review:p1", cwd=str(ROOT), agent="claude", agent_status="idle")
        with self.pane("w-review:p1"):
            sumctl.review(self.store, argparse.Namespace(task=task["id"], text="fine", file=None, verdict="approve", candidate=sha))
        task = self.store.read(task["id"])
        with self.assertRaisesRegex(sumctl.SumError, "still hosts agent"):
            self.cleanup(task, apply=True, reviewer_only=True)
        self.set_pane("w-review:p1", agent=None, agent_status="unknown")
        plan = self.cleanup(task, reviewer_only=True)
        self.assertEqual((plan["state"], plan["reviewer"]["closable"]), ("ready", True))
        self.assertIn("w-review:p1", self.fake_state()["panes"])
        result = self.cleanup(task, apply=True, reviewer_only=True)
        self.assertEqual(result["reviewer"]["closed"], True)
        self.assertNotIn("w-review:p1", self.fake_state()["panes"])
        self.assertTrue(Path(task["worktree"]).is_dir())  # The shared implementation checkout is untouched; the PR is not even open.
        self.assertIn(task["workspace"], self.fake_state()["workspaces"])
        saved = self.store.read(task["id"])
        self.assertNotEqual(saved["status"], "archived")
        self.assertIsNone(saved["cleanup"]["state"])  # Closing a reviewer pane never marks the task cleanup-pending.
        self.assertIsNone(sumctl.cleanup_pending(saved))
        self.assertEqual(self.herdr_calls("pane", "close"), [["pane", "close", "w-review:p1"]])

    def test_reviewer_without_findings_stays_open_and_blocks_task_cleanup(self):
        task, sha = self.merged_task()
        with self.store.lock():
            saved = self.store.read(task["id"])
            saved["reviewer"] = {"machine": sumctl.machine(), "session": "sum-test", "pane": "w-review:p1", "cwd": str(ROOT), "bound_at": sumctl.now()}
            self.store.save(saved)
        self.set_pane("w-review:p1", agent=None)
        with self.assertRaisesRegex(sumctl.SumError, "no saved findings"):
            self.cleanup(self.store.read(task["id"]), apply=True, reviewer_only=True)
        self.refused(self.store.read(task["id"]), "no saved findings")
        self.assertIn("w-review:p1", self.fake_state()["panes"])

    def test_full_cleanup_closes_reviewer_pane_in_another_workspace_after_the_checkout(self):
        task, sha = self.merged_task()
        self.set_pane("w-review:p1", agent=None)
        with self.pane("w-review:p1"):
            sumctl.review(self.store, argparse.Namespace(task=task["id"], text="ok", file=None, verdict="approve", candidate=sha))
        result = self.cleanup(self.store.read(task["id"]), apply=True)
        self.assertEqual((result["state"], result["reviewer"]["closed"]), ("complete", True))
        self.assertNotIn("w-review:p1", self.fake_state()["panes"])

    # --- visibility ------------------------------------------------------------------------------------------------------------

    def test_merged_tasks_are_visibly_cleanup_pending_and_stay_functional(self):
        task, sha = self.merged_task()
        rows = {r["id"]: r for r in sumctl.status(self.store)["tasks"]}
        self.assertEqual(rows[task["id"]]["cleanup"]["state"], "pending")
        inbox = {r["id"]: r for r in sumctl.status(self.store, inbox=True)["tasks"]}
        self.assertIn(task["id"], inbox)
        self.assertEqual(self.init()["cleanup_pending"], [{"task": task["id"], "state": "pending", "at": mock.ANY, "blockers": [], "note": f"PR merged on record; run cleanup {task['id']}"}])
        self.set_pane(task["pane"], agent="codex", agent_status="idle")
        self.refused(task, "still hosts agent")
        rows = {r["id"]: r for r in sumctl.status(self.store)["tasks"]}
        self.assertEqual((rows[task["id"]]["cleanup"]["state"], set(rows[task["id"]]["cleanup"]["blockers"])), ("blocked", {"occupant"}))
        q = self.question(task)  # The blocked task keeps working: questions, answers, and reports still flow.
        self.assertEqual(self.store.read(task["id"])["status"], "waiting")
        self.assertTrue(sumctl.holds_slot(self.store.read(task["id"])))
        result = self.cli("cleanup", task["id"])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["state"], "blocked")

    def test_archive_acknowledge_stays_records_only(self):
        task, sha = self.merged_task()
        attempt = task["execution"]["worker"]["id"]
        parked = self.cli("execution", "park", task["id"], "--attempt", attempt)
        self.assertEqual(parked.returncode, 0, parked.stderr)
        self.assertEqual(sumctl.main(["--home", str(self.store.home), "archive", task["id"], "--acknowledge"]), 0)
        saved = self.store.read(task["id"])
        self.assertEqual(saved["status"], "archived")
        self.assertTrue(Path(task["worktree"]).is_dir())
        self.assertIn(task["workspace"], self.fake_state()["workspaces"])
        self.assertIsNone(sumctl.cleanup_pending(saved))

    def test_other_workers_keep_running_through_a_cleanup(self):
        with mock.patch.object(sumctl, "DEFAULT_CAPACITY", {"global": 4, "per_repository": 1}):
            first, sha = self.merged_task()
            other_repo = self.root / "other"
            shutil.copytree(self.repo, other_repo, ignore=shutil.ignore_patterns("*.lock"))
            subprocess.run(["git", "-C", str(other_repo), "worktree", "prune"], check=True)
            other = self.prepare(repo=str(other_repo))
            self.set_pane(other["pane"], agent="codex", agent_status="working")
            self.assertEqual(self.cleanup(first, apply=True)["state"], "complete")
            state = self.fake_state()
            self.assertEqual(state["panes"][other["pane"]]["agent_status"], "working")
            self.assertTrue(Path(other["worktree"]).is_dir())
            self.assertTrue(sumctl.holds_slot(self.store.read(other["id"])))


if __name__ == "__main__":
    unittest.main()
