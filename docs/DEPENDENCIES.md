# Dependencies and compatibility

The machine-readable inventory in [`dependency-inventory.json`](dependency-inventory.json) is the single record of dependency source, version, checksum or provenance, license, supported platforms, requirements, build/runtime role, owner, and consumed CLI/MCP contracts.
Release manifests copy that inventory and add the platform-specific SHA-256 of each staged native artifact.

## Installation contract

`mise.toml` pins Go 1.25.0, Python 3.13.5, Node 22.20.0, GitHub CLI 2.100.0, Herdr 0.9.0, quota-axi 0.1.37, codegraph 1.5.0 (`npm:@colbymchenry/codegraph`), and Vercel Skills 1.5.25 (`npm:skills`).
Git and mise are host prerequisites.
No global Node package installation is required.

The `go/` module pins Cobra v1.9.1 and the official Model Context Protocol Go SDK v1.6.1 in ordinary `go.mod`/`go.sum` files.
The SDK's reviewed transitive graph remains visible in `go.sum`; no Viper, generator, provider SDK, or configuration framework is installed.

`mise-tasks/setup` installs those versions, then `scripts/setup.py` performs the first install:

1. Creates local runtime symlinks under `.local/bin`, once. An existing link is never retargeted, because a running process may depend on it; setup reports a differing pin instead.
2. Clones Herdr Mesh at **54adef519aa6af4dcd0bbd72586d414abab90046** into a private staging directory, runs `npm ci --omit=dev --ignore-scripts` against the upstream committed lockfile there, applies the documented runtime overlay below, and renames the finished tree to `.deps/herdr-mesh`. An existing `.deps/herdr-mesh` is never rewritten, reinstalled, or re-patched; drift between its overlay and the current `patches/` is only reported.
3. Copies the release-matched Herdr skill from `herdr --skill`.
4. Builds the cgo-free `go/cmd/herdr-mesh` companion as `.local/bin/herdr-mesh-go`; its opt-in launcher is `bin/herdr-mesh-go`, and the existing Node Mesh entrypoint remains available.
5. Generates repository-local MCP settings and tests MCP initialization/discovery.

Re-running setup is therefore safe while a coordinator, workers, or an MCP server are using the checkout; it changes nothing they hold open.
Newer code or dependencies go into a staged release instead (below).

The native companion is also built in a release staging directory with `CGO_ENABLED=0`.
The staged binary's source, build requirements, runtime requirements, and SHA-256 are recorded in `release.json` under `dependencies.native.herdr-mesh-go`.
Running it requires no Go toolchain, module download, Node, Python, or Cobra generator.
The unused Go helper experiment is [not adopted](go-helper-prototype.md); `bin/sumctl` remains the Python entrypoint.
Native artifact requirements come from each release's own dependency inventory, so a missing or corrupt declared artifact is refused without making a retired experiment mandatory for new bundles.
Verification compares the manifest's inventory with the hash-checked bundled inventory and requires each native artifact's canonical path, source identity, version, and current-host platform to agree.

The source revision and upstream lockfile are pinned. This does not claim bit-for-bit reproducibility of every OS/runtime installation. A mise lockfile has not been invented; generate/review it on a networked machine when updating dependency pins.

## GitHub CLI 2.100.0 and `--attach`

Evidence publication (`.agents/skills/evidence/scripts/evidence_publish.py`, `sumctl pr evidence`) uploads media only through `gh pr edit --attach`, added in GitHub CLI 2.99.0 (2026-09-01); 2.100.0 (2026-09-03) fixes its retry windows and the 50-file batch limit.
The pin moved from 2.78.0 to 2.100.0 for that reason and nothing else changed in how gh is used.
The new version reaches an installation the way every dependency does: `release stage` installs the pinned tools of the bundled `mise.toml` and links them into that release's own `.local/bin`, so a staged release carries gh 2.100.0 while the checkout's existing `.local/bin/gh` link stays at whatever it was (setup never retargets a link a running process may hold).
`update apply` activates it with the usual checks and `update rollback` returns to the previous release; no running tool, harness, or MCP server is retargeted or restarted.
Until a capable gh is the runtime's, the publisher reads the installed binary (`capabilities`: version and `pr edit --help`), reports `deferred`, and leaves local evidence and the PR body untouched; capture never depends on it.
Nothing is installed per publication.

## Vercel Skills CLI 1.5.25

`npm:skills` 1.5.25 is pinned in `mise.toml`, requires Node 22.20.0 or newer, and is linked into each runtime as `.local/bin/skills` by setup or release staging.
`sumctl skills install` accepts one target Git project, one source, and explicit repeated skill and agent names.
It invokes only `skills add SOURCE --skill ... --agent ... --copy --yes` with the target as the working directory.
Project scope is the upstream default; Sum never passes `--global`, and a target that is not the Git project root is refused so non-interactive scope detection cannot choose a user-level install.
Sum rejects wildcard and option-looking selections plus the reserved `sum-*` namespace before invocation.
The subprocess disables upstream telemetry with both documented environment variables.

Vercel Skills owns repository discovery, copying, and `skills-lock.json`.
Its `--yes` path may overwrite an existing same-named third-party skill, so the operator reviews the Git diff after installation.
Sum's own `skills check` remains limited to its bundled names, projections, portable imports, and compatibility references; it does not claim third-party content integrity.

## codegraph 1.5.0 per checkout

`npm:@colbymchenry/codegraph` 1.5.0 is pinned in `mise.toml` and reaches an installation like every tool: `mise run setup` and `release stage` install the pinned package and link `.local/bin/codegraph` once (the link is never retargeted; a staged release carries its own).
Provenance recorded in every `release.json` under `dependencies.codegraph`: package `@colbymchenry/codegraph`, version 1.5.0, MIT license, source https://github.com/colbymchenry/codegraph (release tag v1.5.0, git head `ea72e1b190921232aa7bd02e96bef5bbe4fe0ab6`), registry tarball `codegraph-1.5.0.tgz` with integrity `sha512-/l1JMVOQ9WGQLrc/IIuAg7Igr944t79/oNCJTcnGkYtIeQx2XFIqI0ho+9Les/Yu4zKfmPU17hIUshD6yP1fKw==`.
The package is a thin launcher whose per-platform bundle (a vendored Node 24 plus the app) is npm's optional dependency of the same exact version; sum sets `CODEGRAPH_NO_DOWNLOAD=1` so a missing bundle is a reported failure, never a download at task time.
sum uses only the runtime's own link: a global `codegraph`, `npx`, `codegraph upgrade`, and `codegraph install` are never run, and a link whose `--version` is not the pin is reported `unavailable` rather than used.

What sum runs, and only in the checkout it just created and validated (task worktree at `prepare`/`dispatch`, the detached root verification checkout of `verify --execute`, the self-development checkout of `dev prepare`):

1. `codegraph status --json <checkout>` to see whether an index exists and belongs to that path (`indexPath`, `worktreeMismatch`), which version and extraction schema built it, and which uncommitted edits are pending.
2. `codegraph init <checkout>` when there is none; `codegraph index --quiet <checkout>` (full rebuild) when the index belongs to another path, another codegraph version, or another extraction schema; `codegraph sync <checkout>` when edits are pending or HEAD moved since sum last built or synced it; nothing when everything matches.
3. `codegraph status --json` again for the recorded index identity (`fileCount`, `nodeCount`, `edgeCount`, `dbSizeBytes`, `lastIndexed`, `builtWithVersion`).

Every call runs with `CODEGRAPH_NO_DAEMON=1` (no shared background server, no watcher outlives the command), `CODEGRAPH_NO_DOWNLOAD=1`, and `NO_COLOR=1`, under one timeout (300 s by default; `SUM_GRAPH_TIMEOUT` is a lab knob) after which the child is killed and the attempt recorded as timed out, and inside one of two per-installation build slots (lock files under the temporary directory, never in `.sum`); a full house is recorded `deferred`, not queued without bound.
The record (`.sum/tasks/<id>/graph.json`, summarized as `graph` in `task.json`, `dev.json`, and the root run evidence) carries the tool path and version, the checkout identity (top level, HEAD, branch, common Git directory), every attempt with action, exit, duration, and error, the index identity, a point-in-time freshness check, and the exact CLI commands the brief prints.
States: `ready`, `failed` (retry with `sumctl graph init TASK_ID`), `exhausted` (three failed attempts; the recorded fallback is source inspection), `deferred`, `unavailable` (no pinned tool in this runtime, or a version other than the pin). None of them changes the task, its checkout, its worker, or the source-reading route.

Facts taken from the pinned binary in an isolated lab, not from its README:

- `codegraph init` writes `.codegraph/` with its own `.gitignore` (`*` and `!.gitignore`) and does not edit the repository's `.gitignore`; `.codegraph/.gitignore` is therefore an untracked file until something ignores the directory. sum's own `.gitignore` lists `.codegraph/`; for any other repository sum appends `.codegraph/` once, under a marker comment, to the repository-local `.git/info/exclude` (shared by that repository's worktrees, never committed, not a user configuration file) and records the write; a repository that already ignores the directory gets no write. The portable verify runner's dirty check (`--untracked-files=normal`) therefore stays clean.
- A linked worktree without its own index answers queries from the main worktree's index with a warning that the results come from a different worktree; `status --json` reports `initialized: false` for it. Only an index inside the worktree is that worktree's graph, which is why sum initializes each one.
- Indexing honors Git's ignore rules including `info/exclude`: sum's own index (47 files, 2,200 symbols, 10,204 edges at this commit) contains nothing under `projects/`, `.sum/`, `.local/`, `.deps/`, or `.artifacts/`.
- `pendingChanges` in `status --json` counts only uncommitted edits: after a commit it reads zero while a query for the committed symbol returns nothing, and `status` never syncs on its own. sum therefore also records the HEAD it last built or synced and treats a moved HEAD as stale; the brief tells the worker to sync after commits, checkouts, and rebases, and a worker in CLI mode has no watcher.
- `CODEGRAPH_DIR` accepts only a plain directory name, so the writable index always sits inside the checkout.
- `codegraph install --print-config <agent>` prints the per-agent MCP snippet without writing; `sumctl graph config --harness claude|codex|cursor|opencode` prints the same shapes with the pinned binary path instead of a PATH lookup. sum never runs the installer, which by default writes agent configuration, an instructions block, and (for Claude Code) an auto-allow permission list.

Measured on this machine (macOS, Apple Silicon, warm cache, real binary): `init` of sum's checkout 0.69 s wall (234 ms indexing), a repeated `init` on the existing index 0.08 s, `sync` after one edit 0.24 s, `query` 0.15 s, `explore` 0.14 s. Upstream's benchmark figures are not repeated here as local measurements.

Cleanup classifies `.codegraph/` as a regenerable cache (with `__pycache__`, `node_modules`, `.artifacts`), so a merged task's checkout is removable with its index; a records backup carries `graph.json` and rebuild metadata, never an index; a release bundle is refused if it contains `.codegraph`. sum starts no watcher, so it stops none; a harness's own `codegraph serve --mcp` process inside a checkout is that session's and shows up as an ordinary occupant until the session exits.

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

- Herdr 0.9.0 release: https://github.com/herdrdev/herdr/releases/tag/v0.9.0
- Herdr automation and response shapes: https://herdr.dev/docs/agent-automation/
- Herdr installation through mise: https://herdr.dev/docs/install/
- Release-matched skill: https://herdr.dev/docs/agent-skill/
- Herdr integration installation: https://herdr.dev/docs/integrations/
- Mesh source: https://github.com/runchr-works/herdr-mesh/tree/54adef519aa6af4dcd0bbd72586d414abab90046
- quota-axi package: https://github.com/kunchenguid/quota-axi/blob/main/package.json
- GitHub CLI 2.99.0 (`--attach`): https://github.com/cli/cli/releases/tag/v2.99.0 and 2.100.0: https://github.com/cli/cli/releases/tag/v2.100.0 (2026-09-06)
- before-and-after skill (PR block markup): https://github.com/vercel-labs/before-and-after at 8306d34f459b6704e08e6adb5829fcddb0dc3557
- codegraph 1.5.0: https://github.com/colbymchenry/codegraph/releases/tag/v1.5.0 and https://www.npmjs.com/package/@colbymchenry/codegraph/v/1.5.0 (2026-09-06)
- Vercel Skills 1.5.25: https://github.com/vercel-labs/skills and https://www.npmjs.com/package/skills/v/1.5.25 (2026-09-09)
- Codex release: https://github.com/openai/codex/releases/tag/rust-v0.153.4
- Codex MCP configuration: https://developers.openai.com/codex/mcp/
- Claude MCP configuration: https://code.claude.com/docs/en/mcp
- Cursor MCP configuration: https://cursor.com/docs/mcp
- mise file tasks: https://mise.jdx.dev/tasks/file-tasks.html

Third-party licenses are retained in installed dependency distributions. No third-party binary or font is bundled in the source archive.
