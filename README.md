# sum

**Many agents. One finished task.**

A small, Herdr-native agent distro. Launch a coding harness in this directory and it becomes the coordinator: it dispatches every approved request to a worker agent, stays free for inbox notices, gathers results, and brings decisions back to you.

**MVP, not an unattended factory.** Instructions and skills do the reasoning. Herdr owns processes and worktrees. A small synchronous helper preserves task records. There is no sum daemon, scheduler, database server, or permanent hierarchy of managers.

Roles and work objects use the dictionary in [docs/TERMINOLOGY.md](docs/TERMINOLOGY.md).

## Start

Prerequisites: **mise**, Git, and an authenticated coding harness available on the execution host. Setup installs the other dependencies locally through mise. On a Mac without mise, install it through your normal package manager first (`brew install mise`).

Once this repository is published:

```sh
git clone https://github.com/douglasjarquin/sum.git
herdr
# In a Herdr pane:
cd /path/to/sum
mise trust
mise run setup
codex                         # or claude, grok, cursor-agent, pi, opencode, ...
```

If Herdr is not installed yet, run `mise trust && mise run setup` in the clone first, then `mise exec -- herdr`. If mise is not activated in your shell, launch the harness with `mise exec -- codex` instead of bare `codex`.

Accept the harness's normal project/MCP trust prompt. Do not bypass permissions. Codex, Claude, and Cursor receive repository-local MCP configuration; OpenCode receives `opencode.json`. Other shell-capable harnesses can use the same native Herdr CLI skill without MCP. A harness that does not auto-load `AGENTS.md` needs this first prompt:

> Read AGENTS.md in this directory, initialize sum, and act as my coordinator.

Then delegate normally:

> Use Codex to fix the failing login test in `/Users/me/projects/myapp`. Keep scope to the bug, run the existing checks, arrange independent review, and bring me a PR. Do not merge it. Ask before changing the public API.

The coordinator creates a task brief, prepares a separate Herdr worktree, launches the selected worker, submits its brief, and returns control to you. A worker is explicitly given its own role; it does not become another coordinator.

### Session roles

Every harness session starts with `./bin/sumctl init` and follows the role it returns. The first pane in the installation (the checkout where setup ran) claims `coordinator` atomically; exactly one pane wins a simultaneous start. A pane dispatched by the coordinator is registered as that task's `worker` and stays a worker even when its task is editing sum itself. Any other pane, including a second harness you open in the same directory to work on sum, becomes a `developer`: it sees who owns coordination, cannot dispatch or rebind tasks, and can only observe through the Herdr bridge. Identity is the verified machine, Herdr session, and pane, never the working directory. `sumctl doctor` only observes; it never binds. Role bookkeeping prevents accidental takeover; it is not a security sandbox against code running as your user.

### No installed harness?

```sh
mise run setup -- --install-codex
mise exec -- codex             # authenticate using the normal provider flow
```

This optional path installs a pinned Codex CLI inside `.deps/harnesses`, not globally. Other harnesses are deliberately bring-your-own: no subscription, API key, or account is created or switched by setup.

**New local executable caveat:** worker shells must be able to resolve the selected harness too. If you just installed a local harness after starting Herdr, use a fresh named session launched from the configured environment (`mise exec -- herdr --session sum`) and verify the executable in that session. Do not restart an existing busy server just to refresh its environment. Already-installed host harnesses are the ordinary path.

Herdr's optional native integrations can be installed separately, for example `herdr integration install codex`. Those modify the harness's own configuration, so sum setup does not install them implicitly.

## What's included

| Component | Purpose |
| --- | --- |
| `AGENTS.md` and harness instruction aliases | A short coordinator contract, with a separate worker role |
| Plain-language dictionary | [docs/TERMINOLOGY.md](docs/TERMINOLOGY.md): user, coordinator, agent, reviewer, task, brief, question, inbox, result |
| Six bundled skills | Dispatch, worker execution, verification/PR delivery, rundown/recovery, isolated self-development, and atomic updates with rolling session refresh and code-only rollback |
| Project-local third-party skills | `bin/sumctl skills install` delegates explicit skill and agent selections to the pinned Vercel Skills CLI in project copy mode; see `docs/sum-skills.md` |
| Evidence publication into the PR | `.agents/skills/evidence/scripts/evidence_publish.py` and `sumctl pr evidence`: validated before/after media (hashes, containment, size, type, secret checks) uploaded through `gh pr edit --attach` (GitHub CLI 2.99+) into one marked block of the reconciled PR; receipts per content hash and destination so a second publish uploads nothing; prose outside the block preserved; a foreign or hand-edited block refused; every failure leaves the previous body and the local originals intact; an older `gh` defers |
| Portable verification contract | A project-root `VERIFY.md`, the canonical `mise run verify` aggregate, feature maps under `docs/features/`, and the distributable `.agents/skills/verify` runner that records candidate-bound run evidence under Git-ignored `.artifacts/verification/`; works in any clone without sum or Herdr |
| Verification authoring skills | `.agents/skills/create-verification` inspects a repository, scaffolds its contract, `verify` task, seed feature maps, and vendored skills without inventing commands, then proves them once; `.agents/skills/maintain-verification` audits stale tasks, paths, links, and coverage claims, names the maps a change touches, and records a no-map-change rationale; both keep authored map edits separate from run results and work in any clone |
| Release-matched Herdr skill | Copied from the installed `herdr --skill` during setup |
| Pinned Herdr Mesh plus a small runtime overlay | Ten relevant MCP tools, current Herdr commands, bounded reads/waits, no swallowed handoff errors |
| `bin/sumctl` | Durable task/decision/report files, native worktree creation and launch, bounded notices, guarded cleanup of merged task workspaces, records backup, staged releases, atomic update/rollback, and per-session refresh bookkeeping |
| Optional Herdr plugin (`sumctl hook`) | A per-installation manifest under `.sum/hook` whose event handler runs the same bounded pump and records native attention, plus a read-only inbox pane entrypoint; no daemon |
| Optional native metadata (`sumctl metadata`) | sum task state (needs-decision, review-ready, merged-cleanup-pending, instruction-refresh-pending, ...) projected as namespaced `sum_*` tokens on the endpoints sum owns, rendered by sidebar rows the user chooses to add; opt-in coalesced notifications |
| Remainder (Codex TOON) and `quota-axi` (other providers) | Advisory quota evidence through `sumctl quota --provider NAME`; no automatic billing or account switching |
| Pinned codegraph per checkout (`sumctl graph`) | `@colbymchenry/codegraph` 1.5.0 from the mise pin, run in CLI mode once in every checkout sum creates (task, coordinator verification, self-development) after its Git identity is validated; one `.codegraph/` index local to that checkout, kept out of `git status` through the repository-local exclude; exact commands and the source fallback in the brief; bounded retries, bounded concurrency, no daemon, no global configuration |
| Offline tests and a demo | Test behavior without model credentials, a real Herdr installation, or GitHub writes |
| Explicit live smoke test | Validate the real Herdr API in an isolated named session |

The helper is called `sumctl` to avoid shadowing the Unix `sum` command. Normally you talk to the harness, not the helper.

## State and communication

Private state lives in `.sum/` and is ignored by Git. `.sum/context.json` records the coordinator owner and `.sum/sessions/` the registered panes and roles. Optional `.sum/preferences.md` and `.sum/projects.md` hold local preferences and project notes; `.sum/projects.json` is the registry of enrolled projects (host/owner/repo, verified remote, canonical path, enrollment metadata), while their clones live outside the state under the Git-ignored `projects/` directory. `.sum/coordinator/` holds the coordinator's contract revisions and refresh receipts. Each task stores its brief, base SHA, branch/worktree, pane bindings, questions, answers, report, an append-only `evidence` list (reports, structured handoffs, reviewer findings, coordinator verification, exact PR observations), the bound reviewer endpoint, the last reconciled PR identity, a `versions.json` sidecar with brief revisions, and an optional `environment.json` sidecar with the task-local environment record. File updates are locked and atomically replaced on one local machine.

Workers use the exact commands in their generated brief. The core interaction is:

```sh
./bin/sumctl inbox --live
./bin/sumctl show TASK_ID
./bin/sumctl answer TASK_ID QUESTION_ID --text 'Keep both endpoints for one release.'
```

`show` returns the whole record and keeps its shape. `context` is the bounded read beside it:

```sh
./bin/sumctl context TASK_ID                              # outline: counts, outstanding decisions, latest handoff, cursor
./bin/sumctl context TASK_ID --role worker|reviewer|coordinator   # the sections and short contract that role needs
./bin/sumctl context TASK_ID --section decisions --after 20 --limit 20   # stable pages with total/omitted/next_after
./bin/sumctl context TASK_ID --section brief --revision r1 # a recorded brief revision, hash-verified, never rewritten
./bin/sumctl context TASK_ID --since CURSOR               # what changed since the `cursor` of an earlier read
./bin/sumctl notes TASK_ID --text 'finding'               # the one optional task-local notes.md (a claim; secrets refused)
./bin/sumctl env discover TASK_ID                         # declared dev commands from mise/package/Makefile/justfile/Procfile/compose/Dockerfile/devcontainer
./bin/sumctl env record TASK_ID --url http://127.0.0.1:PORT [--log PATH | --pane ID | --container ID] [--ownership shared]
./bin/sumctl env inspect TASK_ID                          # re-observe once: stale URLs, configuration drift, missing logs
./bin/sumctl env start TASK_ID --command NAME [--url http://127.0.0.1:PORT | --match TEXT] [--log PATH] [--timeout S]   # launch one declared command in a pane split under the worker; identity recorded, readiness bounded
./bin/sumctl env stop TASK_ID [--service ID]              # one interrupt to re-proven sum launches only; verified exit, then the sum-created pane closes
./bin/sumctl help [TOPIC]                                  # command discovery without the full manual
```

Sections: `outline`, `brief`, `decisions`, `handoff`, `evidence`, `execution`, `environment`, `update`, `returns`, `notes`. The `environment` section carries `dev`, the task-local environment record in `environment.json`: command references discovered from the checkout's declared configuration (never executed, never a generated `sum.yml`), URLs recorded together with what `lsof` observed for their port at that moment and an ownership classification (`owned` when the listener runs inside this checkout, `shared` for another task's checkout or a deliberately shared service, `unknown` otherwise), log paths stat'ed without following symlinks (a symlinked component under the checkout is never resolved), related pane/container identities, and observation times. A URL owned by another active task is refused so parallel tasks never reuse one by accident; writing a port never binds or reserves it. `env inspect` re-observes on demand and marks stale endpoints and configuration drift; nothing polls or restarts. `env start` launches one command the repository declares (an `env discover` row, run through its own runner: `mise run`, `npm|pnpm|yarn|bun run`, `make`, `just`, a Procfile line, or `docker compose --project-name sum-<task> up <service>` so container resources are task-scoped) in a pane split under the worker pane inside the checkout; the launch intent is written before the pane exists, the pane before the command runs, and the observed process instance (pane, shell pid, pid, argv, cwd) afterwards. Readiness is one bounded wait for the given `--url` port to be taken by that pane's process (a foreign occupant is a recorded `conflict`, never terminated) or for `--match` text; a timeout is an explicit `failed` state, never a retry or restart. `env stop` re-proves that identity (pane in the task workspace and checkout, same shell pid, same pid and argv), sends exactly one `ctrl+c` through Herdr, waits a bound for exit, verifies the port is released, and closes only the pane sum created; anything unproven, restarted outside sum, or still running after the bound stays recorded (`unknown`/`stopping`) and blocks cleanup visibly. Environments started by hand are never stop targets, a shared database or another task's server never gains stop authority through observation, and an unrecorded pane in the task workspace (for example from a launch interrupted before registration) blocks a new launch until someone records or closes it: sum neither adopts nor closes it. Credentials in URLs, labels, or paths are refused; every string of a command reference, including `user:password@` in URLs, is redacted at write. Observation (`lsof`, `lstat`, `pane get`) runs before the store lock so a slow pass never stalls the coordinator. Tasks without a record behave as before, and the record travels in `backup` with those exclusions named. Prose fields are bounded (`--max-chars`, 0 for all) and credential-shaped text is redacted in the view, never in the record. Outstanding decisions are listed in full on every read regardless of paging. Worker prose, handoffs, and notes are labelled claims; only `verify` and `pr reconcile` records are verification evidence. Skills reach agents as explicit file references (path, size, hash) to read when needed, not as an assumed skill standard. Nothing here calls a model or reads a worker's checkout.

A question is saved **before** notification. A notification is attempted only after an idle/done preflight; a busy, absent, blocked, or unverifiable recipient leaves it pending. A successful send means *submitted, not acknowledged*. Saved answers stay visible until the worker marks them applied.

Every pending return is derived from the records themselves: an open question and an unverified report are owed to the parent, an unapplied answer and a requested brief revision to the worker. Nothing has to be acknowledged for it to stay listed, and nothing but a later record (an answer, `resolve`, `verify`, a PR observation, `brief adopt`) closes it. The per-task `returns.json` sidecar keeps only notification state, so a legacy helper rewriting `task.json` cannot erase it.
Each task write, `inbox --live`, a coordinator `init`, `bind`, and the explicit `sumctl pump` run one synchronous pass: open returns are grouped by their current recipient identity and each recipient gets at most one fixed notice naming the record IDs and commands (never question or report prose). A pending return is sent once; a known failure (busy, absent, wrong checkout, not registered) is retried on later passes up to three times and then shows `stalled`; a timeout after a possible submission or an interrupted pass stays `uncertain` and is never re-sent by itself. `sumctl notice TASK_ID --to parent|worker` is the explicit single retry for those. `show` and `inbox` carry the `returns` view; the old single `notice` field mirrors the latest attempt for existing readers.

There is no retry loop while you are away. Run a rundown to find pending returns and workers that stopped without a report. Native `idle`/`done` is not task completion, and a worker's report is not verified success.

### Native event delivery (optional)

`./bin/sumctl hook enable` (coordinator only) writes a Herdr plugin manifest under `.sum/hook/plugin/` for this installation and links it live with `herdr plugin link`; the server keeps running.
The plugin declares `pane.agent_status_changed`, `pane.agent_detected`, `pane.exited`, `pane.closed`, `workspace.closed`, and a startup hook, all running the same command: the installation's `bin/sumctl --home <this .sum> hook event`.
The home is fixed in that command at enable time; the handler never derives it from its working directory or the payload, and a runtime update or rollback changes what the handler runs without touching the registration.
Herdr plugin registration is user-global, so the handler receives events from every session. It acts only when the event's own session and pane match this installation's recorded coordinator or a recorded worker; everything else is counted as `ignored` and touches no record. Two installations get two plugin ids and two homes.
On a recorded pane going `idle`/`done`, the handler re-observes the pane and runs one bounded pump toward that recipient: pending questions, reports, answers, and requested brief revisions are delivered at the boundary instead of waiting for the next rundown. A stalled return is tried again on such an edge because every earlier failure was a known non-delivery; an `uncertain` one is never re-sent by an edge. Unchanged busy status and bookkeeping events wake nothing.
A recorded worker seen `blocked`, idle with nothing owed in either direction and no report, exited, or closed produces an **attention** record on the task with a bounded recent-output excerpt and a pointer to `herdr agent read`. It is evidence, not a question, a result, a quota diagnosis, or permission to approve anything; it is owed to the coordinator like any return and is superseded the moment the worker saves a question or report, closed when the worker resumes, or marked with `sumctl attention TASK_ID ATTENTION_ID --seen`. An open saved question is preserved regardless of newer output.
`hook enable` runs one explicit reconciliation (attention from one snapshot per session, then the pump) because Herdr does not run startup hooks at link time; the startup hook does the same after a server restart, and `inbox --live` does it on every rundown while the hook is enabled. Herdr keeps no durable event replay, so missed events are recovered by these reconciliations, not claimed.
`hook status` shows enabled/disabled, the last handled event, a bounded error log, and the count and age of pending returns; `hook disable` (or `--unlink`) turns the plugin off. Disabled, degraded, or crashing, the handler changes nothing about `ask`/`report`, `inbox --live`, `init`, `bind`, and `pump`: they remain the delivery path and never stop.
Native idle does not detect a question asked only in prose; the attention record points a human or the coordinator at the pane, and the rundown captures the question with `sumctl ask`.

### Native metadata (optional)

`./bin/sumctl metadata enable` (coordinator only) first reads the installed Herdr's own `api schema` to confirm `pane.report_metadata`, `workspace.report_metadata`, and `notification.show` exist in the pinned build; documentation fields are never assumed.
It then projects every saved task once and, from that point, after each state-changing helper command (`ask`, `answer`, `resolve`, `report`, `verify`, `pr reconcile`, `brief`, `refresh`, `update apply`, `bind`, `archive`, `cleanup`, `init`, `inbox --live`) and each native event the hook handles.
The projection is display only: `sum_state`, `sum_task`, `sum_repo`, an optional `sum_rev` (`r1>r2` while a brief revision is requested and not adopted), and an optional `sum_pr` (the exact recorded PR URL) on the task's Herdr workspace and on its worker pane; `sum_inbox` (for example `2 decision · 1 review · contract r2`) and `sum_tasks` on the registered coordinator pane.
The state comes from the records alone, ordered by what you act on first: `needs-attention`, `needs-decision`, `merged-cleanup-pending`, `review-ready`, `attention-blocked|exited|closed|idle`, `instruction-refresh-pending`, `answer-pending`, `pr-open`, `verified`, `preparing`, `running`.
Herdr's agent lifecycle (`working`, `idle`, `blocked`) is never reported or overridden by sum; a worker seen `working` beside a `needs-decision` token is exactly the truth. Pane labels, titles, display names, state labels, theme, keybindings, and every other reporter's tokens are untouched.
All keys live under sum's own source `sum:<instance>`; a worker pane receives tokens only after its identity is verified (the session snapshot or one `pane get` shows the recorded checkout), a stale or reused pane gets nothing and loses sum's old keys, a rebind clears only sum's keys on the old pane, and archive, cleanup, or `metadata disable` clear exactly the recorded keys. Two installations use two sources and two homes.
Writes happen only when the derived tokens differ from what sum last wrote: a duplicate event or an unchanged rundown makes no Herdr call; the coordinator line is recomputed from local records, never from a fleet observation.
Tokens are invisible until the user adds `$sum_state`, `$sum_task`, or `$sum_inbox` to their own `[ui.sidebar.agents]`/`[ui.sidebar.spaces]` rows; `./bin/sumctl metadata snippet` prints a starting point and sum never writes `config.toml`.
Notifications are off by default. `metadata enable --notify` sends at most one `notification show` per projection pass, only for a task newly entering `needs-decision`, `review-ready`, `merged-cleanup-pending`, `instruction-refresh-pending`, `attention-blocked`, or `needs-attention`, naming task ids, states, and repository names only; question and report prose never travel. Delivery follows the user's own `[ui.toast]` setting, and `shown: false, reason: disabled` is recorded honestly when that delivery is off.
`./bin/sumctl metadata inbox` opens the ordinary `sumctl inbox` listing (records only, no prompt) as a popup or split pane through the linked sum plugin's `inbox` entrypoint; it needs `hook enable`, reads nothing new, and is not a dashboard.
A missing capability, a refused write, or a failed notification is recorded as `degraded` in `metadata status`, `inbox --live`, and coordinator `init`; the command that changed the record still succeeds, the CLI and rundown stay authoritative, and `metadata disable` returns Herdr to exactly its previous presentation.

To create a task manually:

```sh
cp templates/task.md /tmp/my-task.md
# Edit the brief, then from inside a Herdr pane:
./bin/sumctl dispatch --repo /absolute/path/to/repo \
  --brief /tmp/my-task.md --harness codex --approved
```

`--approved` records the caller's assertion of approval; it is not a security boundary. `prepare` creates the record/worktree without launching; `start TASK_ID` starts that prepared task once. An uncertain launch is retained for inspection and cannot simply be started again.

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
SUM enforces a task allowance for controlled corrections and relaunches, not spending or wall-clock limits.

### Capacity

An execution slot belongs to a recorded worker attempt or an independent verification run, not to the unfinished task itself.
`prepare` reserves a worker slot before creating its checkout, and `verify --execute` reserves a separate slot before creating its verification checkout.
Admission uses the same global and per-repository limits under the local record lock.
A refused admission launches nothing.

Use `execution show TASK_ID` to read the current attempt IDs.
After a worker exits, `execution park TASK_ID --attempt ATTEMPT_ID` checks its recorded pane, checkout, processes, and owned services before releasing the slot.
The command stops nothing and preserves questions, reports, evidence, and the checkout.
A report, an idle or `done` pane, a missing pane, or an uncertain observation cannot release capacity.
Owned services keep the worker reservation held until their shutdown is proven.

To continue approved work, use `execution resume TASK_ID --attempt ATTEMPT_ID` with the released worker attempt ID.
Resume checks capacity again, saves the previous attempt in the task's evidence history, and records a new attempt before launching.
An old attempt ID cannot release or resume its successor.
Use the current verifier attempt ID with `execution park` to reconcile an interrupted verification only after its operation and checkout processes are conclusively stopped.
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

`.sum/settings.json` is the one owner of executable admission values, worker launch defaults, and named presets (`{"schema": 1, "capacity": {"global": N, "per_repository": M}, "worker": {"harness": "codex", "model": "...", "reasoning": "..."} | {"preset": "deep"}, "presets": {"deep": {"harness": "codex", "model": "...", "reasoning": "...", "args": [...], "revision": 1}}, "reviewer": {"preset": "review"}}`); capacity integers from 1 to 64, `per_repository` at most `global`; `capacity`, `worker`, `presets`, and `reviewer` are optional, a model/reasoning needs a verified adapter for its harness, and a referenced preset must exist.
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

### Controlled repairs

Each task starts with an allowance of two SUM-controlled repair iterations.
Send a correction to its settled worker with the current attempt ID and a stable instruction key:

```sh
./bin/sumctl repair send TASK_ID --attempt ATTEMPT_ID --key correction-1 --file /absolute/path/to/correction.md
```

The command records the iteration before delivery and refuses a busy worker or a changed attempt.
Repeating the same key and instruction reads the saved outcome without sending or charging again.
An uncertain delivery stays charged, including when the helper exits after saving its intent.
Use a new key only for an explicitly requested new iteration.

Every `execution resume` also consumes one iteration, including an infrastructure relaunch.
Initial dispatch, observations, notifications, required worker and coordinator verification, and brief refresh do not consume extra iterations.
Candidate, harness, and runtime changes do not reset the task record.
Read the `repairs` field in `sumctl show TASK_ID` for consumed operations and grants.

Exhaustion refuses another correction or relaunch and saves one budget decision in the task's questions.
It stops no worker, frees no slot, and preserves other obligations.
Only after the user approves an additional allowance, record that decision from the coordinator pane:

```sh
./bin/sumctl repair extend TASK_ID --question QUESTION_ID --additional 1 --approved --file /absolute/path/to/user-decision.md
```

The grant is tied to that exhaustion decision; repeating an identical grant adds nothing.
An ordinary answer or worker report does not grant more iterations.
If the decision is already answered, explicit confirmation must preserve its exact text.
Worker-internal loops and commands issued directly to an external harness remain outside this mechanism.
Older helpers can preserve the records without enforcing this policy.

### Cleanup after a merge

A merged PR archives nothing by itself.
When you say a task is merged, or a rundown shows a task as `cleanup: pending`, the coordinator runs the guarded cleanup:

```sh
./bin/sumctl cleanup TASK_ID                 # inspect: fresh GitHub observation, identity, occupants, artifacts; persists the plan, removes nothing
./bin/sumctl cleanup TASK_ID --apply         # remove the verified workspace and clean checkout through native Herdr, then archive the record
./bin/sumctl cleanup TASK_ID --reviewer-only # close only a bound reviewer pane that saved its findings and whose agent exited
```

`--apply` proceeds only when every check passes, and every failed check is a named blocker in the output and in `show TASK_ID`:
the exact recorded PR must be observed **merged** with a merge commit and its head must be a recorded candidate (closed is not merged, a network or auth failure is uncertain and blocks);
the checkout HEAD must be that merged head or an ancestor of it, so squash and rebase merges pass without the original commits being on the default branch, while an extra local commit blocks;
all questions must be answered and applied and a structured handoff saved;
the workspace, pane, checkout, branch, and repository must match the task record by identity (never by label), the workspace must not be the coordinator's, and an unknown extra pane in the task workspace blocks;
the agent must be gone from the pane (Herdr `idle` or `done` is not exit), the pane must run only its shell, and no process may have a cwd inside the checkout, observed through `agent get`, `pane process-info`, and one bounded `lsof` pass; when that cannot be established the task stays cleanup-pending;
staged, modified, untracked, and ignored files block, except a fixed list of regenerable caches (`__pycache__`, `*.pyc`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `node_modules`, `.DS_Store`), and nothing is ever `git clean`ed, reset, or force-removed.

The checks run again right before the removal, the removal is one `herdr worktree remove --workspace ID` without `--force`, and the cleanup intent is saved before that call.
Services the worker launched with `env start` (#17) are judged by identity: when the recorded pane, shell pid, pid, and argv still match, and only evidence-complete resource-state blockers remain, `--apply` stops exactly those first with one `ctrl+c`, a bounded exit wait, and a port check, closes the pane sum created, and re-inspects; a `service-unknown` blocker (restarted or replaced outside sum), a `writing` blocker (an owned log modified within the last seconds), or a survivor after the bound keeps the task cleanup-pending with the reason named. sum never kills by name, port, or cwd, never runs a broad compose down, and never closes a pane it did not create.
If sum is interrupted between the removal and the archive, the next `cleanup TASK_ID` or `inbox --live` reconciles from records and observation: verifiably absent resources complete the archive, a still-present workspace returns the task to pending, anything else blocks with the observed state.
Already-absent resources are accepted only after that identity inspection; nothing is recreated.
The task branch, `brief.md`, brief revisions, decisions, reports, handoffs, reviewer findings, and PR evidence always stay.
Cleanup records the released reservation before archiving.
Once cleanup saves destructive intent, worker, verifier, and service launches are refused until cleanup finishes or conclusively refuses removal.
An uncertain removal keeps that exclusion until reconciliation.
Competing cleanup commands for the same task refuse while one owns the operation; live-inbox reconciliation cannot rewrite an active cleanup.
`archive --acknowledge` remains records-only, refuses any held reservation, and never removes anything.
Other workers keep running; there is no global stop, restart, or merge poll daemon.

### Brief revisions

A worker brief is generated from the record: the approved task text, base, repository, and kind (immutable input), the decisions recorded so far, the current worker procedure, and the return-channel commands. Each task keeps a `versions.json` sidecar with the sum version and brief schema it was dispatched under and a numbered list of brief revisions. Tasks recorded before this sidecar existed are read as legacy `0.1.0`/schema 1 records; nothing is migrated in place.

```sh
./bin/sumctl brief list TASK_ID          # revisions, integrity, active/requested state, report evidence binding
./bin/sumctl report TASK_ID --file r.md --handoff h.json   # worker: prose report plus a bounded structured handoff bound to the candidate SHA
./bin/sumctl review TASK_ID --verdict changes-requested --candidate SHA --file f.md   # reviewer pane: appended findings; binds the reviewer endpoint
./bin/sumctl verify TASK_ID --candidate SHA --result pass --text '...'               # coordinator: own verification record (prose)
./bin/sumctl verify TASK_ID --candidate SHA --execute                                  # coordinator: run the candidate's VERIFY.md contract in a separate checkout under its own run id
./bin/sumctl review TASK_ID --verdict approve --candidate SHA --tool made --text '...' # coordinator: the configured MADE/No Mistakes result; binds no reviewer pane
./bin/sumctl pr reconcile TASK_ID --number N   # coordinator: exact PR identity observed through gh; merged only for a matching head
./bin/sumctl brief regenerate TASK_ID    # stage briefs/rN.md from the record; no model call; duplicates write nothing
./bin/sumctl brief request TASK_ID rN    # mark the latest intact revision as requested; sends nothing
./bin/sumctl brief adopt TASK_ID rN      # the worker records that it now follows the requested revision
```

The file at `brief_path` and every earlier revision are never rewritten, so a worker mid-read keeps a valid brief. Each revision records a machine-generated summary of what changed (policy versions, procedure hash, decisions, commands) and whether verification is affected. A report is bound to the brief revision active when it was submitted; a later verification-affecting revision marks that evidence as needing refresh review rather than approving or rejecting it. Refresh bookkeeping is separate from the notice mirror; a requested revision that `refresh request` could not deliver rides the next coalesced worker notice and is recorded as an attempt in the sidecar. Old helpers keep working on the same records because both sidecars are additive.

## Update and roll back

```sh
./bin/sumctl update check          # fetch origin, resolve the merged revision, report default/active/compatibility
./bin/sumctl update apply          # stage the release, validate coexistence, switch .local/current in one rename
./bin/sumctl update status         # old/new SHA, default versus active runtime, staged releases, recent selections
./bin/sumctl update rollback       # reselect the previous runtime; records, questions, reports, and worktrees stay
./bin/sumctl update recover --generation GENERATION  # resolve one interrupted activation, without repeating the update
```

An update activates only a revision merged on the sum `origin` default branch, resolved to an immutable SHA.
It never pulls or resets the checkout, never restarts Herdr, agents, dev services, or a connected MCP server, and never upgrades Herdr.
Network, build, and dependency work happen before the activation lock; validation covers the release manifest, the state and brief schemas of the recorded tasks, the installed Herdr, the pinned tools, and a read-only run of the candidate helper against the real records.
Staged files are not approval: `.local/approvals.json` binds approved revisions to this installation, while `.local/activation.json` records the known-good runtime and any pending activation.
Before switching, SUM prepares an independent recovery command using the previous runtime.
If the new stable entrypoint fails its check, SUM restores and checks the previous known-good runtime; if recovery also fails, it preserves the pending operation and reports both failures.
An interrupted update requires explicit generation-bound recovery before another apply or rollback; the atomic pointer change alone does not prove activation completed.
Rollback requires an approved compatible target, and checkout rollback also requires a clean checkout with a matching approval.
Commands already running finish on the runtime they resolved; worker briefs carry stable `<installation>/bin/sumctl` commands, so their callbacks keep working across the switch; new dispatches use the new default; connected MCP clients keep their tool set until the client itself restarts.
A refusal names the exact incompatibility and leaves the old installation serving.
See `skills/sum-update/SKILL.md` for bootstrap, canary, and rollback steps.

### Refresh running sessions

```sh
./bin/sumctl refresh request               # coordinator contract plus every non-archived task; --task TASK_ID or --coordinator narrows it
./bin/sumctl refresh status                # confirmed / submitted-unconfirmed / pending-busy / pending-unreachable / capability-deferred
./bin/sumctl refresh adopt --coordinator rN
```

An update changes which code new commands run; a refresh asks the sessions that are already running to reread their operating instructions, one at a time, without restarting anyone.
For each target the coordinator stages the next immutable revision from the current runtime (a worker brief `briefs/rN.md`, or a coordinator contract snapshot under `.sum/coordinator/`), records it as requested, and then makes one delivery attempt through Herdr's agent boundary: only a pane that exists, runs in the recorded checkout, and is reported idle or done receives a short fixed message naming the revision, the machine-generated change summary, the file, and the exact adopt command.
No question, answer, or report text is ever placed in that message.
A busy, blocked, unknown, missing, or refusing session keeps working on its current brief and shows as pending with the exact reason; nothing polls, sleeps, or relaunches it, and a later `refresh request` or `inbox` rechecks it.
A worker adopts at its next safe point with `brief adopt`, keeping its process, checkout, partial edits, commits, report, and repair count; the coordinator adopts its own contract with `refresh adopt --coordinator`.
Four things stay separate: the installation default, the runtime a process actually resolved, the revision requested of a session, and the revision that session reports it has read.
A submitted prompt is not a receipt and a receipt is not proof of compliance.
Two requests before a receipt coalesce to the newest revision, a stale receipt is refused, a rollback stages the next revision from the rolled-back runtime, and already-connected MCP clients keep their tool set (`capability-deferred`) until they restart.
Developer sessions are excluded from the fan-out.

## Test it

Offline, without installing the full toolchain:

```sh
python3 -m unittest discover -s tests -p 'test_*.py' -v
(cd go && go test ./internal/mesh ./cmd/herdr-mesh)
python3 scripts/demo.py
```

Or after setup:

```sh
mise run test
mise run demo
mise run test-live             # explicit, isolated real-Herdr smoke test
```

The demo uses **real Git and a strict fake Herdr**, plus a scripted worker—not a real coding model. It exercises dispatch, a question while the parent is busy, answering, a real task-branch commit, a saved report, a native idle edge delivering a pending question, and a records-only backup. It never changes your live sessions or pushes code.
The live smoke test links the plugin into an isolated Herdr registry (`XDG_CONFIG_HOME` under the lab directory) and drives real `pane report-agent` edges; it never touches your user-global plugin list.

Setup also runs an MCP initialization/tool-discovery smoke test after installing Mesh. See [validation](docs/VALIDATION.md) for what was actually executed versus what remains to be run on a networked host, and [acceptance](docs/ACCEPTANCE.md) for the first real-harness tasks.

## Recovery and backup

After reopening the coordinator, run `./bin/sumctl init`. If the previous coordinator pane is verifiably gone, run `./bin/sumctl init --role coordinator --reclaim`; it proceeds only when Herdr reports that pane as `pane_not_found`; a pane that still exists (even with its agent exited) or that Herdr cannot observe is refused, and it never rebinds tasks by itself. Then run a rundown. To route an existing task back to the new coordinator:

```sh
./bin/sumctl bind TASK_ID --parent-only
```

If a worker's pane changed, inspect the actual agent and use `bind TASK_ID --worker-pane PANE_ID`. The helper checks the recorded worktree. It never creates a replacement automatically. Herdr remains the sole owner of process/session restoration; sum does not alter its auto-resume setting.

```sh
./bin/sumctl backup ~/backups/sum-records.tar.gz
```

This is deliberately **records-only**. The manifest lists the code worktrees that were **not** captured. Back up source code and unpushed work separately. No credential store is copied, but task text can itself contain sensitive information; protect the archive accordingly.

Restore into a new empty directory, inspect the manifest, and point `sumctl --home /restored/state` at it. Keep the original archive. Unknown schema versions fail without modifying them. Pane IDs are machine-local; cross-machine process recovery is manual in this MVP.

## Limits worth knowing

- Plain-text questions from non-cooperative workers are found during a rundown, not guaranteed to be detected immediately while unattended.
- This is a trusted-local workflow, not an adversarial sandbox. Workers share your execution account unless you supply isolation. Profiles/instructions do not isolate credentials.
- Repository tests run repository-authored code. Use existing safe dev containers/CI and retain normal harness permissions.
- No automatic worker replacement, cross-machine ownership transfer, hard cost enforcement, or exactly-once PR publication.
- Cleanup establishes that an agent and its children exited only through Herdr observation and a process-cwd scan; a writer that runs from outside the checkout is not detected, and cleanup never kills anything.
- Shell-capable harnesses share the task contract. Native instruction, MCP, quota, and resume capabilities still vary. The source does not claim all harnesses have been live-certified.
- Protocol/version mismatches fail visibly. No tmux fallback, terminal-banner classifier, or silent downgrade is installed.
- macOS and Linux are the targets. Windows is not supported by the file-locking helper in this MVP.

## Develop sum without disturbing it

The installation checkout serves the live coordinator, so sum is changed from an isolated development checkout. From the installation directory:

```sh
./bin/sumctl dev prepare --name my-topic        # add --pane to open an ordinary Herdr pane there
cd .sum/dev/my-topic && ./bin/sumctl init       # reports developer
```

`dev prepare` is plain `git worktree add` into `.sum/dev/<name>` on branch `sum-dev/<name>`, plus a `.sum/dev.json` marker in the new checkout. Rerunning it reopens the checkout with uncommitted work intact. The checkout keeps its own `.sum`, `.local`, `.deps`, and generated configs; setup there never designates it, so no coordinator can be claimed in it. Its `bin/sumctl` refuses every write aimed at the installation's state, including through an inherited `SUM_HOME`, so lab tests cannot touch production records; a dispatched task whose target is sum gets the same isolation and keeps using the installed helper for its callbacks. `dev list` shows checkouts, and `dev remove --name my-topic` uses `git worktree remove` and `git branch -d` only, so dirty trees and unmerged branches are preserved. Ship through the normal task, verification, and PR procedure; nothing is installed until a human merges. Details and a bootstrap recipe for the preceding release are in `skills/sum-develop/SKILL.md`.

## Runtime releases

The checkout where setup ran is the installation: it owns `.sum/`, the generated MCP settings, and the absolute `bin/sumctl` path in every worker brief. Code and dependencies can be staged separately as an immutable, commit-addressed release without touching anything a running coordinator, worker, or MCP server uses:

```sh
./bin/sumctl release stage            # builds .local/releases/<sha> for HEAD; nothing is activated
./bin/sumctl release list
```

A release holds the committed sum tree, its own pinned tool links, its own Go Mesh binary, and a `release.json` manifest with source SHA, content hashes, dependency pins, and contract versions. It is validated before it appears, kept read-only, and never contains state. Re-running `mise run setup` never rewrites an existing native binary or retargets a tool link either. Switching a live installation onto a staged release is a separate, explicit step that this version does not perform. See [dependencies](docs/DEPENDENCIES.md).

## Development and publication

See [dependencies](docs/DEPENDENCIES.md) for pins. No secrets or runtime state belong in commits. `mise.lock`, if generated on a networked machine, should be committed with dependency changes; none is fabricated here.

After extracting the source archive, create and push the repository with:

```sh
mise run publish              # creates douglasjarquin/sum PRIVATE and pushes
# mise run publish -- --public   # explicitly choose public instead
```

Authenticate `gh` as `douglasjarquin` first and ensure your Git author identity is configured. The task verifies the authenticated GitHub user, runs the offline tests, refuses an existing origin, and calls `gh repo create`. It does not overwrite an existing repository. This is a local publishing task—not evidence that the remote repository has already been created.

The optional Git bundle preserves the bootstrap commit. To use it instead: `git clone -b main /path/to/sum-mvp.bundle sum`, enter the new clone, and remove its local bundle origin with `git remote remove origin` before running the publish task.

## License and inspiration

MIT. sum stands on projects it did not write — Firstmate, Herdr, Oh My Pi, Solo, Unpeel, and others. See [ATTRIBUTIONS.md](ATTRIBUTIONS.md) for the complete credit and what each one shaped.
