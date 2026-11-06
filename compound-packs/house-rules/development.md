---
title: Develop the current Sum product
applies_when:
  - Planning, implementing, or reviewing changes in the Sum repository
tags: [sum, instructions, verification, astro]
---

# Develop the current Sum product

Sum distributes instructions and skills for native HC; it has no runtime to build, initialize, or install.
Use the repository's current `AGENTS.md` and four procedures, preserving the user's role assignment and chosen harness, account, model, and permissions.
Keep native HC prerequisites separate from offline developer verification; source checks do not qualify authenticated native conversations.

Use mise tasks as the public command interface and the pinned Go, Python, Node, and aube tools in `VERIFY.md`.
Keep Go limited to the retained portable tooling tests; do not rebuild the deleted runtime or introduce a transport service.
Treat the Grok Bot packs and preserved software-factory contracts as separate products and change them only within explicit scope.

Preserve unrelated dirty files, unfinished branches, task notes, and helper reports.
Use one source writer per checkout and separate worktrees for concurrent writers.
Freeze review and verification inputs, retain the actual candidate identity, and follow the prescribed check inventory in `VERIFY.md`.
Capture end-user evidence before repairing a bug when the environment permits it; report unavailable authenticated acceptance honestly.
Require independent review for a changed implementation candidate and independent root review for policy changes.
Only the user merges or authorizes production changes unless explicitly delegated.

The static Astro website uses `/sum/` and `sitePath()` for page routes.
Read `web/AGENTS.md` and `web/DESIGN.md` for website changes, preserve readable HTML without JavaScript, and use the existing Playwright checks for browser behavior.
Do not edit generated files by hand or add speculative abstractions, compatibility layers, or dependencies.
