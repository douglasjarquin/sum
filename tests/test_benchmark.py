from __future__ import annotations

from contextlib import ExitStack
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
import benchmark
from benchmark_fixture import SUMCTL, actor_env, clone_case, fixture, invoke_checked, measured_process
import benchmark_real
from benchmark_report import distribution


class BenchmarkMeasurementTest(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.base = Path(self.stack.enter_context(tempfile.TemporaryDirectory(prefix="sum-benchmark-test-", dir="/tmp")))
        self.case = self.stack.enter_context(fixture(self.base / "source"))
        self.case["env"]["SUM_NOW"] = "2026-10-30T00:00:00+00:00"
        self.case["env"]["BENCHMARK_FIXTURE_SECRET"] = "BENCHMARK_SECRET_DO_NOT_RECORD"
        self.trace = self.base / "measurement.json"

    def command(self, case, *args):
        return [str(part) for part in benchmark.command(case, *args)]

    def snapshot(self, case):
        return {str(path.relative_to(case["home"])): path.read_bytes() for path in case["home"].rglob("*") if path.is_file()}

    def test_external_measurement_is_opt_in_private_bounded_and_conformant(self):
        before = self.snapshot(self.case)
        argv = self.command(self.case, "status")
        plain = subprocess.run(argv, env=actor_env(self.case), text=True, capture_output=True, timeout=20)
        self.assertFalse(self.trace.exists())
        measured, sample = measured_process(argv, actor_env(self.case))
        self.assertEqual((plain.returncode, plain.stdout, plain.stderr), (measured.returncode, measured.stdout, measured.stderr))
        self.assertEqual(measured.returncode, 0, measured.stderr)
        self.assertEqual(before, self.snapshot(self.case))
        invoke_checked(argv, actor_env(self.case), self.trace)
        self.assertEqual(stat.S_IMODE(self.trace.stat().st_mode), 0o600)
        text = self.trace.read_text()
        self.assertLess(len(text.encode()), 16 * 1024)
        self.assertNotIn("BENCHMARK_SECRET_DO_NOT_RECORD", text)
        self.assertNotIn("prompt", text.lower())
        self.assertNotIn("python_owned_ms", text)
        self.assertEqual(sample["measurement"], "external-process")
        self.assertIsNone(sample["internal_phases"])
        self.assertIsNone(sample["subprocess_count"])
        self.assertGreaterEqual(sample["wall_ms"], 0)
        self.assertGreaterEqual(sample["cpu_ms"], 0)
        self.assertGreater(sample["peak_rss_kb"], 0)

    def test_measurement_preserves_failure_and_does_not_strip_product_diagnostics(self):
        argv = self.command(self.case, "show", "t-000000000000")
        plain = subprocess.run(argv, env=actor_env(self.case), text=True, capture_output=True, timeout=20)
        measured, sample = measured_process(argv, actor_env(self.case))
        self.assertEqual((plain.returncode, plain.stdout, plain.stderr), (measured.returncode, measured.stdout, measured.stderr))
        self.assertEqual(plain.returncode, 1)
        self.assertEqual(sample["exit_codes"], [1])
        result, _ = measured_process([sys.executable, "-c", "import sys;sys.stderr.write('123 product errors\\n123 maximum resident set size\\n');sys.exit(2)"], actor_env(self.case))
        self.assertEqual(result.stderr, "123 product errors\n123 maximum resident set size\n")

    def test_measurement_preserves_durable_write_contract(self):
        plain_case = clone_case(self.base, self.case, "plain")
        measured_case = clone_case(self.base, self.case, "measured")
        args = ("settings", "set", "--global", "3", "--per-repository", "2")
        plain = subprocess.run(self.command(plain_case, *args), env=actor_env(plain_case), text=True, capture_output=True, timeout=20)
        measured, sample = measured_process(self.command(measured_case, *args), actor_env(measured_case))
        self.assertEqual(plain.returncode, 0, plain.stderr)
        self.assertEqual((plain.returncode, plain.stdout.replace(str(plain_case["root"]), "<STATE>"), plain.stderr),
                         (measured.returncode, measured.stdout.replace(str(measured_case["root"]), "<STATE>"), measured.stderr))
        self.assertEqual((plain_case["home"] / "settings.json").read_bytes(), (measured_case["home"] / "settings.json").read_bytes())
        self.assertEqual(sample["helper_invocations"], 1)
        self.assertEqual(sample["exit_codes"], [0])

    def test_fixture_uses_current_generation_and_receipt_bound_native_identity(self):
        pointer = json.loads((self.case["root"] / "state.json").read_text())
        state = json.loads((self.case["home"] / "state.json").read_text())
        self.assertEqual((pointer["schema"], state["schema"], state["backend"]), (2, 2, "hc"))
        self.assertEqual(state["root"], str(self.case["root"]))
        for role in ("worker", "coordinator"):
            endpoint = self.case["actors"][role]
            self.assertEqual(endpoint["core_id"], self.case["core"].core_id)
            self.assertEqual(endpoint["role"], role)
            self.assertGreater(endpoint["created_ms"], 0)
            self.assertNotIn("pane", endpoint)
        self.assertTrue(all(call["method"].startswith(("hello", "terminal.")) for call in self.case["core"].calls))


class BenchmarkCommandTest(unittest.TestCase):
    def test_bounded_run_preserves_workloads_without_legacy_modes(self):
        with tempfile.TemporaryDirectory(prefix="sum-benchmark-command-", dir="/tmp") as temporary:
            output = Path(temporary) / "result"
            result = subprocess.run([sys.executable, str(ROOT / "scripts/benchmark.py"), "--output", str(output), "--samples", "2", "--skip-real-hc"],
                                    cwd=ROOT, text=True, capture_output=True, timeout=180)
            self.assertEqual(result.returncode, 0, result.stderr)
            raw = json.loads((output / "raw.json").read_text())
            self.assertTrue((output / "report.md").is_file())
            self.assertEqual(raw["schema"], 2)
            self.assertRegex(raw["source"]["sha"], r"^[0-9a-f]{40}$")
            self.assertEqual({row["frequency_class"] for row in raw["inventory"]}, {"interactive", "lifecycle", "rare"})
            scenarios = {row["id"]: row for row in raw["scenarios"]}
            self.assertEqual(set(scenarios), set(benchmark.ALL_SCENARIOS))
            self.assertEqual(scenarios["hc.real.terminal-list"]["status"], "not-run")
            self.assertEqual(scenarios["timeout.hc-response"]["samples"][0]["expected_outcome"], "response-timeout")
            for row in scenarios.values():
                if row["status"] != "measured":
                    continue
                stats = row["statistics"]
                self.assertGreaterEqual(stats["samples"], 1)
                self.assertGreaterEqual(stats["wall_ms"]["p50"], 0)
                self.assertIn("mad", stats["wall_ms"])
                self.assertNotIn("p99", stats["wall_ms"])
                self.assertNotIn("python_owned_ms", stats)
                for sample in row["samples"]:
                    self.assertIsNone(sample["internal_phases"])
                    self.assertIsNone(sample["subprocess_count"])
            self.assertEqual(raw["decision"]["status"], "not-comparable")
            self.assertFalse(raw["decision"]["avoided_time_proven"])
            self.assertTrue(all(not row["parallel_waits_added"] and row["avoidable_ms_upper_bound"] is None for row in raw["weighted_opportunities"]))
            self.assertEqual(raw["cleanup"]["real_lab"], "not-started")
            self.assertTrue(any("production state" in item for item in raw["limitations"]))

    def test_retired_arguments_are_rejected(self):
        for flag in ("--skip-mcp", "--skip-real-herdr"):
            result = subprocess.run([sys.executable, str(ROOT / "scripts/benchmark.py"), flag], text=True, capture_output=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("unrecognized arguments", result.stderr)

    def test_distribution_keeps_outliers_and_requires_100_samples_for_p99(self):
        self.assertEqual(distribution([1, 1, 1, 100])["max"], 100)
        self.assertNotIn("p99", distribution([1] * 99))
        self.assertIn("p99", distribution([1] * 100))
        self.assertIsNone(distribution([]))


class NativeTransportBenchmarkTest(unittest.TestCase):
    def test_real_transport_requires_explicit_private_lab_and_confirmed_teardown(self):
        with tempfile.TemporaryDirectory(prefix="sum-benchmark-lab-test-", dir="/tmp") as temporary:
            artifact = Path(temporary)
            class FakeLab:
                def __init__(self, binary, artifact):
                    self.binary, self.artifact = binary, artifact
                    self.session, self.core_id = "sum-test-benchmark-private", "owned-core"
                    self.root = Path(temporary) / "removed-scope"
                    self.summary = dict(core_stopped=False, runtime="hc 0.1.14")
                    self.methods = []
                def __enter__(self):
                    return self
                def fake(self, name):
                    return dict(state="live"), None, None
                def call(self, method, **params):
                    self.methods.append((method, params))
                    return dict(terminals=[])
                def __exit__(self, *_):
                    self.summary["core_stopped"] = True
            with patch.object(benchmark_real, "selected_binary", return_value="/explicit/hc") as select, patch.object(benchmark_real, "Lab", FakeLab):
                scenarios, cleanup = benchmark_real.run_real(artifact, 2, "/explicit/hc")
            select.assert_called_once_with("/explicit/hc")
            self.assertEqual(cleanup["real_lab"], "removed")
            self.assertTrue(cleanup["core_stopped"])
            self.assertEqual(scenarios[0]["statistics"]["samples"], 2)
            self.assertIsNone(scenarios[0]["statistics"]["peak_rss_kb"])
            self.assertEqual(cleanup["evidence"], str(artifact / "real-hc-evidence.json"))

    def test_unavailable_pinned_binary_is_not_reported_as_measured(self):
        with patch.object(benchmark_real, "selected_binary", side_effect=RuntimeError("pinned HC unavailable")), patch.object(benchmark_real, "Lab") as lab:
            scenarios, cleanup = benchmark_real.run_real(Path("/tmp/unused-benchmark"), 2)
        lab.assert_not_called()
        self.assertEqual(scenarios[0]["status"], "not-run")
        self.assertEqual(cleanup["real_lab"], "not-started")


if __name__ == "__main__":
    unittest.main()
