---
name: sum-rundown
description: Reconcile saved tasks with bounded Herdr observations, surface unresolved questions, and recover without duplicate workers.
---
# Rundown

Run `./bin/sumctl inbox --live`. This takes one bounded `agent list` snapshot per Herdr session and reads every active task's state from it: twelve workers cost one observation call, not twelve sequential waits, and a worker missing from the snapshot is an attention item without its own lookup. The `fanout` field shows the Herdr calls and local elapsed time of that pass, and `capacity` shows the held slots. It is not a background monitor.
Surface unanswered decisions first, then reports ready for review, then failures/uncertainty, then tasks whose `cleanup` field is `pending` or `blocked`. Keep unchanged status silent unless the boss asked for it.
A `cleanup: pending` row means the exact PR was observed merged and the task still holds its workspace and slot; offer `sumctl cleanup TASK_ID` (see `skills/delivery/SKILL.md`). A `blocked` row names the blocker codes; relay them, do not clear them by force.
A cleanup interrupted after the native removal shows as `removing`; `inbox --live` reconciles it once from records and observation and reports `cleanup_reconciled`. Nothing polls for merges.

For a worker that is idle/done/blocked without a report, read bounded relevant output with MCP `herdr_agent_read` or `bin/herdr-scoped agent read PANE --source visible --lines 120`.
Do not assume idle means done. Inspect ambiguous prose; ask for a file report when screen output is incomplete. If a question was never saved, capture it using `sumctl ask` before relaying it to the boss.
This is the MVP fallback for non-compliant agents. It runs during a rundown, not continuously; do not promise instant unattended detection.

Record the boss's actual answer with:

```sh
./bin/sumctl answer TASK_ID QUESTION_ID --text 'The authorized answer'
```

An answer stays visible until the worker marks it applied. `sumctl brief list TASK_ID` shows whether a report was produced under an older brief revision whose verification policy has since changed; treat that as evidence needing refresh review, not as a failure or an approval.

## Pending returns

Each row's `returns` lists what is still owed and to whom: open questions and unverified reports to you, unapplied answers and requested brief revisions to the worker. The `obligation` is open until a later record closes it; the `notification` beside it is only what is known about telling the current recipient: `pending`, `submitted` (prompt accepted or presented in your own output), `uncertain` (a timeout after a possible submission or an interrupted pass), `not-delivered` (busy, absent, wrong checkout, not registered) or `stalled` (three known failures).
`inbox --live` already ran one bounded pass: at most one coalesced notice per recipient, with record IDs and commands only. Items routed to you appear inline in that output; reading them answers, applies, and verifies nothing.
For an `uncertain` or `stalled` item, first look at the recipient pane, then try once with `sumctl notice TASK_ID --to worker` (or `--to parent`). `sumctl pump` repeats the ordinary pass; `--force` includes uncertain and stalled items. Do not loop.

## Restart

In the new pane run `./bin/sumctl init`. If another pane still owns coordination you become a developer; inspect that pane before anything else.
Only when the boss confirms the old coordinator pane is gone, run `./bin/sumctl init --role coordinator --reclaim`. It proceeds only when Herdr reports the old pane as `pane_not_found`; an existing pane (even with its agent exited) or an unobservable one is refused, and it never rebinds tasks by itself.
Then inspect saved tasks and actual Herdr inventory.
To make an existing task report to this coordinator, explicitly run `sumctl bind TASK_ID --parent-only`. Its output carries one catch-up listing of everything still owed to the parent; the returns that failed against the old pane are not retried against it.
To adopt a known existing worker after a pane ID change, use `sumctl bind TASK_ID --worker-pane PANE` after verifying its cwd and task identity.
Never launch a replacement just because a pane cannot be observed. Missing/uncertain workers need inspection; sum has no automatic retry or process-fencing service.
Do not alter Herdr's global auto-resume policy. Herdr is the sole process/restore owner in this MVP.

## Backup

`sumctl backup /path/outside/state/sum-records.tar.gz` saves versioned task records, every brief revision and version sidecar, reports, and decisions with a manifest.
It is explicitly **records-only**. Worktree code, unpushed commits, dirty files, credentials, and live processes are not captured.
Back up code separately through the repo/host's existing process. A backup of this directory is not a full machine-crash recovery guarantee.
Restore into a new directory, retain the original, and inspect the manifest. Schema mismatches fail rather than attempting an in-place migration.
Do not reuse another machine's pane IDs. The helper refuses cross-machine interaction until explicitly rebound to recovered work.
