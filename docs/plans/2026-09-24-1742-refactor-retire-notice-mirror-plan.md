---
title: Retire the persisted legacy notice mirror behind one read-boundary projection - Plan
type: refactor
date: 2026-09-24
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-plan-bootstrap
execution: code
origin: https://github.com/douglasjarquin/sum/issues/210
---

# Retire the persisted legacy notice mirror behind one read-boundary projection - Plan

## Goal Capsule

- **Objective:** Notification state has one persisted authority (the task's `returns.json` delivery records), so a delivery pass no longer rewrites every notified task's `task.json` just to keep a second copy of the latest attempt, while every current command keeps the same notice meaning and every supported older runtime keeps working on the same records.
- **Means:** Prove from the supported rollback floor that no supported caller decides anything from `task.notice` (KTD1), derive the public `notice` shape at the read boundary from the sidecar with one projection (KTD2), use it in every current reader (KTD3), and delete the mirror write, its reason plumbing, and the dead option (KTD4) while keeping the historical field and the dispatch-time `notice: null` (KTD5).
- **Authority:** Issue #210, then this plan, then `VERIFY.md` and the feature maps.
- **Stop:** No migration, no deletion of the persisted `notice` field from existing records, no new rollback guard or force-retire flag, no schema bump, no change to delivery decisions (`NotificationState`, pump selection, uncertainty rules), no edits to `skills/` or `COORDINATOR.md`. Keep #202 stable/legacy route equivalence and #209 lock order untouched.
- **Execution profile:** Standard Go refactor in `go/internal/returns` plus four readers, proven by offline tests, a recorded lab exercise of older helper binaries against candidate-written state, and a measured write count.
- **Who finishes:** The implementer lands, verifies, opens the PR, and squash-merges after CI is green; the user granted that merge for this run in the invoking instruction. The issue's "human merge" boundary is a sum product behavior this change must not weaken (tasks sum delivers still end at a human merge); it does not govern this PR.

---

## Product Contract

### Summary

Today every recorded delivery outcome writes the delivery into `returns.json` and then rewrites `task.json` for each notified task to set a single `notice` object (the "mirror"). Every decision already comes from `returns.json`; the mirror is read only for display. This change stops the mirror write and computes the same `notice` object from `returns.json` when a command prints it.

### Problem Frame

`go/internal/returns/deliverylock.go` `(*pass).record` stamps the delivery, then `mirrorNoticeLocked` re-reads and saves `task.json` (which carries the full brief text) once per task in the delivery. That is a second persisted authority for the same fact, an extra whole-record write per notified task under the state lock, and a place where two versions of the truth can disagree (the mirror is written only for still-routed tasks, while the sidecar records every claimed attempt). `legacyReasons` and `PumpOpts.Reason` exist only to fill the mirror's `reason`.

### Supported-version inventory (evidence for KTD1)

| Runtime range | How it can be selected today | Reads `task.notice` | Decides delivery from it | Needs the field present |
| --- | --- | --- | --- | --- |
| Pre-sidecar Python (`0f01c9b`..`2c0ea96^`, 2026-09-05/06) | Not selectable: a release target must contain `go/cmd/sumctl/main.go` (current `release.VerifyRelease`; first on main at `11fc3d9`), and a checkout target is always the installation's current HEAD (`apply` only fast-forwards; checkout rollback requires HEAD to equal the recorded SHA, `updatecmd/activation.go`). Reachable only by a manual `git checkout` of the installation, outside the supported floor | yes, the only delivery status | its rundown skill said "a pending notice can be tried once" | yes (`task["notice"]`) |
| Post-sidecar Python (`2c0ea96`..`11fc3d9^`; `bin/sumctl` runs `lib/sumctl.py` directly) | Not selectable, for the same two reasons; manual checkout only | status row, duplicate `ask`, legacy `notify` output | no; `returns.json` decides | yes (`task["notice"]` subscript) |
| Go `11fc3d9`..`4eb8591^` | Staged release; refused once records carry `m-` ids unless `--allow-pre-machine-identity` | status row, duplicate `ask`, `Notify` output, `bind` output | no | no (`Get`) |
| Go `4eb8591`..`ca7b842` (base) | Staged release, or the current HEAD checkout | same | no | no |

Every selectable runtime writes its own mirror whenever it records an outcome. After a rollback, an older runtime may show a null, stale, or synthesized `pending` notice for a return the candidate delivered until it records its own delivery for that task (it never re-delivers a submitted return, so that may be never). That is a stale informational field in the old runtime's output, not a behavior dependency: its pump, `inbox --live`, and its rundown procedure act on the `returns` view (`uncertain`/`stalled`), never on `notice.status`, and it never re-sends because of the field.

### Requirements

- R1. A delivery pass writes no `task.json`; only `returns.json` (and refresh bookkeeping in `versions.json`, unchanged) records an attempt.
- R2. `ask`, `answer`, `report`, and `notice` keep returning `notice` with `at`, `recipient`, `reason`, `status` (`pending`, `submitted-not-acknowledged`, `uncertain`), `delivery`, optional `error`, and `returns`; a duplicate `ask`, `status`/`inbox` rows, `show`, and `bind` carry the same projection.
- R3. Pending, submitted, uncertain (timed out, unknown effect, interrupted in-flight), known-not-delivered, stalled, and inline outcomes keep their meanings; an absent or stale persisted field never changes a delivery decision or permits a resend.
- R4. Stable and legacy-hostname spellings of one recipient still count as one recipient (#202); nothing in the projection keys on the route.
- R5. Old records keep working: a task with a persisted `notice` and no qualifying sidecar delivery shows that persisted value; a task whose persisted value is older than its sidecar shows the sidecar. Questions, answers, reports, briefs, and the persisted field itself are never rewritten or deleted.
- R6. Older helpers from the supported floor run `status`, `show`, `ask`, `answer`, `report`, and `notice` on candidate-written state without errors and without a second prompt for a return the candidate already submitted.
- R7. Removed code and task writes per representative notification are measured and reported; docs and feature maps describe the new authority.

### Scope Boundaries

- Out: removing the `notice` key from records, a state or brief schema bump, rollback-policy changes, a migration, editing `skills/` or `COORDINATOR.md`, changing refresh bookkeeping, or any change to what is sent and when.
- Out: the Mesh relay's own `submitted-not-acknowledged` string (unrelated transport receipt).

---

## Planning Contract

### Key Technical Decisions

- KTD1. **Retirement is supported.** The inventory shows the only runtime that treated `task.notice` as delivery state (pre-sidecar Python) cannot be selected by the current updater, and every selectable runtime reads the field for display only. Chosen over keeping the mirror and recording a dependency: the issue allows retirement when no supported caller requires it, and a display field that an old runtime refreshes on its own next delivery is not a requirement.
- KTD2. **One projection, `returns.Notice(task, sidecar)`.** Pick the last delivery in the task's sidecar whose `obligations` include a non-refresh obligation id (`question:`, `answer:`, `report:`, `attention:`). Map `state`: `submitted` -> `submitted-not-acknowledged`; `uncertain` and `in-flight` -> `uncertain`; anything else -> `pending`. `at` = `finished_at`, else `at`; `recipient` = the delivery's `recipient.recipient`; `delivery` = its id; `error` = its `reason` when the status is not submitted (the recorded reason already carries the error text for not-delivered and uncertain outcomes; an in-flight record gets the interrupted-attempt text `NotificationState` uses); `reason` = `legacyReasons` of the first of question, answer, report the delivery covers, else `saved task state needs attention`. With no qualifying delivery, or an unreadable sidecar, return the persisted `task.notice` unchanged (history, possibly null). In-flight maps to `uncertain` rather than "no change": the mirror never recorded an interrupted attempt, but the sidecar does, and `uncertain` is its honest meaning.
- KTD3. **Every reader uses the projection.** `Notify` (after its pump, re-reads the sidecar, synthesizes the pending object only when the projection is null), the duplicate-question branch of `ask.Ask`, `statuscmd` rows (keeping the key's position), `evidenceview.Show`, and `bindcmd.Bind` (both overwrite the copied `notice` key in place after their work). Each passes the full sidecar from `returns.ReadReturns(s, id)` to `Notice`, never `View`'s 20-entry `deliveries` history, which refresh-only deliveries could fill. `Notify` no longer passes its reason into the pump; it keeps the parameter only for the synthesized pending object.
- KTD4. **Delete the write path.** Remove `mirrorNoticeLocked`, the `reason`/`errStr` parameters of `record` that only fed it, the `legacy` computation in `deliverLocked`, and `PumpOpts.Reason` with its setters in `bindcmd` and `hookstatus`. `legacyReasons` moves beside the projection as read-side presentation.
- KTD5. **Keep the historical field and `notice: null` at dispatch.** Post-sidecar Python subscripts `task["notice"]`; `prepare` keeps writing the key once at creation (not per notification), and nothing deletes it. No migration: an ignored historical field is sufficient.

### High-Level Technical Design

```
record(route, items, claimed, delivery, changes)   # state lock held
  stamp delivery in each task's returns.json         # unchanged
  (mirror write removed)

Notice(task, sidecar) -> latest non-refresh delivery ? legacy shape : task.notice
  Notify:  Pump(...) ; Notice(task, ReadReturns) ?? synthesized pending ; + returns row
  ask dup, status row, show: Notice(task, ReadReturns)
```

### Assumptions

- The legacy `reason` text is presentation only (no test, doc, or skill asserts it); a hook- or bind-delivered notice now reads by kind (for example "a decision is waiting") instead of the hook's one-off phrase.
- For an attempt whose obligation closed between claim and record, the projection shows that attempt (the sidecar has it) where the mirror would have skipped it; that is the more accurate reading.

### Risks

- After a rollback, an old runtime's `status` shows a stale `notice` for tasks the candidate notified last, until that runtime's own next delivery for the task. Mitigation: documented in the PR and architecture doc; the old runtime's decisions and `returns` view are unaffected (R6 lab check).
- Python-era runtimes are outside the supported floor; U4 still runs the post-sidecar `lib/sumctl.py` from `0f6b4a6` against the lab as extra evidence and records the result rather than gating on it.

### Sequencing

U1 adds the projection and its tests; U2 switches readers; U3 deletes the write path and adds the no-write regression; U4 lab-exercises old helpers, measures, and reconciles docs.

---

## Implementation Units

### U1. Read-boundary projection

- **Goal:** `returns.Notice` per KTD2.
- **Requirements:** R2, R3, R4, R5
- **Files:** `go/internal/returns/notice.go` (new), `go/internal/returns/notice_test.go` (new)
- **Test scenarios:** no deliveries and null field -> null; persisted field and no sidecar deliveries -> persisted value verbatim; stale persisted field plus newer sidecar delivery -> sidecar-derived; submitted (prompt and inline) -> `submitted-not-acknowledged`, no `error`; not-delivered -> `pending` with `error`; three not-delivered (stalled) -> `pending`; uncertain -> `uncertain` with `error`; in-flight last -> `uncertain`; refresh-only latest delivery ignored in favor of an earlier question delivery; reason by kind priority; `recipient` from the delivery.
- **Verification:** `cd go && go test ./internal/returns/`.

### U2. Readers on the projection

- **Goal:** KTD3.
- **Requirements:** R2, R5
- **Files:** `go/internal/returns/pump.go` (`Notify`), `go/internal/ask/ask.go`, `go/internal/statuscmd/statuscmd.go`, `go/internal/evidenceview/evidenceview.go`, `go/internal/bindcmd/bind.go`, existing CLI tests (`go/internal/cli/fleet_test.go`, `notice_unregistered_test.go`, `show_test.go`, `status_test.go`)
- **Test scenarios:** existing assertions on `notice.status`/`notice.error` stay green; a duplicate `ask` after a submitted notice returns `submitted-not-acknowledged`; `status` row, `show`, and `bind --parent-only` for a task with a submitted question show the derived notice; pinned stdout fixtures with `notice: null` unchanged.
- **Verification:** `cd go && go test ./internal/cli/ ./internal/ask/ ./internal/statuscmd/ ./internal/evidenceview/`.

### U3. Remove the mirror write

- **Goal:** KTD4 and R1.
- **Requirements:** R1, R3
- **Files:** `go/internal/returns/deliverylock.go`, `go/internal/returns/pump.go`, `go/internal/bindcmd/bind.go`, `go/internal/hookstatus/event.go`, `go/internal/hookstatus/enable.go`, `go/internal/returns/pass_test.go` or `concurrency_test.go`
- **Test scenarios:** a pump that submits, one that records not-delivered, and one that records uncertain each leave every `task.json` byte-identical while `returns.json` gains the attempt; the existing crash, alias, revalidation, and legacy-lock tests stay green.
- **Verification:** `cd go && go test ./internal/returns/ ./internal/bindcmd/ ./internal/hookstatus/`.

### U4. Old helpers, measurement, docs

- **Goal:** R6, R7.
- **Files:** `docs/architecture.md`, `docs/verification.md`, `docs/features/coordination.md`
- **Approach:** Build helpers at `11fc3d9`, `4eb8591`, and `ca7b842` in scratch extractions (plus, out of floor, `python3 lib/sumctl.py` from `0f6b4a6`); in a temporary state home with the fake Herdr, let the candidate dispatch-free fixture record a submitted question, then run each old helper's `status`, `show`, `ask` (duplicate and new), `answer`, `report`, `notice`, and `pump`, recording exit codes, whether a second prompt reached the fake for the already-submitted return, and the stale, null, or synthesized-pending field display. Count `task.json` writes per representative notification (one question to the coordinator; one pass coalescing two tasks) for base and candidate with `strace` or a write-counting harness. Update the architecture paragraph ("the old single notice field mirrors the latest attempt") and `verification.md` L31 wording; add or extend a feature-map row for the projection and no-write guarantee.
- **Verification:** `python3 .agents/skills/verify/scripts/verify_run.py --check`, then the canonical verifier.

---

## Verification Contract

| Gate | Command |
| --- | --- |
| Targeted | `cd go && go test ./internal/returns/ ./internal/ask/ ./internal/statuscmd/ ./internal/evidenceview/ ./internal/cli/ ./internal/bindcmd/ ./internal/hookstatus/` |
| Canonical | `MISE_ENABLE_TOOLS=go,python,node python3 .agents/skills/verify/scripts/verify_run.py --base "$(git merge-base origin/main HEAD)"` |
| Old helpers | Recorded lab exercise (U4), fake Herdr and temporary homes only |
| Live | `mise run test-live` only with a real Herdr in a `sum-test-210-*` session; otherwise `not-run` |

## Definition of Done

- R1-R7 hold with the U1-U3 tests green and the U4 lab and measurement results in the PR body with the inventory table.
- The canonical verifier passes; docs and feature maps updated; no dead code left.
- PR says "Closes #210", CI is green before the squash merge.
