from pathlib import Path
import re
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]
SKILLS = ("sum-work", "sum-review", "sum-finish", "sum-resume")


class InstructionPackageTest(unittest.TestCase):
    def test_four_discoverable_core_procedures(self):
        for name in SKILLS:
            expected = ROOT / "skills" / name / "SKILL.md"
            self.assertTrue(expected.is_file(), expected)
            for harness in (".agents", ".claude"):
                discovered = ROOT / harness / "skills" / name / "SKILL.md"
                self.assertEqual(discovered.resolve(), expected.resolve())
        core = {p.name for p in (ROOT / ".agents/skills").glob("sum-*")}
        self.assertEqual(core, set(SKILLS))

    def test_ordinary_entry_has_no_runtime_installation(self):
        self.assertFalse((ROOT / "bin/sumctl").exists())
        self.assertFalse((ROOT / "go/cmd").exists())
        entry = (ROOT / "AGENTS.md").read_text()
        self.assertNotIn("sumctl", entry)
        for name in SKILLS:
            self.assertIn("skills/" + name + "/SKILL.md", entry)
        for pointer in ("CLAUDE.md", "GEMINI.md", "GROK.md"):
            self.assertEqual((ROOT / pointer).resolve(), ROOT / "AGENTS.md")

    def test_active_documentation_links_resolve(self):
        documents = [ROOT / name for name in ("AGENTS.md", "README.md", "CONTRIBUTING.md", "VERIFY.md")]
        documents += list((ROOT / "docs").glob("*.md"))
        documents += list((ROOT / "skills").glob("*/SKILL.md"))
        documents += [ROOT / "docs/verification/issue-348.md"]
        for document in documents:
            for target in re.findall(r"\]\(([^)]+)\)", document.read_text()):
                if re.match(r"^[a-z]+:", target) or target.startswith("#"):
                    continue
                path = target.split("#", 1)[0]
                self.assertTrue((document.parent / path).exists(), (document, target))

    def test_excluded_contracts_match_immutable_baseline(self):
        baseline = "9456017f2e4fde8f16bc4f2d1bdd7ba978765eb3"
        paths = ["GROK_SUM.md", "GROK_SQUARE.md", "skills/sum-factory/factories/copy.md", "skills/sum-factory/factories/software.md"]
        paths += subprocess.check_output(["git", "ls-files", "grok-bots"], cwd=ROOT, text=True).splitlines()
        for path in paths:
            original = subprocess.check_output(["git", "show", baseline + ":" + path], cwd=ROOT)
            self.assertEqual((ROOT / path).read_bytes(), original, path)


if __name__ == "__main__":
    unittest.main()
