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

Routine wake episodes preserve prepared, claimed, intent, submitted, uncertain and closed states.
`wake show` reads them and provides the exact included/omitted boundary.
`wake consume --boundary TOKEN` records only the work presented, not its approval, application or verification.
`wake reconcile` resolves an interrupted episode from its phase without sending again.
New questions retain priority behind a routine wake; they do not interrupt a working terminal.
Inspect an uncertain return before authorizing one explicit `notice TASK_ID --to parent|worker` recovery.
Do not loop or use another key to evade an uncertain attempt.

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

## Records-only backup

```sh
./bin/sumctl backup /path/outside/state/sum-records.tar.gz
```

The archive preserves task records, brief revisions, reports, decisions and its manifest.
It does not capture worktree code, unpushed commits, dirty files, credentials or live processes.
Back up code separately and protect archives whose task text may be sensitive.
Restore into a new directory, retain the original, inspect the manifest and preserve unsupported schema files unchanged.
Native addresses are scoped to the original core and process; restoring records never transfers their authority.
