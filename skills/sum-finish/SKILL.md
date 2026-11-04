---
name: sum-finish
description: Resolve required results against the final candidate and deliver only within the user's authority.
---
# Finish

Read the task note, approved goal, target repository requirements, and actual final diff.
Keep worker claims, independently inspected findings, your own executed checks, and hosted CI observations distinct.
The optional [engineering rubric](../../.agents/skills/verify/references/engineering-principles.md) supports review; it does not replace project rules.

Before claiming completion:

- Confirm the final stable candidate, including dirty/untracked content where relevant.
- Account for every required helper report, question/actual answer/application, check command, exit status, and review finding.
- Resolve blocking findings and refresh affected verification/review when inputs change; superseded evidence does not cover a later candidate.
- Run the repository's required checks and configured CI as applicable; do not require a second full local run merely because of another role.
- Record failures, cancellations, missing results, limitations, and unrun paths truthfully.

A passed check on another candidate, idle/done status, accepted notification, or all children stopped working is insufficient.
A required failed, missing, cancelled, or superseded result leaves the task incomplete unless the user explicitly changes that requirement.
Independent review is required when the user or repository requires it; useful review can otherwise be selected proportionately.
Read-only review inspects the frozen candidate under [Review](../sum-review/SKILL.md).

Perform only delivery the user authorized: commit, push, PR creation, publication, merge, and deployment are separate actions.
Preserve unrelated WIP and stage only owned paths.
Only the user merges unless they explicitly granted merge authority for this task.
For a requested PR, provide a reviewable description of behavior, verification, remaining limitations, and actual CI state.
Never claim local/static proof establishes authenticated native acceptance or production adoption.

Clean up conservatively after results are accepted or work is deliberately cancelled or superseded.
Record that disposition and preserve useful reports and command output before terminal removal.
Reinspect exact task-owned terminal identities/programs before stopping them using installed HC help; stop no foreign/shared terminal or core.
Cancellation does not satisfy a required check.
An agent returning to a shell may leave its terminal and children alive.
Retain unfinished branches/worktrees and unresolved evidence; never force-reset or delete them to make the task look clean.
Use ordinary Git cleanup only after delivery/preservation requirements and user authority are satisfied.

Update the task note with final candidate, results, unresolved decisions, and next action.
Report what changed, how it was checked, what shipped, and what remains; no unattended completion is promised after this conversation ends.
