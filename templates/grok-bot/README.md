# How to load the Sum Grok Bot recipe

This directory is the source-controlled recipe for a Sum coordinator Bot.
It is not a live share URL.
You paste it into the Grok Bot app, run one safe task, then share it as a template yourself.

You do not need a SUM installation.

## What you paste

| File | Grok Bot field |
| --- | --- |
| `instructions.md` | Bot description |
| `memories.md` | Memories included in the template |
| `worker-procedure.md` | A memory named Worker procedure |
| `skills/*/SKILL.md` | Skills named Dispatch, Persist, Verify, Rundown, and Delivery |
| `routines.md` | A paused Inbox rundown routine after two good manual runs |

Do not paste helper commands, instance ids, or Bot ids into those fields.
This recipe is native to Grok Bot.

## Load the Bot

1. Create a new Bot.
2. Name it Sum.
3. Paste `instructions.md` into the description.
4. Save each skill from `skills/` with the six fields intact.
5. Save `memories.md` and `worker-procedure.md` as memories.
6. Connect GitHub.
7. Connect Cursor if the Bot will dispatch software work.
8. Do not enable routines yet.

## Prove it once

1. Ask the Bot to handle one reversible task in a repository you control.
2. Confirm it dispatches a worker Bot and does not do the work in the coordinator chat.
3. Save a question as a file under `/workspace/sum/` with a stable key.
4. Answer that question by id.
5. Confirm the worker writes a report file.
6. Confirm the coordinator runs a distinct verification and records it.
7. Open a PR if the task ships code.
8. Merge it yourself.
   The Bot never merges.

## Share as a template

1. Open Bot settings and choose Share as Template.
2. Inspect the draft.
   Strip API keys, internal URLs, and anything you would not put in a public document.
3. Confirm the draft does not attach another author's share URL.
   This recipe is Sum.
4. Publish for your team or as a public link.
5. Keep this directory as the source of truth when the contract changes.

## Suggested routine

After two clean manual rundowns, create the Inbox rundown in `routines.md` and leave it paused until a test run looks right.
