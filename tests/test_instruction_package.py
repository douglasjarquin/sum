from pathlib import Path
import re
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]
SKILLS = ("sum-work", "sum-review", "sum-finish", "sum-resume", "sum-develop")


class InstructionPackageTest(unittest.TestCase):
    def test_discoverable_operator_and_maintainer_procedures(self):
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
        documents += list((ROOT / "templates").glob("*.md"))
        documents += [ROOT / "docs/verification/issue-348.md"]
        for document in documents:
            for target in re.findall(r"\]\(([^)]+)\)", document.read_text()):
                if re.match(r"^[a-z]+:", target) or target.startswith("#"):
                    continue
                path = target.split("#", 1)[0]
                self.assertTrue((document.parent / path).exists(), (document, target))

    def test_delivery_template_discoverable_from_harness_procedures(self):
        expected = ROOT / "templates/pull-request.md"
        for harness in (".agents", ".claude"):
            for name in ("sum-work", "sum-finish", "sum-develop"):
                procedure = (ROOT / harness / "skills" / name / "SKILL.md").resolve()
                targets = re.findall(r"\]\(([^)]+)\)", procedure.read_text())
                templates = [(procedure.parent / target).resolve() for target in targets
                             if target.endswith("/pull-request.md")]
                self.assertEqual(templates, [expected], (harness, name))
                self.assertTrue(templates[0].is_file())

    def test_delivery_template_has_review_and_check_accounting(self):
        template = (ROOT / "templates/pull-request.md").read_text()
        headings = re.findall(r"^## (.+)$", template, re.M)
        self.assertEqual(headings, ["Behavior", "Independent review", "Pipeline",
                                    "Before and after", "Remaining limitations"])
        header = next(line for line in template.splitlines() if line.startswith("| Stage"))
        self.assertEqual([cell.strip() for cell in header.split("|")[1:-1]],
                         ["Stage", "Status", "Result"])
        for status in ("✅ Passed", "❌ Failed", "⛔ Blocked", "⬜ Not run", "➖ Not applicable"):
            self.assertIn(status, template)

    def test_excluded_contracts_match_immutable_baseline(self):
        baseline = "9456017f2e4fde8f16bc4f2d1bdd7ba978765eb3"
        pinned = ["skills/sum-factory/factories/copy.md", "skills/sum-factory/factories/software.md"]
        baseline_bots = subprocess.check_output(["git", "ls-tree", "-r", "--name-only", baseline, "--", "grok-bots"], cwd=ROOT, text=True).splitlines()
        candidate_bots = subprocess.check_output(["git", "ls-files", "sum", "square"], cwd=ROOT, text=True).splitlines()
        moved_baseline = {path.removeprefix("grok-bots/") for path in baseline_bots}
        moved_baseline.discard("README.md")
        self.assertEqual(set(candidate_bots), moved_baseline | {"sum/skills/getting-started/SKILL.md"})
        authorized_grok_text = {
            "GROK_SUM.md", "GROK_SQUARE.md",
            "sum/README.md", "sum/instructions.md",
            "sum/memories.md", "sum/worker-procedure.md",
            "sum/permissions.md", "sum/action-log.md",
            "sum/routines.md",
            "square/README.md", "square/instructions.md",
            "square/memories.md", "square/worker-procedure.md",
            "square/routines.md",
            *("sum/skills/" + name + "/SKILL.md" for name in
              ("dispatch", "persist", "verify", "status", "recap", "deliver", "sweep", "factory")),
        }
        replaced_sum_avatar = {"sum/avatar.jpg"}
        paths = [
            path for path in pinned + list(moved_baseline)
            if path not in authorized_grok_text and path not in replaced_sum_avatar
        ]
        for path in paths:
            baseline_path = path if path in pinned else "grok-bots/" + path
            original = subprocess.check_output(["git", "show", baseline + ":" + baseline_path], cwd=ROOT)
            self.assertEqual((ROOT / path).read_bytes(), original, path)


if __name__ == "__main__":
    unittest.main()
