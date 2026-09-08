from __future__ import annotations

import json
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class SkillInstallTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="sum-skill-install-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / "source"
        self.target = self.root / "target"
        self.target.mkdir()
        self.git("init", "-q", "-b", "main", cwd=self.source, create=True)
        self.git("config", "user.name", "sum test", cwd=self.source)
        self.git("config", "user.email", "test@example.invalid", cwd=self.source)
        (self.source / "LICENSE").write_text("license\n")
        alpha = self.source / "pstack/skills/alpha"
        alpha.mkdir(parents=True)
        (alpha / "SKILL.md").write_text("---\nname: alpha\ndescription: Alpha skill\n---\n\nUse alpha.\n")
        (alpha / "scripts").mkdir()
        script = alpha / "scripts/run.sh"
        script.write_text("#!/bin/sh\nprintf alpha\n")
        script.chmod(0o755)
        beta = self.source / "pstack/skills/beta"
        beta.mkdir(parents=True)
        (beta / "SKILL.md").write_text("---\nname: beta\n---\n\nUse beta.\n")
        self.git("add", ".", cwd=self.source)
        self.git("commit", "-q", "-m", "fixture", cwd=self.source)
        self.commit = self.git("rev-parse", "HEAD", cwd=self.source)

    def git(self, *args, cwd, create=False):
        if create:
            cwd.mkdir(parents=True)
        return subprocess.run(["git", "-C", str(cwd), *args], check=True, capture_output=True, text=True).stdout.strip()

    def cli(self, *args):
        return subprocess.run(
            [sys.executable, str(ROOT / "lib/sumctl.py"), "--home", str(self.root / "home"), *args],
            capture_output=True,
            text=True,
        )

    def commit_source(self, message):
        self.git("add", ".", cwd=self.source)
        self.git("commit", "-q", "-m", message, cwd=self.source)
        return self.git("rev-parse", "HEAD", cwd=self.source)

    def test_explicit_selection_copies_only_selected_skill_and_records_pin(self):
        result = self.cli(
            "skills",
            "install",
            "--target",
            str(self.target),
            "--selection",
            str(self.source),
            self.commit,
            "pstack/skills/alpha",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        value = json.loads(result.stdout)
        self.assertEqual(value["installed"], ["alpha"])
        self.assertTrue((self.target / ".agents/skills/alpha/SKILL.md").is_file())
        self.assertTrue((self.target / ".agents/skills/alpha/scripts/run.sh").is_file())
        self.assertFalse((self.target / ".agents/skills/beta").exists())
        lock = json.loads((self.target / ".sum-skills/selection-lock.json").read_text())
        selection = lock["selections"][0]
        self.assertTrue((self.target / ".sum-skills" / selection["snapshot"] / "licenses/LICENSE").is_file())
        self.assertEqual((selection["ref"], selection["commit"], selection["path"], selection["name"]),
                         (self.commit, self.commit, "pstack/skills/alpha", "alpha"))
        self.assertTrue(selection["content_sha256"])
        self.assertTrue(selection["origin"].startswith("git:"))
        self.assertEqual(stat.S_IMODE((self.target / ".agents/skills/alpha/scripts/run.sh").stat().st_mode), 0o755)
        checked = self.cli("skills", "check", "--root", str(self.target))
        self.assertEqual(checked.returncode, 0, checked.stderr)
        self.assertEqual(json.loads(checked.stdout)["selected"]["selections"], ["alpha"])
        (self.target / ".sum-skills" / selection["snapshot"] / "skill/alpha/SKILL.md").write_text("tampered\n")
        tampered = self.cli("skills", "check", "--root", str(self.target))
        self.assertNotEqual(tampered.returncode, 0)

    def test_inspect_is_read_only_and_reports_capability_limits(self):
        result = self.cli("skills", "inspect", "--repository", str(self.source), "--ref", self.commit, "--path", "pstack/skills/alpha")

        self.assertEqual(result.returncode, 0, result.stderr)
        value = json.loads(result.stdout)
        self.assertEqual((value["name"], value["commit"], value["snapshot"]), ("alpha", self.commit, "not-installed"))
        self.assertFalse(value["capabilities"]["invocation"])
        self.assertFalse((self.target / ".sum-skills").exists())

    def test_multiple_selections_support_the_other_native_discovery_route(self):
        target = self.root / "claude-target"
        target.mkdir()
        result = self.cli(
            "skills",
            "install",
            "--target",
            str(target),
            "--route",
            ".claude/skills",
            "--selection",
            str(self.source),
            self.commit,
            "pstack/skills/alpha",
            "--selection",
            str(self.source),
            self.commit,
            "pstack/skills/beta",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sorted(json.loads(result.stdout)["installed"]), ["alpha", "beta"])
        self.assertTrue((target / ".claude/skills/alpha/SKILL.md").is_file())
        self.assertTrue((target / ".claude/skills/beta/SKILL.md").is_file())
        self.assertFalse((target / ".agents").exists())

    def test_pinned_repeat_reuses_snapshot_when_source_is_offline(self):
        first = self.cli("skills", "install", "--target", str(self.target), "--selection", str(self.source), self.commit, "pstack/skills/alpha")
        self.assertEqual(first.returncode, 0, first.stderr)
        shutil.rmtree(self.source)

        second = self.cli("skills", "install", "--target", str(self.target), "--selection", str(self.source), self.commit, "pstack/skills/alpha")

        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(second.stdout)["reused"], ["alpha"])

    def test_reserved_name_missing_license_path_escape_and_denied_transport_are_refused(self):
        reserved = self.source / "pstack/skills/sum-evil"
        reserved.mkdir(parents=True)
        (reserved / "SKILL.md").write_text("---\nname: sum-evil\n---\n")
        reserved_commit = self.commit_source("reserved skill")
        reserved_target = self.root / "reserved-target"
        reserved_target.mkdir()
        refused = self.cli("skills", "install", "--target", str(reserved_target), "--selection", str(self.source), reserved_commit, "pstack/skills/sum-evil")
        self.assertNotEqual(refused.returncode, 0)
        escaped = self.cli("skills", "inspect", "--repository", str(self.source), "--ref", self.commit, "--path", "../alpha")
        self.assertNotEqual(escaped.returncode, 0)
        denied = self.cli("skills", "inspect", "--repository", "http://example.invalid/repo.git", "--ref", self.commit, "--path", "pstack/skills/alpha")
        self.assertNotEqual(denied.returncode, 0)

    def test_malformed_frontmatter_missing_license_and_external_resource_are_refused(self):
        malformed = self.source / "pstack/skills/malformed"
        malformed.mkdir(parents=True)
        (malformed / "SKILL.md").write_text("name: malformed\n")
        malformed_commit = self.commit_source("malformed skill")
        malformed_target = self.root / "malformed-target"
        malformed_target.mkdir()
        malformed_result = self.cli("skills", "install", "--target", str(malformed_target), "--selection", str(self.source), malformed_commit, "pstack/skills/malformed")
        self.assertNotEqual(malformed_result.returncode, 0)

        external = self.source / "pstack/skills/external"
        external.mkdir(parents=True)
        (external / "SKILL.md").write_text("---\nname: external\n---\n")
        (self.source / "shared.txt").write_text("outside\n")
        (external / "shared.txt").symlink_to("../../../shared.txt")
        external_commit = self.commit_source("external resource")
        external_target = self.root / "external-target"
        external_target.mkdir()
        external_result = self.cli("skills", "install", "--target", str(external_target), "--selection", str(self.source), external_commit, "pstack/skills/external")
        self.assertNotEqual(external_result.returncode, 0)

        self.source.joinpath("LICENSE").unlink()
        missing_license_commit = self.commit_source("missing license")
        missing_license_target = self.root / "license-target"
        missing_license_target.mkdir()
        missing_license_result = self.cli("skills", "install", "--target", str(missing_license_target), "--selection", str(self.source), missing_license_commit, "pstack/skills/alpha")
        self.assertNotEqual(missing_license_result.returncode, 0)

    def test_duplicate_selection_is_idempotent_and_existing_name_is_not_overwritten(self):
        first = self.cli("skills", "install", "--target", str(self.target), "--selection", str(self.source), self.commit, "pstack/skills/alpha")
        self.assertEqual(first.returncode, 0, first.stderr)
        second = self.cli("skills", "install", "--target", str(self.target), "--selection", str(self.source), self.commit, "pstack/skills/alpha")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(second.stdout)["reused"], ["alpha"])

        collision = self.root / "collision"
        collision.mkdir()
        (collision / ".agents/skills/alpha").mkdir(parents=True)
        marker = collision / ".agents/skills/alpha/SKILL.md"
        marker.write_text("user-owned\n")
        refused = self.cli("skills", "install", "--target", str(collision), "--selection", str(self.source), self.commit, "pstack/skills/alpha")
        self.assertNotEqual(refused.returncode, 0)
        self.assertEqual(marker.read_text(), "user-owned\n")


if __name__ == "__main__":
    unittest.main()
