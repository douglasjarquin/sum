# MVP validation

Validation date: **2026-09-05**. This report distinguishes executed checks from supplied but unexecuted acceptance tests.

## Executed locally

Environment: Linux x86_64 container; Python **3.13.5**, Node **22.16.0**, Git **2.47.3**. The intended mise Node pin is **22.19.0**; that exact Node version was not installed in this environment.

| Check | Result | What this establishes |
| --- | --- | --- |
| Python task-state and delegation tests | **25 passed** | Real Git worktree isolation with a strict fake Herdr; explicit session scoping; single-start behavior; preserved uncertain launches; task limits; durable questions/answers; no overwritten duplicate questions; concurrent record writes; submitted-vs-applied distinction; unknown-schema refusal; records-only backup |
| Python setup/configuration tests | **8 passed** | Valid local MCP configuration; idempotent config generation; preservation of unrelated settings; conflict refusal; valid source syntax and skill links |
| Node Mesh command tests | **10 passed** | Correct command construction; native prompts; bounded waits using `--until`; busy-target refusal; uncertain handoff errors; bounded reads; curated tool surface |
| Scripted end-to-end demo | **Passed** | Delegation through the helper and fake Herdr into a real Git worktree; a question surviving a busy coordinator; answer/application; an actual branch commit; unchanged primary checkout; report and backup |
| Python compilation, Bash syntax, JavaScript syntax | **Passed** | Source parses on the versions above; this does not prove network installation or live API compatibility |

**43 automated test cases passed; none were skipped in those completed runs.** The Python core tests were executed in three groups of 8, 8, and 9 because whole-suite tool invocations exceeded this container's execution window. The grouped runs contain every core test. Earlier interrupted invocations are not counted as successful runs.

Test output is retained in `docs/test-results.txt`.

## Self-development slice (2026-09-05, macOS)

Executed on the development host for the isolated self-development checkout work (issue #3): the Python suites (60 tests), the Node Mesh tests (10), the offline demo including its new development-checkout section, and `scripts/live_smoke.py` against real Herdr 0.8.2 in a named `sum-test-*` lab session with a short `/tmp` lab root.
The live smoke test now registers its lab coordinator with `init` before `prepare`, uses `pane wait-output --match`, and opens a real workspace whose root pane starts in a development checkout.
No model, GitHub write, or user `default` session was involved.

## Release staging slice (2026-09-05, macOS)

Executed on the development host for issue #4 from a task checkout: the Python suites (69 tests), the Node Mesh tests (10), and the offline demo including its new release-staging section.
The new tests cover staging into an installation reached through a symlink and a path with spaces, manifest validation and tampering, a failing installer and a missing `mise`, four concurrent stagings of one SHA, two installations with separate release directories, a helper call paused inside release N while N+1 is staged, a release tree refusing to own state, and the stable entrypoint serving an old absolute callback before and after staging.
A real staging probe ran in a temporary lab installation with the real installer: `mise install`/`mise which` against the bundled `mise.toml`, a Mesh clone from the installation's local clone, `npm ci`, the overlay, `herdr --skill`, and the MCP smoke test from the staged tree, followed by `doctor` and the MCP smoke test through the installation entrypoint with `.local/current` pointed at that release.
No model, GitHub write, live installation state, or user `default` session was involved; `mise run setup` and `mise run test-live` were not re-run for this slice.

## Versioned brief slice (2026-09-05, macOS)

Executed on the development host for issue #5 from a task checkout: the Python suites (78 tests), the Node Mesh tests (10), and the offline demo including its new brief-revision section.
The new tests cover the dispatch-time version sidecar and `r1`, staging `r2` while a handle reads the original brief (bytes, path, and approved task unchanged), duplicate regeneration writing nothing, the machine-generated change summary and verification flag, refusal to regenerate after the approved body was altered, stale/unknown/damaged/missing revision requests, orphan revision files never overwritten, explicit request and adopt with the notice slot untouched, an unsupported sidecar inspected without migration while `ask`/`report`/`show` keep working, the frozen helper from `b1239a4` preparing, asking, answering, reporting, resolving, and showing on records that also carry new metadata, concurrent old and new writers plus regenerations, and a records-only backup that carries every revision and sidecar and restores relocated paths.
No model, GitHub write, live installation state, or user `default` session was involved.

## Atomic update slice (2026-09-05, macOS)

Executed on the development host for issue #6 from a task checkout: the Python suites, the Node Mesh tests, and the offline demo including its new update/rollback section.
The new tests use a bare Git repository as `origin` and cover: resolving only revisions merged on `origin/main` (a local unmerged commit and a dirty checkout are refused or reported, never reset); an atomic apply while a helper call from the old runtime is paused and while a task's absolute callbacks run before and after the switch; injected failures before the rename, at the rename, and after it (the entrypoint post-check), each leaving a complete selection; a failing installer, a mismatched manifest, a candidate needing another Herdr version, a candidate not supporting a task's brief schema, a candidate helper that cannot read the records, and a concurrent update holding the lock, each leaving the current default and records intact; rollback after a new question and report were saved, with both helper generations reading the same records, rollback to the checkout, an explicit target, and an unknown target; a simulated MCP server keeping its start tree across an apply while a new entrypoint call uses the new default; and gating of `apply`/`rollback` to the coordinator pane and of every update write to the installation's own helper.
No model, GitHub write, live installation state, or user `default` session was involved; `mise run setup` and `mise run test-live` were not re-run for this slice.

## Not executed here

- A full `mise run setup` dependency download/install. The container could not reach the required network endpoints.
- MCP initialization/tool discovery using the actually installed upstream SDK. A smoke test is provided and is run by setup after installation; the executed Node tests instead exercise the production command handlers with a fake runner.
- `mise run test-live` against a real Herdr server. The real binary was not available here.
- Authenticated Codex, Claude, Grok, Cursor, Pi, or other model execution.
- Busy/closed-coordinator behavior with actual harnesses, or human-labeled real-task question capture.
- Remote GitHub repository creation, a push, hosted CI, or PR publication. The connected GitHub interface exposed read operations only; `douglasjarquin/sum` was not created by this build.
- macOS execution. A macOS/Linux CI matrix is included but has not run on GitHub.

The checked-in pin, upstream documentation, and command fixtures are **not** a substitute for live conformance. In particular, source review does not establish every installed harness's trust prompts, instruction loading, or native event behavior.

## Reproduce

With Python and Node already available, no network or credentials are required:

```sh
python3 -m unittest discover -s tests -p 'test_*.py' -v
node --test tests/mesh.test.mjs
python3 scripts/demo.py
```

On the execution host, first run setup, then `mise run test-live` and the steps in [ACCEPTANCE.md](ACCEPTANCE.md). Use a throwaway project for the first real-model delegation.

## Claims intentionally excluded

This MVP does not guarantee unattended delivery or automatic capture of questions printed only in prose. It does not isolate credentials, enforce model spending, safely replace live workers, provide exactly-once external actions, or back up worktree code. A worker report is not verification evidence. See the README for operational limitations.
