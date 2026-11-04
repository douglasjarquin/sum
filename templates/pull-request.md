## Behavior

Describe the problem, resulting behavior, and scope of the change.
Candidate: commit SHA, plus a snapshot identity if relevant dirty or untracked inputs remain.

## Independent review

Reviewed candidate:
Reviewer and accessible report or substantive summary:
Outcome, findings resolved or outstanding, and review limitations:

## Verification

Inventory every prescribed step from the target repository's instructions, verification contract, and referenced scenario maps before running checks.
Include required setup, readiness, and teardown.
Use the repository's own tools; this table does not require Sum's portable verifier.
Replace the example row with actual results and retain non-pass rows with their reasons.

| Source / check or scenario | Status | Evidence | Limitations |
| --- | --- | --- | --- |
| Instruction or map reference and prescribed step | ⬜ Not run | Command, output/report link, and candidate | Why this step has not run |

Status legend: ✅ Passed; ❌ Failed; ⛔ Blocked; ⬜ Not run; ➖ Not applicable.
Passed means the cited evidence establishes the stated check; failed means it ran and did not meet its expectation.
Blocked names the prerequisite preventing execution, not run names the unexercised step, and not applicable explains why the step does not apply.
Label results inferred from an aggregate command as aggregate coverage in the Evidence column, without implying separately observed step execution.
A grouped row must link to a complete table of the included scenarios and their individual statuses.
Keep local verification, hosted CI, authenticated native acceptance, and production acceptance distinguishable.

## Before and after

Show comparable screenshots for visual changes or transcripts/request-response evidence for CLI/API changes, tied to base and candidate identities.
Place the published evidence block here; use accessible PR links rather than machine-local paths.
If the baseline is unavailable, state why and label the available after evidence.
If no user-visible behavior changed, explain why evidence is not applicable.

## Remaining limitations

List unresolved findings, failed or missing checks, publication gaps, and unrun paths.
Missing required results leave delivery incomplete; a draft PR can share the actual state without claiming completion.
