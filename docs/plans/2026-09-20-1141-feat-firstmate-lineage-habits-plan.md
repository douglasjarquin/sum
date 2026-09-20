---
title: Firstmate-lineage coordinator habits - Plan
type: feat
date: 2026-09-20
artifact_contract: ce-unified-plan/v1
product_contract_source: ce-plan-bootstrap
execution: code
origin: https://github.com/douglasjarquin/sum/issues/185
---

# Firstmate-lineage coordinator habits - Plan

## Goal Capsule

- **Objective:** A fresh Grok Bot install of this pack loads Sitdown, cheap-routines, and adversarial-review the same way it loads Dispatch / Persist / Rundown / Verify / Deliver, and the coordinator follows the issue 185 adopt habits (history-only recap, armed Inbox rundown, standing-sweep worker, per-bot secrets, learning notes, role-worker reuse, default independent review, cite prior investigation).
- **Means:** Encode each adopt item as explicit coordinator-facing sentences in `templates/grok-bot/` plus three Sum-native pack skills the installer loads by name, locked by `tests/test_operating_files.py` against the shipped files (KTD1–KTD3).
- **Authority:** Issue 185 adopt list, then this plan, then existing pack heading and dictionary tests.
- **Stop:** Do not add Lavish, forge-agnostic Deliver, or always-reply restatement. Do not paste Firstmate themed copy. Do not write the word `charter`. Do not edit live `.sum/` state or run the live Grok Bot canary.
- **Execution profile:** Lightweight pack-text change. Proof is string tests on shipped files, not a live Bot install.
- **Who finishes:** Implementer lands the pack and tests. User merges the PR.

## Product Contract

### Summary

The Grok Bot pack teaches the coordinator the Firstmate-lineage habits issue 185 adopted, without importing Firstmate vocabulary or changing the Herdr/`sumctl` coordinator path.

### Problem Frame

Issue 185 records verified adopt suggestions from audit t-4.
The pack today lists only Dispatch / Persist / Rundown / Verify / Deliver, and it does not state the sitdown, inbox-arming, cheap-routines, secret-routing, learning-notes, role-worker, adversarial-review, or cite-prior-investigation rules.

### Key Decisions

- Sitdown, cheap-routines, and adversarial-review ship as pack skills the installer loads by name. (session-settled: user-approved — chosen over leaving those names as unshipped Firstmate disk skills: a fresh Grok Bot install must load them the same way it loads Dispatch.) Governs R1, R2, R5, R6, R12.
- Adopt items 1–7 and 9 only. (session-settled: user-approved — chosen over items 8 Lavish, 10 forge-agnostic Deliver, and 11 always-reply restatement: issue 185 marks those out of scope.) Governs R1–R12.
- Write "non-software work", never `charter`. (session-settled: user-approved — chosen over copying issue 185's "charters" wording: `tests/test_operating_files.py` rejects `charter`.) Governs R9, R13.
- Tests read the shipped pack and installer, not copies. (session-settled: user-approved — chosen over asserting against fixtures that could drift: a green suite that never asserts the new phrases does not satisfy the work.) Governs R14.

### Requirements

**Recap and standing work**

- R1. A user ask for a session recap or "sitdown" loads Sitdown by name.
- R2. Sitdown is a history-only recap and does not invent live fleet state.
- R3. After two successful manual Rundowns, the weekday Inbox rundown routine is enabled.
- R4. A scheduled Inbox rundown with an empty inbox stays quiet.
- R5. Standing sweeps other than the weekday Inbox rundown (inbox digests and similar) use a dedicated cheap-routines worker plus routine at the coarsest useful cadence. Prefer event listeners only for integration-backed events. For local-file sweeps such as `inbox.md`, use the coarsest useful schedule and treat that as the preferred path, not a failure to find a listener.
- R6. The coordinator remains the user-facing liaison for those sweeps. The weekday Inbox rundown in `routines.md` stays owned by the coordinator Bot (R3).

**Secrets, learning, dispatch**

- R7. Secrets are per-bot; workers request their own secret cards; the coordinator never holds, pastes, or forwards secrets in chat or in worker-description amendments, and does not keep work in the coordinator chat to avoid a handoff.
- R8. After verified fails or repeated worker mistakes, the coordinator may amend that worker's description with short learning notes (still one task at a time; workers still do not message the user). Learning notes never include secrets or secret-card values.
- R9. Dispatch reuses existing role workers (Marketing, Security, Personal, Operations, Square, and similar) for matching non-software work and only signs a new Sum worker when none fits.
- R10. Dispatch does not recreate Square/Cleaner or Atlas.
- R11. When the user authorizes implementation after a verified investigation, the Dispatch brief cites the prior `report.md` / task id and forbids redoing the investigation.

**Deliver and installer**

- R12. For software Deliver after Verify, the default is a fresh adversarial-review (or equivalent independent reviewer) before opening or updating the PR; if that review is skipped, the coordinator says so; the user still merges.
- R13. Pack files and the installer contain no `sumctl`, no themed role titles, and no `charter`.
- R14. `tests/test_operating_files.py` fails on a pack that omits any of R1–R12's must-have terms, still requires the six skill headings, and still scans every pack markdown file including new skills.
- R15. `GROK_SUM.md` names every pack skill the instructions tell the coordinator to load by name.

### Scope Boundaries

In scope: `templates/grok-bot/` (instructions, memories, routines, Dispatch, Deliver, new Sitdown / cheap-routines / adversarial-review skills), `GROK_SUM.md`, `tests/test_operating_files.py`, and `docs/features/coordination.md` row `grok-bot.static` if the scenario text must name the new habits.

Out of scope: issue 185 items 8, 10, and 11; casino/boss theming; `factory.db` as source of truth; triage auto-merge; Bot merges; recreating Firstmate or Consigliere; changing `AGENTS.md` / `skills/sum-dispatch` / `skills/sum-delivery` except a Group 1 dictionary alignment; live Grok Bot canary; copying Firstmate skill text.

### Acceptance Examples

- AE1. Covers R1, R2. Given a fresh install, when the user asks for a sitdown, the coordinator loads Sitdown by name and recaps saved history only.
- AE2. Covers R3, R4. Given two successful manual Rundowns, the weekday Inbox rundown is enabled and stays silent when `/workspace/sum/inbox.md` is empty.
- AE3. Covers R9, R10, R11. Given a verified investigation and a later "implement this" ask, Dispatch cites that `report.md` / task id, forbids redoing the investigation, and reuses Marketing/Security/Personal/Operations/Square when the work matches rather than signing a new software worker or recreating Square/Cleaner or Atlas.
- AE4. Covers R12. Given software Deliver after Verify, the coordinator runs a fresh adversarial-review (or equivalent independent reviewer) before opening or updating the PR, or says the review was skipped; the user still merges.

## Planning Contract

### Key Technical Decisions

- KTD1. Add three pack skills under `templates/grok-bot/skills/{sitdown,cheap-routines,adversarial-review}/SKILL.md`, each with the six existing skill headings, written in Sum-native Group 1 language from the issue's must-have terms. (session-settled: user-approved — chosen over pointing at Firstmate disk skills: those files are not in this repo and use themed titles.) Instantiates R1, R2, R5, R6, R12.
- KTD2. Put the remaining adopt sentences in the existing coordinator-facing files (`instructions.md`, `memories.md`, `routines.md`, `README.md`, `skills/dispatch/SKILL.md`, `skills/deliver/SKILL.md`) rather than a new memory file. Instantiates R3, R4, R7–R12.
- KTD3. Extend `tests/test_operating_files.py` so it reads those shipped paths and asserts the must-have phrases; add the new skill paths to the recipe and heading scan. Absence of Lavish, forge-agnostic Deliver, and always-reply restatement is an absence assert on shipped files. The merge-base pack diff stays a Verification Contract row, not a unittest. Instantiates R13–R15.
- KTD4. Keep Deliver GitHub-only. Do not add Lavish, forge-agnostic forge wording, or an always-reply restatement. Instantiates R12's out-of-scope companion.
- KTD5. Weekday Inbox rundown stays owned by the coordinator Bot. cheap-routines owns other standing sweeps. Two successful manual Rundowns is pack-text / conversation judgment, not a durable counter file. Instantiates R3, R5, R6.

### Assumptions

- cheap-routines is a named pack skill plus coordinator rule, not a Grok Bot platform primitive this change implements.
- Learning notes are short text on that worker Bot's description, not a new on-disk file type.
- Update the `grok-bot.static` row only if the current scenario text is stale relative to the new habits. The existing generic operating-contract row may already suffice.

### Sequencing

U1 (skills + installer names) then U2 (existing pack sentences) then U3 (tests + feature-map row).
U3's assertions can be written first so U1 and U2 have a failing target.

## Implementation Units

### U1. Pack skills and installer load-by-name

- **Goal:** A fresh install can load Sitdown, cheap-routines, and adversarial-review by name.
- **Requirements:** R1, R2, R5, R6, R12, R13, R15. KTD1.
- **Dependencies:** none
- **Files:**
  - `templates/grok-bot/skills/sitdown/SKILL.md` (create)
  - `templates/grok-bot/skills/cheap-routines/SKILL.md` (create)
  - `templates/grok-bot/skills/adversarial-review/SKILL.md` (create)
  - `GROK_SUM.md` (modify)
  - `templates/grok-bot/README.md` (modify if it lists skills)
- **Approach:**
  1. Write each skill with YAML `name`/`description` and the six headings already required of Dispatch and Deliver.
  2. Sitdown: load on recap/"sitdown"; recap saved history under `/workspace/sum/` only; do not invent live fleet state.
  3. cheap-routines: dedicated worker plus routine for standing sweeps other than the weekday Inbox rundown; coarsest useful cadence; event listeners only for integration-backed events; local-file sweeps such as `inbox.md` use the coarsest useful schedule; coordinator stays the liaison.
  4. adversarial-review: independent reviewer in a fresh context after Verify, before opening or updating the PR; say so if skipped; user still merges.
  5. Point `GROK_SUM.md` at every pack skill the instructions will name; stop saying "five global workflows" if the count is no longer five.
- **Patterns to follow:** `templates/grok-bot/skills/dispatch/SKILL.md` and `templates/grok-bot/skills/deliver/SKILL.md` for heading shape and tone.
- **Execution note:** Keep every sentence free of `sumctl`, themed titles, and `charter`.
- **Test scenarios:** Covered by U3 assertions against these files. This unit creates the files those tests will read.
- **Verification:** Each new skill file has all six headings. `GROK_SUM.md` lists Sitdown, cheap-routines, and adversarial-review beside Dispatch / Persist / Verify / Rundown / Deliver.

### U2. Coordinator-facing adopt sentences

- **Goal:** The coordinator description, memory, Dispatch, Deliver, and Inbox routine state the remaining adopt rules in the issue's must-have terms.
- **Requirements:** R1, R3, R4, R7–R12. KTD2.
- **Dependencies:** U1
- **Files:**
  - `templates/grok-bot/instructions.md`
  - `templates/grok-bot/memories.md`
  - `templates/grok-bot/routines.md`
  - `templates/grok-bot/README.md`
  - `templates/grok-bot/skills/dispatch/SKILL.md`
  - `templates/grok-bot/skills/deliver/SKILL.md`
- **Approach:**
  1. Instructions Skills list: add Sitdown (recap/"sitdown"), cheap-routines (standing sweeps other than weekday Inbox rundown), adversarial-review (software Deliver after Verify). Keep "Load by name."
  2. Instructions or memories: secrets per-bot; workers request their own secret cards; coordinator never holds, pastes, or forwards secrets in chat or in worker-description amendments; do not keep work in the coordinator chat to avoid a handoff; learning notes on that worker's description after verified fails or repeated mistakes, never including secrets or secret-card values.
  3. Routines and README: after two successful manual Rundowns, enable the weekday Inbox rundown; stay quiet when the inbox is empty. Two successful runs is conversation / pack-text judgment, not a durable counter file. Rewrite README so it no longer says to leave the routine paused after those two runs. Leave `GROK_SUM.md` install-time "Do not enable routines yet."
  4. Dispatch: reuse Marketing, Security, Personal, Operations, Square, and similar for matching non-software work; only sign a new Sum worker when none fits; do not recreate Square/Cleaner or Atlas; when promoting a verified investigation to implementation, cite the prior `report.md` / task id and forbid redoing the investigation.
  5. Deliver: default a fresh adversarial-review (or equivalent independent reviewer) before opening or updating the PR; say so if skipped; user still merges. Replace the weaker "when the stakes warrant it" default.
- **Patterns to follow:** Existing short imperative sentences in `instructions.md` and Dispatch.
- **Test scenarios:** Covered by U3.
- **Verification:** Each R1 and R3–R12 must-have phrase appears in the shipped file U3 will read.

### U3. Operating-file tests and feature-map row

- **Goal:** A pack that omits any adopt must-have term fails `tests/test_operating_files.py`; the existing `sumctl` / themed-title / six-heading checks still pass on the new files.
- **Requirements:** R13, R14, R15. KTD3, KTD4.
- **Dependencies:** U1, U2
- **Files:**
  - `tests/test_operating_files.py`
  - `docs/features/coordination.md`
- **Approach:**
  1. Add the three new skill paths to `RECIPE_FILES` so existence and heading scans cover them.
  2. Add tests that read the shipped pack and `GROK_SUM.md` (not copies) and assert the must-have terms from R1–R12.
  3. Keep `_grok_bot_markdown()` scanning every pack `.md` so new skills inherit the no-`sumctl` and no-themed-title checks.
  4. Assert the installer still has no `sumctl` and now names Sitdown, cheap-routines, and adversarial-review.
  5. Assert Lavish, forge-agnostic Deliver, and always-reply restatement stay absent from the shipped pack, installer, and test files. Do not add merge-base git logic to the unittest.
  6. Update the `grok-bot.static` row only if the current scenario text is stale.
- **Execution note:** Write the failing assertions first, then confirm U1/U2 make them pass.
- **Patterns to follow:** `test_grok_bot_instructions_encode_the_operating_contract` and `test_grok_sum_installer_clones_public_sum`.
- **Test scenarios:**
  - Happy path: current pack after U1/U2, `python3 -m unittest tests.test_operating_files` passes twice.
  - Edge: drop Sitdown from `instructions.md` or `GROK_SUM.md`; the new test fails.
  - Edge: omit "history-only" / "do not invent live fleet state" from Sitdown; the new test fails.
  - Edge: omit "two successful manual Rundowns" from routines; the new test fails.
  - Edge: omit "workers request their own secret cards" or "never holds, pastes, or forwards secrets"; the new test fails.
  - Edge: omit that learning notes never include secrets; the new test fails.
  - Edge: omit Marketing, Security, Personal, Operations, Square, or "do not recreate Square/Cleaner or Atlas"; the new test fails.
  - Edge: omit `report.md` / task id cite or "forbid redoing the investigation"; the new test fails.
  - Edge: omit default adversarial-review or "say so if skipped"; the new test fails.
  - Error: a new skill missing one of the six headings fails `test_grok_bot_skills_state_the_six_fields`.
  - Error: introducing `charter` or `sumctl` in a pack file fails the existing forbidden-word tests.
  - Integration: `_grok_bot_markdown()` includes the three new skill files.
- **Verification:** The operating-file module is green twice on the candidate and red if any must-have phrase is removed.

## Verification Contract

| Gate | Command | Proves |
| --- | --- | --- |
| Operating-file module | `python3 -m unittest tests.test_operating_files` (run twice) | R1–R15 string locks on shipped files |
| Offline suite | repo verify aggregate or `mise run test` | no regressions in neighboring operating-file checks |
| Diff vs merge base | pack + installer + test diff | items 8, 10, 11 absent |
| PR | open PR linking https://github.com/douglasjarquin/sum/issues/185 | delivery |
| CI | GitHub check-run conclusion on that PR | CI decided |

Do not treat a local re-run of CI commands as the CI gate.
Do not run the live Grok Bot canary.

## Definition of Done

- U1–U3 landed; abandoned draft skill text is not left in the tree.
- Operating-file tests pass twice against the candidate.
- Offline suite that includes those tests has been run once.
- Open PR on `douglasjarquin/sum` links issue 185.
- GitHub check-run on that PR is decided (success, failure, or cancelled).
- Pack diff does not add Lavish, forge-agnostic Deliver, or always-reply restatement.
- User still merges.
