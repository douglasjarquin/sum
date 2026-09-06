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

Honor the user's chosen harness and authorized account. Coordinator and worker may differ.
Otherwise inspect available harnesses, project preferences, and ask once when authority/account choice is genuinely ambiguous.
Run `quota-axi --provider <provider>` for the selected provider when available. This is an advisory read, not a guaranteed budget or a scheduler.
Known exhaustion: do not start on that route. Unknown/stale evidence: disclose it; do not silently switch to paid API use, a work account, or a new provider.
Do not run repeated quota checks while waiting.

## Submit

Write the approved brief to a temporary file. Then run:

```sh
./bin/sumctl dispatch --repo /absolute/path/to/repo \
  --brief /absolute/path/to/brief.md --harness codex --approved
```

Replace `codex` with the actual selected Herdr integration kind. Extra approved harness arguments are separate `--arg` values, e.g. `--arg=-m --arg=MODEL`.
No permission-bypass flags are added by sum. The helper calls native `herdr worktree create`, verifies the returned checkout, launches with `agent start`, submits the worker brief through `agent prompt`, and registers the new pane as that task's worker so it can never claim coordination.
Use `prepare` instead of `dispatch` to create the worktree/record without starting an agent; later `start TASK_ID` starts it once.

The brief written at dispatch is revision `r1`; the task's `versions.json` records the sum version and brief schema it started under. After answers are applied or sum's worker procedure changes, `./bin/sumctl brief regenerate TASK_ID` stages `briefs/rN.md` from the record with a machine-generated change summary; the worker's current brief is never overwritten. Ask the worker to refresh only explicitly: `./bin/sumctl brief request TASK_ID rN`, then tell it in your own words to read that revision and continue. Requesting is bookkeeping separate from the notice slot; it sends nothing by itself.

Use returned IDs and paths. Do not infer IDs from labels. Do not copy the coordinator's instructions into the target repository.
A trust/auth prompt can make startup uncertain while leaving a real agent alive. Inspect the saved pane. Never blindly call dispatch/start again.

## Return control

Tell the boss what started and what it will deliver. End the turn rather than entering a repeated handoff/wait loop.
The normal worker return path is `sumctl ask` or `sumctl report` from its brief. Those commands save data first and attempt a short notice only to a provably idle/done recipient.
If a notice remains pending, saved work still exists, but automatic delivery is NOT guaranteed. Use a rundown on the next interaction.
