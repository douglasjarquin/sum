---
name: sum-dispatch
description: Delegate explicitly approved work into an isolated Herdr task checkout with a durable worker brief.
---
# Dispatch

Use this only from the pane registered as coordinator by `sumctl init`; the helper refuses dispatch from any other pane. Workers do not dispatch other workers.
The coordinator does not do the requested work.
Research, planning, investigation, and implementation are dispatched.
Write the brief from the boss's words; do not investigate first.
Stay available for inbox notices and further dispatches.

## Capacity

`./bin/sumctl settings show` gives the configured limits, their source, and who holds them; absent capacity is unlimited.
Worker attempts and independent root verification runs hold separate reservations under the same limits.
Read the current IDs with `execution show TASK_ID`.
Use `execution park TASK_ID --attempt ID` to inspect stopped execution and release its reservation without losing unfinished questions, reports, or work.
A report, an idle or missing pane, and unresolved owned services release nothing.
To resume approved work, use `execution resume TASK_ID --attempt ID` with the released worker attempt ID; it checks capacity before launching a successor.
Every resume consumes a task repair iteration, including an infrastructure relaunch.
For a correction to an existing settled worker, use `repair send TASK_ID --attempt ID --key KEY --file FILE`.
Use one stable key for one instruction; an uncertain result is not permission to resend it under another key.
The default allowance is two controlled iterations, shared across attempts and candidates.
Exhaustion saves one question for the boss and starts nothing.
Only an explicit human decision permits a coordinator-recorded `repair extend` grant tied to that question.
A refused dispatch names the held slots.
Do not archive to make room: archive refuses held reservations.
Do not set capacity yourself: `settings set --global N` is the boss's decision, and per-repository isolation is enforced only when a capacity block is configured.
A free slot is never a reason to dispatch; work starts only from an explicit approved instruction.

## Intake

Identify the repository, outcome, scope/non-goals, acceptance criteria, and verification commands from the boss's words. A direct user request can be the approval; an unapproved issue cannot.
Do not research, plan, or implement in this pane to fill those fields.
Use `templates/task.md` as a checklist, not an excuse to make the user rewrite a clear request.
For a missing local clone, enroll the exact user-named repository: `./bin/sumctl project enroll owner/repo` clones exactly it under the Git-ignored `<installation>/projects/<owner>/<repo>` (a non-default host gets its own level: `--host gitlab.example.com` gives `projects/gitlab.example.com/owner/repo`), records host/owner/repo, the verified remote, and the path in `.sum/projects.json`, and is idempotent; never guess a similarly named project, and never clone everything an account can reach.
A clone the earlier procedure made under `.sum/projects/<owner>/<repo>` or a checkout the boss names with `--path` is adopted where it is (`kind: legacy` or `external`) after its origin is verified; nothing is moved, re-cloned, or overwritten, and a dirty tree or different remote at the target is a refusal, not a replacement. Enrolling sum itself registers the installation (`kind: installation`), never a nested copy.
`project list` and `project show NAME` observe the registrations; `project migrate NAME` prints inspect-only guidance and refuses `--apply` while any task, linked worktree, or process still references the clone.
The managed clone is a reference checkout, not a shared writer: every task still gets its own Herdr worktree, and the per-repository slot applies to the clone as to any repository.
If the main checkout has uncommitted changes, explain that dispatch starts from a committed base and does not copy those edits.

## Select execution

Translate the boss's natural-language choice into an explicit launch specification before the worktree exists; the helper resolves, validates, and persists it at `prepare`, and `start` uses exactly that record even if the defaults change in between.
Precedence is fixed and the helper enforces it: an explicit instruction in the task, then the saved worker default from `settings show` (`worker` block of `.sum/settings.json`), then the coordinator's own harness as Herdr observes it, then the harness's native model, disclosed as such.

| The boss says | Pass |
|---|---|
| nothing about harness or model | no flags: saved default, else your own harness with its native model |
| "use codex" / "run this on claude" | `--harness codex` (a harness-only override never carries a saved model from another harness) |
| "use gpt-5-codex" / "fable at low effort" | `--model gpt-5-codex` / `--model fable --reasoning low`, on the saved or explicit harness |
| "same as you" / "use what you're running" | `--same-as-you` (your harness, native model; overrides a saved default; never claims your exact model) |
| "make codex with gpt-5 my default" | `./bin/sumctl settings set --worker-harness codex --worker-model gpt-5` first; future dispatches only |
| "use my deep preset" / "use review for this" | `--preset deep` (see `./bin/sumctl preset list`); an unknown name is refused before anything is created |
| "use deep but with o4-mini" | `--preset deep --model o4-mini`; explicit fields refine a compatible preset, a different `--harness` is refused |
| "save this as my deep preset" | `./bin/sumctl preset set deep --harness codex --model gpt-5 [--reasoning L] [--arg=...]` first; `settings set --worker-preset deep` only if they also say "make it my default" |
| native flags the boss spelled out | separate `--arg` values, e.g. `--arg=-m --arg=MODEL`; never a shell string |

`.sum/preferences.md` can describe a preference; it never silently becomes a launch value. Only the boss's explicit "make this my default" is written with `settings set --worker-*`.
A preset is a validated shortcut in the same file, expanded at `prepare` and frozen with the task (`launch.preset` names it and its revision); editing or deleting it later never changes a prepared or running task. Presets are not agents, roles, or defaults, and none ship built in: create only what the boss names, with the harness they authorized.
Model and reasoning values are passed only through flags verified from the installed CLI's help (codex, claude, grok, copilot, cursor, pi, omp). A refusal names the harness without a verified flag; then pass the native argument with `--arg` or choose another harness. Do not guess a flag or invent a provider/model equivalence.
Ask once only for genuine authority/account ambiguity or a requested combination the helper refuses as unusable; a saved default or same-as-root is never a reason to ask.
Honor the user's chosen harness and authorized account. Coordinator and worker may differ; a worker default never switches your own harness, model, account, or billing route.
For Codex, run `sumctl quota --provider codex` (Remainder).
For any other provider, run `quota-axi --provider <provider>` or `sumctl quota --provider NAME` if the facade forwards that name.
This is an advisory read, not a guaranteed budget or a scheduler.
Known exhaustion: do not start on that route. Unknown/stale evidence: disclose it; do not silently switch to paid API use, a work account, or a new provider.
Do not run repeated quota checks while waiting.

## Submit

Write the approved brief to a temporary file. Then run:

```sh
./bin/sumctl dispatch --repo /absolute/path/to/repo \
  --brief /absolute/path/to/brief.md --harness codex --approved
```

`--project owner/repo` (an enrolled name) replaces `--repo` for a managed clone; the task records the project identity and its brief names it. A `--repo` path that matches a registration is attached the same way; an unregistered path stays usable.
`--harness codex` is the explicit form; omit it to use the saved worker default or your own harness. Add `--preset NAME` to expand a saved preset, and `--model`/`--reasoning` for this task only. Extra approved native arguments stay separate `--arg` values, e.g. `--arg=-m --arg=MODEL`; an `--arg` that sets the same flag as `--model` is refused as a conflict before anything is created.
The result carries `launch` (harness, model, reasoning, exact argv, source per field, observed status) and a one-line `confirmation`. Repeat that line to the boss: which harness and model, from which source, and that a CLI-requested model is requested rather than runtime-verified.
The brief does not describe the repository's environment; the worker discovers it into the task record (`env discover`) and readers take it from `context --section environment`, so no prompt repeats ports or start commands.
No permission-bypass flags are added by sum. The helper calls native `herdr worktree create`, verifies the returned checkout, launches with `agent start`, submits the worker brief through `agent prompt`, and registers the new pane as that task's worker so it can never claim coordination.
Use `prepare` instead of `dispatch` to create the worktree/record without starting an agent; later `start TASK_ID` starts it once.

Dispatch records the checkout's verification contract with the task (`verification_policy`: `standardized` or `not-yet-standardized`, the `VERIFY.md` hash at the base commit, the feature-map index, and the policy file set), and the brief's `## Verification contract` section tells the worker to run the project's verify runner and attach its run to the handoff, that the coordinator runs the contract again under its own run id, and that policy files are reviewed explicitly. Nothing is executed at dispatch.
Dispatch also initializes the checkout's code graph after the identity check and before the brief is written: the pinned codegraph of the runtime (`.local/bin/codegraph`, never a global one) runs `init` in that worktree's root in CLI mode, bounded by a timeout and two build slots per installation, and the result lands in `.sum/tasks/TASK_ID/graph.json` with a summary as `graph` in the task record and `status`. The brief's `## Code graph` section prints the state, the exact CLI commands, and the source fallback; a `failed`, `deferred`, `exhausted`, or `unavailable` graph blocks nothing and launches nothing. Retry a failed one with `./bin/sumctl graph init TASK_ID` (three failures exhaust it; the fallback is then the record), observe freshness with `graph status TASK_ID`, and print an optional MCP snippet for a harness with `graph config --harness NAME` for the boss to merge by hand; sum writes no harness configuration and never runs `codegraph install`.
The brief written at dispatch is revision `r1`; the task's `versions.json` records the sum version and brief schema it started under. After answers are applied or sum's worker procedure changes, `./bin/sumctl brief regenerate TASK_ID` stages `briefs/rN.md` from the record with a machine-generated change summary; the worker's current brief is never overwritten. Ask the worker to refresh only explicitly: `./bin/sumctl brief request TASK_ID rN`, then tell it in your own words to read that revision and continue. Requesting is bookkeeping separate from the notice slot; it sends nothing by itself.

Use returned IDs and paths. Do not infer IDs from labels. Do not copy the coordinator's instructions into the target repository.
Skills reach the worker explicitly: its brief carries a controlled copy of the worker procedure and absolute installed references (`<installation>/bin/sumctl`, `<runtime>/skills/sum-worker/SKILL.md` with size and hash) in a `## Delivered runtime` section, and `context --role worker` names the same paths. Directory nesting under `projects/` delivers nothing to a harness; a Herdr worktree lives elsewhere and some harnesses stop instruction discovery at a Git root.
A trust/auth prompt can make startup uncertain while leaving a real agent alive. Inspect the saved pane. Never blindly call dispatch/start again.

## Return control

Tell the boss what started and what it will deliver. End the turn rather than entering a repeated handoff/wait loop.
The normal worker return path is `sumctl ask` or `sumctl report` from its brief. Those commands save data first and attempt a short notice only to a provably idle/done recipient.
If a notice remains pending, saved work still exists, but automatic delivery is NOT guaranteed. Every open question and report stays listed under `returns` until a record closes it, and a later write to the same recipient coalesces them into one notice. Use a rundown on the next interaction.
With `sumctl hook enable` active, the recipient's next native `idle`/`done` edge runs the same bounded pump, so a pending notice usually arrives when the pane settles; that is still submitted, not read.
