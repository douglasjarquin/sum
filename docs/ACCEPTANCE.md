# HC runtime acceptance

This is a manual acceptance checklist, not a receipt that these scenarios passed.
Run only in an authorized isolated lab with a unique named HC session and scratch state/config/runtime paths.
Never use the user's default session, live installation, credentials from another scope, model/account changes or permission bypasses to obtain a result.
Retain the exact candidate SHA, published HC artifact identity, harness version/argv/keymap, commands, records and limitations.

## 1. Setup and startup

Stage unmodified HC 0.1.14 and its matched manual/template with custom Sum MCP and legacy Herdr unavailable.
Confirm fresh setup generates no custom Sum MCP and unrelated MCP integrations remain intact on a repeat run.
Explicitly start the lab core through native HC; read-only observation of an absent/stopped core must not auto-start it.
Launch with `coordinator start --socket PATH --session NAME --hc-config-home PATH --harness codex`.
Verify bound environment, permanent core ID/address/creation time, process and matching initialization receipt.
Without that qualified context, `init` must default to developer rather than silently claim coordinator.
Manual existing-terminal initialization must preserve known launch fields or an unknown model.

## 2. End-to-end task

Dispatch one approved task into a verified Git worktree, save a worker question, record the user's answer, obtain the worker's matching application receipt and report, arrange independent candidate-bound review, run coordinator verification, and perform only authorized publication.
Only the user merges except an explicitly authorized factory high-confidence merge contract.
After a real merge, inspect and apply guarded cleanup.
Retain each ledger record and endpoint/attempt identity, not only terminal scrollback.
A native send or Done transition proves no application, review, verification or merge.

## 3. Harness compatibility

| Harness/runtime | Required evidence | Current boundary |
| --- | --- | --- |
| Published unmodified HC 0.1.14 plus Codex 0.166.0 with `--no-daemon`, default keymap, namespaced Tab template | Done follow-up, queued busy race, ordered application responses and matching Sum receipts | Isolated actual Codex Done and busy-queue probes passed; full candidate-specific Sum canary must be recorded separately |
| Other terminal harnesses | Exact argv/model/permission disclosure, prompt receipt, blocked dialogs, native status, submit and resume behavior | Unqualified for automatic input; actual harness gaps remain explicit |
| Model-free native HC lab | Typed API, identity, lifecycle and observer boundaries; separately, full Sum application composition with scripted worker/reviewer terminals and local strict fake GitHub | The full application canary passed in `/tmp/sum330-full-canary-final.json`; `authenticated_harness:false`, `model_execution:false` and confirmed core stop remain explicit; no actual-model or production qualification follows |
| Existing production installation | Enforceable legacy writer/sender exclusion and explicit generation cutover | Current legacy APIs cannot prove exclusion; activation must refuse without the external prerequisite; production migration unrun |

For qualified Codex, defer observed working, blocked and unknown; idle uses `if:idle` and observed Done uses ordinary send without `if` or `force`.
The raced Done-to-working transition queues Tab for the next turn, deliberately relaxing an atomic Done guard.
`started`, status and native turn counts remain diagnostics; they do not replace application receipts.
Trust/auth/permission prompts require the user's action and are never accepted by Sum.

## 4. Preparation, launch and uncertainty

Interrupt Git worktree preparation before and after registration and reconcile with `prepare --reconcile TASK_ID`.
Assert one verified checkout, saved branch/base and no reset or duplicate.
Create a terminal with its opening message proven unsent, then recover once through `execution redeliver` or `reviewer redeliver` with the exact attempt.
Lose the response after a creation/send or kill a sender during input; uncertainty must persist without automatic replay.
A failed non-kept launch may leave no recoverable address.
`started:false` or null must never become processed/completed or trigger an extra Enter.

## 5. Identity and replacement

Reuse a name, socket path, address on a different core, reset state and alias; old authority must not transfer.
Replace the harness process at the same native terminal; require current process/role/receipt proof and explicit rebind.
A non-kept agent replaced by a shell must receive no automatic worker role or prompt.
Exit or replace the coordinator while workers/services continue; bind future returns to the verified replacement without duplicating work.
A second same-host scope and remote task execution must refuse before binding rather than create a silent delivery gap.

## 6. Returns, observers and fairness

Save an answer/refresh while the worker is busy and prove one eligible delivery plus a matching application/adoption receipt.
Generate twelve routine returns and an urgent question; routine wakes coalesce while the question retains priority without busy interruption.
Close a question or rebind a route during observation; revalidation must prevent a stale send without blocking unrelated recipients.
Restart a lab core, drop events and fail/restart an observer; fresh snapshots and reconciliation must catch up without sequence-as-ack or uncertain replay.
Use more idle recipients than one bounded pass and no later HC event; fair continuation must eventually reach eligible pending work.
Bind, consume a wake and adopt a revision without a native status change; post-commit reconciliation must expose eligible work.
Verify one proven owned non-agent outliving observer, real startup/readiness receipt and no payload queue.
Default coordinator initialization must ensure it outside the ledger lock.
A startup failure must leave explicit degraded health and working bounded `pump`, not a false active state.
An explicit disabled preference must survive update and initialization without silently launching an observer.

## 7. Services, capacity and cleanup

Start only a declared task-local service and retain exact native/process/readiness ownership.
Repeat start, conflict on a port, replace the process outside Sum and let a service ignore its interrupt.
Stop must send one interrupt only to a proven owned instance, require actual exit and absence of survivors, and never escalate to kill.
An ended wait or idle terminal is not exit.
Reports and uncertain processes retain capacity until exact stopped-attempt evidence permits park.
Cleanup must block dirty/untracked/unmerged work, shared/unproven processes, pending questions/answers/evidence and active attempts.
A maintenance barrier and reinspection must prevent cleanup racing new work.
Remove only exact owned native terminal/history and the verified Git worktree through their respective APIs.
No HC I/O may occur while holding the state lock.
Repeat completed cleanup as a no-op; preserve unrelated work and records.

## 8. Migration and rollback

Inspect schema-one records without migration through normal Open/init/status.
Preserve sealed old policy/procedure/evidence bytes and keep stable root separate from selected physical generation home.
Pause a legacy writer after Open/read, attempt cutover, and prove enforceable exclusion before authority transfer.
Current APIs cannot prove that lease, so absence of proof must refuse activation; do not simulate it with `ps` or an operator boolean.
A repeated or interrupted attempt must neither lose obligations nor permit mixed writers/senders.
Do not claim a production migration was tested while this prerequisite is unavailable.
Test rollback against actual binary capabilities and active receipts/endpoints, not a manifest alone.
An old schema-one runtime must not reactivate the inactive archive.
Compatible rollback preserves domain records and uncertainty.

Restore a real records-only backup with `restore ARCHIVE NEW_HOME` into a nonexistent destination on the same host and another simulated machine identity.
Compare imported task and pinned procedure bytes/hashes, assert a fresh instance/generation and `records_only:true`, and read the saved task through the new home.
Reject coordinator registration, binding, capacity adoption, native launch/send and every mutation; old sessions, owner and observer must remain historical evidence only.
Reject unsupported schema/scope, unsafe paths, links, duplicate entries and a damaged gzip trailer without a created destination; preserve any partial quarantine after a later I/O failure and refuse destination overwrite.
This restores records for inspection, never a live installation or schema-one authority.

## 9. Optional views and graph

Metadata uses owned namespaced HC labels on verified terminals and never changes role, approval or native lifecycle.
Read-only compact/grouped views preserve global decisions, unknown coverage and explicit paging.
An on-demand codegraph remains isolated to its task checkout and is never verification or policy evidence.
Unrelated MCP configuration and the separate Grok Bot product remain unchanged.

## 10. Evidence and release boundaries

Attach the canonical aggregate once for the final candidate, independent policy review, isolated native canary, actual harness matrix and explicit migration refusal evidence.
Record every unrun manual scenario and unsupported harness as a gap.
Local checks, hosted CI, merge, deployment and production acceptance are separate facts.
Historical `VALIDATION.md` and `test-results.txt` are dated pre-HC receipts, not current certification.
