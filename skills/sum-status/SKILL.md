---
name: sum-status
description: Reconcile saved tasks with bounded HC observations, surface unresolved questions, and recover without duplicate workers.
---
# Status

Run `./bin/sumctl inbox --live`.
It reads saved records and adds one bounded native terminal snapshot per HC session of the listed local, unarchived tasks: twelve workers cost one observation call, not twelve sequential waits.
Each such row gets `observed` (the agent state, `absent` when the worker is missing from its session's list, without its own lookup, or `unobserved` with a reason); `fanout` shows the sessions, HC calls, and elapsed and budget milliseconds, and `capacity` shows the held slots.
Without a HC context it returns `live: false` with `live_reason` and the plain records view.
It writes nothing: it delivers no returns, creates no attention records, reconciles no cleanup, and never calls GitHub.
It is not a background monitor.
A status check does not authorize doing requested work in this terminal; dispatch that work.
For one task, `./bin/sumctl context TASK_ID --role coordinator` gives the outline, open questions with their text, the latest handoff (a worker claim), open returns, and the brief/update state in one bounded read; `--since CURSOR` (from the previous read's `cursor`) says whether anything changed and names only the new records.
Reach for full `show` when you need the complete record.
Surface unanswered decisions first, then reports ready for review, then failures/uncertainty, then tasks whose `cleanup` field is `pending` or `blocked`.
Keep unchanged status silent unless the user asked for it.
A `cleanup: pending` row means the exact PR was observed merged and cleanup remains unfinished; offer `sumctl cleanup TASK_ID` (see `skills/sum-deliver/SKILL.md`).
It appears only after `sweep`, `pr reconcile`, or `cleanup` observed the merge; nothing else looks.
Ignored files do not block a ready cleanup.
The cleanup inspection lists and records the ignored entries it will remove, capped at 50 with a total and omitted count; `sweep` applies the same check and records the removed entries with the cleanup result.
A status check reads saved records and HC, never GitHub, so it does not refresh a task's CI row.
The checks are observed at `sumctl pr reconcile` and on demand with `sumctl pipeline ci TASK_ID`; a CI row in the table states what the checks said at that instant, not what they say now.
Read `execution show TASK_ID` for its reservations; unfinished cleanup does not mean every attempt still holds a slot.
A `blocked` row names the blocker codes; relay them, do not clear them by force.
A cleanup interrupted after the native removal shows as `removing`; run `sumctl cleanup TASK_ID` (or `sumctl sweep`) to reconcile it from records and observation.
A status check never reconciles it, and nothing polls for merges.

## Compact presentation

`status --compact` and `inbox --compact` derive presentation from saved records.
`metadata inbox` uses the same compact inbox without requiring HC.
Defaults are 20 items and 240 characters per text; `--limit` accepts 1..100, `--max-chars` accepts 1..2000, and `--after` selects a page.
`counts` always covers all readable sources before paging.
Decisions come first.
`complete: false` and `unknown_sources` mean more work may be hidden by a failed source, never that the inbox is clear.

An open question belongs to the human; an answered question is worker application work and remains in full records.
Reports and review findings are coordinator work; native attention requires inspection.
Passive pipeline and factory context does not become a human decision.
Uncertain or submitted delivery never proves an obligation was handled.

Follow `page.next_after` until the relevant items are covered.
Pages are fresh reads without a receipt or stable cursor; restart at `--after 0` if records change.
Truncated text says so.
Each item has a detail route.
An open decision also carries structured choices, recommendation, rationale, and sources when recorded, plus shared `actions` with task/question target, state, label, and CLI argument array.
Use the offered `answer-choice` route or submit an explicit custom answer with `answer TASK QUESTION --expected-recipient TOKEN --text TEXT`; the expected recipient binds the action to the worker incarnation captured by that view.
A stale route is refused, so refresh before trying again.
The `explain` action asks for context and tradeoffs and leaves the question open; it does not record a decision.
The question detail route selects its page with `context --section decisions --limit 1 --max-chars 0` to read the complete text.
The top-level `detail` route returns full status, including capacity and maintenance.
Use the full live status check above for global live observation and maintenance processing.

With `--compact --live`, observation covers only tasks represented on the rendered page; `observation_scope.omitted_tasks` names the rest.
Counts still describe saved global facts.
No compact view delivers returns, advances gates, releases capacity, marks attention seen, or writes a read receipt.

## Project-scoped status

`status --grouped --project owner/repo` (or `inbox --grouped --project owner/repo`) limits routine detail to one project.
`counts`, `gaps`, and `needs_you` stay global: when another project has a decision, say the count and give its detail route, and keep answering that project's questions and delivering its returns through the ordinary pump; a focus changes presentation, never processing.
Each group carries a `digest` row (current issue and task from the held lane, stage, action owner, saved blocker, outcomes with source and time) and the top-level `digest` carries the envelope.
A direct status request gets the compact scoped answer from that row: the current issue and stage, the blocker or decision if one is recorded, and outcomes since the caller's cursor.
Pass the returned `cursor` back with `--since`; a `resync` means the cursor could not be honoured and nothing is new.
There is no periodic digest: significant outcomes appear at the next existing coordinator interaction, decisions and exceptions follow the wake rules, and passing gates, report arrivals, idle or occupied ticks and unchanged stages are not narrated one by one.
A `factory merge` result, an idle worker or a free lane is never a completion; `observed-merged` needs the saved PR observation.

Keep processing separate from replies.
Answer a direct question, raise a real decision or important exception, and report a meaningful result after the required checks.
Successful commands, report arrivals, passing gates, unchanged status, and idle ticks need no individual narration.
For example:

- An ordinary completion report starts verification and independent review quietly.
  Report the verified result when ready.
- An open question asking which supported behavior the user wants is a decision.
  Show its text and record their actual answer.
- A blocked native status with ambiguous prose needs inspection.
  Save a real question if inspection finds one; never infer a quota failure or permission from the status alone.
- A saved passing CI row is an observation at its recorded time.
  A direct question about current CI requires the existing explicit maintenance command.
- An uncertain delivery remains uncertain.
  Inspect recorded evidence and follow supported recovery.
  Never automatically resend a possibly submitted prompt or claim receipt or processing; an idle recipient alone proves neither.

Sum cannot suppress a harness's own reasoning, tool output, or progress messages.
Running coordinators adopt this policy through the existing refresh procedure; a source edit alone does not change their contract.

## Inbox and ops brief

When the user asks for a weekday morning brief, answer from saved records in this order and omit empty lines:

1.
Unanswered questions, with task id and question id.
2.
Reports that still need coordinator verification.
Reading worker logs is not that run.
3.
Failed verifications and repairs still owed.
4.
Pending approvals: merge, send, publish, delete, spend, `repair extend`, capacity, update, and rollback.
5.
Factory Review count from the saved factory digest, as a count.
6.
Up to three priorities already written in task records.
Do not invent one.
7.
Digests: one line plus the saved link.
Do not paste the digest body.

Stay quiet when every line is empty.
A status check does not enable a routine.
This skill does not arm a schedule.
After two successful manual Status checks the user called clean, they may ask for this shape again.
That request still does not create a scheduled routine.

## Maintenance

Full `status`, full `inbox`, and the coordinator's `init` and `pump` carry a `maintenance` view built from saved records only: `open_prs` (each recorded open PR with its `state` and `observed_at` as recorded, and `next: sumctl pr reconcile TASK_ID`), `cleanup` (pending, blocked, removing, or ready, with state, time, blockers, and `next: sumctl cleanup TASK_ID`), and `next: sumctl sweep` when anything is listed.
Nothing in it was re-observed: an open PR's state and CI are as of its `observed_at`, so say so when you relay it.
When the user says a PR merged or asks about PR or CI state, run `./bin/sumctl sweep` (or `sumctl pr reconcile TASK_ID` for one task).
It is the coordinator's explicit maintenance: one GitHub observation per recorded open PR, close of settled worker and reviewer terminals, and one guarded cleanup apply per pending task, least recently maintained first (a failed observation counts, so an unreachable PR does not head every sweep), each task re-read before it acts.
After a GitHub timeout it observes no further PR in that pass; no task starts after its budget (`--budget SECONDS`, default 60; `0` starts nothing), but a started task finishes under its own helpers' bounds, so the budget bounds admission, not wall time.
Tasks it did not reach are listed under `deferred` with their exact `next` command.
A second sweep with nothing pending does nothing.
The terminal that settled the obligation (the worker report or reviewer verdict for the current candidate, or a terminal task) is closed in that pass; a successor terminal created by resume stays open until it files a new report or review.
`status` shows `pane_closed` and `execution resume` is the path for a later repair, launching at the recorded worktree's current HEAD.
Delivery and refresh to that terminal are `pane-closed`, not `unreachable`.

An intact task checkout belongs to Git and survives native terminal removal.
After conclusive stop evidence, `execution park TASK_ID --attempt ID` releases the exact attempt and `execution resume TASK_ID --attempt ID` reacquires capacity for one successor at the saved checkout HEAD.
Do not recreate legacy workspaces or close an unowned terminal to obtain proof.
Explicit `bind --worker-terminal ADDRESS` and `bind --reviewer-terminal ADDRESS` require inspected native/process identity, task checkout and matching role evidence; a live reviewer cannot be replaced.

For a worker that is idle/done/blocked without a report, read bounded relevant output through the release-matched native HC manual without sending input.
Never accept a trust/auth/permission dialog.
Do not assume idle means done.
Inspect ambiguous prose; ask for a file report when screen output is incomplete.
If a question was never saved, capture it using `sumctl ask` before relaying it to the user.
This is the MVP fallback for non-compliant agents.
It runs during a status check, not continuously; do not promise instant unattended detection.

Record the user's actual answer with:

```sh
./bin/sumctl answer TASK_ID QUESTION_ID --text 'The authorized answer'
```

An answer stays visible until the worker marks it applied.
When that worker is gone and its attempt is already released (`execution park` proved the stop), close the answer instead so the task can be archived:

```sh
./bin/sumctl answer TASK_ID QUESTION_ID --close --reason 'Why no worker will apply it'
```

The question becomes `closed-unapplied` with who closed it, when, and why; it is never recorded as applied.
It refuses an open question, which still needs the user's answer, and any attempt that is not released.
`sumctl brief list TASK_ID` shows whether a report was produced under an older brief revision whose verification policy has since changed; treat that as evidence needing refresh review, not as a failure or an approval.

## Pending returns

A closed or busy parent may have pending returns.
Each row's `returns` lists what is still owed and to whom: open questions, unverified reports, and recorded review verdicts (`review:ID`, with its `verdict` and `candidate`) to you, unapplied answers and requested brief revisions to the worker.
The `obligation` is open until a later record closes it; the `notification` beside it is only what is known about telling the current recipient: `pending`, `submitted` (prompt accepted or presented in your own output), `uncertain` (a timeout after a possible submission or an interrupted pass), `not-delivered` (busy, absent, wrong checkout, not registered) or `stalled` (three known failures).
`inbox --live` delivers nothing.
Delivery is one budgeted pass run by `init`, `bind`, `pump`, and the notice a task write triggers: at most one coalesced notice per recipient, with record IDs and commands only.
Items routed to you appear inline in that output; reading them answers, applies, and verifies nothing.
A pass has one budget (default 20 s; `sumctl pump --budget SECONDS`).
Deliveries to different recipients no longer wait for each other; only two operations delivering to the same recipient take turns.
A recipient the budget or a busy delivery lock did not reach is `deferred`: nothing was recorded for it, it stays pending, and it goes first on the next pass.
A HC call that fails or times out makes that session unavailable for the rest of the pass, so its later recipients are `not-delivered` and count toward `stalled`.
`fanout` shows the sessions, HC calls, elapsed and budget milliseconds, and the deferred count.
For an uncertain historical effect, inspect its exact durable application/consumed-boundary receipt and use `sumctl delivery reconcile TASK_ID --delivery DELIVERY_ID`.
It sends nothing, requires current coordinator authority and exact endpoint/obligation/input-revision proof, and accepts no proof/force/reason override.
It preserves the original effect/error and writes a separate digest-bound reconciliation receipt; repeated identical resolution is a no-op.
It closes no business obligation or reservation.
A separate explicitly requested `sumctl notice TASK_ID --to worker` (or `--to parent`) is a new attempt, never evidence resolving the old uncertain one.
`sumctl pump` repeats the ordinary pass, which also reaches deferred recipients; `--force` is an explicit ledger recovery selection, never a native force-input flag.
Do not replay uncertainty automatically or loop.

Wake consumption and `wake reconcile` affect the wake episode only; they do not by themselves resolve its historical native delivery effect.
Cleanup and activation require the exact separately validated delivery reconciliation where such an effect remains uncertain.

### Coordinator wake episodes

Routine returns to you (reports, review verdicts, attention) are coalesced: an adopted coordinator has at most one outstanding wake episode, persisted in `.sum/deliver/<recipient>.wake.json` before any prompt.
While it is outstanding, a pass that finds more returns for you records nothing for them and reports the row as `coalesced` with the episode id; each such return keeps its real notification state (`pending`), and `inbox` still lists it.
Coalesced is never proof that an ID was delivered or read.

After reading a routine wake, run `./bin/sumctl wake show`.
It writes nothing and prints, per sidecar, the episode and its phase, and for an outstanding episode a `boundary` token over the current open obligations to you: `included` is exactly what the token covers, `omitted` names tasks whose records could not be read.
Read the included work through its records, then run `./bin/sumctl wake consume --boundary TOKEN`.
Consumption is a presentation attestation, not proof of comprehension: it closes only that episode, withholds the included identities from later routine prompts until each closes canonically, and changes nothing else (no answer, application, verification, approval, reservation, or attention record).
Omitted identities and anything that arrived after the token was printed were never covered: the next pass opens a new episode for them.
An exact repeat of a consume is idempotent (`repeated`); a token from an older generation is `expired` and changes nothing; a token for another terminal, another occupant of this terminal, or a tampered payload is refused.
A new occupant of the coordinator terminal does not inherit the old occupant's consumption authority.

Recovery is explicit and timer-free.
`./bin/sumctl wake reconcile [--recipient KEY]` follows the wake recovery table in `docs/recovery.md`: a `prepared` episode (a pass crashed before claiming anything) is closed as `not-submitted` and the next pass may prompt; a `claimed` or `intent` episode (a crash around the prompt) becomes `uncertain` and stays outstanding, with its attempts untouched, until you consume it; `submitted` and `uncertain` are left alone (consume is the way out, or `sumctl notice TASK_ID --to parent` supersedes the episode explicitly with the next generation); an episode bound to a replaced occupant is closed as `replaced` without granting the new occupant anything; an unreadable sidecar is reported by path and never rewritten.
Nothing resends because a process disappeared or time passed.

A new open question owed to you is a decision and is not held behind the routine latch: at the next eligible pass it is sent as one priority prompt (all new decisions to you in that pass coalesced into it) through the same observation, occupant, identity and budget checks as a routine prompt, so a busy terminal receives nothing and the attempt is recorded `not-delivered` until an eligible pass.
The routine episode is untouched (the prompt names it and its `wake consume` route); the decision's attempt is recorded per obligation as usual, and its identity (task, question, key) is recorded on the sidecar so no pass repeats it while it is unchanged: a consumed or already-prompted decision stays in `inbox` and `wake show` (`priority_prompts`) without a new prompt; a question saved again under a new key is a new decision and is eligible once more.
An uncertain priority attempt is never resent by a pass.
Priority is never real-time interruption.

A missed hook event or a disabled hook loses nothing: the return stays in the records and in `inbox`, `wake show` shows whether an episode is outstanding, and the next explicit pass (`init`, `bind`, `pump`, a task write's notice) is the bounded route that delivers it.
Prompts sent by an older or non-adopted helper are listed under `uncoalesced_legacy_prompts`: they were not coalesced, and no episode accounts for them.

## Native events and attention

Coordinator `init` ensures a real scoped HC observer startup receipt outside the ledger lock unless the user explicitly disabled delivery.
The observer is a non-agent outliving HC terminal with no independent payload queue; it takes snapshots after reconnect and runs fair bounded reconciliation.
`hook status` distinguishes enabled preference, owned scope, readiness receipt and degraded errors.
Startup failure is fail-open: `init`, `bind`, `pump` and task-write notices remain available, and no false active state is claimed.
`hook enable` ensures the observer, while `hook disable` preserves the user's explicit preference and stops only a proven owned instance.
A second same-host core/session scope and remote Sum execution are refused before binding.
Qualified Codex 0.166.0 uses the default keymap and namespaced Tab template: defer observed working/blocked/unknown, guard idle with `if:idle`, and send observed Done ordinarily without `if` or `force`.
A raced busy turn queues input for the next turn; this is deliberately not an atomic Done guard.
`started`, native status and turn counters never supply application receipts.
A `review:ID` return is a reviewer's (or an imported Made) verdict you have not acted on yet.
It is a finding, never approval, and it stays open until you act on that candidate: your own `verify`, `pipeline push`, `pipeline pr`, `pipeline ci`, or `pr reconcile`, or a `repair send` after the verdict.
A later report, handoff, or review naming a newer candidate supersedes it.
Reading the notice closes nothing.

While the hook is enabled, a worker that HC observed `blocked`, idle with nothing owed and no report, exited, or closed has an `attention` record (`attention_records` in each row, `attention:ID` under `returns`) with a bounded output excerpt and a native HC output pointer.
Read the terminal before deciding anything: the record proves a native status, not a question, a finished task, a quota cause, or permission to answer an approval prompt.
If the excerpt holds a real question, save it with `sumctl ask` from the worker's brief commands; that record supersedes the attention.
If the worker merely resumed, the record closes itself.
After inspecting a record that needs no action, run `./bin/sumctl attention TASK_ID ATTENTION_ID --seen`; the record stays in the task.
A status check does not replay missed events: `inbox --live` shows each worker's observed state, and `sumctl pump` delivers what is still owed.
Do not wait for the observer to notice something it already missed.

## Native metadata

Native metadata is optional and display only.
Only the user decides to enable it with `metadata enable`.
Projection writes owned namespaced HC labels only to verified recorded terminals and clears only its own recorded keys.
`metadata status` reads saved health; `metadata sync` projects differences; `metadata disable` clears recorded owned keys.
`metadata inbox` and `inbox --grouped --view` print saved task views; no legacy sidebar or plugin-terminal API is required.
A label, name or displayed state never establishes authority, verification or completion.

## Code graph

Each task row and outline carries `graph`: the state of the checkout's codegraph index as sum last recorded it (`ready`, `failed`, `exhausted`, `unavailable`), or nothing when no index was requested (`not built`, the normal case).
`./bin/sumctl graph status TASK_ID` adds one live freshness observation without writing anything; `graph init TASK_ID` builds or rebuilds it on request, refuses while a codegraph writer for that checkout is still running, and stops at three failures.
None of those states blocks a task, changes a slot, or says anything about verification; a worker without a usable graph reads source, as its brief says.

## Restart

Run `init` and follow the exact procedure path it returns.
Without a qualified HC launch context it defaults to developer; it never claims coordinator implicitly.
Prefer `coordinator start` for a new bound launch and matching initialization receipt.
For an existing qualified terminal, initialize explicitly and preserve known launch fields or an unknown model rather than guessing.
Only when the user requested takeover and the previous coordinator is verifiably gone or replaced may you pass `--reclaim`.
Unobservable state is not stop evidence.
Bind old tasks to the new coordinator explicitly with `bind TASK_ID --parent-only`; this supplies one catch-up listing without replaying old uncertain sends.
Native core ID/address/creation time, task/role/attempt/generation, process and matching receipt establish identity; sockets, labels, names, cwd, PID and status alone do not.
Never launch a replacement merely because observation failed.
Capacity remains held until `execution park` proves actual stop and no owned survivors or services.
Normal Open/init/status never migrate schema-one state; existing-installation cutover refuses without an enforceable legacy writer/sender exclusion lease unavailable from current legacy APIs.
See `docs/recovery.md`.

## Backup and records-only restore

`sumctl backup /path/outside/state/sum-records.tar.gz` saves task records, exact brief/procedure revisions, reports, decisions and its manifest.
It does not capture worktree code, unpushed commits, dirty files, credentials or processes; back up code separately.
Use `sumctl restore ARCHIVE NEW_HOME` with a nonexistent destination to inspect a current schema-two records-only backup.
Direct extraction and `--home` against the archived original generation header is not a supported writable restore.
The importer validates scope/schema, safe unique regular paths and the complete gzip stream before target creation.
The imported home has a fresh instance/generation and `records_only:true` quarantine; exact original entries remain under its `restore-source/` directory.
The schema-two pointer/header use `backend:"records"`, distinct from active `backend:"hc"`, so HC-only readers cannot ignore the quarantine flag and acquire authority.
Import is bounded to 64 MiB per entry, 512 MiB expanded archive including trailing data, and 100,000 entries.
It preserves task and pinned procedure bytes but imports no live coordinator, sessions, observer or runtime authority.
Use saved `show`, `status` and bounded `context` reads; all mutations, role claims, binds, capacity adoption and native launch/send are blocked.
There is no automatic promotion or schema-one executable fallback.
Retain the archive and any partial quarantined destination after an I/O failure; a deliberate retry requires another fresh destination rather than overwrite or automatic replay.
See `docs/recovery.md` for the result fields and safety boundaries.
