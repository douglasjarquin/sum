# Grok Bot packs

Role packs live under short names here.
Install them on Grok Bot from the repository-root installer named in each row.

| Directory | Role | Installer |
| --- | --- | --- |
| [`sum/`](sum/) | Sum coordinator | [`GROK_SUM.md`](../GROK_SUM.md) |
| [`square/`](square/) | Square steward (backup agent) | [`GROK_SQUARE.md`](../GROK_SQUARE.md) |

`templates/grok-bot/` is gone.
The coordinator recipe is `grok-bots/sum/`.
The steward recipe is `grok-bots/square/`.

Brief and preference starters stay under [`templates/`](../templates/).

## Current support boundary

The Grok recipes are a separately maintained native Bot product; they do not use the local HC runtime/procedures.
Install/update from the root installers with current observed native capabilities and private identity/component records.
Shared source contains no private backup destination, retention schedule, or operational authority.
The software factory settings remain pinned to their separately preserved compatible legacy installation.
See [the repair evidence](../docs/verification/issue-354.md) for static checks and outstanding native acceptance.
