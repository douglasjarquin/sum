import importlib.util
import platform
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("sumctl", ROOT / "lib/sumctl.py")
sumctl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sumctl)


class NativePackagingTest(unittest.TestCase):
    def test_native_build_produces_cgo_free_binary_without_runtime_tools(self):
        with tempfile.TemporaryDirectory(prefix="sum-native-build-") as name:
            target = Path(name)
            (target / "go").symlink_to(ROOT / "go", target_is_directory=True)
            empty = target / "empty"
            empty.mkdir()
            expected = {"darwin": "darwin", "linux": "linux"}[sys.platform] + "-" + {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine().lower(), platform.machine().lower())
            with mock.patch.dict(sumctl.os.environ, {"SUM_GO_BIN": shutil.which("go"), "GOROOT": "/stale/go", "GOTOOLDIR": "/stale/go/pkg/tool", "GOTOOLCHAIN": "local", "GOOS": "linux", "GOARCH": "amd64"}):
                output = sumctl.build_native_artifact(target)
            result = subprocess.run([str(output), "--version"], env={"PATH": str(empty)}, capture_output=True, text=True, check=True)
            self.assertEqual((result.stdout, result.stderr), ("sum 0.1.0\n", ""))
            self.assertEqual(sumctl.native_platform(), expected)

    def test_native_build_does_not_replace_existing_executable(self):
        with tempfile.TemporaryDirectory(prefix="sum-native-") as name:
            target = Path(name)
            (target / "go").symlink_to(ROOT / "go", target_is_directory=True)
            with mock.patch.dict(sumctl.os.environ, {"SUM_GO_BIN": shutil.which("go"), "GOROOT": "/stale/go", "GOTOOLDIR": "/stale/go/pkg/tool", "GOTOOLCHAIN": "local"}):
                output = sumctl.build_native_artifact(target)
            original = output.read_bytes()
            with mock.patch.dict(sumctl.os.environ, {"SUM_GO_BIN": "/missing/go"}):
                self.assertEqual(sumctl.build_native_artifact(target), output)
            self.assertEqual(output.read_bytes(), original)

    def test_mesh_native_build_produces_cgo_free_opt_in_server(self):
        with tempfile.TemporaryDirectory(prefix="sum-mesh-build-") as name:
            target = Path(name)
            (target / "go").symlink_to(ROOT / "go", target_is_directory=True)
            empty = target / "empty"
            empty.mkdir()
            with mock.patch.dict(sumctl.os.environ, {"SUM_GO_BIN": shutil.which("go"), "GOROOT": "/stale/go", "GOTOOLDIR": "/stale/go/pkg/tool", "GOTOOLCHAIN": "local", "GOOS": "linux", "GOARCH": "amd64"}):
                output = sumctl.build_mesh_artifact(target)
            result = subprocess.run([str(output), "--help"], env={"PATH": str(empty)}, capture_output=True, text=True, check=True)
            self.assertIn("Sum-owned Herdr Mesh MCP bridge", result.stdout)
            self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
