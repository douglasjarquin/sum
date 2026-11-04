---
name: sum-work
description: Complete approved software work directly or with bounded native HC helpers.
---
# Work

Read the approved goal, existing task note, and target repository instructions.
Choose direct implementation or delegation when a helper can do useful independent work.
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
Direct local work may continue when authorized, with native delegation and acceptance explicitly unavailable.

## Prepare the work

Create a small task note from [the template](../../templates/task.md) in an ordinary durable directory outside any disposable worktree.
The lead owns that note; each interactive helper owns a separate report.
Record the approved scope, source checkout/branch, candidate, required checks/review, decisions, helper references, and next action.
Keep question state as open, answered with the actual answer, then applied with evidence.
Never invent user approval.

Inspect Git status and existing work before editing.
Use Git worktrees and separate branches for concurrent writers; do not share a writable checkout or source-generating output tree.
A read-only reviewer needs the actual candidate, not an empty checkout missing dirty work.
Freeze the relevant tracked, dirty, and untracked inputs during review/checks or give them an immutable snapshot that contains all of those inputs.
Record what identifies that snapshot; a commit SHA alone does not identify dirty content.
Tests may mutate source, snapshots, databases, or services; isolate according to their real effects.
The lead may continue independent work elsewhere while those inputs remain stable.

## Brief and launch

Resolve the actual immediate-parent address in the launching agent's context.
Put that concrete address into the helper brief; the helper's own `HC_TERMINAL_ID` is not its return destination.
A helper brief contains:

- The task, bounded role, allowed edits, and forbidden scope.
- Absolute repository/worktree and report paths plus the readable absolute path to the applicable Sum `SKILL.md`; instruct it to read that procedure and necessary referenced resources.
- The target repository's applicable instructions, candidate/snapshot, and required checks/evidence.
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
For template launches inspect `first_message.sent`; a live terminal with an undelivered first message has not accepted the task.
A timeout or uncertain return can leave a live terminal: inspect readiness, output, and current program before deliberate recovery.
Do not blind-relaunch or blindly repeat a send or external action.

## Receive, steer, and wait

Ordinary commands use HC's native exit notifications and captured output.
Record command, candidate, exact terminal, exit status, and relevant output in the task note before cleanup.
A test command needs no agent brief, Markdown report, or report-producing wrapper.

Interactive agents must explicitly save and send their reports.
HC's automatic blocked/error/exit events do not forward the agent's final answer when its status becomes done.
Read the substantive report after its concise native notification; a failed notification preserves useful evidence but does not prove receipt.

Use `hc wait --children any` or `hc wait --children all` when waiting helps.
A message wake may precede its incoming input; do not scrape a report body from a wait response or terminal screen.
A timeout is a check-in, not task failure.
Idle, done, all children stopped working, `started:true`, or a successful send is not completion evidence.
Inspect required reports and actual check output until every required result is accounted for.

After reading a complete assignment report, record its candidate, findings, command output, and disposition in the task note.
Promptly close that completed worker under [Finish](../sum-finish/SKILL.md), without waiting for independent review, PR merge, or other assignments.
Assignment completion does not mean its findings are resolved or the overall task is accepted.
Use fresh workers for review, later fixes, and corrected-candidate review, with the saved reports and current candidate in their briefs.

Use `hc send <returned-helper-address> "<answer or steering>"` only for the same verified live helper whose assignment is still unfinished.
Read native send help for busy/conditional semantics; do not invent a done condition.
Never default to force-input, type through a permission/trust/authentication dialog, or automatically accept it.
Route uncertainty or interruption through [Resume](../sum-resume/SKILL.md), then use [Finish](../sum-finish/SKILL.md).
