from __future__ import annotations

import json
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SUMCTL = ROOT / "lib" / "sumctl.py"


class SkillInstallRegressionTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="sum-skill-regression-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / "source"
        self.target = self.root / "target"
        self.source.mkdir()
        self.target.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "regression fixture")
        self.git("config", "user.email", "regression@example.invalid")
        (self.source / "LICENSE").write_text("license\n")

    def git(self, *args: str) -> str:
        return subprocess.run(
            ["git", "-C", str(self.source), *args],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()

    def commit(self) -> str:
        self.git("add", ".")
        self.git("commit", "-q", "-m", "fixture")
        return self.git("rev-parse", "HEAD")

    def skill(self, body: str, path: str = "skills/alpha") -> Path:
        directory = self.source / path
        directory.mkdir(parents=True)
        (directory / "SKILL.md").write_text(
            f"---\nname: alpha\ndescription: alpha\n---\n\n{body}"
        )
        return directory

    def cli(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SUMCTL), "--home", str(self.root / "home"), *args],
            capture_output=True,
            text=True,
        )

    def install(self, ref: str, path: str = "skills/alpha") -> subprocess.CompletedProcess[str]:
        return self.cli(
            "skills",
            "install",
            "--target",
            str(self.target),
            "--selection",
            str(self.source),
            ref,
            path,
        )

    def test_source_contained_symlink_remains_within_installed_snapshot(self) -> None:
        path = "pstack/foo/bar/skills/alpha"
        directory = self.skill("alpha\n", path)
        (directory / "deep").mkdir()
        (directory / "deep/link").symlink_to(
            "../../../../../../pstack/foo/bar/skills/alpha/SKILL.md"
        )
        ref = self.commit()

        installed = self.install(ref, path)

        self.assertEqual(installed.returncode, 0, installed.stderr)
        snapshot = next((self.target / ".sum-skills/snapshots").iterdir())
        link = snapshot / "skill/alpha/deep/link"
        self.assertTrue(link.resolve().is_relative_to(snapshot.resolve()))
        self.assertEqual(link.read_text(), (directory / "SKILL.md").read_text())

    def test_reference_style_missing_markdown_resource_is_refused(self) -> None:
        self.skill("See [the required guide][guide].\n\n[guide]: references/missing.md\n")
        ref = self.commit()

        installed = self.install(ref)

        self.assertNotEqual(installed.returncode, 0)
        self.assertIn("missing explicit resource", installed.stderr)
        self.assertFalse((self.target / ".agents/skills/alpha").exists())

    def test_check_rejects_tampered_aggregate_content_hash(self) -> None:
        self.skill("alpha\n")
        ref = self.commit()
        installed = self.install(ref)
        self.assertEqual(installed.returncode, 0, installed.stderr)
        lock_path = self.target / ".sum-skills/selection-lock.json"
        lock = json.loads(lock_path.read_text())
        lock["selections"][0]["content_sha256"] = "0" * 64
        lock_path.write_text(json.dumps(lock))

        checked = self.cli("skills", "check", "--root", str(self.target))

        self.assertNotEqual(checked.returncode, 0)
        self.assertIn("content hash mismatch", checked.stdout)

    def test_repeat_install_reuses_unchanged_symbolic_ref(self) -> None:
        self.skill("alpha\n")
        self.commit()
        first = self.install("main")
        self.assertEqual(first.returncode, 0, first.stderr)

        second = self.install("main")

        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(second.stdout)["reused"], ["alpha"])

    def test_installed_snapshot_tree_is_read_only(self) -> None:
        self.skill("alpha\n")
        ref = self.commit()

        installed = self.install(ref)

        self.assertEqual(installed.returncode, 0, installed.stderr)
        snapshot = next((self.target / ".sum-skills/snapshots").iterdir())
        members = [snapshot, *(path for path in snapshot.rglob("*") if not path.is_symlink())]
        self.assertTrue(members)
        for member in members:
            self.assertEqual(stat.S_IMODE(member.stat().st_mode) & 0o222, 0, member)


if __name__ == "__main__":
    unittest.main()
