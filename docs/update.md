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
Live legacy-to-HC migration is unavailable and is not required for a fresh start.
Preserve the old installation, processes, task worktrees and records; inspect its records with `sumctl history` and prepare an independent fresh clone following [HC startup and recovery](hc-workflow.md).
Historical records never become active endpoints or receipts merely by being read or restored.
HC-to-HC updates continue through staged compatible releases and their explicit observer/session handoff.
A shared-core version change requires separate qualification; no silent downgrade or global restart is implied.

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

## Recovery before activation

An older serving helper can refuse activation because it cannot reconcile a lost worker-creation reply or the observer's launcher-to-native executable transition.
Stage the merged repair with the installed helper and inspect the returned release SHA, path and successful verification before proceeding.
From the registered coordinator terminal at the installation root, the staged helper can perform recovery without moving `.local/current`:

```sh
./bin/sumctl update stage
repair_sha=REPLACE_WITH_THE_VERIFIED_STAGED_SHA
SUM_INSTALL_ROOT="$PWD" "$PWD/.local/releases/$repair_sha/bin/sumctl" --home "$PWD/.sum" hook enable
SUM_INSTALL_ROOT="$PWD" "$PWD/.local/releases/$repair_sha/bin/sumctl" --home "$PWD/.sum" execution reconcile TASK --attempt ATTEMPT
```

Use only the exact merged release returned by successful staging, never a development checkout or an unverified executable.
The ordinary coordinator, instance, core and attempt checks still apply.
These recovery commands do not activate the staged release, answer trust prompts, send worker input or release reservations.

`hook enable` can repair the saved Bash-launcher proof only when the original endpoint, creation, PID, scope, receipt, command arguments and checkout still match, and a local process observation corroborates the expected native executable.
It preserves the launcher evidence and records the native foreground without starting a replacement observer.
Future observer startup waits through the Bash launcher instead of recording it as the final foreground.

Worker reconciliation can bind a historical creation intent to its one corroborated original live terminal.
It does not infer that the historical opening was unsent: delivery stays unknown, redelivery stays refused and the attempt remains reserved.
Inspect that terminal and let the user decide any trust or authentication prompt.
Only the exact worker-init receipt permits `execution confirm`; only proven original-process exit and survivor checks permit `execution park`.
Use the verified staged helper for these commands too if the serving helper lacks them.
Activation remains blocked until those attempts are actually settled.
Then retry the authorized `update apply` through the installation's helper.
An uncertain send is never replayed merely to clear an activation blocker.

## Verification boundary

Local offline checks, isolated real-HC tests, actual harness qualification, hosted CI and production cutover are separate evidence.
An unmodified HC 0.1.14 and Codex 0.166.0 passed isolated Done-follow-up and raced-busy queue probes.
That does not certify every harness, a complete production workflow or legacy-writer exclusion.
Record the exact candidate's aggregate, canary, independent policy review and CI results before presenting it as ready.
