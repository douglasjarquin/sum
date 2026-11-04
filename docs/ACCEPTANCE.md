# Ordinary-agent and HC acceptance

This checklist separates automated lab evidence from authenticated harness journeys and production acceptance.
All checks use private state, a unique named HC session, and scratch config/runtime paths.
Never use the user's default session or live installation for a test.
Record the candidate SHA, exact published HC artifact, harness version, commands, ledger records, and limitations with each manual run.
The canonical aggregate is offline evidence and cannot certify an unrun manual journey.

## Issue 342 acceptance matrix

The named tests below are automated drivers, not a claim that the final aggregate or a manual journey has passed.
The verification artifact and PR record their actual result for the final candidate.

| Row | Automated driver | Manual boundary |
| --- | --- | --- |
| A1 | `cli.TestExternalCoordinatorLifecycle`, `cli.TestInitDesignatedWorkspaceClaimsLogicalCoordinator` | Ordinary terminal Grok and Codex coordinator entry: not run for this candidate |
| A2 | CLI unstaged-init blocker and `tests/test_setup.py` | Fresh-host prerequisite installation: not run |
| A3 | `cli.TestExternalCoordinatorLifecycle` | Each helper call is a separate process with a scrubbed lab environment |
| A4 | `cli.TestOrdinaryCoordinatorIgnoresInheritedNativeIdentity` and init environment-precedence tests | No user environment surgery is required |
| A5 | `hcboot` default route tests | Real default-core reuse, generation/minimum-version and missing-core refusal: not run for this candidate |
| A6 | `prepare` parentless native launch tests and `cli.TestOfflineDemo` | Two authenticated workers in distinct checkouts: not run |
| A7 | `collect` answer delivery tests and `cli.TestExternalCoordinatorLifecycle` | Grok and Codex busy-worker answer delivery through active collection: not run |
| A8 | `review.TestCandidateBoundReviewerEvidenceDischargesAmbiguousKickoff`, `cli.TestExternalCoordinatorLifecycle` | Authenticated independent reviewer: not run |
| A9 | `execution` and `review` saved-route reconciliation tests; CLI lost-creation tests | Real transport interruption: not run for this candidate |
| A10 | `cli.TestExternalCoordinatorLifecycle` and retirement preservation tests | Existing installation retirement and resumption: not run |
| A11 | Coordinator claim conflict, transfer, race and stale-generation tests | Another live conversation must not share the recorded token automatically |
| A12 | `collect` shared-budget and terminal-wait tests | Authenticated bounded waiting in Grok and Codex: not run |
| A13 | `cli` ordinary inspection tests for saved live, unready, failed and reused terminal identities; `launch`, `prepare` and `review` Herdr adoption tests | Desktop visibility in the Sum space and actual harness attach: not run |
| A14 | CLI development-marker and undesignated-workspace init tests | No production coordination is initialized from the development checkout |
| A15 | `updatecmd` quiescence, alignment and rollback-floor tests; `cli.TestNativeBaseUpdaterSelectsCandidateWithEventsDisabled` | Installed update and next ordinary harness start: not run |
| A16 | CLI schema-one blocker and `cli.TestHistoricalReaderPreservesEvidenceAndRefusesWrites` | Old task and sealed procedure bytes remain historical records |
| A17 | `launch` explicit/default harness selection tests and `cli.TestOfflineDemo` | Authenticated mixed coordinator/worker harness combination: not run |

## Ordinary conversation journey

Start the usual terminal coding agent in an isolated staged Sum workspace and run `./bin/sumctl init`.
It must return `coordinator`, a claim token, procedure paths, and outstanding work without contacting HC or creating another agent terminal.
Retain the returned token in the conversation and pass `--claim TOKEN` to coordinator mutations and collection.
Repeat init with that token and verify the same session and generation.
A separate conversation without the token must receive a conflict.
Transfer only after the user's decision, then prove the previous token refuses before any ledger or native effect and existing task bytes remain unchanged.
On resume, collect before reporting progress.
Reading a question or report twice must neither settle it nor create an application receipt.

Prepare and start two approved tasks with distinct checkouts, labels, and attempt identities.
Verify HC run requests omit parent and caller, create on HC's default route, carry no Herdr labels, and take the explicit or saved worker harness choice; the Herdr space and tab labels appear only after each original program is corroborated.
Record a worker question, answer with the claim from a fresh helper process, and collect while the worker is busy.
The answer must remain pending until eligible, then be delivered once through the worker route during active collection.
Only the matching worker application receipt settles the answer.
Record a candidate report, launch an independent candidate-bound reviewer, and verify its verdict appears in collect with a null task parent.
Run independent verification and perform only user-authorized publication.
Only the user merges except an explicitly authorized factory high-confidence merge contract.

## User-run authenticated Terminal Grok Build journey

Status: not run for this candidate.
The user runs this journey and handles every authentication, trust and permission prompt.
An agent may prepare the isolated files and record the user's results; preparation does not certify any manual step.
Keep A1, A6, A7, A8, A12, A15 and the Terminal Grok Build harness row marked not run until the user supplies the corresponding evidence.
Fresh-clone setup supplies evidence for A2 separately.
Do not count this journey as Codex, mixed-harness, production retirement or production deployment evidence.

1. **Inspect the prepared handoff and start Grok yourself.**
   The handoff must give absolute paths for `SUM_ACCEPTANCE_ROOT` (an independent staged Sum workspace, never the live installation or a development checkout), `ACCEPTANCE_PROJECT` (a disposable Git project with a standardized verification contract), `ACCEPTANCE_BRIEFS` (approved `task-one.md` and `task-two.md`) and `ACCEPTANCE_EVIDENCE` (a private evidence directory outside either tracked tree).
   The prepared `launch.env` supplies these paths under `.artifacts/pr343-repairs/acceptance/{installation,project,briefs,evidence}`; inspect it before loading it into the user-run shell.
   The disposable project needs a local origin/default branch for task base resolution; no external publication is part of this fixture.
   It must identify the candidate SHA, the exact published HC artifact and matched templates/manual, a unique private HC session and scratch runtime/config paths, and the approved Grok worker/reviewer configuration.
   Both briefs must authorize their exact changes, commits and verification, use distinct owned paths, and retain the generated absolute helper callbacks.
   Task one owns `notes/one.md`, explains the fixture's `hello.py` CLI, saves the question in step 5 about including `APPROVED`, continues finite approved independent read/verification work while the answer is pending, applies the answer before its dependent change, and reports its committed candidate.
   Task two owns `notes/two.md`, documents normal and shout examples, runs the existing verification contract, and independently commits and reports its approved change.
   These fixture documentation changes require no new tests; running the existing verification contract remains part of both briefs.
   The operator must have existing authenticated Grok Build access; do not change accounts, models, billing or permission controls for this exercise.
   Missing prerequisites are blockers to record, not instructions to improvise installation or live configuration.
   Start the usual Terminal Grok Build conversation in `SUM_ACCEPTANCE_ROOT` yourself and give it the two approved briefs and this runbook.
   Preserve the exact starting prompt, harness version/default input configuration, candidate identity, workspace paths and HC artifact identity in `ACCEPTANCE_EVIDENCE`.
   Keep claim-bearing transcripts private and redact tokens from public excerpts.

2. **Claim ordinary coordination without launching an HC coordinator.**
   In that Grok conversation, invoke the staged workspace's helper:

   ```sh
   cd "$SUM_ACCEPTANCE_ROOT"
   ./bin/sumctl init --format json
   ```

   Expect `role: "coordinator"`, nonempty `claim`, `session.kind: "coordinator-session"`, `session.session_id`, `session.generation`, `procedure`, `helper`, `home` and `outstanding`.
   Read every validated path returned under `procedure` before coordination.
   Retain the returned token as `CLAIM` in this conversation and use the returned absolute `helper` as `SUM_ACCEPTANCE_HELPER` for subsequent commands.
   No coordinator terminal or authenticated agent is created by ordinary init, and init must neither start nor query an HC core.
   Preserve init stdout/stderr, exit status, the validated procedure paths and visible terminal/core observations.
   Repeat initialization and verify identical `claim`, `session.session_id` and `session.generation`:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" init --claim "$CLAIM" --format json
   ```

3. **Let the user choose the isolated workspace's capacity before preparing tasks.**
   Only the user sets capacity; the agent must never set or raise it automatically.
   Inspect the current settings first:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" settings show --format json
   ```

   Preserve `limits`, `source`, `occupied` and `effective` when present.
   Fresh default limits are two global execution reservations and one per repository, so the second task in this single-repository fixture cannot be admitted under those defaults.
   This sequence retains two worker reservations and runs coordinator verification serially, requiring room for one additional verifier reservation: three global and three per repository when there is no other held work.
   Reports retain worker reservations until conclusive stop evidence releases them.
   Live reviewers are separate native terminals; the current execution reservation ledger counts workers and verifiers, not reviewers.
   The user must also consider the two real reviewer terminals when deciding whether to run this fixture.
   If the user approves these bounded limits for this isolated workspace, the user runs:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" settings set --global 3 --per-repository 3 --claim "$CLAIM" --format json
   "$SUM_ACCEPTANCE_HELPER" settings show --format json
   ```

   Preserve the user's decision and both command responses, verifying `limits.global: 3`, `limits.per_repository: 3`, the actual settings `source`, and the applicable `effective` scope/limit when present.
   A fresh empty workspace can return `effective: []`; repeat `settings show --format json` after preparation to preserve the actual occupied scope.
   Existing project overrides or held reservations must be considered from the returned view; these values do not authorize an automatic adjustment of an override or a further increase.
   If the user declines, or existing effective limits/occupancy do not permit the fixture, leave the dependent worker, delivery, review and verification steps and their acceptance rows not run.
   Do not clear limits, release unproven attempts or edit state to bypass that decision.

4. **Prepare and launch the two approved tasks.**
   Each line below is a separate helper process in the coordinator conversation.
   `--approved` records the user's actual brief approval; it does not grant approval on its own.

   ```sh
   "$SUM_ACCEPTANCE_HELPER" prepare --repo "$ACCEPTANCE_PROJECT" --brief "$ACCEPTANCE_BRIEFS/task-one.md" --harness grok --approved --claim "$CLAIM" --format json
   "$SUM_ACCEPTANCE_HELPER" prepare --repo "$ACCEPTANCE_PROJECT" --brief "$ACCEPTANCE_BRIEFS/task-two.md" --harness grok --approved --claim "$CLAIM" --format json
   ```

   Save each returned `id` as `TASK_ONE` or `TASK_TWO` in the conversation.
   Expect `status: "prepared"`, distinct `id`, `branch` and `worktree`, `parent: null`, `owner.kind: "coordinator-session"`, and `verification_policy.status: "standardized"`.
   Preserve both JSON responses, approved brief bytes and generated `brief_path` files.

   ```sh
   "$SUM_ACCEPTANCE_HELPER" start "$TASK_ONE" --claim "$CLAIM" --format json
   "$SUM_ACCEPTANCE_HELPER" start "$TASK_TWO" --claim "$CLAIM" --format json
   ```

   Preserve each returned task's `worker_endpoint`, `execution.worker` and native launch/first-message records, including distinct attempt, address and creation identities.
   Observe two real authenticated Grok workers in their separate task checkouts on the default core, shown in Herdr in the installation's Sum space under their own task tabs without stealing focus.
   A created terminal or submitted opening message alone is insufficient: each worker must run its brief's absolute helper `init --format json`, read its validated procedure and retain the returned `role: "worker"`, matching `task`, `endpoint`, `registered: true` and native initialization receipt.
   Keep screenshots or terminal transcripts showing actual harness readiness and the matching saved task records.
   If creation or delivery is uncertain, preserve the exact attempt and returned recovery instruction; do not repeat a launch blindly.

5. **Save a worker question and answer while the worker is busy.**
   Only task one's actual worker invokes the following through its brief's installed absolute helper; `TASK_ONE` below is its actual task id, not a new identity supplied by the coordinator.

   ```sh
   "$SUM_ACCEPTANCE_HELPER" ask "$TASK_ONE" --key acceptance-answer --text 'Should the final note include APPROVED?' --format json
   ```

   Expect `question.id`, `question.key: "acceptance-answer"`, `question.status: "open"` and the durable question record.
   Save `question.id` as `QUESTION_ID` in the coordinator conversation after observing it through collection:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" collect --claim "$CLAIM" --budget 20s --format json
   ```

   Expect `items` containing `kind: "question"`, matching `task` and `ref`, plus `detail` and `next` argv.
   Preserve the question JSON, collection JSON and evidence that the worker is doing the brief's finite independent work when the coordinator records the user's answer:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" answer "$TASK_ONE" "$QUESTION_ID" --text 'Yes; include APPROVED in notes/one.md and complete only the approved brief.' --claim "$CLAIM" --format json
   "$SUM_ACCEPTANCE_HELPER" collect --claim "$CLAIM" --budget 20s --format json
   ```

   Expect the answer response's `question.status: "answered"`, matching `question.id`, saved `question.answer`, and `notice`/`feedback` describing the actual delivery outcome.
   Retain pending/busy delivery evidence before the worker becomes eligible and every subsequent collection response.
   Repeat bounded `collect` calls while work is active; each has `items`, `delivered`, `deadline_hit` and `next`.
   A submitted delivery may appear in `delivered`; delivery is not proof of application.
   Once the actual worker receives and applies the answer, it must invoke its brief's absolute helper:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" resolve "$TASK_ONE" "$QUESTION_ID" --format json
   ```

   Expect the returned question's `status: "applied"`, `applied_at` and `application_receipt` for this worker attempt and answer.
   Preserve the before/after task records and worker transcript proving one application, with no coordinator-forged receipt or duplicate application.
   Reading a saved question or answer is never a substitute for that receipt.
   If the worker was already idle when answered, record that limitation and leave busy-worker delivery unproven.

6. **Collect both reports without equating reports with verification.**
   Each actual worker commits its authorized change, then uses its brief's absolute helper to submit its own report:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" report "$TASK_ONE" --text 'Approved task one complete; committed candidate and verification evidence are recorded in this task.' --format json
   "$SUM_ACCEPTANCE_HELPER" report "$TASK_TWO" --text 'Approved task two complete; committed candidate and verification evidence are recorded in this task.' --format json
   ```

   These are two different worker terminals; neither worker reports for the other task.
   Use truthful report text describing the actual checks and limitations, and the brief's `--file`/`--handoff` form if it requires structured evidence.
   Expect each response's matching `task`, `status: "reported-not-verified"` and `evidence` ids.
   In the coordinator conversation, repeat:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" collect --claim "$CLAIM" --budget 20s --format json
   "$SUM_ACCEPTANCE_HELPER" context "$TASK_ONE" --role coordinator --section evidence --format json
   "$SUM_ACCEPTANCE_HELPER" context "$TASK_TWO" --role coordinator --section evidence --format json
   ```

   Expect collected `kind: "report"` items for both task ids and candidate-bound report evidence.
   Save the two actual committed candidate SHAs as `CANDIDATE_ONE` and `CANDIDATE_TWO`.
   Preserve complete reports, handoffs, run artifacts and bounded collect timing/exit results; an immediate outstanding item can legitimately return before the budget expires.
   Neither Done, a completed wait nor a report proves actual process exit or releases capacity.

7. **Obtain independent review and independent verification.**
   From the coordinator conversation, launch a separately accountable Grok reviewer for each reported candidate:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" reviewer launch "$TASK_ONE" --harness grok --claim "$CLAIM" --format json
   "$SUM_ACCEPTANCE_HELPER" reviewer launch "$TASK_TWO" --harness grok --claim "$CLAIM" --format json
   ```

   Preserve `task`, `reviewer`, its candidate/endpoint/attempt identity and actual opening-message outcome.
   Each real reviewer initializes through its returned absolute helper, reads the validated reviewer procedure, inspects the exact candidate independently and records its honest verdict from that reviewer terminal.
   For example, an approval after an actual successful review uses:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" review "$TASK_ONE" --verdict approve --candidate "$CANDIDATE_ONE" --text 'Independent review findings and limitations for the exact candidate.' --format json
   "$SUM_ACCEPTANCE_HELPER" review "$TASK_TWO" --verdict approve --candidate "$CANDIDATE_TWO" --text 'Independent review findings and limitations for the exact candidate.' --format json
   ```

   Use `changes-requested` or `blocked` when warranted; never manufacture approval to complete the script.
   Collect until both candidate-bound `kind: "review"` items appear, and keep the distinct reviewer initialization receipts, verdict evidence and null task parents.
   Run coordinator-owned verification for each candidate through separate detached checkouts:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" verify "$TASK_ONE" --candidate "$CANDIDATE_ONE" --execute --claim "$CLAIM" --format json
   "$SUM_ACCEPTANCE_HELPER" verify "$TASK_TWO" --candidate "$CANDIDATE_TWO" --execute --claim "$CLAIM" --format json
   ```

   Preserve each response's `task`, `evidence`, `verification`, exact candidate, distinct coordinator verification run id, `run.json` and `verify.log`.
   Inspect the actual result; an emitted record can describe failure or a blocker.
   A worker's check or reviewer verdict cannot replace this independent run, and missing evidence remains a blocker rather than an implicit waiver.
   No merge, publication or cleanup is authorized by this acceptance journey.

8. **Restart only the ordinary Grok conversation and resume the retained claim.**
   Preserve the token privately before the user exits and restarts the ordinary coordinator Grok conversation in the same `SUM_ACCEPTANCE_ROOT`.
   Do not close task terminals, replace the HC core or transfer ownership for this step.
   Provide the retained token to the resumed conversation and run:

   ```sh
   "$SUM_ACCEPTANCE_HELPER" init --claim "$CLAIM" --format json
   "$SUM_ACCEPTANCE_HELPER" collect --claim "$CLAIM" --budget 20s --format json
   ```

   Expect `role: "coordinator"`, unchanged `claim`, `session.session_id` and `session.generation`, preserved task/worker/reviewer identities, returned validated `procedure` paths and durable outstanding work.
   Read those procedure paths again and collect before reporting progress.
   Preserve the new conversation transcript and before/after identity comparison.
   A `blocker: "conflict"` is a failed resume to investigate, not authority to use `--transfer`.

9. **Exercise first-use setup in a separate fresh clone under user approval.**
   The handoff must provide `ACCEPTANCE_CLONE_URL`, `ACCEPTANCE_CLONE_REF` and a nonexistent independent `ACCEPTANCE_FRESH_ROOT` for the same approved candidate.
   The user creates that clone and handles repository trust:

   ```sh
   git clone "$ACCEPTANCE_CLONE_URL" "$ACCEPTANCE_FRESH_ROOT"
   git -C "$ACCEPTANCE_FRESH_ROOT" checkout --detach "$ACCEPTANCE_CLONE_REF"
   cd "$ACCEPTANCE_FRESH_ROOT"
   mise trust
   ```

   Start a fresh usual Grok conversation there yourself before running setup.
   Its first helper command is:

   ```sh
   ./bin/sumctl init --format json
   ```

   Expect nonzero exit, `role: null`, `blocker: "unstaged"` and `next` directing approved setup.
   Preserve `detail` too when the helper emits it.
   The agent must request the user's actual permission for setup through the normal permission prompt.
   Only after that approval, run:

   ```sh
   mise run setup
   ./bin/sumctl init --format json
   ```

   Expect successful setup followed by ordinary `role: "coordinator"`, a new claim/session and validated procedure paths, without setup launching an authenticated agent.
   Preserve the first init response/exit, permission request and user approval, setup transcript/exit and second init response.
   A host with preexisting prerequisites does not prove fresh-host prerequisite installation; retain that limitation on A2.

10. **Perform one separately authorized quiescent update, then inspect the next ordinary start.**
    This step is not authorized by preparing the workspace or running the earlier steps.
    The user must identify and approve the exact isolated installation and `ACCEPTANCE_UPDATE_REF`, and the target must be merged on that installation's origin default branch.
    If no such approved target is available, retain A15 as not run and record the missing prerequisite.
    Return to `SUM_ACCEPTANCE_ROOT` and the corresponding retained `CLAIM`/absolute helper, not the fresh clone's claim.
    First inspect status and collect, then use the existing procedure to settle every running, starting or uncertain attempt and unresolved launch, delivery or maintenance effect.
    Do not edit records, forcibly terminate processes, remove task checkouts or fabricate receipts to make the installation quiescent.
    Open questions or unapplied answers alone are not update blockers.
    Preserve the explicit authorization, quiescence evidence and these command results:

    ```sh
    "$SUM_ACCEPTANCE_HELPER" status --format json
    "$SUM_ACCEPTANCE_HELPER" collect --claim "$CLAIM" --budget 20s --format json
    "$SUM_ACCEPTANCE_HELPER" update check --ref "$ACCEPTANCE_UPDATE_REF" --claim "$CLAIM" --format json
    "$SUM_ACCEPTANCE_HELPER" update stage --ref "$ACCEPTANCE_UPDATE_REF" --claim "$CLAIM" --format json
    "$SUM_ACCEPTANCE_HELPER" update apply --ref "$ACCEPTANCE_UPDATE_REF" --claim "$CLAIM" --format json
    ./bin/sumctl update status --format json
    ./bin/sumctl build-info --format json
    git rev-parse HEAD
    ```

    Check/stage must not select the target; inspect their `source`, `staged` and `compatibility` data and actual exit status.
    Successful activation records `changed: true`, `default`, `checkout`, `post_check` and `generation`; an already-selected no-op does not prove an update.
    After selection, compare checkout HEAD, `update status`'s `default`/`active`/`checkout`, `build-info` and next-start entry instructions against the approved target.
    Restart the ordinary Grok conversation yourself and run `./bin/sumctl init --claim "$CLAIM" --format json`, read its returned procedure paths and collect with the retained claim.
    Keep activation journal/history and before/after hashes of task and sealed procedure records.
    If activation is interrupted, retain its exact generation and returned recovery argv including the claim; do not repeat apply or switch runtime links by hand.
    The current rollback floor starts when schema-3 root state is created, even before the first coordinator claim, and also protects logical ownership after a claim or native retirement.
    A target's actual helper must support the required state format and owner schema; an older preclaim helper that can still read earlier state is not a safe downgrade once that floor is raised.
    This ordinary fresh-workspace journey does not execute native retirement or certify universal exclusion of already-authorized process/Git I/O.

11. **Record only user-observed outcomes.**
    Send the agent each numbered step's outcome, command stdout/stderr/exit status, evidence paths and any interruption or limitation.
    The agent records candidate-specific date, identity, evidence and pass/fail/not-run separately for ordinary entry/resume (A1), two workers (A6), busy-worker answer/application receipt (A7), independent review (A8), bounded active collection (A12), update/next start (A15), and first-use setup (A2).
    Update the Terminal Grok Build harness row only for the combinations actually exercised.
    Preserve failures and unrun portions explicitly; local automated checks and a prepared runbook cannot fill them in.

The helper argv and field expectations above come from `go/internal/cli/{root,init,settings,prepare,start,native,collect,reviewer,review,verify,update}.go`, `go/internal/roleinit/{coordinator_session,designated}.go`, `go/internal/prepare/{prepare,native}.go`, `go/internal/ask/ask.go`, `go/internal/collect/collect.go`, `go/internal/report/report.go`, `go/internal/verifycmd/verify.go` and `go/internal/updatecmd/{update,activation}.go`.
The capacity calculation follows `go/internal/settings/{settings,scope}.go`, `go/internal/reservations/reservations.go`, `go/internal/launch/launch.go` and `go/internal/verifycmd/execute.go`; reviewer launches are recorded separately in `go/internal/review/native.go`.
The preclaim downgrade boundary is exercised by `go/internal/updatecmd/first_update_recovery_test.go`.
Revalidate these expectations against the staged candidate before recording a manual pass.

## Native identity, recovery and inspection

Workers and reviewers retain native identity, exact core/address/creation-time checks, attempt receipts, qualified templates, bounded readiness, and conservative uncertain input handling on generation-1 cores at or above 0.1.14.
The bundled artifact remains published HC 0.1.14; acceptance on a newer core requires its own recorded evidence.
A send, Done state, started result or completed wait is not an application receipt, verification result, review or process-exit proof.
Reuse a socket, address or name on a different core and replace a harness process within one terminal; each old identity must refuse.
An unproven shell occupant gains no worker role or automatic prompt.

Lose a worker or reviewer creation reply after durable intent persistence.
Reconcile from that exact intent's saved socket, core, session and labels, including after coordinator claim transfer.
A reconciled original is adopted into Herdr only after its proof; a lost label reply is settled by reading the exact terminal, and a kept replacement shell is refused.
Changing today's default route must not redirect recovery.
Conflicting candidates refuse, and repeating successful reconciliation records the original endpoint without another launch or opening send.
An uncertain input is never automatically replayed.
Inspect live, unready and failed attempts from an ordinary shell and retain their bounded output, saved address and attach argv.
Trust, auth and permission dialogs require the user's action and are never accepted by Sum.

## Harness matrix

| Coordinator conversation | Worker/reviewer | Candidate-specific status |
| --- | --- | --- |
| Terminal Codex | Codex 0.166.0, default keymap, namespaced Tab template over unmodified HC 0.1.14 | Authenticated journey not run |
| Terminal Grok Build | Grok stable 1.0.67 or newer, default input configuration, namespaced Enter template | Authenticated journey not run |
| Terminal Grok Build | Codex | Authenticated mixed-harness journey not run |
| Terminal Codex | Grok Build | Authenticated mixed-harness journey not run |
| Ordinary external scripted caller | Model-free native HC scripted worker and reviewer plus local strict fake GitHub | Passed locally over published HC 0.1.14; `.artifacts/issue-342/ordinary-composition-final.json` records the source digest, checkpoints and teardown |
| Any ordinary conversation | Claude Code 2.1.316 or local Devin CLI 3000.11.234 with default Enter profiles | Offline contracts covered; authenticated journey not run |
| Any ordinary conversation | Other terminal harnesses | Unqualified for automatic input |

Historical Codex Done/busy probes and older scripted application artifacts do not certify this candidate.
The scripted canary proves exercised wiring only, never real agent reasoning or external publication.

## Update and state boundary

An update requires the user's authorization and a valid claim.
Block activation on running, starting or uncertain attempts and unresolved launch, delivery or maintenance effects.
Open questions or unapplied answers alone do not block an otherwise quiescent update.
Dirty or divergent installation instructions must refuse before selection, preserving all work.
After an allowed update, checkout HEAD, selected helper and entry instructions must agree for the next ordinary agent start.

The one-time native cutover uses the old helper's native-event disable operation before its existing update procedure.
The candidate advertises preserved sidecar protocol support so the old updater can select it without manifest repair.
Then explicitly retire the native coordinator with `init --retire-native`.
Retirement raises the root state pointer format to 3 under the existing locks, fencing old writers before publishing logical authority.
Generation headers remain format 2, and historical task, native owner and sealed procedure bytes remain unchanged.
`cli.TestNativeRetirementFencesPreviouslyBuiltHelper` builds the immutable native-only base helper and checks fresh, physical-generation and paused-authorization writer refusal.
This is ledger and native-send exclusion evidence, not universal OS or Git I/O exclusion for a command already authorized before retirement.
Rollback below the logical-owner/state-format floor must refuse, including a crash after the pointer fence but before session publication.
No production retirement, deployment or migration is certified by development tests.
Schema-one state remains preserved and blocked rather than converted.

## Services, capacity, cleanup and records

Retain exact task-local service/process ownership, ports and readiness records.
Stop sends one interrupt only to a proven owned instance, requires actual exit and no survivors, and never escalates to kill.
Reports and uncertain processes retain capacity until exact stopped-attempt proof permits park.
Cleanup blocks dirty, untracked, unmerged or active work, pending obligations and unproven shared processes.
Its maintenance barrier and final reinspection prevent races.
Remove only proven owned worker/reviewer terminals and verified worktrees through the guarded APIs; repeat completion as a no-op.
A coordinator conversation has no protected parent terminal.

Restore a records-only backup into a nonexistent isolated destination and compare task and pinned procedure hashes.
The restored instance is inspection-only: old owner, endpoints and sessions remain historical evidence and confer no live authority.
Reject unsafe archive paths, links, duplicates, unsupported state and corrupt trailers without overwriting a destination.
Preserve unrelated work, MCP configuration and the separate Grok Bot product.

## Evidence and release boundaries

Attach the canonical aggregate once for the final candidate, independent policy review, isolated native canary when available, and the explicit authenticated matrix above.
Record each unrun scenario as not run.
Local checks, hosted CI, merge, deployment and production acceptance are separate facts.
Historical `VALIDATION.md` and `test-results.txt` are dated receipts, not current certification.

## Local native evidence for issue 342

The isolated identity and runtime smoke checks passed using the existing published HC 0.1.14 binary.
Their records are `.artifacts/issue-342/native-proof-incarnation.json` and `.artifacts/issue-342/native-proof-smoke.json`.
The ordinary external conversation composition passed with a scripted worker and reviewer and local strict fake GitHub.
Its record is `.artifacts/issue-342/ordinary-composition-final.json`; the source Go digest was checked against the development tree after the run.
All three records report successful scratch-core shutdown.
These are local model-free records; authenticated harness combinations, hosted publication and production retirement remain unrun.
