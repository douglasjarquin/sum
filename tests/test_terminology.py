from __future__ import annotations

import importlib.util
from pathlib import Path
import re
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("sumctl", ROOT / "lib/sumctl.py")
sumctl = importlib.util.module_from_spec(spec)
sys.modules["sumctl"] = sumctl
spec.loader.exec_module(sumctl)

# Operating files and generated samples only. ATTRIBUTIONS.md, CLI identifiers,
# JSON keys, and historical Firstmate/Consigliere names are outside this check.
OPERATING = (
    ROOT / "AGENTS.md",
    ROOT / "README.md",
    ROOT / "CONTRIBUTING.md",
    ROOT / "templates" / "task.md",
    ROOT / "templates" / "preferences.md",
    *sorted((ROOT / "skills").glob("sum-*/SKILL.md")),
    *sorted((ROOT / "templates" / "grok-bot").glob("*.md")),
)

FORBIDDEN = (
    re.compile(r"you are the user's consigliere", re.I),
    re.compile(r"the user is the boss", re.I),
    re.compile(r"\bconsigliere\b", re.I),
    re.compile(r"\bboss\b", re.I),
    re.compile(r"\bcapo\b", re.I),
    re.compile(r"\bsoldier\b", re.I),
    re.compile(r"\bcrewmate\b", re.I),
    re.compile(r"first mate", re.I),
    re.compile(r"root session", re.I),
)

GLOSSARY = ROOT / "docs" / "TERMINOLOGY.md"


def hits(text):
    found = []
    for pattern in FORBIDDEN:
        match = pattern.search(text)
        if match:
            found.append(f"{pattern.pattern}: {match.group(0)}")
    return found


class TerminologyTest(unittest.TestCase):
    def test_seeded_regression_phrases_are_detected(self):
        self.assertTrue(hits("You are the user's consigliere"))
        self.assertTrue(hits("The user is the boss"))
        self.assertTrue(hits("Boss, the soldier needs your call."))
        self.assertTrue(hits("The crewmate finished."))
        self.assertTrue(hits("The root session verifies it."))
        self.assertEqual(hits("The coordinator runs verification independently."), [])

    def test_glossary_exists_and_is_linked(self):
        self.assertTrue(GLOSSARY.is_file(), GLOSSARY)
        glossary = GLOSSARY.read_text()
        self.assertIn("**coordinator**", glossary)
        self.assertIn("**worker agent**", glossary)
        self.assertIn("docs/TERMINOLOGY.md", (ROOT / "AGENTS.md").read_text())
        self.assertIn("docs/TERMINOLOGY.md", (ROOT / "README.md").read_text())
        self.assertIn("docs/TERMINOLOGY.md", (ROOT / "CONTRIBUTING.md").read_text())

    def test_operating_files_reject_themed_role_titles(self):
        missing = [str(path) for path in OPERATING if not path.is_file()]
        self.assertEqual(missing, [])
        failures = []
        for path in OPERATING:
            found = hits(path.read_text())
            if found:
                failures.append(f"{path.relative_to(ROOT)}: {found}")
        self.assertEqual(failures, [])

    def test_generated_brief_and_contract_reject_themed_role_titles(self):
        with tempfile.TemporaryDirectory(prefix="sum-terminology-") as directory:
            store = sumctl.Store(Path(directory) / "state")
            store.init()
            task = {
                "id": "t-aaaaaaaaaaaa",
                "brief": "Do the approved work.",
                "repository": "owner/repo",
                "worktree": str(Path(directory) / "work"),
                "base_sha": "0" * 40,
                "branch": "sum/t-aaaaaaaaaaaa",
                "kind": "ship",
                "harness": "codex",
            }
            commands = {
                "ask": "sumctl ask",
                "show": "sumctl show",
                "resolve": "sumctl resolve",
                "report": "sumctl report",
                "context": "sumctl context",
            }
            brief = sumctl.render_brief(store, task, "r1", sumctl.brief_policy(), [], commands)
            contract = sumctl.render_contract(store, "r1", sumctl.contract_policy(), ["initial contract snapshot"])
        self.assertIn("not the coordinator", brief)
        self.assertNotIn("consigliere", brief.lower())
        self.assertEqual(hits(brief), [], brief[:400])
        self.assertEqual(hits(contract), [], contract[:400])
        self.assertIn((ROOT / "AGENTS.md").read_text(), contract)

    def test_role_contract_and_answer_help_reject_themed_role_titles(self):
        for role, lines in sumctl.ROLE_CONTRACT.items():
            found = hits("\n".join(lines))
            self.assertEqual(found, [], role)
        help_text = sumctl.help_view(sumctl.parser())["commands"]["answer"]
        self.assertEqual(hits(help_text), [], help_text)
        self.assertIn("user's actual decision", help_text)


if __name__ == "__main__":
    unittest.main()
