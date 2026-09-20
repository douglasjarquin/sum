---
name: cheap-routines
description: Put standing sweeps on a dedicated cheap-routines worker plus routine. Use for inbox digests and similar repeating work, not for the weekday Inbox rundown.
---

# cheap-routines

## When to use it

Standing sweeps such as inbox digests would otherwise hang timers on the coordinator chat.
Use a dedicated cheap-routines worker plus routine instead.
The weekday Inbox rundown in `routines.md` stays owned by the coordinator Bot.
The coordinator remains the user-facing liaison.

## Required inputs and access

A worker Bot that can own one standing sweep at a time.
The Persist skill, so the sweep has a task id under `/workspace/sum/`.
A Grok Bot routine or integration event for that sweep.

## Sequence of work

1. Sign on or reuse a dedicated cheap-routines worker.
   Do not hang the sweep on the coordinator chat.
2. Prefer event listeners only for integration-backed events.
   For local-file sweeps such as `/workspace/sum/inbox.md`, use the coarsest useful schedule.
   Treat that schedule as the preferred path, not a failure to find a listener.
3. Keep the cadence the coarsest useful cadence.
4. The coordinator relays outcomes to the user.
   The cheap-routines worker does not message the user.

## How to validate the result

The standing sweep is not a timer on the coordinator chat.
The coordinator is still the liaison.
Local-file sweeps use a coarse schedule.

## What to return

Which worker owns the sweep, the cadence or event, and the task id.
Then stop.

## What requires approval

Enabling a write-capable routine.
Messaging anyone except the coordinator.
Changing the weekday Inbox rundown owner away from the coordinator Bot.
