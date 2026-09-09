from __future__ import annotations

# noqa: SIZE_OK - one shared Git/CLI adversarial fixture keeps all skill-ingestion regressions deterministic.

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

    def git_environment(self, mode: str) -> tuple[dict[str, str], Path, Path]:
        real_git = shutil.which("git")
        self.assertIsNotNone(real_git)
        wrapper_dir = self.root / f"git-{mode}"
        wrapper_dir.mkdir()
        log = wrapper_dir / "calls.log"
        marker = wrapper_dir / "blob-read"
        extra = {
            "blob": f'if [ "$3" = "cat-file" ] && [ "$4" = "blob" ] && [ "$5" = "$OVERSIZED_OID" ]; then touch {marker!s}; fi\n',
            "footprint": (
                'if [ "$1" = "clone" ]; then\n'
                '  for destination do :; done\n'
                '  truncate -s 70m "$destination/.git/untrusted-footprint"\n'
                'elif [ "$3" = "fetch" ]; then\n'
                '  truncate -s 70m "$2/.git/untrusted-footprint"\n'
                'fi\n'
            ),
            "stderr": (
                'if [ "$3" = "rev-parse" ]; then\n'
                "  awk 'BEGIN { for (i = 0; i < 70000; i++) printf \"x\" }' >&2\n"
                'fi\n'
            ),
            "log": "",
        }[mode]
        wrapper = wrapper_dir / "git"
        wrapper.write_text(
            "#!/bin/sh\n"
            f"printf '%s\\n' \"$*\" >> {log!s}\n"
            f"{real_git!s} \"$@\"\n"
            "status=$?\n"
            f"{extra}"
            "exit $status\n"
        )
        wrapper.chmod(0o755)
        fake_remote = "https://fixture.invalid/repository.git"
        environment = {
            **os.environ,
            "PATH": f"{wrapper_dir}:{os.environ['PATH']}",
            "GIT_CONFIG_COUNT": "1",
            "GIT_CONFIG_KEY_0": f"url.file://{self.source}/.insteadOf",
            "GIT_CONFIG_VALUE_0": fake_remote,
        }
        return environment, log, marker

    def remote_install(self, environment: dict[str, str], ref: str = "main") -> subprocess.CompletedProcess[str]:
        return self.cli(
            "skills", "install", "--target", str(self.target), "--selection",
            "https://fixture.invalid/repository.git", ref, "skills/alpha",
            env=environment,
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

    def test_blob_size_is_checked_before_payload_read(self) -> None:
        directory = self.skill("alpha\n")
        (directory / "oversized.bin").write_bytes(b"x" * (8 * 1024 * 1024 + 1))
        ref = self.commit()
        environment, _, marker = self.git_environment("blob")
        environment["OVERSIZED_OID"] = self.git("rev-parse", f"{ref}:skills/alpha/oversized.bin")

        installed = self.cli(
            "skills", "install", "--target", str(self.target), "--selection",
            str(self.source), ref, "skills/alpha", env=environment,
        )

        self.assertNotEqual(installed.returncode, 0)
        self.assertIn("larger than", installed.stderr)
        self.assertFalse(marker.exists(), "oversized blob payload was read before rejection")

    def test_selected_resources_have_an_aggregate_byte_cap(self) -> None:
        directory = self.skill("alpha\n")
        for index in range(5):
            (directory / f"resource-{index}.bin").write_bytes(bytes([index]) * (7 * 1024 * 1024))
        ref = self.commit()

        installed = self.install(ref)

        self.assertNotEqual(installed.returncode, 0)
        self.assertIn("aggregate resource limit", installed.stderr)
        self.assertFalse((self.target / ".agents/skills/alpha").exists())

    def test_multiple_selections_share_the_aggregate_byte_cap(self) -> None:
        alpha = self.skill("alpha\n")
        beta = self.source / "skills/beta"
        beta.mkdir(parents=True)
        (beta / "SKILL.md").write_text("---\nname: beta\ndescription: beta\n---\n\nbeta\n")
        for directory in (alpha, beta):
            for index in range(3):
                (directory / f"resource-{index}.bin").write_bytes(bytes([index]) * (6 * 1024 * 1024))
        ref = self.commit()

        installed = self.cli(
            "skills", "install", "--target", str(self.target),
            "--selection", str(self.source), ref, "skills/alpha",
            "--selection", str(self.source), ref, "skills/beta",
        )

        self.assertNotEqual(installed.returncode, 0)
        self.assertIn("aggregate resource limit", installed.stderr)
        self.assertFalse((self.target / ".agents/skills/alpha").exists())

    def test_refspec_syntax_is_refused_before_git_access(self) -> None:
        self.skill("alpha\n")
        self.commit()

        inspected = self.cli(
            "skills", "inspect", "--repository", str(self.source),
            "--ref", "main:refs/heads/injected", "--path", "skills/alpha",
        )

        self.assertNotEqual(inspected.returncode, 0)
        self.assertIn("refspec syntax", inspected.stderr)

    def test_git_stderr_is_hard_capped(self) -> None:
        self.skill("alpha\n")
        ref = self.commit()
        environment, _, _ = self.git_environment("stderr")

        installed = self.cli(
            "skills", "install", "--target", str(self.target), "--selection",
            str(self.source), ref, "skills/alpha", env=environment,
        )

        self.assertNotEqual(installed.returncode, 0)
        self.assertIn("stderr limit", installed.stderr)
        self.assertLess(len(installed.stderr), 4096)

    def test_fast_git_stdout_limit_failure_remains_structured(self) -> None:
        environment = {**os.environ, "PYTHONPATH": str(ROOT / "lib")}
        checked = subprocess.run(
            [
                sys.executable,
                "-c",
                "from skill_content import SkillError; "
                "from skill_git import GitCommand, _run_git; "
                "\ntry: _run_git(GitCommand(None, ('--version',), stdout_limit=1, timeout=5))"
                "\nexcept SkillError as exc: print(exc)"
                "\nelse: raise SystemExit('expected bounded failure')",
            ],
            capture_output=True,
            text=True,
            env=environment,
        )

        self.assertEqual(checked.returncode, 0, checked.stderr)
        self.assertIn("stdout limit", checked.stdout)
        self.assertNotIn("Traceback", checked.stderr)

    def test_remote_source_uses_partial_exact_ref_fetch(self) -> None:
        self.skill("alpha\n")
        ref = self.commit()
        environment, log, _ = self.git_environment("log")

        installed = self.remote_install(environment, ref)

        self.assertEqual(installed.returncode, 0, installed.stderr)
        calls = log.read_text().splitlines()
        self.assertFalse(any(" clone " in f" {line} " for line in calls), calls)
        fetch = next(line for line in calls if " fetch " in f" {line} ")
        self.assertIn("--depth=1", fetch)
        self.assertIn("--filter=blob:none", fetch)
        self.assertIn("--no-tags", fetch)
        self.assertIn(ref, fetch)

    def test_remote_repository_footprint_is_bounded(self) -> None:
        self.skill("alpha\n")
        self.commit()
        environment, _, _ = self.git_environment("footprint")

        installed = self.remote_install(environment)

        self.assertNotEqual(installed.returncode, 0)
        self.assertIn("temporary repository footprint", installed.stderr)
        self.assertFalse((self.target / ".agents/skills/alpha").exists())

    def test_license_discovery_only_inspects_governing_ancestors(self) -> None:
        self.skill("alpha\n")
        ref = self.commit()
        environment, log, _ = self.git_environment("log")

        installed = self.cli(
            "skills", "install", "--target", str(self.target), "--selection",
            str(self.source), ref, "skills/alpha", env=environment,
        )

        self.assertEqual(installed.returncode, 0, installed.stderr)
        recursive = [line for line in log.read_text().splitlines() if " ls-tree -r " in f" {line} "]
        self.assertTrue(recursive)
        self.assertTrue(all(" -- skills/alpha" in line for line in recursive), recursive)

    def test_governing_license_symlink_target_is_loaded_exactly(self) -> None:
        (self.source / "LICENSE").unlink()
        legal = self.source / "legal"
        legal.mkdir()
        (legal / "NOTICE.txt").write_text("license target\n")
        (self.source / "LICENSE").symlink_to("legal/NOTICE.txt")
        self.skill("alpha\n")
        ref = self.commit()
        environment, log, _ = self.git_environment("log")

        installed = self.cli(
            "skills", "install", "--target", str(self.target), "--selection",
            str(self.source), ref, "skills/alpha", env=environment,
        )

        self.assertEqual(installed.returncode, 0, installed.stderr)
        self.assertTrue(any(" -- legal/NOTICE.txt" in line for line in log.read_text().splitlines()))
        snapshot = next((self.target / ".sum-skills/snapshots").iterdir())
        self.assertEqual((snapshot / "licenses/LICENSE").read_text(), "license target\n")

    def test_cyclic_projection_runtime_error_is_structured_corruption(self) -> None:
        self.skill("alpha\n")
        ref = self.commit()
        installed = self.install(ref)
        self.assertEqual(installed.returncode, 0, installed.stderr)
        projection = self.target / ".agents/skills/alpha"
        projection.unlink()
        projection.symlink_to("alpha-cycle")
        projection.with_name("alpha-cycle").symlink_to("alpha")
        custom = self.root / "sitecustomize"
        custom.mkdir()
        (custom / "sitecustomize.py").write_text(
            "from pathlib import Path\n"
            "original_resolve = Path.resolve\n"
            "def raising_resolve(self, *args, **kwargs):\n"
            "    if self.as_posix().endswith('/.agents/skills/alpha'):\n"
            "        raise RuntimeError('forced cyclic projection')\n"
            "    return original_resolve(self, *args, **kwargs)\n"
            "Path.resolve = raising_resolve\n"
        )
        environment = {**os.environ, "PYTHONPATH": str(custom)}

        checked = self.cli("skills", "check", "--root", str(self.target), env=environment)

        self.assertNotEqual(checked.returncode, 0)
        self.assertNotIn("Traceback", checked.stderr)
        self.assertTrue(checked.stdout, checked.stderr)
        value = json.loads(checked.stdout)
        self.assertFalse(value["ok"])
        self.assertTrue(any("projection" in error for error in value["selected"]["errors"]))


if __name__ == "__main__":
    unittest.main()
