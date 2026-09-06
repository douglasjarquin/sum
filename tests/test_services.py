"""Issue #17: start and clean up only explicitly task-owned development resources."""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import sys
import time
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("test_core", ROOT / "tests/test_core.py")
core = importlib.util.module_from_spec(spec)
spec.loader.exec_module(core)
sumctl = core.sumctl

MISE = '[tasks]\ndev = "python3 -m http.server 8001"\nworker = "python3 worker.py"\n'


class ServiceLab:
    """Shared fixture helpers over the fake Herdr (split/run/send-keys/process-info), fake lsof, and fake gh."""

    def lab(self):
        self.gh_root = self.root / "fake-gh"
        self.lsof_root = self.root / "fake-lsof"
        patch = mock.patch.dict(os.environ, {"SUM_GH_BIN": str(ROOT / "tests/fixtures/gh.py"), "FAKE_GH_ROOT": str(self.gh_root),
                                             "SUM_LSOF_BIN": str(ROOT / "tests/fixtures/lsof.py"), "FAKE_LSOF_ROOT": str(self.lsof_root)})
        patch.start()
        self.addCleanup(patch.stop)
        self.lsof()

    def lsof(self, processes=(), listeners=()):
        self.lsof_root.mkdir(exist_ok=True)
        (self.lsof_root / "cwds.json").write_text(json.dumps({"processes": list(processes), "listeners": list(listeners)}))

    def lsof_now(self):
        return json.loads((self.lsof_root / "cwds.json").read_text())

    def fake_state(self):
        return json.loads((self.root / "fake/state.json").read_text())

    def write_fake_state(self, state):
        (self.root / "fake/state.json").write_text(json.dumps(state))

    def set_pane(self, pane_id, **changes):
        state = self.fake_state()
        state["panes"].setdefault(pane_id, {"pane_id": pane_id, "cwd": "/tmp", "workspace_id": pane_id.split(":")[0], "agent_status": "unknown", "agent": None})
        state["panes"][pane_id].update(changes)
        state["workspaces"].setdefault(pane_id.split(":")[0], {"workspace_id": pane_id.split(":")[0], "label": "", "worktree": None})
        self.write_fake_state(state)

    def write(self, task, relative, text):
        path = Path(task["worktree"]) / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return path

    def record(self, task):
        return json.loads(Path(sumctl.environment_path(self.store, task["id"])).read_text())

    def envctl(self, *args, ok=True, env=None):
        result = self.cli("env", *args, env=env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        return json.loads(result.stderr)

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

    def discovered(self, task, text=MISE):
        self.write(task, "mise.toml", text)
        self.envctl("discover", task["id"])
        return task

    def start(self, task, command="dev", listen="127.0.0.1:8001", url="http://127.0.0.1:8001", ok=True, timeout=None, **extra):
        env = {"FAKE_RUN_LISTEN": listen} if listen else {}
        env.update(extra)
        args = ["start", task["id"], "--command", command]
        if url:
            args += ["--url", url]
        if timeout is not None:
            args += ["--timeout", str(timeout)]
        return self.envctl(*args, ok=ok, env=env)

    def stop(self, task, *args, ok=True):
        return self.envctl("stop", task["id"], *args, ok=ok)

    def calls(self):
        path = self.root / "fake/calls.jsonl"
        return [json.loads(line)["args"] for line in path.read_text().splitlines()] if path.exists() else []

    def calls_of(self, *prefix):
        return [c for c in self.calls() if c[:len(prefix)] == list(prefix)]

    def processes(self, pane_id):
        return self.fake_state()["panes"].get(pane_id, {}).get("processes")


class ServiceTest(ServiceLab, core.CoreTest):
    """Launch, readiness, ownership proof, and stop. Inherited core cases run in test_core only."""

    def setUp(self):
        super().setUp()
        self.lab()

    # --- launch and stop ---------------------------------------------------------------------------------------------

    def test_two_task_servers_and_a_shared_database_stop_independently(self):
        first = self.discovered(self.prepare())
        second = self.discovered(self.prepare(repo=str(self.second_repo())), '[tasks]\ndev = "python3 -m http.server 8002"\n')
        self.lsof(processes=[{"pid": 900, "cwd": "/opt/homebrew"}], listeners=[{"pid": 900, "address": "127.0.0.1:5432"}])
        db = self.envctl("record", first["id"], "--url", "postgres://localhost:5432/app", "--ownership", "shared")["endpoint"]
        self.assertEqual(db["ownership"], "shared")
        a = self.start(first)
        b = self.start(second, listen="127.0.0.1:8002", url="http://127.0.0.1:8002")
        sa, sb = a["service"], b["service"]
        self.assertEqual((sa["state"], sa["launch"]["via"], sa["launch"]["pane_created"], sa["command"]), ("ready", "herdr-pane", True, "mise run dev"))
        self.assertEqual((sb["state"], sb["port"]), ("ready", 8002))
        self.assertNotEqual(sa["pane"], sb["pane"])
        self.assertEqual((sa["workspace"], sb["workspace"]), (first["workspace"], second["workspace"]))
        self.assertTrue(sa["process"]["pid"] and sa["process"]["argv"] == ["mise", "run", "dev"] and sa["process"]["shell_pid"])
        self.assertEqual((a["endpoint"]["ownership"], a["endpoint"]["service"]), ("owned", sa["id"]))
        # The record shows intent before the pane and the pane before the process.
        events = [h["event"] for h in sa["history"]]
        self.assertEqual(events[:3], ["pane-recorded", "process-observed", "readiness"])
        self.assertEqual(sa["intent_at"] <= sa["launched_at"], True)
        self.assertIn("start-intent", [h["event"] for h in self.record(first)["history"]])
        # Stopping the first task's service interrupts exactly its pane; the second server and the shared database keep running.
        result = self.stop(first)
        self.assertEqual((result["stopped"], result["refused"], result["pending"]), ([sa["id"]], [], []))
        self.assertEqual(self.calls_of("pane", "send-keys"), [["pane", "send-keys", sa["pane"], "ctrl+c"]])
        self.assertEqual(self.calls_of("pane", "close"), [["pane", "close", sa["pane"]]])
        self.assertNotIn(sa["pane"], self.fake_state()["panes"])
        self.assertEqual(self.processes(sb["pane"])[0]["pid"], sb["process"]["pid"])
        listeners = {l["address"] for l in self.lsof_now()["listeners"]}
        self.assertEqual(listeners, {"127.0.0.1:5432", "127.0.0.1:8002"})
        saved = next(s for s in self.record(first)["services"] if s["id"] == sa["id"])
        self.assertEqual((saved["state"], saved["exit_verified"], saved["stop"]["result"]), ("stopped", True, "exited"))
        pane_row = next(r for r in self.record(first)["resources"] if r["id"] == sa["pane"])
        self.assertEqual((pane_row["ownership"], pane_row["state"], pane_row["service"]), ("owned", "closed", sa["id"]))
        # The shared database was never a stop target and stays recorded as shared.
        self.assertEqual(next(e for e in self.record(first)["endpoints"] if e["port"] == 5432)["ownership"], "shared")
        self.assertEqual(self.stop(first)["services"], [])  # Nothing active: nothing sent.
        self.assertEqual(len(self.calls_of("pane", "send-keys")), 1)

    def test_port_collision_is_reported_never_terminated(self):
        task = self.discovered(self.prepare())
        self.lsof(processes=[{"pid": 777, "cwd": "/Users/someone/elsewhere"}], listeners=[{"pid": 777, "address": "127.0.0.1:8001"}])
        error = self.start(task, ok=False)["error"]
        self.assertIn("Port 8001 is already taken", error)
        self.assertIn("never terminates", error)
        self.assertEqual(self.calls_of("pane", "split"), [])
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertEqual(self.lsof_now()["listeners"], [{"pid": 777, "address": "127.0.0.1:8001"}])
        conflict = self.record(task)["services"][0]
        self.assertEqual((conflict["state"], conflict["pane"], conflict["conflict"]["listeners"][0]["pid"]), ("conflict", None, 777))
        # A URL another active task owns is refused before any pane exists.
        other = self.discovered(self.prepare(repo=str(self.second_repo())))
        self.lsof()
        self.start(task, listen="127.0.0.1:8003", url="http://127.0.0.1:8003")
        self.assertIn("owned by another active task", self.start(other, listen="127.0.0.1:8003", url="http://127.0.0.1:8003", ok=False)["error"])
        self.assertEqual(len(self.calls_of("pane", "split")), 1)

    def test_reused_pane_or_pid_is_not_the_recorded_instance_and_nothing_is_sent(self):
        task = self.discovered(self.prepare())
        service = self.start(task)["service"]
        pane = service["pane"]
        # A different pid with the same argv: the pane was restarted outside sum.
        state = self.fake_state()
        state["panes"][pane]["processes"][0]["pid"] = service["process"]["pid"] + 1
        self.write_fake_state(state)
        result = self.stop(task)
        self.assertEqual((result["refused"], result["stopped"]), ([service["id"]], []))
        self.assertIn("differ from the recorded instance", result["services"][0]["reasons"][0])
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertEqual(self.record(task)["services"][0]["state"], "unknown")
        # Same pid, but the pane's shell changed: a reused pane.
        state = self.fake_state()
        state["panes"][pane]["processes"][0]["pid"] = service["process"]["pid"]
        state["panes"][pane]["shell_pid"] = 9999
        self.write_fake_state(state)
        result = self.stop(task)
        self.assertIn("shell pid changed", result["services"][0]["reasons"][0])
        # Same pid and argv in a pane whose cwd moved out of the checkout: not ours either.
        state = self.fake_state()
        state["panes"][pane]["shell_pid"] = service["process"]["shell_pid"]
        state["panes"][pane]["cwd"] = str(self.repo)
        self.write_fake_state(state)
        self.assertIn("not inside the task checkout", self.stop(task)["services"][0]["reasons"][0])
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertEqual(self.calls_of("pane", "close"), [])
        # Restored identity: the instance is re-proven and stops.
        state = self.fake_state()
        state["panes"][pane]["cwd"] = task["worktree"]
        self.write_fake_state(state)
        self.assertEqual(self.stop(task)["stopped"], [service["id"]])

    def test_uncertain_startup_and_failed_readiness_are_explicit_and_leave_the_pane(self):
        task = self.discovered(self.prepare())
        # The command exits at once: no process instance, no readiness.
        gone = self.start(task, listen=None, url=None, FAKE_RUN_BEHAVIOR="exit")
        self.assertEqual((gone["service"]["state"], gone["service"]["process"]["pid"]), ("unknown", None))
        self.assertIn("no foreground process appeared", gone["warning"])
        # Reconciled at the next start for the same command: the empty pane sum created is reused, not duplicated.
        failed = self.start(task, listen="none", timeout=1)
        self.assertEqual(failed["service"]["pane"], gone["service"]["pane"])
        self.assertEqual(len(self.calls_of("pane", "split")), 1)
        self.assertEqual((failed["service"]["state"], failed["service"]["readiness"]["ready"]), ("failed", False))
        self.assertIn("nothing listened on port 8001 within 1s", failed["service"]["readiness"]["reason"])
        self.assertEqual(self.calls_of("pane", "send-keys"), [])  # Failure never becomes a kill or a retry.
        self.assertEqual(len(self.processes(failed["service"]["pane"])), 1)
        self.assertIsNone(failed.get("endpoint"))
        self.assertEqual([s["state"] for s in self.record(task)["services"]], ["stopped", "failed"])
        # A failed launch is still a proven instance: stop interrupts it and closes the pane sum created (reused from the first launch).
        self.assertTrue(failed["service"]["launch"]["pane_created"])
        self.assertEqual(self.stop(task)["stopped"], [failed["service"]["id"]])
        self.assertNotIn(failed["service"]["pane"], self.fake_state()["panes"])

    def test_readiness_never_adopts_a_listener_that_is_not_the_pane_process(self):
        task = self.discovered(self.prepare())
        # After the launch, something else inside the checkout takes the port while the pane's process does not: not ours, not ready.
        result = self.start(task, timeout=1, FAKE_RUN_LISTEN_PID="4100")
        service = result["service"]
        self.assertEqual((service["state"], service["readiness"]["ready"]), ("conflict", False))
        self.assertIn("not in the service pane", service["readiness"]["reason"])
        self.assertIsNone(result.get("endpoint"))
        self.assertNotEqual(service["process"]["pid"], 4100)
        self.assertEqual([e for e in self.record(task)["endpoints"] if e["port"] == 8001], [])
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertIn({"pid": 4100, "address": "127.0.0.1:8001"}, self.lsof_now()["listeners"])

    def test_replaced_pane_process_during_readiness_is_unknown_and_stop_refuses(self):
        task = self.discovered(self.prepare())
        result = self.start(task, FAKE_RUN_REPLACE="1")
        service = result["service"]
        self.assertEqual((service["state"], service["readiness"]["ready"], service["readiness"]["changed"]), ("unknown", False, True))
        self.assertIn("foreground changed during startup", service["readiness"]["reason"])
        self.assertIsNone(result.get("endpoint"))
        observed_pid = self.processes(service["pane"])[0]["pid"]
        self.assertEqual(observed_pid, service["process"]["pid"] + 1)  # The recorded instance is the one launched, never the replacement.
        stop = self.stop(task)
        self.assertEqual((stop["refused"], stop["stopped"]), ([service["id"]], []))
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertIn("differ from the recorded instance", stop["services"][0]["reasons"][0])

    def test_retry_after_failed_readiness_never_splits_a_second_pane_while_the_first_process_runs(self):
        task = self.discovered(self.prepare())
        failed = self.start(task, listen="none", timeout=1)["service"]
        self.assertEqual(failed["state"], "failed")
        error = self.start(task, ok=False)["error"]
        self.assertIn(f"launch {failed['id']}", error)
        self.assertIn("still runs", error)
        self.assertEqual(len(self.calls_of("pane", "split")), 1)
        self.assertEqual(len(self.calls_of("pane", "run")), 1)
        self.assertEqual(len(self.processes(failed["pane"])), 1)
        self.assertEqual(self.stop(task, "--service", failed["id"])["stopped"], [failed["id"]])
        # With the failed instance gone, a new launch proceeds (and reuses no pane: the stop closed it).
        ready = self.start(task)["service"]
        self.assertEqual((ready["state"], len(self.calls_of("pane", "split"))), ("ready", 2))

    def test_writing_check_never_follows_a_replaced_directory_symlink(self):
        task = self.discovered(self.prepare())
        self.write(task, "logs/dev.log", "x\n")
        self.envctl("record", task["id"], "--log", "logs/dev.log")
        host = self.root / "host-logs"
        host.mkdir()
        (host / "dev.log").write_text("fresh\n")
        logs = Path(task["worktree"]) / "logs"
        (logs / "dev.log").unlink(); logs.rmdir()
        logs.symlink_to(host)
        self.assertEqual(sumctl.writing_logs(self.record(task), task["worktree"]), [])

    def test_immediate_exit_with_url_finishes_within_the_bound_as_unknown(self):
        task = self.discovered(self.prepare())
        started = time.monotonic()
        result = self.start(task, listen=None, timeout=2, FAKE_RUN_BEHAVIOR="exit")  # --url given; nothing ever holds the port.
        self.assertLess(time.monotonic() - started, 30)
        service = result["service"]
        self.assertEqual((service["state"], service["process"]["pid"], service["readiness"]["ready"], service["readiness"]["changed"]), ("unknown", None, False, True))
        self.assertIn("no recorded process instance", service["readiness"]["reason"])
        self.assertIsNone(result.get("endpoint"))
        self.assertEqual(self.record(task)["endpoints"], [])
        self.assertEqual(self.calls_of("pane", "send-keys"), [])

    def test_empty_argv_is_never_a_pid_only_match(self):
        recorded = {"pid": 7, "argv": ["make", "dev"]}
        self.assertFalse(sumctl.same_instance(recorded, {"pid": 7, "argv": None}))
        self.assertFalse(sumctl.same_instance({"pid": 7, "argv": None}, {"pid": 7, "argv": None}))
        self.assertTrue(sumctl.same_instance(recorded, {"pid": 7, "argv": ["/usr/bin/make", "dev"]}))

    def test_crash_between_pane_creation_and_registration_reconciles_without_a_duplicate(self):
        task = self.discovered(self.prepare())
        error = self.start(task, FAKE_SPLIT_CRASH="1", ok=False)["error"]
        self.assertIn("exited 137", error)
        intents = self.record(task)["services"]
        self.assertEqual((len(intents), intents[0]["state"], intents[0]["pane"]), (1, "intended", None))
        orphan = [p for p in self.fake_state()["panes"] if p.startswith(task["workspace"]) and p != task["pane"]]
        self.assertEqual(len(orphan), 1)
        # The next start sees an unrecorded pane in the workspace and refuses to adopt, close, or duplicate.
        error = self.start(task, ok=False)["error"]
        self.assertIn(f"Unrecorded pane(s) ['{orphan[0]}']", error)
        self.assertEqual(self.record(task)["services"][0]["state"], "lost")
        self.assertEqual(len(self.calls_of("pane", "split")), 1)
        self.assertEqual(self.calls_of("pane", "close"), [])
        # Once someone records the pane on purpose it is known, and the launch proceeds in a fresh pane.
        self.envctl("record", task["id"], "--pane", orphan[0], "--label", "orphan of the crash")
        started = self.start(task)
        self.assertEqual(started["service"]["state"], "ready")
        self.assertNotEqual(started["service"]["pane"], orphan[0])

    def test_extra_unowned_pane_blocks_launch_and_is_never_closed(self):
        task = self.discovered(self.prepare())
        self.set_pane(task["workspace"] + ":p2", cwd=task["worktree"])
        self.assertIn("Unrecorded pane(s)", self.start(task, ok=False)["error"])
        self.assertIn(task["workspace"] + ":p2", self.fake_state()["panes"])
        self.assertEqual(self.calls_of("pane", "close"), [])

    def test_duplicate_start_returns_the_running_instance(self):
        task = self.discovered(self.prepare())
        first = self.start(task)
        again = self.start(task)
        self.assertTrue(again["already_running"])
        self.assertEqual(again["service"]["id"], first["service"]["id"])
        self.assertEqual(len(self.calls_of("pane", "split")), 1)
        self.assertEqual(len(self.calls_of("pane", "run")), 1)

    def test_stubborn_process_stays_stopping_without_escalation(self):
        task = self.discovered(self.prepare())
        service = self.start(task, FAKE_RUN_BEHAVIOR="stubborn")["service"]
        result = self.stop(task, "--timeout", "1")
        self.assertEqual((result["pending"], result["stopped"]), ([service["id"]], []))
        self.assertIn("escalates nothing", result["services"][0]["reason"])
        self.assertEqual(self.calls_of("pane", "send-keys"), [["pane", "send-keys", service["pane"], "ctrl+c"]])
        self.assertEqual(self.calls_of("pane", "close"), [])
        self.assertEqual(self.record(task)["services"][0]["state"], "stopping")
        self.assertEqual(len(self.processes(service["pane"])), 1)
        outline = json.loads(self.cli("context", task["id"]).stdout)["outline"]["environment"]
        self.assertEqual(outline["services"], {"stopping": 1})

    def test_compose_projects_are_task_scoped_and_redacted_commands_are_never_reconstructed(self):
        task = self.prepare()
        self.write(task, "compose.yaml", "services:\n  db:\n    image: postgres:16\n    ports:\n      - '5433:5432'\n  web:\n    build: .\n    ports:\n      - '8080:80'\n")
        self.write(task, "Procfile", "web: python3 app.py --api_key=abcd1234efgh5678ijkl\n")
        self.write(task, "Makefile", "serve:\n\tpython3 -m http.server\n")
        self.write(task, "package.json", json.dumps({"scripts": {"dev": "vite"}}))
        self.write(task, "pnpm-lock.yaml", "lockfileVersion: 9\n")
        self.envctl("discover", task["id"])
        self.assertIn("declared in several files", self.envctl("start", task["id"], "--command", "web", ok=False)["error"])
        web = self.envctl("start", task["id"], "--command", "web", "--source", "compose.yaml")["service"]
        project = sumctl.compose_project(self.store.read(task["id"]))
        self.assertEqual(web["command"], f"docker compose -f compose.yaml --project-name {project} up web")
        self.assertEqual((web["launch"]["via"], web["launch"]["project"]), ("compose", project))
        self.assertTrue(project.startswith("sum-") and task["id"].replace("t-", "")[:12] in project)
        error = self.envctl("start", task["id"], "--command", "web", "--source", "Procfile", ok=False)["error"]
        self.assertIn("never reconstructs", error)
        self.assertEqual(self.envctl("start", task["id"], "--command", "serve", "--source", "Makefile")["service"]["command"], "make serve")
        self.assertEqual(self.envctl("start", task["id"], "--command", "dev")["service"]["command"], "pnpm run dev")
        self.assertIn("No declared command 'nope'", self.envctl("start", task["id"], "--command", "nope", ok=False)["error"])
        self.assertIn("run `env discover", self.envctl("start", self.prepare(repo=str(self.second_repo()))["id"], "--command", "dev", ok=False)["error"])

    def test_manually_started_environments_default_to_non_owned_and_are_left_alone(self):
        task = self.discovered(self.prepare())
        self.lsof(processes=[{"pid": 4100, "cwd": task["worktree"]}], listeners=[{"pid": 4100, "address": "127.0.0.1:8001"}])
        endpoint = self.envctl("record", task["id"], "--url", "http://127.0.0.1:8001")["endpoint"]
        self.assertEqual(endpoint["ownership"], "owned")  # Observation: it runs inside the checkout.
        self.assertEqual(self.stop(task)["services"], [])  # ...but it is not a sum launch, so nothing has stop authority over it.
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertEqual(self.lsof_now()["listeners"], [{"pid": 4100, "address": "127.0.0.1:8001"}])

    def test_older_environment_records_without_services_still_read_and_show(self):
        task = self.discovered(self.prepare())
        path = Path(sumctl.environment_path(self.store, task["id"]))
        record = json.loads(path.read_text())
        record.pop("services")
        path.write_text(json.dumps(record))
        shown = self.envctl("show", task["id"])["environment"]
        self.assertEqual(shown["services"], [])
        self.assertEqual(self.stop(task)["services"], [])

    def test_views_and_secrets(self):
        task = self.discovered(self.prepare())
        self.assertIn("credential-shaped", self.envctl("start", task["id"], "--command", "dev", "--match", "password=abcd1234efgh5678ijkl", ok=False)["error"])
        self.start(task, listen=None, url=None)
        self.write(task, "logs/dev.log", "starting\n")
        started = self.envctl("start", task["id"], "--command", "worker", "--log", "logs/dev.log")
        self.assertEqual((started["log"]["state"], started["log"]["service"], started["service"]["log"]), ("present", started["service"]["id"], started["log"]["path"]))
        view = json.loads(self.cli("context", task["id"], "--role", "worker").stdout)["environment"]["dev"]
        self.assertEqual([s["state"] for s in view["services"]], ["running", "running"])
        self.assertIn("start", view["commands"])
        self.assertTrue(view["services"][0]["process"]["pid"])


class ServiceCleanupTest(ServiceLab, core.CoreTest):
    """Cleanup (#10) stops only re-proven sum launches, keeps unknown ones pending, and stays idempotent."""

    def setUp(self):
        super().setUp()
        self.lab()

    def scenario(self, **value):
        self.gh_root.mkdir(exist_ok=True)
        (self.gh_root / "pr.json").write_text(json.dumps(value))

    def merged_task(self):
        task = self.discovered(self.prepare())
        (Path(task["worktree"]) / "greeting.py").write_text("print('hi')\n")
        self.git("add", "greeting.py", "mise.toml", cwd=task["worktree"])
        self.git("commit", "-m", "add greeting", cwd=task["worktree"])
        sha = self.git("rev-parse", "HEAD", cwd=task["worktree"])
        handoff = self.root / "handoff.json"
        handoff.write_text(json.dumps({"outcome": "completed", "candidate": sha, "next_action": "coordinator verification and PR", "review": "none"}))
        sumctl.report(self.store, argparse.Namespace(task=task["id"], text="done", file=None, handoff=str(handoff)))
        self.scenario(repository="douglasjarquin/project", number=7, head_branch=task["branch"], head_sha=sha, state="MERGED", merged_at="2026-09-06T00:00:00Z", merge_commit="f" * 40)
        sumctl.pr_reconcile(self.store, argparse.Namespace(task=task["id"], number=7, repo=None, replace=False))
        return self.store.read(task["id"])

    def cleanup(self, task, apply=False):
        return sumctl.cleanup(self.store, argparse.Namespace(task=task["id"], apply=apply, reviewer_only=False, number=None))

    def test_cleanup_stops_the_proven_service_then_removes_and_repeats_idempotently(self):
        task = self.merged_task()
        service = self.start(task)["service"]
        # The service pane's shell sits in the checkout like any real shell; it is the proven service, not an anonymous occupant.
        self.assertIn({"pid": service["process"]["shell_pid"], "cwd": task["worktree"]}, self.lsof_now()["processes"])
        plan = self.cleanup(task)
        self.assertEqual(([b["code"] for b in plan["blockers"]], plan["stoppable"]), (["service"], [service["id"]]))
        self.assertIn("cleanup --apply stops it gracefully first", plan["blockers"][0]["detail"])
        self.assertEqual(len(self.processes(service["pane"])), 1)  # Inspection stops nothing.
        result = self.cleanup(task, apply=True)
        self.assertEqual((result["state"], result["archived"], result["removed"]["performed"]), ("complete", True, True))
        self.assertEqual(self.calls_of("pane", "send-keys"), [["pane", "send-keys", service["pane"], "ctrl+c"]])
        self.assertNotIn(service["pane"], self.fake_state()["panes"])
        self.assertFalse(Path(task["worktree"]).exists())
        steps = [h["step"] for h in self.store.read(task["id"])["cleanup"]["history"]]
        self.assertLess(steps.index("services-stopped"), steps.index("intent"))
        self.assertEqual((self.lsof_now()["listeners"], self.lsof_now()["processes"]), ([], []))  # Server and pane shell both gone.
        again = self.cleanup(task, apply=True)
        self.assertTrue(again["already"])
        self.assertEqual(len(self.calls_of("pane", "send-keys")), 1)

    def test_service_restarted_outside_sum_keeps_cleanup_pending(self):
        task = self.merged_task()
        service = self.start(task)["service"]
        state = self.fake_state()
        state["panes"][service["pane"]]["processes"][0]["pid"] = 31337
        self.write_fake_state(state)
        with self.assertRaisesRegex(sumctl.SumError, r"\[service-unknown\].*not the recorded instance"):
            self.cleanup(task, apply=True)
        saved = self.store.read(task["id"])
        self.assertEqual((saved["cleanup"]["state"], saved["status"] != "archived"), ("blocked", True))
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertTrue(Path(task["worktree"]).is_dir())
        self.assertEqual(len(self.processes(service["pane"])), 1)
        self.assertEqual(sumctl.cleanup_pending(saved)["blockers"], ["service-unknown", "occupant"])

    def test_still_writing_log_and_stubborn_service_keep_cleanup_pending(self):
        task = self.merged_task()
        log = self.write(task, "logs/dev.log", "line\n")
        service = self.envctl("start", task["id"], "--command", "dev", "--log", "logs/dev.log", env={"FAKE_RUN_LISTEN": "127.0.0.1:8001"})["service"]
        self.git("add", "logs/dev.log", cwd=task["worktree"])  # Committed content: a later mtime alone is not an artifact, only a sign of writing.
        self.git("commit", "-m", "add log", cwd=task["worktree"])
        sha = self.git("rev-parse", "HEAD", cwd=task["worktree"])
        handoff = self.root / "handoff2.json"
        handoff.write_text(json.dumps({"outcome": "completed", "candidate": sha, "next_action": "verify", "review": "none"}))
        sumctl.report(self.store, argparse.Namespace(task=task["id"], text="done", file=None, handoff=str(handoff)))
        self.scenario(repository="douglasjarquin/project", number=7, head_branch=task["branch"], head_sha=sha, state="MERGED", merged_at="2026-09-06T00:00:00Z", merge_commit="f" * 40)
        sumctl.pr_reconcile(self.store, argparse.Namespace(task=task["id"], number=7, repo=None, replace=True))
        os.utime(log, None)
        plan = self.cleanup(task)
        self.assertEqual(sorted(b["code"] for b in plan["blockers"]), ["service", "writing"])
        self.assertEqual(self.calls_of("pane", "send-keys"), [])  # Inspection interrupts nothing.
        # Apply stops the proven service first (one interrupt), then the still-fresh log keeps the removal pending.
        with self.assertRaisesRegex(sumctl.SumError, r"\[writing\] log .*dev.log was modified") as caught:
            self.cleanup(task, apply=True)
        self.assertNotIn("[service]", str(caught.exception))
        self.assertEqual(self.calls_of("pane", "send-keys"), [["pane", "send-keys", service["pane"], "ctrl+c"]])
        self.assertEqual(self.record(task)["services"][0]["state"], "stopped")
        self.assertNotIn(service["pane"], self.fake_state()["panes"])
        self.assertTrue(Path(task["worktree"]).is_dir())
        self.assertEqual(self.store.read(task["id"])["cleanup"]["state"], "blocked")
        # A fresh log with no stoppable service is a plain wait: nothing is interrupted.
        os.utime(log, None)
        with self.assertRaisesRegex(sumctl.SumError, r"\[writing\]"):
            self.cleanup(task, apply=True)
        self.assertEqual(len(self.calls_of("pane", "send-keys")), 1)
        old = time.time() - 600
        os.utime(log, (old, old))
        self.assertEqual(self.cleanup(task, apply=True)["state"], "complete")

    def test_stubborn_service_keeps_cleanup_pending_after_its_one_interrupt(self):
        task = self.merged_task()
        service = self.start(task, FAKE_RUN_BEHAVIOR="stubborn")["service"]
        with mock.patch.object(sumctl, "STOP_TIMEOUT", 1):
            with self.assertRaisesRegex(sumctl.SumError, r"\[service\].*still runs"):
                self.cleanup(task, apply=True)
        self.assertEqual(self.calls_of("pane", "send-keys"), [["pane", "send-keys", service["pane"], "ctrl+c"]])
        self.assertEqual(self.record(task)["services"][0]["state"], "stopping")
        self.assertTrue(Path(task["worktree"]).is_dir())
        self.assertIn(service["pane"], self.fake_state()["panes"])
        self.assertEqual(self.store.read(task["id"])["cleanup"]["state"], "blocked")

    def test_unowned_occupant_and_extra_pane_still_block_after_the_proven_service_stops(self):
        task = self.merged_task()
        service = self.start(task)["service"]
        scenario = self.lsof_now()
        scenario["processes"].append({"pid": 7777, "cwd": task["worktree"] + "/sub"})
        (self.lsof_root / "cwds.json").write_text(json.dumps(scenario))
        self.set_pane(task["workspace"] + ":p2", cwd=task["worktree"])
        with self.assertRaisesRegex(sumctl.SumError, r"\[panes\] unknown pane .*:p2.*\[occupant\].*pid 7777") as caught:
            self.cleanup(task, apply=True)
        self.assertNotIn("[service]", str(caught.exception))
        self.assertEqual(self.calls_of("pane", "send-keys"), [["pane", "send-keys", service["pane"], "ctrl+c"]])
        self.assertEqual(self.calls_of("pane", "close"), [["pane", "close", service["pane"]]])  # Only the sum-created pane; the extra pane stays.
        self.assertIn(task["workspace"] + ":p2", self.fake_state()["panes"])
        self.assertTrue(Path(task["worktree"]).is_dir())

    def test_evidence_blockers_keep_services_running(self):
        task = self.discovered(self.prepare())  # No handoff, no PR: obligations are open.
        service = self.start(task)["service"]
        with self.assertRaisesRegex(sumctl.SumError, r"\[handoff\]"):
            self.cleanup(task, apply=True)
        self.assertEqual(self.calls_of("pane", "send-keys"), [])
        self.assertEqual(len(self.processes(service["pane"])), 1)

    def test_never_uses_broad_termination(self):
        task = self.merged_task()
        self.start(task)
        self.cleanup(task, apply=True)
        forbidden = {("workspace", "close"), ("server", "stop"), ("agent", "send-keys")}
        self.assertFalse(any(tuple(c[:2]) in forbidden for c in self.calls()))
        self.assertFalse(any("--force" in c for c in self.calls()))


class ServiceUpdateTest(ServiceLab, core.UpdateLab):
    """A rolling sum update or instruction refresh never restarts a task's development service."""

    def setUp(self):
        super().setUp()
        self.lab()

    def test_update_apply_leaves_a_serving_service_untouched(self):
        root, store = self.installation()
        self.store = store
        task = self.task_fixture(store)
        self.write(task, "mise.toml", MISE)
        sumctl_bin = [sys.executable, str(ROOT / "lib/sumctl.py"), "--home", str(store.home)]
        self.assertEqual(self.cli([*sumctl_bin, "env", "discover", task["id"]]).returncode, 0)
        with mock.patch.dict(os.environ, {"FAKE_RUN_LISTEN": "127.0.0.1:8001"}):
            started = self.cli([*sumctl_bin, "env", "start", task["id"], "--command", "dev", "--url", "http://127.0.0.1:8001"])
        self.assertEqual(started.returncode, 0, started.stderr)
        service = json.loads(started.stdout)["service"]
        self.assertEqual(service["state"], "ready")
        before = json.loads((self.root / "fake/state.json").read_text())["panes"][service["pane"]]
        calls = len(self.calls())
        self.commit_upstream(root, "one.py")
        self.assertTrue(self.apply(store)["changed"])
        after = json.loads((self.root / "fake/state.json").read_text())["panes"][service["pane"]]
        self.assertEqual(after["processes"], before["processes"])
        self.assertFalse(any(c[:2] in (["pane", "send-keys"], ["pane", "close"]) for c in self.calls()[calls:]))
        self.assertEqual(self.lsof_now()["listeners"], [{"pid": service["process"]["pid"], "address": "127.0.0.1:8001"}])
        stopped = self.cli([*sumctl_bin, "env", "stop", task["id"]])
        self.assertEqual(stopped.returncode, 0, stopped.stderr)
        self.assertEqual(json.loads(stopped.stdout)["stopped"], [service["id"]])


for _name in dir(core.CoreTest):
    if _name.startswith("test_"):
        setattr(ServiceTest, _name, None)
        setattr(ServiceCleanupTest, _name, None)
for _name in dir(core.UpdateLab):
    if _name.startswith("test_"):
        setattr(ServiceUpdateTest, _name, None)


if __name__ == "__main__":
    unittest.main()
