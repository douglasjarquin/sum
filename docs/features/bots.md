# Excluded Bot packs

These are the separately maintained native Grok product described in [the legacy boundary](../legacy.md).

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `bots.contracts` | Bot recipe assets, installer contracts, shared factory settings, and invariant grading cover the current separate products | automated: tests/test_operating_files.py, tests/test_coordinator_invariants.py, tests/test_instruction_package.py | Offline retained tests and immutable comparison for unrelated protected assets/settings; [repair evidence](../verification/issue-354.md) |
| `bots.legacy-lane` | Existing software factory executes through its separately preserved compatible legacy installation | manual: skills/sum-dispatch/references/factory.md and skills/sum-work/references/factory.md | Not exercised by local package tests |

Native Grok install/update, messages/questions, cancellation, independent review, non-code delivery, backup retry, and restore require the manual scenarios in the [Sum pack](../../sum/README.md) and [Square pack](../../square/README.md).
All remain not-run until authenticated receipts are recorded; static traces cannot qualify them.
