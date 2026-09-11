from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import unittest


ROOT = Path(__file__).resolve().parents[1]
TEMPLATES = ROOT / "templates" / "grok-bot"
SUM = TEMPLATES / "sum.md"
PROJECT = TEMPLATES / "project.md"
FIELDS = TEMPLATES / "fields.md"
CONTRIBUTING = ROOT / "CONTRIBUTING.md"

spec = importlib.util.spec_from_file_location("sumctl", ROOT / "lib/sumctl.py")
sumctl = importlib.util.module_from_spec(spec)
sys.modules["sumctl"] = sumctl
spec.loader.exec_module(sumctl)


class GrokBotTemplatesTest(unittest.TestCase):
    def test_role_files_exist_and_name_real_helper_commands(self):
        self.assertTrue(SUM.is_file(), SUM)
        self.assertTrue(PROJECT.is_file(), PROJECT)
        sum_text = SUM.read_text()
        project_text = PROJECT.read_text()
        for command in ("sumctl ask", "sumctl answer", "sumctl report", "sumctl verify"):
            self.assertIn(command, sum_text)
        self.assertIn("sumctl ask", project_text)
        self.assertIn("sumctl report", project_text)
        self.assertNotIn("sumctl answer", project_text)
        for text in (sum_text, project_text):
            self.assertNotIn("gh pr merge", text)
            self.assertNotIn("sumctl merge", text)
        self.assertIn("The human merges.", sum_text)
        commands = sumctl.help_view(sumctl.parser())["commands"]
        self.assertIn("ask", commands)

    def test_contributing_requires_grok_bot_impact_check(self):
        text = CONTRIBUTING.read_text()
        self.assertIn("templates/grok-bot", text)
        self.assertIn("Grok Bot", text)

    def test_fields_record_observed_share_page_without_guessing_hidden_controls(self):
        text = FIELDS.read_text()
        self.assertIn("__4FfrkUdvpdMk6-LKg5r", text)
        self.assertIn("Skills, routines, and requested tools were not in the public HTML.", text)


if __name__ == "__main__":
    unittest.main()
