---
name: sum-rundown
description: Reconcile saved tasks with bounded Herdr observations, surface unresolved questions, and recover without duplicate workers.
---
# Rundown

Run `./bin/sumctl inbox --live`. This takes one bounded `agent list` snapshot per Herdr session and reads every active task's state from it: twelve workers cost one observation call, not twelve sequential waits, and a worker missing from the snapshot is an attention item without its own lookup. The `fanout` field shows the Herdr calls and local elapsed time of that pass, and `capacity` shows the held slots. It is not a background monitor.
A rundown does not authorize doing requested work in this pane; dispatch that work.
For one task, `./bin/sumctl context TASK_ID --role coordinator` gives the outline, open questions with their text, the latest handoff (a worker claim), open returns, and the brief/update state in one bounded read; `--since CURSOR` (from the previous read's `cursor`) says whether anything changed and names only the new records. Reach for full `show` when you need the complete record.
Surface unanswered decisions first, then reports ready for review, then failures/uncertainty, then tasks whose `cleanup` field is `pending` or `blocked`. Keep unchanged status silent unless the boss asked for it.
A `cleanup: pending` row means the exact PR was observed merged and cleanup remains unfinished; offer `sumctl cleanup TASK_ID` (see `skills/sum-delivery/SKILL.md`).
Read `execution show TASK_ID` for its reservations; unfinished cleanup does not mean every attempt still holds a slot.
A `blocked` row names the blocker codes; relay them, do not clear them by force.
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

## Native events and attention

`inbox --live` and `init` show a `hook` field: whether native event delivery is enabled, its last handled event, and the pending count/age. `./bin/sumctl hook status` adds Herdr's own registry row and the bounded error log; `degraded` means the plugin is off, unlinked, or failing and this rundown is the delivery path. Nothing stopped because of that.
While the hook is enabled, a worker that Herdr saw `blocked`, idle with nothing owed and no report, exited, or closed has an `attention` record (`attention_records` in each row, `attention:ID` under `returns`) with a bounded output excerpt and a `herdr agent read` pointer. Read the pane before deciding anything: the record proves a native status, not a question, a finished task, a quota cause, or permission to answer an approval prompt.
If the excerpt holds a real question, save it with `sumctl ask` from the worker's brief commands; that record supersedes the attention. If the worker merely resumed, the record closes itself. After inspecting a record that needs no action, run `./bin/sumctl attention TASK_ID ATTENTION_ID --seen`; the record stays in the task.
A rundown reconciles missed events once from one snapshot per session; do not wait for the plugin to notice something it already missed.

## Native metadata

`inbox --live` and `init` also show a `metadata` field: whether sum's task state is projected into Herdr `sum_*` tokens, the last pass, and any `degraded` reason. When it is enabled, the boss can read `needs-decision`, `review-ready`, `merged-cleanup-pending`, `instruction-refresh-pending`, or an `attention-*` state in their own sidebar rows without asking you; that token is derived from the same records this rundown reads and changes task semantics in no way.
`./bin/sumctl metadata status` lists the tokens sum currently owns per task and the bounded error log; `metadata sync` runs one bounded pass (only changed endpoints are written); `metadata snippet` prints the optional `config.toml` rows for the boss to merge themselves. Do not edit their configuration, and do not use `pane report-agent`, `pane rename`, or metadata titles to make a task look finished: Herdr's lifecycle and labels are not yours.
`metadata inbox` opens the read-only `sumctl inbox` listing as a Herdr pane through the linked plugin (`hook enable` first). A `degraded` metadata row means reduced visibility only; nothing about ask, report, dispatch, update, or this rundown changed.

## Code graph

Each task row and outline carries `graph`: the state of the checkout's codegraph index as sum last recorded it (`ready`, `failed`, `deferred`, `exhausted`, `unavailable`). `./bin/sumctl graph status TASK_ID` adds one live freshness observation without writing anything; `graph init TASK_ID` retries or reconciles within the bound. None of those states blocks a task, changes a slot, or says anything about verification; a worker without a usable graph reads source, as its brief says.

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
