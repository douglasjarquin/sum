# Coordinator durability audit

Compared the five improvements in CS-SUM-IMPROVEMENTS-001 with the Grok Bot pack (`grok-bots/sum/`), `GROK_SUM.md`, and the terminal coordinator (`COORDINATOR.md`, `skills/`, `templates/`). Square (`grok-bots/square/`) has no coordinator role. It is unchanged.

Task questions that already have a home stay there. The new files are an index and a gate, not a second authority and not a daemon.

## 1. Decision ledger

**Grok: PARTIAL.** Persist already writes `/workspace/sum/tasks/<id>/questions/<key>.md` and records the user's actual answer (`grok-bots/sum/skills/persist/SKILL.md`, `grok-bots/sum/instructions.md`). Nothing checked those answers before the next question, and nothing survived as a standing ruling across tasks.

**Terminal: PARTIAL.** `sumctl ask`, `sumctl answer`, and `context --section decisions` already store task questions under `.sum/tasks/` (`COORDINATOR.md`, `skills/sum-status/SKILL.md`, `skills/sum-work/SKILL.md`). `templates/preferences.md` is narrative preference text. It is not a ruling log. `sumctl backup` already archives task records and `preferences.md`.

**Added.** `grok-bots/sum/decisions.md` (schema; live file `/workspace/sum/decisions.md`) and `templates/decisions.md` (copy to `.sum/decisions.md`). Persist, Status, instructions, and the coordinator rules say: read the ledger before asking; append one line when the user answers or states a standing rule; do not edit older lines. Workers may read. Only the coordinator appends. Terminal task records remain the question text. The ledger is the standing index.

## 2. Action log, write-before-act, light dedupe

**Both: MISSING** as a coordinator log.

Nearby behavior that this does not replace:

- Grok dispatch already refuses a second execution when a native message arrives twice (`grok-bots/sum/skills/dispatch/SKILL.md`).
- Terminal repair sends use one stable key (`skills/sum-dispatch/SKILL.md`). Uncertain delivery is not resent (`skills/sum-status/SKILL.md`). Factory merge does not retry on its own (`skills/sum-dispatch/references/factory-merge.md`).

**Added.** `grok-bots/sum/action-log.md` and `templates/action-log.md`. Before an external side effect, append `intended`, then append `done`, `failed`, or `skipped-duplicate`. Same intent and target inside six hours is skipped unless the user explicitly asks again. Three `intended` or `done` lines for one target on the same calendar day stop the next one. Dispatch and delivery skills call this out. Reading status is not a side effect. The log does not grant a gated action.

## 3. Standing permissions

**Both: PARTIAL.** Gates already exist in prose: the user merges, the Bot never merges, draft delivery stops short of merge, capacity and repair grants are the user's, and factory merge-check is high only on an authorized repository list (`COORDINATOR.md`, `skills/sum-deliver/SKILL.md`, `skills/sum-dispatch/references/factory-merge.md`, `grok-bots/sum/instructions.md`, `grok-bots/sum/skills/deliver/SKILL.md`, `grok-bots/sum/skills/factory/SKILL.md`). There was no single auto / draft / gated matrix.

**Added.** `grok-bots/sum/permissions.md` and `templates/permissions.md`. Auto is read and reconcile. Draft is already-approved dispatch, task files, and opening a pull request or other draft. Gated is send, publish, delete, spend, force-push, and merge. Grok stays "the Bot never merges," including factory pieces. Terminal keeps the existing factory exception and does not widen the repository list. Draft still stops before send.

## 4. Weekday morning Inbox and ops brief

**Grok: PARTIAL.** `grok-bots/sum/routines.md` already has a weekday 09:00 Inbox status, armed only after two successful manual Status checks. `GROK_SUM.md` step 7 still says not to enable routines during install. The expected result was only unanswered questions, unverified reports, and failures. Digests were not specified as one chat line. Nothing in the repo turns the routine on.

**Terminal: MISSING** as a brief shape. `skills/sum-status/SKILL.md` already says there is no periodic digest. Status already surfaces unanswered decisions, reports, failures, and cleanup. There is no scheduler in this contract, and this change does not add one.

**Added.** The same Inbox status routine now lists verification still owed, failed repairs, pending approvals, a factory Review count when a confirmed settings file has that lane, up to three priorities already written in files, and digests as one chat line plus the saved link. Empty stays quiet. Enabling is still the user's step after two successful manual Status checks. The terminal status skill documents the same shape when the user asks for the brief. No live routine is enabled.

## 5. Coordinator invariant eval seed

**Both: MISSING** as a fixture seed.

`tests/test_operating_files.py` already locks some prose (coordinator pane does not do the work, merge wording, the two-Status arming gate, history-only recap). Go tests cover factory merge refusal, verification records, and digest cursors. Those are implementation tests, not a coordinator behavior seed.

**Added.** Twenty fixtures in `grok-bots/sum/evals/fixtures.json`, a grading note in `grok-bots/sum/evals/README.md`, and `tests/test_coordinator_invariants.py`. Each fixture has a pass trace and a fail trace (the explicit-retry dedupe case is pass-only on purpose). The grader checks forbidden actions, required actions, and artifact rules: never merge, verify is not log reading, a worker result is a claim, no dual dispatch, digest one-liner, ledger check, write-before-act, dedupe, draft-not-send, and the routine arming gate. A green test means the traces match the rules and the procedure files still contain the anchors. It does not prove a live coordinator follows them.

## Deferred

- Skills packs for Musician, Athlete, and the rest (research B2). Out of scope.
- Cost and fan-out dashboards (research B4 / C6). Out of scope.
- A separate undo log (research C3). The action log has an `undo` column when the reversal is known. A third log is not added.
- Putting `.sum/decisions.md` and `.sum/action-log.md` on the `sumctl backup` allowlist. Task records already travel in a backup. Extending the allowlist is a helper behavior change and is left for a later patch.
- Enforcing the ledger inside `sumctl`. The durable copy is markdown plus the procedure, so a coordinator can follow it without a new daemon.
- Square pack edits. No impact: Square reports to Sum and does not take these coordinator gates.

The Factory skill in the Grok pack named the helper. Pack markdown is not supposed to. That one sentence now says the software factory runs on a Sum installation, and the operating-file check matches the Factory row already in the pack README. The bootstrap check matches the shorter coordinator sentence in `AGENTS.md` and keeps the longer form on `COORDINATOR.md`.
