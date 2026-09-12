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

    def test_native_build_stages_mesh_companion_without_runtime_tools(self):
        with tempfile.TemporaryDirectory(prefix="sum-mesh-native-") as name:
            target = Path(name)
            (target / "go").symlink_to(ROOT / "go", target_is_directory=True)
            empty = target / "empty"
            empty.mkdir()
            with mock.patch.dict(sumctl.os.environ, {"SUM_GO_BIN": shutil.which("go"), "GOROOT": "/stale/go", "GOTOOLDIR": "/stale/go/pkg/tool", "GOTOOLCHAIN": "local"}):
                sumctl.build_native_artifact(target)
            mesh = target / ".local" / "bin" / "herdr-mesh"
            result = subprocess.run([str(mesh), "--version"], env={"PATH": str(empty)}, capture_output=True, text=True, check=True)
            self.assertEqual((result.stdout, result.stderr), ("herdr-mesh 0.1.0\n", ""))

    def test_mesh_launcher_uses_selected_runtime(self):
        with tempfile.TemporaryDirectory(prefix="sum-mesh-launcher-") as name:
            target = Path(name)
            launcher = target / "bin" / "herdr-mesh"
            launcher.parent.mkdir()
            shutil.copy2(ROOT / "bin" / "herdr-mesh", launcher)
            launcher.chmod(0o755)
            release = target / "release"
            binary = release / ".local" / "bin" / "herdr-mesh"
            binary.parent.mkdir(parents=True)
            binary.write_text("#!/bin/sh\nprintf 'selected-runtime\\n'\n")
            binary.chmod(0o755)
            (target / ".local").mkdir()
            (target / ".local" / "current").symlink_to(release, target_is_directory=True)

            result = subprocess.run([str(launcher), "--version"], env={"PATH": "/usr/bin:/bin"}, capture_output=True, text=True, check=True)
            self.assertEqual((result.stdout, result.stderr), ("selected-runtime\n", ""))


if __name__ == "__main__":
    unittest.main()
