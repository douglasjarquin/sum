# HC terminal runtime

HC owns terminal processes; Git owns worktrees.
Sum owns approved briefs, task roles, capacity, receipts, verification, review, publication and guarded cleanup.
All terminal communication uses the single typed client for unmodified upstream HC 0.1.14, protocol generation one.
There is no Sum MCP server, scoped bridge, raw terminal transport or legacy runtime fallback.
The separate Grok Bot product and its native messaging remain outside this terminal contract.

## Identity

The permanent core ID and returned terminal address identify the native terminal.
Sum additionally binds creation time (`created_ms`), task, role, attempt, binding generation, process identity and a matching `init` receipt.
Socket paths, names, labels, cwd, PID or status alone never grant authority.
A replacement process at the same address does not inherit the old role.
Observation connects only to an existing socket; it never starts a missing core through `hc connect`.
Remote Sum execution and a second same-host core/session scope are currently refused before binding; do not infer support from HC's broader capabilities.

## Input and receipts

Automatic input is qualified only for Codex 0.166.0 with the default keymap and Sum's namespaced `submit: tab` HC template.
The qualified argv includes `--no-daemon`, and qualification hashes the effective command, keymap and configuration without changing model, permissions or account.
Sum defers a recipient observed working, blocked or unknown, uses native `if:idle` for idle, and sends ordinarily without `if` for observed Done.
It never supplies native `force`.
This is a deliberately relaxed Done guard, not an atomic Done predicate.
If Done races into working, Codex queues the Tab submission for its next turn.
HC's blocked, human-input quiet and serialization checks remain active.
No trust dialog, permission change, model/account switch or global keymap edit is authorized to obtain readiness.
`started`, status, native turn count, exit zero and completed waits are diagnostics, never an application receipt or task approval.
Only a matching Sum receipt discharges the corresponding durable obligation.
Other harnesses remain unqualified for automatic input until their actual command, status, submit and resume behavior have evidence.

Creation uses `outlive_parent:true`, `keep:false`, `rm:false` and `no_notify:true`.
Creation and first-message delivery are recorded separately.
A lost response after a mutation may have started leaves uncertainty; Sum never reconnects and automatically replays it.
An early dial, identity or validation failure means nothing was submitted.
A stable native refusal before input can leave an obligation pending.
See [Recovery](recovery.md) for an explicit single recovery attempt.

## Native events and attention

Coordinator `init` ensures a real scoped observer startup receipt outside the ledger lock unless `hook.enabled:false` records the user's opt-out.
The observer is a non-agent HC terminal, with the same outlive-parent and non-kept lifecycle, not a supervisor or task executor.
It subscribes to events and runs bounded reconciliation against the durable ledger, with fresh snapshots after reconnect and fair continuation for deferred recipients.
It has no second payload queue.
One provably owned observer serves the installation's supported core/session scope.
A second unsupported scope is refused rather than silently left without delivery.

`sumctl hook status` shows saved health and observer ownership/readiness evidence.
Enabled preference alone is not proof of a running observer.
Startup failure is fail-open and explicitly degraded; `init`, `bind`, task-write notices and `sumctl pump` remain available.
`hook enable` ensures the observer, while `hook disable` preserves the explicit preference and stops only a proven owned observer.
Only the user chooses to disable delivery.
Native attention points to output requiring inspection; it is not a question, report, verification or approval.
`inbox --live` observes without delivering or calling GitHub.

## Services and cleanup

`env discover TASK` records the project's declared commands.
`env start TASK --command NAME` starts a declared service in an owned HC terminal at the Git checkout and records readiness and process identity.
`env inspect TASK` observes once without starting anything.
`env stop TASK` sends one interrupt only to a matching owned instance and verifies actual exit and survivor absence.
An ended wait or idle terminal is not process exit.
There is no kill escalation or broad compose shutdown.
Replaced, shared or unproven processes remain visible blockers.

Cleanup requires candidate-bound merged PR evidence, settled questions/answers/evidence/attempts, clean merged Git work, exact native identity and a maintenance barrier.
It removes only the exact owned native terminal/history and the verified Git worktree through their respective owners.
It never performs HC I/O while holding the state lock.
See [verification](verification.md) and [recovery](recovery.md).

## Metadata

Optional metadata is display only and uses owned namespaced HC labels on verified recorded terminals.
Labels never supply role, task completion or approval.
`metadata status` reads saved projection health; `metadata sync` projects differences, and disable clears only recorded owned keys.
`metadata inbox` and `inbox --grouped --view` expose saved task views.
No legacy sidebar, workspace-token or plugin-terminal API is required.
