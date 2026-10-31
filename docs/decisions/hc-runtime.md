# Native HC runtime contract

Sum targets the published upstream HC 0.1.14 generation-one plain JSON-RPC API.
It connects to an existing local Unix socket and validates the permanent core ID, exact version and required method on each connection before using it.
It never starts a core as a side effect of an observation.
Ordinary calls and event subscriptions use separate connections.
An event stream loss requires a new snapshot and ledger reconciliation.

The selected source is the `v0.1.14` release, commit `29e6d375623de40cb4820e0894aa131ba057b796`.
The versioned [release manifest](https://releases.herdr.dev/core/0.1.14/manifest.json) identifies the published artifacts.
The verified macOS aarch64 archive SHA256 is `ca524329d2e0630219550055dcc71f42aafd85fba356e0d543d6f51244487970`.
The binary's `rpc.discover` schema and the release source schema agree after JSON normalization.
The raw binary schema SHA256 is `7f830922f96da848ec151de10349abddf3233c74a06c00c25b2bd58f448c6b11`.
This selection requires no upstream patch.

A terminal's runtime identity is its permanent core ID and returned address.
A socket, display name or alias is a route, not an identity.
Sum authority additionally includes the current task, role, attempt and binding generation.
A terminal observation, status transition or native turn count cannot grant ledger authority.

## Input and application receipts

Native `terminal.send` supports exact `idle` and `working` guards, but no `done` guard.
For qualified Codex 0.166.0 launches, Sum uses a namespaced HC template whose submit key is Tab.
Sum defers recipients observed working, blocked or unknown.
It uses `if:idle` for idle recipients and an ordinary send without `if` for a recipient observed Done.
Sum never supplies `force`.
HC's blocked check, recipient serialization and human-input quiet gate remain active.

The owner approved the following tradeoff instead of modifying upstream HC.
If the observed Done recipient becomes working before input, Codex queues the Tab submission for its next turn.
This does not provide an atomic Done guard.
Qualification must establish the effective command and keymap; unqualified harnesses cannot use this path.
It must not accept trust dialogs, change models or permissions, or rewrite global keymaps to obtain readiness.

Actual Codex 0.166.0 and unmodified published HC 0.1.14 passed isolated tests for a Done follow-up and an ordered submission queued during a FIFO-held working turn.
The busy queue produced both expected application responses in order while HC counted one continuous working episode.
Consequently `started`, status and native turn counters remain diagnostic evidence, not proof that a particular application message was applied.
A matching Sum receipt is required to discharge the durable obligation.

## Transport effects

A dial, hello, identity or local validation failure means the operation was not submitted.
A stable native pre-input refusal can return an obligation to pending.
After a write attempt, a lost, malformed, truncated, oversized or mismatched response leaves a mutation uncertain.
The client never reconnects or replays that operation automatically.
Native error code, message and data remain available for diagnosis.
Passive get/read calls omit caller identity so they do not mark parent news as seen.

Managed terminal creation uses explicit labels and environment, `outlive_parent:true`, `keep:false`, `rm:false` and `no_notify:true`.
Terminal creation and first-message outcomes are separate facts.
A timeout may leave no recoverable address: HC can remove a failed non-kept launch and kill a late spawn.
Sum preserves uncertainty rather than claiming every failed launch can be found or safely retried.
