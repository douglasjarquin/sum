---
name: sum-review
description: Independently inspect a specified stable candidate and report concrete findings to the immediate parent.
---
# Review

Review retains the approved task's role and transport.
For maintainer review under [Develop](../sum-develop/SKILL.md), use the current environment's supported independent reviewer and reporting route; no HC identity or `hc send` is required unless this is an actual HC assignment.
A bounded reviewer remains a reviewer, even when inspecting Sum itself.

Remain the bounded reviewer named by your brief.
Review independently in a separate agent from the builder; do not substitute the builder's self-review.
Read the absolute procedure path and target repository instructions, assigned scope, candidate/snapshot, required checks, report path, and concrete immediate parent/reporting route.
If any essential input is missing, save the gap and send a bounded question to that parent.
Do not become lead or start helpers unless the brief explicitly permits it.

Confirm the actual source and candidate, including relevant dirty/untracked inputs.
Review only a [`frozen`](../sum-finish/SKILL.md#candidate-lifecycle) candidate; if its inputs change, report it `superseded` and request a new one.
Inspect the requirements and actual diff/source independently of the builder's summary.
Use the target repository's review/verification requirements; the optional [engineering rubric](../../.agents/skills/verify/references/engineering-principles.md) adds questions, not architecture.
Inspect before/after evidence for the changed behavior, base/candidate correspondence, redaction, and explicit baseline limitations.
Check that prescribed-check accounting includes non-pass and unrun scenarios and that aggregate coverage does not claim separately observed steps.
Do not edit the candidate during read-only review.
Run a check only when authorized and safe for this snapshot; tests may write source or shared state.

Save a substantive report at the absolute assigned path outside disposable checkouts.
Include candidate identity, reviewed scope, concrete findings with severity/location/consequence, required action, checks actually run, and limitations.
Account for the prescribed-check inventory and distinguish observed check results from aggregate coverage or unrun scenarios.
Separate observed defects from advice; say when no blocking findings remain without implying unrun checks passed.
A builder report, green CI job, or native status does not substitute for inspection.

After saving, explicitly report findings, completion, a question, or a blocker through the assigned supported reporting route.
For a native HC assignment use `hc send <actual-parent-address> "<concise result and absolute report path>"`; the native reporting and recipient checks in the following paragraph apply to that route.
Use the parent from the brief, never your own `HC_TERMINAL_ID`.
Follow the installed `hc --skill` and command help; automatic done status does not forward your answer.
If the recipient is uncertain, busy in an incompatible program, or no longer an eligible agent, retain the report and follow [Resume](../sum-resume/SKILL.md) before a deliberate send.
Never force input through dialogs or blindly retry uncertain sends.
A complete report makes the assignment [`reported`](../sum-work/SKILL.md#assignment-lifecycle): wait for the parent to collect it and close this worker.
A question or blocker leaves it `asking`.
