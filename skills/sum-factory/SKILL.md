---
name: sum-factory
description: Start or run a factory from a markdown settings file. Software and copy differ only in that file. Use when the user asks to start, run, or update a factory.
---

# Factory

A factory is one lane of work. The shared steps are this skill. What changes between software and copy is a markdown settings file.

Read that file for the factory the user named. Do the software procedure only when the file says `runner: software`. Do the written lane actions only when the file says `runner: directive`.

## Where the file is

Look in this order:

1. `/workspace/sum/factories/<name>.md` on the shared computer, when that file exists. That copy is the one the user confirmed.
2. The `<name>.md` file under `skills/sum-factory/factories/` in this repository. That copy is the recommendation.

`<name>` is `copy`, `software`, or a name the user chose. Match it without caring about case.

## Start

The user asked to start a factory. Ask one question at a time. Save the answer before the next question. Recommend from a file or a page you actually read.

1. Where does this factory live? Text files, or a Notion page? If they name a page, fetch it.
2. What are the lanes, in order? On a Notion floor, read the status options and say the names you saw. Ask them to confirm that order.
3. Which lanes does a pass pick up, and what should happen there? Write the action in their words. Ask which lanes are theirs alone.

When they said "copy" and the recommendation file exists, use it as the recommendation. Still ask. If the page shows different lanes than the file, say what you saw and ask which order to keep.

Write the confirmed answers to `/workspace/sum/factories/<name>.md` in the shape below. Read it back. A later run uses that file.

## Settings shape

```markdown
# <Name> factory

runner: directive
floor: notion | text
floor-ref: <url or path>

## Lanes
<one per line, in flow order>

## Pickup
- <lane>
- <lane>, only when <condition>

## Actions
### <lane>
<what the worker does, in order>

## Never
<what the worker must not do>
```

`runner: software` means the file is a pointer. Follow `skills/sum-dispatch/references/factory.md`.

## Run

The user asked to run a factory, and the settings file is confirmed.

- `runner: software`: follow `skills/sum-dispatch/references/factory.md`. One tick. Do not stay in a loop.
- `runner: directive`: do not do the lane work in the coordinator pane. Dispatch one worker. The brief is the settings file plus the piece or path they named. The worker does only the pickup lanes, then stops. On a Notion floor, a piece URL is the reliable trigger. Search does not see a status property.

## Update

When the user asks Sum to update itself, follow `skills/sum-update/SKILL.md` on an installation. On Grok Bot, refresh `https://github.com/douglasjarquin/sum` and re-read `GROK_SUM.md`. The factory feature arrives with that refresh. Do not paste this skill into a bot description.
