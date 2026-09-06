"""Worker harness/model defaults and per-task overrides (issue #11).

Real Git, the strict fake Herdr, real sum helpers through the installation entrypoint; no model, network, or credentials.
The fake records the exact argv the helper hands to `agent start`, so every assertion is about what would actually be executed.
"""
from __future__ import annotations
import json
import os
from pathlib import Path
import sys
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_fleet
from test_core import sumctl


class LaunchTest(test_fleet.FleetLab):
    def setUp(self):
        super().setUp()
        self.install, self.store = self.installation()
        self.env = {"FAKE_PARENT_CWD": str(self.install.resolve())}
        self.count = 0
        self.settings("--global", "12")

    def dispatch(self, *argv, command="dispatch", ok=True, env=None):
        self.count += 1
        repo = self.project(f"p{self.count}")
        return self.ctl(self.install, self.store, command, "--repo", repo, "--brief", self.brief(), "--approved", *argv, env={**self.env, **(env or {})}, ok=ok)

    def start_call(self, task_id):
        rows = [c for c in self.calls() if c[:3] == ["agent", "start", task_id]]
        self.assertEqual(len(rows), 1, rows)
        return rows[0]

    def argv(self, task_id):
        call = self.start_call(task_id)
        return call[call.index("--") + 1:] if "--" in call else []

    def kind(self, task_id):
        call = self.start_call(task_id)
        return call[call.index("--kind") + 1]

    def settings(self, *argv, ok=True):
        return self.ctl(self.install, self.store, "settings", "set", *argv, env=self.env, ok=ok)

    def test_no_defaults_means_the_coordinators_own_harness_with_its_native_model(self):
        task = self.dispatch()
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("claude", []))
        launch = task["launch"]
        self.assertEqual((launch["harness"], launch["model"], launch["source"]), ("claude", None, {"harness": "root", "model": "native-default", "reasoning": "native-default"}))
        self.assertEqual(launch["root"]["harness"], "claude")
        self.assertIsNone(launch["root"]["model"])  # Herdr never exposes the root model; the record says native default, not "same model".
        self.assertEqual(launch["observed"], {"harness": "claude", "model": "not-exposed", "status": "harness-observed"})
        self.assertIn("harness claude (root), model native default (native-default)", task["confirmation"])
        self.assertIn("not runtime-verified", task["confirmation"])
        shown = self.ctl(self.install, self.store, "settings", "show")
        self.assertIsNone(shown["worker"])
        # A different root harness is followed too; nothing is hard-wired to claude.
        other = self.dispatch(env={"FAKE_PARENT_KIND": "codex"})
        self.assertEqual((self.kind(other["id"]), other["launch"]["source"]["harness"]), ("codex", "root"))

    def test_saved_defaults_are_the_one_structured_surface_and_apply_to_future_dispatches_only(self):
        (self.store.home / "preferences.md").write_text("- Preferred worker harness: grok with model grok-9\n")  # Narrative only.
        before = self.dispatch()
        self.assertEqual(self.kind(before["id"]), "claude")
        saved = self.settings("--worker-harness", "codex", "--worker-model", "gpt-5-codex", "--worker-reasoning", "high")
        self.assertEqual((saved["previous_worker"], saved["worker"]), (None, {"harness": "codex", "model": "gpt-5-codex", "reasoning": "high"}))
        self.assertEqual(json.loads((self.store.home / "settings.json").read_text()),
                         {"schema": 1, "capacity": {"global": 12, "per_repository": 1}, "worker": {"harness": "codex", "model": "gpt-5-codex", "reasoning": "high"}})
        self.assertEqual(oct((self.store.home / "settings.json").stat().st_mode & 0o777), "0o600")
        self.assertEqual(self.ctl(self.install, self.store, "settings", "show")["worker"], saved["worker"])
        # The running task is untouched; capacity edits keep the worker block and vice versa.
        self.assertEqual(self.ctl(self.install, self.store, "show", before["id"])["launch"]["harness"], "claude")
        self.assertEqual(self.settings("--global", "4")["worker"], saved["worker"])
        self.settings("--global", "12")
        self.assertEqual(self.settings("--worker-reasoning", "medium")["worker"], {"harness": "codex", "model": "gpt-5-codex", "reasoning": "medium"})
        self.assertEqual(json.loads((self.store.home / "settings.json").read_text())["capacity"]["global"], 12)
        after = self.dispatch()
        self.assertEqual((self.kind(after["id"]), self.argv(after["id"])), ("codex", ["-m", "gpt-5-codex", "-c", "model_reasoning_effort=medium"]))
        self.assertEqual(after["launch"]["source"], {"harness": "saved-default", "model": "saved-default", "reasoning": "saved-default"})
        self.assertEqual(after["admission"]["source"], "settings.json")
        # Changing the saved harness drops the old harness's model instead of carrying a Codex model to another CLI.
        switched = self.settings("--worker-harness", "claude")
        self.assertEqual(switched["worker"], {"harness": "claude"})
        cleared = self.settings("--clear-worker")
        self.assertIsNone(cleared["worker"])
        self.assertNotIn("worker", json.loads((self.store.home / "settings.json").read_text()))
        self.assertEqual(self.kind(self.dispatch()["id"]), "claude")
        # Validation before any write: a model without a harness, an unverified adapter, a flag-shaped value, and mixed clear/set.
        self.assertIn("Give --worker-harness", self.settings("--worker-model", "x", ok=False)["error"])
        self.assertIn("No verified reasoning flag", self.settings("--worker-harness", "cursor", "--worker-reasoning", "high", ok=False)["error"])
        self.assertIn("No verified model flag", self.settings("--worker-harness", "gemini", "--worker-model", "gemini-3", ok=False)["error"])
        self.assertIn("one plain CLI value", self.settings("--worker-harness", "codex", "--worker-model=--dangerously-bypass", ok=False)["error"])
        self.assertIn("conflicts", self.settings("--clear-worker", "--worker-harness", "codex", ok=False)["error"])
        self.assertNotIn("worker", json.loads((self.store.home / "settings.json").read_text()))
        # A hand-edited invalid worker block is refused with the defect before any dispatch side effect.
        path = self.store.home / "settings.json"
        path.write_text(json.dumps({"schema": 1, "worker": {"harness": "codex", "model": "gpt-5", "temperature": 1}}))
        calls, tasks = len(self.calls()), len(self.store.all())
        error = self.dispatch(command="prepare", ok=False)["error"]
        self.assertIn("unknown worker keys ['temperature']", error)
        self.assertEqual((len(self.calls()), len(self.store.all())), (calls, tasks))
        path.unlink()

    def test_explicit_model_and_harness_override_without_persisting(self):
        self.settings("--worker-harness", "codex", "--worker-model", "gpt-5-codex")
        # Explicit model on the saved harness.
        task = self.dispatch("--model", "o4-mini")
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("codex", ["-m", "o4-mini"]))
        self.assertEqual(task["launch"]["source"], {"harness": "saved-default", "model": "explicit", "reasoning": "native-default"})
        # Explicit harness only: the saved Codex model is never applied to claude.
        task = self.dispatch("--harness", "claude")
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("claude", []))
        self.assertEqual(task["launch"]["source"], {"harness": "explicit", "model": "native-default", "reasoning": "native-default"})
        # Explicit harness and model use that harness's verified flags.
        task = self.dispatch("--harness", "claude", "--model", "fable", "--reasoning", "low")
        self.assertEqual(self.argv(task["id"]), ["--model", "fable", "--effort", "low"])
        task = self.dispatch("--harness", "grok", "--model", "grok-4", "--reasoning", "high")
        self.assertEqual(self.argv(task["id"]), ["-m", "grok-4", "--reasoning-effort", "high"])
        task = self.dispatch("--harness", "omp", "--model", "opus")
        self.assertEqual(self.argv(task["id"]), ["--model=opus"])
        # Nothing above changed the saved default.
        self.assertEqual(self.ctl(self.install, self.store, "settings", "show")["worker"], {"harness": "codex", "model": "gpt-5-codex"})
        self.assertEqual(self.argv(self.dispatch()["id"]), ["-m", "gpt-5-codex"])

    def test_same_as_you_overrides_a_saved_default_and_discloses_the_unknown_root_model(self):
        self.settings("--worker-harness", "codex", "--worker-model", "gpt-5-codex", "--worker-reasoning", "high")
        task = self.dispatch("--same-as-you")
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("claude", []))
        self.assertEqual(task["launch"]["source"], {"harness": "same-as-you", "model": "native-default", "reasoning": "native-default"})
        self.assertTrue(task["launch"]["same_as_root"])
        self.assertIsNone(task["launch"]["root"]["model"])
        self.assertIn("model native default (native-default)", task["confirmation"])
        self.assertNotIn("same model", task["confirmation"])
        self.assertIn("conflicts", self.dispatch("--same-as-you", "--model", "fable", ok=False)["error"])
        self.assertIn("conflicts", self.dispatch("--same-as-you", "--harness", "codex", ok=False)["error"])
        # When the root harness cannot be observed, the helper asks for an explicit choice instead of guessing.
        error = self.dispatch("--same-as-you", ok=False, env={"HERDR_PANE_ID": "w-parent:p1", "FAKE_PARENT_KIND": ""})["error"]
        self.assertIn("Cannot determine the coordinator's own harness", error)

    def test_unsupported_model_or_flags_are_refused_before_any_side_effect(self):
        calls = len(self.calls())
        tasks = len(self.store.all())
        self.assertIn("No verified reasoning flag", self.dispatch("--harness", "cursor", "--model", "gpt-5", "--reasoning", "high", command="prepare", ok=False)["error"])
        self.assertIn("No verified model flag", self.dispatch("--harness", "gemini", "--model", "gemini-3", command="prepare", ok=False)["error"])
        self.assertIn("one plain CLI value", self.dispatch("--harness", "codex", "--model", "gpt; rm -rf /", command="prepare", ok=False)["error"])
        self.assertIn("Conflicting model", self.dispatch("--harness", "codex", "--model", "gpt-5", "--arg=-m", "--arg", "other", command="prepare", ok=False)["error"])
        self.assertIn("Conflicting reasoning", self.dispatch("--harness", "codex", "--reasoning", "high", "--arg=-c", "--arg", "model_reasoning_effort=low", command="prepare", ok=False)["error"])
        self.assertIn("Conflicting model", self.dispatch("--harness", "claude", "--model", "fable", "--arg=--model=opus", command="prepare", ok=False)["error"])
        self.assertIn("Harness must be", self.dispatch("--harness", "Not A Kind", command="prepare", ok=False)["error"])
        self.assertEqual((len(self.calls()), len(self.store.all())), (calls, tasks))  # No Herdr call, no record, no slot.
        # A harness without an adapter still works with explicit native arguments: the caller, not sum, chose them.
        task = self.dispatch("--harness", "gemini", "--arg=--model", "--arg", "gemini-3")
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("gemini", ["--model", "gemini-3"]))
        self.assertEqual(task["launch"]["source"]["model"], "native-default")  # sum does not parse the caller's argv into a model claim.

    def test_prepared_specification_survives_default_changes_until_start(self):
        self.settings("--worker-harness", "codex", "--worker-model", "gpt-5-codex")
        prepared = self.dispatch(command="prepare")
        self.assertEqual(prepared["status"], "prepared")
        self.assertEqual(prepared["launch"]["argv"], ["-m", "gpt-5-codex"])
        self.settings("--worker-harness", "claude", "--worker-model", "fable")
        self.settings("--global", "3")
        started = self.ctl(self.install, self.store, "start", prepared["id"], env=self.env)
        self.assertEqual((self.kind(prepared["id"]), self.argv(prepared["id"])), ("codex", ["-m", "gpt-5-codex"]))
        self.assertEqual(started["launch"]["source"], {"harness": "saved-default", "model": "saved-default", "reasoning": "native-default"})
        self.assertEqual(started["launch"]["observed"]["status"], "harness-observed")
        self.assertEqual(started["launch"]["saved_default"], {"harness": "codex", "model": "gpt-5-codex"})
        self.assertIn("harness codex (saved-default), model gpt-5-codex (saved-default)", started["confirmation"])
        # The next dispatch follows the new defaults.
        self.assertEqual(self.argv(self.dispatch()["id"]), ["--model", "fable"])

    def test_legacy_arguments_and_old_records_keep_working(self):
        # An old caller passing --harness and --arg values gets exactly those, in order, with nothing added.
        task = self.dispatch("--harness", "codex", "--arg=-m", "--arg", "gpt-5", "--arg=--search")
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("codex", ["-m", "gpt-5", "--search"]))
        self.assertEqual(task["launch"]["explicit_args"], ["-m", "gpt-5", "--search"])
        # `start --arg` still appends, but may not contradict the persisted model.
        prepared = self.dispatch("--harness", "codex", "--model", "gpt-5", command="prepare")
        self.assertIn("Conflicting model", self.ctl(self.install, self.store, "start", prepared["id"], "--arg=-m", "--arg", "o3", env=self.env, ok=False)["error"])
        self.assertEqual(self.ctl(self.install, self.store, "show", prepared["id"])["status"], "prepared")
        started = self.ctl(self.install, self.store, "start", prepared["id"], "--arg=--search", env=self.env)
        self.assertEqual(self.argv(prepared["id"]), ["-m", "gpt-5", "--search"])
        self.assertEqual(started["launch"]["started_argv"], ["-m", "gpt-5", "--search"])
        # A record written before this change has only `harness`; it starts as it always did.
        old = self.dispatch("--harness", "grok", command="prepare")
        path = self.store.path(old["id"]) / "task.json"
        record = json.loads(path.read_text())
        del record["launch"]
        path.write_text(json.dumps(record))
        started = self.ctl(self.install, self.store, "start", old["id"], env=self.env)
        self.assertEqual((self.kind(old["id"]), self.argv(old["id"])), ("grok", []))
        self.assertEqual(started["launch"]["source"]["harness"], "legacy-record")
        rundown = self.ctl(self.install, self.store, "status")
        self.assertIn(old["id"], json.dumps(rundown))

    def test_arguments_with_spaces_and_shell_metacharacters_are_passed_as_exact_tokens(self):
        hostile = ["--append-system-prompt", "say hi; echo $(whoami) && rm -rf / | tee 'x' \"y\" `z`", "--arg-with-space", "two words"]
        task = self.dispatch("--harness", "claude", "--model", "fable", *[f"--arg={a}" for a in hostile])
        self.assertEqual(self.argv(task["id"]), ["--model", "fable", *hostile])
        self.assertEqual(task["launch"]["argv"], ["--model", "fable", *hostile])
        self.assertEqual(json.loads((self.store.path(task["id"]) / "task.json").read_text())["launch"]["argv"], ["--model", "fable", *hostile])
        prompt = [c for c in self.calls() if c[:2] == ["agent", "prompt"] and c[2] == task["pane"]][0]
        self.assertIn("You are the sum worker", prompt[3])

    def test_launch_is_resolved_in_process_too(self):
        settings = sumctl.load_settings(self.store)
        ctx = {"session": "sum-test", "pane": "w-parent:p1", "machine": sumctl.machine(), "cwd": str(self.install), "at": sumctl.now()}
        with mock.patch.dict(os.environ, self.env):
            launch = sumctl.resolve_launch(settings, ctx, extra=["--arg with space"])
        self.assertEqual((launch["harness"], launch["argv"], launch["source"]["harness"]), ("claude", ["--arg with space"], "root"))
        with self.assertRaises(sumctl.SumError):
            sumctl.resolve_launch(settings, ctx, harness="codex", model="gpt-5", extra=["-m", "x"])
        with self.assertRaises(sumctl.SumError):
            sumctl.resolve_launch(settings, ctx, harness="pi", reasoning="high")
