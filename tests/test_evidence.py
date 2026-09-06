"""Issue #9: durable handoffs, reviewer findings, coordinator verification, and exact PR identity."""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl


class EvidenceTest(core.CoreTest):
    """Reuses the core lab fixture (fake Herdr, real Git, designated store); the inherited cases run in test_core only."""
    def setUp(self):
        super().setUp()
        self.gh_root = self.root / "fake-gh"
        self.gh_patch = mock.patch.dict(os.environ, {"SUM_GH_BIN": str(ROOT / "tests/fixtures/gh.py"), "FAKE_GH_ROOT": str(self.gh_root)})
        self.gh_patch.start()
        self.addCleanup(self.gh_patch.stop)

    def commit(self, task, name):
        (Path(task["worktree"]) / name).write_text(f"{name}\n")
        self.git("add", name, cwd=task["worktree"])
        self.git("commit", "-m", f"add {name}", cwd=task["worktree"])
        return self.git("rev-parse", "HEAD", cwd=task["worktree"])

    def handoff(self, sha, **changes):
        value = {"outcome": "completed", "task_ref": "issue #9", "candidate": sha, "files": ["lib/sumctl.py"],
                 "checks": [{"command": "python3 -m unittest", "exit": 0}], "review": "none", "decisions_unresolved": [],
                 "next_action": "coordinator verification and PR", "artifacts": ["logs/tests.txt"]}
        value.update(changes)
        path = self.root / "handoff.json"
        path.write_text(json.dumps(value))
        return path

    def report(self, task, text="done", handoff=None):
        return sumctl.report(self.store, argparse.Namespace(task=task["id"], text=text, file=None, handoff=str(handoff) if handoff else None))

    def scenario(self, **value):
        self.gh_root.mkdir(exist_ok=True)
        (self.gh_root / "pr.json").write_text(json.dumps(value))

    def reconcile(self, task, number=7, repo=None, replace=False):
        return sumctl.pr_reconcile(self.store, argparse.Namespace(task=task["id"], number=number, repo=repo, replace=replace))

    def show(self, task):
        result = self.cli("show", task["id"])
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    # --- reports and handoffs ----------------------------------------------------------------------------------

    def test_legacy_report_without_handoff_is_accepted_and_kept_as_evidence(self):
        task = self.prepare()
        result = self.cli("report", task["id"], "--text", "plain prose report")  # The exact command every existing brief carries.
        self.assertEqual(result.returncode, 0, result.stderr)
        saved = self.store.read(task["id"])
        self.assertEqual((saved["status"], saved["report"]["text"], saved["report"]["candidate"]), ("reported", "plain prose report", None))
        self.assertEqual([(r["kind"], r["source"], r["text"]) for r in saved["evidence"]], [("report", "worker", "plain prose report")])
        view = self.show(task)["evidence_view"]
        self.assertEqual(view["closure"]["prerequisites_met"], False)
        self.assertIn("current structured handoff", view["closure"]["missing"])

    def test_second_report_and_reviewer_findings_erase_nothing(self):
        task = self.prepare()
        self.question(task)
        sha = self.commit(task, "a.py")
        self.report(task, "first", self.handoff(sha))
        self.report(task, "second")
        with self.pane("w-review:p1"):
            sumctl.review(self.store, argparse.Namespace(task=task["id"], text="nit: naming", file=None, verdict="comment", candidate=sha))
        saved = self.store.read(task["id"])
        self.assertEqual(saved["report"]["text"], "second")
        self.assertEqual([r["kind"] for r in saved["evidence"]], ["report", "handoff", "report", "review"])
        self.assertEqual(saved["evidence"][0]["text"], "first")
        self.assertEqual(saved["evidence"][1]["handoff"]["candidate"], sha)
        self.assertEqual(saved["questions"][0]["status"], "open")
        self.assertEqual(saved["status"], "reported")
        self.assertEqual(saved["reviewer"]["pane"], "w-review:p1")
        self.assertEqual(saved["evidence"][3]["endpoint"]["pane"], "w-review:p1")
        self.assertTrue(all(r["brief_revision"] == "r1" for r in saved["evidence"]))

    def test_handoff_is_validated_and_bounded(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        for bad in ({"candidate": "abc"}, {"outcome": "great"}, {"transcript": "x" * 10}, {"checks": [{"command": "x"}]},
                    {"pr": {"url": "https://github.com/x/y/pull/1", "guess": True}}, {"files": ["f"] * 201}):
            with self.assertRaises(sumctl.SumError, msg=bad):
                self.report(task, handoff=self.handoff(sha, **bad))
        self.assertEqual(self.store.read(task["id"])["evidence"], [])  # A refused handoff records nothing, not even the prose.
        self.report(task, handoff=self.handoff(sha, pr={"repository": "douglasjarquin/project", "number": 7, "url": "https://github.com/douglasjarquin/project/pull/7",
                                                          "head_repository": None, "head_branch": task["branch"], "base_branch": "main", "head_sha": sha}))
        saved = self.store.read(task["id"])
        self.assertEqual(saved["report"]["candidate"], sha)
        self.assertEqual(saved["evidence"][1]["handoff"]["pr"]["head_repository"], None)
        self.assertIsNone(saved["pr"])  # A worker's PR claim never becomes the recorded identity; only `pr reconcile` does.

    def test_review_endpoint_ownership(self):
        task = self.prepare()
        sumctl.start(self.store, task["id"])
        task = self.store.read(task["id"])
        with self.pane(task["pane"]):
            with self.assertRaises(sumctl.SumError):
                sumctl.review(self.store, argparse.Namespace(task=task["id"], text="lgtm", file=None, verdict="approve", candidate=None))
        with self.pane("w-review:p1"):
            sumctl.review(self.store, argparse.Namespace(task=task["id"], text="finding one", file=None, verdict="changes-requested", candidate=None))
        with self.pane("w-review:p2"):
            with self.assertRaises(sumctl.SumError):
                sumctl.review(self.store, argparse.Namespace(task=task["id"], text="me too", file=None, verdict="approve", candidate=None))
        saved = self.store.read(task["id"])
        self.assertEqual((saved["reviewer"]["pane"], len(saved["evidence"])), ("w-review:p1", 1))
        self.assertNotIn("saved findings from the bound reviewer pane", sumctl.evidence_view(saved)["closure"]["missing"])

    def test_verify_is_coordinator_only_and_bound_to_a_candidate(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        with self.pane("w-second:p1"):
            with self.assertRaises(sumctl.SumError):
                sumctl.verify(self.store, argparse.Namespace(task=task["id"], text="ran tests", file=None, candidate=sha, result="pass"))
        with self.assertRaises(sumctl.SumError):
            sumctl.verify(self.store, argparse.Namespace(task=task["id"], text="ran tests", file=None, candidate="HEAD", result="pass"))
        sumctl.verify(self.store, argparse.Namespace(task=task["id"], text="ran tests", file=None, candidate=sha, result="pass"))
        view = sumctl.evidence_view(self.store.read(task["id"]))
        self.assertEqual([(r["kind"], r["source"], r["current"]) for r in view["records"]], [("verification", "coordinator", True)])
        self.assertNotIn("coordinator verification of the current candidate", view["closure"]["missing"])

    def test_candidate_change_invalidates_current_evidence_without_discarding_history(self):
        task = self.prepare()
        first = self.commit(task, "a.py")
        self.report(task, "first", self.handoff(first))
        sumctl.verify(self.store, argparse.Namespace(task=task["id"], text="ok", file=None, candidate=first, result="pass"))
        second = self.commit(task, "b.py")
        view = sumctl.evidence_view(self.store.read(task["id"]))
        self.assertEqual(view["current_candidate"], second)
        self.assertEqual([r["current"] for r in view["records"]], [False, False, False])
        self.assertEqual(len(view["records"]), 3)
        self.assertIn("current structured handoff", view["closure"]["missing"])
        self.assertIn("coordinator verification of the current candidate", view["closure"]["missing"])
        self.report(task, "second", self.handoff(second))
        view = sumctl.evidence_view(self.store.read(task["id"]))
        self.assertEqual([r["current"] for r in view["records"]], [False, False, False, True, True])
        self.assertEqual(self.store.read(task["id"])["status"], "reported")  # No workflow restart: the task simply carries a newer candidate.

    def test_legacy_record_without_evidence_keys_still_works(self):
        task = self.prepare()
        for key in ("evidence", "reviewer", "pr"):
            del task[key]
        task["report"] = {"text": "old report", "submitted_at": "2026-09-01T00:00:00+00:00"}
        self.store.save(task)
        shown = self.show(task)
        self.assertEqual(shown["evidence_view"]["records"][0]["legacy"], True)
        self.assertIn("complete PR identity from `pr reconcile`", shown["evidence_view"]["closure"]["missing"])
        self.report(task, "new report")
        saved = self.store.read(task["id"])
        self.assertEqual((saved["report"]["text"], len(saved["evidence"]), saved.get("pr")), ("new report", 1, None))

    # --- exact PR identity --------------------------------------------------------------------------------------

    def test_pr_reconcile_records_exact_identity_and_merged_only_for_the_task_candidate(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        self.report(task, handoff=self.handoff(sha))
        self.scenario(number=7, head_branch=task["branch"], head_sha=sha)
        result = self.reconcile(task)
        pr = result["pr"]
        self.assertEqual(pr["identity"], {"repository": "douglasjarquin/project", "number": 7, "url": "https://github.com/douglasjarquin/project/pull/7",
                                          "head_repository": "douglasjarquin/project", "head_branch": task["branch"], "base_branch": "main", "head_sha": sha})
        self.assertEqual((pr["state"], pr["findings"], pr["merged_for_task"], pr["complete"]), ("open", [], False, True))
        calls = [json.loads(line)["args"] for line in (self.gh_root / "calls.jsonl").read_text().splitlines()]
        self.assertEqual(calls[0][:2], ["repo", "view"])
        self.assertEqual(calls[1][:3], ["pr", "view", "7"])
        self.scenario(number=7, head_branch=task["branch"], head_sha=sha, state="MERGED", merged_at="2026-09-06T00:00:00Z", merge_commit="f" * 40)
        pr = self.reconcile(task)["pr"]
        self.assertTrue(pr["merged_for_task"])
        saved = self.store.read(task["id"])
        self.assertEqual([r["kind"] for r in saved["evidence"]], ["report", "handoff", "publication", "publication"])
        view = sumctl.evidence_view(saved)
        self.assertTrue(view["closure"]["merged_for_task"])
        self.assertEqual(view["closure"]["missing"], ["coordinator verification of the current candidate"])
        self.assertEqual(saved["status"], "reported")  # Merged on GitHub is recorded evidence, not an automatic archive or cleanup.

    def test_pr_mismatches_are_never_mistaken_for_the_task_result(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        self.report(task, handoff=self.handoff(sha))
        merged = dict(state="MERGED", merged_at="2026-09-06T00:00:00Z", merge_commit="e" * 40)
        cases = {
            "fork head": dict(number=7, head_branch=task["branch"], head_sha=sha, head_repository="someone/project", **merged),
            "reused branch name": dict(number=7, head_branch=task["branch"], head_sha="a" * 40, **merged),
            "changed PR head": dict(number=7, head_branch=task["branch"], head_sha="b" * 40, **merged),
            "other branch": dict(number=7, head_branch="feature/other", head_sha=sha, **merged),
            "closed unmerged": dict(number=7, head_branch=task["branch"], head_sha=sha, state="CLOSED"),
        }
        for name, scenario in cases.items():
            self.scenario(**scenario)
            pr = self.reconcile(task)["pr"]
            self.assertFalse(pr["merged_for_task"], name)
            if name != "closed unmerged":
                self.assertTrue(pr["findings"], name)
            else:
                self.assertEqual((pr["state"], pr["findings"]), ("closed", []))
        self.scenario(number=7, head_branch=task["branch"], head_sha=sha)
        with self.assertRaises(sumctl.SumError):
            self.reconcile(task, repo="someone/other")  # Wrong repository: refused before any PR lookup.
        with self.assertRaises(sumctl.SumError):
            self.reconcile(task, number=8)  # Unknown PR number: uncertain, recorded, not attached.
        with mock.patch.object(sumctl, "gh", side_effect=sumctl.SumError("gh: Command timed out after 30 seconds")):
            with self.assertRaises(sumctl.SumError):
                self.reconcile(task)
        saved = self.store.read(task["id"])
        uncertain = [r for r in saved["evidence"] if r["kind"] == "publication" and r["outcome"] == "uncertain"]
        self.assertEqual(len(uncertain), 3)
        self.assertIn("timed out", uncertain[-1]["error"])
        self.assertEqual(saved["pr"]["identity"]["number"], 7)  # The last exact observation stands; a failed lookup never overwrites it.
        self.assertFalse(saved["pr"]["merged_for_task"])

    def test_pr_number_switch_needs_replace_and_non_coordinators_are_refused(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        self.scenario(number=7, head_branch=task["branch"], head_sha=sha)
        self.reconcile(task)
        self.scenario(number=9, head_branch=task["branch"], head_sha=sha)
        with self.assertRaises(sumctl.SumError):
            self.reconcile(task, number=9)
        self.assertEqual(self.reconcile(task, number=9, replace=True)["previous"]["identity"]["number"], 7)
        with self.pane("w-second:p1"):
            with self.assertRaises(sumctl.SumError):
                self.reconcile(task, number=9)
        saved = self.store.read(task["id"])
        self.assertEqual(saved["pr"]["identity"]["number"], 9)
        self.assertEqual(len([r for r in saved["evidence"] if r["kind"] == "publication"]), 2)

    def test_pr_attached_to_a_legacy_task_by_explicit_reconciliation(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        for key in ("evidence", "reviewer", "pr"):
            del task[key]
        task["report"] = {"text": f"PR at https://github.com/douglasjarquin/project/pull/7 for {sha}", "submitted_at": "2026-09-01T00:00:00+00:00"}
        self.store.save(task)
        self.scenario(number=7, head_branch=task["branch"], head_sha=sha, state="MERGED", merged_at="2026-09-06T00:00:00Z", merge_commit="d" * 40)
        self.assertIsNone(self.store.read(task["id"]).get("pr"))  # Nothing is inferred from the prose URL.
        pr = self.reconcile(task, number=7)["pr"]
        self.assertTrue(pr["merged_for_task"])  # The checkout HEAD is the only recorded candidate of a legacy task.
        self.assertEqual(self.store.read(task["id"])["report"]["text"], task["report"]["text"])

    def test_status_rows_carry_evidence_summary(self):
        task = self.prepare()
        sha = self.commit(task, "a.py")
        self.scenario(number=7, head_branch=task["branch"], head_sha=sha)
        self.reconcile(task)
        row = sumctl.status(self.store)["tasks"][0]
        self.assertEqual(row["evidence"], {"records": 1, "pr": 7, "merged_for_task": False})


for _name in dir(core.CoreTest):
    if _name.startswith("test_"):
        setattr(EvidenceTest, _name, None)


if __name__ == "__main__":
    unittest.main()
