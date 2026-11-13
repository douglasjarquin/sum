---
name: Sum Dispatch
description: Hand approved work to one worker Bot. Use when the user asks for research, planning, investigation, or implementation.
---

# Dispatch

## When to use it

The user gave an explicit approved request.
Research, planning, investigation, and implementation all go through this skill.
Do not use it to do that work in the coordinator chat.

## Required inputs and access

The Sum Persist skill, so the brief exists at `/workspace/sum/tasks/<id>/brief.md`.
The worker procedure memory.
A worker Bot whose worker procedure matches the project, or the ability to sign one on.
Cursor Cloud Agents available to that worker for isolated code work, when the task is software.

## Sequence of work

1. Write the brief from the user's words.
   Do not investigate first to fill gaps.
   Name the task kind, outcome, allowed edits/actions, acceptance criteria, verification evidence, and concrete coordinator identity for returns.
   Code briefs name repository/base/candidate and required checks; non-code briefs name artifact/source/object identities instead.
   Record the persistent/pre-existing or explicitly temporary worker lifetime and installed role revision.
   Record originating conversation/surface, acting user, supported return conversation, assigned worker identity, report_destination, expected output, and Cloud run references when applicable.
   Use `/workspace/sum/tasks/<id>/report.md` by default; a specialist may own another exact path recorded here, which Persist/Verify/Deliver must consume.
   Establish that native delegation is supported on the originating surface before dispatch.
   Personal Bots can delegate from the app or their user's Slack DM, with returns in the app chat; a personal Bot cannot delegate from a Slack channel thread.
   For that unsupported channel case, direct the user to Sum's app chat or an authorized shared Bot in Slack; do not pretend a handoff occurred.
   Shared Bots in Slack can hand off to the acting teammate's own Bots and return to that originating Slack thread.
   Check observed native behavior for any other surface rather than guessing a route.
   Preserve the actual acting user and thread identity, not only the coordinator Bot ID.
2. Persist the brief under a new task id.
3. Reuse existing role workers (Marketing, Security, Personal, Operations, Square, and similar) for matching non-software work.
   Only sign a new Sum worker when none fits.
   Do not recreate Square/Cleaner or Atlas.
   Reuse a software worker whose worker procedure already matches, or sign one on when none fits.
   Confirm native identity, ownership, availability, and role revision from installation.md; matching display names are insufficient.
   Never overwrite an active assignment: one approved task runs per worker; explicitly queue a next assignment or choose an available qualified worker.
   Keep persistent specialist responsibilities, restrictions, and learning; send the task assignment separately through native messaging.
   Use supported personal chat/memory or authorized shared description updates only at safe role boundaries.
   Never overwrite Square's steward protections with generic worker rules.
   Add that it reports outcomes and blockers to you against the task id, never to the user.
   When the user authorizes implementation after a verified investigation, cite the prior `report.md` / task id in the brief and forbid redoing the investigation.
4. Use the canonical `action-log.md` operation/attempt rules before messaging.
   Persist the task/operation/native-target/revision tuple, log intended, then record the actual result.
   Reuse the operation ID for duplicate/delayed messages and confirmed-failure retries.
   Reconcile uncertain effects before another attempt; a send response alone does not prove receipt.
5. Tell the user which worker started, what it will deliver, and the task id.
6. End the turn.

## How to validate the result

`/workspace/sum/tasks/<id>/brief.md` exists and matches the user's words.
The worker's acknowledged role says it is not the coordinator and preserves its specialist restrictions.
The coordinator chat contains no research, planning, investigation, or implementation of the request.

## What to return

The task id, the worker Bot, and the outcome the user should expect.
Then stop.

## What requires approval

Work starts only from an explicit user instruction or an already-approved task.
An unapproved issue is not approval.
Do not start a second execution because a native message arrived twice.
