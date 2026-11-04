---
name: sum-review
description: Independently inspect a specified stable candidate and report concrete findings to the immediate parent.
---
# Review

Remain the bounded reviewer named by your brief.
Review independently in a separate agent from the builder; do not substitute the builder's self-review.
Read the absolute procedure path and target repository instructions, assigned scope, candidate/snapshot, required checks, report path, and concrete immediate parent.
If any essential input is missing, save the gap and send a bounded question to that parent.
Do not become lead or start helpers unless the brief explicitly permits it.

Confirm the actual source and candidate, including relevant dirty/untracked inputs.
The candidate must remain frozen during review; an unrelated empty checkout or a bare commit identifier cannot represent missing dirty content.
If another writer changes the inputs, report the review as superseded and request a stable candidate.
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

After saving, explicitly report findings, completion, a question, or a blocker with `hc send <actual-parent-address> "<concise result and absolute report path>"`.
Use the parent from the brief, never your own `HC_TERMINAL_ID`.
Follow the installed `hc --skill` and command help; automatic done status does not forward your answer.
If the recipient is uncertain, busy in an incompatible program, or no longer an eligible agent, retain the report and follow [Resume](../sum-resume/SKILL.md) before a deliberate send.
Never force input through dialogs or blindly retry uncertain sends.
Remain available for follow-up.
For a corrected frozen candidate, inspect the intervening changes, refresh affected checks/findings, and save and send a new candidate-bound report.
