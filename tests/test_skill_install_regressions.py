from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
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

    def object(self, kind: str, data: bytes) -> str:
        return subprocess.run(
            ["git", "-C", str(self.source), "hash-object", "-w", "--stdin", "-t", kind, "--literally"],
            input=data,
            check=True,
            capture_output=True,
        ).stdout.decode().strip()

    def tree(self, entries: list[tuple[str, str, str]]) -> str:
        data = b"".join(
            f"{mode} {name}".encode() + b"\0" + bytes.fromhex(oid)
            for mode, name, oid in entries
        )
        return self.object("tree", data)

    def skill(self, body: str, path: str = "skills/alpha") -> Path:
        directory = self.source / path
        directory.mkdir(parents=True)
        (directory / "SKILL.md").write_text(
            f"---\nname: alpha\ndescription: alpha\n---\n\n{body}"
        )
        return directory

    def cli(self, *args: str, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SUMCTL), "--home", str(self.root / "home"), *args],
            capture_output=True,
            text=True,
            env=env,
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

    def test_markdown_code_examples_do_not_require_resources(self) -> None:
        self.skill(
            "Use `[guide](references/example.md)` as a literal.\n\n"
            "```markdown\n![preview](assets/example.png)\n```\n"
        )
        ref = self.commit()

        installed = self.install(ref)

        self.assertEqual(installed.returncode, 0, installed.stderr)

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

    def test_unterminated_quoted_frontmatter_scalar_is_refused(self) -> None:
        directory = self.skill("alpha\n")
        (directory / "SKILL.md").write_text(
            '---\nname: alpha\ndescription: "unterminated\n---\n\nalpha\n'
        )
        ref = self.commit()

        inspected = self.cli(
            "skills", "inspect", "--repository", str(self.source),
            "--ref", ref, "--path", "skills/alpha",
        )

        self.assertNotEqual(inspected.returncode, 0)
        self.assertIn("malformed frontmatter", inspected.stderr)

    def test_flow_collection_frontmatter_scalar_is_refused(self) -> None:
        directory = self.skill("alpha\n")
        (directory / "SKILL.md").write_text(
            "---\nname: alpha\ndescription: [unterminated\n---\n\nalpha\n"
        )
        ref = self.commit()

        inspected = self.cli(
            "skills", "inspect", "--repository", str(self.source),
            "--ref", ref, "--path", "skills/alpha",
        )

        self.assertNotEqual(inspected.returncode, 0)
        self.assertIn("malformed frontmatter", inspected.stderr)

    def test_malformed_tree_paths_are_refused_before_materialization(self) -> None:
        skill = self.object(
            "blob", b"---\nname: alpha\ndescription: alpha\n---\n\nalpha\n",
        )
        license_blob = self.object("blob", b"license\n")
        escaped = self.object("blob", b"escaped\n")
        nested = self.tree([("100644", "escaped", escaped)])
        for _ in range(4):
            nested = self.tree([("40000", "..", nested)])
        alpha = self.tree([
            ("40000", "..", nested),
            ("100644", "SKILL.md", skill),
        ])
        skills = self.tree([("40000", "alpha", alpha)])
        root = self.tree([
            ("100644", "LICENSE", license_blob),
            ("40000", "skills", skills),
        ])
        commit = self.object(
            "commit",
            (
                f"tree {root}\n"
                "author fixture <fixture@example.invalid> 0 +0000\n"
                "committer fixture <fixture@example.invalid> 0 +0000\n\n"
                "malformed tree\n"
            ).encode(),
        )

        installed = self.install(commit)

        self.assertNotEqual(installed.returncode, 0)
        self.assertIn("unsafe Git tree path", installed.stderr)
        self.assertFalse((self.target / "escaped").exists())

    def test_git_replacement_objects_do_not_change_pinned_bytes(self) -> None:
        directory = self.skill("original\n")
        original_text = (directory / "SKILL.md").read_text()
        original = self.commit()
        (directory / "SKILL.md").write_text(original_text.replace("original", "replacement"))
        replacement = self.commit()
        self.git("replace", original, replacement)

        installed = self.install(original)

        self.assertEqual(installed.returncode, 0, installed.stderr)
        self.assertEqual((self.target / ".agents/skills/alpha/SKILL.md").read_text(), original_text)

    def test_ref_change_after_resolution_keeps_original_commit_bytes(self) -> None:
        directory = self.skill("original\n")
        original_text = (directory / "SKILL.md").read_text()
        original = self.commit()
        (directory / "SKILL.md").write_text(original_text.replace("original", "replacement"))
        replacement = self.commit()
        self.git("update-ref", "refs/heads/main", original)
        real_git = shutil.which("git")
        self.assertIsNotNone(real_git)
        wrapper_dir = self.root / "bin"
        wrapper_dir.mkdir()
        wrapper = wrapper_dir / "git"
        wrapper.write_text(
            "#!/bin/sh\n"
            f"real_git={real_git!s}\n"
            f"replacement={replacement}\n"
            'if [ "$3" = "rev-parse" ] && [ "$6" = "main^{commit}" ]; then\n'
            '  output=$("$real_git" "$@") || exit $?\n'
            '  "$real_git" -C "$2" update-ref refs/heads/main "$replacement" || exit $?\n'
            "  printf '%s\\n' \"$output\"\n"
            "else\n"
            '  exec "$real_git" "$@"\n'
            "fi\n"
        )
        wrapper.chmod(0o755)
        environment = {**os.environ, "PATH": f"{wrapper_dir}:{os.environ['PATH']}"}

        installed = self.cli(
            "skills", "install", "--target", str(self.target), "--selection",
            str(self.source), "main", "skills/alpha", env=environment,
        )

        self.assertEqual(installed.returncode, 0, installed.stderr)
        self.assertEqual(self.git("rev-parse", "main"), replacement)
        self.assertEqual((self.target / ".agents/skills/alpha/SKILL.md").read_text(), original_text)

    def test_governing_license_symlink_is_preserved(self) -> None:
        (self.source / "LICENSE").unlink()
        (self.source / "LICENSE.txt").write_text("license target\n")
        (self.source / "LICENSE").symlink_to("LICENSE.txt")
        self.skill("alpha\n")
        ref = self.commit()

        installed = self.install(ref)

        self.assertEqual(installed.returncode, 0, installed.stderr)
        snapshot = next((self.target / ".sum-skills/snapshots").iterdir())
        license_link = snapshot / "licenses/LICENSE"
        self.assertTrue(license_link.is_symlink())
        self.assertEqual(license_link.read_text(), "license target\n")


if __name__ == "__main__":
    unittest.main()
