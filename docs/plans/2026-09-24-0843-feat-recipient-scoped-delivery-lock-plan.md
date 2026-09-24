---
title: Recipient-scoped delivery serialization without duplicate prompts - Plan
type: feat
date: 2026-09-24
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-plan-bootstrap
execution: code
origin: https://github.com/douglasjarquin/sum/issues/209
---

# Recipient-scoped delivery serialization without duplicate prompts - Plan

## Goal Capsule

- **Objective:** A worker or coordinator waiting on a saved return gets its notice promptly even while another sum operation is stuck prompting a different, slow or unreachable pane, and no recipient is ever prompted twice for the same return because two operations raced or spelled its machine differently.
- **Means:** Replace the one exclusive delivery lock held across Herdr I/O with a shared compatibility lock plus an exclusive per-recipient lock keyed by canonical recipient identity (KTD1, KTD2), visited contention-last within the existing #216 pass budget (KTD3), with a state-locked claim that revalidates before the prompt and before recording (KTD4) under an enforced lock order (KTD5).
- **Authority:** Issue #209, then this plan, then `VERIFY.md` and the feature maps.
- **Stop:** No daemon, generic queue, lease service, exactly-once claim, automatic retry of uncertain attempts, or unbounded fan-out. Do not touch #207 (pane incarnation) or #210 (legacy notice mirrors). Do not add a second subprocess runner (`proc.RunContext` stays the only one). Keep #202 stable/legacy machine equivalence. Do not drop `.deliver.lock` while older runtimes may still take it exclusively.
- **Execution profile:** Standard-to-deep Go change in `go/internal/store` and `go/internal/returns` (plus the one `repair.Send` caller), proven by multi-process tests against a scripted fake Herdr, with doc and feature-map reconciliation and the canonical verifier.
- **Who finishes:** The implementer lands, verifies, opens the PR, and (granted for this run) squash-merges after CI is green. The issue's "human merge" boundary is a sum product behavior this change must not weaken (tasks sum delivers still end at a human merge); it does not govern this PR.

---

## Product Contract

### Summary

Delivery passes stop serializing on one global lock. Each recipient pane (canonical machine, session, pane) has its own exclusive lock, so two sum processes delivering to unrelated recipients proceed in parallel, and two delivering to the same recipient still take turns. The old `.deliver.lock` stays as a compatibility lock that new runtimes take shared and older runtimes take exclusive, so mixed versions stay fully serialized with each other. The durable in-flight record, uncertainty rules, coalescing, and manual retry are unchanged.

### Problem Frame

`go/internal/returns/pump.go` `deliver` takes `s.DeliveryLockContext` and holds it, via `defer`, through revalidation, Herdr `agent list`/`agent get`, the in-flight stamp, `agent prompt` (up to `PromptTimeout` plus pipe grace), and outcome recording. A second process (a worker's `report` notice, a hook event, a coordinator `pump`) that wants to notify an unrelated recipient waits behind that external call and, when its budget ends, is `deferred`. One hung pane therefore delays every return in the installation. #216 bounded that wait and made it visible; it did not narrow the lock.

### Requirements

**Concurrency**

- R1. Two separate processes delivering to unrelated recipients do not wait for each other's Herdr observation or prompt; B completes while A's prompt is still held.
- R2. Concurrent operations targeting the same recipient make at most one prompt per open obligation, including when the recipient's machine is recorded under the stable ID in one task and a legacy hostname alias in another.
- R3. Lock acquisition is bounded by the caller's deadline (the pass budget for the pump, a fixed bound for `repair send`); a busy recipient defers only itself.

**Safety**

- R4. The in-flight attempt is persisted before any possible submission; a crash before it leaves the obligation pending, a crash after it leaves it `uncertain`, and nothing replays an uncertain attempt automatically or clears it by age or PID disappearance.
- R5. Immediately before the in-flight stamp, under the state lock, each sendable obligation is re-read and must still be open, still routed to the same canonical recipient and cwd, and the recipient still registered in its role; any change sends nothing.
- R6. Outcome recording re-reads the records under the state lock: an attempt already stamped in-flight is always finalized; a no-effect outcome and the legacy notice mirror are written only for obligations still open and still routed to that recipient.
- R7. A documented lock order (compatibility, then recipient, then state) is enforced so an out-of-order acquisition fails immediately instead of deadlocking, and the test suite exercises that refusal.

**Compatibility**

- R8. An older runtime that takes `.deliver.lock` exclusively and a new runtime serialize completely in both directions.
- R9. Inline notices, explicit `notice` retry, refresh delivery, one coalesced notice per recipient per pass, fixed record-pointer text, submitted-not-acknowledged semantics, and `stalled`/`uncertain` rules behave as before.
- R10. Lock wait and independent-recipient latency are measured with deterministic fixtures and reported; the feature maps describe what is actually exercised.

### Success Criteria

- In the two-process fixture with A's prompt held for most of the helper's `PromptTimeout` (seconds, set by the test), B's pass finishes `submitted` in well under one second of lock wait (baseline before the change: B waits for A or is `deferred` at its budget).
- The pass output's `fanout` carries `lock_wait_ms` so contention is visible without a profiler.

### Scope Boundaries

- Pane incarnation checks (#207), legacy notice mirror removal (#210), and pass scheduling or budgets (#216) are unchanged.
- The general state lock (`.lock`) keeps its blocking acquisition; this plan only keeps the pump's use of it short and free of remote I/O.
- Per-recipient lock files are never deleted; they are empty files, one per recipient ever contacted, and deleting a flock file is racy.

---

## Planning Contract

### Key Technical Decisions

- KTD1. **Shared compatibility lock plus exclusive recipient lock (flock).** New deliveries take `.deliver.lock` with `LOCK_SH` and then `deliver/<hash>.lock` with `LOCK_EX`. Older runtimes' `LOCK_EX` on `.deliver.lock` excludes every new delivery and vice versa (R8), while new runtimes share it (R1). Chosen over claim/commit records in `returns.json`: a durable claim needs expiry to recover from a crash, which R4 forbids; flock is released by the kernel on process death, and the existing in-flight delivery record already carries the durable uncertainty. The shared lock is held across Herdr I/O only to exclude legacy holders; it never blocks a new-runtime peer.
- KTD2. **Recipient key is the canonical endpoint.** `identity(host, route)` (`host.Canonical(machine)`, session, pane) hashed with SHA-256 names the lock file, so a legacy hostname alias and the stable ID collide by construction (R2, #202 preserved). Role is not part of the key; one pane is one recipient whatever role it holds. Incomplete routes hash their empty fields and lock like any other.
- KTD3. **Contended recipients go last, within the #216 budget.** The pass visits buckets in #216's fair order; a recipient whose lock is busy is set aside after one non-blocking try and revisited after every other bucket with a wait bounded by the remaining budget minus the cheapest observe-and-prompt cost (`ObserveTimeout + PromptTimeout + 2 x PipeGrace`), so a pump never waits for a lock it could not use, and a hook event's later pumps keep their share. When that bound is not positive, or the wait ends, the recipient gets #216's existing `deferred` row with the busy-lock reason. No new scheduler or budget.
- KTD4. **Claim under the state lock.** The in-flight stamp becomes one `s.Lock()` hold that re-reads every sendable task and rechecks obligation, route, cwd, registration, and owner (the rules `revalidate` and `checkIdentity` already encode). Obligations that changed are dropped and the notice text is rebuilt from the survivors (local, cheap); when none survive nothing is stamped or sent. The prompt-fit check (`fits(PromptTimeout)`) runs inside that hold, after the recheck, so time spent waiting for the state lock can never start a prompt the pass deadline would cut; a prompt that no longer fits writes nothing and defers. Then the in-flight entry is written for the survivors. Outcome recording is likewise one hold (R6). No Herdr call happens under the state lock.
- KTD5. **Lock ranks enforced per Store.** `store` tracks held ranks (compat 1, recipient 2, state 3) per `Store` value; acquiring a rank not greater than one already held returns `ErrLockOrder`. This makes inversion a test failure rather than a latent deadlock (R7). `repair.Send` moves to the same compat-plus-recipient pair: it reads the worker route without a lock only to choose the lock, takes compat-shared then the recipient lock with a bounded wait, and only then runs its existing first state-lock block (duplicate key, allowance, cleanup, running attempt), which also refuses if the worker route's canonical identity no longer matches the lock held. Duplicate-key and allowance checks therefore stay serialized per worker.

### High-Level Technical Design

Directional sequence for one recipient in one pass:

```
shared(.deliver.lock, ctx)            # rank 1, bounded by pass ctx
  exclusive(deliver/<h>.lock, try)    # rank 2; busy -> revisit last, then bounded wait
    revalidate (records, no lock)     # existing #216 step
    Herdr list / get                  # no state lock held
    state lock { recheck; stamp in-flight }   # rank 3, local I/O only
    Herdr prompt
    state lock { finalize delivery; mirror notice if still routed }
  release recipient
release shared
```

### Assumptions

- sumctl performs one operation per process on one goroutine, so a per-`Store` rank tracker reflects the calling operation; concurrent in-process actors in tests use separate `Store` values.
- Linux and macOS `flock` provide shared/exclusive semantics on the same file across processes, as the existing locks already rely on.

### Risks

- `flock` gives exclusive waiters no priority, so during a mixed-version window an older runtime's exclusive `.deliver.lock` acquire can be starved by overlapping new-runtime shared holders. Safety (R8) holds; only the older runtime's progress degrades until the upgrade completes.
- Other prompt paths that never took `.deliver.lock` (`refresh`, `prepare`, the mesh relay) stay outside recipient serialization, as before this change.
- Concurrent `agent get`/`agent prompt` from two sum processes into one real Herdr session is proven only against the fake; the live gate stays `not-run`.

### Sequencing

U1 lands the regression and records the baseline first. U2 adds the lock primitives, U3 rewires the pump and repair, U4 adds the remaining safety tests, U5 documents and measures.

---

## Implementation Units

### U1. Two-process regression and baseline

- **Goal:** Reproduce head-of-line blocking deterministically across two OS processes before changing the lock.
- **Requirements:** R1, R10
- **Files:** `go/internal/returns/concurrency_test.go` (new), `go/internal/returns/pass_test.go` (fake gains per-pane prompt/get holds)
- **Approach:** Re-exec the test binary as a helper process that runs `Pump` for recipient A against the shared state home; the helper takes `ObserveTimeout`, `PromptTimeout`, and `Budget` from env set by the parent (PromptTimeout longer than the hold, Budget at least `2 x ObserveTimeout + PromptTimeout + PipeGrace`), and the parent releases A before A's `PromptTimeout` ends. The fake Herdr marks `held-<pane>` and waits for `release-<pane>` on A's prompt. Once the mark exists, the parent runs `Pump` for B and measures elapsed time and `lock_wait_ms`. Run on the unchanged code to capture the baseline.
- **Test scenarios:** A held, B unrelated in the same session: B `submitted` quickly, A still held; release A: A `submitted`, exactly one prompt each.
- **Verification:** Fails (B deferred or slow) before U3, passes after.

### U2. Lock primitives and ordering

- **Goal:** Shared compatibility lock, per-recipient exclusive lock, bounded acquisition, and rank enforcement.
- **Requirements:** R3, R7, R8
- **Files:** `go/internal/store/store.go`, `go/internal/store/store_test.go`
- **Approach:** One internal flock helper with mode and ctx; `DeliveryShared(ctx)`, `RecipientLock(ctx, key [3]string, wait bool)` returning `ErrRecipientBusy`/`ErrDeliveryLockBusy`; `DeliveryLock` keeps its exclusive legacy meaning. Rank tracking wraps every acquire and release, including `Lock`.
- **Test scenarios:** Out-of-order acquisitions (state then recipient, recipient then compat, two recipients) return `ErrLockOrder`; a held recipient lock gives up at the deadline; a shared holder blocks an exclusive try and two shared holders coexist; alias spellings map to one lock path.
- **Verification:** `go test ./internal/store/`.

### U3. Pump and repair on recipient locks

- **Goal:** Deliver under compat-shared plus recipient-exclusive, with contention-last revisit and the state-locked claim and record.
- **Requirements:** R1-R6, R9
- **Files:** `go/internal/returns/pump.go`, `go/internal/repair/repair.go`
- **Approach:** Split `deliver` into lock acquisition and the existing body; `Pump` collects busy buckets and revisits them. Replace `beforePrompt` with the KTD4 claim; make `stampDelivery`/`mirrorNotice` run inside one state-lock hold with the R6 filter. Add `lock_wait_ms` to `fanout`. `repair.Send` follows KTD5's order with a bounded wait and a clear busy error.
- **Test scenarios:** Existing `pass_test.go` stays green; the legacy-holder deferral test holds `DeliveryLock()` through a second `store.Open` of the same home (standing in for a separate legacy process) and still gets `deferred` with no Herdr calls. Existing `repair` tests stay green.
- **Verification:** `go test ./internal/returns/ ./internal/repair/`.

### U4. Same-recipient, alias, crash, and revalidation coverage

- **Goal:** Prove R2, R4, R5, R6, R8 across processes.
- **Requirements:** R2, R4, R5, R6, R8
- **Files:** `go/internal/returns/concurrency_test.go`
- **Test scenarios:**
  - Two processes, same recipient via stable ID and legacy hostname alias: one prompt total; the second reports `quiet` or `deferred`.
  - Helper killed while its prompt is held: next pass reports `uncertain`, sends nothing, lock is free.
  - Helper killed while its `agent get` is held: no delivery record, next pass sends once.
  - Obligation closed, or task rebound, while observation is held: no prompt, no in-flight stamp.
  - One of two coalesced obligations closes while observation is held: the survivor is prompted once and the notice names only it.
  - While a new pass holds A, a legacy-style exclusive try on `.deliver.lock` fails.
- **Verification:** `go test ./internal/returns/`.

### U5. Documentation, feature maps, measurements

- **Goal:** Truthful docs and coverage rows; numbers in the PR.
- **Requirements:** R10
- **Files:** `docs/architecture.md`, `skills/sum-rundown/SKILL.md`, `docs/features/coordination.md`
- **Approach:** Describe lock order and scope in architecture; update the `returns.pass-budget` row and add `returns.recipient-serialization`; live Herdr concurrency stays manual/not-run.
- **Verification:** `python3 .agents/skills/verify/scripts/verify_run.py --check`, then the canonical verifier.

---

## Verification Contract

| Gate | Command |
| --- | --- |
| Targeted | `cd go && go test ./internal/store/ ./internal/returns/ ./internal/repair/` |
| Canonical | `MISE_ENABLE_TOOLS=go,python,node python3 .agents/skills/verify/scripts/verify_run.py --base "$(git merge-base origin/main HEAD)"` |
| Live | `mise run test-live` only if a real Herdr is available; otherwise `not-run` |

## Definition of Done

- R1-R10 hold with the tests named in U1-U4 green, and the U1 baseline recorded in the PR body.
- The canonical verifier passes; feature maps updated; no dead-end code left in the diff.
- PR body records decisions, measurements, and residuals, says "Closes #209", and CI is green before merge.
