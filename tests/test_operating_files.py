"""Operating files keep the Group 1 dictionary and a native Grok Bot recipe."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
FORBIDDEN = re.compile(
    r"\b(consigliere|capo|soldier|crewmate|charter)\b|first mate|root session",
    re.I,
)
GROK_BOT = ROOT / "templates" / "grok-bot"
RECIPE_FILES = (
    "README.md",
    "instructions.md",
    "memories.md",
    "routines.md",
    "worker-procedure.md",
    "skills/dispatch/SKILL.md",
    "skills/persist/SKILL.md",
    "skills/verify/SKILL.md",
    "skills/rundown/SKILL.md",
    "skills/deliver/SKILL.md",
)
SKILL_HEADINGS = (
    "## When to use it",
    "## Required inputs and access",
    "## Sequence of work",
    "## How to validate the result",
    "## What to return",
    "## What requires approval",
)
REMOVED_BINDINGS = ("sum.md", "project.md", "fields.md")


def _grok_bot_markdown():
    return sorted(path for path in GROK_BOT.rglob("*.md") if path.is_file())


class OperatingFilesTest(unittest.TestCase):
    def test_grok_bot_recipe_files_exist(self):
        for name in RECIPE_FILES:
            path = GROK_BOT / name
            self.assertTrue(path.is_file(), path)
        self.assertTrue((ROOT / "GROK_SUM.md").is_file())

    def test_grok_sum_installer_clones_public_sum(self):
        text = (ROOT / "GROK_SUM.md").read_text()
        self.assertIn("This file is an installer.", text)
        self.assertIn("https://github.com/douglasjarquin/sum.git", text)
        self.assertIn("/home/box/agent-data/sum/src/", text)
        for skill in ("Dispatch", "Persist", "Verify", "Rundown", "Deliver"):
            self.assertIn(skill, text)
        self.assertNotIn("Paste `templates/grok-bot/instructions.md`", text)
        self.assertNotIn("Save each skill from `skills/`", text)
        self.assertNotIn("sumctl", text)

    def test_grok_bot_readme_does_not_ask_for_a_hand_paste(self):
        text = (GROK_BOT / "README.md").read_text()
        self.assertIn("GROK_SUM.md", text)
        self.assertNotIn("What you paste", text)
        self.assertNotIn("Paste `instructions.md`", text)

    def test_removed_helper_binding_files_are_gone(self):
        for name in REMOVED_BINDINGS:
            path = GROK_BOT / name
            self.assertFalse(path.exists(), path)

    def test_grok_bot_recipe_is_not_a_helper_binding(self):
        self.assertTrue(GROK_BOT.is_dir(), GROK_BOT)
        paths = [*_grok_bot_markdown(), ROOT / "GROK_SUM.md"]
        for path in paths:
            text = path.read_text()
            self.assertNotIn("sumctl", text, path)
            self.assertNotIn("lib/sumctl.py", text, path)
            self.assertNotIn("__4FfrkUdvpdMk6-LKg5r", text, path)

    def test_grok_bot_instructions_encode_the_operating_contract(self):
        text = (GROK_BOT / "instructions.md").read_text()
        self.assertIn("Never do the requested work in this chat", text)
        self.assertIn("Research, planning, investigation, and implementation", text)
        self.assertIn("The user merges", text)
        self.assertIn("A worker result is a claim", text)
        self.assertIn("/workspace/sum", text)
        self.assertIn("coordinator", text.lower())

    def test_agents_md_still_forbids_coordinator_pane_work(self):
        text = (ROOT / "AGENTS.md").read_text()
        self.assertIn(
            "Never do the requested work in this coordinator pane",
            text,
        )
        self.assertIn(
            "not research, not planning, not investigation, not implementation",
            text,
        )

    def test_grok_bot_skills_state_the_six_fields(self):
        for name in RECIPE_FILES:
            if not name.startswith("skills/"):
                continue
            text = (GROK_BOT / name).read_text()
            for heading in SKILL_HEADINGS:
                self.assertIn(heading, text, f"{name} missing {heading}")

    def test_grok_bot_worker_procedure_stays_a_worker(self):
        text = (GROK_BOT / "worker-procedure.md").read_text()
        self.assertIn("You are not the coordinator", text)
        self.assertIn("The user merges", text)
        self.assertIn("Do not message the user", text)
        self.assertIn("Write the commands and exit results into the report", text)

    def test_grok_bot_verify_does_not_drive_cloud_agents(self):
        text = (GROK_BOT / "skills" / "verify" / "SKILL.md").read_text()
        self.assertIn("Do not call a Cursor Cloud Agent", text)
        persist = (GROK_BOT / "skills" / "persist" / "SKILL.md").read_text()
        self.assertIn("Do not author or overwrite that claim", persist)

    def test_operating_files_reject_themed_role_titles(self):
        paths = [
            ROOT / "AGENTS.md",
            ROOT / "README.md",
            ROOT / "CONTRIBUTING.md",
            ROOT / "GROK_SUM.md",
            ROOT / "templates" / "task.md",
            *_grok_bot_markdown(),
        ]
        for path in paths:
            hits = FORBIDDEN.findall(path.read_text())
            self.assertEqual(hits, [], path)


if __name__ == "__main__":
    unittest.main()
