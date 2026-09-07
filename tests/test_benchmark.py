from __future__ import annotations

import json
import os
from pathlib import Path
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


if __name__ == "__main__":
    unittest.main()
