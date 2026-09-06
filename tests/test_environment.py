"""Issue #16: task-local environment record: declared commands, observed URLs/ports, logs, and pane/container references."""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import tarfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl


class EnvironmentTest(core.CoreTest):
    """Fake Herdr, fake lsof, real Git worktrees. Inherited core cases run in test_core only."""

    def setUp(self):
        super().setUp()
        self.lsof_root = self.root / "fake-lsof"
        patch = mock.patch.dict(os.environ, {"SUM_LSOF_BIN": str(ROOT / "tests/fixtures/lsof.py"), "FAKE_LSOF_ROOT": str(self.lsof_root)})
        patch.start()
        self.addCleanup(patch.stop)
        self.lsof()

    # --- fixture helpers -------------------------------------------------------------------------------------------

    def lsof(self, processes=(), listeners=(), fail=None, fail_listeners=None):
        self.lsof_root.mkdir(exist_ok=True)
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": list(processes), "listeners": list(listeners), "fail": fail, "fail_listeners": fail_listeners}))

    def lsof_calls(self):
        path = self.lsof_root / "calls.jsonl"
        return [json.loads(line)["args"] for line in path.read_text().splitlines()] if path.exists() else []

    def envctl(self, *args, ok=True, env=None):
        result = self.cli("env", *args, env=env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        self.assertNotEqual(result.returncode, 0)
        return json.loads(result.stderr)

    def context(self, task, *args):
        result = self.cli("context", task["id"], *args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def write(self, task, relative, text):
        path = Path(task["worktree"]) / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return path

    def record(self, task):
        return json.loads(Path(sumctl.environment_path(self.store, task["id"])).read_text())

    def second_repo(self, name="other"):
        repo = self.root / name
        repo.mkdir()
        self.git("init", "-b", "main", cwd=repo)
        self.git("config", "user.name", "sum test", cwd=repo)
        self.git("config", "user.email", "test@example.invalid", cwd=repo)
        (repo / "README.md").write_text("other\n")
        self.git("add", ".", cwd=repo)
        self.git("commit", "-m", "fixture", cwd=repo)
        return repo

    # --- discovery ---------------------------------------------------------------------------------------------------

    def test_static_repo_without_dev_server_records_no_commands_and_context_stays_usable(self):
        task = self.prepare()
        before = self.context(task, "--role", "worker")
        self.assertFalse(before["environment"]["dev"]["present"])
        self.assertFalse(before["outline"]["environment"]["present"])
        found = self.envctl("discover", task["id"])
        self.assertEqual((found["sources"], found["commands"]), ([], {"verification": 0, "service": 0, "container": 0, "task": 0}))
        view = self.context(task, "--role", "worker")["environment"]["dev"]
        self.assertTrue(view["present"])
        self.assertEqual((view["discovery"]["commands"], view["endpoints"], view["logs"]), ([], [], []))
        self.assertEqual([r["id"] for r in view["resources"] if r["kind"] == "pane"], [task["pane"]])
        self.assertEqual(view["resources"][0]["ownership"], "owned")
        self.assertFalse(view["stale"])
        self.assertEqual(self.lsof_calls(), [])  # Discovery reads files; it observes no process and runs no command.

    def test_mise_tasks_package_scripts_and_compose_are_discovered_as_references_never_executed(self):
        task = self.prepare()
        marker = self.root / "executed"
        self.write(task, "mise.toml", f'[tasks.test]\nrun = "python3 -m unittest"\ndescription = "unit tests"\n\n[tasks.dev]\nrun = ["touch {marker}", "python3 -m http.server 8000"]\n\n[tasks]\nlint = "ruff check ."\n')
        self.write(task, "package.json", json.dumps({"scripts": {"start": "node server.js --port 3000", "test": "vitest", "deploy": "echo TOKEN=ghp_" + "a" * 30}}))
        self.write(task, "Makefile", "check: lint\n\tmake lint\n.PHONY: check\nVAR := x\nlint:\n\truff .\n")
        self.write(task, "compose.yaml", "services:\n  db:\n    image: postgres:16\n    ports:\n      - \"5432:5432\"\n  web:\n    build: .\n    ports: [\"8080:80\"]\nvolumes:\n  data: {}\n")
        self.write(task, "Dockerfile", "FROM python:3.13\nEXPOSE 8000 9000/tcp\n")
        self.write(task, ".devcontainer/devcontainer.json", '{\n  // comment\n  "image": "mcr/dev:1",\n  "forwardPorts": [3000, 5432]\n}\n')
        self.write(task, "Procfile", "web: gunicorn app:app\nworker: celery -A app worker\n")
        found = self.envctl("discover", task["id"])
        self.assertFalse(marker.exists())
        self.assertEqual(found["commands"], {"verification": 5, "service": 6, "container": 2, "task": 1})  # Makefile `check` counts as verification.
        commands = {(c["source"], c["name"]): c for c in self.record(task)["discovery"]["commands"]}
        self.assertEqual(commands[("mise.toml", "test")]["kind"], "verification")
        self.assertEqual(commands[("mise.toml", "dev")]["kind"], "service")
        self.assertIn("http.server 8000", commands[("mise.toml", "dev")]["command"])
        self.assertEqual(commands[("package.json", "start")]["kind"], "service")
        self.assertEqual((commands[("package.json", "deploy")]["redactions"], "ghp_" in commands[("package.json", "deploy")]["command"]), (1, False))
        self.assertEqual(sorted(n for s, n in commands if s == "Makefile"), ["check", "lint"])
        self.assertEqual((commands[("compose.yaml", "db")]["image"], commands[("compose.yaml", "db")]["declared_ports"]), ("postgres:16", [5432]))
        self.assertEqual((commands[("compose.yaml", "web")]["declared_ports"], commands[("compose.yaml", "web")]["build"]), ([8080], True))
        self.assertEqual(commands[("Dockerfile", "image")]["declared_ports"], [8000, 9000])
        self.assertEqual(commands[(".devcontainer/devcontainer.json", "devcontainer")]["declared_ports"], [3000, 5432])
        self.assertEqual(commands[("Procfile", "web")]["kind"], "service")
        self.assertNotIn("sum.yml", "".join(str(p) for p in Path(task["worktree"]).rglob("*")))
        self.assertEqual(self.git("status", "--porcelain", cwd=task["worktree"]).count("\n") + 1, 7)  # Only the fixture files; discovery wrote nothing into the checkout.
        # Declared ports are never claimed bound: the record has no endpoint until one is observed.
        self.assertEqual(self.record(task)["endpoints"], [])
        view = self.context(task, "--role", "reviewer")["environment"]["dev"]
        self.assertNotIn("ghp_", json.dumps(view))
        self.assertEqual(view["discovery"]["summary"]["service"], 6)

    def test_symlinked_and_oversized_configuration_is_skipped_not_followed(self):
        task = self.prepare()
        secret = self.root / "secret.toml"
        secret.write_text('[tasks]\nleak = "cat /etc/passwd"\n')
        os.symlink(secret, Path(task["worktree"]) / "mise.toml")
        self.write(task, "Makefile", "x" * (sumctl.CONFIG_MAX_BYTES + 1) + ":\n")
        found = self.envctl("discover", task["id"])
        self.assertEqual([(s["path"], s["skipped"].split(" ")[0]) for s in found["sources"]], [("mise.toml", "symlink"), ("Makefile", "larger")])
        self.assertEqual(found["commands"]["task"], 0)

    # --- endpoints -----------------------------------------------------------------------------------------------------

    def test_two_tasks_with_different_ports_each_own_their_url_and_never_reuse_the_other(self):
        first = self.prepare()
        second = self.prepare(repo=str(self.second_repo()))
        self.lsof(processes=[{"pid": 501, "cwd": first["worktree"]}, {"pid": 502, "cwd": second["worktree"] + "/app"}],
                  listeners=[{"pid": 501, "address": "127.0.0.1:8001"}, {"pid": 502, "address": "*:8002"}])
        a = self.envctl("record", first["id"], "--url", "http://127.0.0.1:8001/", "--label", "app")["endpoint"]
        b = self.envctl("record", second["id"], "--url", "http://localhost:8002")["endpoint"]
        self.assertEqual((a["state"], a["ownership"], a["observation"]["listeners"][0]["pid"]), ("observed", "owned", 501))
        self.assertEqual((b["state"], b["ownership"], b["observation"]["listeners"][0]["owner"]), ("observed", "owned", "this-task"))
        # The second task may not record the first task's URL as its own: the observation names the owner and the record refuses.
        error = self.envctl("record", second["id"], "--url", "http://127.0.0.1:8001", ok=False)["error"]
        self.assertIn(first["id"], error)
        self.assertNotIn("http://127.0.0.1:8001", json.dumps(self.record(second)["endpoints"]))
        # Claiming ownership of a listener inside another checkout is refused too; sharing it deliberately is recorded as shared.
        wrong = self.envctl("record", second["id"], "--url", "http://127.0.0.1:8001", "--ownership", "owned", ok=False)["error"]
        self.assertIn("another", wrong)
        shared = self.envctl("record", second["id"], "--url", "http://127.0.0.1:8001", "--ownership", "shared")["endpoint"]
        self.assertEqual((shared["ownership"], shared["conflicts"][0]["task"], shared["observation"]["listeners"][0]["owner"]), ("shared", first["id"], first["id"]))
        worker = self.context(first, "--role", "worker")["environment"]["dev"]
        self.assertEqual([(e["url"], e["ownership"], e["state"]) for e in worker["endpoints"]], [("http://127.0.0.1:8001/", "owned", "observed")])

    def test_shared_database_and_unknown_process_are_classified_by_observation_and_flag(self):
        task = self.prepare()
        self.lsof(processes=[{"pid": 900, "cwd": "/opt/homebrew"}, {"pid": 901, "cwd": "/Users/someone/elsewhere"}],
                  listeners=[{"pid": 900, "address": "127.0.0.1:5432"}, {"pid": 901, "address": "127.0.0.1:9229"}])
        db = self.envctl("record", task["id"], "--url", "postgres://localhost:5432/app", "--ownership", "shared", "--label", "team db")["endpoint"]
        self.assertEqual((db["port"], db["state"], db["ownership"], db["claimed_ownership"]), (5432, "observed", "shared", "shared"))
        unknown = self.envctl("record", task["id"], "--url", "http://127.0.0.1:9229")["endpoint"]
        self.assertEqual((unknown["state"], unknown["ownership"], unknown["observation"]["listeners"][0]["owner"]), ("observed", "unknown", "unknown"))
        # A default port that nothing listens on is recorded as not listening, never as bound.
        idle = self.envctl("record", task["id"], "--url", "http://localhost:3000")["endpoint"]
        self.assertEqual((idle["state"], idle["ownership"]), ("not-listening", "unknown"))
        remote = self.envctl("record", task["id"], "--url", "https://staging.example.test/api")["endpoint"]
        self.assertEqual((remote["state"], remote["local"], remote["port"]), ("unverified", False, 443))
        outline = self.context(task)["outline"]["environment"]
        self.assertEqual(outline["endpoints"], {"observed": 2, "not-listening": 1, "unverified": 1})
        self.assertTrue(self.context(task, "--section", "environment")["environment"]["dev"]["stale"])  # unverified/not-listening are flagged

    def test_stale_url_and_config_revision_are_marked_on_inspect_only(self):
        task = self.prepare()
        self.write(task, "mise.toml", '[tasks]\ndev = "python3 -m http.server 8000"\n')
        found = self.envctl("discover", task["id"])
        self.lsof(processes=[{"pid": 700, "cwd": task["worktree"]}], listeners=[{"pid": 700, "address": "127.0.0.1:8000"}])
        live = self.envctl("record", task["id"], "--url", "http://127.0.0.1:8000")["endpoint"]
        self.assertEqual((live["state"], live["config_revision"]), ("observed", found["config_revision"]))
        cursor = self.context(task)["cursor"]
        # The server exits and the configuration changes. Reading context reports the last observation and observes nothing.
        self.lsof()
        self.write(task, "mise.toml", '[tasks]\ndev = "python3 -m http.server 8080"\n')
        calls = len(self.lsof_calls())
        view = self.context(task, "--role", "worker")["environment"]["dev"]
        self.assertEqual((view["endpoints"][0]["state"], view["discovery"]["stale"], len(self.lsof_calls())), ("observed", False, calls))
        self.assertTrue(self.context(task, "--since", cursor)["changes"]["unchanged"])
        inspected = self.envctl("inspect", task["id"])
        self.assertTrue(inspected["changes"]["config_drift"])
        self.assertEqual(inspected["endpoints"][0]["state"], "stale")
        self.assertIn("nothing listens", inspected["endpoints"][0]["stale_reason"])
        self.assertTrue(inspected["endpoints"][0]["config_stale"])
        self.assertIn("env discover", inspected["discovery"]["stale_reason"])
        self.assertEqual(inspected["touched"], "nothing was started, stopped, or reconfigured")
        after = self.context(task, "--since", cursor)["changes"]
        self.assertEqual((after["unchanged"], after["state_changed"]), (False, True))
        view = self.context(task, "--role", "worker")["environment"]["dev"]
        self.assertEqual((view["stale"], view["discovery"]["stale"], view["endpoints"][0]["state"]), (True, True, "stale"))
        # A replacement listener with another pid is a change, not silently the same endpoint; re-discovery clears the configuration drift.
        self.lsof(processes=[{"pid": 701, "cwd": task["worktree"]}], listeners=[{"pid": 701, "address": "127.0.0.1:8000"}])
        again = self.envctl("inspect", task["id"])
        self.assertEqual(again["endpoints"][0]["state"], "observed")  # The stale record carried no pid to compare against; the new observation stands.
        refreshed = self.envctl("discover", task["id"])
        self.assertTrue(refreshed["changed"])
        self.assertFalse(self.envctl("inspect", task["id"])["changes"]["config_drift"])
        self.assertEqual(self.record(task)["endpoints"][0]["config_stale"], True)  # Recorded under the old revision until it is re-recorded.
        rerecorded = self.envctl("record", task["id"], "--url", "http://127.0.0.1:8000")["endpoint"]
        self.assertEqual((rerecorded["config_revision"], rerecorded["history"][-1]["state"], rerecorded.get("config_stale")), (refreshed["config_revision"], "observed", None))
        self.assertLessEqual(len(self.record(task)["history"]), sumctl.ENVIRONMENT_HISTORY)

    def test_unavailable_lsof_is_uncertainty_not_emptiness(self):
        task = self.prepare()
        self.lsof(fail_listeners="permission denied")
        row = self.envctl("record", task["id"], "--url", "http://127.0.0.1:8000")["endpoint"]
        self.assertEqual((row["state"], row["ownership"]), ("unverified", "unknown"))
        self.assertIn("permission denied", row["observation"]["error"])

    # --- logs, panes, containers -----------------------------------------------------------------------------------------

    def test_missing_logs_symlinks_and_paths_outside_the_checkout_are_reported_never_read(self):
        task = self.prepare()
        missing = self.envctl("record", task["id"], "--log", "logs/dev.log")["log"]
        self.assertEqual((missing["state"], missing["scope"], missing["path"]), ("missing", "checkout", str(Path(task["worktree"]) / "logs/dev.log")))
        self.write(task, "logs/dev.log", "secret=abcdefgh12345 line\n")
        present = self.envctl("inspect", task["id"])["logs"][0]
        self.assertEqual((present["state"], present["bytes"]), ("present", 26))
        self.assertNotIn("abcdefgh12345", json.dumps(self.record(task)))
        os.symlink("/etc/hosts", Path(task["worktree"]) / "hosts.log")
        link = self.envctl("record", task["id"], "--log", "hosts.log")["log"]
        self.assertEqual(link["state"], "symlink-not-followed")
        outside = self.envctl("record", task["id"], "--log", str(self.root / "service.log"), "--ownership", "shared")["log"]
        self.assertEqual((outside["scope"], outside["ownership"], outside["state"]), ("outside-checkout", "shared", "missing"))
        self.assertIn("leaves the checkout", self.envctl("record", task["id"], "--log", "../escape.log", ok=False)["error"])
        self.assertIn("credential", self.envctl("record", task["id"], "--log", "/tmp/token=ghp_" + "b" * 30 + ".log", ok=False)["error"])
        view = self.context(task, "--role", "worker")["environment"]["dev"]
        self.assertEqual([l["state"] for l in view["logs"]], ["present", "symlink-not-followed", "missing"])
        self.assertEqual(self.context(task)["outline"]["environment"]["logs_missing"], 2)

    def test_panes_and_containers_are_classified_and_pane_observation_is_bounded(self):
        task = self.prepare()
        before = len([c for c in self.calls() if c[:2] == ["pane", "get"]])
        own = self.envctl("record", task["id"], "--pane", task["pane"])["resource"]
        self.assertEqual((own["ownership"], own["state"]), ("owned", "observed"))
        absent = self.envctl("record", task["id"], "--pane", "w9:p9")["resource"]
        self.assertEqual((absent["ownership"], absent["state"], absent["error"]), ("unknown", "unverified", "pane_not_found"))
        self.assertIn("cannot be recorded as owned", self.envctl("record", task["id"], "--pane", "w9:p9", "--ownership", "owned", ok=False)["error"])
        container = self.envctl("record", task["id"], "--container", "sum-db-1", "--ownership", "shared", "--label", "compose db")["resource"]
        self.assertEqual((container["kind"], container["ownership"], container["state"]), ("container", "shared", "unverified"))
        self.assertIn("--container", self.envctl("record", task["id"], "--container", "bad id!", ok=False)["error"])
        self.assertIn("exactly one", self.envctl("record", task["id"], "--pane", "w9:p9", "--container", "x", ok=False)["error"])
        self.assertEqual(len(self.record(task)["resources"]), 3)
        self.assertEqual(len([c for c in self.calls() if c[:2] == ["pane", "get"]]) - before, 2)  # One bounded observation per unknown-pane record; the own pane needs none.

    # --- redaction, validation, read-only, backups, compatibility ----------------------------------------------------------

    def test_urls_with_credentials_or_secrets_are_refused_and_views_stay_redacted(self):
        task = self.prepare()
        self.assertIn("user information", self.envctl("record", task["id"], "--url", "postgres://app:hunter22@localhost:5432/db", ok=False)["error"])
        self.assertIn("credential-shaped", self.envctl("record", task["id"], "--url", "http://localhost:8000/?access_token=abcdefgh12345678", ok=False)["error"])
        self.assertIn("scheme", self.envctl("record", task["id"], "--url", "file:///etc/passwd", ok=False)["error"])
        self.assertIn("scheme://host", self.envctl("record", task["id"], "--url", "localhost:8000", ok=False)["error"])
        self.assertIn("credential-shaped", self.envctl("record", task["id"], "--url", "http://localhost:8000", "--label", "password=supersecret1", ok=False)["error"])
        self.assertIsNone(sumctl.read_environment(self.store, task["id"]))  # Nothing was written by refusals.

    def test_env_show_and_context_are_read_only_and_a_candidate_checkout_may_only_read(self):
        task = self.prepare()
        self.envctl("discover", task["id"])
        self.lsof(processes=[{"pid": 700, "cwd": task["worktree"]}], listeners=[{"pid": 700, "address": "127.0.0.1:8000"}])
        self.envctl("record", task["id"], "--url", "http://127.0.0.1:8000")
        calls = (len(self.lsof_calls()), len(self.calls()))
        shown = self.envctl("show", task["id"])["environment"]
        self.context(task, "--role", "worker")
        self.context(task, "--role", "reviewer")
        self.assertEqual((len(self.lsof_calls()), len(self.calls())), calls)
        self.assertEqual(shown["endpoints"][0]["url"], "http://127.0.0.1:8000")
        with mock.patch.object(sumctl, "ROOT", self.root / "candidate"), mock.patch.object(sumctl, "installation_hint", lambda root: self.store.home):
            self.assertIsNone(sumctl.guard_candidate(self.store, "env-show"))
            for command in ("env-discover", "env-record", "env-inspect", "env-start", "env-stop"):
                with self.assertRaisesRegex(sumctl.SumError, "Refusing"):
                    sumctl.guard_candidate(self.store, command)
        topic = json.loads(self.cli("help", "env").stdout)
        self.assertEqual((sorted(topic["subcommands"]), topic["read_only_subcommands"]), (["discover", "inspect", "record", "show", "start", "stop"], ["show"]))

    def test_environment_record_travels_in_backups_with_exclusions_named_and_symlinks_refused(self):
        task = self.prepare()
        self.envctl("discover", task["id"])
        target = self.root / "records.tar.gz"
        result = sumctl.backup(self.store, target)
        self.assertTrue(result["manifest"]["environment_records_included"])
        self.assertIn("redacted", result["manifest"]["environment_exclusions"])
        with tarfile.open(target) as archive:
            names = archive.getnames()
        self.assertIn(f"state/tasks/{task['id']}/environment.json", names)
        self.assertFalse(any(name.endswith("mise.toml") for name in names))
        other = self.prepare(repo=str(self.second_repo()))
        os.symlink(sumctl.environment_path(self.store, task["id"]), sumctl.environment_path(self.store, other["id"]))
        self.assertIn("symlink", self.envctl("show", other["id"])["environment"]["error"])
        self.assertIn("symlink", self.envctl("discover", other["id"], ok=False)["error"])
        second = self.root / "second.tar.gz"
        sumctl.backup(self.store, second)
        with tarfile.open(second) as archive:
            self.assertNotIn(f"state/tasks/{other['id']}/environment.json", archive.getnames())

    def test_tasks_without_an_environment_record_keep_every_existing_view(self):
        task = self.prepare()
        shown = json.loads(self.cli("show", task["id"]).stdout)
        self.assertNotIn("environment", shown)
        worker = self.context(task, "--role", "worker")
        self.assertEqual(worker["sections"], ["outline", "decisions", "execution", "environment", "notes"])
        self.assertFalse(worker["environment"]["dev"]["present"])
        self.assertIn("env discover", worker["environment"]["dev"]["commands"]["discover"])
        self.assertIn("no environment record yet", self.envctl("inspect", task["id"], ok=False)["error"])
        cursor = self.context(task)["cursor"]
        self.assertTrue(self.context(task, "--since", cursor)["changes"]["unchanged"])
        self.envctl("discover", task["id"])
        self.assertFalse(self.context(task, "--since", cursor)["changes"]["unchanged"])



class EnvironmentRepairTest(EnvironmentTest):
    """Repair 1: missing worktree refuses, directory symlinks are never followed, owned claims cannot cover outside logs, references are fully redacted, observation runs before the lock."""

    def test_task_without_worktree_refuses_every_env_write_without_a_traceback(self):
        task = self.prepare()
        with self.store.lock():
            record = self.store.read(task["id"])
            record["worktree"] = None
            self.store.save(record)
        for args in (("record", task["id"], "--url", "http://127.0.0.1:8000"), ("record", task["id"], "--log", "dev.log"),
                     ("record", task["id"], "--pane", "w9:p9"), ("inspect", task["id"]), ("discover", task["id"])):
            error = self.envctl(*args, ok=False)
            self.assertIn("no recorded worktree", error["error"], args)
        self.assertIsNone(sumctl.read_environment(self.store, task["id"]))
        self.assertFalse(self.context(task, "--role", "worker")["environment"]["dev"]["present"])

    def test_directory_symlinks_under_the_checkout_are_never_followed_by_discovery_or_logs(self):
        task = self.prepare()
        host = self.root / "host"
        (host / "secrets").mkdir(parents=True)
        (host / "config.toml").write_text('[tasks]\nleak = "cat /etc/passwd"\n')
        (host / "devcontainer.json").write_text('{"image": "host/image", "forwardPorts": [1]}')
        (host / "secrets/shadow").write_text("root:hash\n")
        os.symlink(host, Path(task["worktree"]) / ".mise")
        os.symlink(host, Path(task["worktree"]) / ".devcontainer")
        os.symlink(host / "secrets", Path(task["worktree"]) / "logs")
        found = self.envctl("discover", task["id"])
        self.assertEqual(found["commands"], {"verification": 0, "service": 0, "container": 0, "task": 0})
        self.assertEqual(sorted(s["path"] for s in found["sources"]), [".devcontainer/devcontainer.json", ".mise/config.toml"])
        self.assertTrue(all(s["skipped"].startswith("symlink not followed") for s in found["sources"]))
        self.assertNotIn("leak", json.dumps(self.record(task)))
        with mock.patch.object(os, "lstat", wraps=os.lstat) as lstat:
            log = self.envctl("record", task["id"], "--log", "logs/shadow")["log"]
            self.assertFalse(any(str(host / "secrets/shadow") in str(c.args[0]) for c in lstat.call_args_list))
        self.assertEqual((log["scope"], log["state"], log["bytes"]), ("symlink-not-followed", "symlink-not-followed", None))
        self.assertIn("only a regular path inside the checkout", self.envctl("record", task["id"], "--log", "logs/shadow", "--ownership", "owned", ok=False)["error"])
        inspected = self.envctl("inspect", task["id"])["logs"][0]
        self.assertEqual((inspected["state"], inspected["bytes"]), ("symlink-not-followed", None))
        absolute = self.envctl("record", task["id"], "--log", str(Path(task["worktree"]) / "logs/shadow"))["log"]
        self.assertEqual(absolute["scope"], "symlink-not-followed")

    def test_owned_claim_cannot_cover_a_log_outside_the_checkout(self):
        task = self.prepare()
        outside = str(self.root / "service.log")
        self.assertIn("only a regular path inside the checkout", self.envctl("record", task["id"], "--log", outside, "--ownership", "owned", ok=False)["error"])
        self.assertIsNone(sumctl.read_environment(self.store, task["id"]))
        self.assertEqual(self.envctl("record", task["id"], "--log", outside)["log"]["ownership"], "unknown")

    def test_every_string_field_of_a_command_reference_is_redacted_including_url_user_information(self):
        task = self.prepare()
        self.write(task, "mise.toml", '[tasks.db]\nrun = "psql postgres://app:hunter22@localhost:5432/app"\ndescription = "uses password=supersecret1 for now"\ndepends = ["token=ghp_' + "c" * 30 + '"]\n')
        self.write(task, "Procfile", "web: curl https://user:pw@example.test/health\n")
        self.write(task, "package.json", json.dumps({"scripts": {"deploy": "curl https://deploy:s3cret@host/hook"}}))
        self.envctl("discover", task["id"])
        text = json.dumps(self.record(task))
        for secret in ("hunter22", "supersecret1", "ghp_", ":pw@", "s3cret"):
            self.assertNotIn(secret, text, secret)
        rows = {c["name"]: c for c in self.record(task)["discovery"]["commands"]}
        self.assertEqual(rows["db"]["command"], "psql postgres://[redacted]@localhost:5432/app")
        self.assertEqual(rows["db"]["redactions"], 3)
        self.assertEqual(rows["web"]["command"], "curl https://[redacted]@example.test/health")

    def test_observation_runs_before_the_store_lock(self):
        task = self.prepare()
        self.write(task, "mise.toml", '[tasks]\ndev = "python3 -m http.server 8000"\n')
        self.lsof(processes=[{"pid": 700, "cwd": task["worktree"]}], listeners=[{"pid": 700, "address": "127.0.0.1:8000"}])
        self.envctl("discover", task["id"])
        self.envctl("record", task["id"], "--url", "http://127.0.0.1:8000")
        self.envctl("record", task["id"], "--log", "dev.log")
        held = []
        real_lock = sumctl.Store.lock
        def locked(store):
            held.append(True)
            return real_lock(store)
        def spy(name):
            original = getattr(sumctl, name)
            def wrapper(*a, **k):
                self.assertFalse(held, f"{name} ran while the store lock was held")
                return original(*a, **k)
            return mock.patch.object(sumctl, name, wrapper)
        from contextlib import ExitStack
        for command, spies in (("discover", ("discover_configuration",)), ("record", ("observe_port",)), ("inspect", ("discover_configuration", "observe_port", "observe_log"))):
            held.clear()
            with ExitStack() as stack:
                stack.enter_context(mock.patch.object(sumctl.Store, "lock", locked))
                for name in spies:
                    stack.enter_context(spy(name))
                args = argparse.Namespace(task=task["id"], url="http://127.0.0.1:8000" if command == "record" else None, log=None, pane=None, container=None, ownership=None, label=None)
                getattr(sumctl, f"env_{command}")(self.store, args)
            self.assertTrue(held, command)


for _name in dir(core.CoreTest):
    if _name.startswith("test_"):
        setattr(EnvironmentTest, _name, None)
for _name in dir(EnvironmentTest):
    if _name.startswith("test_") and _name not in EnvironmentRepairTest.__dict__:
        setattr(EnvironmentRepairTest, _name, None)


if __name__ == "__main__":
    unittest.main()
