"""Portable verification contract (issue #31): VERIFY.md + `mise run verify` + feature maps, exercised through the distributable runner in
.agents/skills/verify with a strict fake mise and real Git, from a managed clone, an external clone with sum/Herdr absent, and a linked worktree."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock  # noqa: F401
unittest.mock = mock

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "tests/fixtures/verify"
RUNNER_RELATIVE = ".agents/skills/verify/scripts/verify_run.py"
sys.path.insert(0, str(ROOT / "lib"))
import sumctl  # noqa: E402


class VerifyLab(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory(prefix="sum-verify-")
        self.root = Path(self._tmp.name).resolve()
        self.addCleanup(self._tmp.cleanup)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "mise").symlink_to(ROOT / "tests/fixtures/mise.py")
        python_dir = str(Path(sys.executable).resolve().parent)
        # The external-clone environment: no SUM_*/HERDR_* variables, no sum on PATH, only the fake mise, this interpreter, and system tools.
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("SUM_", "HERDR_", "FAKE_", "MISE_"))}
        self.env.update(PATH=os.pathsep.join([str(self.bin), python_dir, "/usr/bin", "/bin"]), FAKE_MISE_STOP=str(self.root), MISE_QUIET="1")

    # -- helpers -----------------------------------------------------------------------------
    def git(self, repo, *args, check=True):
        result = subprocess.run(["git", "-C", str(repo), *args], text=True, capture_output=True, check=check)
        return result.stdout.strip()

    def make_repo(self, fixture, path, skill=True):
        """A real Git repository from a fixture, with the portable skill copied in exactly as a project would vendor it."""
        path = Path(path)
        shutil.copytree(FIXTURES / fixture, path)
        if skill:
            shutil.copytree(ROOT / ".agents/skills/verify", path / ".agents/skills/verify")
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=path, check=True)
        self.git(path, "config", "user.email", "lab@example.invalid")
        self.git(path, "config", "user.name", "verify lab")
        self.git(path, "add", "-A")
        self.git(path, "commit", "-q", "-m", "fixture")
        return path

    def run_verify(self, repo, *args, cwd=None, env=None, timeout=240):
        cmd = [sys.executable, str(Path(repo) / RUNNER_RELATIVE), "--json", *args]
        result = subprocess.run(cmd, cwd=str(cwd or repo), env={**self.env, **(env or {})}, text=True, capture_output=True, timeout=timeout)
        try:
            record = json.loads(result.stdout) if result.stdout.strip() else None
        except ValueError:
            record = None
        return result.returncode, record, result

    # -- contract and maps ---------------------------------------------------------------------
    def test_check_validates_contract_without_running_and_names_every_malformation(self):
        repo = self.make_repo("cli", self.root / "cli")
        code, record, _ = self.run_verify(repo, "--check")
        self.assertEqual((code, record["outcome"]), (0, "checked"), record)
        self.assertEqual(record["task"]["source"], str(repo / "mise.toml"))
        self.assertEqual([s["id"] for s in record["scenarios"]], ["cli.greet", "cli.shout", "cli.terminal-colors"])
        self.assertTrue(record["artifacts"]["git_ignored"])
        self.assertFalse((repo / ".artifacts").exists())  # --check writes no record.
        contract = repo / "VERIFY.md"
        original = contract.read_text()
        cases = {
            "missing file": None,
            "no block": original.replace("```verify", "```toml"),
            "bad toml": original.replace('entrypoint = "mise run verify"', "entrypoint = = nope"),
            "wrong entrypoint": original.replace('entrypoint = "mise run verify"', 'entrypoint = "make check"'),
            "absolute maps": original.replace('feature_maps = "docs/features/README.md"', 'feature_maps = "/etc/passwd"'),
            "missing section": original.replace("## Isolation", "## Elsewhere"),
        }
        for label, text in cases.items():
            with self.subTest(label):
                if text is None:
                    contract.unlink()
                else:
                    contract.write_text(text)
                code, record, _ = self.run_verify(repo, "--check")
                self.assertEqual((code, record["outcome"]), (2, "blocked"), (label, record))
                self.assertTrue(record["blocked_reason"])
                self.assertIsNone(record["certifies"])
        contract.write_text(original)
        index = repo / "docs/features/README.md"
        index.write_text("# maps\n\n- [gone](missing.md)\n")
        code, record, _ = self.run_verify(repo, "--check")
        self.assertEqual(record["outcome"], "blocked")
        self.assertIn("missing.md", record["blocked_reason"])
        index.write_text("# maps\n\n- [out](../../../outside.md)\n")
        code, record, _ = self.run_verify(repo, "--check")
        self.assertIn("leaves the repository", record["blocked_reason"])
        (repo / "docs/features/cli.md").write_text("| ID | S | Driver |\n| --- | --- | --- |\n| `dup` | a | automated |\n| `dup` | b | manual |\n")
        index.write_text("# maps\n\n- [cli](cli.md)\n")
        code, record, _ = self.run_verify(repo, "--check")
        self.assertIn("defined twice", record["blocked_reason"])
        (repo / "docs/features/cli.md").write_text("| ID | S | Driver |\n| --- | --- | --- |\n| `x` | a | sometimes |\n")
        code, record, _ = self.run_verify(repo, "--check")
        self.assertIn("must start with `automated` or `manual`", record["blocked_reason"])
        (repo / "docs/features/cli.md").write_text("| ID | Scenario | Notes |\n| --- | --- | --- |\n| `x` | a | automated |\n")
        code, record, _ = self.run_verify(repo, "--check")
        self.assertEqual((record["outcome"], record["scenarios"]), ("checked", []))  # No Driver column: no scenarios are claimed.

    def test_missing_task_inherited_task_and_blocked_dependency_are_blocked_never_passed(self):
        repo = self.make_repo("cli", self.root / "cli")
        (repo / "mise.toml").write_text('[tasks]\ntest = "python3 -m unittest"\n')  # `verify` no longer exists.
        code, record, _ = self.run_verify(repo)
        self.assertEqual((code, record["outcome"]), (2, "blocked"), record)
        self.assertIn("no `verify` task", record["blocked_reason"])
        self.assertFalse((repo / ".artifacts").exists())  # Nothing ran, nothing recorded as a run.
        # Inherited: a parent directory (sum's installation shape) defines `verify`; the nested project defines nothing.
        parent = self.root / "installation"
        parent.mkdir()
        (parent / "mise.toml").write_text('[tasks]\nverify = "echo parent verify; exit 0"\ntest = "echo parent"\n')
        nested = self.make_repo("cli", parent / "projects/acme/cli")
        (nested / "mise.toml").unlink()
        self.git(nested, "commit", "-qam", "drop own tasks")
        code, record, _ = self.run_verify(nested)
        self.assertEqual((code, record["outcome"]), (2, "blocked"), record)
        self.assertIn("outside this repository", record["blocked_reason"])
        self.assertIn(str(parent / "mise.toml"), record["blocked_reason"])
        # Blocked dependency: a required command is absent from PATH.
        repo2 = self.make_repo("cli", self.root / "cli2")
        contract = repo2 / "VERIFY.md"
        contract.write_text(contract.read_text().replace('commands = ["python3"]', 'commands = ["python3", "definitely-not-installed-tool"]'))
        code, record, _ = self.run_verify(repo2)
        self.assertEqual((code, record["outcome"]), (2, "blocked"), record)
        self.assertIn("definitely-not-installed-tool", record["blocked_reason"])
        self.assertNotIn("execution", record)
        # No mise at all is blocked too.
        (self.bin / "mise").unlink()
        code, record, _ = self.run_verify(repo)
        self.assertEqual(record["outcome"], "blocked")
        self.assertIn("mise is not on PATH", record["blocked_reason"])

    def test_clean_pass_certifies_sha_dirty_run_is_provisional_and_failure_keeps_the_log(self):
        repo = self.make_repo("cli", self.root / "cli")
        head = self.git(repo, "rev-parse", "HEAD")
        code, record, _ = self.run_verify(repo, "--base", head)  # A clean pass certifies only when policy was compared against a base.
        self.assertEqual((code, record["outcome"]), (0, "pass"), record)
        self.assertEqual(record["certifies"], head)
        self.assertFalse(record["provisional"])
        self.assertEqual(record["execution"]["exit"], 0)
        self.assertEqual({s["id"]: s["status"] for s in record["scenarios"]}, {"cli.greet": "pass", "cli.shout": "pass", "cli.terminal-colors": "not-run"})
        self.assertEqual(record["not_exercised"], ["cli.terminal-colors"])
        run_dir = repo / record["artifacts"]["run_dir"]
        self.assertTrue((run_dir / "run.json").is_file() and (run_dir / "verify.log").is_file())
        self.assertIn("Ran 2 tests", (run_dir / "verify.log").read_text())
        self.assertEqual(json.loads((repo / ".artifacts/verification/latest.json").read_text())["run_id"], record["run_id"])
        self.assertEqual(self.git(repo, "status", "--porcelain"), "")  # The record directory is ignored; the tree stays clean.
        self.assertEqual(record["contract"]["sha256"], sumctl.sha256_text((repo / "VERIFY.md").read_text()))
        self.assertEqual(len(record["feature_maps"]), 2)
        # Dirty tree: still runs, provisional, certifies nothing.
        (repo / "hello.py").write_text((repo / "hello.py").read_text() + "\n# local edit\n")
        code, record, _ = self.run_verify(repo)
        self.assertEqual((code, record["outcome"]), (0, "pass"))
        self.assertTrue(record["provisional"] and record["candidate"]["dirty"])
        self.assertIsNone(record["certifies"])
        # Intentional failure: fail with the log retained; a second run gets its own id.
        (repo / "tests/test_hello.py").write_text((repo / "tests/test_hello.py").read_text().replace('"Hello, Ada!")', '"Goodbye, Ada!")', 1))
        code, record, _ = self.run_verify(repo)
        self.assertEqual((code, record["outcome"]), (1, "fail"), record)
        self.assertNotEqual(record["execution"]["exit"], 0)
        self.assertIn("AssertionError", (repo / record["execution"]["log"]).read_text())
        self.assertEqual({s["status"] for s in record["scenarios"] if s["driver"] == "automated"}, {"fail"})
        self.assertEqual(len(list((repo / ".artifacts/verification").glob("*/run.json"))), 3)
        self.assertEqual(len({p.parent.name for p in (repo / ".artifacts/verification").glob("*/run.json")}), 3)

    def test_manual_scenarios_are_not_run_unless_reported_and_reports_need_reasons_and_known_ids(self):
        repo = self.make_repo("web", self.root / "web")
        code, record, _ = self.run_verify(repo, "--scenario", "web.visual-layout=pass:checked at 375px and 1280px in Safari")
        self.assertEqual((code, record["outcome"]), (0, "pass"), record)
        rows = {s["id"]: s for s in record["scenarios"]}
        self.assertEqual(rows["web.visual-layout"]["status"], "pass")
        self.assertIn("Safari", rows["web.visual-layout"]["reason"])
        self.assertEqual(record["not_exercised"], [])
        code, record, _ = self.run_verify(repo, "--scenario", "web.visual-layout=not-applicable:headless CI host")
        self.assertEqual(rows := {s["id"]: s for s in record["scenarios"]}, rows)
        self.assertEqual((record["outcome"], rows["web.visual-layout"]["status"], record["not_exercised"]), ("pass", "not-applicable", ["web.visual-layout"]))
        code, record, _ = self.run_verify(repo, "--scenario", "web.visual-layout=fail:header overlaps at 375px")
        self.assertEqual((code, record["outcome"]), (1, "fail"))
        self.assertEqual(record["execution"]["exit"], 0)  # The aggregate passed; the reported manual failure still fails the run.
        code, _, result = self.run_verify(repo, "--scenario", "web.visual-layout=not-applicable")
        self.assertEqual(code, 3)
        self.assertIn("needs a reason", result.stderr)
        code, _, result = self.run_verify(repo, "--scenario", "web.nonexistent=pass")
        self.assertEqual(code, 3)
        self.assertIn("not present in any feature map", result.stderr)
        code, _, result = self.run_verify(repo, "--scenario", "web.visual-layout=maybe")
        self.assertEqual(code, 3)

    def test_stale_build_fails_even_when_the_entrypoint_exits_zero(self):
        repo = self.make_repo("web", self.root / "web")
        code, record, _ = self.run_verify(repo)
        self.assertEqual((code, record["outcome"]), (0, "pass"), record)
        self.assertEqual(record["freshness"], {"fresh": True, "reason": None})
        # The candidate stops rebuilding before it checks, then edits a source: dist/ is older than src/ after the run.
        (repo / "mise.toml").write_text('[tasks]\nbuild = "python3 build.py"\nverify = "python3 check.py --no-build"\n')
        time.sleep(0.05)
        source = repo / "src/index.html.in"
        source.write_text(source.read_text().replace("Fixture service", "Fixture service v2"))
        future = time.time() + 5
        os.utime(source, (future, future))
        code, record, _ = self.run_verify(repo)
        self.assertEqual((code, record["outcome"]), (1, "fail"), record)
        self.assertEqual(record["execution"]["exit"], 0)
        self.assertFalse(record["freshness"]["fresh"])
        self.assertIn("stale", record["freshness"]["reason"])
        self.assertIsNone(record["certifies"])
        # Missing outputs entirely: also not fresh.
        shutil.rmtree(repo / "dist")
        code, record, _ = self.run_verify(repo)
        self.assertEqual(record["outcome"], "fail")  # check.py exits 1 without dist; both signals agree.

    def test_policy_change_since_base_requires_root_review_and_cannot_certify(self):
        repo = self.make_repo("cli", self.root / "cli")
        base = self.git(repo, "rev-parse", "HEAD")
        code, record, _ = self.run_verify(repo, "--base", base)
        self.assertEqual((record["outcome"], record["policy"]), ("pass", {"checked": True, "base": base, "changed": []}))
        self.assertEqual(record["certifies"], base)
        self.git(repo, "checkout", "-q", "-b", "candidate")
        (repo / "docs/features/cli.md").write_text((repo / "docs/features/cli.md").read_text().replace("| `cli.shout` |", "| `cli.shout-renamed` |"))
        (repo / "VERIFY.md").write_text((repo / "VERIFY.md").read_text().replace("## Isolation\n\nTests write nothing outside the process.", "## Isolation\n\nWhatever."))
        (repo / "mise.toml").write_text('[tasks]\nverify = "true"\n')  # A candidate weakening the entrypoint to a command that merely succeeds.
        self.git(repo, "commit", "-qam", "weaken verification")
        head = self.git(repo, "rev-parse", "HEAD")
        code, record, _ = self.run_verify(repo, "--base", base)
        self.assertEqual((code, record["outcome"]), (0, "pass"), record)
        self.assertTrue(record["requires_root_review"])
        self.assertEqual(sorted(record["policy"]["changed"]), ["VERIFY.md", "docs/features/cli.md", "mise.toml"])
        self.assertIsNone(record["certifies"])
        self.assertNotEqual(head, base)
        code, record, _ = self.run_verify(repo, "--base", "deadbeef")
        self.assertEqual(record["outcome"], "blocked")
        self.assertIn("not a commit", record["blocked_reason"])
        # Without --base the policy comparison is explicitly not checked: root review stays required and nothing is certified.
        code, record, _ = self.run_verify(repo)
        self.assertEqual((code, record["outcome"]), (0, "pass"))
        self.assertEqual(record["policy"], {"checked": False, "base": None, "changed": []})
        self.assertTrue(record["requires_root_review"])
        self.assertIsNone(record["certifies"])

    def test_candidate_cannot_shrink_the_policy_file_set(self):
        repo = self.make_repo("cli", self.root / "cli")
        base = self.git(repo, "rev-parse", "HEAD")
        self.git(repo, "checkout", "-q", "-b", "candidate")
        (repo / "mise.toml").write_text('[tasks]\nverify = "true"\n')
        contract = repo / "VERIFY.md"
        for label, declaration in (("empty", "policy_files = []"), ("subset without mise.toml", 'policy_files = ["docs/features/README.md"]')):
            with self.subTest(label):
                contract.write_text(contract.read_text().replace('entrypoint = "mise run verify"', f'entrypoint = "mise run verify"\n{declaration}', 1))
                self.git(repo, "commit", "-qam", f"weaken with {label}")
                code, record, _ = self.run_verify(repo, "--base", base)
                self.assertEqual((code, record["outcome"]), (0, "pass"), record)
                self.assertTrue(record["policy"]["checked"])
                self.assertIn("mise.toml", record["policy"]["changed"])
                self.assertIn("VERIFY.md", record["policy"]["changed"])
                self.assertTrue(record["requires_root_review"])
                self.assertIsNone(record["certifies"])
                contract.write_text(contract.read_text().replace(f"\n{declaration}", "", 1))
        # Adding a path is allowed; a malformed declaration blocks.
        contract.write_text(contract.read_text().replace('entrypoint = "mise run verify"', 'entrypoint = "mise run verify"\npolicy_files = ["tests/"]', 1))
        (repo / "tests/test_hello.py").write_text("import unittest\n")
        self.git(repo, "commit", "-qam", "gut tests")
        code, record, _ = self.run_verify(repo, "--base", base)
        self.assertIn("tests/test_hello.py", record["policy"]["changed"])
        self.assertIsNone(record["certifies"])
        contract.write_text(contract.read_text().replace('policy_files = ["tests/"]', 'policy_files = ["/etc"]', 1))
        code, record, _ = self.run_verify(repo, "--check")
        self.assertEqual(record["outcome"], "blocked")
        self.assertIn("policy_files", record["blocked_reason"])

    # -- three checkout shapes ---------------------------------------------------------------
    def test_same_contract_from_managed_clone_external_clone_and_linked_worktree(self):
        for fixture in ("cli", "web"):
            with self.subTest(fixture=fixture):
                origin = self.make_repo(fixture, self.root / f"origin-{fixture}")
                # (a) Managed clone under an installation that has sum's mise.toml; the project's own `verify` shadows the parent's.
                installation = self.root / f"installation-{fixture}"
                (installation / "projects/acme").mkdir(parents=True)
                shutil.copy(ROOT / "mise.toml", installation / "mise.toml")
                shutil.copytree(ROOT / "mise-tasks", installation / "mise-tasks")
                managed = installation / "projects/acme" / fixture
                subprocess.run(["git", "clone", "-q", str(origin), str(managed)], check=True)
                code, record, _ = self.run_verify(managed, cwd=managed / "docs")  # Started from a subdirectory: the Git root is resolved.
                self.assertEqual((code, record["outcome"]), (0, "pass"), record)
                self.assertEqual(record["root"], str(managed.resolve()))
                self.assertEqual(record["task"]["source"], str(managed.resolve() / "mise.toml"))
                self.assertFalse(record["candidate"]["git_dir_is_file"])
                # (b) External ordinary clone: no sum, no Herdr, no installation path anywhere in the environment.
                external = self.root / f"elsewhere/{fixture}"
                external.parent.mkdir(exist_ok=True)
                subprocess.run(["git", "clone", "-q", str(origin), str(external)], check=True)
                code, record, result = self.run_verify(external, "--base", "HEAD")
                self.assertEqual((code, record["outcome"]), (0, "pass"), record)
                self.assertNotIn(str(ROOT), json.dumps(record))  # Nothing in the record points back at sum's checkout.
                self.assertFalse(any(k.startswith(("SUM_", "HERDR_")) for k in self.env))
                self.assertEqual(record["certifies"], self.git(external, "rev-parse", "HEAD"))
                # (c) Linked worktree: `.git` is a file, and the run records that fact.
                linked = self.root / f"worktrees/{fixture}-task"
                linked.parent.mkdir(exist_ok=True)
                self.git(external, "worktree", "add", "-q", "-b", "task", str(linked))
                code, record, _ = self.run_verify(linked)
                self.assertEqual((code, record["outcome"]), (0, "pass"), record)
                self.assertTrue(record["candidate"]["git_dir_is_file"])
                self.assertEqual(record["root"], str(linked.resolve()))
                self.assertEqual(record["candidate"]["branch"], "task")
                self.assertTrue((linked / ".artifacts/verification/latest.json").is_file())
                self.assertFalse((external / ".artifacts/verification" / record["run_id"]).exists())  # Records stay with the checkout that ran.

    def test_reading_verify_md_directly_is_enough_without_the_skill_directory(self):
        # A harness without skill discovery follows VERIFY.md: the entrypoint it names works with plain mise, and the runner is optional extra procedure.
        repo = self.make_repo("cli", self.root / "cli", skill=False)
        text = (repo / "VERIFY.md").read_text()
        self.assertIn("mise run verify", text)
        result = subprocess.run(["mise", "run", "verify"], cwd=repo, env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Ran 2 tests", result.stderr + result.stdout)

    # -- sum's own discovery surfaces the status, never runs anything ----------------------------
    def test_env_discovery_reports_standardized_or_not_yet_standardized_without_running(self):
        repo = self.make_repo("cli", self.root / "cli")
        with unittest.mock.patch.dict(os.environ, {"SUM_MISE_BIN": str(ROOT / "tests/fixtures/mise.py"), "FAKE_MISE_STOP": str(self.root)}):
            discovery = sumctl.discover_configuration(repo)
            self.assertEqual(discovery["verification_contract"]["status"], "standardized")
            self.assertEqual(discovery["verification_contract"]["runner"], RUNNER_RELATIVE)
            plain = self.root / "plain"
            plain.mkdir()
            (plain / "mise.toml").write_text('[tasks]\ntest = "python3 -m unittest"\n')
            subprocess.run(["git", "init", "-q"], cwd=plain, check=True)
            discovery = sumctl.discover_configuration(plain)
            self.assertEqual(discovery["verification_contract"]["status"], "not-yet-standardized")
            self.assertFalse(discovery["verification_contract"]["verify_md"])
            (plain / "VERIFY.md").write_text("# not wired\n")
            discovery = sumctl.discover_configuration(plain)
            self.assertEqual(discovery["verification_contract"]["status"], "not-yet-standardized")
            self.assertIn("no `verify` task", discovery["verification_contract"]["why"])
        self.assertFalse((repo / ".artifacts").exists())

    def test_sum_repository_contract_checks_against_its_own_tasks(self):
        # sum's own VERIFY.md, maps, and `mise-tasks/verify` validate through the same runner (--check runs nothing).
        node = shutil.which("node")
        self.assertTrue(node, "node is required by sum's own contract")
        path = os.pathsep.join([str(Path(node).resolve().parent), self.env["PATH"]])
        code, record, result = self.run_verify(ROOT, "--check", cwd=ROOT, env={"FAKE_MISE_STOP": str(ROOT), "PATH": path})
        self.assertEqual((code, record["outcome"]), (0, "checked"), (record, result.stderr))
        self.assertEqual(Path(record["task"]["source"]).resolve(), (ROOT / "mise-tasks/verify").resolve())
        ids = [s["id"] for s in record["scenarios"]]
        self.assertIn("verify.three-checkouts", ids)
        self.assertEqual(len(ids), len(set(ids)))
        drivers = {s["id"]: s["driver"] for s in record["scenarios"]}
        self.assertEqual(drivers["verify.skipped-scenario"], "automated")  # Its description starts with "Manual"; only the Driver column decides.
        self.assertEqual([i for i, d in drivers.items() if d == "manual"], ["live.herdr-smoke", "live.harness-canary", "verify.sum-self", "root.real-harness-canary", "evidence.publish-rendered", "evidence.sum-self"])
        self.assertEqual(record["contract"]["entrypoint"], "mise run verify")
        self.assertTrue(record["artifacts"]["git_ignored"])


if __name__ == "__main__":
    unittest.main()
