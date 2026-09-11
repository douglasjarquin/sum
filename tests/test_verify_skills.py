"""Create/maintain verification skills (issue #32): bootstrap web, CLI, and HTTP-service fixtures from their existing tasks, prove one feature per
recipe with captured evidence that survives teardown, re-run without churn, preserve user edits and a custom map location, expose a broken base,
detect the features a change touches, audit stale claims, and follow every generated instruction from an external clone with sum absent."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "tests/fixtures/verify"
SCAFFOLD = "scripts/verify_scaffold.py"
RUNNER = ".agents/skills/verify/scripts/verify_run.py"
CAPTURE = ".agents/skills/verify/scripts/verify_capture.py"
AUDIT = ".agents/skills/maintain-verification/scripts/verify_audit.py"
TEST_FILE = {"cli": "tests/test_hello.py", "web": "check.py", "service": "tests/test_app.py"}
SOURCE_FILE = {"cli": "hello.py", "web": "serve.py", "service": "app.py"}


class SkillLab(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory(prefix="sum-verify-skills-")
        self.root = Path(self._tmp.name).resolve()
        self.addCleanup(self._tmp.cleanup)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "mise").symlink_to(ROOT / "tests/fixtures/mise.py")
        # The skills as a project would receive them: a copy of `.agents/skills` somewhere that is not sum, so nothing can resolve through sum's checkout.
        self.toolkit = self.root / "toolkit/.agents/skills"
        shutil.copytree(ROOT / ".agents/skills", self.toolkit, ignore=shutil.ignore_patterns("__pycache__"))
        python_dir = str(Path(sys.executable).resolve().parent)
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("SUM_", "HERDR_", "FAKE_", "MISE_"))}
        self.env.update(PATH=os.pathsep.join([str(self.bin), python_dir, "/usr/bin", "/bin"]), FAKE_MISE_STOP=str(self.root), MISE_QUIET="1")
        self.servers = []
        self.addCleanup(self.stop_servers)

    # -- helpers -----------------------------------------------------------------------------
    def git(self, repo, *args, check=True):
        return subprocess.run(["git", "-C", str(repo), *args], text=True, capture_output=True, check=check).stdout.strip()

    def raw_repo(self, fixture, path):
        """A fixture stripped back to an ordinary project: its code, tests, README, and its own tasks, with no contract, maps, or skills."""
        path = Path(path)
        shutil.copytree(FIXTURES / fixture, path, ignore=shutil.ignore_patterns("__pycache__", "data"))
        for gone in ("VERIFY.md", "docs/features", ".agents", ".claude"):
            target = path / gone
            if target.is_dir():
                shutil.rmtree(target)
            elif target.exists():
                target.unlink()
        if fixture == "cli":
            (path / "mise.toml").write_text('[tasks]\ntest = "python3 -m unittest discover -s tests -p \'test_*.py\'"\n')
        if fixture == "web":
            (path / "mise.toml").write_text('[tasks]\nbuild = "python3 build.py"\ncheck = "python3 check.py"\nserve = "python3 serve.py"\n')
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=path, check=True)
        self.git(path, "config", "user.email", "lab@example.invalid")
        self.git(path, "config", "user.name", "verify lab")
        self.git(path, "add", "-A")
        self.git(path, "commit", "-q", "-m", "ordinary project")
        return path

    def run_py(self, script, repo, *args, cwd=None, env=None, timeout=240):
        result = subprocess.run([sys.executable, str(script), *args], cwd=str(cwd or repo), env={**self.env, **(env or {})}, text=True, capture_output=True, timeout=timeout)
        record = None
        if "--json" in args and result.stdout.strip():
            try:
                record = json.loads(result.stdout)
            except ValueError:
                record = None
        return result.returncode, record, result

    def scaffold(self, repo, *args, toolkit=None):
        return self.run_py((toolkit or self.toolkit) / "create-verification" / SCAFFOLD, repo, "--root", str(repo), "--json", *args, cwd=self.root)

    def runner(self, repo, *args):
        return self.run_py(repo / RUNNER, repo, "--json", *args)

    def audit(self, repo, *args):
        return self.run_py(repo / AUDIT, repo, "--json", *args)

    def capture(self, repo, *args):
        return self.run_py(repo / CAPTURE, repo, *args)

    def fill(self, repo, fixture):
        """What the agent does after generation: replace every placeholder with what it observed. Kept mechanical here; the content is the fixture's."""
        contract = repo / "VERIFY.md"
        text = contract.read_text()
        text = text.replace("TODO(verify): state what this check proves", f"the checks in `{TEST_FILE[fixture]}`")
        text = re.sub(r"^TODO\(verify\): how a fresh clone is prepared.*$", "Nothing to install beyond Python 3.", text, flags=re.M)
        text = re.sub(r"^TODO\(verify\): name the temporary directories.*$", "Tests use temporary directories and ephemeral loopback ports only.", text, flags=re.M)
        text = re.sub(r"^TODO\(verify\): how anything a check started.*$", "Each check stops the process it started; nothing is left listening.", text, flags=re.M)
        contract.write_text(text)
        index = repo / "docs/features/README.md"
        for path in index.parent.glob("*.md"):
            if path == index:
                continue
            body = path.read_text()
            body = body.replace("automated: TODO(verify): name the test that exercises it", f"automated: {TEST_FILE[fixture]}")
            body = body.replace("TODO(verify): the success path a user sees", "the success path answers as documented")
            body = re.sub(r"TODO\(verify\):[^|\n]*", f"observed in `{SOURCE_FILE[fixture]}` and `{TEST_FILE[fixture]}`", body)
            path.write_text(body)

    def start(self, repo, script, env=None):
        proc = subprocess.Popen([sys.executable, str(repo / script)], cwd=str(repo), stdout=subprocess.PIPE, text=True, env={**self.env, **(env or {})})
        self.servers.append(proc)
        return proc, proc.stdout.readline().strip()

    def stop_servers(self):
        for proc in self.servers:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=5)
            proc.stdout.close()
        self.servers = []

    # -- bootstrap, prove, re-run -----------------------------------------------------------------
    def test_bootstrap_three_fixtures_prove_one_feature_each_and_rerun_without_churn(self):
        for fixture in ("cli", "web", "service"):
            with self.subTest(fixture=fixture):
                repo = self.raw_repo(fixture, self.root / f"raw-{fixture}")
                code, plan, result = self.scaffold(repo)  # Inspect only.
                self.assertEqual(code, 0, result.stderr)
                self.assertEqual(plan["mode"], "inspect")
                self.assertFalse((repo / "VERIFY.md").exists())
                self.assertIn("mise run " + {"cli": "test", "web": "check", "service": "test"}[fixture], plan["plan"]["commands"])
                self.assertEqual(plan["inspection"]["check_tasks"], {"cli": ["test"], "web": ["build", "check"], "service": ["test"]}[fixture])
                self.assertIn({"cli": "cli", "web": "web", "service": "service"}[fixture], plan["inspection"]["surfaces"])
                self.assertTrue(plan["inspection"]["capabilities"]["python3"])
                code, written, result = self.scaffold(repo, "--write")
                self.assertEqual(code, 0, result.stderr)
                statuses = {r["path"]: r["status"] for r in written["results"]}
                self.assertEqual(statuses["VERIFY.md"], "created")
                self.assertEqual(statuses["mise.toml"], "appended")
                self.assertEqual(statuses["docs/features/README.md"], "created")
                self.assertEqual(statuses[".agents/skills/verify"], "vendored")
                self.assertEqual(statuses[".agents/skills/maintain-verification"], "vendored")
                self.assertEqual(statuses[".claude/skills/verify"], "linked")
                self.assertTrue((repo / ".claude/skills/verify").is_symlink() and (repo / ".claude/skills/verify/SKILL.md").is_file())
                self.assertTrue(os.access(repo / RUNNER, os.X_OK) and os.access(repo / AUDIT, os.X_OK) and os.access(repo / CAPTURE, os.X_OK))
                index = (repo / "docs/features/README.md").read_text()
                self.assertIn("Inventory: **incomplete**", index)
                features = sorted(p.name for p in (repo / "docs/features").glob("*.md") if p.name != "README.md")
                self.assertTrue(features)
                self.assertLessEqual(len(features), 5)
                if fixture == "service":
                    self.assertEqual(features, ["service.health.md", "service.items.md"])  # Routes from source; app.py is the served process, not a CLI.
                    self.assertIn("`mise run serve`", (repo / "docs/features/service.health.md").read_text())
                if fixture == "web":
                    self.assertIn("web.home.md", features)
                    self.assertIn("[freshness]", (repo / "VERIFY.md").read_text())
                if fixture == "cli":
                    self.assertIn("cli.hello.md", features)
                for feature in features:
                    body = (repo / "docs/features" / feature).read_text()
                    for heading in ("## Entry points", "## Scenarios", "## Driving it", "## Expected states and side effects", "## Gotchas and manual gaps"):
                        self.assertIn(heading, body)
                # The generated contract validates before anything is filled in; the audit calls it a draft.
                code, record, result = self.runner(repo, "--check")
                self.assertEqual((code, record["outcome"]), (0, "checked"), (record, result.stderr))
                self.assertEqual(Path(record["task"]["source"]), repo / "mise.toml")
                code, audit, _ = self.audit(repo)
                self.assertEqual((code, audit["outcome"]), (1, "findings"))
                self.assertIn("placeholder", {f["kind"] for f in audit["findings"]})
                self.assertEqual(audit["runs"]["proof"], "none")
                self.assertTrue(audit["authored"]["inventory_incomplete"])
                # Fill from observation, commit, audit clean, then prove: run the contract and drive one mapped feature with captured evidence.
                self.fill(repo, fixture)
                self.git(repo, "add", "-A")
                self.git(repo, "commit", "-q", "-m", "verification contract and seed maps")
                head = self.git(repo, "rev-parse", "HEAD")
                code, audit, result = self.audit(repo)
                self.assertEqual((code, audit["outcome"]), (0, "clean"), (audit["findings"], result.stderr))
                code, record, result = self.runner(repo, "--base", head)
                self.assertEqual((code, record["outcome"]), (0, "pass"), (record, result.stderr))
                self.assertEqual(record["certifies"], head)
                run_dir = repo / record["artifacts"]["run_dir"]
                if fixture == "cli":
                    code, _, result = self.capture(repo, "--feature", "cli.hello", "run", "--expect-text", "Hello, Ada!", "--", sys.executable, "hello.py", "Ada")
                    self.assertEqual(code, 0, result.stdout + result.stderr)
                    code, _, result = self.capture(repo, "--feature", "cli.hello", "--scenario", "cli.hello.shout", "run", "--expect-text", "HELLO, ADA!", "--", sys.executable, "hello.py", "Ada", "--shout")
                    self.assertEqual(code, 0, result.stdout + result.stderr)
                elif fixture == "web":
                    subprocess.run([sys.executable, "build.py"], cwd=repo, check=True, capture_output=True)
                    _, url = self.start(repo, "serve.py")
                    code, _, result = self.capture(repo, "--feature", "web.home", "http", "--expect-status", "200", "--expect-text", "<h1>Fixture service</h1>", "GET", url + "/")
                    self.assertEqual(code, 0, result.stdout + result.stderr)
                else:
                    data = self.root / f"data-{fixture}"
                    _, url = self.start(repo, "app.py", env={"ITEMS_DATA_DIR": str(data)})
                    code, _, result = self.capture(repo, "--feature", "service.health", "http", "--expect-status", "200", "--expect-text", '"ok": true', "GET", url + "/health")
                    self.assertEqual(code, 0, result.stdout + result.stderr)
                    code, _, result = self.capture(repo, "--feature", "service.items", "--scenario", "service.items.create", "http", "--data", '{"name": "pen"}', "--expect-status", "201", "POST", url + "/items")
                    self.assertEqual(code, 0, result.stdout + result.stderr)
                    code, _, result = self.capture(repo, "--feature", "service.items", "--scenario", "service.items.persisted", "http", "--expect-status", "200", "--expect-text", '"pen"', "GET", url + "/items")
                    self.assertEqual(code, 0, result.stdout + result.stderr)
                    self.assertTrue((data / "items.json").is_file())  # The side effect, not only the response.
                    shutil.rmtree(data)  # Teardown removes the scratch data the run created...
                self.stop_servers()  # ...and stops what the run started.
                evidence = sorted((run_dir / "evidence").glob("*.json"))
                self.assertGreaterEqual(len(evidence), 1)
                self.assertTrue((run_dir / "run.json").is_file())  # ...but never the proof.
                first = json.loads(evidence[0].read_text())
                self.assertTrue(first["met"])
                self.assertIn(first["recipe"], ("run", "http"))
                self.assertEqual(self.git(repo, "status", "--porcelain"), "")  # Evidence is Git-ignored.
                # Re-run: nothing changes, nothing is written, the filled maps stay exactly as edited.
                before = {p: p.read_bytes() for p in repo.rglob("*") if p.is_file() and ".git" not in p.parts and ".artifacts" not in p.parts}
                code, again, _ = self.scaffold(repo, "--write")
                self.assertEqual(code, 0)
                self.assertTrue({r["status"] for r in again["results"]} <= {"unchanged", "kept"}, again["results"])
                self.assertEqual(again["conflicts"], [])
                self.assertEqual({p: p.read_bytes() for p in repo.rglob("*") if p.is_file() and ".git" not in p.parts and ".artifacts" not in p.parts}, before)
                self.assertEqual(self.git(repo, "status", "--porcelain"), "")

    def test_generation_keeps_user_edits_custom_map_location_and_custom_tasks(self):
        repo = self.raw_repo("service", self.root / "custom")
        (repo / "mise.toml").write_text((repo / "mise.toml").read_text() + 'lint = "python3 -m py_compile app.py"\n')
        (repo / "docs/verification").mkdir(parents=True)
        (repo / "docs/verification/index.md").write_text("# Our maps\n\nInventory: incomplete. Custom location, hand written.\n\n- [Health](health.md)\n")
        (repo / "docs/verification/health.md").write_text("# Health\n\nHand written by the team.\n\n| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `svc.health` | `/health` answers ok | automated: tests/test_app.py | suite |\n")
        (repo / "VERIFY.md").write_text("# Verification contract\n\n```verify\nentrypoint = \"mise run verify\"\nfeature_maps = \"docs/verification/index.md\"\nartifacts = \".artifacts/verification\"\n\n[requires]\ncommands = [\"python3\"]\n```\n\n"
                                        "## Setup\n\nNone.\n\n## Readiness\n\nNone.\n\n## Automated checks\n\n`mise run test`.\n\n## Scenarios\n\nSee `docs/verification/index.md`.\n\n## Isolation\n\nTemp dirs.\n\n## Artifacts\n\n`.artifacts/verification/`.\n\n## Teardown\n\nNone.\n")
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-q", "-m", "team's own contract")
        code, written, result = self.scaffold(repo, "--write")
        self.assertEqual(code, 0, result.stderr)
        statuses = {r["path"]: r["status"] for r in written["results"]}
        self.assertEqual(written["plan"]["feature_maps"], "docs/verification/index.md")  # The declared location is preserved, not moved to docs/features.
        self.assertEqual(statuses["VERIFY.md"], "kept")  # The team's contract has no placeholders: theirs, never regenerated.
        self.assertEqual(statuses["docs/verification/index.md"], "kept")
        self.assertEqual(statuses["docs/verification/service.health.md"], "created")  # New seed files go beside the declared index.
        self.assertFalse((repo / "docs/features").exists())
        self.assertEqual(written["conflicts"], [])
        self.assertIn("Hand written by the team.", (repo / "docs/verification/health.md").read_text())
        self.assertNotIn("```verify\nentrypoint", (repo / "VERIFY.md").read_text()[:20])
        mise_toml = (repo / "mise.toml").read_text()
        self.assertIn('lint = "python3 -m py_compile app.py"', mise_toml)  # Custom tasks survive; verify is appended once and reuses them.
        self.assertEqual(mise_toml.count("[tasks.verify]"), 1)
        self.assertEqual(written["plan"]["commands"], ["mise run test", "mise run lint"])
        code, record, result = self.runner(repo)
        self.assertEqual((code, record["outcome"]), (0, "pass"), (record, result.stderr))  # The two-command `verify` task runs both reused checks.
        self.assertIn("Ran 3 tests", (repo / record["execution"]["log"]).read_text())
        self.assertEqual(self.git(repo, "diff", "--name-only"), ".gitignore\nmise.toml")  # Only the two appends touched tracked files.
        # The user's index is theirs: the audit reports the new seed files as unlinked (their placeholders count once they are linked) and rewrites nothing.
        code, audit, _ = self.audit(repo)
        self.assertEqual({f["kind"] for f in audit["findings"]}, {"unlinked-map"})
        self.assertEqual(sorted(f["file"] for f in audit["findings"]), ["docs/verification/service.health.md", "docs/verification/service.items.md"])
        code, again, _ = self.scaffold(repo, "--write")
        self.assertEqual({r["status"] for r in again["results"] if r["path"] not in ("VERIFY.md", "docs/verification/index.md")}, {"unchanged"})
        # A draft the inspection would now generate differently is a conflict: kept, proposal written, listed.
        (repo / "mise.toml").write_text((repo / "mise.toml").read_text().replace('serve = "python3 app.py"', 'dev = "python3 app.py"'))
        code, again, result = self.scaffold(repo, "--write")
        self.assertEqual(code, 0, result.stdout)
        conflicts = {c["path"]: c for c in again["conflicts"]}
        self.assertEqual(set(conflicts), {"docs/verification/service.health.md", "docs/verification/service.items.md"})
        proposal = repo / conflicts["docs/verification/service.health.md"]["proposal"]
        self.assertIn("`mise run dev`", proposal.read_text())
        self.assertIn("`mise run serve`", (repo / "docs/verification/service.health.md").read_text())  # Yours is untouched.
        self.assertEqual(subprocess.run(["git", "-C", str(repo), "check-ignore", "-q", str(proposal)]).returncode, 0)
        self.assertIn("conflict: docs/verification/service.health.md", subprocess.run([sys.executable, str(self.toolkit / "create-verification" / SCAFFOLD), "--root", str(repo)], env=self.env, text=True, capture_output=True).stdout)

    # -- broken base and nothing to reuse -----------------------------------------------------------
    def test_broken_base_is_exposed_and_a_project_without_checks_gets_no_fictional_task(self):
        repo = self.raw_repo("service", self.root / "broken")
        test = repo / "tests/test_app.py"
        test.write_text(test.read_text().replace('(200, {"ok": True})', '(200, {"ok": "yes"})'))
        self.git(repo, "commit", "-qam", "regression on the base")
        code, _, _ = self.scaffold(repo, "--write")
        self.assertEqual(code, 0)
        self.fill(repo, "service")
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-q", "-m", "contract")
        code, record, _ = self.runner(repo, "--base", "HEAD")
        self.assertEqual((code, record["outcome"]), (1, "fail"), record)  # The base cannot pass its own checks: reported, not masked.
        self.assertIn("AssertionError", (repo / record["execution"]["log"]).read_text())
        self.assertIsNone(record["certifies"])
        code, audit, _ = self.audit(repo)
        self.assertEqual(audit["outcome"], "clean")  # Authored maps are fine; the run result is a separate fact the audit reports as it is.
        self.assertEqual((audit["runs"]["proof"], audit["runs"]["outcome"]), ("current", "fail"))
        # A project with no test, lint, check task, script, or test directory: no verify task is invented and the runner stays blocked.
        empty = self.root / "empty"
        empty.mkdir()
        (empty / "README.md").write_text("# empty\n")
        (empty / "tool.py").write_text("#!/usr/bin/env python3\nprint('hi')\n")
        subprocess.run(["git", "init", "-q"], cwd=empty, check=True)
        self.git(empty, "add", "-A")
        self.git(empty, "-c", "user.email=l@x", "-c", "user.name=l", "commit", "-q", "-m", "x")
        code, written, result = self.scaffold(empty, "--write")
        self.assertEqual(code, 1, result.stdout)
        self.assertTrue(any("no existing check was found" in p for p in written["problems"]))
        self.assertFalse((empty / "mise.toml").exists())
        self.assertIn("TODO(verify): no existing check was found", (empty / "VERIFY.md").read_text())
        code, record, _ = self.runner(empty)
        self.assertEqual((code, record["outcome"]), (2, "blocked"))
        self.assertIn("no `verify` task", record["blocked_reason"])
        # Inherited verify from a parent installation is a problem, not a reuse.
        parent = self.root / "installation"
        (parent / "projects").mkdir(parents=True)
        (parent / "mise.toml").write_text('[tasks]\nverify = "echo parent"\n')
        nested = self.raw_repo("cli", parent / "projects/cli")
        code, written, _ = self.scaffold(nested)
        self.assertTrue(any("inherited from a parent directory" in p for p in written["problems"]))
        self.assertIn("mise run test", written["plan"]["commands"])  # Its own `test` is still what the new task would reuse.

    # -- maintenance: change detection and stale claims ---------------------------------------------
    def test_change_detection_affected_maps_rationale_and_deliberately_failing_branch(self):
        repo = self.raw_repo("service", self.root / "maint")
        self.scaffold(repo, "--write")
        self.fill(repo, "service")
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-q", "-m", "contract")
        base = self.git(repo, "rev-parse", "HEAD")
        code, record, _ = self.runner(repo, "--base", base)
        self.assertEqual(record["certifies"], base)
        code, audit, _ = self.audit(repo, "--base", base)
        self.assertEqual((code, audit["outcome"]), (0, "clean"), audit["findings"])
        self.assertEqual(audit["changes"]["files"], [])
        # A real behavior change: /health also reports a version. The map that references app.py is affected; nothing is unmapped.
        self.git(repo, "checkout", "-q", "-b", "feature")
        app = repo / "app.py"
        app.write_text(app.read_text().replace('{"ok": True}', '{"ok": True, "version": 2}'))
        test = repo / "tests/test_app.py"
        test.write_text(test.read_text().replace('(200, {"ok": True})', '(200, {"ok": True, "version": 2})'))
        code, audit, _ = self.audit(repo, "--base", base)
        self.assertEqual((code, audit["outcome"]), (0, "clean"), audit["findings"])
        affected = {n["file"]: n["files"] for n in audit["notes"] if n["kind"] == "affected-map"}
        self.assertEqual(set(affected), {"docs/features/service.health.md", "docs/features/service.items.md"})
        self.assertEqual(affected["docs/features/service.health.md"], ["app.py", "tests/test_app.py"])
        self.assertEqual(audit["changes"]["unmapped"], [])
        self.assertEqual(audit["runs"]["proof"], "current")  # Maps unchanged, so the earlier record still describes them; the code did change, which the runner, not the audit, proves.
        # An internal file no map references: a finding until a rationale is recorded with the audit.
        (repo / "util.py").write_text("def helper():\n    return 1\n")
        code, audit, _ = self.audit(repo, "--base", base)
        self.assertEqual((code, audit["outcome"]), (1, "findings"))
        self.assertEqual([f["file"] for f in audit["findings"] if f["kind"] == "unmapped-change"], ["util.py"])
        code, audit, _ = self.audit(repo, "--base", base, "--rationale", "util.py is an internal helper with no user-facing path")
        self.assertEqual((code, audit["outcome"]), (0, "clean"))
        note = next(n for n in audit["notes"] if n["kind"] == "no-map-change")
        self.assertEqual((note["files"], note["detail"]), (["util.py"], "util.py is an internal helper with no user-facing path"))
        self.assertEqual(json.loads((repo / audit["record"]).read_text())["changes"]["rationale"], note["detail"])
        # Updating the affected map is an authored change: flagged as policy for root review, and the earlier proof becomes stale.
        health = repo / "docs/features/service.health.md"
        health.write_text(health.read_text().replace("| run record |", "| run record |\n| `service.health.version` | `/health` also reports `version` | automated: tests/test_app.py | run record |", 1))
        code, audit, _ = self.audit(repo, "--base", base, "--rationale", "x")
        self.assertEqual(audit["outcome"], "clean", audit["findings"])
        self.assertIn("docs/features/service.health.md", next(n for n in audit["notes"] if n["kind"] == "policy-changed")["files"])
        self.assertEqual(audit["runs"]["proof"], "stale")
        self.assertIn("service.health.version", [s["id"] for s in audit["authored"]["scenarios"]])
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-q", "-m", "version in health and its map row")
        code, record, _ = self.runner(repo, "--base", base)
        self.assertEqual(record["outcome"], "pass")
        self.assertTrue(record["requires_root_review"])  # The candidate changed a map: it cannot certify itself.
        self.assertIsNone(record["certifies"])
        self.assertIn("docs/features/service.health.md", record["policy"]["changed"])
        # A deliberately failing branch: the test contradicts the code. The runner fails; the audit does not paper over it.
        self.git(repo, "checkout", "-q", "-b", "failing")
        source = test.read_text()
        self.assertIn('"version": 2})', source)
        test.write_text(source.replace('"version": 2})', '"version": 3})'))
        self.assertIn('"version": 3})', test.read_text())
        self.git(repo, "commit", "-qam", "wrong expectation")
        # The earlier runner in this method compiled tests/test_app.py. A same-second rewrite can leave a timestamp-valid
        # .pyc of the passing tests, so the next discover run would not see the wrong expectation.
        cache = repo / "tests/__pycache__"
        if cache.is_dir():
            shutil.rmtree(cache)
        code, record, _ = self.runner(repo, "--base", base)
        self.assertEqual((code, record["outcome"]), (1, "fail"))
        self.assertEqual({s["status"] for s in record["scenarios"] if s["driver"] == "automated"}, {"fail"})
        code, audit, _ = self.audit(repo, "--base", base, "--rationale", "util.py is an internal helper with no user-facing path")
        self.assertEqual(audit["runs"]["outcome"], "fail")
        self.assertEqual(audit["outcome"], "clean", audit["findings"])  # The map still describes the product truthfully; a failing run is not a map problem to edit away.

    def test_audit_detects_stale_tasks_paths_links_and_coverage_claims(self):
        repo = self.raw_repo("cli", self.root / "stale")
        self.scaffold(repo, "--write")
        self.fill(repo, "cli")
        self.git(repo, "add", "-A")
        self.git(repo, "commit", "-q", "-m", "contract")
        code, audit, _ = self.audit(repo)
        self.assertEqual((code, audit["outcome"]), (0, "clean"), audit["findings"])
        (repo / "mise.toml").write_text('[tasks]\nunit = "python3 -m unittest discover -s tests -p \'test_*.py\'"\n\n[tasks.verify]\nrun = "mise run unit"\n')  # `test` renamed.
        (repo / "tests/test_hello.py").rename(repo / "tests/test_greeting.py")  # Path referenced by the map moved.
        (repo / "docs/features/orphan.md").write_text("# Orphan\n\n| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `cli.hello` | dup | sometimes | - |\n| `cli.extra` | claims | automated | - |\n")
        index = repo / "docs/features/README.md"
        index.write_text(index.read_text() + "- [Gone](gone.md)\n")
        code, audit, _ = self.audit(repo)
        self.assertEqual((code, audit["outcome"]), (1, "findings"))
        by_kind = {}
        for finding in audit["findings"]:
            by_kind.setdefault(finding["kind"], []).append(finding)
        self.assertTrue(any("mise run test" in f["detail"] for f in by_kind["stale-task"]))
        self.assertTrue(any("tests/test_hello.py" in f["detail"] for f in by_kind["stale-path"]))
        self.assertTrue(any("tests/test_hello.py" in f["detail"] for f in by_kind["coverage-claim"]))
        self.assertEqual([f["file"] for f in by_kind["unlinked-map"]], ["docs/features/orphan.md"])
        self.assertTrue(any("gone.md" in f["detail"] for f in by_kind["missing-link"]))
        self.assertNotIn("duplicate-id", by_kind)  # The orphan is not linked, so its rows are not part of the contract; linking it would surface bad-driver and duplicate-id.
        index.write_text(index.read_text().replace("- [Gone](gone.md)\n", "- [Orphan](orphan.md)\n"))
        code, audit, _ = self.audit(repo)
        kinds = {f["kind"] for f in audit["findings"]}
        self.assertTrue({"bad-driver", "coverage-claim"} <= kinds)
        self.assertTrue(any(f["kind"] == "coverage-claim" and "names no test" in f["detail"] for f in audit["findings"]))
        self.assertFalse(any(f["kind"] == "duplicate-id" for f in audit["findings"]))  # The `sometimes` row is rejected before its id counts.
        (repo / "docs/features/orphan.md").write_text("| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `cli.hello` | dup | manual | - |\n")
        code, audit, _ = self.audit(repo)
        self.assertTrue(any(f["kind"] == "duplicate-id" for f in audit["findings"]))
        self.assertTrue((repo / "docs/features/cli.hello.md").read_text())  # Nothing was edited by the audit.
        self.assertEqual(self.git(repo, "status", "--porcelain", "--", "docs/features/cli.hello.md", "VERIFY.md"), "")

    # -- external clone with sum absent ----------------------------------------------------------------
    def test_external_clone_follows_every_generated_instruction_with_sum_absent(self):
        origin = self.raw_repo("service", self.root / "origin")
        self.scaffold(origin, "--write")
        self.fill(origin, "service")
        self.git(origin, "add", "-A")
        self.git(origin, "commit", "-q", "-m", "contract")
        external = self.root / "elsewhere/service"
        external.parent.mkdir()
        subprocess.run(["git", "clone", "-q", str(origin), str(external)], check=True)
        shutil.rmtree(self.toolkit.parent.parent)  # The toolkit that generated the files is gone; the clone must stand alone.
        for path in external.rglob("*"):
            if path.is_file() and ".git" not in path.parts:
                self.assertNotIn(str(ROOT), path.read_text(errors="replace"), path)  # Nothing points back at sum's checkout.
        self.assertFalse(any(k.startswith(("SUM_", "HERDR_")) for k in self.env))
        # The generated `next` steps, in order, from the clone: check, audit, run with base, capture, teardown.
        code, record, result = self.runner(external, "--check")
        self.assertEqual((code, record["outcome"]), (0, "checked"), result.stderr)
        code, audit, result = self.audit(external)
        self.assertEqual((code, audit["outcome"]), (0, "clean"), result.stderr)
        head = self.git(external, "rev-parse", "HEAD")
        code, record, result = self.runner(external, "--base", head)
        self.assertEqual((code, record["outcome"], record["certifies"]), (0, "pass", head), result.stderr)
        data = self.root / "data-external"
        _, url = self.start(external, "app.py", env={"ITEMS_DATA_DIR": str(data)})
        code, _, result = self.capture(external, "--feature", "service.health", "http", "--expect-status", "200", "GET", url + "/health")
        self.assertEqual(code, 0, result.stdout + result.stderr)
        code, _, result = self.capture(external, "--feature", "service.health", "--scenario", "service.health.unknown-route", "http", "--expect-status", "404", "GET", url + "/nope")
        self.assertEqual(code, 0, result.stdout + result.stderr)  # An expected error path is evidence too.
        code, _, result = self.capture(external, "--feature", "service.health", "http", "--expect-status", "500", "GET", url + "/health")
        self.assertEqual(code, 1)  # An unmet expectation is recorded and reported, never silently passed.
        self.assertIn("UNMET", result.stdout)
        self.stop_servers()
        shutil.rmtree(data, ignore_errors=True)  # Health alone writes nothing; the data directory exists only when items were stored.
        run_dir = external / record["artifacts"]["run_dir"]
        evidence = sorted(p.name for p in (run_dir / "evidence").glob("*.json"))
        self.assertEqual(len(evidence), 3)
        self.assertTrue(evidence[0].startswith("001-service.health") and evidence[1].startswith("002-service.health.unknown-route"))
        self.assertFalse(json.loads((run_dir / "evidence" / evidence[2]).read_text())["met"])
        code, audit, _ = self.audit(external)
        self.assertEqual((audit["runs"]["proof"], audit["runs"]["outcome"]), ("current", "pass"))
        # Native harness discovery aliases are thin symlinks into the one project-local owner.
        self.assertEqual(os.readlink(external / ".claude/skills/maintain-verification"), "../../.agents/skills/maintain-verification")

    # -- sum's own maps pass the audit ----------------------------------------------------------------
    def test_sum_repository_maps_audit_clean(self):
        code, audit, result = self.run_py(ROOT / AUDIT, ROOT, "--json", "--no-record", cwd=ROOT, env={"FAKE_MISE_STOP": str(ROOT)})
        self.assertEqual((code, audit["outcome"]), (0, "clean"), (audit.get("findings"), result.stderr))
        self.assertIn("verify.three-checkouts", [s["id"] for s in audit["authored"]["scenarios"]])
        self.assertFalse(audit["authored"]["inventory_incomplete"])


if __name__ == "__main__":
    unittest.main()
