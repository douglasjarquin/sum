# Update and rollback

Only the user authorizes update, rollback or activation.
Staging code is not approval, and a branch checkout is not an installation update.
Use the installed trusted helper; candidate checkouts cannot publish into the installation.

```sh
./bin/sumctl update check
./bin/sumctl update stage
./bin/sumctl update status
```

These inspect compatibility and stage a merged revision without selecting it.
A release contains its matched helper, exact upstream HC 0.1.14 dependency, namespaced templates/manual and manifest.
It contains no custom Sum MCP server or legacy terminal bridge.
Unrelated MCP configuration is preserved.
Explicitly owned legacy Sum configuration can be retired only after controlled cutover.

## Activation and state compatibility

`update apply` and `update rollback` are coordinator-only authorized actions.
Selection is journaled and atomic; every post-selection receipt remains distinct from staging and approval.
Pending activation recovery uses the exact generation and recovery command returned by the helper rather than repeating apply.
A failure or unknown effect remains visible until reconciled.
No update restarts a core, agent or service or changes a model, account, permissions or global keymap.

The stable state root and selected physical generation home are distinct.
Schema-one history stays sealed and byte-preserved, including old policy/procedure snapshots and evidence.
Normal Open, `init` and `status` never migrate it.
An explicit cutover requires a real enforceable exclusion lease over all legacy writers and senders, including a helper paused after opening old state.
Existing legacy APIs cannot prove that lease.
Process-list inspection, an operator assertion, a schema bump or a symlink switch cannot supply it.
Existing-installation cutover must refuse without proof; no working production lease or automatic hot adoption is claimed.
This external prerequisite is not waived by local HC canaries.

Rollback must verify the actual target binary's capabilities against the active schema, endpoint/receipt contracts and pending obligations.
An older schema-one runtime cannot reactivate the inactive archive as writable state.
Compatible code rollback preserves domain records and uncertainty rather than replaying ambiguous sends.
See [Recovery](recovery.md).

## Rolling refresh

`refresh request` writes a sealed next revision and request before one eligible HC delivery attempt.
The message names the task/revision/runtime/path/hash and exact adoption command; the ledger remains the source of question and report content.
Busy, blocked, unknown or unqualified recipients remain pending.
Idle uses native `if:idle`; qualified Codex observed Done uses ordinary Tab submission with no `if` and no `force`.
A raced working turn queues the message for Codex's next turn; this is not an atomic Done guard.
`submitted-unconfirmed` remains unconfirmed until the matching adoption receipt.
Native `started`, status and turn counters do not prove a refresh was applied.
A new revision never rewrites the worker's original brief or grants new scope.

The coordinator reads the exact contract path named by `init` and adopts its requested revision through `refresh adopt --coordinator rN`.
Workers follow their exact installed helper and sealed procedure paths.
Old procedure and policy bytes are historical evidence and never edited in place.

## Observer health

Coordinator initialization ensures the real observer outside the ledger lock unless delivery was explicitly disabled.
`hook status` distinguishes enabled preference, a matching startup/readiness receipt and degraded failure.
A startup failure is fail-open; explicit `pump`, binding and task-write delivery remain available.
Only a proven owned observer in the supported installation/core/session scope may be replaced or stopped.
A second same-host scope and remote Sum execution remain unsupported and are refused before binding.

## Verification boundary

Local offline checks, isolated real-HC tests, actual harness qualification, hosted CI and production cutover are separate evidence.
An unmodified HC 0.1.14 and Codex 0.166.0 passed isolated Done-follow-up and raced-busy queue probes.
That does not certify every harness, a complete production workflow or legacy-writer exclusion.
Record the exact candidate's aggregate, canary, independent policy review and CI results before presenting it as ready.
