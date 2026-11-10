---
name: sum-work
description: Brief and launch a native HC worker for approved software work. The lead session stays free for messages.
---
# Work

This procedure governs native HC operator work and bounded HC helpers.
For maintenance of Sum itself, including issues and PRs, use [Develop](../sum-develop/SKILL.md) before applying HC prerequisites.
Investigation, implementation, verification, or CI in a maintainer task does not by itself select this operator workflow.
An explicit bounded helper keeps its assigned role and reporting route.

The lead session stays free to send and receive messages.
Read the approved goal, existing task note, and target repository instructions.
Write a brief and launch a native HC worker for implementation, investigation, builds, and CI.
Return to messages once that launch is accepted.
An explicit helper brief keeps that agent on the bounded work.
The worker executes that assignment itself; it does not become a lead or delegate its implementation onward.
Stay within the user's authority; a question that changes scope or permission goes to the user.
A bounded builder may start a reviewer or test only when its brief permits it.
Do not create a management hierarchy.

## Native prerequisite

Read the installed `hc --skill` and relevant command help before using unfamiliar native commands.
[Native setup](../../docs/native-hc.md) describes the supported launch path and qualification limits.
Require a nonempty `HC_TERMINAL_ID` before addressing the current terminal; never pass an empty target and invoke a picker.
Then inspect `hc get "$HC_TERMINAL_ID"` against the matching live core and confirm the actual running agent/status belongs to this conversation.
An environment variable or successful connection alone is insufficient.
Verify native status integration and notifications, including that HC notes have not been disabled.

If HC is missing, connection fails, the address is an old TUI pane such as `wJ5:p5`, the terminal is a foreign PTY, or native status is unavailable, report the exact prerequisite gap.
Start the supported Herdr app independently, use its HC-backed terminal, and launch the selected interactive agent there; Codex requires `codex --no-daemon`.
Do not fabricate environment variables, adopt another conversation, bridge old panes, restart the shared core, install a service, or launch a hidden lead.
HC's own connection/start behavior belongs to upstream HC.
If native launch is unavailable, report the exact gap and stay available for messages.
Do not take the heavy work in the lead session.

## Prepare the work

Create a small task note from [the template](../../templates/task.md) in an ordinary durable directory outside any disposable worktree.
The lead owns that note; each interactive helper owns a separate report.
Record the approved scope, source checkout/branch, candidate, required checks/review, decisions, helper references, and next action.
Before execution, inventory every prescribed check from the target repository's instructions, verification contract, and referenced scenario maps, including required setup, readiness, and teardown.
Keep that detailed inventory in a verification report or task artifact referenced from the task note.
Use the [PR template](../../templates/pull-request.md) for a category-level Pipeline summary at delivery, not a copy of the detailed inventory.
The target's own tools suffice; using this format does not require installing Sum's portable verifier there.
Choose changed behavior scenarios and the base revision before implementation; capture comparable before evidence while the baseline is available.
For later-discovered behavior changes, capture the baseline from its own base checkout when runnable.
Require screenshots for visual changes and transcripts or HTTP request/response evidence for CLI/API changes; use synthetic data and record unavailable baselines honestly.
The portable [Evidence procedure](../../.agents/skills/evidence/SKILL.md) is one capture option; equivalent target-repository tools may produce the same artifacts.
Resolve its absolute Sum checkout path in a helper brief when the target does not carry these tools, and keep captures outside disposable worktrees.
Keep question state as open, answered with the actual answer, then applied with evidence.
Never invent user approval.

Inspect Git status and existing work before editing.
Use Git worktrees and separate branches for concurrent writers; do not share a writable checkout or source-generating output tree.
A read-only reviewer needs the actual candidate, not an empty checkout missing dirty work.
Before review or checks the candidate must be [`frozen`](../sum-finish/SKILL.md#candidate-lifecycle): hold the tracked, dirty, and untracked inputs still, or copy all of them into an immutable snapshot.
Tests may mutate source, snapshots, databases, or services; isolate according to their real effects.
The lead returns to messages.
Do not block the lead turn on `hc wait`, a build, or CI.
A worker report arrives as a message.

## Brief and launch

Resolve the actual immediate-parent address in the launching agent's context.
Put that concrete address into the helper brief; the helper's own `HC_TERMINAL_ID` is not its return destination.
A helper brief contains:

- The task, bounded role, allowed edits, and forbidden scope.
- Absolute repository/worktree and report paths plus the readable absolute path to the applicable Sum `SKILL.md`; instruct it to read that procedure and necessary referenced resources.
- The target repository's applicable instructions, candidate/snapshot, and required checks/evidence.
- The resolved absolute path to the PR template when the helper prepares delivery artifacts in another repository.
- Whether bounded further delegation is allowed.
- The actual immediate parent and this obligation: save the report, then report findings, completion, a question, or a blocker with `hc send <actual-parent-address>`; wait for the parent to collect the report and close the completed worker.

Keep the referenced Sum checkout available while helpers use it, including when working in another repository.
Use native `hc run --bg` from the validated lead so HC supplies parentage, notifications, and parent-linked lifetime.
Read `hc run --help` for the installed launch syntax and selected interactive harness.
Preserve the user's chosen harness, account, model, and permissions; inspect any template before adopting its command or model.
Do not substitute a one-shot print-mode agent for an interactive helper.
Default launches omit `--no-parent`, `--no-notify`, and `--outlive-parent`.
An exceptional outliving helper needs an explicit reason and manual recovery/reporting plan in the task note.

For example, after validating the native prerequisite, substitute actual task-specific values:

```sh
hc run --bg --name "task-tests" --cwd "/absolute/task-worktree" <project-check-command>
hc run --bg --name "task-review" --cwd "/absolute/task-worktree" codex --no-daemon "<complete brief with absolute procedure/report paths and actual parent>"
```

Record exact returned addresses; names aid discovery but do not establish ownership after recovery.
Put the assignment in the launch where supported.
For template launches read `first_message.sent`.

## Receive reports and steer

Ordinary commands use HC's native exit notifications and captured output.
Record command, candidate, exact terminal, exit status, and relevant output in the task note before cleanup.
A test command needs no agent brief, Markdown report, or report-producing wrapper.

Interactive agents must explicitly save and send their reports.
HC's automatic blocked/error/exit events do not forward the agent's final answer when its status becomes done.
Read the substantive report after its concise native notification; a failed notification preserves useful evidence but does not prove receipt.

A bounded helper may use `hc wait --children any` or `hc wait --children all` when waiting helps; the lead stays available for messages.
A message wake may precede its incoming input; do not scrape a report body from a wait response or terminal screen.
A timeout is a check-in, not task failure.
Inspect required reports and actual check output until every required result is accounted for.

### Assignment lifecycle

Record each helper or command row in the task note by one of these states.
Only a row's entry evidence moves an assignment into that state, and its legal next move is the only move.

| State | Entry evidence | Legal next move |
| --- | --- | --- |
| `launched` | `hc run --bg` returned an address | Confirm the first message was sent |
| `delivered` | `first_message.sent` is true | Return to messages |
| `working` | The agent is running the assignment | Return to messages |
| `asking` | A question or blocker arrived | Answer the same verified helper, or take the question to the user |
| `reported` | The report is saved and its notification arrived | Read the report and record its candidate, findings, and command output |
| `collected` | The task note holds the report's results | Close the exact owned worker under [Finish](../sum-finish/SKILL.md) |
| `uncertain` | From any open state: a timeout, unclear return, dead terminal, returned shell, or an address that no longer hosts the helper | Inspect under [Resume](../sum-resume/SKILL.md) and record the state the evidence proves, or cancel; never relaunch, resend, or repeat an external action blind |
| `cancelled` | From any open state: the parent cancelled the assignment or its candidate was superseded | Record the reason, preserve evidence, and close |
| `closed` | The terminal finished and was removed | None |

An ordinary command row skips `delivered`, `working`, `asking`, and `reported`.
Closing a `collected` worker does not wait for independent review, PR merge, or other assignments, and does not mean its findings are resolved or the task accepted.

## Review the completed candidate

After the builder reports completion, the lead reads its saved report and launches a separate reviewer under [Review](../sum-review/SKILL.md) against the frozen candidate.
Give the reviewer the prescribed-check inventory, actual results, and the same concrete candidate identity used by the builder.
The builder's self-review or green CI does not earn the `reviewed` mark.
Record the reviewer address and report path, then return to messages.

A blocking finding makes the candidate [`superseded`](../sum-finish/SKILL.md#candidate-lifecycle); take its replacement through the same states.
Account for every finding against the current candidate before proceeding to Finish.

Use `hc send <returned-helper-address> "<answer or steering>"` only for the same verified live helper in `working` or `asking`.
Read native send help for busy/conditional semantics; do not invent a done condition.
Never default to force-input, type through a permission/trust/authentication dialog, or automatically accept it.
Route uncertainty or interruption through [Resume](../sum-resume/SKILL.md), then use [Finish](../sum-finish/SKILL.md).
