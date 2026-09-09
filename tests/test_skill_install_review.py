from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SUMCTL = ROOT / "lib" / "sumctl.py"
spec = importlib.util.spec_from_file_location("sumctl_review", SUMCTL)
sumctl = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(sumctl)


class SkillInstallReviewTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="sum-skill-review-")
        self.root = Path(self.temp.name)
        self.home = self.root / "home"

    def tearDown(self):
        self.temp.cleanup()

    def git(self, repo: Path, *args: str) -> str:
        return subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, text=True).stdout.strip()

    def source(self, name: str = "source", path: str = "skills/alpha", frontmatter: str | None = None) -> tuple[Path, str]:
        repo = self.root / name
        repo.mkdir()
        self.git(repo, "init", "-q", "-b", "main")
        self.git(repo, "config", "user.name", "review fixture")
        self.git(repo, "config", "user.email", "review@example.invalid")
        (repo / "LICENSE").write_text("root license\n")
        skill = repo / path
        skill.mkdir(parents=True)
        skill_file = frontmatter or "---\nname: alpha\ndescription: alpha\n---\n\nalpha\n"
        (skill / "SKILL.md").write_text(skill_file)
        (skill / "run.sh").write_text("#!/bin/sh\nprintf ok\n")
        (skill / "run.sh").chmod(0o755)
        self.git(repo, "add", ".")
        self.git(repo, "commit", "-q", "-m", "fixture")
        return repo, self.git(repo, "rev-parse", "HEAD")

    def cli(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run([sys.executable, str(SUMCTL), "--home", str(self.home), *args], capture_output=True, text=True)

    def test_existing_destination_ancestor_symlink_is_refused(self):
        repo, ref = self.source()
        target = self.root / "target"
        target.mkdir()
        outside = self.root / "outside"
        (outside / "skills").mkdir(parents=True)
        (target / ".agents").symlink_to(outside)

        result = self.cli("skills", "install", "--target", str(target), "--selection", str(repo), ref, "skills/alpha")

        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((outside / "skills" / "alpha").is_symlink())

    def test_selected_resources_and_governing_ancestor_notices_keep_relative_paths(self):
        repo, ref = self.source(path="pstack/skills/alpha")
        skill = repo / "pstack/skills/alpha"
        (skill / "licenses").mkdir()
        (skill / "licenses/detail.txt").write_text("skill notice\n")
        (repo / "pstack/LICENSE").write_text("pstack license\n")
        self.git(repo, "add", ".")
        self.git(repo, "commit", "-q", "-m", "resources")
        ref = self.git(repo, "rev-parse", "HEAD")
        target = self.root / "target"
        target.mkdir()

        result = self.cli("skills", "install", "--target", str(target), "--selection", str(repo), ref, "pstack/skills/alpha")

        self.assertEqual(result.returncode, 0, result.stderr)
        snapshot = next((target / ".sum-skills/snapshots").iterdir())
        self.assertTrue((snapshot / "skill/alpha/licenses/detail.txt").is_file())
        self.assertFalse((snapshot / "licenses/detail.txt").exists())
        self.assertTrue((snapshot / "licenses/LICENSE").is_file())
        self.assertTrue((snapshot / "licenses/pstack/LICENSE").is_file())

    def test_reuse_checks_bytes_and_modes_before_reporting_reused(self):
        repo, ref = self.source()
        target = self.root / "target"
        target.mkdir()
        first = self.cli("skills", "install", "--target", str(target), "--selection", str(repo), ref, "skills/alpha")
        self.assertEqual(first.returncode, 0, first.stderr)
        snapshot = next((target / ".sum-skills/snapshots").iterdir())
        (snapshot / "skill/alpha/run.sh").write_text("tampered\n")
        (snapshot / "skill/alpha/run.sh").chmod(0o644)

        reused = self.cli("skills", "install", "--target", str(target), "--selection", str(repo), ref, "skills/alpha")
        checked = self.cli("skills", "check", "--root", str(target))

        self.assertNotEqual(reused.returncode, 0)
        self.assertNotEqual(checked.returncode, 0)
        self.assertIn("error", checked.stdout)

    def test_capability_bearing_skill_is_classified_and_not_projected(self):
        frontmatter = "---\nname: alpha\ndescription: alpha\nallowed-tools: Bash\n---\n\nalpha\n"
        repo, ref = self.source(frontmatter=frontmatter)
        inspected = self.cli("skills", "inspect", "--repository", str(repo), "--ref", ref, "--path", "skills/alpha")
        target = self.root / "target"
        target.mkdir()
        installed = self.cli("skills", "install", "--target", str(target), "--selection", str(repo), ref, "skills/alpha")

        self.assertEqual(inspected.returncode, 0, inspected.stderr)
        capabilities = json.loads(inspected.stdout)["capabilities"]
        self.assertFalse(capabilities["native_projection"])
        self.assertIn("allowed-tools", capabilities["unsupported"])
        self.assertNotEqual(installed.returncode, 0)
        self.assertFalse((target / ".agents/skills/alpha").exists())

    def test_folded_yaml_and_malformed_lock_are_structured(self):
        frontmatter = "---\nname: alpha\ndescription: >-\n  folded description\n  continues here\n---\n\nalpha\n"
        repo, ref = self.source(frontmatter=frontmatter)
        inspected = self.cli("skills", "inspect", "--repository", str(repo), "--ref", ref, "--path", "skills/alpha")
        target = self.root / "target"
        target.mkdir()
        installed = self.cli("skills", "install", "--target", str(target), "--selection", str(repo), ref, "skills/alpha")
        (target / ".sum-skills").mkdir(exist_ok=True)
        (target / ".sum-skills/selection-lock.json").write_text("[]\n")
        malformed = self.cli("skills", "check", "--root", str(target))

        self.assertEqual(inspected.returncode, 0, inspected.stderr)
        self.assertEqual(installed.returncode, 0, installed.stderr)
        self.assertNotEqual(malformed.returncode, 0)
        self.assertNotIn("Traceback", malformed.stderr)
        self.assertIn("error", malformed.stdout)

    def test_inventory_validates_selection_lock_during_release_style_checks(self):
        with tempfile.TemporaryDirectory(prefix="sum-skill-inventory-review-") as directory:
            root = Path(directory)
            for name in ("skills", ".agents/skills", ".claude/skills"):
                shutil.copytree(ROOT / name, root / name, symlinks=True)
            store = root / ".sum-skills"
            store.mkdir()
            (store / "selection-lock.json").write_text("[]\n")

            inventory = sumctl.skill_inventory(root)

            self.assertFalse(inventory["ok"])
            self.assertIn("selection lock", " ".join(inventory["errors"]).lower())


if __name__ == "__main__":
    unittest.main()
