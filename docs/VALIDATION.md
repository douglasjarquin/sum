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
