"""Exercise the sum-verify release build guard that the tag-only release workflow runs."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "build-sum-verify-release.sh"
WORKFLOW = ROOT / ".github" / "workflows" / "release-sum-verify.yml"
TARGETS = ("darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64")

# Stand-in go: reports FAKE_GOVERSION as the build Go and FAKE_EMBEDDED as the
# version embedded in each binary, and records the GOTOOLCHAIN it was given.
FAKE_GO = """#!/usr/bin/env bash
set -euo pipefail
echo "${GOTOOLCHAIN-unset}" >> "$FAKE_LOG"
case "$1" in
  env) echo "$FAKE_GOVERSION" ;;
  build) while [ "$1" != "-o" ]; do shift; done; echo binary > "$2" ;;
  version) shift; for f in "$@"; do echo "$f: ${FAKE_EMBEDDED:-$FAKE_GOVERSION}"; done ;;
  *) exit 2 ;;
esac
"""


def pinned_go():
    return subprocess.run(
        ["mise", "config", "get", "--file", str(ROOT / "mise.toml"), "tools.go"],
        check=True, capture_output=True, text=True,
    ).stdout.strip()


class ReleaseBuildGuardTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="release-build-"))
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.pin = pinned_go()
        self.repo = self.tmp / "repo"
        (self.repo / "scripts").mkdir(parents=True)
        (self.repo / "tools" / "verify").mkdir(parents=True)
        shutil.copy2(SCRIPT, self.repo / "scripts" / SCRIPT.name)
        shutil.copy2(ROOT / "mise.toml", self.repo / "mise.toml")
        fake_bin = self.tmp / "fake-bin"
        fake_bin.mkdir()
        (fake_bin / "go").write_text(FAKE_GO)
        (fake_bin / "go").chmod(0o755)
        self.log = self.tmp / "go.log"
        self.env = {
            **os.environ,
            "PATH": f"{fake_bin}{os.pathsep}{os.environ['PATH']}",
            "FAKE_LOG": str(self.log),
            "FAKE_GOVERSION": f"go{self.pin}",
            "MISE_TRUSTED_CONFIG_PATHS": str(self.repo),
        }
        self.out = self.tmp / "out dir"

    def build(self, script=None, **env):
        return subprocess.run(
            ["bash", str(script or self.repo / "scripts" / SCRIPT.name), "1.2.3", str(self.out)],
            env={**self.env, **env}, capture_output=True, text=True,
        )

    def assert_refused(self, result, message):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stderr)
        self.assertFalse((self.out / "checksums.txt").exists())

    def test_pin_is_exact(self):
        self.assertRegex(self.pin, r"^\d+\.\d+\.\d+$")

    def test_matching_go_builds_every_target_with_checksums(self):
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stderr)
        checksums = (self.out / "checksums.txt").read_text()
        for target in TARGETS:
            self.assertIn(f"sum-verify-{target}", checksums)
            self.assertIn(f"sum-verify-{target}: go{self.pin}", result.stdout)

    def test_older_go_first_on_path_is_refused(self):
        self.assert_refused(self.build(FAKE_GOVERSION="go1.27.1"), f"build Go go1.27.1 is not the mise-pinned go{self.pin}")
        self.assertFalse(self.out.exists())

    def test_mise_go_override_does_not_lower_the_expected_version(self):
        result = self.build(FAKE_GOVERSION="go1.27.1", MISE_GO_VERSION="1.27.1")
        self.assert_refused(result, f"is not the mise-pinned go{self.pin}")

    def test_incoming_gotoolchain_is_neutralized(self):
        self.assertEqual(self.build(GOTOOLCHAIN="go1.27.1").returncode, 0)
        self.assertEqual(set(self.log.read_text().split()), {"local"})

    def test_binary_embedding_another_go_is_refused(self):
        self.assert_refused(self.build(FAKE_EMBEDDED="go1.27.1"), f"embeds go1.27.1, want go{self.pin}")

    def test_floating_or_missing_pin_is_refused(self):
        for toml in (
            '[tools]\ngo = "1.27"\n',
            '[tools]\npython = "3.13.5"\n',
            f'[tools]\ngo = "{self.pin}\\n"\n',
            f'[tools]\ngo = "{self.pin}\\n\\n"\n',
        ):
            with self.subTest(toml=toml):
                (self.repo / "mise.toml").write_text(toml)
                result = self.build()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("mise.toml", result.stderr)
                self.assertFalse(self.out.exists())

    def test_pin_comes_from_toml_structure_not_text(self):
        # A multiline task string holding a fake [tools] table must not be read as the pin.
        (self.repo / "mise.toml").write_text(
            f'[tools] # pinned\ngo = "{self.pin}"\n\n[tasks.x]\nrun = """\n[tools]\ngo = "1.27.1"\n"""\n'
        )
        self.assert_refused(self.build(FAKE_GOVERSION="go1.27.1", MISE_GO_VERSION="1.27.1"), f"go{self.pin}")

    def test_real_pinned_go_ignores_incoming_overrides(self):
        real_env = {**os.environ, "GOTOOLCHAIN": "go1.27.1", "MISE_GO_VERSION": "1.27.1"}
        result = subprocess.run(["bash", str(SCRIPT), "1.2.3", str(self.out)], env=real_env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        for target in TARGETS:
            self.assertIn(f"sum-verify-{target}: go{self.pin}", result.stdout)

    def test_release_workflow_builds_through_the_guarded_script(self):
        workflow = WORKFLOW.read_text()
        self.assertIn("scripts/build-sum-verify-release.sh", workflow)
        self.assertNotIn("go build", workflow)
        self.assertNotIn("mise current", workflow)


if __name__ == "__main__":
    unittest.main()
