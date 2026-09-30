---
name: factory
description: Start or run a factory from a markdown settings file. Use when the user asks to start a factory, run one, or say where a factory lives.
---

# Factory

## When to use it

The user asked to start a factory, run a factory, or tell you where one lives.
Software and copy use this same skill.
The difference is a markdown settings file.

## Required inputs and access

The Factory settings in the Sum clone at `skills/sum-factory/factories/<name>.md`.
A confirmed copy, when one exists, at `/workspace/sum/factories/<name>.md`.
Notion, when the floor is a Notion page.
Spiral, when the confirmed settings say to call it.
The Dispatch and Persist skills, once a run is actually approved.

## Sequence of work

Ask one question at a time. Save the answer under `/workspace/sum/factories/` before you ask the next. Recommend from a file or a page you read. Do not invent lanes or tools.

1. Name the factory. `copy` and `software` already have recommendation files.
2. Where does it live? Text files, or a Notion page? If they name a page, fetch it.
3. What are the lanes, in order? On Notion, read the status options and say the names you saw. Ask them to confirm that order.
4. Which lanes does a pass pick up, and what should happen there? Ask which lanes are theirs alone.

For copy, read `skills/sum-factory/factories/copy.md` in the clone and offer it as the recommendation. Still ask. If the Notion page shows different lanes, say what you saw and ask which order to keep.

Write the confirmed answers to `/workspace/sum/factories/<name>.md`. Read the file back.

A run comes after that confirmation, as its own request.

- `runner: software` means follow the software settings file. That lane is Sum's `sumctl` factory. Do not tick it from this chat. Tell the user it runs on a Sum installation, and dispatch a worker only when they asked you to run it there.
- `runner: directive` means dispatch one worker. The brief is the confirmed settings file plus the piece URL or path they named. The worker does only the pickup lanes, then stops. You do not call Spiral in this chat.

On a Notion floor, a piece URL is the reliable trigger. Search does not see a status property.

## How to validate the result

The settings file exists and matches what the user confirmed.
A start did not call Spiral and did not move a piece.
A run left the coordinator chat free of the lane work.
The worker report names the piece, the command, and the status it set.

## What to return

On a start: the settings path, the lanes you saw, and the action they confirmed.
On a run: the task id, the worker, and the piece or issue it took.
Then stop.

## What requires approval

Starting a factory, confirming the settings, and each run.
A recommendation file is not approval.
A piece sitting in Ready is not approval until they ask for a run.
Do not post. Do not set Approved. The user merges. The Bot never merges.
