# Sum on Grok Bot

Tell any Bot in Grok Bot to follow this file.

This file is an installer.
Do not summarize.

You do not need a SUM installation on the user's computer.

## What you are installing

- A coordinator Bot named Sum that the user talks to from then on
- Global skills: Dispatch, Persist, Verify, Rundown, Deliver, Sitdown, cheap-routines, adversarial-review
- Memories: the coordinator memory and the worker procedure
- Task files under `/workspace/sum/` after the first persist

## The computers

- The user's computer. Bots never execute there.
- The shared Grok Bot computer. A persistent cloud VM that runs all Bots.
- Cursor Cloud Agents. Ephemeral VMs that a worker may start for software work. The coordinator never calls them.

## Source

Sum is open source.

Clone `https://github.com/douglasjarquin/sum.git` onto the shared computer at `/home/box/agent-data/sum/src/`.
If that directory already exists, fetch and update it.
If the user named a commit SHA, check that SHA out.
Otherwise stay on the default branch.

The recipe is `templates/grok-bot/` in that clone.
Every later reference to a pack file means that directory.

Do not invent skill text.
Read the files.

## Files in the pack

- `instructions.md` — coordinator Bot description
- `memories.md` — coordinator memory
- `worker-procedure.md` — worker Bot description memory
- `skills/dispatch/SKILL.md`
- `skills/persist/SKILL.md`
- `skills/verify/SKILL.md`
- `skills/rundown/SKILL.md`
- `skills/deliver/SKILL.md`
- `skills/sitdown/SKILL.md`
- `skills/cheap-routines/SKILL.md`
- `skills/adversarial-review/SKILL.md`
- `routines.md` — Inbox rundown, enabled after two successful manual Rundowns

## Steps

1. Clone or refresh the source as above.

2. Look at the existing roster.
   If a Bot named Sum already exists, reuse it.
   Do not create a second coordinator.

3. Read `instructions.md`.
   CreateAgent name `Sum` with that description.
   If you are already Sum, keep your name and update your description.

4. Write global workflows from the skill files.
   Names:
   - Dispatch
   - Persist
   - Verify
   - Rundown
   - Deliver
   - Sitdown
   - cheap-routines
   - adversarial-review
   Use each skill's description line as the workflow description.
   Keep the six fields in each skill file intact.
   Do not install extra plugins without a yes from the user.

5. Save `memories.md` as a memory included in the template.
   Save `worker-procedure.md` as a memory named Worker procedure.

6. Detect GitHub CLI on the shared computer and confirm it is authenticated.
   Cursor Cloud Agents need the user's Cursor account connected to GitHub.
   Ask the user to connect whatever is missing.
   Do not ask them to paste a token in chat.

7. Do not enable routines yet.

8. Message Sum with a task id (for example SUM-READY).
   Tell it the skills are installed and to reply ready against that id.
   Empty or blocked still gets a reply.
   Tell Sum to leave a greeting for the user.

9. Tell the user: talk only to Sum from here.
   This starter Bot is leftover.
   They can delete it from the sidebar.
   You cannot delete it yourself.

The user merges.
The Bot never merges.
