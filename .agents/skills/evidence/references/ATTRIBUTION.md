# Attribution

The evidence skill borrows presentation conventions and proof standards from two public projects. No code was copied from either.

- `vercel-labs/before-and-after` (https://github.com/vercel-labs/before-and-after): the labelled two-up "Before" / "After" presentation used by `comparison.html`. That project publishes existing media; it is not the capture driver here and is not required to run this skill.
- `poteto/verification-skill-example`, `.cursor/skills/verify-atlas/SKILL.md` (https://github.com/poteto/verification-skill-example): the proof standard - drive the real user path, show the triggering action and the stable end state, verify side effects, never accept an image alone as proof.

Differences here: the driver is a Chromium-family browser spoken to over the DevTools Protocol from Node's built-in WebSocket, with Python standard library only for everything else; the video original is a Motion-JPEG AVI assembled from the browser's own frames, converted only optionally; nothing is harness-specific and no Cursor-only logic is used.
