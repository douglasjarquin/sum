import json
import os
from pathlib import Path
import shutil
import sys
from unittest import mock

from tests.test_core import UpdateLab, sumctl


class RecoveryReviewTest(UpdateLab):
    def cli_at(self, root, store, *args):
        return self.cli([root / "bin/sumctl", "--home", store.home, *args], cwd=root,
                        env={"PATH": f"{Path(sys.executable).parent}:{os.environ['PATH']}"})

    def pending_update(self, root, store, before_selection=False):
        select = sumctl.select_default

        def interrupt(installation, target):
            if not before_selection:
                select(installation, target)
            raise KeyboardInterrupt("after selection")

        with mock.patch.object(sumctl, "select_default", side_effect=interrupt):
            with self.assertRaises(KeyboardInterrupt):
                self.apply(store, no_fetch=True)
        return json.loads((root / ".local/activation.json").read_text())["pending"]

    def test_failed_activation_audit_preserves_recoverable_generation(self):
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        original = sumctl.update_log

        def fail_audit(installation, entry):
            if entry.get("result") == "selected":
                raise OSError("audit flush failed")
            return original(installation, entry)

        with mock.patch.object(sumctl, "update_log", side_effect=fail_audit):
            with self.assertRaisesRegex(OSError, "audit flush failed"):
                self.apply(store, no_fetch=True)
        pending = json.loads((root / ".local/activation.json").read_text())["pending"]
        self.assertIsInstance(pending, dict, "failed audit must retain the pending generation")
        recovered = self.cli_at(root, store, "update", "recover", "--generation", pending["generation"])
        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertEqual(self.current(root), root / ".local/releases" / first)

    def test_cli_recovery_audit_failure_retains_pending_and_audits_prior_resume(self):
        root, store = self.installation()
        task = self.task_fixture(store)
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        pending = self.pending_update(root, store)
        asked = self.cli_at(root, store, "ask", task["id"], "--key", "before-recovery", "--text", "Still working?")
        self.assertEqual(asked.returncode, 0, asked.stderr)
        callbacks = self.snapshot(store.home)
        audit_log = root / sumctl.UPDATE_LOG
        self.assertTrue(audit_log.is_file())
        audit_history = audit_log.with_name("updates.jsonl.history")
        audit_log.replace(audit_history)
        audit_log.mkdir()

        failed = self.cli_at(root, store, "update", "recover", "--generation", pending["generation"])
        self.assertNotEqual(failed.returncode, 0, "recovery must not clear pending state when its audit cannot append")
        self.assertIn("Is a directory", failed.stderr)
        saved = json.loads((root / ".local/activation.json").read_text())["pending"]
        self.assertIsInstance(saved, dict, "failed recovery audit must retain the pending generation")
        self.assertEqual(saved["generation"], pending["generation"])
        self.assertEqual(self.current(root), root / ".local/releases" / first)
        self.assertEqual(self.snapshot(store.home), callbacks, "recovery must not replay or alter callback records")
        audit_log.rmdir()
        audit_history.replace(audit_log)

        recovered = self.cli_at(root, store, "update", "recover", "--generation", pending["generation"])
        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        self.assertFalse(json.loads(recovered.stdout)["changed"])
        self.assertIsNone(json.loads((root / ".local/activation.json").read_text())["pending"])
        self.assertEqual(self.snapshot(store.home), callbacks, "resumed recovery must preserve callback records")
        audit = json.loads(audit_log.read_text().splitlines()[-1])
        self.assertEqual(audit["action"], "recover")
        self.assertEqual(audit["result"], "recovered")
        self.assertEqual(audit["generation"], pending["generation"])
        self.assertFalse(audit["changed"])
        self.assertEqual(audit["from"], pending["from"])
        self.assertEqual(audit["to"], pending["from"])

    def test_standalone_recovery_audit_failure_retains_pending_and_audits_prior_resume(self):
        root, store = self.installation()
        task = self.task_fixture(store)
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        pending = self.pending_update(root, store, before_selection=True)
        asked = self.cli_at(root, store, "ask", task["id"], "--key", "before-standalone", "--text", "Still working?")
        self.assertEqual(asked.returncode, 0, asked.stderr)
        callbacks = self.snapshot(store.home)
        audit_log = root / sumctl.UPDATE_LOG
        self.assertTrue(audit_log.is_file())
        audit_history = audit_log.with_name("updates.jsonl.history")
        audit_log.replace(audit_history)
        audit_log.mkdir()

        failed = self.cli(pending["recovery"]["argv"], env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)
        self.assertNotEqual(failed.returncode, 0, "standalone recovery must not clear pending state when its audit cannot append")
        self.assertIn("Is a directory", failed.stderr)
        saved = json.loads((root / ".local/activation.json").read_text())["pending"]
        self.assertIsInstance(saved, dict, "failed standalone audit must retain the pending generation")
        self.assertEqual(saved["generation"], pending["generation"])
        self.assertEqual(self.current(root), root / ".local/releases" / first)
        self.assertEqual(self.snapshot(store.home), callbacks, "standalone recovery must not replay or alter callback records")
        audit_log.rmdir()
        audit_history.replace(audit_log)

        recovered = self.cli(pending["recovery"]["argv"], env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)
        self.assertEqual(recovered.returncode, 0, recovered.stderr)
        value = json.loads(recovered.stdout)
        self.assertFalse(value["changed"])
        self.assertIsNone(json.loads((root / ".local/activation.json").read_text())["pending"])
        self.assertEqual(self.snapshot(store.home), callbacks, "resumed standalone recovery must preserve callback records")
        audit = json.loads(audit_log.read_text().splitlines()[-1])
        self.assertEqual(audit["action"], "recover")
        self.assertEqual(audit["result"], "recovered")
        self.assertEqual(audit["generation"], pending["generation"])
        self.assertFalse(audit["changed"])
        self.assertEqual(audit["from"], pending["from"])
        self.assertEqual(audit["to"], pending["from"])

    def test_incomplete_approval_provenance_refuses_rollback(self):
        root, store = self.installation()
        first = self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        self.apply(store)
        selected = self.current(root)
        path = root / ".local/approvals.json"
        original = path.read_text()
        for field in ("branch", "tip", "approved_at"):
            with self.subTest(field=field):
                state = json.loads(original)
                state["revisions"][first].pop(field)
                sumctl.atomic_json(path, state)
                result = self.cli_at(root, store, "update", "rollback", "--to", first)
                self.assertNotEqual(result.returncode, 0, f"missing {field} must not authorize selection")
                self.assertEqual(self.current(root), selected)

    def test_independent_recovery_refuses_a_copied_state_home(self):
        root, store = self.installation()
        self.commit_upstream(root, "one.py", "one = True\n")
        self.apply(store)
        self.commit_upstream(root, "two.py", "two = True\n")
        pending = self.pending_update(root, store)
        copied = self.root / "copied-home"
        shutil.copytree(store.home, copied)
        argv = list(pending["recovery"]["argv"])
        argv[argv.index("--home") + 1] = str(copied)
        selected = self.current(root)
        before = (root / ".local/activation.json").read_bytes()
        result = self.cli(argv, env={"SUM_INSTALL_ROOT": str(root)}, cwd=root)
        self.assertNotEqual(result.returncode, 0, "a copied instance ID is not authority for another state home")
        self.assertEqual(self.current(root), selected)
        self.assertEqual((root / ".local/activation.json").read_bytes(), before)
