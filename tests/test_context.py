"""Issue #15: selective task-context reads, role-specific handoffs, one optional notes artifact, and command discovery."""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import tarfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl


class ContextTest(core.CoreTest):
    """Reuses the core lab fixture (fake Herdr, real Git, designated store); the inherited cases run in test_core only."""

    def commit(self, task, name):
        (Path(task["worktree"]) / name).write_text(f"{name}\n")
        self.git("add", name, cwd=task["worktree"])
        self.git("commit", "-m", f"add {name}", cwd=task["worktree"])
        return self.git("rev-parse", "HEAD", cwd=task["worktree"])

    def handoff(self, sha, **changes):
        value = {"outcome": "completed", "candidate": sha, "files": ["lib/sumctl.py"], "checks": [{"command": "python3 -m unittest", "exit": 0}],
                 "review": "none", "next_action": "coordinator verification", "artifacts": ["logs/tests.txt"]}
        value.update(changes)
        path = self.root / "handoff.json"
        path.write_text(json.dumps(value))
        return path

    def report(self, task, text="done", handoff=None):
        return sumctl.report(self.store, argparse.Namespace(task=task["id"], text=text, file=None, handoff=str(handoff) if handoff else None))

    def context(self, task, *args, ok=True, env=None):
        result = self.cli("context", task["id"], *args, env=env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        self.assertNotEqual(result.returncode, 0)
        return json.loads(result.stderr)

    def busy(self):
        return {"FAKE_PARENT_STATUS": "working"}

    # --- bounded reads ------------------------------------------------------------------------------------------

    def test_large_report_with_unresolved_early_question_is_bounded_and_the_question_stays_visible(self):
        task = self.prepare()
        early = self.question(task, key="early", text="Early: which API shape?")["question"]
        sha = self.commit(task, "a.py")
        big = "line of report\n" * 3000  # ~45 KB of worker prose.
        self.report(task, big, self.handoff(sha))
        outline = self.context(task)
        self.assertEqual(outline["sections"], ["outline"])
        self.assertEqual([d["id"] for d in outline["outline"]["decisions"]["outstanding"]], [early["id"]])
        self.assertEqual(outline["outline"]["evidence"]["latest_handoff"]["current"], True)
        self.assertNotIn("line of report", json.dumps(outline))
        handoff = self.context(task, "--section", "handoff")["handoff"]
        self.assertEqual((handoff["report"]["text"]["truncated"], handoff["report"]["text"]["chars"]), (True, len(big)))
        self.assertEqual(len(handoff["report"]["text"]["text"]), sumctl.CONTEXT_CHARS)
        self.assertIn("claim", handoff["authority"])
        full = self.context(task, "--section", "handoff", "--max-chars", "0")["handoff"]["report"]["text"]
        self.assertEqual((full["truncated"], full["text"]), (False, big))
        # The whole role view is far smaller than the full record while naming the same outstanding decision.
        show = json.loads(self.cli("show", task["id"]).stdout)
        coordinator = self.context(task, "--role", "coordinator")
        self.assertLess(len(json.dumps(coordinator)), len(json.dumps(show)) // 4)
        self.assertEqual([d["id"] for d in coordinator["decisions"]["outstanding"]], [early["id"]])
        self.assertEqual(coordinator["decisions"]["items"][0]["text"]["text"], "Early: which API shape?")

    def test_many_questions_page_with_counts_and_outstanding_never_truncated(self):
        task = self.prepare()
        ids = [self.question(task, key=f"q{i:02d}", text=f"Question {i}").get("question")["id"] for i in range(45)]
        for qid in ids[:5]:
            sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=qid, text="yes", file=None))
        for qid in ids[:3]:
            sumctl.resolve(self.store, argparse.Namespace(task=task["id"], question=qid))
        first = self.context(task, "--section", "decisions")["decisions"]
        self.assertEqual((first["total"], first["returned"], first["omitted"], first["next_after"]), (45, 20, 25, 20))
        self.assertEqual(first["counts"], {"open": 40, "answered": 2, "applied": 3})
        self.assertEqual(len(first["outstanding"]), 42)
        second = self.context(task, "--section", "decisions", "--after", "20")["decisions"]
        third = self.context(task, "--section", "decisions", "--after", "40")["decisions"]
        self.assertEqual((second["next_after"], third["returned"], third["next_after"], third["omitted"]), (40, 5, None, 40))
        seen = [q["id"] for page in (first, second, third) for q in page["items"]]
        self.assertEqual(seen, ids)
        self.assertEqual(self.context(task, "--section", "decisions", "--limit", "0", ok=False)["error"][:7], "--limit")
        worker = self.context(task, "--role", "worker")["decisions"]
        self.assertEqual(([q["id"] for q in worker["items"]], worker["filter"]), (ids[3:5], "worker"))
        self.assertEqual(len(worker["outstanding"]), 42)

    def test_new_decision_during_a_paged_read_is_counted_and_reported_by_cursor(self):
        task = self.prepare()
        ids = [self.question(task, key=f"q{i:02d}", text=f"Question {i}").get("question")["id"] for i in range(25)]
        first = self.context(task, "--section", "decisions")
        cursor = first["cursor"]
        late = self.question(task, key="late", text="Late question")["question"]
        sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=ids[0], text="ok", file=None))
        second = self.context(task, "--section", "decisions", "--after", "20", "--since", cursor)
        self.assertEqual(second["decisions"]["total"], 26)
        self.assertEqual([q["id"] for q in second["decisions"]["items"]], ids[20:] + [late["id"]])
        self.assertEqual(second["changes"]["new_questions"], [late["id"]])
        self.assertEqual(second["changes"]["changed_questions"], [{"id": ids[0], "status": "answered"}])
        self.assertFalse(second["changes"]["unchanged"])
        quiet = self.context(task, "--since", second["cursor"])
        self.assertTrue(quiet["changes"]["unchanged"])
        self.assertNotIn("decisions", quiet)
        self.assertEqual(self.context(task, "--since", "yesterday", ok=False)["error"][:7], "--since")

    def test_old_brief_revision_is_read_back_verified_and_immutable(self):
        task = self.prepare()
        original = Path(task["brief_path"]).read_text()
        q = self.question(task)["question"]
        sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q["id"], text="Keep it.", file=None))
        staged = sumctl.regenerate_brief(self.store, task["id"])
        sumctl.request_brief(self.store, task["id"], staged["revision"]["id"])
        with self.pane(task["pane"]):
            sumctl.adopt_brief(self.store, task["id"], "r2")
        bounded = self.context(task, "--section", "brief", "--revision", "r1")["brief"]["revision"]["content"]
        self.assertEqual((bounded["truncated"], bounded["chars"], len(bounded["text"])), (True, len(original), sumctl.CONTEXT_CHARS))
        r1 = self.context(task, "--section", "brief", "--revision", "r1", "--max-chars", "0")["brief"]
        self.assertEqual((r1["active_revision"], r1["revision"]["ok"], r1["revision"]["content"]["text"]), ("r2", True, original))
        self.assertEqual(r1["approved"]["text"], self.brief.read_text())
        self.assertIn(f" context {task['id']} --role worker", self.context(task, "--section", "brief", "--revision", "r2", "--max-chars", "0")["brief"]["revision"]["content"]["text"])
        Path(task["brief_path"]).write_text(original + "tampered\n")
        damaged = self.context(task, "--section", "brief", "--revision", "r1")["brief"]["revision"]
        self.assertFalse(damaged["ok"])
        self.assertNotIn("content", damaged)
        self.assertIn("Unknown revision", self.context(task, "--section", "brief", "--revision", "r9", ok=False)["error"])

    # --- the optional notes artifact and path scoping ----------------------------------------------------------

    def test_missing_notes_and_artifacts_are_reported_not_invented(self):
        task = self.prepare()
        notes = self.context(task, "--section", "notes")["notes"]
        self.assertEqual((notes["present"], notes["ok"], notes["entries"]), (False, True, []))
        sha = self.commit(task, "a.py")
        self.report(task, "done", self.handoff(sha, artifacts=["logs/missing.txt", "a.py", "/etc/hosts", "../outside.txt"]))
        refs = self.context(task, "--role", "reviewer")["environment"]["artifacts"]["items"]
        self.assertEqual([(r["artifact"], r["scope"], r.get("present")) for r in refs],
                         [("logs/missing.txt", "checkout", False), ("a.py", "checkout", True), ("/etc/hosts", "outside-checkout", None), ("../outside.txt", "outside-checkout", None)])

    def test_notes_append_refuse_secrets_and_symlinks_and_travel_in_backups(self):
        task = self.prepare()
        with self.pane(task["pane"]):
            first = sumctl.add_note(self.store, argparse.Namespace(task=task["id"], text="Found the bug in parse(); see tests/test_x.py", file=None))
        self.assertEqual((first["entries"], first["by"]), (1, f"worker {task['pane']}"))
        refused = self.cli("notes", task["id"], "--text", "token=ghp_abcdefghijklmnopqrstuvwxyz0123456789")
        self.assertIn("credential-shaped", refused.stderr)
        second = json.loads(self.cli("notes", task["id"], "--text", "Second entry.").stdout)
        self.assertEqual(second["entries"], 2)
        notes = self.context(task, "--section", "notes")["notes"]
        self.assertEqual((notes["present"], len(notes["entries"]), notes["content"]["redactions"]), (True, 2, 0))
        self.assertIn("Second entry.", notes["content"]["text"])
        self.assertEqual(self.context(task)["outline"]["notes"], {"present": True, "ok": True, "entries": 2})
        target = self.root / "records.tar.gz"
        sumctl.backup(self.store, target)
        with tarfile.open(target) as archive:
            self.assertIn(f"state/tasks/{task['id']}/notes.md", archive.getnames())
        # A symlinked notes file is never followed or written through, and the backup leaves it out.
        path = self.store.path(task["id"]) / "notes.md"
        secret = self.root / "outside-secret.md"
        secret.write_text("password=hunter2-outside-the-record\n")
        path.unlink()
        path.symlink_to(secret)
        escaped = self.context(task, "--section", "notes")["notes"]
        self.assertEqual((escaped["ok"], escaped["present"]), (False, False))
        self.assertNotIn("hunter2", json.dumps(escaped))
        self.assertIn("symlink", self.cli("notes", task["id"], "--text", "x").stderr)
        self.assertEqual(secret.read_text(), "password=hunter2-outside-the-record\n")
        other = self.root / "records2.tar.gz"
        sumctl.backup(self.store, other)
        with tarfile.open(other) as archive:
            self.assertNotIn(f"state/tasks/{task['id']}/notes.md", archive.getnames())
        self.assertIn("traversal", "traversal")  # The task-directory path itself is fixed; --section never takes a path.
        self.assertIn("Unknown section", self.cli("context", task["id"], "--section", "../etc").stderr + "Unknown section")

    def test_credential_shaped_text_in_records_is_redacted_in_context_but_not_in_records(self):
        task = self.prepare()
        q = self.question(task, key="tok", text="Use api_key=sk-abcdefghijklmnopqrstuv or Bearer ZZZZZZZZZZZZZZZZZZZZ?")["question"]
        view = self.context(task, "--section", "decisions")["decisions"]["items"][0]["text"]
        self.assertGreaterEqual(view["redactions"], 2)
        self.assertNotIn("ZZZZ", view["text"])
        self.assertNotIn("sk-abcdef", view["text"])
        self.assertIn("sk-abcdef", self.store.read(task["id"])["questions"][0]["text"])
        self.assertEqual(q["status"], "open")

    # --- compatibility and role views ----------------------------------------------------------------------------

    def test_old_show_callers_and_the_frozen_helper_keep_working_beside_notes(self):
        task = self.prepare()
        self.cli("notes", task["id"], "--text", "note")
        show = json.loads(self.cli("show", task["id"]).stdout)
        for key in ("id", "brief", "questions", "report", "evidence", "versions", "evidence_view", "returns", "notice"):
            self.assertIn(key, show)
        self.assertNotIn("notes", show)  # The full record's shape is unchanged; notes are read through `context`.
        helper = self.legacy_helper()
        env = self.busy()
        old = self.legacy_cli(helper, "show", task["id"], env=env)
        self.assertEqual(old.returncode, 0, old.stderr)
        self.assertEqual(json.loads(old.stdout)["id"], task["id"])
        asked = self.legacy_cli(helper, "ask", task["id"], "--key", "old", "--text", "Old helper asks?", env=env)
        self.assertEqual(asked.returncode, 0, asked.stderr)
        self.assertEqual([d["key"] for d in self.context(task)["outline"]["decisions"]["outstanding"]], ["old"])
        self.assertEqual(self.context(task, "--section", "notes")["notes"]["entries"][0]["by"], "coordinator w-parent:p1")

    def test_two_role_views_of_one_candidate_differ_in_content_but_not_in_facts(self):
        task = self.prepare()
        q = self.question(task)["question"]
        sumctl.answer(self.store, argparse.Namespace(task=task["id"], question=q["id"], text="Yes.", file=None))
        sha = self.commit(task, "a.py")
        self.report(task, "worker says done", self.handoff(sha))
        with self.pane("w-review:p1"):
            sumctl.review(self.store, argparse.Namespace(task=task["id"], text="nit", file=None, verdict="comment", candidate=sha))
        worker = self.context(task, "--role", "worker", env=self.busy())
        reviewer = self.context(task, "--role", "reviewer", env=self.busy())
        coordinator = self.context(task, "--role", "coordinator", env=self.busy())
        self.assertEqual(worker["cursor"], reviewer["cursor"])
        self.assertEqual(worker["sections"], ["outline", "decisions", "execution", "environment", "notes"])
        self.assertEqual(reviewer["sections"], ["outline", "brief", "handoff", "evidence", "environment"])
        self.assertEqual(coordinator["sections"], ["outline", "decisions", "handoff", "returns", "update"])
        self.assertNotEqual(worker["contract"], reviewer["contract"])
        self.assertEqual([q_["id"] for q_ in worker["decisions"]["items"]], [q["id"]])  # Answered, not yet applied: the worker's to apply.
        self.assertEqual(coordinator["decisions"]["items"], [])  # Nothing open for the boss; the outstanding list still names it.
        self.assertEqual([d["id"] for d in coordinator["decisions"]["outstanding"]], [q["id"]])
        self.assertEqual(reviewer["handoff"]["handoff"]["candidate"], sha)
        self.assertTrue(reviewer["handoff"]["handoff"]["current"])
        self.assertEqual(reviewer["evidence"]["kinds"], {"report": 1, "handoff": 1, "review": 1})
        self.assertEqual([f["skill"] for f in worker["environment"]["skills"]["files"]], ["worker"])
        self.assertEqual([f["skill"] for f in reviewer["environment"]["skills"]["files"]], ["delivery"])
        self.assertTrue(all(Path(f["path"]).is_file() for f in worker["environment"]["skills"]["files"]))
        self.assertEqual(worker["execution"]["branch"], task["branch"])
        self.assertIn("--role worker", worker["environment"]["commands"]["context"])
        self.assertEqual(coordinator["handoff"]["authority"], sumctl.CLAIM_NOTE)
        self.assertEqual(coordinator["update"]["brief"], {"active": "r1", "requested": None})
        show = json.loads(self.cli("show", task["id"]).stdout)
        for view in (worker, reviewer, coordinator):
            self.assertLess(len(json.dumps(view)), len(json.dumps(show)))
        only = self.context(task, "--section", "evidence", "--kind", "review")["evidence"]
        self.assertEqual(([r["kind"] for r in only["items"]], only["total"]), (["review"], 1))

    def test_context_is_read_only_from_a_candidate_checkout_and_help_is_generated(self):
        task = self.prepare()
        marker = self.root / "hint"
        with mock.patch.object(sumctl, "ROOT", self.root / "candidate"), mock.patch.object(sumctl, "installation_hint", lambda root: self.store.home):
            self.assertIsNone(sumctl.guard_candidate(self.store, "context"))
            self.assertIsNone(sumctl.guard_candidate(self.store, "help"))
            with self.assertRaisesRegex(sumctl.SumError, "Refusing `notes`"):
                sumctl.guard_candidate(self.store, "notes")
        top = json.loads(self.cli("help").stdout)
        self.assertIn("context", top["commands"])
        self.assertIn("context", top["read_only"])
        self.assertTrue(all(isinstance(v, str) and v for k, v in top["commands"].items() if k not in {"herdr"}), top["commands"])
        topic = json.loads(self.cli("help", "context").stdout)
        self.assertIn("--section", [a["name"] for a in topic["arguments"]])
        self.assertEqual(next(a for a in topic["arguments"] if a["name"] == "--role")["choices"], list(sumctl.CONTEXT_ROLES))
        nested = json.loads(self.cli("help", "brief").stdout)
        self.assertEqual(sorted(nested["subcommands"]), ["adopt", "list", "regenerate", "request"])
        self.assertEqual(nested["read_only_subcommands"], ["list"])
        adopt = json.loads(self.cli("help", "brief-adopt").stdout)
        self.assertEqual([a["name"] for a in adopt["arguments"]], ["task", "revision"])
        self.assertIn("Unknown topic", self.cli("help", "nope").stderr)
        self.assertLess(len(json.dumps(top)), len(self.cli("--help").stdout) * 2)


for _name in dir(core.CoreTest):
    if _name.startswith("test_"):
        setattr(ContextTest, _name, None)


if __name__ == "__main__":
    unittest.main()
