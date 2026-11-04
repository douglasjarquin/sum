# Excluded Bot packs

These are the unchanged separate product described in [the legacy boundary](../legacy.md).

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `bots.contracts` | Bot recipe assets, installer contracts, shared factory settings, and invariant grading remain intact | automated: tests/test_operating_files.py, tests/test_coordinator_invariants.py, tests/test_instruction_package.py | Offline retained tests and immutable baseline byte comparison |
| `bots.legacy-lane` | Existing software factory executes through its separately preserved compatible legacy installation | manual: skills/sum-dispatch/references/factory.md and skills/sum-work/references/factory.md | Not exercised by local package tests |
