---
name: sum-dispatch
description: Delegate explicitly approved work into an isolated Herdr task checkout with a durable worker brief.
---
# Dispatch

Use this only from the pane registered as coordinator by `sumctl init`; the helper refuses dispatch from any other pane. Workers do not dispatch other workers.

## Capacity

`./bin/sumctl settings show` gives the limits (default two slots globally, one per repository), their source, and who holds them.
Every non-archived task holds a slot; a report or an idle pane frees nothing, only `archive --acknowledge` after you inspected and preserved the work.
A refused dispatch names the held slots. Do not archive to make room unless the work was actually inspected, and do not raise capacity yourself: `settings set --global N` is the boss's decision, and per-repository isolation stays at one writer per checkout unless the boss also raises `--per-repository`.
A free slot is never a reason to dispatch; work starts only from an explicit approved instruction.

## Intake

Identify the repository, outcome, scope/non-goals, acceptance criteria, and verification commands. A direct user request can be the approval; an unapproved issue cannot.
Use `templates/task.md` as a checklist, not an excuse to make the user rewrite a clear request.
For a missing local clone, clone the exact user-named repository under `.sum/projects/<owner>/<repo>` with `gh repo clone`; never guess a similarly named project.
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
Run `quota-axi --provider <provider>` for the selected provider when available. This is an advisory read, not a guaranteed budget or a scheduler.
Known exhaustion: do not start on that route. Unknown/stale evidence: disclose it; do not silently switch to paid API use, a work account, or a new provider.
Do not run repeated quota checks while waiting.

## Submit

Write the approved brief to a temporary file. Then run:

```sh
./bin/sumctl dispatch --repo /absolute/path/to/repo \
  --brief /absolute/path/to/brief.md --harness codex --approved
```

`--harness codex` is the explicit form; omit it to use the saved worker default or your own harness. Add `--preset NAME` to expand a saved preset, and `--model`/`--reasoning` for this task only. Extra approved native arguments stay separate `--arg` values, e.g. `--arg=-m --arg=MODEL`; an `--arg` that sets the same flag as `--model` is refused as a conflict before anything is created.
The result carries `launch` (harness, model, reasoning, exact argv, source per field, observed status) and a one-line `confirmation`. Repeat that line to the boss: which harness and model, from which source, and that a CLI-requested model is requested rather than runtime-verified.
No permission-bypass flags are added by sum. The helper calls native `herdr worktree create`, verifies the returned checkout, launches with `agent start`, submits the worker brief through `agent prompt`, and registers the new pane as that task's worker so it can never claim coordination.
Use `prepare` instead of `dispatch` to create the worktree/record without starting an agent; later `start TASK_ID` starts it once.

The brief written at dispatch is revision `r1`; the task's `versions.json` records the sum version and brief schema it started under. After answers are applied or sum's worker procedure changes, `./bin/sumctl brief regenerate TASK_ID` stages `briefs/rN.md` from the record with a machine-generated change summary; the worker's current brief is never overwritten. Ask the worker to refresh only explicitly: `./bin/sumctl brief request TASK_ID rN`, then tell it in your own words to read that revision and continue. Requesting is bookkeeping separate from the notice slot; it sends nothing by itself.

Use returned IDs and paths. Do not infer IDs from labels. Do not copy the coordinator's instructions into the target repository.
A trust/auth prompt can make startup uncertain while leaving a real agent alive. Inspect the saved pane. Never blindly call dispatch/start again.

## Return control

Tell the boss what started and what it will deliver. End the turn rather than entering a repeated handoff/wait loop.
The normal worker return path is `sumctl ask` or `sumctl report` from its brief. Those commands save data first and attempt a short notice only to a provably idle/done recipient.
If a notice remains pending, saved work still exists, but automatic delivery is NOT guaranteed. Use a rundown on the next interaction.
