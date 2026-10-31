# Recovery

Recover from saved task records and exact runtime identity; never infer permission to repeat an uncertain operation.
HC owns processes and Git owns worktrees.

## Recovery and backup

Run `./bin/sumctl init` and read the procedure path it returns.
A qualified terminal with no explicit coordinator launch defaults to developer rather than taking over.
Use `coordinator start` for a new bound launch, or explicit initialization for an inspected existing qualified terminal.
Pass `--reclaim` only when the user requested takeover and the recorded coordinator is verifiably gone or replaced.
An unobservable core, idle status or an exited wait is insufficient proof.
The coordinator's exit does not stop outliving workers, services or observer terminals.

```sh
./bin/sumctl bind TASK_ID --parent-only
./bin/sumctl bind TASK_ID --worker-terminal ADDRESS
./bin/sumctl bind TASK_ID --reviewer-terminal ADDRESS
```

These are explicit rebindings after inspection, not guesses from a reused name or cwd.
A replacement reviewer requires verified stop evidence for its predecessor; the worker cannot review itself.
No automatic worker replacement or cross-machine transfer is supported.
Another same-host core/session scope is currently refused before binding.
Never close an unowned terminal or delete a checkout to make recovery appear successful.

## Interrupted preparation and opening messages

`prepare --reconcile TASK_ID` uses only the saved approved task intent and verifies Git branch, base, path and registration.
It neither force-resets existing work nor creates a duplicate checkout.
Once a terminal exists, the saved endpoint and first-message outcome are separate.
If the exact attempt proves its opening message was not submitted, use one explicit recovery:

```sh
./bin/sumctl execution redeliver TASK_ID --attempt ATTEMPT_ID
./bin/sumctl reviewer redeliver TASK_ID --attempt ATTEMPT_ID
```

Do not redeliver an uncertain message or type the brief into a terminal by hand.
Worker opening-message recovery requires the saved native effect to be `created` and `first_message.sent` explicitly false; true, missing and null outcomes are refused.
The final claim rechecks the exact launch, initialization receipt and maintenance barrier; a concurrent receipt or cleanup prevents duplicate input.
`execution confirm TASK_ID --attempt ATTEMPT_ID` requires the matching worker receipt and fresh native identity evidence.
A trust/auth dialog is for the user to answer, never for Sum to accept.
A failed non-kept HC launch may leave no recoverable address; uncertainty is retained rather than claiming it can always be rediscovered.
`execution park` releases capacity only after conclusive stop and survivor evidence for the exact attempt.
Then `execution resume` reacquires capacity and starts one successor at the recorded checkout's current HEAD.
A report, idle status or missing identity releases nothing.

## Terminal incarnation

Permanent core ID and returned terminal address are native identity.
Creation time, process identity, task, role, attempt, binding generation and matching initialization receipt establish Sum authority.
A socket path, name, label, cwd, PID or status alone establishes none of it.
A replacement harness process at the same terminal does not inherit the old role.
A lost non-kept agent becoming a shell cannot receive automatic task prompts as that worker.
Follow the helper's explicit recovery reason; preserve pending answers, uncertainty and prior evidence.
Do not manufacture a receipt, endpoint or model observation.

## Observer and wake recovery

`hook status` reports preference, startup/readiness receipt, owned scope and health.
Coordinator `init` ensures startup unless explicitly disabled; enabled preference without a real observer receipt is degraded, not active.
A failed observer startup does not fail the saved task operation.
Use `pump` for one bounded explicit pass while degraded.
Reconnect uses fresh snapshots and ledger reconciliation; an event sequence is never a delivery acknowledgement.
The observer contains no independent payload queue and never automatically replays a submitted or uncertain mutation.
After proving a prior observer finished, replacement retires its old endpoint/receipt proof and persists a distinct `launch_attempt` before native creation.
An early child may record its exact matching startup receipt before `terminal.run` replies; the parent merges only that same launch intent and never overwrites a newer receipt or changed launch.
Transient read-only HC transport/observation failures reconnect to the existing core with bounded backoff and a fresh subscription snapshot.
Cancellation or explicit disablement stops the observer; wrong core, incompatible config/protocol and invalid durable records are hard failures rather than reconnect permission.
No reconnect replays a submitted or uncertain mutation.

Routine wake episodes preserve prepared, claimed, intent, submitted, uncertain and closed states.
`wake show` reads them and provides the exact included/omitted boundary.
`wake consume --boundary TOKEN` records only the work presented, not its approval, application or verification.
`wake reconcile` resolves an interrupted episode from its phase without sending again.
New questions retain priority behind a routine wake; they do not interrupt a working terminal.
Inspect exact durable receipts for an uncertain return and use `delivery reconcile` when they prove its historical application.
An explicitly requested `notice TASK_ID --to parent|worker` is a separate delivery attempt; its success does not resolve or erase the earlier uncertain effect.
Do not loop or use another key to evade an uncertain attempt.

## Reconcile one historical delivery effect

```sh
./bin/sumctl delivery reconcile TASK_ID --delivery DELIVERY_ID
```

This coordinator-only command resolves one exact historical `in-flight`, `uncertain` or `unknown` delivery effect from existing durable application or consumed-boundary evidence.
It sends no input and accepts no proof, force, reason or operator-override flags.
A generic later success, a new notice, native status or an operator assertion is not proof that the old input was applied.
The receipt must bind the exact original endpoint, obligation and semantic input revision; missing historical proof is an explicit refusal.
The locked claim rereads current task authority and the matching receipt rather than trusting an earlier snapshot.

The original returns row, effect state and native error remain unchanged.
A separate `delivery-reconciliations.json` sidecar records the original row digest, copies of the exact resolving receipts, coordinator endpoint and timestamp.
Success returns `task`, `delivery`, `result:reconciled` and `original_state`; an identical repeat returns `result:repeated` without rewriting the file.
Cleanup, lifecycle maintenance and activation consult the same strict resolution validator instead of treating every historical uncertain row as permanently unresolved.
No reconciliation closes a question, applies an answer, verifies a report, releases capacity or approves publication.
Wake consumption still closes only its presentation episode; reconciling a historical native effect is a separate durable operation.
Consumed proof for unresolved deliveries is retained per task before rolling wake history can discard it, so a delayed first reconciliation remains possible.
Retention reads only a successfully committed wake; if retention fails, later wake replacement refuses and preserves that source rather than losing the proof.
A changed or insufficient receipt does not make replay safe.

## Cleanup

Inspect `cleanup TASK_ID` before `cleanup TASK_ID --apply`.
It requires exact merged PR/candidate evidence, no unsettled questions, unapplied answers, required evidence or active attempts, and verified Git/native ownership.
Dirty, untracked or unmerged work and shared, replaced or surviving processes remain named blockers.
The maintenance barrier prevents cleanup racing new work.
Owned services receive one interrupt and require actual exit and survivor proof; there is no kill escalation.
Only exact proven-owned native terminal/history is removed, and Git removes the verified worktree without force.
Never manually remove a task checkout, terminal or branch.

## State generations and rollback

Normal Open, `init` and `status` do not migrate schema-one state.
The stable root selects a separate physical writable generation; sealed historical procedures, policy and evidence retain their original bytes.
An enforceable exclusion lease must prevent every legacy writer and sender before cutover.
Current legacy APIs do not provide that proof, and `ps` output or an operator boolean cannot replace it.
Existing-installation activation therefore refuses without this external prerequisite.
A schema-one runtime cannot reactivate the inactive archive as writable state.
Rollback requires the actual target binary's compatible capabilities, not merely a compatible-looking manifest.
No production migration has been established by offline tests.

## Records-only backup and restore

```sh
./bin/sumctl backup /path/outside/state/sum-records.tar.gz
./bin/sumctl restore /path/to/sum-records.tar.gz /new/absolute/state-home
./bin/sumctl --home /new/absolute/state-home show TASK_ID
./bin/sumctl --home /new/absolute/state-home context TASK_ID --role coordinator
```

Backup preserves task records, brief/procedure revisions, reports, decisions and its manifest.
It does not capture worktree code, unpushed commits, dirty files, credentials or live processes.
Back up code separately and protect archives whose task text may be sensitive.
Use `restore ARCHIVE NEW_HOME`, not direct extraction followed by opening the extracted source header as writable state.
`NEW_HOME` must not exist; an existing destination is refused without overwrite.
The importer accepts the current schema-two records-only archive and validates its manifest, source schema, regular entry paths, uniqueness and complete gzip stream before creating the destination.
Unknown schemas/scopes, traversal or absolute/backslash paths, links and duplicate entries are refused.
Import limits are 64 MiB per entry, 512 MiB total expanded archive including trailing data, and 100,000 entries; oversized input is refused rather than read without bound.

Restore creates a fresh instance, a new physical generation and `records_only:true` quarantine.
Both the root pointer and physical header use schema two with `backend:"records"` and `records_only:true`; ordinary active homes use `backend:"hc"`.
The reader accepts the records backend only with its quarantine flag, while HC-only helpers that require the HC backend reject it rather than treating an unknown flag as writable authority.
Imported task and pinned procedure/version files retain their exact archived bytes; settings, findings and other saved records remain historical records rather than live authority.
The complete archive entries, including the original header and old sessions, are retained under `NEW_HOME/restore-source/` for inspection.
No live session registrations, coordinator owner, observer or runtime binding is imported.
Old native addresses and worktree paths inside historical records never establish authority at the new location.
Saved `show`, `status` and `context` reads are available; `init` may report developer only.
All writes, coordinator claims, rebindings, capacity adoption, native launch/send and other mutations are blocked.
There is no automatic promotion or executable schema-one fallback.

The result names the absolute `home`, new `generation`, `records_only:true`, archive `sha256` and a historical/no-authority note.
If an I/O failure occurs after destination creation, retain that quarantined partial result and the original archive for inspection.
There is no automatic replay, overwrite or cleanup; use a different fresh destination for a deliberate retry.
