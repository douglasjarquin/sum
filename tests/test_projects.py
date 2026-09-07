"""Issue #30: managed project clones under <installation>/projects/, one registry, explicit skill delivery to workers wherever the worktree is."""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl


class ProjectLab(core.ReleaseLab):
    """A designated installation (path with spaces) reached through its entrypoint, local bare 'remote' repositories, a strict fake Herdr, fake gh/lsof/mise."""

    def setUp(self):
        super().setUp()
        self.lsof_root = self.root / "fake-lsof"
        self.lsof_root.mkdir()
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": [], "listeners": []}))
        patch = mock.patch.dict(os.environ, {"SUM_LSOF_BIN": str(ROOT / "tests/fixtures/lsof.py"), "FAKE_LSOF_ROOT": str(self.lsof_root),
                                             "SUM_GH_BIN": str(ROOT / "tests/fixtures/gh.py"), "FAKE_GH_ROOT": str(self.root / "fake-gh")})
        patch.start()
        self.addCleanup(patch.stop)
        self.root_install, self.store = self.installation()
        self.assertEqual(self.ctl("init")["role"], "coordinator")

    def ctl(self, *argv, ok=True, env=None, home=None):
        result = self.cli([self.root_install / "bin" / "sumctl", "--home", home or self.store.home, *argv], env=env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        return json.loads(result.stderr)

    def remote(self, owner="acme", repo="widgets", files=None):
        """A bare repository standing in for the hosted one; the file:// URL is passed as --remote so no network or gh is involved."""
        source = self.root / "sources" / owner / repo
        source.mkdir(parents=True)
        self.git("init", "-b", "main", cwd=source)
        self.git("config", "user.name", "sum test", cwd=source)
        self.git("config", "user.email", "test@example.invalid", cwd=source)
        for name, text in (files or {"README.md": f"{owner}/{repo}\n"}).items():
            (source / name).parent.mkdir(parents=True, exist_ok=True)
            (source / name).write_text(text)
        self.git("add", ".", cwd=source)
        self.git("commit", "-q", "-m", "fixture", cwd=source)
        bare = self.root / "remotes" / owner / f"{repo}.git"
        bare.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "clone", "-q", "--bare", str(source), str(bare)], check=True, capture_output=True)
        return bare.as_uri()

    def enroll(self, spec="acme/widgets", remote=None, ok=True, **extra):
        argv = ["project", "enroll", spec]
        if remote:
            argv += ["--remote", remote]
        for key, value in extra.items():
            argv += [f"--{key}", str(value)]
        return self.ctl(*argv, ok=ok)

    def registry(self):
        path = self.store.home / "projects.json"
        return json.loads(path.read_text()) if path.exists() else {"schema": 1, "projects": {}}

    def tracked(self):
        return subprocess.run(["git", "-C", str(self.root_install), "ls-files"], check=True, capture_output=True, text=True).stdout.splitlines()

    def brief_for(self, *argv):
        brief = self.root / "brief.md"
        brief.write_text("Add a greeting and test it. Do not publish or merge.")
        return self.ctl("prepare", "--brief", str(brief), "--harness", "codex", "--approved", *argv)


class EnrollmentTest(ProjectLab):
    def test_fresh_enrollment_clones_exactly_one_repository_under_gitignored_projects(self):
        url = self.remote()
        value = self.enroll(remote=url)
        project = value["project"]
        self.assertEqual((value["enrolled"], value["reason"], project["kind"], project["name"]), (True, "cloned", "managed", "acme/widgets"))
        self.assertEqual(Path(project["path"]), self.root_install / "projects" / "acme" / "widgets")
        self.assertTrue((Path(project["path"]) / "README.md").is_file())
        self.assertEqual(project["remote"], url)
        self.assertEqual(project["observed"]["remote_matches"], True)
        self.assertEqual(self.registry()["projects"]["acme/widgets"]["path"], project["path"])
        # Project code is invisible to sum's own repository: ignored, not tracked, and no gh call was made for a --remote enrollment.
        self.assertFalse([f for f in self.tracked() if f.startswith("projects/")])
        status = subprocess.run(["git", "-C", str(self.root_install), "status", "--porcelain", "--ignored"], check=True, capture_output=True, text=True).stdout
        self.assertIn("!! projects/", status)
        self.assertNotIn("?? projects", status)
        self.assertFalse((self.root / "fake-gh" / "calls.jsonl").exists())
        self.assertFalse(list((self.root_install / "projects" / "acme").glob(".staging-*")))
        # Only the requested repository was cloned; nothing else the "account" holds appeared.
        self.assertEqual(sorted(p.name for p in (self.root_install / "projects").rglob("*") if p.is_dir() and p.parent.name == "acme"), ["widgets"])

    def test_repeated_enrollment_is_idempotent_and_never_reclones(self):
        url = self.remote()
        first = self.enroll(remote=url)
        marker = Path(first["project"]["path"]) / "local-note.txt"
        marker.write_text("kept\n")
        again = self.enroll(remote=url)
        self.assertEqual((again["enrolled"], again["reason"]), (False, "already-enrolled"))
        self.assertEqual(again["project"]["enrolled_at"], first["project"]["enrolled_at"])
        self.assertTrue(marker.is_file())
        self.assertEqual(again["project"]["observed"]["dirty"], True)
        plain = self.enroll()  # Without --remote the recorded identity still matches; nothing is fetched or compared against gh.
        self.assertEqual(plain["reason"], "already-enrolled")
        self.assertEqual(len(self.registry()["projects"]), 1)
        other = self.enroll(remote="https://github.com/acme/widgets-mirror.git", ok=False)
        self.assertIn("different remote is refused", other["error"])

    def test_same_name_repositories_of_two_owners_and_a_non_default_host_never_collide(self):
        a, b = self.remote("acme", "tools"), self.remote("globex", "tools")
        c = self.remote("acme", "tools-on-gitlab")
        first = self.enroll("acme/tools", remote=a)["project"]
        second = self.enroll("globex/tools", remote=b)["project"]
        third = self.enroll("acme/tools", remote=c, host="gitlab.example.invalid")["project"]
        self.assertEqual(Path(first["path"]), self.root_install / "projects/acme/tools")
        self.assertEqual(Path(second["path"]), self.root_install / "projects/globex/tools")
        self.assertEqual(Path(third["path"]), self.root_install / "projects/gitlab.example.invalid/acme/tools")
        self.assertEqual((first["name"], second["name"], third["name"]), ("acme/tools", "globex/tools", "gitlab.example.invalid/acme/tools"))
        self.assertEqual(len(self.registry()["projects"]), 3)
        listing = self.ctl("project", "list")
        self.assertEqual([p["name"] for p in listing["projects"]], ["acme/tools", "gitlab.example.invalid/acme/tools", "globex/tools"])
        url_form = self.enroll("https://gitlab.example.invalid/acme/tools.git", remote=c)
        self.assertEqual(url_form["reason"], "already-enrolled")
        self.assertIn("never expanded", self.enroll("tools", ok=False)["error"])

    def test_unexpected_existing_directory_or_remote_is_refused_and_left_untouched(self):
        url = self.remote()
        canonical = self.root_install / "projects/acme/widgets"
        canonical.mkdir(parents=True)
        (canonical / "keep.txt").write_text("user data\n")
        refused = self.enroll(remote=url, ok=False)
        self.assertIn("not a Git checkout top level", refused["error"])
        self.assertEqual((canonical / "keep.txt").read_text(), "user data\n")
        self.assertNotIn("acme/widgets", self.registry()["projects"])
        shutil.rmtree(canonical)
        other = self.remote("someone", "else")
        subprocess.run(["git", "clone", "-q", other, str(canonical)], check=True, capture_output=True)
        head = self.git("rev-parse", "HEAD", cwd=canonical)
        wrong = self.enroll(remote=url, ok=False)
        self.assertIn("refusing to register a different repository", wrong["error"])
        self.assertEqual(self.git("rev-parse", "HEAD", cwd=canonical), head)
        self.assertEqual(self.git("remote", "get-url", "origin", cwd=canonical), other)
        self.assertNotIn("acme/widgets", self.registry()["projects"])
        # A --path whose origin is another repository is refused the same way.
        external = self.root / "external clone"
        subprocess.run(["git", "clone", "-q", other, str(external)], check=True, capture_output=True)
        self.assertIn("refusing to register", self.enroll(remote=url, path=external, ok=False)["error"])
        # A path inside the installation but off the canonical location is not adopted either.
        stray = self.root_install / "stray"
        subprocess.run(["git", "clone", "-q", url, str(stray)], check=True, capture_output=True)
        self.assertIn("inside the installation but not at", self.enroll(remote=url, path=stray, ok=False)["error"])
        self.assertEqual(self.registry(), {"schema": 1, "projects": {}})

    def test_symlink_escapes_are_refused_and_paths_with_spaces_work(self):
        url = self.remote()
        elsewhere = self.root / "elsewhere"
        elsewhere.mkdir()
        (self.root_install / "projects").mkdir()
        (self.root_install / "projects" / "acme").symlink_to(elsewhere)
        refused = self.enroll(remote=url, ok=False)
        self.assertIn("symlink", refused["error"])
        self.assertEqual(list(elsewhere.iterdir()), [])
        (self.root_install / "projects" / "acme").unlink()
        link = self.root / "link to clone"
        real = self.root / "real clone"
        subprocess.run(["git", "clone", "-q", url, str(real)], check=True, capture_output=True)
        link.symlink_to(real)
        self.assertIn("symlinked project path", self.enroll(remote=url, path=link, ok=False)["error"])
        adopted = self.enroll(remote=url, path=real)
        self.assertEqual((adopted["reason"], adopted["project"]["kind"], adopted["project"]["path"]), ("adopted-external", "external", str(real)))
        self.assertIn(" ", str(self.root_install))  # The installation path itself carries a space.
        self.assertEqual(self.ctl("project", "show", "acme/widgets")["project"]["observed"]["present"], True)

    def test_failed_clone_leaves_no_directory_and_no_registration(self):
        # The default host without --remote goes through gh; the strict fake refuses the call like a failed authentication would.
        refused = self.enroll("acme/widgets", ok=False)
        self.assertIn("exited 2", refused["error"])
        self.assertFalse(list((self.root_install / "projects").rglob("*")) if (self.root_install / "projects").exists() else [])
        self.assertEqual(self.registry(), {"schema": 1, "projects": {}})
        calls = [json.loads(l)["args"] for l in (self.root / "fake-gh/calls.jsonl").read_text().splitlines()]
        self.assertEqual(calls[0][:3], ["repo", "clone", "acme/widgets"])

    def test_enrolling_sum_itself_points_at_the_installation_and_clones_nothing(self):
        self.git("remote", "add", "origin", "https://github.com/douglasjarquin/sum.git", cwd=self.root_install)
        value = self.enroll("douglasjarquin/sum")
        self.assertEqual((value["reason"], value["project"]["kind"], Path(value["project"]["path"])), ("installation", "installation", self.root_install))
        self.assertFalse((self.root_install / "projects").exists())
        again = self.enroll("https://github.com/douglasjarquin/sum")
        self.assertEqual(again["reason"], "already-enrolled")
        migrate = self.ctl("project", "migrate", "douglasjarquin/sum")
        self.assertIn("the installation itself is never migrated", migrate["blockers"])
        self.assertIn("never migrated", self.ctl("project", "migrate", "douglasjarquin/sum", "--apply", ok=False)["error"])
        task = self.brief_for("--project", "douglasjarquin/sum")
        self.assertEqual(task["project"]["kind"], "installation")
        self.assertNotEqual(Path(task["worktree"]), self.root_install)

    def test_developer_pane_and_candidate_checkout_cannot_enroll(self):
        url = self.remote()
        with mock.patch.dict(os.environ, {"HERDR_PANE_ID": "w-dev:p1"}):
            self.assertEqual(self.ctl("init")["role"], "developer")
            self.assertIn("not the registered coordinator", self.enroll(remote=url, ok=False)["error"])
            self.assertEqual(self.ctl("project", "list")["projects"], [])
        self.assertFalse((self.root_install / "projects").exists())


class LegacyAndMigrationTest(ProjectLab):
    def legacy_clone(self, url, owner="acme", repo="widgets"):
        legacy = self.store.home / "projects" / owner / repo
        legacy.parent.mkdir(parents=True)
        subprocess.run(["git", "clone", "-q", url, str(legacy)], check=True, capture_output=True)
        return legacy

    def test_dirty_legacy_clone_with_an_active_worker_stays_registered_where_it_is(self):
        url = self.remote()
        legacy = self.legacy_clone(url)
        task = self.brief_for("--repo", str(legacy))   # The earlier procedure: dispatch straight from .sum/projects/<owner>/<repo>.
        (legacy / "wip.txt").write_text("uncommitted\n")
        value = self.enroll(remote=url)
        project = value["project"]
        self.assertEqual((value["reason"], project["kind"], Path(project["path"])), ("adopted-legacy", "legacy", legacy))
        self.assertEqual(project["observed"]["dirty"], True)
        self.assertEqual(project["observed"]["linked_worktrees"], [task["worktree"]])
        self.assertFalse((self.root_install / "projects").exists())  # No second clone appeared.
        self.assertEqual((legacy / "wip.txt").read_text(), "uncommitted\n")
        shown = self.ctl("project", "show", "acme/widgets")
        self.assertEqual([t["task"] for t in shown["active_tasks"]], [task["id"]])
        inspection = self.ctl("project", "migrate", "acme/widgets")
        self.assertFalse(inspection["applied"])
        self.assertTrue(any(task["id"] in b for b in inspection["blockers"]))
        self.assertTrue(any("linked Git worktrees" in b for b in inspection["blockers"]))
        refused = self.ctl("project", "migrate", "acme/widgets", "--apply", ok=False)
        self.assertIn("Migration refused", refused["error"])
        self.assertTrue(legacy.is_dir() and (legacy / "wip.txt").is_file())
        self.assertEqual(self.git("rev-parse", "HEAD", cwd=task["worktree"]), self.git("rev-parse", "HEAD", cwd=legacy))
        self.ctl("settings", "set", "--global", "2", "--per-repository", "1")
        self.assertIn("Capacity", self.brief_for("--project", "acme/widgets").get("error", "Capacity") if False else
                      self.cli([self.root_install / "bin/sumctl", "--home", self.store.home, "prepare", "--project", "acme/widgets", "--brief", str(self.root / "brief.md"), "--harness", "codex", "--approved"]).stderr)

    def test_migration_applies_only_after_zero_references_are_proven_and_keeps_git_metadata(self):
        url = self.remote()
        external = self.root / "external clone"
        subprocess.run(["git", "clone", "-q", url, str(external)], check=True, capture_output=True)
        (external / "wip.txt").write_text("uncommitted survives a rename\n")
        head = self.git("rev-parse", "HEAD", cwd=external)
        self.enroll(remote=url, path=external)
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": [{"pid": 4242, "cwd": str(external / "src")}], "listeners": []}))
        inspection = self.ctl("project", "migrate", "acme/widgets")
        self.assertTrue(any("processes run inside it: [4242]" in b for b in inspection["blockers"]))
        self.assertIn("Migration refused", self.ctl("project", "migrate", "acme/widgets", "--apply", ok=False)["error"])
        self.assertTrue(external.is_dir())
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": [], "listeners": [], "fail": "lsof: permission denied"}))
        self.assertTrue(any("process table unavailable" in b for b in self.ctl("project", "migrate", "acme/widgets")["blockers"]))
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": [], "listeners": []}))
        self.assertEqual(self.ctl("project", "migrate", "acme/widgets")["blockers"], [])
        applied = self.ctl("project", "migrate", "acme/widgets", "--apply")
        canonical = self.root_install / "projects/acme/widgets"
        self.assertTrue(applied["applied"])
        self.assertEqual((Path(applied["project"]["path"]), applied["project"]["kind"]), (canonical, "managed"))
        self.assertFalse(external.exists())
        self.assertEqual(self.git("rev-parse", "HEAD", cwd=canonical), head)
        self.assertEqual((canonical / "wip.txt").read_text(), "uncommitted survives a rename\n")
        self.assertEqual(self.registry()["projects"]["acme/widgets"]["migrated_from"], str(external))
        self.assertIn("already at the canonical path", self.ctl("project", "migrate", "acme/widgets")["blockers"])

    def test_canonical_clone_placed_by_hand_is_adopted_not_replaced(self):
        url = self.remote()
        canonical = self.root_install / "projects/acme/widgets"
        canonical.parent.mkdir(parents=True)
        subprocess.run(["git", "clone", "-q", url, str(canonical)], check=True, capture_output=True)
        (canonical / "notes.txt").write_text("mine\n")
        value = self.enroll(remote=url)
        self.assertEqual((value["reason"], value["project"]["kind"]), ("adopted-managed", "managed"))
        self.assertEqual((canonical / "notes.txt").read_text(), "mine\n")


class DeliveryTest(ProjectLab):
    def test_external_worktree_receives_pinned_skill_and_helper_paths_not_relative_ones(self):
        url = self.remote()
        self.enroll(remote=url)
        task = self.brief_for("--project", "acme/widgets")
        worktree = Path(task["worktree"])
        self.assertFalse(worktree.is_relative_to(self.root_install))  # Herdr placed the task checkout outside the installation.
        self.assertEqual(task["project"]["name"], "acme/widgets")
        self.assertEqual(task["repository"], str(self.root_install / "projects/acme/widgets"))
        brief = Path(task["brief_path"]).read_text()
        skill_path = self.root_install / "skills/worker/SKILL.md"
        digest = sumctl.sha256_text(skill_path.read_text())[:16]
        self.assertIn("## Delivered runtime", brief)
        self.assertIn(f"`{skill_path}` ({len(skill_path.read_bytes())} bytes, sha256 `{digest}`)", brief)
        self.assertIn(f"Helper: `{self.root_install / 'bin/sumctl'}`", brief)
        self.assertIn("- Project: `acme/widgets` (managed clone at", brief)
        self.assertNotIn("../../skills", brief)
        self.assertNotIn("../skills", brief)
        self.assertIn("Do not look for `bin/sumctl` or `skills/` relative to your checkout.", brief)
        # The worker's bounded context view names the same installed paths; nothing is resolved relative to the worktree.
        context = self.ctl("context", task["id"], "--role", "worker")
        skills = context["environment"]["skills"] if "skills" in context.get("environment", {}) else context["skills"]
        self.assertEqual(skills["helper"], str(self.root_install / "bin/sumctl"))
        self.assertEqual(skills["files"][0]["path"], str(skill_path))
        self.assertEqual(skills["files"][0]["sha256"], digest)

    def test_repo_path_dispatch_attaches_the_registered_identity_or_none(self):
        url = self.remote()
        path = self.enroll(remote=url)["project"]["path"]
        task = self.brief_for("--repo", path)
        self.assertEqual(task["project"]["name"], "acme/widgets")
        plain = self.root / "unregistered"
        subprocess.run(["git", "clone", "-q", url, str(plain)], check=True, capture_output=True)
        with mock.patch.dict(os.environ, {"HERDR_PANE_ID": "w-parent:p1"}):
            other = self.brief_for("--repo", str(plain))
        self.assertIsNone(other["project"])
        self.assertNotIn("- Project:", Path(other["brief_path"]).read_text())
        self.assertIn("either --repo PATH or --project NAME", self.ctl("prepare", "--repo", path, "--project", "acme/widgets", "--brief", str(self.root / "brief.md"), "--approved", ok=False)["error"])
        self.assertIn("No enrolled project", self.ctl("prepare", "--project", "acme/nothing", "--brief", str(self.root / "brief.md"), "--approved", ok=False)["error"])

    def test_missing_enrolled_clone_refuses_dispatch_without_recloning(self):
        url = self.remote()
        path = Path(self.enroll(remote=url)["project"]["path"])
        shutil.rmtree(path)
        refused = self.ctl("prepare", "--project", "acme/widgets", "--brief", str(self.root / "brief.md"), "--approved", ok=False)
        self.assertIn("directory is missing", refused["error"])
        self.assertFalse(path.exists())
        self.assertEqual(self.ctl("project", "list")["projects"][0]["observed"]["present"], False)


class NestedSessionTest(ProjectLab):
    def test_a_pane_inside_a_managed_clone_never_becomes_coordinator_or_developer(self):
        url = self.remote()
        path = self.enroll(remote=url)["project"]["path"]
        # A second installation without an owner yet: the first pane to run init would normally claim coordinator.
        fresh_root, fresh_store = self.installation("second install")
        (fresh_root / "projects/acme").mkdir(parents=True)
        subprocess.run(["git", "clone", "-q", url, str(fresh_root / "projects/acme/widgets")], check=True, capture_output=True)
        for cwd in (str(fresh_root / "projects/acme/widgets"), str(fresh_root / "projects/acme/widgets/src")):
            env = {"HERDR_PANE_ID": "w-project:p1", "FAKE_PARENT_CWD": cwd}
            result = self.cli([fresh_root / "bin/sumctl", "--home", fresh_store.home, "init"], env=env)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("A project session is not a sum session", json.loads(result.stderr)["error"])
            self.assertFalse((fresh_store.home / "context.json").exists())
            self.assertEqual(fresh_store.registrations(), [])
        # The same pane inside an enrolled external clone of this installation is refused too; a pane elsewhere still initializes.
        external = self.root / "external clone"
        subprocess.run(["git", "clone", "-q", url, str(external)], check=True, capture_output=True)
        self.enroll("acme/widgets-external", remote=url, path=external)
        refused = self.ctl("init", env={"HERDR_PANE_ID": "w-project:p1", "FAKE_PARENT_CWD": str(external)}, ok=False)
        self.assertIn("acme/widgets-external", refused["error"])
        self.assertEqual(self.ctl("init", env={"HERDR_PANE_ID": "w-other:p1", "FAKE_PARENT_CWD": str(self.root)})["role"], "developer")
        self.assertEqual(self.ctl("init")["role"], "coordinator")
        self.assertTrue(Path(path).is_dir())

    def test_dispatched_worker_pane_keeps_its_role_even_when_its_checkout_sits_under_projects(self):
        url = self.remote()
        self.enroll(remote=url)
        task = self.brief_for("--project", "acme/widgets")
        started = self.ctl("start", task["id"])
        self.assertEqual(started["status"], "running")
        # Herdr placed the worktree elsewhere; even if a worker pane's cwd were under projects/, its recorded task wins over the nesting check.
        value = self.ctl("init", env={"HERDR_PANE_ID": started["pane"], "FAKE_PARENT_CWD": str(self.root_install / "projects/acme/widgets")})
        self.assertEqual((value["role"], value["task"]), ("worker", task["id"]))


class TaskOwnershipTest(ProjectLab):
    def test_nested_project_without_its_own_verify_task_reports_inherited_parent_tasks(self):
        # The installation carries sum's mise.toml and mise-tasks (test, demo, ...); a nested clone with no configuration of its own would resolve them.
        url = self.remote(files={"README.md": "no mise here\n"})
        path = Path(self.enroll(remote=url)["project"]["path"])
        origins = sumctl.mise_task_origins(path)
        self.assertTrue(origins["available"])
        inherited = {t["name"]: t for t in origins["inherited"]}
        self.assertIn("test", inherited)
        self.assertEqual(inherited["test"]["source"], str(self.root_install / "mise-tasks/test"))
        self.assertFalse(inherited["test"]["owned"])
        self.assertEqual(origins["verification"], {"verify": False, "test": False, "inherited_verification": ["test", "verify"]})  # The installation now ships mise-tasks/verify (issue #31); a nested clone inherits it.
        self.assertIn("would execute another repository's task", origins["problem"])
        discovery = sumctl.discover_configuration(path)
        self.assertEqual(discovery["commands"], [])  # Nothing declared by the project itself becomes a command reference.
        self.assertIn(origins["problem"], discovery["problems"])
        # A project that owns `verify` and `test` shadows the parent's names; only the remaining parent tasks are inherited.
        owned_url = self.remote("acme", "owned", files={"mise.toml": '[tasks]\nverify = "python -m unittest"\ntest = "python -m unittest"\n'})
        owned = Path(self.enroll("acme/owned", remote=owned_url)["project"]["path"])
        origins = sumctl.mise_task_origins(owned)
        self.assertEqual(origins["verification"], {"verify": True, "test": True, "inherited_verification": []})
        self.assertEqual({t["name"] for t in origins["tasks"] if t["owned"]}, {"verify", "test"})
        self.assertTrue(all(t["name"] not in ("verify", "test") for t in origins["inherited"]))

    def test_env_discover_records_task_origins_for_a_worktree_under_a_foreign_parent_configuration(self):
        (self.root / "mise.toml").write_text('[tasks]\ntest = "echo parent"\nverify = "echo parent"\n')  # A parent above the Herdr worktree directory.
        url = self.remote(files={"README.md": "plain\n", "package.json": json.dumps({"scripts": {"dev": "node server.js"}})})
        self.enroll(remote=url)
        task = self.brief_for("--project", "acme/widgets")
        started = self.ctl("start", task["id"])
        value = self.ctl("env", "discover", task["id"], env={"HERDR_PANE_ID": started["pane"]})
        self.assertEqual(value["task_origins"]["verification"], {"verify": False, "test": False, "inherited_verification": ["test", "verify"]})
        self.assertTrue(any("another repository's task" in p for p in value["problems"]))
        self.assertEqual(value["commands"]["service"], 1)  # The project's own package script is still discovered.
        record = json.loads(Path(value["path"]).read_text())
        self.assertEqual([t["source"] for t in record["discovery"]["task_origins"]["inherited"]], [str(self.root / "mise.toml")] * 2)
        shown = self.ctl("env", "show", task["id"])
        self.assertIn("task_origins", json.dumps(shown))

    def test_missing_mise_is_reported_not_treated_as_no_tasks(self):
        url = self.remote()
        path = self.enroll(remote=url)["project"]["path"]
        with mock.patch.dict(os.environ, {"FAKE_MISE_MISSING": "1"}):
            origins = sumctl.mise_task_origins(path)
        self.assertEqual((origins["available"], origins["tasks"]), (True, []))
        self.assertIn("mise exited 127", origins["error"])
        with mock.patch.dict(os.environ, {"SUM_MISE_BIN": "/nonexistent/mise"}):
            origins = sumctl.mise_task_origins(path)
        self.assertFalse(origins["available"])

    @unittest.skipUnless(shutil.which("mise"), "real mise not installed")
    def test_real_mise_resolves_parent_tasks_in_a_nested_clone(self):
        url = self.remote(files={"README.md": "no mise here\n"})
        path = Path(self.enroll(remote=url)["project"]["path"])
        env = {"SUM_MISE_BIN": shutil.which("mise"), "MISE_TRUSTED_CONFIG_PATHS": str(self.root), "MISE_YES": "1"}
        with mock.patch.dict(os.environ, env):
            origins = sumctl.mise_task_origins(path)
        self.assertTrue(origins["available"], origins)
        names = {t["name"]: t for t in origins["inherited"]}
        self.assertIn("test", names, origins)
        self.assertFalse(names["test"]["owned"])
        self.assertEqual(Path(names["test"]["source"]).resolve(), (self.root_install / "mise-tasks/test").resolve())
        self.assertEqual(origins["verification"]["test"], False)


class PackagingTest(ProjectLab):
    def test_release_bundle_source_scan_and_backup_exclude_project_code_but_keep_registrations(self):
        url = self.remote(files={"README.md": "project\n", "secret-ish.txt": "project contents must never ship with sum\n"})
        self.enroll(remote=url)
        release = self.stage(self.store)
        manifest = json.loads((Path(release["release"]) / "release.json").read_text())
        self.assertFalse([f for f in manifest["files"] if f.startswith("projects/")])
        self.assertFalse((Path(release["release"]) / "projects").exists())
        self.assertFalse([f for f in sumctl.source_files(self.root_install, manifest["source"]["sha"]) if f.startswith("projects/")])
        self.assertFalse([f for f in self.tracked() if f.startswith("projects/")])
        destination = self.root / "backup.tar.gz"
        backup = self.ctl("backup", str(destination))
        self.assertEqual(backup["manifest"]["managed_projects"], {"registry_included": True, "clone_code_included": False,
                                                                   "note": "projects.json registrations travel; clone and worktree contents are the user's code-backup responsibility"})
        with tarfile.open(destination) as archive:
            names = archive.getnames()
        self.assertIn("state/projects.json", names)
        self.assertFalse([n for n in names if "secret-ish" in n or "/projects/acme" in n])

    def test_registry_is_a_records_only_state_file_and_an_unknown_schema_is_preserved(self):
        (self.store.home / "projects.json").write_text(json.dumps({"schema": 99, "projects": {}}))
        refused = self.ctl("project", "list", ok=False)
        self.assertIn("Unsupported project registry", refused["error"])
        self.assertEqual(json.loads((self.store.home / "projects.json").read_text()), {"schema": 99, "projects": {}})
        self.assertIn("help", json.dumps(self.ctl("help", "project-enroll")))  # Discoverable like every other command.
        self.assertEqual(self.ctl("help")["read_only"].count("project-list"), 1)


if __name__ == "__main__":
    unittest.main()
