# Grok instruction repair evidence

## Scope and candidate

Issue [#354](https://github.com/douglasjarquin/sum/issues/354) requests intentional repair of the two Grok installers and their native Bot packs.
The clean baseline is `20995d504ef7c80eb1b35a87be2c48a42b6c65f8`.
The developer branch is `fix/grok-native-contracts-354`; the PR head identifies the final committed candidate.
The user explicitly invoked lfg and clarified the repository developer role in this API session.
No production Bot, account, routine, backup destination, or private data was modified.

The implementation scope explicitly includes the minimum affected existing assertions in `test_instruction_package.py`, `test_operating_files.py`, and `test_coordinator_invariants.py`, including the missing Factory inventory.
The original immutable exclusion deliberately protected the Grok pack during the HC refactor; #354 intentionally supersedes that exclusion for its named Markdown sources.
The maintained test still pins all unrelated baseline files, avatars, eval fixtures, decision schema, and both legacy factory settings to `9456017f2e4fde8f16bc4f2d1bdd7ba978765eb3`, and preserves the complete Bot file inventory assertion.
No new tests, fixtures, helpers, code comments, or runtime were added.
The four local HC procedures, their entry instructions, and the legacy factory settings are unchanged.

## Diagnosis and baseline characterization

The installer writes personal descriptions and assumes ownership by display name, while current documentation uses personal chat/memory and managed shared descriptions.
The installer update list omits role memories, existing worker adoption, Square, and routine prompts; a fetched SHA therefore cannot establish consistent adoption.
Deliver invokes an unavailable terminal reviewer helper even though the native Bot install never supplies it.
Persist counts task directories to allocate IDs, so a gap can collide with an existing task; saving an answer does not notify the waiting worker.
The worker saves a question without an explicit question notification obligation.
The action log retains an intended row before a failed row, yet deduplicates on either historical intended or done rows, blocking retry after failure.
Counting those rows also counts one successful operation twice toward the daily cap and omits task identity from dedupe.
Square skips when clean after staging, so a committed but unpushed backup never reaches the retry path.
Its shared instructions also embed a private destination and unbounded workspace staging.

A baseline `rg -n 'reviewer launch|counting existing|six hours|git add -A|Skip only|grokbot|two of those' GROK_SUM.md grok-bots` capture is retained locally under `.artifacts/issue-354/before.txt`.
The existing 31 operating-file checks passed before repair; they did not detect these contract contradictions.
This is a source-level characterization, not a live red/green execution.
Authenticated Grok tools, installed Bot identities, account approval, and an isolated private backup destination are unavailable in this API session, so an end-user E2E reproduction could not run.
The issue explicitly permits completing reviewable instruction changes while leaving native acceptance outstanding.

## Official platform context

Consulted on 2026-11-05:

- [Bots](https://docs.x.ai/grok-bot/bots): personal role changes use chat/memory; managed shared descriptions are distinct; shared Bots cannot be primary.
- [Shared Bots](https://docs.x.ai/grok-bot/shared-bots): owner/Manager configuration authority and shared access boundaries.
- [Skills/routines](https://docs.x.ai/grok-bot/skills-routines-and-automations): account-wide skills and routine behavior.
- [Messages](https://docs.x.ai/grok-bot/chat-and-collaboration): native conversations and ongoing task interaction.
- [Computer/apps](https://docs.x.ai/grok-bot/computer-and-apps): shared files, browser sessions, and CLI credentials.
- [Vault](https://docs.x.ai/grok-bot/vault): account-wide Vault and separate secure-entry/shared-Bot Secrets surfaces.

Public guides establish context, not observed native tool schemas or live installation qualification.
The recipes require discovery and recorded capability mapping in the actual deployment.

## Prescribed check inventory

| Category | Setup/readiness and command | Required outcome and limits |
| --- | --- | --- |
| Readiness | Pinned Go/Python/Node/aube; `MISE_ENABLE_TOOLS=go,python,node,aube mise tasks ls`; `MISE_ENABLE_TOOLS=go,python,node,aube python3 .agents/skills/verify/scripts/verify_run.py --check` | Root-owned verify resolves and prerequisites validate; a dirty result is provisional |
| Targeted contracts | `python3 scripts/fixture_git_env.py -- python3 -m unittest discover -s tests -p 'test_operating_files.py'`, corresponding instruction-package and coordinator-invariant invocations | 31 operating, 6 package, 4 invariant checks; source/fixture evidence only |
| Canonical aggregate | `MISE_ENABLE_TOOLS=go,python,node,aube python3 .agents/skills/verify/scripts/verify_run.py --base 20995d504ef7c80eb1b35a87be2c48a42b6c65f8` after commit | One final-candidate run per verification role; preserve candidate identity, verify.log, JSON result, and manual scenario statuses |
| Aggregate internals | `mise-tasks/verify` runs isolated Go portable tests, all retained Python tests, frozen web install, Astro check, web unit tests, build, and ten route file assertions | Aggregate coverage, not separately observed per-stage executions |
| Lint | `MISE_ENABLE_TOOLS=go,python,node,aube mise run lint`; `git diff --check` | Go format/vet and whitespace checks |
| Policy maintenance | Existing `.agents/skills/maintain-verification/scripts/verify_audit.py --base` interface | Preserve base policy protection; independently review changed map and assertion scope |
| Website E2E | Existing hosted website E2E workflow; local changed-route browser test is not applicable | No website source, rendered routes, or visual assets changed; report hosted state separately |
| Independent review | Frozen candidate against the base, issue requirements, existing policy, exact assertion diff | Distinct reviewer report; not authenticated Grok native review acceptance |
| Hosted CI | Existing verification and website workflows for the PR head | Record actual workflow/gate status and URLs; never infer from a local aggregate |
| Native scenarios | Every issue #354 matrix row, detailed in both pack READMEs | Not run without authenticated isolated deployment; never infer from static checks |
| HC regression acceptance | Existing manual rows in docs/ACCEPTANCE.md and docs/features/native.md | Not run in this API developer session; HC source procedures unchanged |

Teardown preserves reports/logs and unfinished work, introduces no intended services, and never restarts a shared core or removes foreign processes.
Local raw artifacts are retained under `.artifacts/issue-354/` and `.artifacts/verification/`; those paths are not published evidence links.

## Action-log walkthroughs

These are reasoned applications of the new schema, not executed integrations or new fixtures.

| History | Required next step |
| --- | --- |
| Task A / dispatch / worker X / brief revision R, operation O1, attempt A1: intended then confirmed failed | Retry O1 with A2 within scope/budget; A1's latest state is failed, so its earlier intended row cannot block the retry |
| O1 / A1: intended, acknowledgement lost | Read the actual native/external result first; record done if found, failed only if proved absent, otherwise remain uncertain and stop that operation |
| O1 / A1: intended then done, repeated message tomorrow | Append skipped-duplicate with the receipt; elapsed time does not replay success |
| Task B / dispatch / worker X / brief R | Different task means a different operation identity; sharing a worker cannot collapse tasks |
| O1, O2, O3 each intended then done on the same UTC day for X | Three logical actions, not six rows; a fourth needs the user's actual decision |
| O1's failed attempt retries as A2 | One logical action plus another attempt against its separate repair budget; no extra scope is granted |
| New approved scheduled occurrence or explicit user-authorized repetition | New operation ID with its occurrence/decision reference; duplicate native messages for the old occurrence retain the old ID |

## Actual verification and limitations

Readiness and targeted results, final aggregate, lint, maintenance audit, review, and hosted CI are recorded in the PR pipeline once actually run.
Manual acceptance remains Not run for every row of issue #354: fresh personal/shared setup, repeat installation, complete/partial update, specialist reuse, question round trip, empty/failed result, safe retry, lost acknowledgement, shared worker identities, daily cap, allocation after gaps, cancellation, native code review/delivery, non-code delivery, scope conflict, failed backup push, scoped restore, and unsupported dependencies.
The isolated backup/restore steps use synthetic data and explicit user-chosen destinations; this implementation does not perform them.
No live workflow, verbatim memory retention, exactly-once execution, deployment readiness, or remote backup success is claimed.

## Developer review and corrections

The initial committed candidate `6bedc88a12710cfea03199cbbab6f03134393a8b` passed the canonical aggregate, all 73 Python checks, portable Go checks, Astro checks, four website unit tests, build/route assertions, lint, and the maintenance audit.
The run reported 51 automated mapped scenarios passing and 24 manual scenarios not run, with no self-certification because the Bot coverage map changed.

Independent developer review and validation confirmed six source-contract gaps: originating conversation/return route, specialist report path, isolated Cloud Agent context/run identity, non-code branching, first-time Square creation, and adoption of unrecorded legacy skills/routines.
The corrected instructions explicitly record those identities and routes, consume the worker-owned report_destination, branch before code-only prerequisites, and provide safe user-confirmed adoption and first-time creation steps.
Square also requires the approved scope manifest in the remote backup commit so restore verification does not depend on the lost original computer.
Workers wait for a correlated pre-effect logging/authority acknowledgement, and Dispatch explicitly queues rather than overwrites a worker's active assignment.
These corrections fill the approved issue requirements and preserve permission restrictions; they do not grant live deployment authority.
The final corrected candidate's refreshed checks and separate follow-up review are reported in the PR rather than inferred from the initial run.
Historical invariant seed coverage remains limited to its retained traces; it does not execute the new operation/attempt recovery procedure.
