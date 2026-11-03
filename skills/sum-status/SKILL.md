---
name: sum-status
description: Reconcile saved tasks with bounded HC observations, surface unresolved questions, and recover without duplicate workers.
---
# Status

Run `./bin/sumctl collect --claim TOKEN --budget 20s` during active work or on resume, then use `./bin/sumctl inbox --live` for read-only observation.
Pass this conversation's captured `--claim TOKEN` to every coordinator mutation shown below.
It reads saved records and adds one bounded native terminal snapshot per HC session of the listed local, unarchived tasks: twelve workers cost one observation call, not twelve sequential waits.
Each such row gets `observed` (the agent state, `absent` when the worker is missing from its session's list, without its own lookup, or `unobserved` with a reason); `fanout` shows the sessions, HC calls, and elapsed and budget milliseconds, and `capacity` shows the held slots.
Without a HC context it returns `live: false` with `live_reason` and the plain records view.
It writes nothing: it delivers no returns, creates no attention records, reconciles no cleanup, and never calls GitHub.
It is not a background monitor.
A status check grants no new scope; investigate within the user's instruction and dispatch implementation.
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
The next ordinary harness start reads the selected policy; no live coordinator-contract replacement is performed.

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

## Collection and pending returns

Questions, reports and review verdicts owed to the logical coordinator are listed from the ledger and never routed to an HC parent.
They remain open until their existing action settles them.
Reading an item twice lists it twice; reading records no receipt.
`collect --claim TOKEN [--budget 20s] [--task TASK_ID]` returns actionable items, delivered worker answers, a deadline indicator and one next action.
The default budget is 20 seconds and the maximum is 60 seconds across all native calls and waits.
It never ensures a missing core.
A failed saved route is a `core-unreachable` item; an uncertain launch names explicit worker/reviewer reconciliation.
Inspect the named detail command before deciding how to act.

Unapplied answers and requested worker brief revisions use the existing exact worker route and native input checks.
Collection retries eligible stalled worker deliveries, defers busy/blocked/unknown input and never replays submitted or uncertain effects.
`pump` remains a bounded worker-directed delivery pass; parent-directed prompts are removed.
Historical uncertain delivery effects require exact durable application or retained consumed-boundary proof through `delivery reconcile TASK_ID --delivery DELIVERY_ID --claim TOKEN`.
That command sends nothing, preserves original effect/error bytes and settles no business obligation or reservation.
Do not manufacture proof or bypass an unresolved effect.

Collection snapshots native worker/reviewer attention and output routes.
A blocked or failed terminal is not a question, permission grant or verified result.
Inspect it and preserve trust/authentication boundaries.
Saved attention may be marked seen with its existing `attention` command; the record remains.
A reviewer verdict is a finding, never approval, and remains listed until candidate-bound verification, publication/reconciliation or an authorized repair settles it.
Native status, turn counters and successful sends never replace application receipts.

## Native metadata

Native metadata is optional and display only.
Only the user decides to enable it with `metadata enable`.
Projection writes owned namespaced HC labels only to verified task worker/reviewer terminals and clears only its own recorded keys.
`metadata status` reads saved health; `metadata sync` projects differences; `metadata disable` clears recorded owned keys.
`metadata inbox` and `inbox --grouped --view` print saved task views; no legacy sidebar or plugin-terminal API is required.
A label, name or displayed state never establishes authority, verification or completion.

## Code graph

Each task row and outline carries `graph`: the state of the checkout's codegraph index as sum last recorded it (`ready`, `failed`, `exhausted`, `unavailable`), or nothing when no index was requested (`not built`, the normal case).
`./bin/sumctl graph status TASK_ID` adds one live freshness observation without writing anything; `graph init TASK_ID` builds or rebuilds it on request, refuses while a codegraph writer for that checkout is still running, and stops at three failures.
None of those states blocks a task, changes a slot, or says anything about verification; a worker without a usable graph reads source, as its brief says.

## Restart

Run `init --claim TOKEN` with this conversation's retained token and reread its validated procedure paths.
Collect before claiming progress.
A new conversation without that token sees a conflict naming the recorded session.
Transfer only under the user's explicit takeover instruction; ask first when another conversation may still be coordinating.
`init --transfer` advances the claim generation without rewriting tasks, changing worker endpoints or relaunching work.
An old token is refused at its next locked mutation.
Native worker and reviewer identity checks remain unchanged.
Never launch a replacement merely because observation failed; capacity remains held until the existing park path proves actual stop and no owned survivors or services.
Normal Open/init/status never migrate schema-one state.
Native retirement uses its explicit quiescent gate and root format fence, preserving historical task and sealed procedure bytes.
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
It preserves task and pinned procedure bytes but imports no live coordinator, sessions or runtime authority.
Use saved `show`, `status` and bounded `context` reads; all mutations, role claims, binds, capacity adoption and native launch/send are blocked.
There is no automatic promotion or schema-one executable fallback.
Retain the archive and any partial quarantined destination after an I/O failure; a deliberate retry requires another fresh destination rather than overwrite or automatic replay.
See `docs/recovery.md` for the result fields and safety boundaries.
