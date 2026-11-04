---
name: sum-review
description: Independently inspect a specified stable candidate and report concrete findings to the immediate parent.
---
# Review

Remain the bounded reviewer named by your brief.
Read the absolute procedure path and target repository instructions, assigned scope, candidate/snapshot, required checks, report path, and concrete immediate parent.
If any essential input is missing, save the gap and send a bounded question to that parent.
Do not become lead or start helpers unless the brief explicitly permits it.

Confirm the actual source and candidate, including relevant dirty/untracked inputs.
The candidate must remain frozen during review; an unrelated empty checkout or a bare commit identifier cannot represent missing dirty content.
If another writer changes the inputs, report the review as superseded and request a stable candidate.
Inspect the requirements and actual diff/source independently of the builder's summary.
Use the target repository's review/verification requirements; the optional [engineering rubric](../../.agents/skills/verify/references/engineering-principles.md) adds questions, not architecture.
Do not edit the candidate during read-only review.
Run a check only when authorized and safe for this snapshot; tests may write source or shared state.

Save a substantive report at the absolute assigned path outside disposable checkouts.
Include candidate identity, reviewed scope, concrete findings with severity/location/consequence, required action, checks actually run, and limitations.
Separate observed defects from advice; say when no blocking findings remain without implying unrun checks passed.
A builder report, green CI job, or native status does not substitute for inspection.

After saving, explicitly report findings, completion, a question, or a blocker with `hc send <actual-parent-address> "<concise result and absolute report path>"`.
Use the parent from the brief, never your own `HC_TERMINAL_ID`.
Follow the installed `hc --skill` and command help; automatic done status does not forward your answer.
If the recipient is uncertain, busy in an incompatible program, or no longer an eligible agent, retain the report and follow [Resume](../sum-resume/SKILL.md) before a deliberate send.
Never force input through dialogs or blindly retry uncertain sends.
After sending a complete assignment report, wait for the parent to collect it and close this worker.
A question or blocker leaves the assignment unfinished and eligible for steering.
A corrected frozen candidate goes to a fresh reviewer with the prior report and intervening changes; refresh affected checks/findings in a new candidate-bound report.
