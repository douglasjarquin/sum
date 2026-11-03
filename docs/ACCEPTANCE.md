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
| A5 | `hcboot` workspace route tests | Real private HC reuse and changed-core refusal: not run for this candidate |
| A6 | `prepare` parentless native launch tests and `cli.TestOfflineDemo` | Two authenticated workers in distinct checkouts: not run |
| A7 | `collect` answer delivery tests and `cli.TestExternalCoordinatorLifecycle` | Grok and Codex busy-worker answer delivery through active collection: not run |
| A8 | `review.TestCandidateBoundReviewerEvidenceDischargesAmbiguousKickoff`, `cli.TestExternalCoordinatorLifecycle` | Authenticated independent reviewer: not run |
| A9 | `execution` and `review` saved-route reconciliation tests; CLI lost-creation tests | Real transport interruption: not run for this candidate |
| A10 | `cli.TestExternalCoordinatorLifecycle` and retirement preservation tests | Existing installation retirement and resumption: not run |
| A11 | Coordinator claim conflict, transfer, race and stale-generation tests | Another live conversation must not share the recorded token automatically |
| A12 | `collect` shared-budget and terminal-wait tests | Authenticated bounded waiting in Grok and Codex: not run |
| A13 | `cli` ordinary inspection tests for saved live, unready, failed and reused terminal identities | Desktop visibility and actual harness attach: not run |
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
Verify HC run requests omit parent and caller, reuse the workspace route, and take the explicit or saved worker harness choice.
Record a worker question, answer with the claim from a fresh helper process, and collect while the worker is busy.
The answer must remain pending until eligible, then be delivered once through the worker route during active collection.
Only the matching worker application receipt settles the answer.
Record a candidate report, launch an independent candidate-bound reviewer, and verify its verdict appears in collect with a null task parent.
Run independent verification and perform only user-authorized publication.
Only the user merges except an explicitly authorized factory high-confidence merge contract.

## Native identity, recovery and inspection

Workers and reviewers retain published HC 0.1.14 native identity, exact core/address/creation-time checks, attempt receipts, qualified templates, bounded readiness, and conservative uncertain input handling.
A send, Done state, started result or completed wait is not an application receipt, verification result, review or process-exit proof.
Reuse a socket, address or name on a different core and replace a harness process within one terminal; each old identity must refuse.
An unproven shell occupant gains no worker role or automatic prompt.

Lose a worker or reviewer creation reply after durable intent persistence.
Reconcile from that exact intent's saved socket, core, session and labels, including after coordinator claim transfer.
Changing today's workspace route must not redirect recovery.
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
