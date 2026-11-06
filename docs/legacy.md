# Separate legacy products

The default Sum package is the four native HC instruction procedures.
`GROK_SUM.md`, `GROK_SQUARE.md`, and `grok-bots/**` are the separately maintained native Grok product.
Issue #354 intentionally repairs their install/update, handoff, review, and backup contracts after the HC refactor.
The retained `skills/sum-factory/factories/{copy,software}.md` settings and unrelated assets/eval fixtures remain protected against the historical immutable baseline.
They keep Bot messaging and Cursor Cloud Agent delegation; native local HC instructions do not replace those contracts.

The shared software-factory settings still refer to [the legacy dispatch guide](../skills/sum-dispatch/references/factory.md) and [legacy worker guide](../skills/sum-work/references/factory.md).
Those paths explicitly require a separately preserved compatible legacy installation pinned to `9456017f2e4fde8f16bc4f2d1bdd7ba978765eb3` and its complete procedures.
Keep that lane targeted there, with its installed helper and settings available.
Do not run it against this instruction checkout or silently reroute Bot workflows.
The shared copy settings remain independent and unchanged.

Historical plans, decisions, solutions, and verification reports about the removed runtime are historical evidence, not active setup instructions or current product claims.
Read [independent adoption](update.md) for the boundary between old installations and new tasks.
