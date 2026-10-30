"""Failure-path checks for private HC lab ownership and truthful teardown."""
import importlib.util
import json
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("hc_lab", Path(__file__).parents[1] / "scripts/hc_lab.py")
hc_lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hc_lab)


class BinarySelectionTests(unittest.TestCase):
    def test_missing_explicit_binary_never_uses_path_candidate(self):
        with tempfile.TemporaryDirectory() as directory:
            missing = str(Path(directory) / "missing-staged-hc")
            with patch.object(hc_lab.shutil, "which", return_value="/otherwise-valid/hc") as discover, patch.object(hc_lab.subprocess, "check_output", return_value="hc 0.1.14\n") as execute:
                with self.assertRaisesRegex(RuntimeError, "missing-staged-hc"):
                    hc_lab.selected_binary(missing)
                discover.assert_not_called()
                execute.assert_not_called()


class LabTeardownTests(unittest.TestCase):
    def lab(self):
        with patch.object(hc_lab, "selected_binary", return_value="/fake/hc"):
            lab = hc_lab.Lab()
        self.addCleanup(lambda: shutil.rmtree(lab.root, ignore_errors=True))
        return lab

    @staticmethod
    def cli_result(code=0):
        return subprocess.CompletedProcess(["/fake/hc"], code, "", "failed to stop" if code else "")

    def test_stop_failure_is_failed_and_preserves_scope(self):
        lab = self.lab()
        lab.core_id = "owned-core"
        with patch.object(lab, "call", return_value={"core": {"id": "owned-core"}}), patch.object(lab, "cli", return_value=self.cli_result(1)):
            with self.assertRaisesRegex(RuntimeError, "failed to stop"):
                lab.__exit__(None, None, None)
        self.assertEqual(lab.summary["outcome"], "failed")
        self.assertFalse(lab.summary["core_stopped"])
        self.assertTrue(lab.root.is_dir())
        self.assertEqual(lab.summary["retained_scope"], str(lab.root))
        self.assertEqual(json.loads((lab.root / "evidence.json").read_text())["outcome"], "failed")

    def test_identity_change_refuses_stop_and_preserves_scope(self):
        lab = self.lab()
        lab.core_id = "owned-core"
        with patch.object(lab, "call", return_value={"core": {"id": "other-core"}}), patch.object(lab, "cli") as cli:
            with self.assertRaisesRegex(RuntimeError, "identity changed"):
                lab.__exit__(None, None, None)
            cli.assert_not_called()
        self.assertEqual(lab.summary["outcome"], "failed")
        self.assertTrue(lab.root.exists())

    def test_cli_success_requires_socket_stop_observation(self):
        lab = self.lab()
        lab.core_id = "owned-core"
        lab.socket.parent.mkdir(parents=True)
        with socket.socket(socket.AF_UNIX) as listener:
            listener.bind(str(lab.socket))
            listener.listen()
            with patch.object(lab, "call", return_value={"core": {"id": "owned-core"}}), patch.object(lab, "cli", return_value=self.cli_result()), patch.object(hc_lab.time, "monotonic", side_effect=[0, 6]):
                with self.assertRaisesRegex(RuntimeError, "still reachable"):
                    lab.__exit__(None, None, None)
        self.assertFalse(lab.summary["core_stopped"])
        self.assertTrue(lab.root.exists())

    def test_startup_failure_records_and_retains_uncertain_scope(self):
        lab = self.lab()
        with patch.object(lab, "cli", return_value=self.cli_result(1)):
            with self.assertRaisesRegex(RuntimeError, "failed to stop"):
                lab.__enter__()
        self.assertTrue(lab.root.exists())
        self.assertEqual(lab.summary["outcome"], "failed")
        self.assertIn("startup_error", lab.summary)
        self.assertEqual(lab.summary["retained_scope"], str(lab.root))

    def test_body_failure_retains_diagnostics_after_confirmed_stop(self):
        lab = self.lab()
        lab.core_id = "owned-core"
        with patch.object(lab, "call", return_value={"core": {"id": "owned-core"}}), patch.object(lab, "cli", return_value=self.cli_result()):
            self.assertFalse(lab.__exit__(AssertionError, AssertionError("canary failed"), None))
        self.assertEqual(lab.summary["outcome"], "failed")
        self.assertTrue(lab.summary["core_stopped"])
        self.assertTrue(lab.root.exists())

    def test_success_cleans_only_after_stop_observation(self):
        lab = self.lab()
        lab.core_id = "owned-core"
        with patch.object(lab, "call", return_value={"core": {"id": "owned-core"}}), patch.object(lab, "cli", return_value=self.cli_result()):
            lab.__exit__(None, None, None)
        self.assertEqual(lab.summary["outcome"], "pass")
        self.assertTrue(lab.summary["core_stopped"])
        self.assertFalse(lab.root.exists())


if __name__ == "__main__":
    unittest.main()
