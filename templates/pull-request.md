## Behavior

Describe the problem, resulting behavior, and scope of the change.
Candidate: commit SHA, plus a snapshot identity if relevant dirty or untracked inputs remain.

## Independent review

Reviewed candidate:
Reviewer and accessible report or substantive summary:
Outcome, findings resolved or outstanding, and review limitations:

## Pipeline

Summarize verification and delivery by category, with one row per stage rather than one row per test, scenario, or execution instance.
Use stages relevant to the task, such as Intent, Rebase, Review, Test, Document, Lint, Push, PR, and CI.
Add separate categories for authenticated native or production acceptance when applicable; local checks cannot establish those results.
Replace the example rows with actual results, keeping each Result to a short summary and an accessible report or run link when available.
Do not paste the check inventory, scenario IDs, or logs into the PR description.

| Stage | Status | Result |
|---|:---:|---|
| Review | ⬜ | Not run |
| Test | ⬜ | Not run |
| Lint | ⬜ | Not run |
| CI | ⬜ | Not run |

Status legend: ✅ Passed; ❌ Failed; ⛔ Blocked; ⬜ Not run; ➖ Not applicable.
A category passes only when all required checks within its stated scope passed.
For mixed results use failed if any required check failed, otherwise blocked if any is blocked, otherwise not run if any is unrun; summarize the gap in Result.
Use not applicable only when the entire category does not apply, with a reason.
Keep failed, blocked, and unrun categories visible; a passing aggregate must not hide unexercised manual acceptance.
Label aggregate-derived results as aggregate coverage rather than independently observed per-step passes.
Keep the complete prescribed-check inventory, including setup, readiness, teardown, individual scenario statuses, evidence, and limitations, in the verification report or task artifacts outside the PR body.
Link to those details when published; identify unavailable publication briefly rather than expanding the table or presenting a machine-local path as accessible evidence.

## Before and after

Show comparable screenshots for visual changes or transcripts/request-response evidence for CLI/API changes, tied to base and candidate identities.
When using the evidence publisher, omit this placeholder section before first publication; its managed Before/after evidence block supplies the section.
Otherwise place the published evidence here; use accessible PR links rather than machine-local paths.
If the baseline is unavailable, state why and label the available after evidence.
If no user-visible behavior changed, explain why evidence is not applicable.

## Remaining limitations

List unresolved findings, failed or missing checks, publication gaps, and unrun paths.
Missing required results leave delivery incomplete; a draft PR can share the actual state without claiming completion.
