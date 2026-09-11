from __future__ import annotations
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("sumctl", ROOT / "lib/sumctl.py")
sumctl = importlib.util.module_from_spec(spec)
sys.modules["sumctl"] = sumctl
spec.loader.exec_module(sumctl)

REMAINDER = ROOT / "tests/fixtures/remainder.py"
QUOTA_AXI = ROOT / "tests/fixtures/quota_axi.py"


class QuotaTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="sum-quota-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.store = sumctl.Store(self.root / "state")
        self.store.init()
        self.env = {
            "SUM_HOME": "", "SUM_SESSION": "", "HERDR_SOCKET_PATH": "",
            "SUM_REMAINDER_BIN": str(REMAINDER),
            "SUM_QUOTA_AXI_BIN": str(QUOTA_AXI),
            "SUM_HERDR_BIN": "", "SUM_GH_BIN": "",
            "SUM_REMAINDER_NO_DOWNLOAD": "1",
            "FAKE_REMAINDER_ROOT": str(self.root / "remainder"),
            "FAKE_QUOTA_AXI_ROOT": str(self.root / "quota-axi"),
            "FAKE_REMAINDER_STATUS": "healthy",
            "HERDR_ENV": "1", "HERDR_PANE_ID": "w-parent:p1", "HERDR_SESSION": "sum-test",
        }
        self.patch = mock.patch.dict(os.environ, self.env)
        self.patch.start()
        self.addCleanup(self.patch.stop)

    def cli(self, *args, env=None):
        merged = os.environ.copy()
        merged.update(env or {})
        return subprocess.run(
            [sys.executable, str(ROOT / "lib/sumctl.py"), "--home", str(self.store.home), *args],
            env=merged, capture_output=True, text=True,
        )

    def remainder_calls(self):
        path = self.root / "remainder/calls.jsonl"
        return [json.loads(line)["args"] for line in path.read_text().splitlines()] if path.exists() else []

    def axi_calls(self):
        path = self.root / "quota-axi/calls.jsonl"
        return [json.loads(line)["args"] for line in path.read_text().splitlines()] if path.exists() else []

    def test_healthy_codex_json_contains_literal_remaining(self):
        result = self.cli("quota", "--provider", "codex", "--format", "json")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('"remaining": 40', result.stdout)
        self.assertEqual(self.remainder_calls(), [["--provider", "codex", "--profile", "default", "--format", "json"]])
        self.assertEqual(self.axi_calls(), [])

    def test_healthy_codex_compact_default(self):
        result = self.cli("quota", "--provider", "codex")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("codex remaining=40", result.stdout)
        self.assertEqual(self.remainder_calls(), [["--provider", "codex", "--profile", "default"]])
        self.assertEqual(self.axi_calls(), [])

    def test_exhausted_codex_is_usable_zero(self):
        result = self.cli("quota", "--provider", "codex", "--format", "json",
                          env={"FAKE_REMAINDER_STATUS": "exhausted"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('"remaining": 0', result.stdout)
        self.assertNotIn("missing", result.stdout.lower())
        self.assertEqual(self.axi_calls(), [])

    def test_unavailable_codex_is_nonzero_and_names_unavailable(self):
        result = self.cli("quota", "--provider", "codex", env={"FAKE_REMAINDER_STATUS": "unavailable"})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.returncode, 1)
        self.assertFalse(result.stdout.strip())
        self.assertIn("unavailable", result.stderr)
        self.assertEqual(self.axi_calls(), [])

    def test_invalid_remainder_selection_exits_2(self):
        result = self.cli("quota", "--provider", "codex", env={"FAKE_REMAINDER_STATUS": "invalid"})
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("invalid", result.stderr)
        self.assertEqual(self.axi_calls(), [])

    def test_partial_remainder_exits_3(self):
        result = self.cli("quota", "--provider", "codex", env={"FAKE_REMAINDER_STATUS": "partial"})
        self.assertEqual(result.returncode, 3, result.stderr)
        self.assertIn("partial", result.stderr)
        self.assertEqual(self.axi_calls(), [])

    def test_claude_uses_quota_axi_not_remainder(self):
        result = self.cli("quota", "--provider", "claude")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.remainder_calls(), [])
        self.assertEqual(self.axi_calls(), [["--provider", "claude"]])
        self.assertIn("quota-axi provider=claude", result.stdout)

    def test_missing_remainder_for_codex_refuses_without_quota_axi(self):
        result = self.cli("quota", "--provider", "codex", env={
            "SUM_REMAINDER_BIN": str(self.root / "missing-remainder"),
        })
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("remainder", result.stderr.lower())
        self.assertIn("quota-axi is not used", result.stderr)
        self.assertEqual(self.remainder_calls(), [])
        self.assertEqual(self.axi_calls(), [])

    def test_doctor_lists_remainder_without_crashing_when_missing(self):
        with mock.patch.object(sumctl, "RUNTIME", self.root):
            with mock.patch.dict(os.environ, {
                "SUM_REMAINDER_BIN": "", "SUM_QUOTA_AXI_BIN": "", "SUM_HERDR_BIN": "",
                "SUM_GH_BIN": "", "PATH": "/usr/bin:/bin", "HERDR_ENV": "", "HERDR_PANE_ID": "",
            }, clear=False):
                result = sumctl.doctor(self.store)
        tools = {row["tool"]: row for row in result["checks"]}
        self.assertIn("remainder", tools)
        self.assertFalse(tools["remainder"]["ok"])
        self.assertIn("quota-axi", tools)


if __name__ == "__main__":
    unittest.main()
