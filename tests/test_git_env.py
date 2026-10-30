import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
WRAPPER = ROOT / "scripts" / "fixture_git_env.py"
spec = importlib.util.spec_from_file_location("test_git_environment", WRAPPER)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class GitEnvironmentTests(unittest.TestCase):
    def test_fixture_commit_ignores_inherited_signer_and_identity(self):
        with tempfile.TemporaryDirectory(prefix="git fixture ") as temporary:
            root = Path(temporary)
            signer = root / "failing-signer"
            signer.write_text("#!/bin/sh\necho private-fixture-signer-invoked >&2\nexit 1\n")
            signer.chmod(0o700)
            hostile = root / "hostile.config"
            hostile.write_text(f'[user]\n name = Host User\n email = host@example.invalid\n signingKey = private-fixture-key\n[commit]\n gpgSign = true\n[tag]\n gpgSign = true\n[gpg]\n program = "{signer}"\n')
            before = hostile.read_bytes()
            env = dict(os.environ, GIT_CONFIG_GLOBAL=str(hostile), GIT_CONFIG_NOSYSTEM="1")
            for key in list(env):
                if key.startswith("GIT_CONFIG") and key not in {"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"}:
                    del env[key]
                if key in {"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_TEMPLATE_DIR"}:
                    del env[key]
            repo = root / "repo"
            subprocess.run(["git", "init", "-q", str(repo)], env=env, check=True)
            command = ["git", "-C", str(repo), "commit", "--allow-empty", "-m", "fixture"]
            red = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(red.returncode, 128, red.stderr)
            self.assertIn("private-fixture-signer-invoked", red.stderr)
            env.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="commit.gpgSign", GIT_CONFIG_VALUE_0="true",
                       GIT_AUTHOR_NAME="Inherited Author", GIT_AUTHOR_EMAIL="inherited@example.invalid",
                       GIT_COMMITTER_NAME="Inherited Committer", GIT_COMMITTER_EMAIL="inherited@example.invalid")
            green = subprocess.run([sys.executable, str(WRAPPER), "--", *command], env=env, cwd=repo, capture_output=True, text=True)
            self.assertEqual(green.returncode, 0, green.stderr)
            commit = subprocess.check_output(["git", "-C", str(repo), "cat-file", "commit", "HEAD"], env=env, text=True)
            self.assertIn("author Sum Test <sum-test@example.invalid>", commit)
            self.assertIn("committer Sum Test <sum-test@example.invalid>", commit)
            self.assertNotIn("gpgsig", commit)
            tagged = subprocess.run([sys.executable, str(WRAPPER), "--", "git", "-C", str(repo), "tag", "-a", "fixture", "-m", "fixture"], env=env, cwd=repo, capture_output=True, text=True)
            self.assertEqual(tagged.returncode, 0, tagged.stderr)
            tag = subprocess.check_output(["git", "-C", str(repo), "cat-file", "tag", "fixture"], env=env, text=True)
            self.assertNotIn("BEGIN PGP SIGNATURE", tag)
            self.assertEqual(hostile.read_bytes(), before)
            with patch.dict(os.environ, env, clear=True):
                isolated = module.environment(root)
                self.assertEqual(os.environ, env)
            self.assertNotIn("GIT_CONFIG_COUNT", isolated)
            self.assertNotIn("GIT_CONFIG_KEY_0", isolated)
            self.assertNotIn("GIT_AUTHOR_NAME", isolated)
            self.assertEqual(isolated["GIT_CONFIG_NOSYSTEM"], "1")
            config = Path(isolated["GIT_CONFIG_GLOBAL"])
            self.assertEqual(config.stat().st_mode & 0o777, 0o600)
            self.assertEqual(config.parent.stat().st_mode & 0o777, 0o700)

    def test_child_failure_is_preserved(self):
        result = subprocess.run([sys.executable, str(WRAPPER), "--", sys.executable, "-c", "raise SystemExit(37)"])
        self.assertEqual(result.returncode, 37)


if __name__ == "__main__":
    unittest.main()
