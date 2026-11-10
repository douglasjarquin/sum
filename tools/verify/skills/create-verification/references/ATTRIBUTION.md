# Attribution

The create-verification and maintain-verification skills adapt the approach of two MIT-licensed sources.
No driver code was copied; the procedure shape (inspect the repository first, generate a project-local skill with a feature map, prove it once, then keep it honest) is theirs.

- `cursor/plugins`, `pstack/skills/create-verification-skill` and `pstack/skills/maintain-verification-skill`, MIT License, Copyright (c) 2026 Lauren Tan. https://github.com/cursor/plugins/tree/main/pstack
- `poteto/verification-skill-example`, `.cursor/skills/verify-atlas/SKILL.md`, usage guidance for feature-driven verification. https://github.com/poteto/verification-skill-example

Differences here: the output is the harness-neutral `VERIFY.md` contract with `mise run verify` and Markdown feature maps under `docs/features/` (or the location the repository already declares), not a Cursor-only skill directory; helpers are Python standard library only; no Atlas or CDP driver is assumed, and a surface without an installed driver stays explicitly manual.
