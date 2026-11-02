import importlib.util
from pathlib import Path
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "scripts/install_runtime.py"
SPEC = importlib.util.spec_from_file_location("native_build_runtime", SCRIPT)
runtime = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runtime)


class NativeBuildTest(unittest.TestCase):
    def test_setup_preserves_and_refuses_unverifiable_existing_helper(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "go").mkdir()
            (root / "go/go.mod").write_text("module current\n")
            output = root / ".local/bin/sumctl"
            output.parent.mkdir(parents=True)
            original = b"#!/bin/sh\necho old-helper\n"
            output.write_bytes(original)
            output.chmod(0o755)
            with self.assertRaisesRegex(runtime.InstallError, "stale or unverifiable"):
                runtime.build_native_artifact(root)
            self.assertEqual(output.read_bytes(), original)
            self.assertFalse(output.with_name("sumctl.build.json").exists())
