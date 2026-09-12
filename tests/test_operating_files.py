"""Operating files that must not depend on the deleted Python CLI."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
FORBIDDEN = re.compile(r"\b(consigliere|capo|soldier|crewmate)\b|first mate|root session", re.I)


class OperatingFilesTest(unittest.TestCase):
    def test_grok_bot_templates_exist_and_name_sumctl(self):
        for name in ("sum.md", "project.md"):
            path = ROOT / "templates" / "grok-bot" / name
            self.assertTrue(path.is_file(), path)
            text = path.read_text()
            self.assertIn("sumctl", text)
            self.assertNotIn("lib/sumctl.py", text)

    def test_operating_files_reject_themed_role_titles(self):
        for path in (
            ROOT / "AGENTS.md",
            ROOT / "README.md",
            ROOT / "CONTRIBUTING.md",
            ROOT / "templates" / "task.md",
            ROOT / "templates" / "grok-bot" / "sum.md",
        ):
            hits = FORBIDDEN.findall(path.read_text())
            self.assertEqual(hits, [], path)


if __name__ == "__main__":
    unittest.main()
