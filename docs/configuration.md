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

### Permission and trust overrides

sum adds no permission or trust flag to any worker or reviewer launch; the only argument it adds itself is the Claude task-access grant (`--add-dir TASK_DIR`), which scopes file reach and changes no approval behavior.
Selecting a harness, including `devin`, therefore never changes that harness's permission behavior: a Devin worker that stops at its trust gate is answered by a person or launched with an explicitly authorized override, never past the gate silently.

A permission or trust flag reaches a launch only through `--arg` or a preset's `args`, and only when the same `prepare`, `dispatch`, or `reviewer launch` carries `--permission-override`, which records the user's explicit authorization for that one launch.
Without the flag such an argument is refused before any record, slot, worktree, or Herdr call; with the flag the launch record carries a `permissions` object naming each override argument, its source (`explicit` or `preset`), the authorization, and `status: requested`, and the one-line `confirmation` repeats it.
`requested` is the only status an override ever gets: no harness exposes its running permission mode, so sum never claims one was applied, and a per-task Git worktree is not a process or credential sandbox.
`harness-default` means sum passed no override argument on argv; it does not describe on-disk harness settings or profiles (`codex -p/--profile`, `claude --settings`, whether given as a file or as inline JSON, user config files), other `codex -c` keys such as `sandbox_workspace_write.*`, environment variables such as copilot's `COPILOT_ALLOW_ALL`, or an agent started through the mesh `herdr_agent_start` tool, all of which are outside this gate.

The flags sum recognizes per harness are verified from the installed CLI's help, the way model and reasoning flags are: `claude --permission-mode --permission-prompts --permission-prompt-tool --allowedTools --allowed-tools --disallowedTools --disallowed-tools --tools --dangerously-skip-permissions --allow-dangerously-skip-permissions --restricted`; `codex -a --ask-for-approval -s --sandbox --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --approve-for-me` plus the `-c`/`--config` keys `approval_policy`, `sandbox_mode`, and `sandbox_permissions`, in the spaced, `=`-joined, and attached short forms (`-a never`, `-a=never`, `-anever`, `-capproval_policy=never`); `copilot --allow-tool --allow-url --deny-tool --deny-url --mode --allow-all --allow-all-tools --allow-all-paths --allow-all-urls --yolo --autopilot --disallow-temp-dir`; `devin --permission-mode --respect-workspace-trust` (carried forward from the help recorded when Devin support landed; not re-verified on a machine without the CLI).
A flag that narrows access counts too, because the record exists to disclose every explicit permission choice.
A harness without a verified list (grok, cursor, pi, omp) fails closed: `--permission-override` is refused for it because sum cannot tell an override from an ordinary argument, and the record says `registry: none`.
`--permission-override` with nothing to authorize is refused as well, so a dangling authorization never reaches a record.
`preset show` lists a preset's override arguments under `launch.permission_overrides` when it carries any.

```sh
./bin/sumctl dispatch --repo R --brief B --approved --harness devin                                                  # no permission flag; the worker meets Devin's own trust gate
./bin/sumctl dispatch --repo R --brief B --approved --harness devin --arg=--permission-mode --arg=dangerous          # refused: an override without authorization
./bin/sumctl dispatch --repo R --brief B --approved --harness devin --arg=--permission-mode --arg=dangerous --permission-override   # recorded as requested, source explicit
./bin/sumctl reviewer launch TASK --preset rev --permission-override                                                 # a reviewer preset carrying an override
./bin/sumctl dispatch --repo R --brief B --approved --harness grok --permission-override                              # refused: no verified list for grok
```

A recorded launch is checked again at `start` and `execution resume`, after any `start --arg` is appended and before a pane is allocated (and, for a resume, before the successor attempt is recorded): every override in the argv must appear in the record's authorized `permissions.arguments`, otherwise the start is refused and the task stays as it was.
That covers a record prepared before this rule existed, whose Devin argv still carries the old implicit flags, and a `start --arg` that tries to add an override; the recovery is to dispatch the task again with `--permission-override` after the user authorizes it.
A running worker is never reconfigured or interrupted; the rule applies to new launches only.
Reading such a legacy record discloses its overrides with source `legacy-record` and no authorization rather than calling them the harness default.

Admission is unlimited until a capacity block is configured; see [capacity](#capacity) for the optional settings file.
SUM enforces a task allowance for out-of-scope corrections, not spending or wall-clock limits.

### Capacity

An execution slot belongs to a recorded worker attempt or an independent verification run, not to the unfinished task itself.
`prepare` reserves a worker slot before creating its checkout, and `verify --execute` reserves a separate slot before creating its verification checkout.
Admission uses the same global and per-repository limits under the local record lock.
A refused admission launches nothing.

An enrolled project can declare its own limit: `capacity.projects` maps an enrolled project name to the slots that project may hold, overriding `per_repository` for it alone.
Admission resolves the limit for one task as the project override when its enrolled project declares one, otherwise `per_repository`, and always enforces `global` first; worker preparation, `execution resume`, and `verify --execute` share that one resolver.
The task's scope is its enrolled project, identified from the registry at `prepare` (`--project NAME`, or a `--repo` path that is the enrolled clone or a linked worktree of it), and adopted through the registry by repository path at each admission for a task recorded before its clone was enrolled.
Held reservations for a project count every task recorded under that name, at the task's own repository path, or at the enrolled clone path, so a linked worktree prepared after enrollment, a symlink, or a pre-enrollment record at the clone path never evades the override or gets its own writer; a record prepared at a linked worktree before its clone was enrolled stays path-scoped.
A `--repo` path that is not an enrolled clone or one of its linked worktrees, including a second independent clone of the same remote, keeps today's behavior: its scope is its resolved git toplevel and its limit is `per_repository`.

Use `execution show TASK_ID` to read the current attempt IDs.
A launched worker attempt becomes `running` only when Herdr's `pane process-info` shows its occupant: the leader of the pane's foreground process group (the process whose pid is that group id), not the shell, observed exactly once with a pid and argv.
Children the agent forks into that group, such as a harness's MCP servers, do not make the identity ambiguous.
A foreground without such a leader (the shell holding it, or a leader that already exited) leaves the attempt `uncertain` with its reservation held; nothing binds a guessed pid, and an attempt recorded `uncertain` before this rule stays so.
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
./bin/sumctl settings set --project-limit docs=3 --project-limit app=1   # per-project overrides; the name must be enrolled
./bin/sumctl settings set --clear-project-limit docs           # that project returns to per_repository
./bin/sumctl settings set --clear-capacity                   # return to unlimited without changing worker or preset settings
./bin/sumctl execution show TASK_ID                         # read current attempt IDs and state
./bin/sumctl execution park TASK_ID --attempt ATTEMPT_ID     # inspect stopped execution; preserve unfinished work
./bin/sumctl execution resume TASK_ID --attempt ATTEMPT_ID   # reacquire capacity and launch approved work
```

`.sum/settings.json` is the one owner of executable admission values, worker launch defaults, named presets, and the evidence publication switch (`{"schema": 1, "capacity": {"global": N, "per_repository": M, "projects": {"NAME": K}}, "worker": {"harness": "codex", "model": "...", "reasoning": "..."} | {"preset": "deep"}, "presets": {"deep": {"harness": "codex", "model": "...", "reasoning": "...", "args": [...], "revision": 1}}, "reviewer": {"preset": "review"}, "evidence": {"auto_publish": false}, "usage": {"enabled": true}}`); capacity integers from 1 to 64, `per_repository` and every `projects` value at most `global`, `projects` keys enrolled project names; `capacity`, `capacity.projects`, `worker`, `presets`, `reviewer`, `evidence`, and `usage` are optional, a model/reasoning needs a verified adapter for its harness, and a referenced preset must exist.
An absent `evidence` block means `pr reconcile` publishes the worker's before/after evidence into the PR; `{"auto_publish": false}` is the only way to turn that off, and `settings show` reports the effective value with its source (`default` or `settings`).
Absent `capacity` means no admission cap, including when the settings file exists for worker or preset defaults; `settings show` reports `limits: null` for this state.
With a capacity block, `settings show` and `status` add `effective`: one row per declared project override and per scope holding reservations, with the resolved `limit`, its `source` (`projects.NAME` or `per_repository`), whether the name is still `enrolled`, and the held attempt ids; a capacity refusal names the same scope, limit, source, holders, and the key to raise.
A `projects` key naming a project that is no longer enrolled is reported `enrolled: false`, still caps at `global`, and governs only tasks recorded under that name; `settings set --project-limit` refuses a name that is not enrolled.
`.sum/preferences.md` and `.sum/projects.md` stay narrative and never set a limit or a worker default; only your explicit "make this my default" becomes a `settings set --worker-*` write.
An invalid file is refused with the exact defect before any side effect: nothing is admitted, no task or worker is touched, `ask`/`report`/`show` keep working, and `settings set` refuses to overwrite it silently.
Lowering a limit, global, per-repository, or per-project, affects future admission only; tasks above the new limit keep their slots, processes, and checkouts.
Raising `global` never raises `per_repository`: one checkout gets one writer unless you say otherwise.
Nothing schedules or dispatches work because a slot is free; a dispatch is always an explicit approved instruction.
The settings file travels with `sumctl backup`.
Legacy non-archived tasks without reservation metadata count as held until explicit stop inspection adopts them.
Malformed reservation metadata refuses admission and release rather than counting as free capacity.
Older helpers may preserve these records, but an old coordinator does not enforce the new reservation policy.

Rundown, delivery, and refresh over a fleet are one bounded pass: one `herdr agent list` snapshot per session replaces a per-worker observation call, each Herdr call gets its own timeout, no transcript is read, and one unobservable worker costs at most one bounded call: a Herdr call that fails or times out marks only its own session unavailable for the rest of that pass (its later recipients are `not-delivered`, which counts toward `stalled`), and other sessions are still served.
`inbox --live` and `status --live` report `fanout` with the sessions, Herdr calls, and elapsed and budget milliseconds of their read-only observation; `pump` and coordinator `init` (under `returns`) report the same for their delivery pass, plus the count of `deferred` recipients, whenever the pass visited a recipient; `refresh request` reports its Herdr calls and elapsed time.
A delivery pass has a 20-second budget (`pump --budget SECONDS` overrides it for one pass) and `sweep` a 60-second admission budget (`sweep --budget SECONDS`); neither is a setting in this file.

### Usage accounting

Usage accounting is off until `.sum/settings.json` carries `"usage": {"enabled": true}`; with the block absent or `false`, no command resolves a Claude projects directory, reads a transcript, or creates a usage record.
The block accepts exactly one boolean key, `enabled`; any other key or value refuses the whole settings file with the defect, like `hook` and `evidence`.
`settings set --usage-accounting true|false` writes it and `--clear-usage` removes it (coordinator only, atomic, every other settings writer preserves the block); `usage show TASK_ID` reports the effective value with its source.
An older `sumctl` refuses a settings file that carries `usage` as an unknown key, so remove the block before rolling back below the release that introduced it.

The one supported source is a Claude Code session transcript, verified against Claude Code 2.1.302: `<projects>/<slug>/<session-id>.jsonl` plus `<projects>/<slug>/<session-id>/subagents/*.jsonl`, where `<projects>` is `$CLAUDE_CONFIG_DIR/projects` from sum's own environment (else `$HOME/.claude/projects`) and `<slug>` is the pane's working directory, symlinks resolved, with every character outside `[A-Za-z0-9]` replaced by `-`.
A session belongs to a task only when sum recorded its id: the worker registration's `incarnation.agent_session` (agent `claude`, kind `id`, the conversation id Herdr reports at `start`), and the `incarnation` that `reviewer launch` records on the reviewer endpoint.
Sum opens no other path; a slug without a transcript is reported `unavailable` with the path it tried, and a task on another harness, or dispatched before the block was enabled, reports `unavailable` for the missing sessions rather than zero.

Each session is attributed to the worker attempt whose checkout matches the registration and that was current when the session was first seen, or to the reviewer endpoint; the category is `implementation` for a worker attempt, `repair` when that attempt's repair operation has `kind: send`, `review` for the reviewer, and `unknown` when the attempt cannot be established; a plain `execution resume` stays `implementation`.
One invocation is one distinct `message.id` among `type: "assistant"` lines; its outcome is the last `stop_reason` recorded for that id (`end_turn`, `tool_use`, `max_tokens`, `stop_sequence`), `unrecorded` when the source never wrote one (subagent transcripts usually do not), or `api_error` for an `isApiErrorMessage` line.
Tokens are summed once per `message.id` from `message.usage` (`input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`); a field the source never reported is `null`, never zero, and a total names how many sessions were unknown.
Per-session timing is the first and last line timestamp and the derived `wall_ms`; the source carries no per-invocation duration, so none is reported, and nothing is inferred from elapsed time or transcript length.

Collection runs at exactly three points: `usage collect TASK_ID` (coordinator only), `report` after the task record is saved, and `cleanup` (including `--reviewer-only`) after reservations are released; read-only views never collect.
The ledger `.sum/tasks/TASK_ID/usage.json` keeps, per source file, the byte offset consumed and the last `message.id` folded; a later collection resumes from the offset, skips a leading repeat of that id, stops before a trailing partial line without advancing, and stops at the first unparsable line with status `partial` and the offset kept before it; a file shorter than its offset is re-read from zero; a file that disappears keeps its counters and marks the session `partial`.
One collection holds the state lock from load to write and writes through the same temp-and-rename as every record, so an interrupted write never publishes a partial ledger and two collections never fold the same delta; collection runs only on the task's recorded machine and skips elsewhere with a warning.
A session is `partial` with reason `session-live` while its attempt is held or its registration still occupies the pane, and `complete` only after release when the file was read to its end; task coverage is `complete` when every known worker attempt and reviewer endpoint has a recorded session and every session is complete, `partial` otherwise with the reasons, and `unavailable` when nothing could be read, the harness is unsupported, or no ledger exists.
A Claude restart inside one attempt between two collections replaces the registration's conversation id before sum sees the old one; that gap is not detectable and is the one known undercount.
Any source or ledger failure is a warning under `usage.warnings` in the command result and never changes report acceptance, delivery gates, reservation release, cleanup, or the exit status.
The ledger survives `archive` and `cleanup` and travels with `sumctl backup`; it holds counts and ids only, never transcript text, and nothing publishes it to GitHub.
Totals are counts, not a bill: no dollar estimate, quota, enforcement, daemon, account or model switching, or second harness adapter is involved.

```sh
./bin/sumctl settings set --usage-accounting true     # coordinator only; written atomically beside the other blocks
./bin/sumctl usage show TASK_ID                        # per-session rows, totals by category and task-wide, coverage with reasons
./bin/sumctl usage collect TASK_ID                     # coordinator only; folds new transcript lines from each recorded cursor
./bin/sumctl settings set --clear-usage                # back to the default, which is off
```
