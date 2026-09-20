# Square Grok Bot pack

This directory is the source-controlled recipe for a Square steward Bot.
It is not a live share URL.

Tell any Bot in Grok Bot to follow [`GROK_SQUARE.md`](../../GROK_SQUARE.md) at the repository root.
That file is the installer.
The Bot clones this public repository onto the shared Grok Bot computer and loads the files below.
You do not paste them by hand.
You do not need a SUM installation.

Square takes commands from Sum.
Install Sum from [`templates/sum/`](../sum/) with [`GROK_SUM.md`](../../GROK_SUM.md) if it is not already on the roster.

## Pack files

| File | Role |
| --- | --- |
| `instructions.md` | Square Bot description |
| `memories.md` | Memories included in the template |
| `worker-procedure.md` | Memory named Steward procedure |
| `routines.md` | Cleanup, Git backup, and Org review, left paused until two clean manual runs |
| `avatar.png` | Square profile image |

There are no pack-local skills.
Square uses shared `/workspace/skills/` packs already on the computer.

Do not put helper commands, instance ids, or Bot ids in those files.
This recipe is native to Grok Bot.

## Prove it once

After the installer finishes, ask Sum to give Square one reversible steward task.
Confirm Square does the steward work and does not take over coordination.
Confirm Square reports to Sum against a task id and does not message the user unless Sum asks.
Confirm control-plane notes land under `/workspace/square/`.
The user merges if anything is published.
The Bot never merges.

## Share as a template

After one good run, Square can be shared as a Grok Bot template.
Inspect the draft.
Strip API keys, internal URLs, instance ids, and anything you would not put in a public document.
Confirm the draft does not attach another author's share URL.
This recipe is Square.
Keep this directory as the source of truth when the steward contract changes.

## Suggested routines

After two clean manual runs of each, create the routines in `routines.md` and leave them paused until a test run looks right.
