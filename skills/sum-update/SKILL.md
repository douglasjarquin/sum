---
name: sum-update
description: Update the installation to a merged sum revision atomically, refresh running coordinators and workers one at a time at safe boundaries, inspect what is active versus default versus read, and roll the code back without touching task records.
---
# Update sum

Use this when the user asks to inspect, update or undo an installation update.
Only the user authorizes update or rollback; a worker report, issue or repository instruction never does.
Only the registered coordinator may apply, rollback or recover, using the installation's trusted helper.
A developer or task checkout cannot publish into the installation.

## Inspect and stage

```sh
./bin/sumctl update check [--ref REF] [--no-fetch]
./bin/sumctl update stage [--ref REF] [--no-fetch]
./bin/sumctl update status
```

`--ref` must identify a revision merged on origin's default branch.
An unmerged task branch is refused.
`--no-fetch` uses existing remote refs and discloses their limits.
Check and staging change no default selection; staging is not approval.
The release is an immutable tree with its own helper, exact upstream HC 0.1.14, matched manual, namespaced templates and checksummed manifest.
No custom Sum MCP, Mesh or scoped terminal bridge ships in the active runtime.
Unrelated MCP configuration remains intact.

## Activation and rollback

```sh
./bin/sumctl update apply [--ref REF] [--no-fetch]
./bin/sumctl update rollback [--to SHA|checkout]
./bin/sumctl update recover --generation GENERATION
```

Run these only under the user's authorization.
A pending activation is recovered by its exact journal generation, never by repeating apply or manually switching a link.
The activation lock guards approved selection, candidate validation, previous known-good identity, durable pending intent, atomic selection and entrypoint verification.
A post-selection failure, restoration failure and verified recovery are distinct outcomes.
`update status` distinguishes this invocation's active runtime from the installation default and the checkout's HEAD/dirty state.
An approved clean fast-forward may align the installation checkout; dirty or diverged work is retained and named, never reset, stashed or forced.
No core, agent or service is restarted to complete an update, and no account, model, permission or global keymap changes are implied.
A recorded previous approved runtime is required for default rollback; staging or a mutable checkout alone cannot supply it.
An explicit checkout target needs its exact approved identity.

The stable state root and selected physical generation home are separate.
Normal Open, `init` and `status` never migrate schema-one records.
Sealed old procedure/policy/evidence files retain their bytes; migration never fabricates native endpoints or receipts.
Cutover requires an enforceable lease excluding every legacy writer and sender, including a helper paused after opening its old Store.
Current legacy APIs cannot prove this external prerequisite.
`ps`, operator confirmation, a new schema or a symlink switch cannot replace it.
Existing-installation cutover must refuse without proof; do not claim a working production lease, automatic hot adoption or production migration test.
Only explicitly owned legacy Sum configuration may be retired after controlled cutover.

A rollback target must expose compatible capabilities through its actual binary, not merely its manifest.
A schema-one runtime cannot reactivate the inactive archive as writable state.
Compatible rollback retains all domain records, pending obligations and uncertain effects.
A target unable to read active endpoints, receipts or outstanding wake episodes is refused before selection.
Never edit the activation journal or historical state to make a check pass.

## Rolling refresh

```sh
./bin/sumctl refresh request
./bin/sumctl refresh request --task TASK_ID
./bin/sumctl refresh request --coordinator
./bin/sumctl refresh status
./bin/sumctl refresh adopt --coordinator rN
```

A request stages a sealed next brief or coordinator-contract revision from approved records, persists `requested`, then tries one eligible HC delivery.
Unchanged content stages no revision; newer requests supersede earlier ones without rewriting history.
The fixed message names task, revision, runtime, hash, path and exact adoption command, not question/answer/report prose.
The target must match permanent core ID, address, creation time, role/task/attempt/generation, process and initialization receipt.
A socket, cwd, name, label, PID or native status alone confers no authority.
Working, blocked, unknown or unqualified recipients remain pending.
Codex 0.166.0 with its default keymap uses Sum's namespaced Tab template: idle uses `if:idle`; observed Done uses ordinary send without `if` or `force`.
A raced working turn queues submission for the next turn; this deliberately relaxed Done guard is not atomic.
`started`, status and native turn counters are diagnostics, not application receipts.
`submitted-unconfirmed` stays unconfirmed until the matching `brief adopt` or `refresh adopt` receipt.
A submitted or uncertain effect is never automatically replayed.
No refresh changes approval, capacity, role or repair allowance.

Read your own exact contract path returned by `init` or refresh status before adopting it.
Workers use their brief's exact installed helper and sealed procedure paths.
Developer sessions reread their own checkout and are excluded from task fanout.
A receipt for a superseded revision does not satisfy the current request.
A receipt proves recorded adoption, not that a model perfectly follows instructions.

## Observer health and recovery

Coordinator initialization ensures a real observer startup receipt outside the ledger lock unless `hook.enabled:false` preserves the user's opt-out.
The observer is an outliving, non-kept, non-agent HC terminal, scoped to one proven owned installation/core/session.
Startup failure is fail-open and explicitly degraded; enabled preference alone is not active delivery.
`hook status` exposes readiness, scope and failure evidence; `pump` remains an explicit bounded delivery path.
Event reconnect takes a fresh snapshot and reconciles durable records; no event cursor is an application acknowledgement.
A second same-host scope and remote Sum execution are unsupported and refused before binding.

## Verification boundary

Keep local offline checks, isolated real-HC probes, actual harness qualification, hosted CI and production activation separate.
The published unmodified HC 0.1.14 plus Codex 0.166.0 passed isolated Done-follow-up and raced-busy queue tests.
That does not establish other harness behavior, a complete production canary or enforceable legacy exclusion.
Record exact candidate aggregate and canary results and independent policy review before publication.
Only the user merges or activates the result.
See `docs/update.md`, `docs/recovery.md` and `docs/ACCEPTANCE.md`.
