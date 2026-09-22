# Configuration

Worker launch defaults, presets, and the capacity schema. Moved out of the README.

### Worker harness and model

`--harness` is optional.
Without it, the worker runs the saved worker default when one exists, otherwise the coordinator's own harness, as observed from Herdr; the model is then that harness's native default because no harness exposes its running model through Herdr, and sum says so instead of claiming exact inheritance.
The precedence is fixed: an explicit instruction (`--harness`, `--model`, `--reasoning`, or `--same-as-you`), then the saved worker default in `.sum/settings.json`, then the reliably known root harness, then the disclosed native default.

```sh
./bin/sumctl settings set --worker-harness codex --worker-model gpt-5-codex --worker-reasoning high   # coordinator only; future dispatches
./bin/sumctl settings set --worker-harness claude                                                    # switching harness drops the old harness's model
./bin/sumctl settings set --clear-worker                                                             # back to the coordinator's harness
./bin/sumctl dispatch --repo R --brief B --approved --model o4-mini                                  # this task only; the default is unchanged
./bin/sumctl dispatch --repo R --brief B --approved --harness claude                                 # harness-only override: a saved Codex model is never applied to claude
./bin/sumctl dispatch --repo R --brief B --approved --same-as-you                                    # the coordinator's harness and native model, ignoring the saved default
```

A model or reasoning value is translated only through a flag verified from the installed CLI's own help: `codex -m MODEL -c model_reasoning_effort=LEVEL`, `claude --model MODEL --effort LEVEL`, `grok -m MODEL --reasoning-effort LEVEL`, `copilot --model MODEL --effort LEVEL`, `cursor --model MODEL`, `pi --model MODEL`, `omp --model=MODEL`.
A harness without a verified flag refuses `--model`/`--reasoning` instead of guessing; pass the native argument yourself with `--arg`, which still works exactly as before.
Conflicts (`--same-as-you` with a harness or model, `--model` plus an `--arg` that sets the same flag, a flag-shaped value) are refused before any record, slot, or Herdr call, and arguments are passed as exact argv tokens, never through a shell.
The resolved specification (harness, model, reasoning, exact argv, the source of each field, the observed root harness, and the observed-versus-requested status) is saved with the task at `prepare`, so a prepared task starts with the same specification even if the defaults change in between; `start --arg` may append but not contradict it.
`settings show` and every `prepare`/`dispatch`/`start` result carry the saved default and a one-line `confirmation`; Herdr confirms the harness kind after start, while a CLI-requested model stays "requested, not runtime-verified".
Saving a default affects future dispatches only: no running worker, the coordinator's own harness or model, account, or billing route changes, and the existing advisory quota checks still apply.

### Named launch presets

A preset is a named, validated harness/model/argv shortcut in the same `.sum/settings.json`, so you can say "use deep for this task" instead of repeating launch arguments.
It is not an agent, a role, a credential store, or a default until you save it as one; an installation without presets behaves exactly as before.

```sh
./bin/sumctl preset set deep --harness codex --model gpt-5-codex --reasoning high --arg=--search   # coordinator only; revision 1 (illustrative values, not a built-in)
./bin/sumctl preset set review --harness claude --model fable --reasoning low                       # another placeholder; nothing is subscribed or enabled by it
./bin/sumctl preset list                                                                             # names, harness, model, reasoning, args, revision
./bin/sumctl preset show deep                                                                        # the exact argv it expands to and which defaults use it
./bin/sumctl dispatch --repo R --brief B --approved --preset deep                                   # this task only; expanded at prepare
./bin/sumctl dispatch --repo R --brief B --approved --preset deep --model o4-mini --arg=--full-auto # explicit fields refine a compatible preset
./bin/sumctl settings set --worker-preset deep                                                      # the saved worker default becomes a reference to the preset
./bin/sumctl settings set --reviewer-preset review                                                  # used only when you launch a reviewer yourself; see the delivery skill
./bin/sumctl preset delete deep                                                                     # refused while a default still references it
```

Precedence stays fixed: an explicit `--harness`/`--model`/`--reasoning` refines the chosen preset when compatible; `--preset X --harness Y` with a different harness, an `--arg` that repeats the preset's model or reasoning flag, an unknown preset name, or `--same-as-you` together with `--preset` are refused before any record, slot, or Herdr call, naming the fix.
`--same-as-you` also bypasses a saved default preset, and a harness-only override never carries another harness's preset along.
The preset is expanded at `prepare` and the resolved specification is persisted with the task together with the preset's name and revision (`launch.preset`); `preset set` bumps the revision and, like `preset delete`, changes future dispatches only, so a prepared or running task keeps exactly what it was prepared with.
A model in a preset is CLI-requested, never runtime-verified, and presets change nothing about authorization, accounts, or the advisory quota checks.

Admission is unlimited until a capacity block is configured; see [capacity](#capacity) for the optional settings file.
SUM enforces a task allowance for out-of-scope corrections, not spending or wall-clock limits.

### Capacity

An execution slot belongs to a recorded worker attempt or an independent verification run, not to the unfinished task itself.
`prepare` reserves a worker slot before creating its checkout, and `verify --execute` reserves a separate slot before creating its verification checkout.
Admission uses the same global and per-repository limits under the local record lock.
A refused admission launches nothing.

Use `execution show TASK_ID` to read the current attempt IDs.
After a worker exits, `execution park TASK_ID --attempt ATTEMPT_ID` checks its recorded attempt, instance, occupant, pane, checkout, processes, and owned services before releasing the slot.
Missing, stale, or unobservable identity is unknown, not stopped.
The command stops nothing and preserves questions, reports, evidence, and the checkout.
A report, an idle or `done` pane, a dead parent with a surviving owned child, or an uncertain observation cannot release capacity.
A user-closed worker pane (`pane_not_found` or `agent_not_found`) with no occupant in the recorded checkout is conclusive stop for that attempt.
Owned services keep the worker reservation held until their shutdown is proven.

To continue approved work, use `execution resume TASK_ID --attempt ATTEMPT_ID` with the released worker attempt ID.
Resume checks capacity again, saves the previous attempt in the task's evidence history, and records a new attempt before launching.
An old attempt ID cannot release or resume its successor.
Use the current verifier attempt ID with `execution park` to reconcile an interrupted verification only after its recorded operation, occupant, and checkout processes are conclusively stopped.
A verifier without those identities stays reserved.
Parking does not remove a leftover verification checkout.
`archive --acknowledge` refuses held reservations and never substitutes for stop inspection.

```sh
./bin/sumctl settings show                                   # limits, their source, and the held slots per repository
./bin/sumctl settings set --global 12 --per-repository 1     # coordinator only; validated and written atomically
./bin/sumctl settings set --clear-capacity                   # return to unlimited without changing worker or preset settings
./bin/sumctl execution show TASK_ID                         # read current attempt IDs and state
./bin/sumctl execution park TASK_ID --attempt ATTEMPT_ID     # inspect stopped execution; preserve unfinished work
./bin/sumctl execution resume TASK_ID --attempt ATTEMPT_ID   # reacquire capacity and launch approved work
```

`.sum/settings.json` is the one owner of executable admission values, worker launch defaults, named presets, and the evidence publication switch (`{"schema": 1, "capacity": {"global": N, "per_repository": M}, "worker": {"harness": "codex", "model": "...", "reasoning": "..."} | {"preset": "deep"}, "presets": {"deep": {"harness": "codex", "model": "...", "reasoning": "...", "args": [...], "revision": 1}}, "reviewer": {"preset": "review"}, "evidence": {"auto_publish": false}}`); capacity integers from 1 to 64, `per_repository` at most `global`; `capacity`, `worker`, `presets`, `reviewer`, and `evidence` are optional, a model/reasoning needs a verified adapter for its harness, and a referenced preset must exist.
An absent `evidence` block means `pr reconcile` publishes the worker's before/after evidence into the PR; `{"auto_publish": false}` is the only way to turn that off, and `settings show` reports the effective value with its source (`default` or `settings`).
Absent `capacity` means no admission cap, including when the settings file exists for worker or preset defaults; `settings show` reports `limits: null` for this state.
`.sum/preferences.md` and `.sum/projects.md` stay narrative and never set a limit or a worker default; only your explicit "make this my default" becomes a `settings set --worker-*` write.
An invalid file is refused with the exact defect before any side effect: nothing is admitted, no task or worker is touched, `ask`/`report`/`show` keep working, and `settings set` refuses to overwrite it silently.
Lowering a limit affects future admission only; tasks above the new limit keep their slots, processes, and checkouts.
Raising `global` never raises `per_repository`: one checkout gets one writer unless you say otherwise.
Nothing schedules or dispatches work because a slot is free; a dispatch is always an explicit approved instruction.
The settings file travels with `sumctl backup`.
Legacy non-archived tasks without reservation metadata count as held until explicit stop inspection adopts them.
Malformed reservation metadata refuses admission and release rather than counting as free capacity.
Older helpers may preserve these records, but an old coordinator does not enforce the new reservation policy.

Rundown and refresh over a fleet are one bounded pass: one `herdr agent list` snapshot per session replaces a per-worker observation call, each delivery gets its own timeout, no transcript is read, and one unobservable worker delays nobody else.
`inbox --live`, `status --live`, and `refresh request` report `fanout` with the number of Herdr calls and the local elapsed time of that pass.
