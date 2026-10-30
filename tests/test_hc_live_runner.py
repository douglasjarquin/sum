"""The canonical native runner retains independently inspectable canary proofs."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).parents[1]


class LiveRunnerEvidenceTests(unittest.TestCase):
    def test_each_canary_keeps_its_own_artifact(self):
        for joined_flag in (False, True, None):
            with self.subTest(joined_flag=joined_flag), tempfile.TemporaryDirectory(prefix="hc runner ") as directory:
                root = Path(directory)
                (root / "mise-tasks").mkdir()
                (root / "scripts").mkdir()
                (root / "bin").mkdir()
                runner = root / "mise-tasks/test-live"
                shutil.copy2(ROOT / "mise-tasks/test-live", runner)
                stub = """import argparse,json
from pathlib import Path
parser=argparse.ArgumentParser()
parser.add_argument('--artifact')
parser.add_argument('--hc')
args=parser.parse_args()
if args.artifact:
    Path(args.artifact).write_text(json.dumps({'scenario':Path(__file__).stem,'hc':args.hc}))
else:
    print(Path(__file__).stem)
"""
                for name in ("live_incarnation.py", "live_smoke.py"):
                    (root / "scripts" / name).write_text(stub)
                python = root / "bin/python"
                python.write_text("#!" + sys.executable + "\nimport os,sys\nos.execv(sys.executable,[sys.executable,*sys.argv[1:]])\n")
                python.chmod(0o700)
                artifact = root / "proof with spaces.json"
                artifact.write_text("earlier evidence remains untouched")
                flags = ["--artifact=" + str(artifact)] if joined_flag else ["--artifact", str(artifact)]
                common = ["--hc", "/exact staged hc"]
                if joined_flag is None:
                    flags, common = [], []
                result = subprocess.run(["/bin/bash", str(runner), *common, *flags],
                                        env={**os.environ, "PATH": str(root / "bin") + os.pathsep + os.environ["PATH"]},
                                        text=True, capture_output=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stderr)
                if joined_flag is None:
                    self.assertEqual(result.stdout.splitlines(), ["live_incarnation", "live_smoke"])
                    continue
                identity = json.loads((root / "proof with spaces-incarnation.json").read_text())
                smoke = json.loads((root / "proof with spaces-smoke.json").read_text())
                self.assertEqual(identity, {"scenario": "live_incarnation", "hc": "/exact staged hc"})
                self.assertEqual(smoke, {"scenario": "live_smoke", "hc": "/exact staged hc"})
                self.assertEqual(artifact.read_text(), "earlier evidence remains untouched")


if __name__ == "__main__":
    unittest.main()
