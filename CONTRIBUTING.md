# Contributing to sum

Thanks for wanting to help with sum.

Sum is a small, HC-native agent distro: harness instructions in [AGENTS.md](AGENTS.md) do the reasoning, HC owns terminal processes and Git owns worktrees, and `bin/sumctl` keeps task records. A scoped non-agent observer reconciles native events; there is no task scheduler, and contributions do not go through a special push proxy.

The normal path is an ordinary GitHub pull request targeting `main`. Keep the change scoped, follow the verification contract in [VERIFY.md](VERIFY.md), and leave merge to a human.

## Workflow

Pull requests target `main`. Fork, branch, commit, push, and open a PR in the usual way.

1. Fork [douglasjarquin/sum](https://github.com/douglasjarquin/sum) if you do not have push access, then clone your fork (or clone this repository).
2. Update `main` from upstream and create a topic branch.
3. Make the smallest change that satisfies the accepted scope.
4. Commit with a message that says what changed and why.
5. Push the branch to your remote.
6. Open a pull request against `main`.

If you are editing a checkout that already serves a live coordinator, do the work in an isolated development checkout instead of that installation. See [Repo conventions](#repo-conventions). That is local hygiene, not a second publication path: the PR still targets `main` through ordinary git.

GitHub Actions runs the same verification runner described in [VERIFY.md](VERIFY.md) on pull requests to `main`. Treat CI as extra evidence. A human reviews and merges; do not assume a green check is merge authority.

## Verification

[VERIFY.md](VERIFY.md) is this repository's verification contract. Use that file. It is not a stub, and a contribution should not replace it with a different gate.

The contract works in an ordinary clone with Git, mise, and the pinned tools. You do not need a sum installation, a HC session, or any path outside this checkout.

Install the pinned verification tools once:

```sh
mise install go python node
```

Git must also be available from the host. For a local run, prefix the runner with `MISE_ENABLE_TOOLS=go,python,node` so mise does not automatically install unrelated repository tools.

The canonical aggregate entrypoint is:

```sh
mise run verify
```

That command is what [VERIFY.md](VERIFY.md) names as `entrypoint`. It runs, in order, the Go CLI/typed-HC/skill suites and offline demo, then frozen-lockfile site checks, unit tests and build, and stops at the first failure. Feature maps under `docs/features/` say which user journeys those checks cover.

For a candidate you intend to land, run the recorded runner against the merge-base so edits to the contract, mise tasks, or maps are flagged for root review:

```sh
MISE_ENABLE_TOOLS=go,python,node python3 .agents/skills/verify/scripts/verify_run.py --base <merge-base>
```

A preflight that validates the contract without running the suites:

```sh
python3 .agents/skills/verify/scripts/verify_run.py --check
```

Scoped iteration can use `mise run test` (suites only), `mise run lint` (`gofmt -l` and `go vet` over `go/`), and `mise run demo`.
`mise run test-live` is the explicit isolated pinned-HC smoke test and is not part of the aggregate; run it only when that scenario applies and the pinned HC binary is available, otherwise record it as not-run.

Keep checks pointed at temporary state homes and named lab sessions. Never aim a test at a live `.sum/` directory or the user's `default` HC session.

Record the command, result, and evidence path (under `.artifacts/verification/`) in the pull request. Say so when a mapped manual scenario was not exercised. A green `mise run verify` covers the automated feature-map rows only.

When the change is delivered through sum, the worker agent's run, a fresh coordinator verification run of the same candidate, and independent review remain required before a human merges. CI does not replace those.

## Repo conventions

These conventions are for this repository. Sum is not a Node/pnpm app; do not import that workflow.

- **Tools.** [mise.toml](mise.toml) pins Go, Python, and Node. Verification requires `git`, `mise`, `go`, `python3`, and `node`. Use those pins. Do not add a competing toolchain to make the checks easier to pass.
- **Helper.** The CLI is `./bin/sumctl` (or `sumctl` after setup). That name exists so this project does not shadow the Unix `sum` command.
- **Roles.** A harness session in a sum checkout starts with `./bin/sumctl init` and follows the role it returns: `coordinator`, `worker`, or `developer`. Read [AGENTS.md](AGENTS.md), then the role file `init` names under `procedure`. Sending a pull request does not require becoming a coordinator. If another terminal already owns coordination, or you are in a development checkout, stay a developer.
- **Terminology.** New first-party instructions and user-visible text use [docs/terminology.md](docs/terminology.md). Address the user naturally. Do not use themed role titles. Keep technical identifiers (`sumctl report`, `--role worker`, JSON keys) unchanged.
- **Live installs.** The checkout where setup ran serves live work. Change sum from an isolated checkout:

  ```sh
  ./bin/sumctl dev prepare --name my-topic
  cd .sum/dev/my-topic && ./bin/sumctl init
  ```

  Details are in `skills/sum-develop/SKILL.md`. Do not edit the installation's `.sum/`, run setup for it, or dispatch from a developer terminal.
- **Scope.** Change only what the accepted work needs. Do not retarget verification to a `verify` task inherited from a parent directory.
- **State.** `.sum/` is Git-ignored private state. Do not commit it, secrets, or runtime credentials. Commit `mise.lock` with dependency changes when a networked machine generates it; do not fabricate one.
- **Attributions.** When new material or inspiration enters the tree, add an entry to [ATTRIBUTIONS.md](ATTRIBUTIONS.md). That page supplements license and source notices; it does not replace them.
- **Platforms.** macOS and Linux are the targets. Windows is not supported by the file-locking helper in this MVP.
- **Merge.** Leave branch-protection and merge actions to their owner.

### Before you open a PR

- [ ] Confirm the repository root, target branch (`main`), and the change's scope.
- [ ] Install pinned verification prerequisites with `mise install go python node`.
- [ ] Run the [VERIFY.md](VERIFY.md) entrypoint (or the runner with `--base`) and keep the candidate-bound record.
- [ ] Run `mise run test-live` only when that live scenario applies; otherwise say it was not run.
- [ ] Note deployment or release impact when it exists.
- [ ] Use [docs/terminology.md](docs/terminology.md) in new first-party instructions and user-visible text.
- [ ] Assess Grok Bot deployment impact for every new feature or changed contract, and update `grok-bots/sum/` and `grok-bots/square/` references and tests or record an explicit no-impact or deferred rationale.
  Remainder integration is advisory quota only (`sumctl quota`). It does not bind Grok Bot, change `grok-bots/sum/` or `grok-bots/square/`, or alter Bot commands.
  Default TOON stdout does not bind Grok Bot templates. Agents read TOON. jq callers pass `--format json`.
  Remote Sum execution and a second same-host core/session scope are unsupported and refused before binding.
  Native HC remote capability does not enable Sum state synchronization or change the separate Grok Bot product.
  The HC-only terminal runtime has no custom Sum MCP, Mesh or scoped bridge.
  `GROK_SUM.md`, `GROK_SQUARE.md`, `grok-bots/**` and shared factory copy/software procedures remain outside this migration.
  Keeping overlay sources so a prior helper can stage this tree does not bind Grok Bot.
  This dictionary binds `grok-bots/sum/` and `grok-bots/square/`.
  A platform Bot stays a Bot.
  The published Sum recipe is a coordinator.
  Square is the backup agent.
  Worker Bots Sum signs on are worker agents.
  The recipes are native to Grok Bot and do not name helper commands.
  Adding `.grok/hooks/` binds Grok project hooks once the folder is trusted (`/hooks-trust` or `--trust`).
  Adding `.codex/hooks.json` binds Codex project hooks once trusted.
  Neither changes `grok-bots/sum/`, `grok-bots/square/`, or Bot commands.
  `bin/lsp-ensure` and `sumctl lsp ensure` install allowlisted LSP binaries only.
  They fail open and keep hook stdout empty so a missing worktree runtime is not a harness hook failure.
- [ ] Update [ATTRIBUTIONS.md](ATTRIBUTIONS.md) when credit is due.

## Questions

Open a [GitHub issue](https://github.com/douglasjarquin/sum/issues).

## HC migration evidence

Normal Open/init/status never migrate schema-one state.
Existing-installation activation must refuse without an enforceable legacy writer/sender exclusion lease that current legacy APIs cannot prove.
Do not substitute ps output, an operator boolean or a schema/symlink switch.
Historical sealed policy/procedure/evidence bytes remain unchanged; rollback checks actual target binary capabilities and cannot reactivate an inactive schema-one archive.
Keep offline results, isolated native probes, actual harness qualification, hosted CI and production acceptance separate.
Qualified Codex 0.166.0 default-keymap automatic input uses the namespaced HC Tab template with an intentionally non-atomic observed-Done send; diagnostics never replace application receipts.
See `docs/ACCEPTANCE.md` for actual gaps and unrun migration scenarios.
