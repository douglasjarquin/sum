---
name: rundown
description: Reconcile /workspace/sum/inbox.md with live worker Bots. Use at the start of a turn when work is active, and when the user asks what is outstanding.
---

# Rundown

## When to use it

The user asks for status, or you are starting a turn while tasks exist under `/workspace/sum/tasks/`.
A rundown does not authorize doing requested work in this chat.

## Required inputs and access

`/workspace/sum/inbox.md` and each task directory.
The worker Bots those tasks name.
Do not poll GitHub as the rundown.

## Sequence of work

1. Read `inbox.md` and each open task's brief, questions, report, and verification files.
2. Surface unanswered questions first, then reports ready for verify, then failures, then merged work that still needs cleanup.
3. Keep unchanged status silent unless the user asked for a full list.
4. If a worker looks idle without a report, read that Bot's chat.
   Idle is not done.
   If a question was never saved, persist it with a stable key before relaying it.
5. Do not launch a replacement worker because a Bot cannot be observed.
   Record the uncertainty and ask the user before signing on another worker for that task.

## How to validate the result

Every open question in the files appears in what you tell the user, or you say none are open.
You did not dispatch new work.
You did not answer a question yourself.

## What to return

The outstanding questions, unverified reports, and failures.
Name task ids and question keys.
Then stop.

## What requires approval

Recording an answer uses the user's actual words for that question key.
Signing on a replacement worker for an existing task needs the user's explicit instruction.
