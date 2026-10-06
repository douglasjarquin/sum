# Action log

The live log is `/workspace/sum/action-log.md` on the shared computer.
This file is the schema.
Copy the header once when the live file is missing.
Leave a live file that already has lines alone.

Only the coordinator appends.
Workers may read.
A worker about to push, open a pull request, or write outside the task files tells the coordinator first.
The coordinator appends the line, with actor `worker:<task id>`, before the worker continues.

Append one line before the side effect.
Append a second line after, with the same intent and target.
Do not edit or delete a line.

## What counts

An external side effect is a worker dispatch message, a draft create, a pull request open or update, a digest write, or any send, publish, or delete.
Reading files, a status check, and a recap are not side effects.

## Line

`YYYY-MM-DDTHH:MM:SSZ | actor | intent | target | task | status | undo`

- actor is `coordinator`, or `worker:<task id>` when the coordinator is logging a worker's side effect
- intent is a short verb, such as `dispatch`, `open-pr`, `draft-email`, or `write-digest`
- target is who or what it hits
- task is the task id, or `-` when there is none
- status is `intended`, `done`, `failed`, or `skipped-duplicate`
- undo is how to reverse it when you know, or `-`

## Dedupe

Before appending `intended`, read the live file.
If the same intent and target has `intended` or `done` in the last six hours, append `skipped-duplicate` and do not act.
`failed` does not block a later try.
An explicit user instruction to do that action again is not a duplicate.
A repeated native message is not that instruction.

## Cap

If the same target already has three `intended` or `done` lines dated today, stop and ask the user before another.
The cap does not allow a gated action.

## Header to copy

```markdown
# Action log

Append-only. One line per status. Do not edit or delete a line.

| When | Actor | Intent | Target | Task | Status | Undo |
| --- | --- | --- | --- | --- | --- | --- |
```
