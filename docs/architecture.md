# Architecture

Sum is an agent distro using HC for terminal processes and Git for isolated worktrees.
Instructions and skills do the reasoning; a local ledger preserves approved work and its evidence.
The scoped event observer is a long-lived non-agent helper, not a scheduler, supervisor or manager hierarchy.

## Session roles

Run `./bin/sumctl init` and follow the exact procedure path it returns.
Without a qualified HC launch context, `init` defaults to developer and does not implicitly claim coordination.
The preferred coordinator launch is:

```sh
./bin/sumctl coordinator start --socket /absolute/path/to/hc.sock \
  --session sum --hc-config-home /absolute/path/to/hc-config --harness codex
```

Start the selected HC core explicitly using its release-matched native manual before this command.
Observation never auto-starts it.
`coordinator start` seeds the bound environment and matching initialization receipt; `--model`, `--reasoning`, repeatable `--arg`, and `--preset` retain their explicit launch meanings.
A manually started qualified existing terminal needs explicit coordinator initialization and records its known launch or an unknown model; `--same-as-you` cannot invent an exact model.
Exactly one verified coordinator binding owns the installation.
A dispatched session remains its task's worker; other sessions are developers.
A developer modifies Sum only in an isolated development checkout and uses temporary lab state.
`doctor` observes without binding.
Role bookkeeping is not an OS sandbox.

## What ships

| Component | Responsibility |
| --- | --- |
| `AGENTS.md`, `COORDINATOR.md`, canonical skills | Role bootstrap and action-specific procedures |
| `bin/sumctl` and native Go helper | Durable tasks, answers, receipts, candidate-bound review/verification and guarded publication |
| Typed HC client | Exact published 0.1.14 protocol and version/core checks, bounded I/O, conservative mutation effects |
| Matched HC manual and namespaced template | Qualified local terminal execution; Codex 0.166.0 default keymap with Tab submission |
| Scoped observer | Native event subscription, snapshot catch-up and bounded ledger reconciliation |
| Git worktree operations | Task checkout intent, registration and removal with dirty/unmerged-work protection |
| `VERIFY.md` and portable verify/evidence skills | Repository verification usable without Sum or HC |
| Pinned development tools | Go, Python, Node, project tools, quota and optional codegraph/LSP integrations |

Fresh setup generates no custom Sum MCP configuration.
Unrelated MCP configuration remains owned by its existing integrations.
Only explicitly owned legacy Sum configuration may be retired after a successful controlled cutover.
See [Dependencies](DEPENDENCIES.md), [configuration](configuration.md) and [HC runtime](herdr-backend.md).

## State and communication

The stable state root and selected physical generation home are distinct.
Tasks retain approved briefs, sealed procedure/policy revisions, questions, answers, reports, attempts, receipts and append-only evidence.
Generation selection does not rewrite historical bytes or fabricate HC endpoints for old records.
Normal Open, `init` and `status` never migrate schema-one state.
An explicit migration requires an enforceable lease excluding all legacy writers and senders; current legacy APIs cannot prove that prerequisite.
Existing-installation cutover therefore refuses without proof.
No production migration or automatic hot adoption is claimed.
See [Update](update.md).
`restore ARCHIVE NEW_HOME` imports a records-only backup into a fresh write-blocked quarantine, preserving task/procedure bytes without transferring coordinator, session or native runtime authority.
Read the imported saved records through `--home NEW_HOME`; direct extraction is not a writable generation restore.

Each terminal binding includes permanent core ID, returned address, creation time, task/role/attempt/generation, process and matching receipt.
Names, labels, cwd, sockets, PIDs and native status alone cannot confer authority.
The local ledger remains authoritative; terminal scrollback and HC documents are not task storage.
File writes are locked and atomic; no HC I/O runs under the state lock.

Questions are saved before notification.
Open questions, unapplied answers, unverified reports, reviewer verdicts and requested revisions remain obligations until their canonical records close them.
Native send success means submitted, never acknowledged, applied or verified.
Uncertain mutations are preserved without automatic replay.
Routine wakes coalesce; new decision questions retain priority without interrupting busy recipients.
`wake show` provides an exact boundary token and `wake consume --boundary TOKEN` records only the covered presentation, closing no task obligation.

## Bounded reads and explicit maintenance

```sh
./bin/sumctl inbox --live
./bin/sumctl context TASK_ID --role coordinator
./bin/sumctl context TASK_ID --section decisions --after 20 --limit 20
./bin/sumctl context TASK_ID --since CURSOR
./bin/sumctl answer TASK_ID QUESTION_ID --text 'The approved decision'
./bin/sumctl help TOPIC
```

`context` is bounded; full `show` and transcripts are for explicit inspection.
Read-only status commands neither deliver nor call GitHub.
`init`, `bind`, `pump`, task-write notices and the observer run bounded fast delivery without PR observation, publication or cleanup.
`pr reconcile`, `pipeline`, `cleanup` and `sweep` are explicit maintenance.
An idle or Done terminal is not task completion, actual process exit or released capacity.
Only the user merges, except the separately authorized factory contract's high-confidence merge gate.

## Manual dispatch

```sh
./bin/sumctl dispatch --repo /absolute/path/to/repo \
  --brief /absolute/path/to/approved-brief.md --harness codex --approved
```

`--approved` records the caller's assertion, not an independent authority grant.
`prepare` creates a verified Git checkout and durable intent without launching; `start TASK_ID` starts the saved task once.
`prepare --reconcile TASK_ID` reconciles interrupted checkout preparation from its saved approved intent.
Terminal creation and opening-message delivery have separate outcomes.
An uncertain launch cannot simply be started again.
See [Recovery](recovery.md) for receipt-based confirmation and proven-unsent redelivery.

## No installed harness

Setup can stage the runtime without starting an authenticated model.
The execution host must already have the user's selected harness and account available.
Use its configured environment in a named HC session; do not restart a busy core to refresh PATH.
No harness installation, account change, permission bypass or global configuration edit is implied by a task.
