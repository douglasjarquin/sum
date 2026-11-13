# Action log

The live append-only log is `/workspace/sum/action-log.md`.
Only the coordinator appends; workers request a logged authorization before an external side effect and return its actual result.
Initialize only a missing log's header; preserve old rows and record a schema boundary when adopting this schema.
Legacy unresolved effects must be reconciled rather than guessed into new identities.

## Identity and state

An external side effect includes a worker dispatch message, a reviewer launch through native messaging, answer/resume/cancel messages, a draft create, a pull request open/update, a digest write, a push, or any authorized send, publish, or cleanup.
Reads, status, and recaps are not effects.
Allocate one stable operation ID per task, operation, exact target identity, and candidate/content revision or approved routine occurrence.
Persist that tuple and ID before sending; duplicate/delayed messages retain it.
A repair/new candidate has a different tuple.
A newly user-authorized repetition or a new occurrence of an approved routine gets a new ID and cites that authorization/occurrence.
A source file, repeated native message, or elapsed six hours is not new authority.

Allocate a new attempt ID for each actual try of the same operation.
Append `intended` before the side effect, then `done`, `failed`, or `uncertain` with the same operation and attempt IDs plus evidence/receipt.
The latest row for an attempt resolves that attempt's state; preserve earlier rows.
`failed` means confirmed no effect, not merely an error or timeout.
Partial or unobservable effects are `uncertain`, including an `intended` row without a result.
A failed acknowledgement does not prove an operation failed.

Before another attempt inspect all attempts for the operation and read back the actual external result by its identity when supported.
Any confirmed success deduplicates the operation permanently: append `skipped-duplicate` referencing the receipt and do not execute again.
For unresolved intent/partial effects, record read-back evidence; append a resolved result only when proved.
If the effect exists, record `done`; if proved absent and safe, record `failed` before a bounded retry.
If still unknown, stop that operation and ask the user; do not blindly resend or relaunch.
For a confirmed failed attempt, retry within the approved scope and attempt budget using the same operation ID and a new attempt ID.
Stop after two unsuccessful internal repair iterations; that budget grants no extra scope.
These rules do not promise atomic or exactly-once external execution.

## Daily cap

Use UTC dates and exact native target identity.
Count each distinct logical operation ID once on the date its first `intended` row was written.
Three logical actions to the same target in that day require the user's explicit decision before a fourth.
Retries keep the original operation's date and count; status rows and `skipped-duplicate` add no action.
A retry still consumes its separate attempt/repair budget.
The cap never grants permission for a restricted action.

## Header to copy

```markdown
# Action log

Append-only. One line per status; operation identity differs from attempt identity.

| When UTC | Actor | Operation ID | Attempt ID | Task | Operation | Target identity | Revision or occurrence | Status | Evidence and authority | Undo |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
```
