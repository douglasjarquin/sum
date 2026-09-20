---
name: recap
description: Recap this session from saved history only. Use when the user asks for a session recap or status of what already happened.
---

# Recap

## When to use it

The user asks for a session recap or a status of what already happened.
Load Recap by name.
This is a history-only recap.
Do not invent live fleet state.

## Required inputs and access

Saved files under `/workspace/sum/`: `inbox.md`, task briefs, questions, reports, and verification files.
Do not poll workers or GitHub to fill gaps.

## Sequence of work

1. Read `/workspace/sum/inbox.md` and the open task files.
2. Recap only what those files already record: questions, answers, results, and verification.
3. If a file is missing, say it is missing.
   Do not guess what a worker is doing now.
4. Do not dispatch work, answer a question, or invent live fleet state.

## How to validate the result

Every claim in the recap points at a saved file.
Nothing in the recap is live worker or fleet state you did not read from disk.

## What to return

A short history-only recap of this session from the saved files.
Then stop.

## What requires approval

Do not treat a recap as approval to start work.
Work still starts only from an explicit user instruction or an already-approved task.
