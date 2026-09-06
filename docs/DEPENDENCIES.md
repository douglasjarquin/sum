# Dependencies and compatibility

## Installation contract

`mise.toml` pins Python 3.13.5, Node 22.19.0, GitHub CLI 2.78.0, Herdr 0.8.2, and quota-axi 0.1.37. Git and mise are host prerequisites. No global Node package installation is required.

`mise-tasks/setup` installs those versions, then `scripts/setup.py` performs the first install:

1. Creates local runtime symlinks under `.local/bin`, once. An existing link is never retargeted, because a running process may depend on it; setup reports a differing pin instead.
2. Clones Herdr Mesh at **54adef519aa6af4dcd0bbd72586d414abab90046** into a private staging directory, runs `npm ci --omit=dev --ignore-scripts` against the upstream committed lockfile there, applies the documented runtime overlay below, and renames the finished tree to `.deps/herdr-mesh`. An existing `.deps/herdr-mesh` is never rewritten, reinstalled, or re-patched; drift between its overlay and the current `patches/` is only reported.
3. Copies the release-matched Herdr skill from `herdr --skill`.
4. Generates repository-local MCP settings and tests MCP initialization/discovery.

Re-running setup is therefore safe while a coordinator, workers, or an MCP server are using the checkout; it changes nothing they hold open.
Newer code or dependencies go into a staged release instead (below).

The source revision and upstream lockfile are pinned. This does not claim bit-for-bit reproducibility of every OS/runtime installation. A mise lockfile has not been invented; generate/review it on a networked machine when updating dependency pins.

## Installation identity versus runtime tree

The checkout where setup ran is the **installation**: it owns `.sum/` (records, roles, preferences), the generated MCP configuration, and every absolute path written into worker briefs (`<installation>/bin/sumctl ...`).
A **runtime** is a code-plus-dependency tree that a process executes from: the installation checkout itself today, or an immutable release staged beside it.

`bin/sumctl` and `bin/herdr-mesh` are small stable entrypoints.
Each resolves its runtime exactly once per invocation (the checkout, or the tree behind `.local/current` once `sumctl update apply` creates that pointer), exports `SUM_INSTALL_ROOT=<installation>`, and executes that runtime's Python or Node.
A started Mesh process and the `bin/herdr-scoped` bridge it spawns keep using the tree they started from; nothing re-reads a pointer mid-call.
The helper honors `SUM_INSTALL_ROOT` only when it runs from that installation or from one of its releases, so an inherited variable cannot make a development or task checkout adopt another installation's state, and a release run directly refuses to own state.

## Staged releases

```sh
./bin/sumctl release stage            # HEAD of the installation repository; --ref REF for another commit
./bin/sumctl release list
./bin/sumctl release show SHA
```

`release stage` builds `<installation>/.local/releases/<commit sha>/`:

- the committed tree from `git archive` (no working-tree edits, `.sum`, `.deps`, `.local`, or credentials);
- `.local/bin/*` links to the mise tool versions pinned by the bundled `mise.toml` (`mise install` may add a version; nothing is pruned);
- `.deps/herdr-mesh` cloned (from the installation's local Mesh clone when it has the pinned revision, otherwise upstream), installed with `npm ci`, and overlaid;
- `.local/skills/herdr/SKILL.md` from the pinned `herdr --skill`;
- `release.json`: source SHA and tree, a content hash for every bundled file, the Mesh revision and overlay hashes, tool pins and resolved paths, the Herdr CLI and MCP tool contract versions, the supported state and brief schema versions, and who staged it.

Everything happens in a private `.staging-*` directory.
The bundle is validated against its manifest and the MCP smoke test, made read-only, and then renamed to its final name in one step, so a listed release is always complete.
A failed download, build, or validation removes only that staging directory and reports the reason; existing releases, the checkout's runtime, and `.sum` are untouched.
Two concurrent requests for one SHA end with a single bundle; each installation has its own `releases` directory.
Staging never activates anything: no pointer, MCP configuration, or live process changes.
Old releases are kept until you remove one deliberately; there is no automatic garbage collection.
A release tree contains no `.sum`, and running its `bin/sumctl` directly is refused; only the installation's entrypoint selects a runtime.

## Activation and rollback

```sh
./bin/sumctl update check|stage|apply [--ref REF] [--no-fetch]
./bin/sumctl update status
./bin/sumctl update rollback [--to SHA|checkout]
```

`.local/current` is the installation default; `bin/sumctl` and `bin/herdr-mesh` follow it when it exists and otherwise run the checkout.
`update apply` resolves the source to a SHA merged on `origin/<default branch>` (only `refs/remotes/origin/*` are fetched; HEAD, the working tree, and remotes are never changed), stages the release, and only then takes `.local/update.lock`.
Under the lock it validates the manifest, the installation state schema, each non-archived task's brief schema, the installed Herdr CLI version against `release.json`, the pinned tool links, and a read-only run of the candidate helper (`--version`, `status`, `show`) against the records; then it creates the new symlink under a private name and renames it over `.local/current`.
`.local/updates.jsonl` records each refusal and selection (old/new SHA, blocking items, deferred work, post-check); the symlink, not the log, is the source of truth.
A candidate needing another Herdr version is refused here.
A changed MCP contract is applied with `deferred` naming the clients that keep their old tool set until they restart.
`update rollback` reselects a staged release or the checkout through the same checks and touches nothing under `.sum`, the releases, or any worktree.
Coexistence is the design: several releases stay staged, each process keeps the tree it started from, and every brief's absolute `<installation>/bin/sumctl` command reaches whichever runtime is the default at call time.

An optional `--install-codex` installs `@openai/codex@0.153.4` into `.deps/harnesses`. It does not authenticate or switch accounts. Other existing harnesses remain usable.

## Managed projects

`sumctl project enroll owner/repo` clones exactly one repository into `<installation>/projects/<owner>/<repo>`; a non-default host gets an explicit level (`projects/<host>/<owner>/<repo>`), so two owners' same-named repositories and two hosts never collide.
The default host clones through `gh repo clone` (the user's existing authentication); a full URL or `--remote` clones through `git clone` and records that URL as the verified remote.
The clone lands in a private `.staging-*` directory first and is renamed into place in one step under the store lock; a failed clone removes only its staging directory and any empty parent it created.
`/projects/` is in sum's `.gitignore`: project code is neither sum source nor runtime state, so `git ls-files`, `git archive`, `release stage` bundles, the source manifest, and the test discovery never contain it. A records backup carries `.sum/projects.json` and explicitly excludes clone and worktree contents.

`.sum/projects.json` is the one registry: `host`, `owner`, `repo`, `name`, `kind` (`managed`, `legacy`, `external`, `installation`), `path`, the verified `remote`, `canonical_path`, and who enrolled when.
Enrollment is idempotent and never overwrites: an enrolled name returns its record; a checkout already at the canonical path, under the earlier `.sum/projects/<owner>/<repo>` location, or named with `--path` is adopted only when it is a Git top level whose origin is the same repository; a different remote, a non-Git directory, or a symlinked component is a refusal that changes nothing.
Enrolling the installation's own repository registers the installation itself (`kind: installation`); sum is never cloned under its own `projects/`, and self-development stays in `dev prepare` checkouts.
`project migrate NAME` is inspect-only by default: it lists non-archived tasks, linked Git worktrees, and processes that reference a legacy or external clone and prints guidance; `--apply` renames the directory to the canonical path only when that list is empty and the process table was readable, keeping `.git`, uncommitted files, and the registration.
No task is ever pointed at a moved directory: existing tasks keep their recorded worktrees and absolute callbacks.

The clone is a reference checkout for `prepare --project NAME` (or a matching `--repo` path): each task still gets its own Herdr worktree, the per-repository capacity applies to the clone like any repository, and the task record carries the project identity.
The worker learns nothing from directory nesting. Its brief's `## Delivered runtime` section names the installed helper (`<installation>/bin/sumctl`), a controlled copy of the worker procedure with its hash, and the absolute runtime skill file with size and hash; `context --role worker` reports the same `helper` and `files`. A Herdr worktree lives outside the installation, some harnesses stop instruction discovery at a Git root, and a project's own `AGENTS.md` and mise tasks remain the project's.
Conversely a pane whose working directory lies inside a managed clone is a project session: `sumctl init` there refuses to register any role, so a parent directory's instructions never make a project pane the coordinator.

mise resolves configuration up the directory tree, so a clone nested under the installation sees sum's `mise.toml` and `mise-tasks` (`test`, `demo`, ...). `env discover` records `task_origins` from `mise tasks ls --json` (a listing, never a run): each task's source file and whether the checkout owns it, the inherited ones as a named problem, and whether `verify`/`test` are the project's own. An absent or refusing mise is reported as such, never as "no tasks".

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
