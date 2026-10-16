---
title: Project-specific capacity overrides in admission - Plan
type: feat
date: 2026-10-16
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-plan-bootstrap
execution: code
origin: https://github.com/douglasjarquin/sum/issues/296
---

# Project-specific capacity overrides in admission - Plan

## Goal Capsule

- **Objective:** An operator running several enrolled projects can let a cheap project admit more concurrent executions than an expensive one, while the global ceiling, the admission lock, and the conservative reservation lifecycle behave exactly as they do today.
- **Means:** An optional `projects` map inside the existing `capacity` block, resolved per admission from the task's recorded registry identity (KTD1, KTD2).
- **Authority:** Issue #296, then this plan, then `VERIFY.md` and the feature maps.
- **Stop:** No scheduler, queue, daemon, automatic dispatch, automatic termination, or replacement reservation system. No change to how or when a reservation is released. No edits to `skills/`, `COORDINATOR.md`, `VERIFY.md`, `mise.toml`, or `.agents/skills/`. No new tests and no code comments (KTD7).
- **Execution profile:** Bounded Go change in `go/internal/settings`, `go/internal/launch`, `go/internal/project`, the three `launch.Admit` callers, and the settings CLI, plus docs, the web configuration page, the help catalog, and the coordination feature map. Proven by the existing suite, hand exercise against a lab state home, and before/after CLI evidence.
- **Who finishes:** The worker implements, verifies, and opens the pull request. The user merges.

---

## Product Contract

### Summary

Add `capacity.projects`, a map from enrolled project name to a per-project execution limit. Every admission resolves the applicable limit as the project override when one exists, otherwise `per_repository`, and always enforces `global`. Views, deferral messages, help, docs, and the feature map disclose the resolved limit, its source, and the held reservations.

### Problem Frame

`.sum/settings.json` carries one `global` ceiling and one uniform `per_repository` limit. A documentation repository that could safely run three workers is held to the same single writer as an application whose integration environment tolerates one. The only way to admit more on the cheap project today is to raise `per_repository` for every repository, which removes the protection the expensive one needs.

### Requirements

**Configuration**

- R1. `capacity` accepts an optional `projects` object whose keys are enrolled project names and whose values are integers from 1 to 64, each at most `global`.
- R2. A `projects` value that is not an object, a key that is not a plausible project name, a value that is not an integer in range or exceeds `global`, or any other defect refuses the whole settings file with the exact defect, the way other capacity defects do today, so nothing becomes unlimited.
- R3. `settings set` gains `--project-limit NAME=N` (repeatable) and `--clear-project-limit NAME` (repeatable); it refuses a name that is not enrolled in the project registry and a value outside range or above `global`.
- R4. An installation without a `capacity` block behaves byte-for-byte as before in every view and admission. A settings file with `capacity` but without `projects` keeps today's limit values and refusal wording; the two disclosed changes are the `effective` list of KTD6 in views and the counting rule of R6, under which a task at a linked worktree of an enrolled clone, or recorded before that clone was enrolled, is counted under the project instead of getting its own `per_repository` writer.

**Resolution and admission**

- R5. The applicable per-scope limit is the `projects` entry for the task's enrolled project name when one exists, otherwise `per_repository`; `global` is enforced first in every case. An enrolled task is scoped by its registry name whether or not an override exists; only an unenrolled task is scoped by its path (R7).
- R6. The held count for an enrolled project is every held reservation of a task that records the same project name, plus every held reservation of a task that records the same resolved repository path, so a path alias or a task recorded before enrollment never evades or splits the count.
- R7. A task whose repository is not an enrolled clone, or a linked worktree of one, at admission time keeps today's behavior: its scope is its resolved git toplevel, its limit is `per_repository`, and the documentation says so, including that a second independent clone of an enrolled remote at an unenrolled path is such a task.
- R8. A `--repo` path that is a linked worktree of an enrolled clone resolves to that enrolled project through the git common directory, so a worktree alias does not bypass the override.
- R9. Worker preparation, execution resume, and independent verification admission use one resolver with the task's recorded project and repository; a task whose recorded project is null is re-resolved against the registry by its recorded repository path at each admission, and a refusal creates no task, checkout, pane, or reservation.
- R10. Lowering a limit affects future admissions only; held reservations, running processes, and checkouts are untouched, and nothing dispatches because a slot is free.

**Disclosure**

- R11. The capacity deferral names the scope, the resolved limit, its source (`projects.NAME` or `per_repository`), the holders, and the settings key to raise, while keeping the existing `N of M slots for` and `N of N global execution slots` phrasing that callers and the demo test match on.
- R12. `settings show` and the `capacity` block of `status` list, when a `capacity` block is present, every declared project override and every scope holding reservations with its limit, source, enrollment state, and held attempt ids; `limits` echoes `projects` only when declared.
- R13. The help catalog, `docs/configuration.md`, the web configuration page, and `docs/features/coordination.md` describe the setting, the resolver order, the unenrolled-path rule, and the legacy-compatibility guarantee.

### Key Decisions

- **Overrides are keyed by the registry name, never by a clone directory basename or a path.** Governs R1, R6, R7, R8.
- **The reservation lifecycle is unchanged.** A report, a ready PR, an idle pane, or an uncertain process state releases nothing; only the existing conclusive stop observation does. (session-settled: user-directed — chosen over firstmate#5343's release-at-PR-handoff rule: the issue text keeps Sum's conservative lifecycle.) Governs R10.
- **No tests are added or expanded in this change, and no code comments are written.** (session-settled: user-directed — chosen over the issue's "regression tests" checkbox: the user's standing #287 rule carried in the worker brief overrides it and must be disclosed in the PR body and report.) Governs R13.

### Scope Boundaries

- Out: any queue, scheduler, daemon, persistent project manager, automatic worker termination, cross-machine scheduling, or token or cost accounting (held for #299).
- Out: changing `project show`'s active-task listing, which already matches tasks by repository path.
- Considered and not built: refusing admission for every task when a `projects` key names a project that is no longer enrolled. The stale key makes nothing unlimited: it still caps at `global`, applies only to tasks that recorded that name, and the default applies to every other task, while `settings show` discloses `enrolled: false` for it; evidence that operators miss that disclosure would change the call.
- Considered and not built: detecting duplicate JSON keys inside `projects`. `ordjson` keeps the last value for a repeated key everywhere in the file today; a general duplicate-key refusal is a parser change outside this issue.

#### Deferred to Follow-Up Work

- Refactoring the duplicate occupancy helpers in `go/internal/settings/settings.go` and `go/internal/launch/launch.go` into one; #299 is held behind this change and both files must stay minimal.

---

## Planning Contract

### Key Technical Decisions

- KTD1. **Schema: `capacity.projects` is an object map inside the existing capacity block, canonicalized at load.** `validateCapacity` learns a third allowed key; `projects` validates as name to integer, each 1..64 and at most `global`, and an empty map canonicalizes to absence so the saved file and every view stay identical to today when nothing is declared. (session-settled: user-directed — chosen over a separate top-level settings block: one configuration owner, the existing admission lock, and minimal change inside `go/internal/settings` for #299.)
- KTD2. **Resolution happens in `go/internal/settings` and `launch.Admit` takes the task's project identity.** A resolver in the settings package returns the limit, its source, and the scope label for a (capacity, project name, repository) triple; `launch.Admit(s, tasks, repository, project)` calls it, and the three callers pass the recorded `project` object they already hold. When that object is null, the resolver reads the registry (`project.ReadProjects`, a local file read that is safe under the state lock) and adopts the project whose resolved `path` equals the recorded repository, so a task prepared before its clone was enrolled resumes under the same scope as newer tasks; a registry read failure leaves the task path-scoped. (session-settled: user-directed — chosen over a per-caller lookup: resume and verification must use the same resolver as preparation.)
- KTD3. **Held reservations for an enrolled project are the union of same-project-name and same-repository-path tasks.** This is a superset of today's per-path count, so no existing refusal becomes an admission, and a legacy task without a `project` field at the same clone path still counts.
- KTD4. **`project.ByPath` also matches a linked worktree of a registered clone through `git rev-parse --path-format=absolute --git-common-dir`.** Exact resolved-path equality stays the first check; the common-directory comparison runs only when it fails, and both sides are read with `--path-format=absolute` and passed through `resolvePath`, because the main clone otherwise reports the relative string `.git`. `go/internal/bindcmd/bind.go` already makes this call; the `linkedWorktrees` helper in `go/internal/project/project.go` shows the convention for bounded git observation.
- KTD5. **Enrollment is validated on the write path, disclosed on the read path.** `settings set --project-limit` refuses an unenrolled name with the registry hint; `LoadSettings` validates shape only, so a project removed later never blocks admission for everyone, and `settings show` marks the stale override `enrolled: false`.
- KTD6. **Views add an `effective` list only when a `capacity` block exists.** The legacy `limits`, `occupied.by_repository`, and `source` keys keep their shape; the only golden pins that change are those whose fixture already declares capacity. Each `effective` entry carries `scope`, `limit`, `source`, `enrolled` (for project scopes), and `held`.
- KTD7. **No tests, fixtures, or golden files are added; no code comments are written.** Golden pins that the output change moves are regenerated minimally and named in the handoff. (session-settled: user-directed — chosen over adding the regression tests the issue lists: the user's standing #287 rule in the worker brief.)
- KTD8. **The pull request is opened, never merged, and the reservation lifecycle is untouched.** (session-settled: user-directed — chosen over factory auto-merge and firstmate's release-at-handoff: the brief and the issue text.)

### Assumptions

- The enrolled project name is the registry key returned under `project.name` on every task record prepared with `--project` or with a `--repo` path that `ByPath` matched; a task prepared without a match records `project: null`.
- A plausible project name for R2 is validated with the same shape the registry accepts for `name`; the exact pattern is read from `go/internal/project/enroll.go` at implementation time.
- Duplicate JSON keys cannot be detected without changing `ordjson`; the issue's "duplicate/ambiguous" criterion is met by refusing malformed shapes and unenrolled names at write time and disclosing stale names in views.

---

## Implementation Units

### U1. Schema, validation, and resolver in the settings package

- **Goal:** `capacity.projects` loads, validates, canonicalizes, and resolves.
- **Requirements:** R1, R2, R4, R5, R6, R7, R12 (resolver and held-count helpers only).
- **Dependencies:** none.
- **Files:** `go/internal/settings/settings.go`.
- **Approach:**
  1. Extend `validateCapacity` with the `projects` key: object, each key a plausible project name, each value an integer 1..64 and at most `global`; canonicalize sorted and omit when empty. Update the allowed-keys error text to list `projects`.
  2. Add an exported resolver returning the scope label, limit, and source for a capacity object, a project name, and a repository path, with the null-project registry adoption of KTD2.
  3. Add an exported held-reservation collector implementing KTD3 over the task list, reusing `reservations.Held`.
  4. Extend `CapacityViewOf` (reached by `settings show` and `status`; coordinator `init` carries no capacity block and is intentionally left alone) with the `effective` list of KTD6 when `limits` is non-nil, reading the registry through `project.ReadProjects` for the `enrolled` flag; a registry read failure never fails the view, the entries then carry `enrolled: null`.
- **Patterns to follow:** `validateCapacity` and `validateEvidence` for error wording; `presetSummary` for sorted canonical objects.
- **Test scenarios:** Test expectation: none -- KTD7. Hand exercise in U6 covers: no `capacity` block leaves `settings show` byte-identical; `{"docs": 3}` with `global: 6` loads; `{"docs": 7}` with `global: 6` is refused naming the key; `{"docs": "3"}` and `"projects": []` are refused; a stale name shows `enrolled: false`.
- **Verification:** `mise run lint` and `mise run test` pass; the existing capacity golden pins without a capacity block are unchanged.

### U2. Admission takes the project identity

- **Goal:** `launch.Admit` enforces the resolved limit and records the scope.
- **Requirements:** R5, R6, R9, R10, R11.
- **Dependencies:** U1.
- **Files:** `go/internal/launch/launch.go`, `go/internal/prepare/prepare.go`, `go/internal/execution/park.go`, `go/internal/verifycmd/execute.go`.
- **Approach:**
  1. Add a `project` parameter to `Admit`; derive the project name from it.
  2. Keep the global check and its message unchanged; replace the per-repository check with the resolver and the held collector from U1.
  3. Reword the per-scope refusal per R11, keeping `N of M slots for` as its prefix.
  4. Record `scope`, `limit`, and `limit_source` on the admission object alongside the existing `limits`, `source`, and `occupied_before`.
  5. Pass the recorded `project` object from each caller: `projectObj` in prepare, `task.Get("project")` in park, `current.Get("project")` in the verifier.
- **Patterns to follow:** `intField` and `asObject` helpers already in `launch.go`.
- **Test scenarios:** Test expectation: none -- KTD7. Hand exercise in U6 covers two enrolled projects with limits 3 and 1 under `global: 6`, a third dispatch to the limit-1 project refused with no task directory created, resume refused by the same limit, a task prepared before enrollment resuming under the project scope, and an unenrolled `--repo` path counted under its own path.
- **Verification:** `go/internal/cli/demo_test.go`, `fleet_test.go`, and `verify_execute_test.go` still pass against the reworded message.

### U3. Linked worktrees resolve to their enrolled project

- **Goal:** A `--repo` path inside a linked worktree of a registered clone records that project's identity.
- **Requirements:** R8.
- **Dependencies:** none.
- **Files:** `go/internal/project/project.go`.
- **Approach:** In `ByPath`, after exact resolved-path matching fails, read the candidate's and each record's common directory with `git -C PATH rev-parse --path-format=absolute --git-common-dir`, pass both through `resolvePath`, and return the record's identity on a match. Keep failures (not a git directory, git absent) as no match.
- **Patterns to follow:** the common-directory check in `go/internal/bindcmd/bind.go`; `gitToplevel` and `linkedWorktrees` in the same file for bounded failure handling.
- **Test scenarios:** Test expectation: none -- KTD7. Hand exercise in U6 covers `git worktree add` from an enrolled clone and a `prepare --repo` on that worktree recording the project name.
- **Verification:** `go test ./internal/project/...` passes.

### U4. Settings CLI and help catalog

- **Goal:** Operators set and clear per-project limits with validated flags.
- **Requirements:** R3, R13.
- **Dependencies:** U1.
- **Files:** `go/internal/settings/write.go`, `go/internal/cli/settings.go`, `go/internal/helpview/catalog.json`.
- **Approach:**
  1. Add `ProjectLimits map[string]int` and `ClearProjectLimits []string` to `WriteArgs`; merge them into the capacity base before `validateCapacity`; refuse a name absent from `project.ReadProjects`.
  2. Add `--project-limit` (string array, `NAME=N`) and `--clear-project-limit` (string array) flags; extend the conflict and "give one of" checks; `--clear-capacity` conflicts with both.
  3. Add both flags to the `settings-set` catalog entry.
- **Patterns to follow:** the existing `--global` and `--per-repository` handling in `Write` and `runSettingsSet`.
- **Test scenarios:** Test expectation: none -- KTD7. Hand exercise in U6 covers `settings set --project-limit docs=3`, `--project-limit nope=2` refused with the registry hint, `--project-limit docs=9` refused against `global: 6`, and `--clear-project-limit docs` restoring the default.
- **Verification:** `mise run lint` passes; `sumctl help settings-set` lists both flags.

### U5. Documentation, web page, and feature map

- **Goal:** Every surface that documents capacity describes overrides, the resolver order, the unenrolled-path rule, and legacy compatibility.
- **Requirements:** R7, R13.
- **Dependencies:** U1, U2, U4.
- **Files:** `docs/configuration.md`, `web/src/pages/configuration/index.astro`, `docs/features/coordination.md`.
- **Approach:**
  1. Extend the Capacity paragraph and the settings-file shape in `docs/configuration.md` and the mirrored web page, including the R4 counting change and the R7 independent-clone rule.
  2. Add a `execution.project-capacity` row to the coordination map, driver `manual`, naming the hand exercise and the CLI evidence capture; do not claim automation.
- **Patterns to follow:** the existing `execution.*` rows.
- **Test scenarios:** Test expectation: none -- documentation.
- **Verification:** `mise run verify` passes (the web build and the feature-map parse are part of it).

### U6. Hand exercise, evidence, and golden pins

- **Goal:** Observable proof for the changed user path, and regenerated pins named for review.
- **Requirements:** R4, R11, R12.
- **Dependencies:** U1 through U5.
- **Files:** `go/internal/cli/testdata/settings-show-populated.stdout` and any other pin whose fixture declares capacity; `.artifacts/evidence/` (ignored).
- **Approach:**
  1. Run the lab exercise named in U1 through U4 against a temporary state home through the built helper and the fake Herdr.
  2. Capture before/after CLI transcripts with `.agents/skills/evidence/` for `dispatch.harness-argv` (its implementation list names the changed files) and for the new `execution.project-capacity` scenario, with visual proof not applicable.
  3. Regenerate only the pins the `effective` list moves; list them in the handoff.
- **Test scenarios:** Test expectation: none -- KTD7.
- **Verification:** `python3 .agents/skills/verify/scripts/verify_run.py --base e4c0daaeaa495a184abd2d858bb4c415632952f5 --json` on the clean final commit reports `pass`.

---

## Verification Contract

| Check | Command | Proves |
| --- | --- | --- |
| Lint | `mise run lint` | gofmt and vet over `go/` |
| Suites | `mise run test` | existing Go contracts, including the capacity message assertions |
| Aggregate | `mise run verify` | Go suite plus the web site build that embeds the configuration page |
| Contract run | `python3 .agents/skills/verify/scripts/verify_run.py --base <base> --json` | immutable run record for the handoff |
| Evidence | `.agents/skills/evidence/SKILL.md` capture and compare | before/after CLI transcripts for the two scenarios |

---

## Definition of Done

- Every requirement R1 through R13 is observable in the hand exercise or the docs.
- `mise run verify` is green on the final commit and the contract run is attached to the handoff.
- Two evidence comparisons exist for the candidate SHA.
- No test, fixture, or comment was added; regenerated pins are named in the handoff and the PR body states that the issue's test checkbox was skipped on the user's rule and discloses the R4 counting change.
- No dead-end code remains in the diff.
