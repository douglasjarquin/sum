---
name: sum-rundown
description: Reconcile saved tasks with bounded Herdr observations, surface unresolved questions, and recover without duplicate workers.
---
# Rundown

Run `./bin/sumctl inbox --live`. It reads saved records and adds one bounded `agent list` per Herdr session of the listed local, unarchived tasks: twelve workers cost one observation call, not twelve sequential waits. Each such row gets `observed` (the agent state, `absent` when the worker is missing from its session's list, without its own lookup, or `unobserved` with a reason); `fanout` shows the sessions, Herdr calls, and elapsed and budget milliseconds, and `capacity` shows the held slots. Without a Herdr context it returns `live: false` with `live_reason` and the plain records view.
It writes nothing: it delivers no returns, creates no attention records, reconciles no cleanup, and never calls GitHub. It is not a background monitor.
A rundown does not authorize doing requested work in this pane; dispatch that work.
For one task, `./bin/sumctl context TASK_ID --role coordinator` gives the outline, open questions with their text, the latest handoff (a worker claim), open returns, and the brief/update state in one bounded read; `--since CURSOR` (from the previous read's `cursor`) says whether anything changed and names only the new records. Reach for full `show` when you need the complete record.
Surface unanswered decisions first, then reports ready for review, then failures/uncertainty, then tasks whose `cleanup` field is `pending` or `blocked`. Keep unchanged status silent unless the user asked for it.
A `cleanup: pending` row means the exact PR was observed merged and cleanup remains unfinished; offer `sumctl cleanup TASK_ID` (see `skills/sum-delivery/SKILL.md`). It appears only after `sweep`, `pr reconcile`, or `cleanup` observed the merge; nothing else looks.
A rundown reads saved records and Herdr, never GitHub, so it does not refresh a task's CI row. The checks are observed at `sumctl pr reconcile` and on demand with `sumctl pipeline ci TASK_ID`; a CI row in the table states what the checks said at that instant, not what they say now.
Read `execution show TASK_ID` for its reservations; unfinished cleanup does not mean every attempt still holds a slot.
A `blocked` row names the blocker codes; relay them, do not clear them by force.
A cleanup interrupted after the native removal shows as `removing`; run `sumctl cleanup TASK_ID` (or `sumctl sweep`) to reconcile it from records and observation. A rundown never reconciles it, and nothing polls for merges.

## Maintenance

`status`, `inbox`, and the coordinator's `init` and `pump` carry a `maintenance` view built from saved records only: `open_prs` (each recorded open PR with its `state` and `observed_at` as recorded, and `next: sumctl pr reconcile TASK_ID`), `cleanup` (pending, blocked, removing, or ready, with state, time, blockers, and `next: sumctl cleanup TASK_ID`), and `next: sumctl sweep` when anything is listed. Nothing in it was re-observed: an open PR's state and CI are as of its `observed_at`, so say so when you relay it.
When the user says a PR merged or asks about PR or CI state, run `./bin/sumctl sweep` (or `sumctl pr reconcile TASK_ID` for one task). It is the coordinator's explicit maintenance: one GitHub observation per recorded open PR and one guarded cleanup apply per pending task, least recently maintained first (a failed observation counts, so an unreachable PR does not head every sweep), each task re-read before it acts. After a GitHub timeout it observes no further PR in that pass; no task starts after its budget (`--budget SECONDS`, default 60; `0` starts nothing), but a started task finishes under its own helpers' bounds, so the budget bounds admission, not wall time. Tasks it did not reach are listed under `deferred` with their exact `next` command. A second sweep with nothing pending does nothing.

For a worker that is idle/done/blocked without a report, read bounded relevant output with MCP `herdr_agent_read` or `bin/herdr-scoped agent read PANE --source visible --lines 120`.
Do not assume idle means done. Inspect ambiguous prose; ask for a file report when screen output is incomplete. If a question was never saved, capture it using `sumctl ask` before relaying it to the user.
This is the MVP fallback for non-compliant agents. It runs during a rundown, not continuously; do not promise instant unattended detection.

Record the user's actual answer with:

```sh
./bin/sumctl answer TASK_ID QUESTION_ID --text 'The authorized answer'
```

An answer stays visible until the worker marks it applied.
When that worker is gone and its attempt is already released (`execution park` proved the stop), close the answer instead so the task can be archived:

```sh
./bin/sumctl answer TASK_ID QUESTION_ID --close --reason 'Why no worker will apply it'
```

The question becomes `closed-unapplied` with who closed it, when, and why; it is never recorded as applied. It refuses an open question, which still needs the user's answer, and any attempt that is not released.
`sumctl brief list TASK_ID` shows whether a report was produced under an older brief revision whose verification policy has since changed; treat that as evidence needing refresh review, not as a failure or an approval.

## Pending returns

A closed or busy parent may have pending returns. Each row's `returns` lists what is still owed and to whom: open questions and unverified reports to you, unapplied answers and requested brief revisions to the worker. The `obligation` is open until a later record closes it; the `notification` beside it is only what is known about telling the current recipient: `pending`, `submitted` (prompt accepted or presented in your own output), `uncertain` (a timeout after a possible submission or an interrupted pass), `not-delivered` (busy, absent, wrong checkout, not registered) or `stalled` (three known failures).
`inbox --live` delivers nothing. Delivery is one budgeted pass run by `init`, `bind`, `pump`, and the notice a task write triggers: at most one coalesced notice per recipient, with record IDs and commands only. Items routed to you appear inline in that output; reading them answers, applies, and verifies nothing.
A pass has one budget (default 20 s; `sumctl pump --budget SECONDS`). Deliveries to different recipients no longer wait for each other; only two operations delivering to the same recipient take turns. A recipient the budget or a busy delivery lock did not reach is `deferred`: nothing was recorded for it, it stays pending, and it goes first on the next pass. A Herdr call that fails or times out makes that session unavailable for the rest of the pass, so its later recipients are `not-delivered` and count toward `stalled`. `fanout` shows the sessions, Herdr calls, elapsed and budget milliseconds, and the deferred count.
For an `uncertain` or `stalled` item, first look at the recipient pane, then try once with `sumctl notice TASK_ID --to worker` (or `--to parent`). `sumctl pump` repeats the ordinary pass, which also reaches deferred recipients; `--force` includes uncertain and stalled items. Do not loop.

## Native events and attention

Native event delivery is optional, and only the user decides whether to enable it: `./bin/sumctl hook enable` links a per-installation Herdr plugin that runs the same budgeted delivery pump when a recorded pane settles (never a PR observation or cleanup) and records `attention` for a worker seen blocked, idle without anything owed, exited, or closed. Attention is evidence with a pointer, never a question, a result, or approval; disabling it or a handler failure leaves the rundown path exactly as it was.
`init` shows a `hook` field: whether native event delivery is enabled, its last handled event, and the pending count/age. `./bin/sumctl hook status` adds Herdr's own registry row and the bounded error log; `degraded` means the plugin is off, unlinked, or failing and `init`, `bind`, `pump`, and task-write notices are the delivery path. Nothing stopped because of that.
While the hook is enabled, a worker that Herdr saw `blocked`, idle with nothing owed and no report, exited, or closed has an `attention` record (`attention_records` in each row, `attention:ID` under `returns`) with a bounded output excerpt and a `herdr agent read` pointer. Read the pane before deciding anything: the record proves a native status, not a question, a finished task, a quota cause, or permission to answer an approval prompt.
If the excerpt holds a real question, save it with `sumctl ask` from the worker's brief commands; that record supersedes the attention. If the worker merely resumed, the record closes itself. After inspecting a record that needs no action, run `./bin/sumctl attention TASK_ID ATTENTION_ID --seen`; the record stays in the task.
A rundown does not replay missed events: `inbox --live` shows each worker's observed state, and `sumctl pump` delivers what is still owed. Do not wait for the plugin to notice something it already missed.

## Native metadata

Native metadata is optional and display only, and only the user decides whether to enable it: `./bin/sumctl metadata enable` projects each task's sum state as namespaced `sum_*` tokens on the endpoints sum records, after the helper commands that change records and on handled events, writing only what changed. It never reports or overrides Herdr's agent lifecycle, never renames or relabels anything, never edits the user's configuration, and sends notifications only after `--notify`.
`inbox`, `status`, and `init` also show a `metadata` field: whether sum's task state is projected into Herdr `sum_*` tokens, the last pass, and any `degraded` reason. When it is enabled, the user can read `needs-decision`, `review-ready`, `merged-cleanup-pending`, `instruction-refresh-pending`, or an `attention-*` state in their own sidebar rows without asking you, alongside `sum_pipeline`, which names the one delivery gate the task is waiting on (`ci-fail`, `test-pending`, `settled`); that token is derived from the same records this rundown reads and changes task semantics in no way.
`./bin/sumctl metadata status` lists the tokens sum currently owns per task and the bounded error log; `metadata sync` runs one bounded pass (only changed endpoints are written); `metadata snippet` prints the optional `config.toml` rows for the user to merge themselves. Do not edit their configuration, and do not use `pane report-agent`, `pane rename`, or metadata titles to make a task look finished: Herdr's lifecycle and labels are not yours.
`metadata inbox` opens the read-only `sumctl inbox` listing as a Herdr pane through the linked plugin (`hook enable` first). A `degraded` metadata row means reduced visibility only, not a blocked task; nothing about ask, report, dispatch, update, or this rundown changed, and `inbox --live` stays the authoritative view.

## Code graph

Each task row and outline carries `graph`: the state of the checkout's codegraph index as sum last recorded it (`ready`, `failed`, `exhausted`, `unavailable`), or nothing when no index was requested (`not built`, the normal case). `./bin/sumctl graph status TASK_ID` adds one live freshness observation without writing anything; `graph init TASK_ID` builds or rebuilds it on request, refuses while a codegraph writer for that checkout is still running, and stops at three failures. None of those states blocks a task, changes a slot, or says anything about verification; a worker without a usable graph reads source, as its brief says.

## Restart

In the new pane run `./bin/sumctl init`. If another pane still owns coordination you become a developer; inspect that pane before anything else.
Only when the user confirms the old coordinator pane is gone, run `./bin/sumctl init --role coordinator --reclaim`. It proceeds only when Herdr reports the old pane as `pane_not_found`, or when `init` judged that pane `replaced` (Herdr restarted and a different occupant holds the pane ID). A pane still held by the recorded occupant (even with its agent exited), or an unobservable or unprovable one, is refused, and reclaim never rebinds tasks by itself.
An `init` output whose `incarnation.outcome` is `replaced`, `unrecorded`, or `unobservable` means this pane is not the recorded occupant. Follow its `recovery`; tasks, answers, reservations, and delivery history are unchanged.
Then inspect saved tasks and actual Herdr inventory.
To make an existing task report to this coordinator, explicitly run `sumctl bind TASK_ID --parent-only`. Its output carries one catch-up listing of everything still owed to the parent; the returns that failed against the old pane are not retried against it.
To adopt a known existing worker after a pane ID change, or one whose restored pane is judged `replaced`, use `sumctl bind TASK_ID --worker-pane PANE` after verifying its cwd and task identity. The bind records the inspected occupant, and it is the only way such a pane becomes that task's worker again. A delivery row `refused` names that recovery and sent nothing.
Never launch a replacement just because a pane cannot be observed.
Missing, uncertain, or identity-less execution stays reserved until `execution park` sees verified stop evidence; sum has no automatic retry or process-fencing service.
A pane Herdr reports gone (`pane_not_found` or `agent_not_found`) with no occupant in the recorded checkout is that stop evidence.
Idle is not.
Cleanup occupancy names remaining live checkout processes; it does not block forever on the missing agent.
A refresh delivery that saw `agent_not_found` is `unreachable` for that revision, not a looping worker inbox item.
Do not alter Herdr's global auto-resume policy. Herdr is the sole process/restore owner in this MVP.

## Backup

`sumctl backup /path/outside/state/sum-records.tar.gz` saves versioned task records, every brief revision and version sidecar, reports, and decisions with a manifest.
It is explicitly **records-only**. Worktree code, unpushed commits, dirty files, credentials, and live processes are not captured.
Back up code separately through the repo/host's existing process. A backup of this directory is not a full machine-crash recovery guarantee.
Restore into a new directory, retain the original, and inspect the manifest. Schema mismatches fail rather than attempting an in-place migration.
Do not reuse another machine's pane IDs. The helper refuses cross-machine interaction until explicitly rebound to recovered work.
