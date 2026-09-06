# Portable verification

The project-root `VERIFY.md` contract, the canonical `mise run verify` entrypoint, and the recorded evidence, as used by sum itself and by projects that adopt the same files.
The `create-verification` and `maintain-verification` skills under `.agents/skills/` bootstrap and keep that contract; their fixtures live under `tests/fixtures/verify/`.

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
| `verify.create-bootstrap` | The create-verification scaffold inspects CLI, web, and HTTP-service fixtures, reuses their existing tasks, seeds at most five features labelled incomplete, and vendors the skills with thin aliases | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.create-prove` | One mapped feature per recipe (CLI run, HTTP request) is driven for real and its evidence under the run directory survives the teardown | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.create-idempotent` | Re-running the scaffold writes nothing, keeps user edits and custom tasks, preserves a declared map location, and shows a draft conflict with a proposal instead of overwriting | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.create-broken-base` | A base that fails its own checks is reported `fail`, and a project with no reusable check gets no fictional `verify` task | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.maintain-change-impact` | With `--base`, the audit names the maps whose references changed, requires a rationale for unmapped source changes, and flags map edits as policy while the runner refuses to certify them | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.maintain-stale-claims` | Stale task names, moved paths, unlinked or missing maps, bad drivers, duplicate ids, and unbacked `automated` coverage claims are findings; the audit edits nothing | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.external-clone-skills` | An ordinary clone with the generating toolkit gone follows every generated instruction: check, audit, run, capture, teardown | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.sum-self-audit` | sum's own contract and maps pass the maintenance audit | automated: `tests/test_verify_skills.py` | offline suite |
| `verify.sum-self` | sum's own `mise run verify` runs the suites and demo from a task checkout | manual: `python3 .agents/skills/verify/scripts/verify_run.py` in a sum checkout | run record path in the task report |
