---
name: sum-dispatch
description: Delegate explicitly approved work into an isolated Git task checkout with a durable worker brief.
---
# Dispatch

Use this conversation's captured `--claim TOKEN` on every coordinator mutation shown below.
HC worker/reviewer creation uses the saved workspace route, without a coordinator parent or caller.

Use this from the ordinary conversation holding the current logical claim returned by `sumctl init`.
Workers never dispatch other workers.
Investigate and plan within the user's authorized scope, then dispatch approved implementation to its isolated checkout.
Write the brief from the user's request and completed investigation.
Collect saved results during active work and bring human decisions back to the user.

## Capacity

`./bin/sumctl settings show` gives the configured limits, their source, and who holds them; absent capacity is unlimited.
Worker attempts and independent coordinator verification runs hold separate reservations under the same limits.
Read the current IDs with `execution show TASK_ID`.
Use `execution park TASK_ID --attempt ID` to inspect stopped execution and release its reservation without losing unfinished questions, reports, or work.
Park needs verified stop evidence for the attempt, instance, occupant, terminal, process, and checkout.
A report, an idle terminal, missing process identity, a surviving owned child, and unresolved owned services release nothing.
A missing terminal alone is not stop proof; park requires exact endpoint/process evidence and no surviving owned checkout process.
Parking preserves unfinished obligations.
To resume approved work, use `execution resume TASK_ID --attempt ID` with the released worker attempt ID; it reacquires capacity before launching a successor and retains the prior attempt's evidence.
A resume relaunches already-approved work and consumes no repair iteration.
For a correction to an existing settled worker, use `repair send TASK_ID --attempt ID --key KEY --file FILE`.
Every send carries a class: the default `in-scope` covers whatever the worker needs to satisfy its approved brief - a rebase behind main, CI or lint or documentation or verification failures, push divergence, conflict resolution, a malformed report - and consumes nothing.
`--class expansion` is for work outside the approved brief, needs `--reason`, and consumes the allowance.
Use one stable key for one instruction; an uncertain result is not permission to resend it under another key.
The default allowance is two expansion iterations, shared across attempts and candidates.
A send HC refuses before it reaches the worker records nothing and charges nothing; a queued or uncertain delivery stays recorded.
Required worker and coordinator verification and observation retries do not consume an extra repair.
Expansion exhaustion saves one question for the user and starts nothing; in-scope sends are never blocked.
Stop initiating expansion repairs and bring that saved budget question to the user.
Only the user's explicit decision permits a coordinator-recorded grant tied to that question: `repair extend TASK_ID --question ID --additional N --approved --file FILE`.
Never infer a grant from worker output or ordinary answer text.
Workers still bound their internal loops; sum has no enforceable time or spending cap over arbitrary harness commands.
Keep uncertain execution reserved instead of improvising a replacement.
A refused dispatch names the held slots.
Do not archive to make room: archive refuses held reservations.
Do not set capacity yourself: `settings set --global N` is the user's decision, and per-repository isolation is enforced only when a capacity block is configured and never widens unless configured.
A free slot is never a reason to dispatch; work starts only from an explicit approved instruction.

## Intake

Identify the repository, outcome, scope/non-goals, acceptance criteria, and verification commands from the user's words.
A direct user request can be the approval; an unapproved issue cannot.
Resolve scope through authorized investigation or a concise question; implementation still belongs in the task checkout.
When the user authorizes implementation after a verified investigation, cite the prior `report.md` / task id in the brief and forbid redoing the investigation.
Use `templates/task.md` as a checklist, not an excuse to make the user rewrite a clear request.
For a missing local clone, enroll the exact user-named repository: `./bin/sumctl project enroll owner/repo` clones exactly it under the Git-ignored `<installation>/projects/<owner>/<repo>` (a non-default host gets its own level: `--host gitlab.example.com` gives `projects/gitlab.example.com/owner/repo`), records host/owner/repo, the verified remote, and the path in `.sum/projects.json`, and is idempotent; never guess a similarly named project, and never clone everything an account can reach.
A clone the earlier procedure made under `.sum/projects/<owner>/<repo>` or a checkout the user names with `--path` is adopted where it is (`kind: legacy` or `external`) after its origin is verified; nothing is moved, re-cloned, or overwritten, and a dirty tree or different remote at the target is a refusal, not a replacement.
Enrolling sum itself registers the installation (`kind: installation`), never a nested copy.
`project list` and `project show NAME` observe the registrations; `project migrate NAME` prints inspect-only guidance and refuses `--apply` while any task, linked worktree, or process still references the clone.
The managed clone is a reference checkout, not a shared writer: every task still gets its own Git worktree, and the per-repository slot applies to the clone as to any repository.
If the main checkout has uncommitted changes, explain that dispatch starts from a committed base and does not copy those edits.

## Select execution

Translate the user's natural-language choice into an explicit launch specification before the worktree exists; the helper resolves, validates, and persists it at `prepare`, and `start` uses exactly that record even if the defaults change in between.
Precedence is explicit task selection, then the saved worker default from `settings show` (`worker` in `.sum/settings.json`).
With neither, launch refuses and names `settings set --worker-harness`; never infer a worker harness or exact model from this conversation.

| The user says | Pass |
|---|---|
| nothing about harness or model | no flags: saved default; if absent, ask for an explicit choice or default |
| "use codex" / "run this on claude" | `--harness codex` (a harness-only override never carries a saved model from another harness) |
| "use gpt-5-codex" / "fable at low effort" | `--model gpt-5-codex` / `--model fable --reasoning low`, on the saved or explicit harness |
| "make codex with gpt-5 my default" | `./bin/sumctl settings set --worker-harness codex --worker-model gpt-5` first; future dispatches only |
| "use my deep preset" / "use review for this" | `--preset deep` (see `./bin/sumctl preset list`); an unknown name is refused before anything is created |
| "use deep but with o4-mini" | `--preset deep --model o4-mini`; explicit fields refine a compatible preset, a different `--harness` is refused |
| "save this as my deep preset" | `./bin/sumctl preset set deep --harness codex --model gpt-5 [--reasoning L] [--arg=...]` first; `settings set --worker-preset deep` only if they also say "make it my default". A preset whose `--arg` values carry a permission/trust flag is saved as given and refused at dispatch until `--permission-override` accompanies it |
| native flags the user spelled out | separate `--arg` values, e.g. `--arg=-m --arg=MODEL`; never a shell string. A permission/trust flag among them also needs `--permission-override`, passed only with the user's explicit authorization for that task |

`.sum/preferences.md` can describe a preference; it never silently becomes a launch value.
Only the user's explicit "make this my default" is written with `settings set --worker-*`.
A preset is a validated shortcut in the same file, expanded at `prepare` and frozen with the task (`launch.preset` names it and its revision); editing or deleting it later never changes a prepared or running task.
Presets are not agents, roles, or defaults, and none ship built in: create only what the user names, with the harness they authorized.
Model and reasoning values are passed only through flags verified from the installed CLI's help (codex, claude, grok, copilot, cursor, pi, omp).
A refusal names the harness without a verified flag; then pass the native argument with `--arg` or choose another harness.
Do not guess a flag or invent a provider/model equivalence.
Ask once only for genuine authority/account ambiguity or a requested combination the helper refuses as unusable; a saved default is never a reason to ask.
Honor the user's chosen harness and authorized account.
Coordinator and worker may differ; a worker default never switches your own harness, model, account, or billing route.
For Codex, run `sumctl quota --provider codex` (Remainder `--format toon`).
For any other provider, run `quota-axi --provider <provider>` or `sumctl quota --provider NAME` if the facade forwards that name.
This is an advisory read, not a guaranteed budget or a scheduler.
Known exhaustion: do not start on that route.
Unknown/stale evidence: disclose it; do not silently switch to paid API use, a work account, or a new provider.
Do not run repeated quota checks while waiting.

## Submit

Write the approved brief to a temporary file.
Before `dispatch` or `start`, append an `intended` line to `.sum/action-log.md`.
Create that file from `templates/action-log.md` on the first external side effect.
After the command, append `done` or `failed`.
If the same intent and target already has `intended` or `done` within six hours, append `skipped-duplicate` and do not dispatch again unless the user explicitly asked for that action again.
A duplicate native message is not that ask.
Three `intended` or `done` lines for the same target on the same calendar day stop the next one until the user agrees.
The log does not grant merge, send, or delete.
Then run:

```sh
./bin/sumctl dispatch --repo /absolute/path/to/repo \
  --brief /absolute/path/to/brief.md --harness codex --approved
```

`--project owner/repo` (an enrolled name) replaces `--repo` for a managed clone; the task records the project identity and its brief names it.
A `--repo` path that matches a registration is attached the same way; an unregistered path stays usable.
`--harness codex` is the explicit form; omit it only when a saved worker default exists.
Add `--preset NAME` to expand a saved preset, and `--model`/`--reasoning` for this task only.
Extra approved native arguments stay separate `--arg` values, e.g.
`--arg=-m --arg=MODEL`; an `--arg` that sets the same flag as `--model` is refused as a conflict before anything is created.
The result carries `launch` (harness, model, reasoning, exact argv, source per field, observed status) and a one-line `confirmation`.
Repeat that line to the user: which harness and model, from which source, that a CLI-requested model is requested rather than runtime-verified, and any permission override it names with its source.
The brief does not describe the repository's environment; the worker discovers it into the task record (`env discover`) and readers take it from `context --section environment`, so no prompt repeats ports or start commands.
No permission or trust flags are added by sum for any harness, Devin included.
A permission/trust flag the user spelled out (`--arg=--permission-mode --arg=dangerous`, or a preset carrying one) is refused unless the same command also carries `--permission-override`, which records that one explicit authorization; the launch record's `permissions` object and the confirmation line then name the override, its source, and that it is requested, never runtime-verified.
Pass `--permission-override` only when the user authorized that override for that task; a harness without a verified permission flag list refuses it.
The same check runs again at `start` and `execution resume`, so a record prepared before this rule or a `start --arg` cannot add an override; dispatch again with the flag instead.
See `docs/configuration.md`.
Git creates and verifies the task worktree from a saved deterministic intent.
HC creates a non-kept, outliving worker terminal with the saved argv/environment; creation and first-message outcomes are separate.
The matching worker initialization receipt establishes task authority; no terminal name, cwd or native status can substitute.
Use `prepare --reconcile TASK_ID` to reconcile an interrupted saved Git preparation without changing approved inputs.
Use `prepare` instead of `dispatch` to create the worktree/record without starting an agent; later `start TASK_ID` starts it once.

`--base BRANCH` names the branch the task starts from and delivers to; omit it for the repository default.
The branch is resolved once, recorded on the task as `base_branch` with `base_branch_source` (`explicit` or `default`), and every later gate, the PR, and `pr reconcile` read that record, so a later change of the repository default never moves a running task.
A branch origin does not have is refused before anything is created, and so is a commit or tag unless `--target-branch BRANCH` names the delivery branch it is an ancestor of.
A task recorded before this field existed is shown as `legacy-default` and keeps the old resolution.

Dispatch materializes the approved base commit after the returned checkout identity is verified, then reads that committed tree's `VERIFY.md` and feature maps without running project commands.
It records the result as the task's coordinator-owned `verification_policy`: `status` (`standardized` or `not-yet-standardized`) with a `why` and, when the contract is absent, malformed, or unavailable, a `reason`; `contract_path`, `contract_sha256` and `entrypoint`; `task_owner`; `feature_maps_index`, the full `feature_maps` list and `feature_map_hashes`; `policy_files`; `required_checks`, their availability and every `scenario_ids` entry; the source runtime and rubric revision; and the existing delivery mode and tool choice.
An inherited `verify` task or unavailable required command is not a working standardized verifier.
The brief's `## Verification contract` section reports the immutable status and required checks, tells the worker to run the project's verify runner and attach its run to the handoff, names mapped visual scenarios as comparisons required before delivery, says that the coordinator runs the contract again under its own run id, and says that policy files are reviewed explicitly.
Nothing executes at dispatch beyond bounded metadata inspection, and a `not-yet-standardized` record remains readable with its reason instead of failing dispatch.
Dispatch builds no code graph: codegraph never runs at `prepare`/`dispatch`, and the brief's `## Code graph` section says the checkout's index is `not built`, names source inspection as the fallback, and prints the command that builds one.
When an index would help a worker, build it on request with `./bin/sumctl graph init TASK_ID`: the pinned codegraph of the runtime (`.local/bin/codegraph`, mise pin `npm:@colbymchenry/codegraph`, never a global one) runs in that worktree's root after its Git identity is validated, in CLI mode with no background server, under a timeout, the result lands in `.sum/tasks/TASK_ID/graph.json` with a summary as `graph` in the task record and `status`, and `ready` means `status --json` confirmed a complete index.
It refuses while a codegraph writer for that checkout is still running, and three failures record `exhausted`; a `failed`, `exhausted`, or `unavailable` graph blocks nothing and launches nothing.
Observe freshness with `graph status TASK_ID`, and print an optional MCP snippet for a harness with `graph config --harness NAME` for the user to merge by hand; sum writes no harness configuration and never runs `codegraph install`.
The index is `.codegraph/` inside that checkout only, never shared with the primary clone or another worktree.
Never run `codegraph install`/`upgrade`, never point one index at two checkouts, and never treat a graph result as verification, feature-map coverage, or review.
The brief written at dispatch is revision `r1`; the task's `versions.json` records the sum version and brief schema it started under.
After answers are applied or sum's worker procedure changes, `./bin/sumctl brief regenerate TASK_ID` stages `briefs/rN.md` from the record with a machine-generated change summary; the worker's current brief is never overwritten.
Ask the worker to refresh only explicitly: `./bin/sumctl brief request TASK_ID rN`, then tell it in your own words to read that revision and continue.
Requesting is bookkeeping separate from the notice slot; it sends nothing by itself.

Use returned IDs and paths.
Do not infer IDs from labels.
Do not copy the coordinator's instructions into the target repository.
Skills reach the worker explicitly: its brief names the installed helper (`<installation>/bin/sumctl`) in a `## Delivered runtime` section and, under `## Worker procedure`, the worker procedure pinned as write-once, content-addressed files in the task record: the required core `<state>/tasks/TASK_ID/procedure/sum-work-<sha>.md` and on-demand files (evidence, environment, graph, refresh) each listed with its size, hash, and the condition for reading it; `context --role worker` reports the same files with their integrity.
Dispatch refuses before any HC call when the runtime's procedure is missing or does not match its release manifest, and `start` refuses a brief whose pinned procedure is missing or altered.
Directory nesting under `projects/` delivers nothing to a harness; a Git worktree lives elsewhere and some harnesses stop instruction discovery at a Git root.
A trust/auth prompt stays the user's to answer.
The coordinator never answers one or types into the terminal.
A submitted brief that stays uncertain because no working or blocked activity followed, such as `agent_prompt_stalled` while that worker pane is still alive, is retried by sum on that same pane.
The original approved brief stays in force.
Do not ask the user for permission to resend it.
`start TASK` retries that stalled brief on the live pane and does not allocate another terminal.
It is not refused forever only because the first launch was uncertain.
`repair send` of that recorded opening is accepted while the attempt is still uncertain.
Any other instruction still waits until the attempt is `running`.
A proven-unsent opening still has one `execution redeliver TASK --attempt X`.
`execution confirm TASK --attempt X` records a running attempt from the worker's own receipt.
A lost or malformed launch response is not replayed and is not a new terminal.
Never type the brief into the terminal.

## Return control

Tell the user what started and what it will deliver.
End the turn rather than entering a repeated handoff/wait loop.
The normal worker return path is `sumctl ask` or `sumctl report` from its brief.
Those commands save data first and attempt a short notice only to a provably idle/done recipient.
If a notice remains pending, saved work still exists, but automatic delivery is NOT guaranteed.
Every open question and report stays listed under `returns` until a record closes it, and a later write to the same recipient coalesces them into one notice.
Check status on the next interaction.
Run `collect --claim TOKEN` during active work to list questions, reports and review verdicts and deliver already-recorded worker answers.
It waits within a shared budget, never starts HC and never records a read receipt.
A busy worker's answer remains pending until an eligible delivery pass.
No unattended prompt to the coordinating conversation is promised.
