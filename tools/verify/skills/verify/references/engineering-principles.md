# Shared engineering principles

This reference is the portable engineering rubric for Sum-managed work.
It defines a shared procedure and review lens, not a universal application architecture.
Each project remains authoritative for its package layout, dependencies, data ownership, commands, examples and executable checks.

## Principles

1. Make the supported path easier than the shortcut.
   Review question: Would a contributor find and use the supported path without knowing an internal trick?
2. Make forbidden dependencies fail mechanically.
   Review question: Does the relevant check reject a forbidden import or dependency instead of relying on reviewer memory?
3. Keep one logical owner for durable state, external effects and processors while preserving necessary atomic transactions.
   Review question: Can you name exactly one owner for each durable write, external effect and processor, and are required related changes still atomic?
4. Keep ordinary changes in owned leaf files with colocated tests.
   Review question: Is this change in the owning leaf with its behavior test nearby?
5. Keep exceptions narrow, reviewed, attributable and removable.
   Review question: Is the exception scoped to this case, bound to an independent review, attributable to an owner and easy to remove?
6. Treat the nearest README as the local contract and AGENTS files as dispatch guidance.
   Review question: Did you read the nearest README and use AGENTS only to route work rather than replace the local contract?
7. Keep one maintained and tested canonical example for each recurring change.
   Review question: Does this change reuse or update the one canonical example and prove that it still works?
8. Keep one contract authority and generate only where generation removes duplication.
   Review question: Which file is authoritative, and does generation remove duplicate hand-written copies rather than create another authority?
9. Make verification reproducible and report pass, fail, blocked and not-run evidence truthfully.
   Review question: Can another agent rerun the declared command and distinguish each of those outcomes from the recorded evidence?
10. Delete obsolete paths and keep abstractions and migration machinery proportional.
    Review question: What obsolete path is deleted, and if machinery remains why is it proportional to a current requirement?

## Scope boundary

Sum distributes these principles and the review procedure through its existing verification and delivery skills.
Sum does not impose Encore, `apps/backend`, Atlas, one database per service, a blanket Dockerfile ban, one queue choice or any other project-specific architecture.
An architecture requirement that is documented but not mechanically checked remains a documented requirement until the project adds and runs the relevant check.
An unstandardized project that lacks this reference, an owner README or a canonical example stays an explicit onboarding gap rather than receiving an invented package, command or architecture rule.

## Attribution

This reference is authored for Sum and draws its procedure from Sum's existing verification, delivery and portable-skill contracts.
The Sum source distribution records the external projects that shaped those contracts and remains the source of broader historical credit.
