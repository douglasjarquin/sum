# Square on Grok Bot

Tell any Bot in Grok Bot to follow this file.

This file is an installer.
Do not summarize.

You do not need a SUM installation on the user's computer.

## What you are installing

- A steward Bot named Square that keeps the shared computer organized, backed up, and easy to restore
- Memories: the steward memory and the steward procedure
- Control-plane files under `/workspace/square/` after the first persist
- No pack-local skills. Square uses the shared `/workspace/skills/` tree already on the computer.

Square takes commands from Sum.
Sum is the sole coordinator.
If a Bot named Sum does not already exist, stop and tell the user to follow `GROK_SUM.md` first.

## The computers

- The user's computer. Bots never execute there.
- The shared Grok Bot computer. A persistent cloud VM that runs all Bots.
- Cursor Cloud Agents. Ephemeral VMs that a software worker may start. Square does not call them.

## Source

Sum is open source.

Clone `https://github.com/douglasjarquin/sum.git` onto the shared computer at `/home/box/agent-data/sum/src/`.
If that directory already exists, fetch and update it.
If the user named a commit SHA, check that SHA out.
Otherwise stay on the default branch.

The recipe is `templates/square/` in that clone.
Every later reference to a pack file means that directory.
The Sum coordinator pack is `templates/sum/`. Install it with `GROK_SUM.md`.

Do not invent steward text.
Read the files.

## Files in the pack

- `instructions.md` — Square Bot description
- `memories.md` — steward memory
- `worker-procedure.md` — memory named Steward procedure
- `routines.md` — Cleanup, Git backup, and Org review, left paused until two clean manual runs
- `avatar.png` — Square profile image
- `README.md` — pack index

There is no `skills/` directory in this pack.

## Steps

1. Clone or refresh the source as above.

2. Look at the existing roster.
   If a Bot named Square already exists, reuse it.
   Do not create a second Square.
   If Sum is missing, stop and tell the user to install Sum first.

3. Read `instructions.md`.
   CreateAgent name `Square` with that description.
   If you are already Square, keep your name and update your description.

4. Set the Bot avatar from `avatar.png`.
   Do not convert it.
   Do not invent another image.

5. Do not write pack-local workflows from this directory.
   Shared skills already live at `/workspace/skills/` on the shared computer.
   Do not install extra plugins without a yes from the user.

6. Save `memories.md` as a memory included in the template.
   Save `worker-procedure.md` as a memory named Steward procedure.

7. Detect GitHub CLI on the shared computer and confirm it is authenticated.
   Ask the user to connect whatever is missing.
   Do not ask them to paste a token in chat.

8. Do not enable routines yet.

9. Message Square with a task id (for example SQUARE-READY).
   Tell it the memories are installed and to reply ready against that id.
   Empty or blocked still gets a reply.
   Tell Square to leave a short ready note for Sum, not a greeting for the user.

10. Tell the user: Square reports to Sum.
    Talk to Sum for work.
    This starter Bot is leftover if it is not Square.
    They can delete a leftover starter from the sidebar.
    You cannot delete it yourself.

The user merges.
The Bot never merges.
