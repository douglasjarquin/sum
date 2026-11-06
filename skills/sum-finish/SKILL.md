---
name: sum-finish
description: Resolve required results against the final candidate and deliver only within the user's authority.
---
# Finish

Recover the approved role before finishing.
Maintainers follow [Develop](../sum-develop/SKILL.md) and apply the shared candidate, review, evidence, check-accounting, and delivery requirements below with current supported tools.
They may perform fixes/checks and monitor CI directly or use supported independent helpers; no native HC dispatch or report destination is required merely to finish maintenance.
Explicit bounded helpers retain their assignment and immediate parent.

Read the task note, approved goal, target repository requirements, and actual final diff.
Keep worker claims, independently inspected findings, your own executed checks, and hosted CI observations distinct.
The optional [engineering rubric](../../.agents/skills/verify/references/engineering-principles.md) supports review; it does not replace project rules.

In the native operational workflow, the lead accounts for reports and delivery state; workers perform fixes, substantive review, evidence capture, checks, and CI monitoring.
If any of that work remains, brief the appropriate worker under [Work](../sum-work/SKILL.md) and return to messages.
Invoking a finishing or shipping skill does not transfer operational worker work into the lead session or reclassify a maintainer as an operator.

Before claiming completion:

- Confirm the final stable candidate, including dirty/untracked content where relevant.
- Account for every required helper report, question/actual answer/application, check command, exit status, and review finding.
- Resolve blocking findings and refresh affected verification/review when inputs change; superseded evidence does not cover a later candidate.
- Require actual results for the repository's required checks and configured CI as applicable; native operator leads collect worker results, while maintainers use their supported execution route.
  Do not require a second full local run merely because of another role.
- Record failures, cancellations, missing results, limitations, and unrun paths truthfully.

A passed check on another candidate, idle/done status, accepted notification, or all children stopped working is insufficient.
A required failed, missing, cancelled, or superseded result leaves the task incomplete unless the user explicitly changes that requirement.
Every implementation candidate requires a separate reviewer's substantive report before completion.
Read-only review inspects the frozen candidate under [Review](../sum-review/SKILL.md).

Perform only delivery the user authorized: commit, push, PR creation, publication, merge, and deployment are separate actions.
Preserve unrelated WIP and stage only owned paths.
Only the user merges unless they explicitly granted merge authority for this task.
For a requested PR, use the [PR template](../../templates/pull-request.md), preserving any additional target-repository requirements.
Keep every prescribed check and scenario, including setup, readiness, and teardown, accounted for with its source, status, evidence, and limitations in the verification report or task artifacts.
The PR body contains a compact Pipeline table with Stage, Status, and Result columns: one row per category, never per test or execution instance.
Summarize each category using the template's status rules, retain non-pass categories with short reasons, and link to detailed results when published instead of copying the inventory into the PR.
Label aggregate-derived results as aggregate coverage, never independently observed per-step passes.
Include the reviewed candidate, independent review outcome, findings and their disposition, before/after evidence, and actual CI state.
Publish comparable before/after evidence for changed behavior using the [Evidence procedure](../../.agents/skills/evidence/SKILL.md) or equivalent target tools.
Visual changes need screenshots; CLI/API changes need transcripts or request/response evidence, with provenance and safe redaction.
Use an explicit reason for unavailable baselines or not-applicable visual evidence, never an invented before result.
A local artifact path is not a published artifact; retain local originals and expose the evidence on the PR.
The authorized delivery role publishes; capture and review do not confer write authority.
Read back the PR body and current head after publication; verify the table, review summary, and accessible evidence match the final candidate.
If the head changed, refresh affected review/checks/evidence before claiming completion.
Missing required review, checks, or publication leaves delivery incomplete; an authorized draft PR may expose those gaps without claiming completion.
Never claim local/static proof establishes authenticated native acceptance or production adoption.

For native HC resources, the immediate parent closes each completed worker promptly after reading and preserving its assignment report, candidate identity, findings, and relevant command output.
Record the assignment disposition in the task note; do not wait for overall acceptance, independent review, or PR merge.
Questions, blockers, missing reports, and idle/done status alone do not establish assignment completion.
Deliberate cancellation or supersession also permits cleanup after recording that disposition and preserving useful evidence.
Before ending a parent-linked terminal, account for required child work and preserve its results.
Reinspect exact task-owned terminal identities/programs using installed HC help; stop no foreign/shared terminal or core.
Use `hc kill <exact-owned-address>`, confirm the terminal finished, then `hc rm <exact-owned-address>` to remove it.
If identity or cleanup is uncertain, preserve the evidence and reconcile actual state under [Resume](../sum-resume/SKILL.md); do not blindly retry.
Cancellation does not satisfy a required check.
An agent returning to a shell may leave its terminal and children alive.
Retain unfinished branches/worktrees and unresolved evidence; never force-reset or delete them to make the task look clean.
Maintainers clean up only their proven-owned resources through the current environment's supported tools; native HC commands apply only to actual HC terminals.
Use ordinary Git cleanup only after delivery/preservation requirements and user authority are satisfied.

Update the task note with final candidate, results, unresolved decisions, and next action.
Report what changed, how it was checked, what shipped, and what remains; no unattended completion is promised after this conversation ends.
