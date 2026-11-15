# Decision ledger

The live log is `/workspace/sum/decisions.md` on the shared computer.
This file is the schema.
Copy the header once when the live file is missing.
Leave a live file that already has lines alone.

Only the coordinator appends.
Workers may read.

The question file under the task remains the record of the user's words.
A ledger line is an index so a later chat can check before asking again.
A ledger line is approval only when the user actually gave it.

## Check before asking

Before you save a question, read the live file.
If a standing ruling covers the choice, apply it and do not ask again.
If the line is ambiguous, read the question file it names.
A new explicit user instruction overrides an older ruling.
Append the newer line.
Do not edit or delete the older one.

## When to append

Append one line when the user answers a question, and when the user states a standing rule.
Use their actual words in the ruling.
Keep the question key so the line points at the file.

## Line

`YYYY-MM-DD | task or standing | key | ruling | rationale`

`standing` is the task column when the ruling is not tied to one task.

## Header to copy

```markdown
# Decisions

Append-only. Do not edit or delete a line. A newer line supersedes an older one on the same key.

| Date | Task | Key | Ruling | Rationale |
| --- | --- | --- | --- | --- |
```
