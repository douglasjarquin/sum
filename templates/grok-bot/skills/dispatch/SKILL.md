---
name: dispatch
description: Hand approved work to one worker Bot. Use when the user asks for research, planning, investigation, or implementation.
---

# Dispatch

## When to use it

The user gave an explicit approved request.
Research, planning, investigation, and implementation all go through this skill.
Do not use it to do that work in the coordinator chat.

## Required inputs and access

The Persist skill, so the brief exists at `/workspace/sum/tasks/<id>/brief.md`.
The worker procedure memory.
A worker Bot whose worker procedure matches the project, or the ability to sign one on.
Cursor Cloud Agents available to that worker for isolated code work, when the task is software.

## Sequence of work

1. Write the brief from the user's words.
   Do not investigate first to fill gaps.
   Name the repository, the outcome, the scope, and how to check the result.
2. Persist the brief under a new task id.
3. Sign on a worker Bot when no existing one fits.
   Reuse one whose worker procedure already matches.
   Write the worker procedure memory into that Bot's description.
   Add that it reports outcomes and blockers to you against the task id, never to the user.
4. Message the worker with the task id and the brief path.
   Ask for the outcome back against that id.
5. Tell the user which worker started, what it will deliver, and the task id.
6. End the turn.

## How to validate the result

`/workspace/sum/tasks/<id>/brief.md` exists and matches the user's words.
The worker Bot description says it is not the coordinator.
The coordinator chat contains no research, planning, investigation, or implementation of the request.

## What to return

The task id, the worker Bot, and the outcome the user should expect.
Then stop.

## What requires approval

Work starts only from an explicit user instruction or an already-approved task.
An unapproved issue is not approval.
Do not start a second execution because a native message arrived twice.
