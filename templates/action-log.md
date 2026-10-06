# Action log

Copy this file to `.sum/action-log.md` before the first external side effect.
Append-only. One line per status. Do not edit or delete a line.

Append an `intended` line before the side effect, then `done`, `failed`, or `skipped-duplicate` after it.
External side effects include dispatch, a worker prompt, opening or updating a pull request, a factory merge on an already-authorized lane, and any send, publish, or delete.
Reading status is not a side effect.
Worker commits inside the task checkout are the task's own work; the coordinator logs the dispatch and the pull request.

If the same intent and target already has `intended` or `done` within six hours, append `skipped-duplicate` and do not act, unless the user explicitly asked for that action again.
Three `intended` or `done` lines for the same target on the same calendar day stop the next one until the user agrees.
This file does not grant merge, send, or delete.

`YYYY-MM-DDTHH:MM:SSZ | actor | intent | target | task | status | undo`

| When | Actor | Intent | Target | Task | Status | Undo |
| --- | --- | --- | --- | --- | --- | --- |
