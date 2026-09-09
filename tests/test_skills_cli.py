from __future__ import annotations

import argparse
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
SUMCTL = ROOT / "lib/sumctl.py"
spec = importlib.util.spec_from_file_location("sumctl_skills", SUMCTL)
sumctl = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(sumctl)


class SkillsCliTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="sum-skills-cli-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.project = self.root / "project"
        self.project.mkdir()
        sumctl.run(["git", "-C", self.project, "init", "-q"])
        self.log = self.root / "skills.json"
        self.fake = self.root / "skills"
        self.fake.write_text(
            "#!/usr/bin/env python3\n"
            "import json, os, pathlib, sys\n"
            "if sys.argv[1:] == ['--version']:\n"
            "    print('1.5.25')\n"
            "else:\n"
            "    pathlib.Path(os.environ['FAKE_SKILLS_LOG']).write_text(json.dumps({'argv': sys.argv[1:], 'cwd': os.getcwd()}))\n"
        )
        self.fake.chmod(0o755)

    def args(self, *extra: str) -> argparse.Namespace:
        return sumctl.parser().parse_args(
            [
                "skills",
                "install",
                "--target",
                str(self.project),
                "--source",
                "owner/repository",
                "--skill",
                "alpha",
                "--agent",
                "codex",
                *extra,
            ]
        )

    def test_project_install_invokes_pinned_skills_with_copy_and_explicit_selection(self) -> None:
        args = self.args("--skill", "beta", "--agent", "claude-code")

        with mock.patch.object(sumctl, "SKILLS_BIN", self.fake), mock.patch.dict(
            os.environ, {"FAKE_SKILLS_LOG": str(self.log)}
        ):
            result = sumctl.install_project_skills(args)

        call = json.loads(self.log.read_text())
        self.assertEqual(Path(call["cwd"]).resolve(), self.project.resolve())
        self.assertEqual(
            call["argv"],
            [
                "add",
                "owner/repository",
                "--skill",
                "alpha",
                "beta",
                "--agent",
                "codex",
                "claude-code",
                "--copy",
                "--yes",
            ],
        )
        self.assertEqual((result["scope"], result["mode"]), ("project", "copy"))

    def test_project_install_rejects_reserved_or_wildcard_skill_before_launch(self) -> None:
        for name in ("sum-worker", "*", "--global"):
            with self.subTest(name=name):
                args = self.args()
                args.skill = [name]
                with mock.patch.object(sumctl, "SKILLS_BIN", self.fake), self.assertRaises(sumctl.SumError):
                    sumctl.install_project_skills(args)

        self.assertFalse(self.log.exists())

    def test_project_install_rejects_option_looking_source_before_launch(self) -> None:
        args = self.args()
        args.source = "--global"

        with mock.patch.object(sumctl, "SKILLS_BIN", self.fake), self.assertRaises(sumctl.SumError):
            sumctl.install_project_skills(args)

        self.assertFalse(self.log.exists())

    def test_project_install_requires_the_target_to_be_a_git_project_root(self) -> None:
        nested = self.project / "nested"
        nested.mkdir()
        args = self.args()
        args.target = str(nested)

        with mock.patch.object(sumctl, "SKILLS_BIN", self.fake), self.assertRaises(sumctl.SumError):
            sumctl.install_project_skills(args)

        self.assertFalse(self.log.exists())


if __name__ == "__main__":
    unittest.main()
