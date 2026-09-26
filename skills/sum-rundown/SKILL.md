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

## Compact presentation

`status --compact` and `inbox --compact` derive presentation from saved records. `metadata inbox` uses the same compact inbox without requiring Herdr. Defaults are 20 items and 240 characters per text; `--limit` accepts 1..100, `--max-chars` accepts 1..2000, and `--after` selects a page. `counts` always covers all readable sources before paging. Decisions come first. `complete: false` and `unknown_sources` mean more work may be hidden by a failed source, never that the inbox is clear.

An open question belongs to the human; an answered question is worker application work and remains in full records. Reports and review findings are coordinator work; native attention requires inspection. Passive pipeline and factory context does not become a human decision. Uncertain or submitted delivery never proves an obligation was handled.

Follow `page.next_after` until the relevant items are covered. Pages are fresh reads without a receipt or stable cursor; restart at `--after 0` if records change. Truncated text says so. Each item has a detail route, and a decision has an answer route. These are argument arrays for the same helper and home; supply the actual user's answer after `--text`, without interpolating question prose into a shell command. The question detail route selects its page with `context --section decisions --limit 1 --max-chars 0` to read the complete text. The top-level `detail` route returns full status, including capacity and maintenance. Use the full rundown above for global live observation and maintenance processing.

With `--compact --live`, observation covers only tasks represented on the rendered page; `observation_scope.omitted_tasks` names the rest. Counts still describe saved global facts. No compact view delivers returns, advances gates, releases capacity, marks attention seen, or writes a read receipt.

## Project-scoped rundown

`status --grouped --project owner/repo` (or `inbox --grouped --project owner/repo`) limits routine detail to one project. `counts`, `gaps`, and `needs_you` stay global: when another project has a decision, say the count and give its detail route, and keep answering that project's questions and delivering its returns through the ordinary pump; a focus changes presentation, never processing. Each group carries a `digest` row (current issue and task from the held lane, stage, action owner, saved blocker, outcomes with source and time) and the top-level `digest` carries the envelope. A direct status request gets the compact scoped answer from that row: the current issue and stage, the blocker or decision if one is recorded, and outcomes since the caller's cursor. Pass the returned `cursor` back with `--since`; a `resync` means the cursor could not be honoured and nothing is new. There is no periodic digest: significant outcomes appear at the next existing coordinator interaction, decisions and exceptions follow the wake rules, and passing gates, report arrivals, idle or occupied ticks and unchanged stages are not narrated one by one. A `factory merge` result, an idle worker or a free lane is never a completion; `observed-merged` needs the saved PR observation.

Keep processing separate from replies. Answer a direct question, raise a real decision or important exception, and report a meaningful result after the required checks. Successful commands, report arrivals, passing gates, unchanged status, and idle ticks need no individual narration. For example:

- An ordinary completion report starts verification and independent review quietly. Report the verified result when ready.
- An open question asking which supported behavior the user wants is a decision. Show its text and record their actual answer.
- A blocked native status with ambiguous prose needs inspection. Save a real question if inspection finds one; never infer a quota failure or permission from the status alone.
- A saved passing CI row is an observation at its recorded time. A direct question about current CI requires the existing explicit maintenance command.
- An uncertain delivery remains uncertain. Inspect recorded evidence and follow supported recovery. Never automatically resend a possibly submitted prompt or claim receipt or processing; an idle recipient alone proves neither.

Sum cannot suppress a harness's own reasoning, tool output, or progress messages. Running coordinators adopt this policy through the existing refresh procedure; a source edit alone does not change their contract.

## Maintenance

Full `status`, full `inbox`, and the coordinator's `init` and `pump` carry a `maintenance` view built from saved records only: `open_prs` (each recorded open PR with its `state` and `observed_at` as recorded, and `next: sumctl pr reconcile TASK_ID`), `cleanup` (pending, blocked, removing, or ready, with state, time, blockers, and `next: sumctl cleanup TASK_ID`), and `next: sumctl sweep` when anything is listed. Nothing in it was re-observed: an open PR's state and CI are as of its `observed_at`, so say so when you relay it.
When the user says a PR merged or asks about PR or CI state, run `./bin/sumctl sweep` (or `sumctl pr reconcile TASK_ID` for one task).
It is the coordinator's explicit maintenance: one GitHub observation per recorded open PR, close of settled worker and reviewer panes, and one guarded cleanup apply per pending task, least recently maintained first (a failed observation counts, so an unreachable PR does not head every sweep), each task re-read before it acts.
After a GitHub timeout it observes no further PR in that pass; no task starts after its budget (`--budget SECONDS`, default 60; `0` starts nothing), but a started task finishes under its own helpers' bounds, so the budget bounds admission, not wall time.
Tasks it did not reach are listed under `deferred` with their exact `next` command.
A second sweep with nothing pending does nothing.
The pane that settled the obligation (the worker report or reviewer verdict for the current candidate, or a terminal task) is closed in that pass; a successor pane created by resume stays open until it files a new report or review. `status` shows `pane_closed` and `execution resume` is the path for a later repair, launching at the recorded worktree's current HEAD. Delivery and refresh to that pane are `pane-closed`, not `unreachable`.

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

A closed or busy parent may have pending returns. Each row's `returns` lists what is still owed and to whom: open questions, unverified reports, and recorded review verdicts (`review:ID`, with its `verdict` and `candidate`) to you, unapplied answers and requested brief revisions to the worker. The `obligation` is open until a later record closes it; the `notification` beside it is only what is known about telling the current recipient: `pending`, `submitted` (prompt accepted or presented in your own output), `uncertain` (a timeout after a possible submission or an interrupted pass), `not-delivered` (busy, absent, wrong checkout, not registered) or `stalled` (three known failures).
`inbox --live` delivers nothing. Delivery is one budgeted pass run by `init`, `bind`, `pump`, and the notice a task write triggers: at most one coalesced notice per recipient, with record IDs and commands only. Items routed to you appear inline in that output; reading them answers, applies, and verifies nothing.
A pass has one budget (default 20 s; `sumctl pump --budget SECONDS`). Deliveries to different recipients no longer wait for each other; only two operations delivering to the same recipient take turns. A recipient the budget or a busy delivery lock did not reach is `deferred`: nothing was recorded for it, it stays pending, and it goes first on the next pass. A Herdr call that fails or times out makes that session unavailable for the rest of the pass, so its later recipients are `not-delivered` and count toward `stalled`. `fanout` shows the sessions, Herdr calls, elapsed and budget milliseconds, and the deferred count.
For an `uncertain` or `stalled` item, first look at the recipient pane, then try once with `sumctl notice TASK_ID --to worker` (or `--to parent`). `sumctl pump` repeats the ordinary pass, which also reaches deferred recipients; `--force` includes uncertain and stalled items. Do not loop.

### Coordinator wake episodes

Routine returns to you (reports, review verdicts, attention) are coalesced: an adopted coordinator has at most one outstanding wake episode, persisted in `.sum/deliver/<recipient>.wake.json` before any prompt. While it is outstanding, a pass that finds more returns for you records nothing for them and reports the row as `coalesced` with the episode id; each such return keeps its real notification state (`pending`), and `inbox` still lists it. Coalesced is never proof that an ID was delivered or read.

After reading a routine wake, run `./bin/sumctl wake show`. It writes nothing and prints, per sidecar, the episode and its phase, and for an outstanding episode a `boundary` token over the current open obligations to you: `included` is exactly what the token covers, `omitted` names tasks whose records could not be read. Read the included work through its records, then run `./bin/sumctl wake consume --boundary TOKEN`. Consumption is a presentation attestation, not proof of comprehension: it closes only that episode, withholds the included identities from later routine prompts until each closes canonically, and changes nothing else (no answer, application, verification, approval, reservation, or attention record). Omitted identities and anything that arrived after the token was printed were never covered: the next pass opens a new episode for them. An exact repeat of a consume is idempotent (`repeated`); a token from an older generation is `expired` and changes nothing; a token for another pane, another occupant of this pane, or a tampered payload is refused. A new occupant of the coordinator pane does not inherit the old occupant's consumption authority.

Recovery is explicit and timer-free. `./bin/sumctl wake reconcile [--recipient KEY]` follows the wake recovery table in `docs/recovery.md`: a `prepared` episode (a pass crashed before claiming anything) is closed as `not-submitted` and the next pass may prompt; a `claimed` or `intent` episode (a crash around the prompt) becomes `uncertain` and stays outstanding, with its attempts untouched, until you consume it; `submitted` and `uncertain` are left alone (consume is the way out, or `sumctl notice TASK_ID --to parent` supersedes the episode explicitly with the next generation); an episode bound to a replaced occupant is closed as `replaced` without granting the new occupant anything; an unreadable sidecar is reported by path and never rewritten. Nothing resends because a process disappeared or time passed.

A new open question owed to you is a decision and is not held behind the routine latch: at the next eligible pass it is sent as one priority prompt (all new decisions to you in that pass coalesced into it) through the same observation, occupant, identity and budget checks as a routine prompt, so a busy pane receives nothing and the attempt is recorded `not-delivered` until an eligible pass. The routine episode is untouched (the prompt names it and its `wake consume` route); the decision's attempt is recorded per obligation as usual, and its identity (task, question, key) is recorded on the sidecar so no pass repeats it while it is unchanged: a consumed or already-prompted decision stays in `inbox` and `wake show` (`priority_prompts`) without a new prompt; a question saved again under a new key is a new decision and is eligible once more. An uncertain priority attempt is never resent by a pass. Priority is never real-time interruption.

A missed hook event or a disabled hook loses nothing: the return stays in the records and in `inbox`, `wake show` shows whether an episode is outstanding, and the next explicit pass (`init`, `bind`, `pump`, a task write's notice) is the bounded route that delivers it. Prompts sent by an older or non-adopted helper are listed under `uncoalesced_legacy_prompts`: they were not coalesced, and no episode accounts for them.

## Native events and attention

Native event delivery is optional, and only the user decides whether to enable it: `./bin/sumctl hook enable` links a per-installation Herdr plugin that runs the same budgeted delivery pump when a recorded pane settles (never a PR observation or cleanup) and records `attention` for a worker seen blocked, idle without anything owed, exited, or closed. Attention is evidence with a pointer, never a question, a result, or approval; disabling it or a handler failure leaves the rundown path exactly as it was.
`init` shows a `hook` field: whether native event delivery is enabled, its last handled event, and the pending count/age. `./bin/sumctl hook status` adds Herdr's own registry row and the bounded error log; `degraded` means the plugin is off, unlinked, or failing and `init`, `bind`, `pump`, and task-write notices are the delivery path. Nothing stopped because of that.
A `review:ID` return is a reviewer's (or an imported Made) verdict you have not acted on yet. It is a finding, never approval, and it stays open until you act on that candidate: your own `verify`, `pipeline push`, `pipeline pr`, `pipeline ci`, or `pr reconcile`, or a `repair send` after the verdict. A later report, handoff, or review naming a newer candidate supersedes it. Reading the notice closes nothing.

While the hook is enabled, a worker that Herdr saw `blocked`, idle with nothing owed and no report, exited, or closed has an `attention` record (`attention_records` in each row, `attention:ID` under `returns`) with a bounded output excerpt and a `herdr agent read` pointer. Read the pane before deciding anything: the record proves a native status, not a question, a finished task, a quota cause, or permission to answer an approval prompt.
If the excerpt holds a real question, save it with `sumctl ask` from the worker's brief commands; that record supersedes the attention. If the worker merely resumed, the record closes itself. After inspecting a record that needs no action, run `./bin/sumctl attention TASK_ID ATTENTION_ID --seen`; the record stays in the task.
A rundown does not replay missed events: `inbox --live` shows each worker's observed state, and `sumctl pump` delivers what is still owed. Do not wait for the plugin to notice something it already missed.

## Native metadata

Native metadata is optional and display only. Only the user decides whether to enable it with `./bin/sumctl metadata enable`, which probes the installed Herdr's schema and refuses locally when tokens are unsupported. Projection writes namespaced `sum_*` tokens under the source `sum:<instance id>` only to endpoints this installation recorded and verified, and only the keys whose value changed; it runs on explicit enable or sync and after successful domain writes and native events (`ask`, `answer`, `report`, `review`, `verify`, `attention`, `archive`, `cleanup`, `bind`, `prepare`, `start`, `refresh`, `pr reconcile`, handled hook events). It uses the same saved presentation facts as compact views: open questions count as human decisions, answered questions as worker work, reports and reviews as coordinator work, and native attention as inspection. Unknown coverage stays explicit. These values never change Herdr's agent lifecycle or grant approval.

`./bin/sumctl metadata status` reads saved projection health, applied tokens per endpoint, ambiguous legacy sources, and the error log without calling Herdr; `metadata sync` probes and writes one full pass (only differences; `--force` rewrites every recorded endpoint after a Herdr restart). `metadata disable` clears exactly the recorded owned keys and reports any refused clear. `metadata snippet` prints optional sidebar rows for the user to merge. Do not edit their configuration or use `pane report-agent`, `pane rename`, or metadata titles to make a task look finished. The `--notify` flag records a preference; transition notification delivery is not implemented.

`metadata inbox` prints the bounded saved compact inbox; `metadata inbox --grouped` (like `inbox --grouped`, and `inbox --grouped --view` for the explicit-refresh terminal view) prints the project-grouped overview; `metadata inbox --open [--placement split|overlay|tab|zoomed]` opens that view natively through the hook plugin's inbox entrypoint after `hook enable`, and otherwise prints the grouped overview with `open.outcome: fallback`. None of these sync metadata or mutate records. A degraded metadata summary concerns visibility only; continue the ordinary rundown and saved obligation processing.

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
