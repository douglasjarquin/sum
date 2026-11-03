# Architecture

Sum is an agent distro using HC for worker and reviewer terminals and Git for isolated worktrees.
The user's ordinary agent conversation coordinates approved work.
Instructions and skills do the reasoning; a local ledger preserves approved tasks and evidence.
There is no persistent supervisor, coordinator terminal or event subscriber.

## Session roles

Run `./bin/sumctl init` and read each exact validated procedure path it returns.
Ordinary workspace entry creates a logical session and returns a claim token without querying or starting HC.
Retain that token in the conversation and pass `--claim TOKEN` to every coordinator mutation.
A matching claim resumes idempotently; a missing or stale token conflicts with the recorded owner.
Only explicit `init --transfer` advances its generation.
Transfer changes write authority, preserves the session's task ownership and never relaunches workers.
Ask before takeover if another conversation may still be coordinating.

An explicit worker brief, task checkout or inherited task identity must validate as its exact worker or reviewer, otherwise initialization fails.
It never becomes a coordinator by falling through an error.
A development marker selects developer; developers modify Sum only in isolated development checkouts and temporary lab state.
Inherited environment cannot redirect ordinary entry into another installation or grant a role.
Role bookkeeping is not an OS sandbox.

## Components

| Component | Responsibility |
| --- | --- |
| `AGENTS.md`, `COORDINATOR.md`, canonical skills | Ordinary entry and action-specific procedures |
| Native Go helper and file ledger | Claims, approved tasks, answers, reports, candidate-bound review and guarded publication |
| Workspace runtime route and settings | Saved HC socket/core/session/template configuration and explicit worker defaults |
| Typed HC client and matched manual/templates | Exact native worker identity, bounded transport and conservative mutation effects |
| Git worktrees | Isolated task checkouts with dirty/unmerged-work protection |
| `VERIFY.md` and portable verification skills | Verification usable without Sum or HC |

Fresh setup generates no custom Sum MCP configuration and preserves unrelated integrations.
See [dependencies](DEPENDENCIES.md) and [configuration](configuration.md).

## State and authority

The stable state root and selected physical generation home are distinct.
`coordinator.json` records one logical session and claim generation.
Every coordinator mutation revalidates its captured token under the state lock.
New tasks record logical `owner` and `parent: null`.
Native retirement records historical ownership without rewriting existing task parents.
The root state pointer uses format 3 to reject older helpers before they mutate the ledger or submit native input; generation headers and task schemas stay unchanged.
This fence is not a universal process or Git I/O exclusion mechanism.
Normal Open, init and status never migrate schema-one records.
Preserve those installations and use an independent fresh workspace.
See [update](update.md).

Worker/reviewer identity includes permanent core ID, returned address, creation time, task/role/attempt/generation, process and matching initialization receipt.
Names, labels, cwd, sockets, PIDs and native status alone confer no authority.
The local ledger remains authoritative; scrollback is not task storage.
File writes are locked and atomic; HC I/O runs outside the state lock and the claim is rechecked before effects.
Records-only restore preserves task/procedure bytes in a fresh write-blocked quarantine without transferring live authority.

## Collection and maintenance

```sh
./bin/sumctl collect --claim TOKEN --budget 20s
./bin/sumctl inbox --compact
./bin/sumctl context TASK_ID --role coordinator
./bin/sumctl answer TASK_ID QUESTION_ID --claim TOKEN --text 'The approved decision'
```

Questions, unapplied answers, reports, reviews and requested worker revisions remain obligations until their canonical actions settle them.
Collection lists actionable items, delivers queued worker answers and waits on recorded native addresses within one shared bound.
It neither ensures HC nor records read receipts.
Worker input submission is not application acknowledgement; uncertain input is never automatically replayed.
There are no prompts to the coordinator and no unattended delivery guarantee.
Read-only status never delivers, calls GitHub or cleans up.
`pr reconcile`, `pipeline`, `cleanup` and `sweep` are explicit maintenance under the claim.
Idle or Done status is not completion, process exit or released capacity.
Only the user merges, except the separately authorized factory contract's high-confidence merge gate.

## Dispatch

```sh
./bin/sumctl dispatch --claim TOKEN --repo /absolute/path/to/repo \
  --brief /absolute/path/to/approved-brief.md --harness codex --approved
```

The user grants scope; `--approved` records the caller's assertion.
`prepare` records intent and creates an isolated checkout without launching; `start TASK_ID --claim TOKEN` starts it once.
Launch uses the saved worker selection and workspace runtime route with no HC parent or caller.
HC is ensured lazily at launch; observation never starts a core.
Creation and opening delivery have distinct durable outcomes.
An uncertain launch is inspected and reconciled from its saved route, never blindly repeated.
See [recovery](recovery.md).
