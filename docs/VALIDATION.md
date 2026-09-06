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

## Rolling refresh slice (2026-09-05, macOS)

Executed on the development host for issue #7 from a task checkout: the Python suites, the Node Mesh tests, and the offline demo including its new refresh section.
The new offline tests cover: a request persisted before the delivery attempt with the fixed instruction carrying only IDs, hashes, paths, and the machine-generated summary (question and answer prose never travel); the notice slot left untouched; busy, blocked, unknown, missing-pane, stale-cwd, and refused-prompt workers left on their brief with the exact reason recorded; repeated requests coalescing to one `requested` event while every delivery attempt is recorded; `r2` superseded by `r3` and a stale receipt refused; four concurrent `ask` calls racing four `refresh request` calls plus a report without losing a record; developer sessions excluded; an MCP contract change and a legacy record without start metadata reported as `capability-deferred`; CLI gating to the coordinator pane and the installation helper; and, through the installation entrypoint, two upstream updates and a rollback that stage `r2`, `r3`, `r4` for the worker and `r1`..`r3` for the coordinator, refuse every earlier receipt, and leave the open question, the report binding, the worktree, and the branch unchanged.

### Two real harnesses in an isolated lab

One run in a named Herdr 0.8.2 session `sum-lab-t288-*` with a lab state home under `/tmp`, this checkout's helper, and two throwaway repositories; the user's `default` session and the production `.sum` were never touched, and the lab session was deleted afterwards.
Both harnesses were dispatched with the same tiny brief (list the brief, answer READY, stop), given one recorded decision each, and then refreshed once with `refresh request`.

| Observation | Claude Code (`claude`, Fable 5.1, auto mode) | Codex CLI (`codex`, gpt-5.6) |
| --- | --- | --- |
| Startup in a fresh worktree | Blocked on the folder-trust dialog; Herdr returned `agent_not_ready`, sum recorded `needs-attention` and did not relaunch | Same: directory-trust dialog, `agent_not_ready`, `needs-attention` |
| After the lab accepted the dialog | `idle`; the brief prompt ran, `sumctl brief list` was executed, READY, `done` in 26 s | `done`, but the account had hit its usage limit: the brief prompt sat in the composer and no turn ran |
| Refresh delivery | Pane observed `done`; instruction submitted; state `submitted-unconfirmed` | Pane observed `done`; instruction submitted; state `submitted-unconfirmed` |
| Receipt | Read `briefs/r2.md`, ran `brief adopt`, applied the recorded decision with `resolve`, kept its checkout and finished work, stopped; `confirmed` after 39 s | None: the composer showed the answer notice and the refresh text concatenated, unprocessed; status honestly stayed `submitted-unconfirmed` with one attempt recorded |
| Native safe boundary | Herdr `agent get`/`agent prompt` lifecycle; `agent prompt` refuses `blocked` agents | Same Herdr surface; no harness-specific hook was available or used |

What this establishes: the same fixed instruction reaches both harnesses only through Herdr's settled-state gate, the receipt is the only thing that turns a row `confirmed`, and a settled (`done`) client that cannot act, here for a billing reason, is reported as unconfirmed rather than updated.
What it does not establish: parity between harnesses, behavior under a blocked permission dialog mid-task, or any harness-native refresh hook; none exists in this slice, so pending state is rechecked only at ordinary interactions.

## Fleet capacity slice (2026-09-05, macOS)

Executed on the development host for issue #8 from a task checkout: the Python suites (95 tests, of which 3 in the new `tests/test_fleet.py`), the Node Mesh tests (10), the offline demo including its capacity check, and `scripts/live_smoke.py` against real Herdr 0.8.2 in a named `sum-test-*` lab session.
The refactor of `tests/test_core.py` into shared `ReleaseLab`/`UpdateLab` fixtures also removed seven tests that the previous class inheritance ran twice; no test case was dropped.

The deterministic regression dispatches twelve scripted workers across twelve isolated Git repositories through the installation entrypoint with capacity raised to twelve, then records a long in-flight tool call, a dirty checkout, an open question, an answered-unapplied question, a pending report, a client connected under an older MCP tool contract, a closed parent, an unknown (removed) worker pane, a worker whose prompt Herdr refuses, a worker that ignores its refresh, and two cooperative workers.
It stages and activates N+1, requests a rolling refresh, answers and reports through the old absolute callbacks, activates N+2 and interrupts the refresh mid-pass with `KeyboardInterrupt`, inspects and recovers with an ordinary repeated request, rolls the default back, refreshes again, refuses an unmerged update while `ask`, `report`, and `inbox --live` continue, and finally admits a new task only after a reported task is archived.
Asserted: no `agent start`, `worktree create`, `agent read`, or any stop/kill call after the initial twelve launches; every worktree keeps its HEAD, branch, and uncommitted file; every question, answer, and report is present with its status; reports stay bound to the revision they were made under; the new task's sidecar records the rolled-back default; the thirteenth dispatch was refused at admission.
Two further tests cover twelve concurrent `prepare` calls against a global limit of six (exactly six admitted, six refused, six `worktree create` calls, admission order serialized) and six concurrent writers into one repository (one admitted), legacy defaults without a settings file, ten invalid settings variants refused before any side effect with the file left as found, a symlinked settings file refused, developer panes and candidate checkouts refused for `settings set`, a lowered limit that evicts nothing, a reported task holding its slot until archived, and the settings file in the records backup.

Measured on this host (Apple Silicon macOS, Python 3.13.5, strict fake Herdr subprocess per call). These are the numbers of one run, not a hardware-independent guarantee:

| Pass over 12 workers | Wall time (ms) | Herdr calls | `agent list` | `agent get` | `agent prompt` |
| --- | --- | --- | --- | --- | --- |
| `status --live` | 87 | 1 | 1 | 0 | 0 |
| `refresh request` after update one | 430 | 11 | 1 | 0 | 10 |
| `refresh request` recovery after interruption | 310 | 7 | 1 | 0 | 6 |
| `refresh request` after rollback | 459 | 11 | 1 | 0 | 10 |
| `inbox --live` after a refused update | 92 | 1 | 1 | 0 | 0 |

Before this slice the same passes made one `agent get` per task (twelve observation calls, each with its own five-second ceiling) before any delivery; now one snapshot per session bounds observation, each prompt keeps its own five-second ceiling, and no transcript is read.

The real-Herdr lab (`scripts/live_smoke.py`) prepared thirteen tasks as shell panes without agents in one named session: `status --live` took 73 ms and `refresh request` 289 ms with exactly one real `herdr agent list` each, every row honestly `pending-unreachable`.
No model was launched; the authenticated-harness fleet canary in `skills/update/SKILL.md` and `docs/ACCEPTANCE.md` section 8 remains a documented manual step and was not executed for this slice.

## Pending returns slice (2026-09-06, macOS)

Executed on the development host for issue #13 from a task checkout: the Python suites (234 tests, of which 15 in the new `tests/test_returns.py`), the Node Mesh tests (10), and the offline demo including its new coalesced-question assertion.
The new tests cover: two distinct questions producing two bounded notices where the second names both IDs and neither carries question text; the same key asked twice recording one obligation and one send; three concurrent `ask` calls, a `report`, and a `refresh request` leaving every question, the report, and the requested revision open with at most one parent prompt per write; an answer visible as `submitted` until `resolve` closes it, never re-sent; a busy root reaching exactly three known-not-delivered attempts, then `stalled`, then one explicit `notice`; a closed root with two tasks keeping every return, `bind --parent-only` presenting one inline catch-up for the rebound task only, and a coordinator `init` listing the same items without a second delivery; a parent rebound between observation and send getting nothing typed and the new route refused until it is the registered coordinator; a prompt that timed out after the fake had received it recorded as `uncertain`, not retried by the pump or `inbox --live`, and sent once more only by an explicit `notice`; a `KeyboardInterrupt` during the prompt leaving an `in-flight` record that reads as `uncertain` with no duplicate turn and no claim in the legacy slot; the frozen helper from `b1239a4` reporting, answering, and asking on the same task without changing a byte of `returns.json`, its records reconciled as pending returns; two instances on one machine keeping separate sidecars; the worker pane refused when answering its own question (in-process and through the CLI with a forged approval text); a report obligation surviving `show` and `inbox --live` and closing only on `verify`; a `pending-busy` refresh riding the next worker notice with the exact `brief adopt` command and recorded as `submitted-unconfirmed` for `refresh status`; and the `pump` CLI refused for an unregistered pane.
The fleet regression still makes exactly one Herdr call for `status --live` over twelve workers: returns owed to the calling coordinator are presented inline, and the closed root's returns fail against the snapshot without an extra call.
No model, GitHub write, live installation state, or user `default` session was involved.

## Native events slice (2026-09-06, macOS)

Executed on the development host for issue #14 from a task checkout: the Python suites (253 tests, of which 19 in the new `tests/test_hook.py`), the Node Mesh tests, the offline demo with its new plugin scenario, and the explicit real-Herdr live smoke test with its new plugin section.
Plugin contract facts were taken from the installed Herdr 0.8.2 (`herdr api schema` plus a probe plugin in an isolated named session), not from the website: accepted manifest event names `pane.agent_status_changed`, `pane.agent_detected`, `pane.exited`, `pane.closed`, `workspace.closed` (an unknown name is a link warning, not a failure); handler environment `HERDR_SESSION`, `HERDR_SOCKET_PATH`, `HERDR_PLUGIN_ID`, `HERDR_PLUGIN_EVENT`, `HERDR_PLUGIN_EVENT_JSON` (`{"event", "data": {"type", "pane_id", "workspace_id", "agent_status", "agent"}}`); `plugin link` takes effect while the server runs and is idempotent for the same path; startup hooks do not run at link time; a failing hook is logged with its exit code and leaves the plugin enabled; `agent read` prints text, not JSON.
The deterministic tests cover: manifest and live link with one explicit reconciliation; enable refused for a developer pane; disable/unlink and re-enable with the synchronous `ask` path unchanged in between; read-only `hook status` with pending count and age; a question saved while the root was busy, a stale idle edge that observed `working` and typed nothing, then the real idle edge delivering once within at most three Herdr calls and a duplicate edge sending nothing; already-satisfied conditions on enable and startup reconciliation; a `stalled` return retried on an edge while an `uncertain` one is not; a `pending-busy` brief revision delivered on the worker's idle edge with the exact `brief adopt` command; a blocked permission UI recorded as attention with the excerpt kept out of the notice and no question or approval invented, closed when the worker resumed; a prose-only question recorded once, unchanged by newer routine output, superseded by the captured `ask`; an open saved question preserved through newer output and edges; exit, `pane.exited`, and `pane.closed` recorded as distinct attention with `--seen`; the launch handshake idle not counted; unrelated panes, other sessions, a wrong plugin id, a missing session, and a malformed pane id refused or ignored without records; two installations with distinct plugin ids and homes; a closed root leaving returns pending until `--reclaim` and `bind --parent-only`, with the rebound root then served on its own edge; six concurrent handler processes sending one notice; a handler crash and an unreadable health file leaving `ask` and `report` unaffected and the rundown reporting `degraded`; `inbox --live` reconciling only while enabled; and twelve workers served by one edge with one snapshot, then disable, blocked edges, and re-enable catching up the already-blocked three.
The live smoke test linked the lab plugin into an isolated registry, drove real `pane report-agent` edges, and observed the real server run the handler: event-to-attention wall time on this host was about 90-140 ms per edge with a 2-36 ms handler; an unrelated pane was ignored; unlink left the registry empty.
Repair 1 (coordinator review of `f33661b`): a coalesced delivery now stamps and prompts only pending/retryable items; a `submitted` sibling is still named in the text, an `uncertain` one is neither named nor restamped. Every `agent read` excerpt invalidates that session's snapshot, so the pump observes again before any prompt. A payload `working` closes attention only after a fresh observation shows it, any fresh non-blocked status clears a blocked record, an unobservable pane degrades without using the payload as truth, and every handler exception is recorded in health. Three regressions added (uncertain sibling, recipient turning busy during the read, out-of-order working); one issue-13 fleet expectation updated because the first task's already-submitted return is no longer restamped by the second task's write. Suite now 256 tests.
Not measured: human-labeled question capture on real harness tasks. A shell reported as an agent is not a model; native idle alone does not detect a question asked in prose, and no such claim is made. Unsupported observation (plugin disabled, unlinked, or a Herdr build without these events) is reported as `degraded` with the rundown path intact.

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
