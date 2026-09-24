# Verification

Evidence publication, the delivery pipeline, and brief revisions. Moved out of the README.

## Evidence publication into the PR

On by default: `pr reconcile` runs `.agents/skills/evidence/scripts/evidence_publish.py` for every evidence run the worker's handoff lists, and `sumctl pr evidence` repeats it on demand. Validated before/after media (hashes, containment, size, type, secret checks) is uploaded through `gh pr edit --attach` (GitHub CLI 2.99+) into one marked block of the reconciled PR; receipts per content hash and destination so a second publish uploads nothing; prose outside the block preserved; a foreign or hand-edited block refused; every failure leaves the previous body and the local originals intact; an older `gh` defers. `"evidence": {"auto_publish": false}` in `.sum/settings.json` turns the automatic step off

## Delivery pipeline in the PR

Nine ordered gates (Intent, Rebase, Review, Test, Document, Lint, Push, PR, CI) derived from the task's own records and rendered as a status table in one marked `<!-- sum-pipeline:* -->` block of the reconciled PR, beside the evidence block. All nine run: Rebase observes the candidate against its base branch without ever rewriting it, Lint runs whatever lint task the project itself declares, Push fast-forward pushes the reported candidate and nothing else, and CI reads the checks GitHub reports on the PR. CI is observed, never watched, because sum runs no daemon and polls nothing: the checks are read at `pr reconcile` and on demand with `sumctl pipeline ci`, and the row is stamped with when the checks last changed, so a green row means green as of the time shown and a re-read that finds nothing moved edits nothing. A feature-map row whose Evidence cell names a screenshot, screencast, or red/green pair blocks Test until a comparison for that candidate exists, so user-visible work cannot reach origin unproven; the worker captures it with `.agents/skills/evidence/`, or the user waives it and the coordinator records that decision with `verify --accept-missing-evidence`. `sumctl pipeline run` drives the coordinator's gates in order through opening the PR and reconciling it, and stops at the first failure or a blocked Test: it reads GitHub for a pull request on the branch first, so a second run creates nothing, and `--no-pr` stops after Push. `show/refresh/rebase/lint/push/document/pr/ci/publish` do the gates one at a time, and `pr reconcile` publishes the table under the same `auto_publish` switch as the evidence block. The table records what has run, never that a change may merge

### Brief revisions

A worker brief is generated from the record: the approved task text, base, repository, and kind (immutable input), the decisions recorded so far, the current worker procedure, and the return-channel commands. Each task keeps a `versions.json` sidecar with the sum version and brief schema it was dispatched under and a numbered list of brief revisions. Tasks recorded before this sidecar existed are read as legacy `0.1.0`/schema 1 records; nothing is migrated in place.

```sh
./bin/sumctl brief list TASK_ID          # revisions, integrity, active/requested state, report evidence binding
./bin/sumctl report TASK_ID --file r.md --handoff h.json   # worker: prose report plus a bounded structured handoff bound to the candidate SHA
./bin/sumctl review TASK_ID --verdict changes-requested --candidate SHA --file f.md --finding 'blocking: …'   # reviewer pane: full findings plus compact PR notes; binds the reviewer endpoint
./bin/sumctl verify TASK_ID --candidate SHA --result pass --text '...'               # coordinator: own verification record (prose)
./bin/sumctl verify TASK_ID --candidate SHA --execute                                  # coordinator: run the candidate's VERIFY.md contract in a separate checkout under its own run id
./bin/sumctl review TASK_ID --verdict approve --candidate SHA --tool made --text '...' # coordinator: the configured MADE/No Mistakes result; binds no reviewer pane
./bin/sumctl pr reconcile TASK_ID --number N   # coordinator: exact PR identity observed through gh; merged only for a matching head, then publishes the worker's before/after evidence
./bin/sumctl pr evidence TASK_ID              # coordinator: republish that evidence on demand; --run, --visibility, --evidence-root, --dry-run all optional
./bin/sumctl brief regenerate TASK_ID    # stage briefs/rN.md from the record; no model call; duplicates write nothing
./bin/sumctl brief request TASK_ID rN    # mark the latest intact revision as requested; sends nothing
./bin/sumctl brief adopt TASK_ID rN      # the worker records that it now follows the requested revision
```

The file at `brief_path` and every earlier revision are never rewritten, so a worker mid-read keeps a valid brief. Each revision records a machine-generated summary of what changed (policy versions, procedure hash, decisions, commands) and whether verification is affected. A report is bound to the brief revision active when it was submitted; a later verification-affecting revision marks that evidence as needing refresh review rather than approving or rejecting it. Refresh bookkeeping is separate from the notice mirror; a requested revision that `refresh request` could not deliver rides the next coalesced worker notice and is recorded as an attempt in the sidecar. Old helpers keep working on the same records because both sidecars are additive.
