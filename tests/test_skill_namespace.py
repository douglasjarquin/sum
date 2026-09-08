from __future__ import annotations

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


class SkillNamespaceTest(unittest.TestCase):
    def test_role_skill_references_use_prefixed_canonical_directories(self):
        expected = ["sum-worker", "sum-delivery", "sum-rundown", "sum-dispatch"]
        references = sumctl.skill_references(["worker", "reviewer", "coordinator"])

        self.assertEqual([row["skill"] for row in references["files"]], expected)
        self.assertTrue(all(Path(row["path"]).parent.name.startswith("sum-") for row in references["files"]))

    def test_worker_skill_reads_canonical_runtime_and_old_path_remains_usable(self):
        with tempfile.TemporaryDirectory(prefix="sum-skill-runtime-") as directory:
            runtime = Path(directory)
            source = ROOT / "skills" / "worker" / "SKILL.md"
            canonical = runtime / "skills" / "sum-worker" / "SKILL.md"
            canonical.parent.mkdir(parents=True)
            shutil.copyfile(source, canonical)

            with mock.patch.object(sumctl, "RUNTIME", runtime):
                self.assertIn("sum-worker", sumctl.worker_skill())

            legacy = runtime / "skills" / "worker" / "SKILL.md"
            legacy.parent.mkdir(parents=True)
            shutil.copyfile(source, legacy)
            canonical.unlink()
            canonical.parent.rmdir()
            with mock.patch.object(sumctl, "RUNTIME", runtime):
                self.assertIn("sum-worker", sumctl.worker_skill())

    def test_skill_inventory_accepts_sources_and_rejects_namespace_collision(self):
        with tempfile.TemporaryDirectory(prefix="sum-skill-inventory-") as directory:
            root = Path(directory)
            for name in ("skills", ".agents/skills", ".claude/skills"):
                shutil.copytree(ROOT / name, root / name, symlinks=True)

            clean = sumctl.skill_inventory(root)
            self.assertTrue(clean["ok"], clean)
            self.assertEqual(clean["active"], ["sum-delivery", "sum-develop", "sum-dispatch", "sum-rundown", "sum-update", "sum-worker"])

            collision = root / ".agents/skills/sum-external"
            collision.symlink_to("../../skills/sum-worker")
            rejected = sumctl.skill_inventory(root)
            self.assertFalse(rejected["ok"], rejected)
            self.assertIn("sum-external", " ".join(rejected["errors"]))
            self.assertTrue((root / ".agents/skills/sum-worker").is_symlink())


if __name__ == "__main__":
    unittest.main()
