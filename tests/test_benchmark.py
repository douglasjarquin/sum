from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import stat
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]


class BenchmarkInstrumentationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="sum-benchmark-test-")
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name).resolve()
        self.home = self.base / "state"
        self.home.mkdir(mode=0o700)
        (self.home / "tasks").mkdir(mode=0o700)
        (self.home / "state.json").write_text(
            '{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-09-07T00:00:00+00:00"}\n',
            encoding="utf-8",
        )
        self.trace = self.base / "trace.jsonl"
        self.secret = "SUM_BENCHMARK_SECRET_DO_NOT_RECORD"

    def invoke(self, *args: str, measured: bool) -> subprocess.CompletedProcess[str]:
        env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("SUM_", "HERDR_"))
        }
        env["BENCHMARK_FIXTURE_SECRET"] = self.secret
        if measured:
            env["SUM_MEASURE_FILE"] = str(self.trace)
            env["SUM_MEASURE_PARENT_NS"] = str(time.perf_counter_ns())
        return subprocess.run(
            [str(ROOT / "bin" / "sumctl"), "--home", str(self.home), *args],
            env=env,
            text=True,
            capture_output=True,
            timeout=20,
        )

    def snapshot(self) -> dict[str, bytes]:
        return {
            str(path.relative_to(self.home)): path.read_bytes()
            for path in self.home.rglob("*")
            if path.is_file()
        }

    def test_measurement_is_opt_in_bounded_private_and_conformant(self):
        # Given: one immutable lab state and no requested measurement file.
        before = self.snapshot()

        # When: the production status entrypoint runs first normally, then measured.
        plain = self.invoke("status", measured=False)
        after_plain = self.snapshot()
        measured = self.invoke("status", measured=True)

        # Then: public behavior and state are byte-identical, while only the opt-in run records metrics.
        self.assertEqual((plain.returncode, plain.stdout, plain.stderr), (0, measured.stdout, measured.stderr))
        self.assertEqual(before, after_plain)
        self.assertEqual(before, self.snapshot())
        [line] = self.trace.read_text(encoding="utf-8").splitlines()
        self.assertLess(len(line.encode("utf-8")), 16 * 1024)
        self.assertEqual(stat.S_IMODE(self.trace.stat().st_mode), 0o600)
        self.assertNotIn(self.secret, line)
        self.assertNotIn("prompt", line.lower())
        record = json.loads(line)
        self.assertEqual((record["schema"], record["command"], record["exit_code"]), (1, "status", 0))
        self.assertGreaterEqual(record["wall_ms"], 0)
        self.assertGreaterEqual(record["startup_ms"], 0)
        self.assertGreaterEqual(record["cpu_user_ms"], 0)
        self.assertGreaterEqual(record["cpu_system_ms"], 0)
        self.assertGreater(record["peak_rss_kb"], 0)
        self.assertGreaterEqual(record["subprocess_count"], 0)
        self.assertGreaterEqual(record["state_lock_wait_ms"], 0)
        self.assertIn("io.read_json", record["phases"])

    def test_measurement_preserves_failure_contract(self):
        # Given: a valid lab home and a task id that has no record.
        task_id = "t-000000000000"

        # When: the same production failure runs without and with measurement.
        plain = self.invoke("show", task_id, measured=False)
        measured = self.invoke("show", task_id, measured=True)

        # Then: exit/stdout/stderr stay exact and the trace truthfully records failure.
        self.assertEqual((plain.returncode, plain.stdout, plain.stderr), (measured.returncode, measured.stdout, measured.stderr))
        self.assertEqual(plain.returncode, 1)
        [record] = [json.loads(line) for line in self.trace.read_text(encoding="utf-8").splitlines()]
        self.assertEqual((record["command"], record["exit_code"]), ("show", 1))

    def test_measurement_preserves_durable_write_contract(self):
        template = self.base / "template"
        template.mkdir(mode=0o700)
        (template / "state.json").write_text(
            '{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-09-07T00:00:00+00:00"}\n',
            encoding="utf-8",
        )
        fake = self.base / "fake-template"
        env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("SUM_", "HERDR_"))
        }
        env.update(
            {
                "HERDR_ENV": "1",
                "HERDR_PANE_ID": "w-parent:p1",
                "HERDR_SESSION": "sum-benchmark-test",
                "SUM_SESSION": "sum-benchmark-test",
                "SUM_HERDR_BIN": str(ROOT / "tests" / "fixtures" / "herdr.py"),
                "FAKE_HERDR_ROOT": str(fake),
                "FAKE_PARENT_CWD": str(ROOT),
                "FAKE_SESSION": "sum-benchmark-test",
            }
        )
        initialized = subprocess.run(
            [str(ROOT / "bin" / "sumctl"), "--home", str(template), "init"],
            env=env,
            text=True,
            capture_output=True,
            timeout=20,
        )
        self.assertEqual(initialized.returncode, 0, initialized.stderr)
        plain_home = self.base / "plain-write"
        measured_home = self.base / "measured-write"
        plain_fake = self.base / "plain-fake"
        measured_fake = self.base / "measured-fake"
        shutil.copytree(template, plain_home)
        shutil.copytree(template, measured_home)
        shutil.copytree(fake, plain_fake)
        shutil.copytree(fake, measured_fake)
        command = ["settings", "set", "--global", "3", "--per-repository", "2"]
        plain_env = {**env, "FAKE_HERDR_ROOT": str(plain_fake)}
        measured_env = {
            **env,
            "FAKE_HERDR_ROOT": str(measured_fake),
            "SUM_MEASURE_FILE": str(self.trace),
            "SUM_MEASURE_PARENT_NS": str(time.perf_counter_ns()),
        }
        plain = subprocess.run([str(ROOT / "bin" / "sumctl"), "--home", str(plain_home), *command], env=plain_env, text=True, capture_output=True, timeout=20)
        measured = subprocess.run([str(ROOT / "bin" / "sumctl"), "--home", str(measured_home), *command], env=measured_env, text=True, capture_output=True, timeout=20)
        plain_stdout = plain.stdout.replace(str(plain_home), "<HOME>")
        measured_stdout = measured.stdout.replace(str(measured_home), "<HOME>")
        self.assertEqual((plain.returncode, plain_stdout, plain.stderr), (measured.returncode, measured_stdout, measured.stderr))
        plain_files = {str(path.relative_to(plain_home)): path.read_bytes() for path in plain_home.rglob("*") if path.is_file()}
        measured_files = {str(path.relative_to(measured_home)): path.read_bytes() for path in measured_home.rglob("*") if path.is_file()}
        self.assertEqual(plain_files, measured_files)
        [record] = [json.loads(line) for line in self.trace.read_text(encoding="utf-8").splitlines()]
        self.assertEqual(record["command"], "settings.set")
        self.assertGreater(record["phases"]["io.atomic_json"]["count"], 0)
        self.assertGreater(record["phases"]["lock.state_wait"]["count"], 0)


class BenchmarkCommandTest(unittest.TestCase):
    def test_bounded_run_produces_complete_machine_contract(self):
        with tempfile.TemporaryDirectory(prefix="sum-benchmark-command-") as tmp:
            output = Path(tmp) / "result"

            result = subprocess.run(
                [
                    os.environ.get("PYTHON", "python3"),
                    str(ROOT / "scripts" / "benchmark.py"),
                    "--output",
                    str(output),
                    "--samples",
                    "2",
                    "--skip-real-herdr",
                    "--skip-mcp",
                    "--skip-timeout",
                ],
                cwd=ROOT,
                text=True,
                capture_output=True,
                timeout=180,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            raw = json.loads((output / "raw.json").read_text(encoding="utf-8"))
            self.assertTrue((output / "report.md").is_file())
            self.assertEqual(raw["schema"], 1)
            self.assertRegex(raw["source"]["sha"], r"^[0-9a-f]{40}$")
            self.assertEqual({row["frequency_class"] for row in raw["inventory"]}, {"interactive", "lifecycle", "rare"})
            required = {
                "startup.version.cold",
                "startup.help.warm-fs",
                "read.settings",
                "read.status.empty",
                "read.status.12-workers",
                "read.status.archived-25",
                "read.status.archived-100",
                "read.inbox",
                "read.show",
                "read.context",
                "write.ask",
                "write.report",
                "role.init",
                "brief.regenerate",
                "dispatch.prepare",
                "herdr.fake.agent-list",
                "herdr.real.workspace-list",
                "mcp.real.agent-list",
                "contention.concurrent-writes",
                "failure.show-missing",
                "timeout.herdr-prompt",
                "question.worker-to-inbox",
            }
            scenarios = {row["id"]: row for row in raw["scenarios"]}
            self.assertEqual(set(scenarios), required)
            for scenario in scenarios.values():
                self.assertIn(scenario["status"], {"measured", "not-run"})
                if scenario["status"] == "measured":
                    self.assertGreaterEqual(scenario["statistics"]["samples"], 1)
                    self.assertGreaterEqual(scenario["statistics"]["wall_ms"]["p50"], 0)
                    self.assertGreaterEqual(scenario["statistics"]["wall_ms"]["p95"], 0)
                    self.assertIn("mad", scenario["statistics"]["wall_ms"])
                    self.assertNotIn("p99", scenario["statistics"]["wall_ms"])
            self.assertIn("harness_overhead_ms", raw["methodology"])
            self.assertEqual({row["basis"] for row in raw["frequency"]}, {"observed-lab", "simulated-fixture"})
            self.assertTrue(all(not row["parallel_waits_added"] for row in raw["weighted_opportunities"]))
            self.assertGreaterEqual(len(raw["alternatives"]), 3)
            self.assertTrue(all(not row["mixed_into_language_claim"] for row in raw["alternatives"]))
            self.assertTrue(any("production state" in limitation for limitation in raw["limitations"]))
            self.assertEqual(raw["cleanup"]["real_lab"], "not-started")
            envelope = raw["decision"]["minimum_meaningful_improvement"]
            self.assertGreater(envelope["absolute_ms"], 0)
            self.assertGreater(envelope["relative_percent"], 0)
            self.assertGreater(envelope["peak_memory_regression_percent"], 0)
            self.assertIn(raw["decision"]["outcome"], {"proceed", "defer"})


if __name__ == "__main__":
    unittest.main()
