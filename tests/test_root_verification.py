"""Issue #33: worker verification run, then a separate coordinator (root) run under its own run id, then the existing independent
review, before a task is ready. Exercised against a deterministic standardized fixture (VERIFY.md + fake mise + vendored verify skill)
inside the core lab: fake Herdr, real Git, a designated store. Nothing here touches a live installation or the user's Herdr session."""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl
RUNNER = ".agents/skills/verify/scripts/verify_run.py"


class RootVerificationTest(core.CoreTest):
    """The repository under test is the `cli` verification fixture, so every task checkout is `standardized` at its base commit."""
    def setUp(self):
        super().setUp()
        for item in (ROOT / "tests/fixtures/verify/cli").iterdir():
            (shutil.copytree if item.is_dir() else shutil.copy2)(item, self.repo / item.name)
        shutil.copytree(ROOT / ".agents/skills/verify", self.repo / ".agents/skills/verify")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "standardized fixture")
        self.base = self.git("rev-parse", "HEAD")
        # The portable runner finds mise on PATH (it has no SUM_* override); the fake resolves tasks below the lab root only.
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "mise").symlink_to(ROOT / "tests/fixtures/mise.py")
        self.gh_root = self.root / "fake-gh"
        self.lsof_root = self.root / "fake-lsof"
        env = {"PATH": os.pathsep.join([str(self.bin), str(Path(sys.executable).resolve().parent), "/usr/bin", "/bin"]),
               "SUM_GH_BIN": str(ROOT / "tests/fixtures/gh.py"), "FAKE_GH_ROOT": str(self.gh_root),
               "SUM_LSOF_BIN": str(ROOT / "tests/fixtures/lsof.py"), "FAKE_LSOF_ROOT": str(self.lsof_root)}
        patch = mock.patch.dict(os.environ, env)
        patch.start()
        self.addCleanup(patch.stop)

    # --- helpers -------------------------------------------------------------------------------------------

    def commit(self, task, name, text=None):
        (Path(task["worktree"]) / name).write_text(text if text is not None else f"{name}\n")
        self.git("add", name, cwd=task["worktree"])
        self.git("commit", "-q", "-m", f"change {name}", cwd=task["worktree"])
        return self.git("rev-parse", "HEAD", cwd=task["worktree"])

    def worker_run(self, task, *args):
        """What the brief tells the worker to do: run the project's own runner from its checkout and read run.json."""
        result = subprocess.run([sys.executable, str(Path(task["worktree"]) / RUNNER), "--json", "--base", task["base_sha"], *args],
                                cwd=task["worktree"], text=True, capture_output=True, timeout=240)
        record = json.loads(result.stdout)
        record["_path"] = str(Path(task["worktree"]) / record["artifacts"]["run_dir"] / "run.json")
        return record

    def claim(self, record, **changes):
        value = {"run_id": record["run_id"], "outcome": record["outcome"], "record": record["_path"], "candidate": record["candidate"]["sha"],
                 "certifies": record["certifies"], "requires_root_review": record["requires_root_review"], "contract_sha256": record["contract"]["sha256"],
                 "policy_changed": record["policy"]["changed"]}
        value.update(changes)
        return value

    def report(self, task, sha, verification=None, text="worker done", **changes):
        handoff = {"outcome": "completed", "candidate": sha, "files": ["hello.py"], "checks": [{"command": "mise run verify", "exit": 0}],
                   "review": "none", "next_action": "coordinator verification, review, PR"}
        if verification is not None:
            handoff["verification"] = verification
        handoff.update(changes)
        path = self.root / "handoff.json"
        path.write_text(json.dumps(handoff))
        return sumctl.report(self.store, argparse.Namespace(task=task["id"], text=text, file=None, handoff=str(path)))

    def verify(self, task, sha, **changes):
        args = dict(task=task["id"], candidate=sha, result=None, run=None, execute=False, base=None, text=None, file=None)
        args.update(changes)
        return sumctl.verify(self.store, argparse.Namespace(**args))

    def review(self, task, sha, pane="w-review:p1", verdict="approve", **changes):
        args = dict(task=task["id"], text="reviewed the diff against the approved task", file=None, verdict=verdict, candidate=sha, tool=None, policy_reviewed=False)
        args.update(changes)
        if pane is None:
            return sumctl.review(self.store, argparse.Namespace(**args))
        with self.pane(pane):
            return sumctl.review(self.store, argparse.Namespace(**args))

    def pr(self, task, sha, number=7):
        self.gh_root.mkdir(exist_ok=True)
        (self.gh_root / "pr.json").write_text(json.dumps(dict(number=number, head_branch=task["branch"], head_sha=sha)))
        return sumctl.pr_reconcile(self.store, argparse.Namespace(task=task["id"], number=number, repo=None, replace=False))

    def view(self, task):
        return sumctl.evidence_view(self.store.read(task["id"]))

    def artifacts(self, task):
        root = Path(task["worktree"]) / ".artifacts"
        return sorted((str(p.relative_to(root)), p.stat().st_mtime_ns) for p in root.rglob("*") if p.is_file()) if root.exists() else []

    # --- dispatch records the contract ------------------------------------------------------------------------

    def test_dispatch_records_the_contract_and_the_brief_names_both_runs(self):
        task = self.prepare()
        policy = self.store.read(task["id"])["verification_policy"]
        self.assertEqual((policy["status"], policy["base_sha"], policy["runner"]), ("standardized", self.base, RUNNER))
        self.assertEqual(policy["contract_sha256"], sumctl.sha256_text((self.repo / "VERIFY.md").read_text()))
        self.assertEqual(policy["feature_maps"], "docs/features/README.md")
        self.assertIn("docs/features/README.md", policy["policy_files"])
        self.assertTrue(set(sumctl.VERIFICATION_POLICY_FILES) <= set(policy["policy_files"]))
        brief = Path(task["brief_path"]).read_text()
        self.assertIn("## Verification contract", brief)
        self.assertIn(f"{RUNNER} --base {self.base} --json", brief)
        self.assertIn("own run id", brief)
        self.assertIn("must not weaken the gate", brief)
        # A repository without VERIFY.md keeps its existing path and the brief says so.
        plain = self.root / "plain"
        plain.mkdir()
        self.git("init", "-q", "-b", "main", cwd=plain)
        self.git("config", "user.email", "t@example.invalid", cwd=plain)
        self.git("config", "user.name", "t", cwd=plain)
        (plain / "README.md").write_text("plain\n")
        self.git("add", ".", cwd=plain)
        self.git("commit", "-q", "-m", "plain", cwd=plain)
        other = self.prepare(repo=str(plain))
        self.assertEqual(self.store.read(other["id"])["verification_policy"]["status"], "not-yet-standardized")
        self.assertIn("`not-yet-standardized`", Path(other["brief_path"]).read_text())
        self.assertEqual(sumctl.evidence_view(self.store.read(other["id"]))["verification"]["contract"], "not-yet-standardized")

    # --- the preserved sequence -------------------------------------------------------------------------------

    def test_worker_run_then_separate_root_run_then_review_before_readiness(self):
        task = self.prepare()
        sha = self.commit(task, "NOTES.md")
        worker = self.worker_run(task)
        self.assertEqual((worker["outcome"], worker["certifies"]), ("pass", sha))
        self.report(task, sha, self.claim(worker))
        saved = self.store.read(task["id"])
        self.assertEqual([(r["kind"], r["source"]) for r in saved["evidence"]], [("report", "worker"), ("handoff", "worker"), ("verification", "worker")])
        self.assertEqual(saved["evidence"][2]["run_id"], worker["run_id"])
        view = sumctl.evidence_view(saved)
        self.assertEqual(view["verification"]["worker_run"]["run_id"], worker["run_id"])
        self.assertIsNone(view["verification"]["root_run"])
        self.assertIn("coordinator verification of the current candidate", view["closure"]["missing"])
        self.assertNotIn("worker verification run of the current candidate (handoff.verification from the project's verify runner)", view["closure"]["missing"])
        # The worker's report stays an open return until the coordinator's own verification exists; the worker's run closes nothing.
        self.assertEqual([o["kind"] for o in sumctl.open_obligations(self.store, saved)], ["report"])
        before = self.artifacts(task)
        # Root: a fresh execution in a separate detached checkout of the exact candidate, under its own run id.
        result = self.verify(task, sha, execute=True)
        record = result["evidence"]
        self.assertEqual((record["source"], record["result"], record["outcome"], record["isolation"]), ("coordinator", "pass", "pass", "separate-checkout"))
        self.assertNotEqual(record["run_id"], worker["run_id"])
        self.assertEqual(record["certifies"], sha)
        self.assertFalse(record["requires_root_review"])
        self.assertNotEqual(Path(record["root"]).resolve(), Path(task["worktree"]).resolve())
        self.assertFalse(Path(record["root"]).exists())  # The verification checkout is removed after its record was kept.
        kept = Path(record["record"])
        self.assertTrue(kept.is_file() and kept.parent.parent == self.store.path(task["id"]) / "verification")
        self.assertTrue((kept.parent / "verify.log").is_file())
        self.assertEqual(json.loads(kept.read_text())["run_id"], record["run_id"])
        self.assertEqual(self.artifacts(task), before)  # The worker's checkout and its artifacts were neither read as the result nor written.
        self.assertEqual(self.git("worktree", "list", "--porcelain").count("worktree "), 2)  # main + task; no leftover verification checkout.
        saved = self.store.read(task["id"])
        self.assertEqual(sumctl.open_obligations(self.store, saved), [])
        view = sumctl.evidence_view(saved)
        self.assertTrue(view["verification"]["distinct_run_ids"])
        self.assertEqual(view["verification"]["review"]["status"], "not-performed")
        self.assertIn("independent review findings for the current candidate (reviewer pane or the configured MADE/No Mistakes record); until then the result is not reviewed", view["closure"]["missing"])
        self.review(task, sha)
        self.pr(task, sha)
        view = self.view(task)
        self.assertEqual(view["closure"]["missing"], [])
        self.assertTrue(view["closure"]["prerequisites_met"])
        self.assertEqual(view["verification"]["review"], {"status": "performed", "current": True, "verdict": "approve", "tool": None, "policy_reviewed": False, "reviewer_pane": "w-review:p1"})
        kinds = [(r["kind"], r["source"]) for r in self.store.read(task["id"])["evidence"]]
        self.assertEqual(kinds, [("report", "worker"), ("handoff", "worker"), ("verification", "worker"), ("verification", "coordinator"), ("review", "reviewer"), ("publication", "github")])

    def test_worker_pass_claim_with_root_fail_parks_the_task_with_evidence(self):
        task = self.prepare()
        sha = self.commit(task, "hello.py", (self.repo / "hello.py").read_text().replace('f"Hello, {name}!"', 'f"Hi, {name}!"'))
        worker = self.worker_run(task)
        self.assertEqual(worker["outcome"], "fail")
        self.report(task, sha, self.claim(worker, outcome="pass", certifies=sha))  # The worker claims green anyway; the claim is data.
        result = self.verify(task, sha, execute=True)
        self.assertEqual((result["evidence"]["result"], result["evidence"]["outcome"], result["evidence"]["certifies"]), ("fail", "fail", None))
        self.assertIn("Hi, Ada!", (Path(result["evidence"]["record"]).parent / "verify.log").read_text())
        saved = self.store.read(task["id"])
        self.assertEqual(saved["status"], "reported")  # Parked with evidence, not archived, not reset; other tasks are untouched.
        view = sumctl.evidence_view(saved)
        self.assertFalse(view["closure"]["prerequisites_met"])
        self.assertTrue(any("passed (latest root run" in m and "was fail" in m and "parked" in m for m in view["closure"]["missing"]))
        self.assertEqual(view["verification"]["root_run"]["result"], "fail")
        with self.assertRaises(sumctl.SumError):
            self.verify(task, sha, run=Path(result["evidence"]["record"]), result="pass")  # A fail record cannot be relabelled pass.

    def test_prose_only_root_record_shows_no_fresh_execution_on_a_standardized_task(self):
        task = self.prepare()
        sha = self.commit(task, "NOTES.md")
        self.report(task, sha, self.claim(self.worker_run(task)))
        self.verify(task, sha, result="pass", text="looked at the worker log")  # The legacy public command still records.
        view = self.view(task)
        self.assertEqual(view["verification"]["root_run"]["run_id"], None)
        self.assertIn("coordinator verification run record (`verify --run run.json` or `verify --execute`); a prose-only record shows no fresh execution of the contract", view["closure"]["missing"])
        self.assertNotIn("coordinator verification of the current candidate", view["closure"]["missing"])
        with self.assertRaises(sumctl.SumError):
            self.verify(task, sha, result="pass")  # No text and no run: refused.

    def test_reused_worker_run_id_is_refused_for_root_and_for_a_second_worker_report(self):
        task = self.prepare()
        sha = self.commit(task, "NOTES.md")
        worker = self.worker_run(task)
        self.report(task, sha, self.claim(worker))
        with self.assertRaisesRegex(sumctl.SumError, "already recorded on this task by the worker"):
            self.verify(task, sha, run=worker["_path"])
        with self.assertRaisesRegex(sumctl.SumError, "already recorded"):
            self.report(task, sha, self.claim(worker), text="again")
        saved = self.store.read(task["id"])
        self.assertEqual(len([r for r in saved["evidence"] if r["kind"] == "verification"]), 1)
        self.assertEqual(len([r for r in saved["evidence"] if r["kind"] == "report"]), 1)  # The refused report saved nothing.
        # A run the coordinator executed itself is attached by path and gets its own record.
        root = subprocess.run([sys.executable, str(Path(task["worktree"]) / RUNNER), "--json", "--base", task["base_sha"]], cwd=task["worktree"], text=True, capture_output=True)
        record = json.loads(root.stdout)
        result = self.verify(task, sha, run=str(Path(task["worktree"]) / record["artifacts"]["run_dir"] / "run.json"))
        self.assertEqual((result["evidence"]["run_id"], result["evidence"]["isolation"], result["evidence"]["result"]), (record["run_id"], "task-checkout", "pass"))
        self.assertTrue(self.view(task)["verification"]["distinct_run_ids"])
        with self.assertRaisesRegex(sumctl.SumError, "already recorded on this task by the coordinator"):
            self.verify(task, sha, run=str(Path(task["worktree"]) / record["artifacts"]["run_dir"] / "run.json"))

    def test_candidate_changed_between_stages_makes_the_earlier_run_historical(self):
        task = self.prepare()
        first = self.commit(task, "NOTES.md")
        worker = self.worker_run(task)
        self.report(task, first, self.claim(worker))
        second = self.commit(task, "MORE.md")
        with self.assertRaisesRegex(sumctl.SumError, f"verified {first}, not --candidate {second}"):
            self.verify(task, second, run=worker["_path"])
        with self.assertRaisesRegex(sumctl.SumError, "not handoff.candidate"):
            self.report(task, second, self.claim(worker))  # The worker cannot carry the old run over to the new SHA either.
        self.verify(task, second, execute=True)
        view = self.view(task)
        self.assertEqual(view["verification"]["worker_run"]["current"], False)
        self.assertIn("worker verification run of the current candidate (handoff.verification from the project's verify runner)", view["closure"]["missing"])
        self.assertIn("current structured handoff", view["closure"]["missing"])
        self.assertEqual(view["verification"]["root_run"]["current"], True)
        # The repair candidate reruns both: the worker's new run keeps the old records as history.
        repaired = self.worker_run(task)
        self.report(task, second, self.claim(repaired))
        self.review(task, second)
        self.pr(task, second)
        view = self.view(task)
        self.assertEqual(view["closure"]["missing"], [])
        self.assertEqual(len([r for r in view["records"] if r["kind"] == "verification"]), 3)

    def test_reviewer_unavailable_stays_visible_and_is_not_labelled_reviewed(self):
        task = self.prepare()
        sha = self.commit(task, "NOTES.md")
        self.report(task, sha, self.claim(self.worker_run(task)))
        self.verify(task, sha, execute=True)
        self.pr(task, sha)
        view = self.view(task)
        self.assertEqual(view["verification"]["review"]["status"], "not-performed")
        self.assertEqual(len(view["closure"]["missing"]), 1)
        self.assertIn("independent review", view["closure"]["missing"][0])
        self.review(task, sha, verdict="comment")  # Findings of any verdict are the review event; the verdict is the reviewer's, readiness stays the human's.
        self.assertEqual(self.view(task)["closure"]["missing"], [])

    def test_policy_weakened_by_the_candidate_needs_an_explicit_policy_review(self):
        task = self.prepare()
        sha = self.commit(task, "mise.toml", '[tasks]\nverify = "true"\n')  # The gate now proves nothing.
        worker = self.worker_run(task)
        self.assertEqual((worker["outcome"], worker["requires_root_review"], worker["certifies"], worker["policy"]["changed"]), ("pass", True, None, ["mise.toml"]))
        self.report(task, sha, self.claim(worker))
        record = self.verify(task, sha, execute=True)["evidence"]
        self.assertEqual((record["result"], record["requires_root_review"], record["certifies"], record["policy"]["changed"]), ("pass", True, None, ["mise.toml"]))
        self.pr(task, sha)
        self.review(task, sha)  # An ordinary approve does not cover the policy change.
        missing = self.view(task)["closure"]["missing"]
        self.assertEqual(len(missing), 1)
        self.assertIn("explicit review of the changed verification policy", missing[0])
        self.review(task, sha, policy_reviewed=True, text="read mise.toml: verify was replaced by `true`; this must not merge")
        self.assertEqual(self.view(task)["closure"]["missing"], [])  # Recorded as reviewed; whether it merges stays the human's decision.
        self.assertTrue(self.view(task)["verification"]["review"]["policy_reviewed"])
        # A contract edited after dispatch is flagged even when the runner had no base to compare (unchecked policy is unreviewed).
        clone = self.root / "clone"
        subprocess.run(["git", "clone", "-q", str(self.repo), str(clone)], check=True)
        other = self.prepare(repo=str(clone))
        sha2 = self.commit(other, "VERIFY.md", (self.repo / "VERIFY.md").read_text().replace("Small CLI fixture.", "Edited contract."))
        loose = subprocess.run([sys.executable, str(Path(other["worktree"]) / RUNNER), "--json"], cwd=other["worktree"], text=True, capture_output=True)
        rec = json.loads(loose.stdout)
        saved = self.verify(other, sha2, run=str(Path(other["worktree"]) / rec["artifacts"]["run_dir"] / "run.json"))["evidence"]
        self.assertEqual((saved["requires_root_review"], saved["contract_changed_since_dispatch"], saved["policy"]["checked"], saved["certifies"]), (True, True, False, None))

    def test_legacy_evidence_and_legacy_tasks_keep_their_path(self):
        task = self.prepare()
        sha = self.commit(task, "NOTES.md")
        saved = self.store.read(task["id"])
        del saved["verification_policy"]  # A task dispatched by an earlier sum: no contract recorded.
        self.store.save(saved)
        self.report(task, sha)  # Handoff without a run claim, as every earlier brief produced.
        self.verify(task, sha, result="pass", text="ran the documented commands in the task checkout")
        self.pr(task, sha)
        view = self.view(task)
        self.assertEqual((view["verification"]["contract"], view["verification"]["worker_run"], view["closure"]["missing"]), ("legacy", None, []))
        self.assertTrue(view["closure"]["prerequisites_met"])
        # A worker-sourced verification record can never satisfy the coordinator prerequisite, on any task.
        legacy = self.store.read(task["id"])
        legacy["evidence"] = [r for r in legacy["evidence"] if r["source"] == "worker"]
        legacy["evidence"].append({**legacy["evidence"][-1], "id": "e-worker00001", "kind": "verification", "source": "worker", "result": "pass", "candidate": sha, "run_id": "20260906T000000Z-abcd"})
        self.store.save(legacy)
        self.assertIn("coordinator verification of the current candidate", sumctl.evidence_view(legacy)["closure"]["missing"])
        self.assertEqual([o["kind"] for o in sumctl.open_obligations(self.store, legacy)], ["report"])

    def test_made_owned_review_binds_no_reviewer_pane_and_starts_no_second_loop(self):
        task = self.prepare()
        sha = self.commit(task, "NOTES.md")
        self.report(task, sha, self.claim(self.worker_run(task)), review="performed", review_ref="made run 42 (worker claim)")
        self.verify(task, sha, execute=True)
        self.pr(task, sha)
        self.assertEqual(self.view(task)["verification"]["review"]["status"], "not-performed")  # The worker's `review: performed` is a claim.
        result = self.review(task, sha, pane=None, tool="made", text="MADE run 42: approved; two nits, non-blocking")  # Recorded from the coordinator pane.
        self.assertEqual((result["evidence"]["source"], result["evidence"]["tool"]), ("coordinator", "made"))
        self.assertIsNone(result["reviewer"])
        view = self.view(task)
        self.assertEqual(view["closure"]["missing"], [])
        self.assertEqual(view["verification"]["review"], {"status": "performed", "current": True, "verdict": "approve", "tool": "made", "policy_reviewed": False, "reviewer_pane": None})
        # The same facility recorded from another pane binds no reviewer endpoint either.
        with self.assertRaises(sumctl.SumError):
            self.review(task, sha, pane=str(task["pane"]), tool="made")  # The worker pane still cannot review its own candidate.
        self.review(task, sha, pane="w-other:p9", tool="made", verdict="comment")
        self.assertIsNone(self.store.read(task["id"])["reviewer"])

    def test_run_claims_are_validated_and_check_records_are_not_runs(self):
        task = self.prepare()
        sha = self.commit(task, "NOTES.md")
        worker = self.worker_run(task)
        good = self.claim(worker)
        for bad in ({"run_id": "x"}, {"outcome": "checked"}, {"transcript": "..."}, {"candidate": "abc"}, {"contract_sha256": "zz"}, {"certifies": sha, "outcome": "fail"}):
            with self.assertRaises(sumctl.SumError, msg=str(bad)):
                self.report(task, sha, {**good, **bad})
        for key in ("run_id", "outcome", "record"):
            with self.assertRaises(sumctl.SumError, msg=key):
                self.report(task, sha, {k: v for k, v in good.items() if k != key})
        self.assertEqual(self.store.read(task["id"])["evidence"], [])
        check = subprocess.run([sys.executable, str(Path(task["worktree"]) / RUNNER), "--json", "--check"], cwd=task["worktree"], text=True, capture_output=True)
        check_path = self.root / "check.json"
        check_path.write_text(check.stdout)
        with self.assertRaisesRegex(sumctl.SumError, "--check record"):
            self.verify(task, sha, run=str(check_path))
        with self.assertRaises(sumctl.SumError):
            self.verify(task, sha, run=str(self.root / "missing.json"))
        with self.pane("w-other:p2"):
            with self.assertRaises(sumctl.SumError):
                self.verify(task, sha, execute=True)  # Only the coordinator verifies.
        with self.assertRaises(sumctl.SumError):
            self.verify(task, "f" * 40, execute=True)  # Not a commit of the task repository.
        self.assertFalse((self.store.path(task["id"]) / "verification").exists())
        # The CLI keeps the public shape and reports a fresh-run summary.
        result = self.cli("verify", task["id"], "--candidate", sha, "--execute")
        self.assertEqual(result.returncode, 0, result.stderr)
        value = json.loads(result.stdout)
        self.assertEqual((value["evidence"]["source"], value["evidence"]["result"], value["verification"]["root_run"]["isolation"]), ("coordinator", "pass", "separate-checkout"))
        self.assertEqual(self.cli("verify", task["id"], "--candidate", sha).returncode, 1)  # Neither prose nor a run: refused.

    def test_root_execution_of_a_candidate_without_the_runner_is_refused_and_leaves_no_checkout(self):
        task = self.prepare()
        (Path(task["worktree"]) / ".agents").rename(Path(task["worktree"]) / "agents-moved")
        self.git("add", "-A", cwd=task["worktree"])
        self.git("commit", "-q", "-m", "drop runner", cwd=task["worktree"])
        sha = self.git("rev-parse", "HEAD", cwd=task["worktree"])
        with self.assertRaisesRegex(sumctl.SumError, "carries no .agents/skills/verify"):
            self.verify(task, sha, execute=True)
        self.assertEqual(self.git("worktree", "list", "--porcelain").count("worktree "), 2)
        self.assertFalse(any((self.store.path(task["id"]) / "verification").rglob("checkout")))
        self.assertEqual(self.store.read(task["id"])["evidence"], [])
        verifier = self.store.read(task["id"])["execution"]["verifiers"][-1]
        self.assertEqual((verifier["state"], sumctl.occupancy(self.store.all())["global"]), ("uncertain", 2))
        parked = sumctl.execution_park(self.store, task["id"], verifier["id"])
        self.assertEqual((parked["attempt"]["state"], sumctl.occupancy(self.store.all())["global"]), ("released", 1))

    def test_root_execution_reserves_capacity_before_creating_a_checkout(self):
        task = self.prepare()
        sha = self.commit(task, "capacity.py")
        sumctl.write_settings(self.store, {"global": 1, "per_repository": 1})
        before = self.git("worktree", "list", "--porcelain").count("worktree ")
        with self.assertRaisesRegex(sumctl.SumError, "1 of 1 global execution slots"):
            self.verify(task, sha, execute=True)
        self.assertEqual(self.git("worktree", "list", "--porcelain").count("worktree "), before)
        self.assertEqual(self.store.read(task["id"])["execution"]["verifiers"], [])


for _name in dir(core.CoreTest):
    if _name.startswith("test_"):
        setattr(RootVerificationTest, _name, None)


if __name__ == "__main__":
    unittest.main()
