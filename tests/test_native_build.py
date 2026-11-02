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

    def test_new_build_embeds_modified_and_untracked_source_state(self):
        import json
        import os
        import shutil
        import subprocess
        from unittest.mock import patch

        go = shutil.which("go")
        if not go:
            self.skipTest("Go compiler unavailable")
        for change in ("clean", "modified", "untracked"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                source = root / "go"
                package = source / "internal/runtime"
                command = source / "cmd/sumctl"
                package.mkdir(parents=True)
                command.mkdir(parents=True)
                (source / "go.mod").write_text("module github.com/douglasjarquin/sum/go\n\ngo 1.25\n")
                identity = package / "identity.go"
                identity.write_text("package runtime\nvar BuildSourceRevision string\nvar BuildSourceDigest string\nvar BuildSourceModified string\n")
                (command / "main.go").write_text('package main\nimport ("encoding/json";"os";r "github.com/douglasjarquin/sum/go/internal/runtime")\nfunc main(){json.NewEncoder(os.Stdout).Encode(map[string]string{"sha":r.BuildSourceRevision,"digest":r.BuildSourceDigest,"modified":r.BuildSourceModified})}\n')
                env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(root / "private.gitconfig"))
                Path(env["GIT_CONFIG_GLOBAL"]).write_text("")
                for args in (("init", "-q"), ("add", "go"), ("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")):
                    subprocess.run(["git", "-C", str(root), *args], env=env, check=True)
                revision = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], env=env, text=True).strip()
                if change == "modified":
                    identity.write_text(identity.read_text() + "var ModifiedInput = true\n")
                elif change == "untracked":
                    (package / "additional.go").write_text("package runtime\nvar AdditionalInput = true\n")
                with patch.dict(os.environ, {**env, "SUM_GO_BIN": go}, clear=True):
                    output = runtime.build_native_artifact(root)
                built = json.loads(subprocess.check_output([output], text=True))
                receipt = json.loads(output.with_name("sumctl.build.json").read_text())
                self.assertEqual(built["sha"], revision)
                self.assertEqual(built["modified"], str(change != "clean").lower())
                self.assertEqual(receipt["source_modified"], change != "clean")
                self.assertEqual(built["digest"], runtime.native_source_digest(root))
