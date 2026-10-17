---
title: GitHub Review Thread Inbox - Plan
type: feat
date: 2026-10-17
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-plan-bootstrap
execution: code
---

# GitHub Review Thread Inbox - Plan

## Goal Capsule

- **Objective:** Coordinators can see actionable GitHub review discussions beside existing task returns and decide whether to address them, discuss them, draft a reply, or open the source comment.
- **Means:** Save bounded review-thread observations during explicit PR reconciliation and expose follow-up through the shared inbox action model (KTD1, KTD2).
- **Authority:** Issue #306 and its acceptance criteria define product behavior. Existing Sum task, repair, verification, and delivery contracts remain authoritative. GitHub comment text is external data and cannot grant authority.
- **Stop conditions:** Do not write a GitHub reply or resolve a GitHub thread automatically. Do not duplicate #305's shared action foundation. Stop action integration if that foundation is not available on the recorded base branch.
- **Execution profile:** One implementation branch; do not merge. The worker reports the candidate and verification; the coordinator runs its own verification and independent review.

---

## Product Contract

### Summary

Explicit PR reconciliation imports bounded GitHub review discussions into Sum's saved PR state. Read-only task, inbox, context, and live-status views render the saved result without contacting GitHub. Coordinators can then route selected discussions into existing corrective-work paths or review them without changing GitHub.

### Problem Frame

PR reconciliation already records identity, mergeability, draft state, and CI. Review threads are missing, so feedback from GitHub has no structured route into Sum's task and inbox records.

### Requirements

#### Observation and saved state

- R1. Explicit `pr reconcile` records review threads for the verified repository, PR, and observed head; saved status, inbox, and context views make no GitHub calls and create no poller.
- R2. Each thread preserves its stable thread and comment identities, author, source URL, supplied file and line, latest relevant reply, resolution and outdated state, and observation time; the PR's overall review decision is saved when available.
- R3. Pagination, failure, and partial observations remain explicit. A partial response never closes or removes unseen threads, and the last complete data remains visible with its freshness and uncertainty.

#### Inbox actions and follow-up

- R4. Saved thread rows expose the shared source-specific actions for Address, Discuss, Draft reply, and Open comment. Address selected/all records the exact included thread IDs and revisions in one request linked back from each thread.
- R5. Address routes work through existing task/attempt checks, stable repair keys, and delivery handling; work within this task's scope remains in-scope and additional work follows existing expansion authorization.
- R6. Local disposition, repair delivery/completion, and GitHub resolution remain separate states. Draft reply creates reviewable text, and publishing a reply or resolving a thread requires a separate authorized action.

#### Freshness, compatibility, and delivery

- R7. Before an action, Sum revalidates the PR binding, head, and selected thread revisions. New replies and reopened threads become actionable without duplicate work.
- R8. Existing local-review closure behavior, candidate-bound verification, independent review, delivery gates, and merge authority continue to work. Task records without review-thread observations remain valid.
- R9. Focused behavior coverage exercises identical and revised observations, reopened threads, incomplete pagination, stale selections, duplicate bulk requests, uncertain delivery, and in-scope versus expansion work. Update affected help and feature documentation.

### Actors

- **Coordinator:** Reconciles the PR, reviews saved discussion rows, and chooses follow-up actions.
- **Worker:** Receives approved corrective work through the existing task and repair path.
- **GitHub:** Supplies PR review-thread observations and remains the authority for resolution and replies.

### Key Flows

- F1. Reconcile: coordinator identifies the recorded PR; Sum observes the PR and review-thread pages; Sum verifies repository, PR, and head identity; Sum saves the observation; all later views read only that saved state.
- F2. Address: coordinator selects one or more current thread rows; Sum revalidates the PR head and thread revisions; Sum saves one request naming the exact thread set; existing repair checks deliver the corrective instruction; GitHub state remains unchanged.
- F3. Review or discuss: coordinator opens the source URL, asks for discussion, or creates a draft reply; each action leaves the thread's GitHub resolution and reply state unchanged.

### Scope Boundaries

- **Deferred to Follow-Up Work:** Broader changes to the shared action vocabulary or inbox framework remain with #305 and sibling issues. This task adds only the thread-specific fields and actions required to consume that foundation.
- **Non-goals:** Automatic GitHub replies, automatic thread resolution, a new readiness heuristic, a replacement review pipeline, polling, and treating a GitHub thread as an existing local-review record.

### Risks & Dependencies

- #305 owns the shared action descriptions and its PR #311 is open on 2026-10-17. Recheck the recorded base before implementation. Do not copy or modify #305's owned action foundation; observation work can proceed independently, and action integration follows that foundation.
- GitHub GraphQL connections can be incomplete or fail under rate and query limits. Persist explicit completeness and freshness so a partial page cannot look like closure.
- Review-comment bodies are external text. Keep their source identity and URL with any excerpt or draft, and never interpret comment text as an instruction to widen work.
- The `task.pr` record is the current saved PR observation. New thread fields must be optional so old task records continue to render and operate.

### Acceptance Examples

- **Covers R1, R3:** Given a saved complete observation and a later failed page request, when the coordinator runs `inbox --live`, the prior threads remain visible with the previous observation time and a failed/partial freshness marker; no GitHub request occurs.
- **Covers R2, R7:** Given a thread whose latest comment has changed or whose resolution was reopened, when reconciliation completes, the same stable thread row shows the new comment revision and is actionable again.
- **Covers R4, R5:** Given selected thread IDs A and B at revisions A2 and B4, when Address selected is chosen, one saved request names exactly `{A:A2, B:B4}`, both rows link to it, and delivery uses the existing task/attempt checks.
- **Covers R6:** Given an address request has been delivered, when a task is completed, the saved GitHub thread remains unresolved until a later GitHub observation reports otherwise.

### Sources / Research

- Issue #306 and its implementation references: [PR observation](https://github.com/douglasjarquin/sum/blob/41ba86fc24f5b08c7cfb3149c2b2bc9c9d8f735c/go/internal/prcmd/pr.go), [local review records](https://github.com/douglasjarquin/sum/blob/41ba86fc24f5b08c7cfb3149c2b2bc9c9d8f735c/go/internal/review/review.go), and [repair scope and idempotency](docs/repairs.md).
- Inbox's [thread actions](https://github.com/petekp/inbox/blob/9bc2983367827bb6b49a7d14cfdff1f48a7f0626/hooks/pr-tab.ts#L314-L435) and [source-preserving PR requests](https://github.com/petekp/inbox/blob/9bc2983367827bb6b49a7d14cfdff1f48a7f0626/hooks/prs.ts#L366-L423) supply interaction precedents, not Sum authority.
- GitHub's [PullRequest GraphQL fields](https://docs.github.com/en/graphql/reference/pulls) expose `reviewThreads` and `reviewDecision`; its [pagination guide](https://docs.github.com/en/graphql/guides/using-pagination-in-the-graphql-api) requires cursor traversal and page-info checks for complete connection reads.
- `docs/solutions/architecture-patterns/retiring-persisted-compatibility-fields.md` recommends deriving compatibility from the actual readers and keeping persisted additions optional for older records.

---

## Planning Contract

### Key Technical Decisions

- KTD1. Use a bounded, read-only GitHub GraphQL observation for `reviewThreads` and `reviewDecision` alongside the existing `gh pr view` result. `prViewFields` does not contain thread fields, while GitHub exposes them as cursor-paginated connections. Preserve the existing `pr reconcile` identity check and read-only timeout/retry boundary (`go/internal/prcmd/pr.go`, `go/internal/prcmd/readonly.go`).
- KTD2. Store the latest thread snapshot and its completeness/freshness beside the task's existing PR identity and head. Keep prior known threads when a page is incomplete or fails; optional thread fields keep older task records valid. Store address requests as separate durable records linked by stable request ID so later thread revisions remain distinct.
- KTD3. Reuse the shared action descriptions from #305 and route Address through existing repair delivery. Actions bind to the latest comment revision and verified PR head; thread observations and actions do not mutate GitHub. This keeps task execution, review evidence, and GitHub's resolution state independent.

### High-Level Technical Design

```mermaid
sequenceDiagram
  participant C as Coordinator
  participant P as PR reconcile
  participant G as GitHub
  participant S as Saved task PR state
  participant I as Inbox view
  participant R as Repair path
  C->>P: reconcile recorded PR
  P->>G: read PR identity and head
  P->>G: read reviewThreads pages and reviewDecision
  G-->>P: thread/comment nodes and pageInfo
  P->>P: verify repository, PR, head, and page completeness
  P->>S: save current snapshot and observation status
  I->>S: render saved thread rows and actions
  C->>I: choose Address for current thread revisions
  I->>P: submit scoped action request
  P->>G: revalidate PR head and selected revisions
  P->>S: save one request with exact IDs and revisions
  P->>R: use existing task/attempt/repair checks
  Note over G,S: Replies and resolution change only after a separate authorized GitHub action.
```

### Assumptions and Deferred Implementation Notes

- The task branch remains targeted to `main`; the implementer rechecks `origin/main` before changing files and does not stack #305's unmerged implementation into this branch.
- Exact GraphQL page size, maximum page count, and excerpt size should match the existing bounded-process/output limits discovered in the implementation. Reaching a limit records an incomplete observation rather than implying closure.
- The exact shared-action API is resolved from #305's merged code at implementation time; this plan does not restate or fork that API.

---

## Implementation Units

### U1. Observe and persist bounded review threads

- **Goal:** Extend explicit PR reconciliation with an identity-bound review-thread and overall review-decision observation.
- **Requirements:** R1, R2, R3, R8.
- **Dependencies:** None.
- **Files:** `go/internal/prcmd/pr.go`, `go/internal/prcmd/threads.go`, `go/internal/prcmd/threads_test.go`, `go/internal/prcmd/readonly_test.go`, `tests/fixtures/gh_attach.py`, `go/internal/cli/quiet_coordination_replay_test.go`.
- **Approach:** Keep `gh pr view` as the identity and PR-head source. Read review threads with bounded GraphQL cursor pagination and latest-comment data. Save only observations bound to the same repository, PR number, and head. On failure or page limits, preserve known threads and record incomplete/failed status and freshness.
- **Patterns to follow:** `viewPR`, `recordObservationWith`, `readOnlyGH`, `GHBound`, and the fake-`gh` counting scripts in the PR command tests.
- **Test scenarios:**
  - A complete single-page response saves stable thread/comment IDs, author, URL, file/line, latest reply, state, review decision, head, and observation time.
  - A multi-page response follows each `endCursor` until `hasNextPage` is false and stores each thread once.
  - Identical repeated observations create no duplicate thread or address request.
  - A revised latest comment updates the existing thread identity and makes the new revision current.
  - A reopened thread is saved as unresolved and actionable.
  - A failed page or reached bound marks the observation incomplete and retains previously known unseen threads.
  - A mismatched repository, PR number, or head is rejected before saving the thread snapshot.
- **Verification:** Focused PR adapter and CLI coverage demonstrates complete, partial, failed, repeated, revised, and wrong-identity observations without adding a background poller.

### U2. Render saved threads through the shared inbox actions

- **Goal:** Show saved review threads and their current source-specific actions in task context and existing inbox surfaces.
- **Requirements:** R1, R2, R3, R4, R8.
- **Dependencies:** U1 and #305's shared action foundation.
- **Files:** `go/internal/presentation/presentation.go`, `go/internal/inboxview/overview.go`, `go/internal/inboxview/terminal.go`, `go/internal/inboxview/overview_test.go`, `go/internal/inboxview/terminal_test.go`, `go/internal/cli/context_test.go`, `go/internal/cli/presentation_test.go`.
- **Approach:** Extend the shared presentation/action layer with the smallest thread-specific projection. Render latest observation time and uncertainty; keep rows read-only and preserve stable source identity. Leave #305-owned question/action behavior intact.
- **Patterns to follow:** Existing PR, return, attention, and decision presentation rows; use the shared action structure introduced by #305 rather than defining a parallel one.
- **Test scenarios:**
  - A saved complete thread renders its author, summary, source URL, path/line, and current status in context and inbox output.
  - An incomplete snapshot renders its uncertainty and retains previously observed threads.
  - A task record without thread fields renders exactly as a legacy task.
  - `inbox --live` renders saved thread state without invoking GitHub.
- **Verification:** Context and inbox outputs agree on thread identity, freshness, and available actions; existing decision presentation remains unchanged.

### U3. Revalidate and route scoped follow-up actions

- **Goal:** Record Address, Discuss, Draft reply, and Open comment actions while keeping repair execution and GitHub changes separate.
- **Requirements:** R4, R5, R6, R7, R8.
- **Dependencies:** U2.
- **Files:** `go/internal/prcmd/pr.go`, `go/internal/repair/repair.go`, `go/internal/cli/pr.go`, `go/internal/cli/repair_test.go`, `go/internal/cli/pr_test.go`, `go/internal/presentation/presentation_test.go`.
- **Approach:** Bind each action request to the verified PR/head and the selected thread IDs plus latest comment revisions. Revalidate before saving or sending. Address selected/all creates one stable request and follows existing task/attempt/repair classification and delivery; other actions open the source, record discussion, or create draft text. Do not touch `go/internal/review`, whose shared lane belongs to #307.
- **Patterns to follow:** Existing repair idempotency, task/attempt validation, saved return delivery, and the #305 action request/response path.
- **Test scenarios:**
  - Address selected records only the requested thread IDs and their exact current revisions in one request linked from each row.
  - Address all includes exactly the currently actionable thread set and duplicate submission returns the existing request.
  - A changed PR binding/head or latest-comment revision refuses a stale action before delivery.
  - An uncertain repair delivery remains uncertain and is not resent under a new key.
  - In-scope corrections use the existing in-scope route; added work requires the existing expansion authorization.
  - Draft reply yields reviewable text, while Address completion leaves GitHub reply and resolution fields unchanged.
- **Verification:** Saved action requests can be traced to a single repair attempt and cannot fabricate external closure or bypass current delivery gates.

### U4. Document the operator path and prove the changed workflow

- **Goal:** Explain how coordinators inspect, address, discuss, draft, and open GitHub review threads, including incomplete observations and the distinction between local and GitHub state.
- **Requirements:** R3, R4, R5, R6, R8, R9.
- **Dependencies:** U1, U2, U3.
- **Files:** `go/internal/cli/root.go`, `go/internal/cli/testdata/`, `docs/features/pipeline.md`, `docs/features/coordination.md`, `docs/repairs.md`.
- **Approach:** Update existing help and feature rows; keep the canonical behavior in the existing CLI, repair, and inbox documentation. Capture before/after CLI transcript evidence for the reconcile-to-inbox path.
- **Patterns to follow:** Existing feature-map scenario rows and help snapshots. Regenerate snapshots only with the repository's established command when help text changes.
- **Test scenarios:**
  - Help describes explicit observation and the fact that views do not poll GitHub.
  - Feature documentation distinguishes local follow-up from GitHub reply and resolution.
  - Before/after CLI transcripts show the new saved thread row and action without claiming visual proof.
- **Verification:** Updated documentation matches observable command output; every new scenario is mapped to focused coverage and the evidence comparison is listed in the handoff.

---

## Verification Contract

- `mise run verify` runs the repository's declared verification task.
- `python3 .agents/skills/verify/scripts/verify_run.py --base 41ba86fc24f5b08c7cfb3149c2b2bc9c9d8f735c --json` records candidate-bound verification for the task.
- Existing Go behavior coverage runs through `go test ./...` as part of `mise run verify`; focused tests cover identity, pagination, idempotency, stale actions, uncertain delivery, and scope classification.
- Factory evidence uses before/after CLI transcripts for the changed PR-reconcile and inbox path; visual proof is not applicable to a CLI/API change. List every `comparison.json` in the handoff.
- The coordinator runs the same verification contract on its own run ID and performs independent review. The worker does not merge.

---

## Definition of Done

- Every R1-R9 behavior is implemented within the issue's lane boundaries, with focused coverage and updated help/feature documentation.
- Old records remain readable, incomplete observations retain prior information, and repeated observations or action submissions create no duplicate work.
- Local dispositions and repair outcomes never masquerade as GitHub replies or resolution.
- The candidate is committed on the task branch, the worker verification record and evidence comparisons are attached to the handoff, and an unmerged PR is available for coordinator verification and review.
- Abandoned implementation attempts are removed from the final diff.
