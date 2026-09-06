# Portable verification

The project-root `VERIFY.md` contract, the canonical `mise run verify` entrypoint, and the recorded evidence, as used by sum itself and by projects that adopt the same files.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `verify.contract-check` | `--check` validates the contract, maps, task ownership, and requirements without running anything | automated: `tests/test_verify.py` | offline suite |
| `verify.clean-pass` | A clean committed tree passing `mise run verify` yields a record that certifies the exact SHA | automated: `tests/test_verify.py` | offline suite |
| `verify.dirty-provisional` | A dirty tree yields a provisional record that certifies nothing | automated: `tests/test_verify.py` | offline suite |
| `verify.missing-task` | A contract naming an entrypoint mise does not define is blocked, not passed | automated: `tests/test_verify.py` | offline suite |
| `verify.inherited-task` | A `verify` task resolved from a parent directory is refused as another project's command | automated: `tests/test_verify.py` | offline suite |
| `verify.blocked-dependency` | A missing required command blocks the run instead of failing or passing it | automated: `tests/test_verify.py` | offline suite |
| `verify.stale-build` | Build outputs older than their inputs after the run fail the record | automated: `tests/test_verify.py` | offline suite |
| `verify.intentional-failure` | A failing check yields `fail` and retains the log | automated: `tests/test_verify.py` | offline suite |
| `verify.skipped-scenario` | Manual scenarios are `not-run` unless reported; `not-applicable` needs a reason | automated: `tests/test_verify.py` | offline suite |
| `verify.policy-change` | Edits to the contract, tasks, or maps since `--base` flag the run for root review | automated: `tests/test_verify.py` | offline suite |
| `verify.three-checkouts` | The same fixture verifies from a managed clone, an external clone with sum and Herdr absent, and a linked worktree | automated: `tests/test_verify.py` | offline suite |
| `verify.sum-self` | sum's own `mise run verify` runs the suites and demo from a task checkout | manual: `python3 .agents/skills/verify/scripts/verify_run.py` in a sum checkout | run record path in the task report |
