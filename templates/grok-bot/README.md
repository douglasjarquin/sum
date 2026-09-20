# Sum Grok Bot pack

This directory is the source-controlled recipe for a Sum coordinator Bot.
It is not a live share URL.

Tell any Bot in Grok Bot to follow [`GROK_SUM.md`](../../GROK_SUM.md) at the repository root.
That file is the installer.
The Bot clones this public repository onto the shared Grok Bot computer and loads the files below.
You do not paste them by hand.
You do not need a SUM installation.

## Pack files

| File | Role |
| --- | --- |
| `instructions.md` | Coordinator Bot description |
| `memories.md` | Memories included in the template |
| `worker-procedure.md` | Memory named Worker procedure |
| `skills/*/SKILL.md` | Skills named Dispatch, Persist, Verify, Rundown, Recap, Deliver, and Sweep |
| `routines.md` | Weekday Inbox rundown, enabled after two successful manual Rundowns |

Do not put helper commands, instance ids, or Bot ids in those files.
This recipe is native to Grok Bot.

## Prove it once

After the installer finishes, ask Sum to handle one reversible task in a repository you control.
Confirm it dispatches a worker Bot and does not do the work in the coordinator chat.
Save a question as a file under `/workspace/sum/` with a stable key.
Answer that question by id.
Confirm the worker writes a report file.
Confirm the coordinator runs a distinct verification and records it.
Open a PR if the task ships code.
Merge it yourself.
The Bot never merges.

## Share as a template

After one good run, Sum can be shared as a Grok Bot template.
Inspect the draft.
Strip API keys, internal URLs, and anything you would not put in a public document.
Confirm the draft does not attach another author's share URL.
This recipe is Sum.
Keep this directory as the source of truth when the contract changes.

## Suggested routine

After two successful manual Rundowns, enable the weekday Inbox rundown in `routines.md`.
Stay quiet when the inbox is empty.
