from __future__ import annotations

import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("benchmark_go", ROOT / "scripts" / "benchmark_go.py")
benchmark = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(benchmark)


class GoBenchmarkEvidenceTest(unittest.TestCase):
    def scenarios(self, latency=6):
        return [{"id": name, "p50_ms": latency} for name in (
            "startup.version.cold", "startup.help.cobra", "read.status.fixture", "failure.show-missing",
        )]

    def test_exit_codes_alone_do_not_establish_behavior_parity(self):
        result = benchmark.gate(self.scenarios(), {})
        self.assertFalse(result["behavior"]["pass"])
        self.assertIsNone(result["behavior"]["observed_regressions"])
        self.assertEqual(result["behavior"]["status"], "not-evaluated")

    def test_failed_latency_gate_is_not_described_as_crossed(self):
        result = benchmark.gate(self.scenarios(latency=1000), {})
        self.assertFalse(result["interactive_hot_path"]["pass"])
        self.assertNotIn("The native startup/help path crosses the latency gate.", result["reasons"])

    def test_reference_snapshot_uses_frozen_bytes_not_working_tree(self):
        with tempfile.TemporaryDirectory(prefix="sum-go-reference-test-") as temporary:
            base = Path(temporary)
            repo = base / "repo"
            repo.mkdir()
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.name", "fixture"], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.email", "fixture@example.invalid"], check=True)
            helper = repo / "bin" / "sumctl"
            helper.parent.mkdir()
            helper.write_text("#!/bin/sh\nprintf frozen\n")
            helper.chmod(0o755)
            subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "frozen"], check=True)
            revision = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
            helper.write_text("#!/bin/sh\nprintf changed\n")
            with patch.object(benchmark, "ROOT", repo), patch.object(benchmark, "REFERENCE_REVISION", revision, create=True):
                snapshot = benchmark.reference_snapshot(base / "reference")
            result = subprocess.run([str(snapshot / "bin" / "sumctl")], capture_output=True, text=True, check=True)
            self.assertEqual(result.stdout, "frozen")
            self.assertEqual(helper.read_text(), "#!/bin/sh\nprintf changed\n")
            self.assertFalse((snapshot / ".git").exists())


if __name__ == "__main__":
    unittest.main()
