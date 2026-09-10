from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock

from tests.test_core import UpdateLab, fake_installer, sumctl


RUNNER_PYTHON_BIN = str(Path(sys.executable).parent)


class UpdateRecoveryRegressionTest(UpdateLab):
    def test_interrupted_recovery_is_serialized_and_never_replays_callbacks(self) -> None:
        root, store = self.installation()
        task = self.task_fixture(store)
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("after pointer replacement")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)
        journal = root / ".local" / "activation.json"
        pending_bytes = journal.read_bytes()
        pending = json.loads(pending_bytes)["pending"]
        candidate = self.current(root)
        with sumctl.activation_lock(root):
            refused = self.stable_cli(root, store, "update", "recover", "--generation", pending["generation"])
        self.assertNotEqual(refused.returncode, 0)
        self.assertIn("activation lock", refused.stderr)
        self.assertEqual(self.current(root), candidate)
        self.assertEqual(journal.read_bytes(), pending_bytes)
        asked = self.stable_cli(root, store, "ask", task["id"], "--key", "interrupted-recovery", "--text", "Still working?")
        self.assertEqual(asked.returncode, 0, asked.stderr)
        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                sumctl.recover_activation(store, root, pending["generation"])
        self.assertEqual(self.current(root), root / ".local" / "releases" / first)
        self.assertEqual(journal.read_bytes(), pending_bytes)
        reported = self.stable_cli(root, store, "report", task["id"], "--text", "Still working after recovery interruption.")
        self.assertEqual(reported.returncode, 0, reported.stderr)
        records = self.snapshot(store.home)
        recovered = self.stable_cli(root, store, "update", "recover", "--generation", pending["generation"])
        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertFalse(json.loads(recovered.stdout)["changed"])
        self.assertEqual(self.snapshot(store.home), records)
        self.assertIsNone(json.loads(journal.read_text())["pending"])
        third = self.commit_upstream(root, "three.py", "three = True\n")
        self.apply(store)
        stale = self.cli(pending["recovery"]["argv"], env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)
        self.assertNotEqual(stale.returncode, 0)
        self.assertEqual(self.current(root), root / ".local" / "releases" / third)
        self.assertEqual(self.snapshot(store.home), records)

    def test_post_selection_status_timeout_recovers_without_repeating_the_probe(self) -> None:
        root, store = self.installation()
        task = self.task_fixture(store)
        self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        previous = self.current(root)
        second = self.commit_upstream(root, "two.py", "two = True\n")
        candidate = root / ".local" / "releases" / second
        real_run = sumctl.run
        failed_probes = []

        def timeout_candidate_status(argv, **kwargs):
            if Path(argv[0]) == root / "bin" / "sumctl" and self.current(root) == candidate:
                failed_probes.append(tuple(str(value) for value in argv))
                raise sumctl.CommandTimeout("candidate status timed out")
            return real_run(argv, **kwargs)

        with mock.patch.object(sumctl, "run", side_effect=timeout_candidate_status):
            with self.assertRaises(sumctl.SumError):
                self.apply(store, no_fetch=True)
        self.assertEqual(self.current(root), previous, "a read-only post-check timeout must recover the known-good selection")
        self.assertEqual(len(failed_probes), 1)
        self.assertIsNone(json.loads((root / ".local" / "activation.json").read_text())["pending"])
        asked = self.stable_cli(root, store, "ask", task["id"], "--key", "timeout", "--text", "Question after timeout?")
        reported = self.stable_cli(root, store, "report", task["id"], "--text", "Report after timeout.")
        self.assertEqual((asked.returncode, reported.returncode), (0, 0), (asked.stderr, reported.stderr))
        self.assertEqual([q["key"] for q in store.read(task["id"])["questions"]], ["timeout"])
        self.assertEqual(store.read(task["id"])["report"]["text"], "Report after timeout.")

    def test_first_activation_refuses_an_unusable_stable_fallback(self) -> None:
        root, store = self.installation()
        (root / "bin" / "sumctl").write_text("#!/bin/sh\nexit 37\n")
        self.git("add", "bin/sumctl", cwd=root)
        self.git("commit", "-q", "-m", "broken stable entrypoint", cwd=root)
        self.git("push", "-q", "origin", "main", cwd=root)

        with self.assertRaises(sumctl.SumError):
            self.apply(store, no_fetch=True)

        self.assertIsNone(self.current(root))
        self.assertFalse((root / ".local" / "activation.json").exists(), "an unusable fallback must not be recorded as known-good")

    def test_checkout_rollback_refuses_a_mismatched_approval_tree(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        selected = self.current(root)
        approvals = root / ".local" / "approvals.json"
        receipt = json.loads(approvals.read_text())
        receipt["revisions"][first]["tree"] = "0" * 40
        sumctl.atomic_json(approvals, receipt)
        before = (root / ".local" / "activation.json").read_bytes()

        rolled = self.stable_cli(root, store, "update", "rollback", "--to", "checkout")

        self.assertNotEqual(rolled.returncode, 0, "checkout approval must match its actual Git tree")
        self.assertEqual(self.current(root), selected)
        self.assertEqual((root / ".local" / "activation.json").read_bytes(), before)

    def test_recovery_file_durability_failure_prevents_selection(self) -> None:
        root, store = self.installation()
        self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        selected = self.current(root)
        self.commit_upstream(root, "two.py", "two = True\n")
        real_fsync = os.fsync

        def fail_recovery_file_flush(fd):
            descriptor = os.fstat(fd)
            for capsule in (root / ".local" / "recovery").glob("*/sum-recover.py"):
                info = capsule.stat()
                if (info.st_dev, info.st_ino) == (descriptor.st_dev, descriptor.st_ino):
                    raise OSError("recovery durability failure")
            return real_fsync(fd)

        with mock.patch.object(sumctl.os, "fsync", side_effect=fail_recovery_file_flush):
            with self.assertRaisesRegex(OSError, "recovery durability failure"):
                self.apply(store, no_fetch=True)
        self.assertEqual(self.current(root), selected)
        self.assertIsNone(json.loads((root / ".local" / "activation.json").read_text())["pending"])

    def assert_checkout_recovery_refuses_drift(self, standalone: bool) -> None:
        root, store = self.installation()
        self.commit_upstream(root, "one.py", "one = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("after first selection")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)
        journal = root / ".local" / "activation.json"
        before = journal.read_bytes()
        pending = json.loads(before)["pending"]
        self.assertEqual(pending["from"]["kind"], "checkout")
        self.commit_upstream(root, "two.py", "two = True\n")
        sumctl.approve_update_target(store, root, None)
        selected = self.current(root)
        if standalone:
            recovered = self.cli(pending["recovery"]["argv"], env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)
        else:
            recovered = self.stable_cli(root, store, "update", "recover", "--generation", pending["generation"])
        self.assertNotEqual(recovered.returncode, 0, "recovery must restore the recorded revision, not another approved checkout")
        self.assertEqual(self.current(root), selected)
        self.assertEqual(journal.read_bytes(), before)

    def test_recovery_refuses_checkout_revision_drift(self) -> None:
        self.assert_checkout_recovery_refuses_drift(standalone=False)

    def test_independent_recovery_refuses_checkout_revision_drift(self) -> None:
        self.assert_checkout_recovery_refuses_drift(standalone=True)

    def test_pending_activation_requires_explicit_recovery_before_another_update(self) -> None:
        root, store = self.installation()
        self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("after selection")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)
        journal = root / ".local" / "activation.json"
        before = journal.read_bytes()
        selected = self.current(root)
        with self.subTest(operation="apply"):
            with mock.patch.object(sumctl, "resolve_authorized", side_effect=AssertionError("must not fetch or resolve while activation is pending")):
                with self.assertRaisesRegex(sumctl.SumError, "pending.*recover"):
                    self.apply(store)
        with self.subTest(operation="rollback"):
            with self.assertRaisesRegex(sumctl.SumError, "pending.*recover"):
                sumctl.update_rollback(store, self.ns())
        self.assertEqual(self.current(root), selected)
        self.assertEqual(journal.read_bytes(), before)

    def test_independent_recovery_refuses_an_unregistered_pane(self) -> None:
        root, store = self.installation()
        self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("after selection")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)
        journal = root / ".local" / "activation.json"
        before = journal.read_bytes()
        selected = self.current(root)
        pending = json.loads(before)["pending"]
        recovered = self.cli(pending["recovery"]["argv"],
                             env={"SUM_INSTALL_ROOT": str(root), "HERDR_PANE_ID": "unregistered-pane"}, cwd=root)
        self.assertNotEqual(recovered.returncode, 0, "independent recovery must enforce coordinator authorization")
        self.assertEqual(self.current(root), selected)
        self.assertEqual(journal.read_bytes(), before)

    def stable_cli(self, root: Path, store, *args: str) -> subprocess.CompletedProcess[str]:
        return self.cli(
            [root / "bin" / "sumctl", "--home", store.home, *args],
            env={"PATH": f"{RUNNER_PYTHON_BIN}:{os.environ['PATH']}"},
            cwd=root,
        )

    def test_update_stage_records_approval_but_plain_stage_does_not(self) -> None:
        root, store = self.installation()
        sha = self.commit_upstream(root, "one.py", "one = True\n")
        sumctl.stage(store, sha, installer=fake_installer)
        approvals = root / ".local" / "approvals.json"
        self.assertFalse(approvals.exists())

        sumctl.update_stage(store, self.ns(no_fetch=True), installer=fake_installer)

        self.assertTrue(approvals.is_file(), "authorized update stage must persist local approval")
        receipt = json.loads(approvals.read_text())["revisions"][sha]
        self.assertEqual(receipt["sha"], sha)
        self.assertEqual(receipt["tree"], self.git("rev-parse", f"{sha}^{{tree}}", cwd=root))

    def test_approved_rollback_survives_missing_remote_tracking_refs(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        self.apply(store)
        self.git("update-ref", "-d", "refs/remotes/origin/main", cwd=root)

        rollback = self.stable_cli(root, store, "update", "rollback", "--to", first)

        self.assertEqual(rollback.returncode, 0, rollback.stderr)
        self.assertEqual(self.current(root), root / ".local" / "releases" / first)

    def test_approved_release_rollback_uses_manifest_tree_when_git_object_is_unavailable(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        self.apply(store)
        real_run = sumctl.run

        def reject_historical_tree_lookup(argv, **kwargs):
            if f"{first}^{{tree}}" in [str(value) for value in argv]:
                raise sumctl.SumError("historical Git object unavailable")
            return real_run(argv, **kwargs)

        with mock.patch.object(sumctl, "run", side_effect=reject_historical_tree_lookup):
            rolled = sumctl.update_rollback(store, self.ns(to=first))

        self.assertEqual(rolled["default"]["sha"], first)
        self.assertEqual(self.current(root), root / ".local" / "releases" / first)

    def test_interrupted_selection_keeps_a_generation_for_explicit_recovery(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("interrupted after pointer replacement")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)

        journal = root / ".local" / "activation.json"
        self.assertTrue(journal.is_file(), "interrupted activation must retain durable recovery state")
        pending = json.loads(journal.read_text())["pending"]
        status = self.stable_cli(root, store, "update", "status")
        self.assertEqual(json.loads(status.stdout)["activation"]["pending"]["generation"], pending["generation"])
        recovered = self.stable_cli(root, store, "update", "recover", "--generation", pending["generation"])
        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertEqual(self.current(root), root / ".local" / "releases" / first)
        self.assertIsNone(json.loads(journal.read_text())["pending"])

    def test_recovery_capsule_uses_prior_runtime_when_selected_candidate_cannot_import(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        second = self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("interrupted after pointer replacement")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)

        journal = root / ".local" / "activation.json"
        pending = json.loads(journal.read_text())["pending"]
        candidate = root / ".local" / "releases" / second
        sumctl.set_read_only(candidate, read_only=False)
        (candidate / "lib" / "sumctl.py").write_text("this is not valid Python !!!\n")
        stable = self.stable_cli(root, store, "update", "status")
        self.assertNotEqual(stable.returncode, 0)

        recovered = self.cli(
            pending["recovery"]["argv"],
            env={"SUM_INSTALL_ROOT": str(root), "PATH": f"{RUNNER_PYTHON_BIN}:{os.environ['PATH']}"},
            cwd=root,
        )

        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertEqual(json.loads(recovered.stdout)["generation"], pending["generation"])
        self.assertEqual(self.current(root), root / ".local" / "releases" / first)
        self.assertIsNone(json.loads(journal.read_text())["pending"])

    def test_recovery_capsule_runs_with_pre_recovery_prior_helper(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        prior = root / ".local" / "releases" / first
        legacy = subprocess.run(
            ["git", "-C", str(Path(__file__).resolve().parents[1]), "show", "eaf75e4:lib/sumctl.py"],
            capture_output=True,
            text=True,
            check=True,
        ).stdout
        sumctl.set_read_only(prior, read_only=False)
        (prior / "lib" / "sumctl.py").write_text(legacy)
        manifest_path = prior / "release.json"
        manifest = json.loads(manifest_path.read_text())
        manifest["files"]["lib/sumctl.py"] = "sha256:" + sumctl.sha256_file(prior / "lib" / "sumctl.py")
        manifest_path.write_text(json.dumps(manifest))
        sumctl.set_read_only(prior)
        second = self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("interrupted after pointer replacement")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)

        journal = root / ".local" / "activation.json"
        pending = json.loads(journal.read_text())["pending"]
        candidate = root / ".local" / "releases" / second
        sumctl.set_read_only(candidate, read_only=False)
        (candidate / "lib" / "sumctl.py").write_text("this is not valid Python !!!\n")

        recovered = self.cli(pending["recovery"]["argv"], env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)

        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertEqual(self.current(root), prior)
        self.assertIsNone(json.loads(journal.read_text())["pending"])

    def test_recovery_before_pointer_replacement_clears_pending_without_mutation(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        previous = self.current(root)
        self.commit_upstream(root, "two.py", "two = True\n")

        with mock.patch.object(sumctl, "select_default", side_effect=KeyboardInterrupt("before pointer replacement")):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)

        journal = root / ".local" / "activation.json"
        pending = json.loads(journal.read_text())["pending"]
        recovered = self.cli(pending["recovery"]["argv"], env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)

        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertFalse(json.loads(recovered.stdout)["changed"])
        self.assertEqual(self.current(root), previous)
        self.assertIsNone(json.loads(journal.read_text())["pending"])

    def test_recovery_capsule_refuses_tampered_prior_helper_before_import(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("interrupted after pointer replacement")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)

        pending = json.loads((root / ".local" / "activation.json").read_text())["pending"]
        marker = self.root / "tampered-helper-executed"
        prior = root / ".local" / "releases" / first
        sumctl.set_read_only(prior, read_only=False)
        (prior / "lib" / "sumctl.py").write_text(f"from pathlib import Path\nPath({str(marker)!r}).write_text('executed')\n")

        recovered = self.cli(pending["recovery"]["argv"], env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)

        self.assertNotEqual(recovered.returncode, 0)
        self.assertIn("helper hash", recovered.stderr)
        self.assertFalse(marker.exists())
        self.assertIsNotNone(self.current(root))

    def test_stale_recovery_generation_refuses_without_pointer_mutation(self) -> None:
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        select = sumctl.select_default

        def interrupt_after_selection(installation, target):
            select(installation, target)
            raise KeyboardInterrupt("interrupted after pointer replacement")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt_after_selection):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)

        selected = self.current(root)
        recovered = self.stable_cli(root, store, "update", "recover", "--generation", "stale-generation")

        self.assertNotEqual(recovered.returncode, 0)
        self.assertIn("stale", recovered.stderr)
        self.assertEqual(self.current(root), selected)
        self.assertNotEqual(self.current(root), root / ".local" / "releases" / first)

    def test_candidate_post_check_failure_restores_prior_pointer_and_callbacks(self) -> None:
        # Given: a known-good release and callbacks recorded by the stable entrypoint.
        root, store = self.installation()
        task = self.task_fixture(store)
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        previous = self.current(root)
        self.assertEqual(previous, root / ".local" / "releases" / first)
        real_post_check = sumctl.post_check
        self.assertTrue(real_post_check(store, root)["ok"])
        second = self.commit_upstream(root, "two.py", "two = True\n")
        candidate = root / ".local" / "releases" / second
        post_check_selections: list[Path | None] = []

        def fail_only_selected_candidate(candidate_store, candidate_root):
            selected = self.current(candidate_root)
            post_check_selections.append(selected)
            if selected == candidate:
                return {"ok": False, "detail": "injected candidate-only post-check failure"}
            return real_post_check(candidate_store, candidate_root)

        # When: only the newly selected candidate fails its post-check.
        with mock.patch.object(sumctl, "post_check", side_effect=fail_only_selected_candidate):
            with self.assertRaisesRegex(sumctl.SumError, "entrypoint check failed"):
                self.apply(store, no_fetch=True)

        question = self.stable_cli(root, store, "ask", task["id"], "--key", "after-failure", "--text", "Question after failed update?")
        report = self.stable_cli(root, store, "report", task["id"], "--text", "Report after failed update.")
        shown = self.stable_cli(root, store, "show", task["id"])

        # Then: the failed candidate is checked once, the old runtime is restored, and each real callback survives once.
        self.assertEqual(post_check_selections.count(candidate), 1)
        self.assertIn(previous, post_check_selections)
        self.assertEqual(self.current(root), previous)
        self.assertEqual(question.returncode, 0, question.stderr)
        self.assertEqual(report.returncode, 0, report.stderr)
        self.assertEqual(shown.returncode, 0, shown.stderr)
        record = json.loads(shown.stdout)
        self.assertEqual([row["key"] for row in record["questions"]], ["after-failure"])
        self.assertEqual(record["report"]["text"], "Report after failed update.")

    def test_cli_rollback_to_staged_unmerged_sha_refuses_before_pointer_mutation(self) -> None:
        # Given: a current known-good release and a locally committed, staged but unmerged release.
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        previous = self.current(root)
        (root / "unmerged.py").write_text("unmerged = True\n")
        self.git("add", "unmerged.py", cwd=root)
        self.git("commit", "-q", "-m", "unmerged local candidate", cwd=root)
        unmerged = self.git("rev-parse", "HEAD", cwd=root)
        sumctl.stage(store, unmerged, installer=fake_installer)
        ancestor = subprocess.run(
            ["git", "-C", str(root), "merge-base", "--is-ancestor", unmerged, "refs/remotes/origin/main"],
            capture_output=True,
            text=True,
        )
        self.assertNotEqual(ancestor.returncode, 0)

        # When: the stable CLI is asked to roll back to that staged SHA.
        rollback = self.stable_cli(root, store, "update", "rollback", "--to", unmerged)

        # Then: provenance is rejected before the selected pointer can change.
        self.assertNotEqual(rollback.returncode, 0, rollback.stdout + rollback.stderr)
        self.assertEqual(self.current(root), previous)
        self.assertEqual(self.git("rev-parse", "HEAD", cwd=root), unmerged)

    def test_cli_rollback_to_dirty_checkout_refuses_before_pointer_mutation(self) -> None:
        for source in ("tracked", "untracked"):
            with self.subTest(source=source):
                # Given: a current release and a checkout with a tracked or untracked local change.
                root, store = self.installation(name=f"dirty {source} install")
                self.commit_upstream(root, "one.py", "one = True\n")
                self.apply(store)
                previous = self.current(root)
                if source == "tracked":
                    (root / "README.md").write_text("dirty tracked edit\n")
                else:
                    (root / "untracked.txt").write_text("dirty untracked edit\n")
                dirty_before = self.git("status", "--porcelain", cwd=root)
                self.assertTrue(dirty_before)

                # When: the stable CLI is asked to restore the dirty checkout.
                rollback = self.stable_cli(root, store, "update", "rollback", "--to", "checkout")

                # Then: it refuses and leaves both the pointer and source tree untouched.
                self.assertNotEqual(rollback.returncode, 0, rollback.stdout + rollback.stderr)
                self.assertEqual(self.current(root), previous)
                self.assertEqual(self.git("status", "--porcelain", cwd=root), dirty_before)


if __name__ == "__main__":
    unittest.main()
