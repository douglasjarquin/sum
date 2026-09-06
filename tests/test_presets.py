"""Optional named launch presets (issue #12): validated harness/model/argv shortcuts, expanded at prepare and frozen with the task.

Real Git, the strict fake Herdr, real sum helpers through the installation entrypoint; no model, network, or credentials.
The fake records the exact argv handed to `agent start`, so every assertion is about what would actually be executed.
"""
from __future__ import annotations
import json
from pathlib import Path
import sys
import tarfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_fleet
import test_launch
from test_core import sumctl


class PresetTest(test_fleet.FleetLab):
    dispatch, start_call, argv, kind, settings = (test_launch.LaunchTest.dispatch, test_launch.LaunchTest.start_call,
                                                 test_launch.LaunchTest.argv, test_launch.LaunchTest.kind, test_launch.LaunchTest.settings)

    def setUp(self):
        super().setUp()
        self.install, self.store = self.installation()
        self.env = {"FAKE_PARENT_CWD": str(self.install.resolve())}
        self.count = 0
        self.settings("--global", "12")

    def preset(self, *argv, ok=True):
        return self.ctl(self.install, self.store, "preset", *argv, env=self.env, ok=ok)

    def settings_file(self):
        return json.loads((self.store.home / "settings.json").read_text())

    def test_no_presets_configured_keeps_the_old_paths_byte_identical(self):
        listed = self.preset("list")
        self.assertEqual((listed["presets"], listed["worker"], listed["reviewer"]), ({}, None, None))
        shown = self.ctl(self.install, self.store, "settings", "show")
        self.assertEqual((shown["presets"], shown["reviewer"]), ({}, None))
        self.assertNotIn("presets", self.settings_file())  # A capacity-only file stays as #11 wrote it.
        task = self.dispatch()
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"]), task["launch"]["preset"]), ("claude", [], None))
        self.assertNotIn("preset", task["confirmation"])
        old = self.dispatch("--harness", "codex", "--arg=-m", "--arg", "gpt-5", "--arg=--search")
        self.assertEqual((self.kind(old["id"]), self.argv(old["id"]), old["launch"]["preset"]), ("codex", ["-m", "gpt-5", "--search"], None))
        self.assertIn("Unknown preset 'review'", self.dispatch("--preset", "review", command="prepare", ok=False)["error"])
        self.assertIn("unknown preset 'review'", self.preset("show", "review", ok=False)["error"])

    def test_set_show_list_and_delete_are_validated_and_revisioned(self):
        created = self.preset("set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--reasoning", "high", "--arg=--search")
        self.assertEqual(created["preset"], {"harness": "codex", "model": "gpt-5-codex", "reasoning": "high", "args": ["--search"], "revision": 1})
        self.assertEqual(created["launch"]["argv"], ["-m", "gpt-5-codex", "-c", "model_reasoning_effort=high", "--search"])
        self.assertIsNone(created["previous"])
        self.assertEqual(oct((self.store.home / "settings.json").stat().st_mode & 0o777), "0o600")
        self.assertEqual(self.settings_file()["presets"]["deep"]["revision"], 1)
        revised = self.preset("set", "deep", "--reasoning", "medium")
        self.assertEqual((revised["previous"]["revision"], revised["preset"]["revision"], revised["preset"]["reasoning"], revised["preset"]["args"]), (1, 2, "medium", ["--search"]))
        cleared = self.preset("set", "deep", "--clear-args", "--clear-reasoning")
        self.assertEqual(cleared["preset"], {"harness": "codex", "model": "gpt-5-codex", "revision": 3})
        switched = self.preset("set", "deep", "--harness", "claude")  # A harness change drops the Codex model instead of carrying it to another CLI.
        self.assertEqual(switched["preset"], {"harness": "claude", "revision": 4})
        self.preset("set", "review", "--harness", "claude", "--model", "fable", "--reasoning", "low")
        shown = self.preset("show", "review")
        self.assertEqual((shown["revision"], shown["launch"]["argv"], shown["used_by"]), (1, ["--model", "fable", "--effort", "low"], []))
        self.assertEqual(sorted(self.preset("list")["presets"]), ["deep", "review"])
        self.assertEqual(self.preset("list")["presets"]["review"]["model"], "fable")
        # Validation before any write: a missing harness, an unverified adapter, a flag-shaped value, a preset arg that duplicates the model flag, a bad name.
        before = self.settings_file()
        self.assertIn("Give --harness", self.preset("set", "fresh", "--model", "x", ok=False)["error"])
        self.assertIn("No verified reasoning flag", self.preset("set", "fresh", "--harness", "cursor", "--reasoning", "high", ok=False)["error"])
        self.assertIn("one plain CLI value", self.preset("set", "fresh", "--harness", "codex", "--model=--dangerously-bypass", ok=False)["error"])
        self.assertIn("both set the codex model flag", self.preset("set", "fresh", "--harness", "codex", "--model", "gpt-5", "--arg=-m", "--arg", "o3", ok=False)["error"])
        self.assertIn("preset name must be", self.preset("set", "Not Valid", "--harness", "codex", ok=False)["error"])
        self.assertIn("conflicts", self.preset("set", "deep", "--clear-model", "--model", "x", ok=False)["error"])
        self.assertEqual(self.settings_file(), before)
        # Delete: unknown names and referenced presets are refused; a free one goes, and other settings stay.
        self.assertIn("unknown preset", self.preset("delete", "nope", ok=False)["error"])
        self.settings("--reviewer-preset", "review")
        self.assertIn("reviewer default", self.preset("delete", "review", ok=False)["error"])
        self.assertIn("review", self.settings_file()["presets"])
        deleted = self.preset("delete", "deep")
        self.assertEqual(deleted["remaining"], ["review"])
        self.assertEqual(self.settings_file()["capacity"]["global"], 12)
        # A hand-edited invalid preset block is refused with the defect before any dispatch side effect.
        path = self.store.home / "settings.json"
        path.write_text(json.dumps({"schema": 1, "presets": {"deep": {"harness": "codex", "temperature": 1}}}))
        calls, tasks = len(self.calls()), len(self.store.all())
        self.assertIn("unknown keys ['temperature'] in presets.deep", self.dispatch(command="prepare", ok=False)["error"])
        path.write_text(json.dumps({"schema": 1, "worker": {"preset": "ghost"}}))
        self.assertIn("worker.preset names unknown preset 'ghost'", self.dispatch(command="prepare", ok=False)["error"])
        path.write_text(json.dumps({"schema": 1, "reviewer": {"preset": "ghost"}}))
        self.assertIn("reviewer.preset names unknown preset 'ghost'", self.dispatch(command="prepare", ok=False)["error"])
        self.assertEqual((len(self.calls()), len(self.store.all())), (calls, tasks))
        path.unlink()

    def test_chosen_preset_expands_at_dispatch_and_is_frozen_with_the_task(self):
        self.preset("set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--reasoning", "high", "--arg=--search")
        task = self.dispatch("--preset", "deep")
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("codex", ["-m", "gpt-5-codex", "-c", "model_reasoning_effort=high", "--search"]))
        self.assertEqual(task["launch"]["source"], {"harness": "preset", "model": "preset", "reasoning": "preset"})
        self.assertEqual(task["launch"]["preset"], {"name": "deep", "revision": 1, "source": "preset", "harness": "codex", "model": "gpt-5-codex", "reasoning": "high", "args": ["--search"]})
        self.assertEqual(task["launch"]["explicit_args"], [])
        self.assertIn("preset deep r1 (preset)", task["confirmation"])
        self.assertIn("harness codex (preset), model gpt-5-codex (preset)", task["confirmation"])
        # Nothing was saved as a default; the next plain dispatch is unchanged.
        self.assertIsNone(self.ctl(self.install, self.store, "settings", "show")["worker"])
        self.assertEqual((self.kind(self.dispatch()["id"]), self.argv(self.dispatch()["id"])), ("claude", []))
        # Deleted or changed preset after prepare: the prepared task starts with exactly the specification it was prepared with.
        prepared = self.dispatch("--preset", "deep", command="prepare")
        self.preset("set", "deep", "--model", "o4-mini", "--clear-args")
        self.assertEqual(self.preset("show", "deep")["revision"], 2)
        started = self.ctl(self.install, self.store, "start", prepared["id"], env=self.env)
        self.assertEqual(self.argv(prepared["id"]), ["-m", "gpt-5-codex", "-c", "model_reasoning_effort=high", "--search"])
        self.assertEqual((started["launch"]["preset"]["revision"], started["launch"]["preset"]["model"]), (1, "gpt-5-codex"))
        second = self.dispatch("--preset", "deep", command="prepare")
        self.assertEqual(second["launch"]["preset"]["revision"], 2)
        self.preset("delete", "deep")
        started = self.ctl(self.install, self.store, "start", second["id"], env=self.env)
        self.assertEqual(self.argv(second["id"]), ["-m", "o4-mini", "-c", "model_reasoning_effort=high"])
        self.assertEqual(started["launch"]["preset"]["name"], "deep")
        self.assertEqual(self.ctl(self.install, self.store, "show", prepared["id"])["launch"]["preset"]["revision"], 1)
        self.assertIn("Unknown preset 'deep'", self.dispatch("--preset", "deep", command="prepare", ok=False)["error"])

    def test_explicit_fields_refine_a_compatible_preset_and_cross_harness_choices_fail_first(self):
        self.preset("set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--reasoning", "high", "--arg=--search")
        task = self.dispatch("--preset", "deep", "--model", "o4-mini", "--arg=--full-auto")
        self.assertEqual(self.argv(task["id"]), ["-m", "o4-mini", "-c", "model_reasoning_effort=high", "--search", "--full-auto"])
        self.assertEqual(task["launch"]["source"], {"harness": "preset", "model": "explicit", "reasoning": "preset"})
        self.assertEqual((task["launch"]["preset"]["model"], task["launch"]["explicit_args"]), ("gpt-5-codex", ["--full-auto"]))
        same = self.dispatch("--preset", "deep", "--harness", "codex", "--reasoning", "low")
        self.assertEqual(same["launch"]["source"], {"harness": "explicit", "model": "preset", "reasoning": "explicit"})
        self.assertEqual(self.argv(same["id"]), ["-m", "gpt-5-codex", "-c", "model_reasoning_effort=low", "--search"])
        calls, tasks = len(self.calls()), len(self.store.all())
        error = self.dispatch("--preset", "deep", "--harness", "claude", command="prepare", ok=False)["error"]
        self.assertIn("Preset 'deep' runs on codex but --harness claude was requested", error)
        self.assertIn("Conflicting model", self.dispatch("--preset", "deep", "--arg=-m", "--arg", "o3", command="prepare", ok=False)["error"])
        self.assertIn("conflicts", self.dispatch("--preset", "deep", "--same-as-you", command="prepare", ok=False)["error"])
        self.assertIn("Unknown preset 'nope'", self.dispatch("--preset", "nope", "--harness", "codex", command="prepare", ok=False)["error"])
        self.assertEqual((len(self.calls()), len(self.store.all())), (calls, tasks))  # No Herdr call, no record, no slot.

    def test_saved_default_preset_and_same_as_you_bypass(self):
        self.preset("set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--arg=--search")
        self.assertIn("unknown preset 'nope'", self.settings("--worker-preset", "nope", ok=False)["error"])
        self.assertIn("conflicts", self.settings("--worker-preset", "deep", "--worker-harness", "codex", ok=False)["error"])
        saved = self.settings("--worker-preset", "deep")
        self.assertEqual(saved["worker"], {"preset": "deep"})
        self.assertEqual(self.settings_file()["worker"], {"preset": "deep"})
        self.assertEqual(self.preset("show", "deep")["used_by"], ["worker default"])
        task = self.dispatch()
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"])), ("codex", ["-m", "gpt-5-codex", "--search"]))
        self.assertEqual(task["launch"]["source"], {"harness": "saved-default", "model": "saved-default", "reasoning": "native-default"})
        self.assertEqual((task["launch"]["preset"]["name"], task["launch"]["preset"]["source"]), ("deep", "saved-default"))
        self.assertIn("preset deep r1 (saved-default)", task["confirmation"])
        # Explicit same-as-you bypasses the default preset; a different explicit harness never carries the Codex preset along.
        same = self.dispatch("--same-as-you")
        self.assertEqual((self.kind(same["id"]), self.argv(same["id"]), same["launch"]["preset"]), ("claude", [], None))
        self.assertEqual(same["launch"]["source"]["harness"], "same-as-you")
        other = self.dispatch("--harness", "claude")
        self.assertEqual((self.argv(other["id"]), other["launch"]["preset"]), ([], None))
        self.assertEqual(self.dispatch("--harness", "codex", "--model", "o3")["launch"]["preset"]["name"], "deep")  # Same harness: the default preset still applies underneath.
        self.assertEqual(self.dispatch("--preset", "deep", "--reasoning", "high")["launch"]["source"]["harness"], "preset")
        # The default is a reference: a later revision of the preset changes future dispatches only.
        prepared = self.dispatch(command="prepare")
        self.preset("set", "deep", "--model", "gpt-5")
        self.ctl(self.install, self.store, "start", prepared["id"], env=self.env)
        self.assertEqual(self.argv(prepared["id"]), ["-m", "gpt-5-codex", "--search"])
        self.assertEqual(self.argv(self.dispatch()["id"]), ["-m", "gpt-5", "--search"])
        self.assertIn("still the worker default", self.preset("delete", "deep", ok=False)["error"])
        # Replacing the reference with a plain default, and clearing it, both work; the file never carries both shapes.
        plain = self.settings("--worker-harness", "claude", "--worker-model", "fable")
        self.assertEqual(plain["worker"], {"harness": "claude", "model": "fable"})
        self.settings("--worker-preset", "deep")
        self.assertIsNone(self.settings("--clear-worker")["worker"])
        self.assertEqual(self.preset("delete", "deep")["remaining"], [])
        self.assertEqual(self.kind(self.dispatch()["id"]), "claude")

    def test_reviewer_preset_is_guidance_for_the_coordinators_own_launch_only(self):
        self.preset("set", "review", "--harness", "claude", "--model", "fable", "--reasoning", "low")
        self.assertIn("conflicts", self.settings("--reviewer-preset", "review", "--clear-reviewer", ok=False)["error"])
        saved = self.settings("--reviewer-preset", "review")
        self.assertEqual((saved["previous_reviewer"], saved["reviewer"]), (None, {"preset": "review"}))
        self.assertEqual(self.ctl(self.install, self.store, "settings", "show")["reviewer"], {"preset": "review"})
        self.assertEqual(self.preset("show", "review")["launch"], {"harness": "claude", "model": "fable", "reasoning": "low", "argv": ["--model", "fable", "--effort", "low"]})
        # A reviewer default changes nothing about dispatch, the worker launch, or the evidence route: sum still starts exactly one agent per task
        # and records review/verify evidence the same way; the repository's own verification tool is never wrapped.
        calls = len(self.calls())
        task = self.dispatch()
        self.assertEqual((self.kind(task["id"]), self.argv(task["id"]), task["launch"]["preset"]), ("claude", [], None))
        self.assertEqual([c for c in self.calls()[calls:] if c[:2] == ["agent", "start"]], [self.start_call(task["id"])])
        self.assertNotIn("review", json.dumps(task["launch"]))
        head = self.git("rev-parse", "HEAD", cwd=task["worktree"])
        verified = self.ctl(self.install, self.store, "verify", task["id"], "--candidate", head, "--result", "pass", "--text", "Ran the repository's own checks.", env=self.env)
        self.assertEqual(verified["evidence"]["kind"], "verification")
        self.assertEqual([c for c in self.calls()[calls:] if c[:2] == ["agent", "start"]], [self.start_call(task["id"])])  # Still one launch: no reviewer agent was started by sum.
        self.assertIsNone(self.settings("--clear-reviewer")["reviewer"])
        self.assertNotIn("reviewer", self.settings_file())

    def test_presets_travel_with_the_records_backup_and_restore(self):
        self.preset("set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--arg=--search")
        self.preset("set", "review", "--harness", "claude", "--model", "fable")
        self.settings("--reviewer-preset", "review", "--worker-preset", "deep")
        backup = sumctl.backup(self.store, self.root / "records.tar.gz")
        self.assertTrue(backup["manifest"]["settings_included"])
        restored = self.root / "restored"
        with tarfile.open(backup["backup"]) as archive:
            archive.extractall(restored, filter="data")
        restored_store = sumctl.Store(restored / "state")
        settings = sumctl.load_settings(restored_store)
        self.assertEqual(sorted(settings["presets"]), ["deep", "review"])
        self.assertEqual((settings["worker"], settings["reviewer"]), ({"preset": "deep"}, {"preset": "review"}))
        self.assertEqual(sumctl.preset_show(restored_store, "deep")["launch"]["argv"], ["-m", "gpt-5-codex", "--search"])
        self.assertEqual(json.loads((restored / "state/settings.json").read_text()), self.settings_file())

    def test_presets_resolve_in_process_too(self):
        sumctl.write_preset(self.store, "deep", harness="codex", model="gpt-5-codex", args=["--search"])
        settings = sumctl.load_settings(self.store)
        ctx = {"session": "sum-test", "pane": "w-parent:p1", "machine": sumctl.machine(), "cwd": str(self.install), "at": sumctl.now()}
        launch = sumctl.resolve_launch(settings, ctx, preset="deep", reasoning="low", extra=["--x"])
        self.assertEqual(launch["argv"], ["-m", "gpt-5-codex", "-c", "model_reasoning_effort=low", "--search", "--x"])
        self.assertEqual(launch["preset"]["revision"], 1)
        with self.assertRaises(sumctl.SumError):
            sumctl.resolve_launch(settings, ctx, preset="deep", harness="claude")
        with self.assertRaises(sumctl.SumError):
            sumctl.resolve_launch(settings, ctx, preset="missing")
        with self.assertRaises(sumctl.SumError):
            sumctl.write_preset(self.store, "deep", harness="codex", args=["-m", "x"])  # args may not duplicate the kept model flag

