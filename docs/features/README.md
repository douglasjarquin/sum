# Feature maps

Each map lists the user-facing journeys of one area and, per scenario, whether `mise run verify` exercises it (`automated`) or a person with a real Herdr session or authenticated harness must (`manual`).
The runner in `.agents/skills/verify/` reads the scenario tables from the files linked here; a scenario id must be unique across maps.

- [Coordination and delegation](coordination.md)
- [Portable verification](verification.md)
- [Before/after evidence](evidence.md)
- [Code graph per checkout](graph.md)
- [Project-local third-party skills](skills.md)

A row's driver column starts with `automated` or `manual`; anything after that is free text naming the test or the procedure.
Keep maps truthful: describe what is exercised today, not what a future slice will cover.
