# Dependencies and compatibility

## Installation contract

`mise.toml` pins Python 3.13.5, Node 22.19.0, GitHub CLI 2.78.0, Herdr 0.8.2, and quota-axi 0.1.37. Git and mise are host prerequisites. No global Node package installation is required.

`mise-tasks/setup` installs those versions, then `scripts/setup.py`:

1. Creates local runtime symlinks under `.local/bin`.
2. Clones Herdr Mesh at **54adef519aa6af4dcd0bbd72586d414abab90046**.
3. Runs `npm ci --omit=dev --ignore-scripts` against the upstream committed lockfile.
4. Applies the documented runtime overlay below.
5. Copies the release-matched Herdr skill from `herdr --skill`.
6. Generates repository-local MCP settings and tests MCP initialization/discovery.

The source revision and upstream lockfile are pinned. This does not claim bit-for-bit reproducibility of every OS/runtime installation. A mise lockfile has not been invented; generate/review it on a networked machine when updating dependency pins.

An optional `--install-codex` installs `@openai/codex@0.153.4` into `.deps/harnesses`. It does not authenticate or switch accounts. Other existing harnesses remain usable.

## Mesh overlay

The upstream snapshot uses older Herdr command shapes (`agent wait --status`, `agent send`, and agent start creating layout). The overlay is deliberately explicit, not a string replacement hidden in an installer.

`patches/herdr-mesh/server.js` replaces only the installed `dist/server.js`; `commands.mjs` is copied as `dist/sum-commands.mjs`. The upstream MCP transport, dependency lock, and CLI subprocess runner remain in use. The original upstream repository and LICENSE stay in `.deps/herdr-mesh`.

The exposed ten-tool surface uses:

- `agent start NAME --kind KIND --pane PANE` in an existing shell pane;
- native `agent prompt`, not separate raw typing plus Enter;
- bounded `agent wait --until` and `agent prompt --wait`;
- bounded visible output, with no interpretation of a settled lifecycle as task success;
- an error on uncertain handoff rather than reading stale output and reporting success.

`bin/herdr-scoped` supplies an explicit session derived from the current pane environment or the coordinator's local context record. It does not create its own socket protocol.

MCP clients have different environment policies. The Codex config explicitly forwards Herdr context variables; the bridge acts only as the calling pane, which must be registered by `sumctl init` in this instance. There is no saved-context fallback, so a client that omits those variables fails locally instead of borrowing another pane's session.

The overlay is versioned sum code and has its own command-contract tests. It is not an upstream release or a claim that upstream has accepted these changes. Prefer upstreaming it if the experiment proves useful.

## Harness matrix

| Harness | Root instructions | Tool path |
| --- | --- | --- |
| Codex | `AGENTS.md` | Generated `.codex/config.toml` MCP plus CLI |
| Claude Code | `CLAUDE.md` alias | Generated `.mcp.json` plus CLI |
| Cursor Agent | `AGENTS.md` / always-applied local rule | Generated `.cursor/mcp.json` plus CLI |
| OpenCode | `AGENTS.md` when supported | Generated `opencode.json` plus CLI |
| Grok | `GROK.md` / `AGENTS.md` when supported | CLI; generic MCP snippet for the installed version |
| Pi / other shell-capable harness | `AGENTS.md` or an explicit startup prompt | CLI; use native MCP support only when available |

The contract is harness-neutral. The matrix describes installation surfaces, **not live conformance certification**. In particular, this environment did not run an authenticated model in any of those harnesses.

## Sources used when implementing (2026-09-05)

- Herdr 0.8.2 release: https://github.com/herdrdev/herdr/releases/tag/v0.8.2
- Herdr automation and response shapes: https://herdr.dev/docs/agent-automation/
- Herdr installation through mise: https://herdr.dev/docs/install/
- Release-matched skill: https://herdr.dev/docs/agent-skill/
- Herdr integration installation: https://herdr.dev/docs/integrations/
- Mesh source: https://github.com/runchr-works/herdr-mesh/tree/54adef519aa6af4dcd0bbd72586d414abab90046
- quota-axi package: https://github.com/kunchenguid/quota-axi/blob/main/package.json
- Codex release: https://github.com/openai/codex/releases/tag/rust-v0.153.4
- Codex MCP configuration: https://developers.openai.com/codex/mcp/
- Claude MCP configuration: https://code.claude.com/docs/en/mcp
- Cursor MCP configuration: https://cursor.com/docs/mcp
- mise file tasks: https://mise.jdx.dev/tasks/file-tasks.html

Third-party licenses are retained in installed dependency distributions. No third-party binary or font is bundled in the source archive.
