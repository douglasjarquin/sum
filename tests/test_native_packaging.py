import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("sumctl", ROOT / "lib/sumctl.py")
sumctl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sumctl)


class NativePackagingTest(unittest.TestCase):
    def test_native_build_reuses_existing_executable_without_replacing_it(self):
        with tempfile.TemporaryDirectory(prefix="sum-native-") as name:
            target = Path(name)
            (target / "go").symlink_to(ROOT / "go", target_is_directory=True)
            with mock.patch.dict(sumctl.os.environ, {"SUM_GO_BIN": shutil.which("go"), "GOROOT": "/stale/go", "GOTOOLDIR": "/stale/go/pkg/tool", "GOTOOLCHAIN": "local"}):
                output = sumctl.build_native_artifact(target)
            original = output.read_bytes()
            output.write_bytes(b"existing native artifact")
            with mock.patch.dict(sumctl.os.environ, {"SUM_GO_BIN": "/missing/go"}):
                self.assertEqual(sumctl.build_native_artifact(target), output)
            self.assertEqual(output.read_bytes(), b"existing native artifact")
            self.assertNotEqual(original, output.read_bytes())


if __name__ == "__main__":
    unittest.main()
