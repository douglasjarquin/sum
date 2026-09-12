# Contributing to sum

Thanks for wanting to help with sum.

Sum is a small, Herdr-native agent distro: harness instructions in [AGENTS.md](AGENTS.md) do the reasoning, Herdr owns panes and worktrees, and `bin/sumctl` keeps task records. There is no sum daemon, and contributions do not go through a special push proxy.

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

The contract works in an ordinary clone with Git, mise, and the pinned tools. You do not need a sum installation, a Herdr session, or any path outside this checkout.

Install the pinned verification tools once:

```sh
mise install go python node
```

Git must also be available from the host. For a local run, prefix the runner with `MISE_ENABLE_TOOLS=go,python,node` so mise does not automatically install unrelated repository tools.

The canonical aggregate entrypoint is:

```sh
mise run verify
```

That command is what [VERIFY.md](VERIFY.md) names as `entrypoint`. It runs, in order, the offline Python suites, the Mesh Node tests, and the scripted demo, and stops at the first failure. Feature maps under `docs/features/` say which user journeys those checks cover.

For a candidate you intend to land, run the recorded runner against the merge-base so edits to the contract, mise tasks, or maps are flagged for root review:

```sh
MISE_ENABLE_TOOLS=go,python,node python3 .agents/skills/verify/scripts/verify_run.py --base <merge-base>
```

A preflight that validates the contract without running the suites:

```sh
python3 .agents/skills/verify/scripts/verify_run.py --check
```

Scoped iteration can use `mise run test` (suites only) and `mise run demo`. `mise run test-live` is the explicit real-Herdr smoke test and is not part of the aggregate; run it only when that scenario applies and a real Herdr is available, otherwise record it as not-run.

Keep checks pointed at temporary state homes and named lab sessions. Never aim a test at a live `.sum/` directory or the user's `default` Herdr session.

Record the command, result, and evidence path (under `.artifacts/verification/`) in the pull request. Say so when a mapped manual scenario was not exercised. A green `mise run verify` covers the automated feature-map rows only.

When the change is delivered through sum, the worker agent's run, a fresh coordinator verification run of the same candidate, and independent review remain required before a human merges. CI does not replace those.

## Repo conventions

These conventions are for this repository. Sum is not a Node/pnpm app; do not import that workflow.

- **Tools.** [mise.toml](mise.toml) pins Go, Python, and Node. Verification requires `git`, `mise`, `go`, `python3`, and `node`. Use those pins. Do not add a competing toolchain to make the checks easier to pass.
- **Helper.** The CLI is `./bin/sumctl` (or `sumctl` after setup). That name exists so this project does not shadow the Unix `sum` command.
- **Roles.** A harness session in a sum checkout starts with `./bin/sumctl init` and follows the role it returns: `coordinator`, `worker`, or `developer`. Read [AGENTS.md](AGENTS.md). Sending a pull request does not require becoming a coordinator. If another pane already owns coordination, or you are in a development checkout, stay a developer.
- **Terminology.** New first-party instructions and user-visible text use [docs/TERMINOLOGY.md](docs/TERMINOLOGY.md). Address the user naturally. Do not use themed role titles. Keep technical identifiers (`sumctl report`, `--role worker`, JSON keys) unchanged.
- **Live installs.** The checkout where setup ran serves live work. Change sum from an isolated checkout:

  ```sh
  ./bin/sumctl dev prepare --name my-topic
  cd .sum/dev/my-topic && ./bin/sumctl init
  ```

  Details are in `skills/sum-develop/SKILL.md`. Do not edit the installation's `.sum/`, run setup for it, or dispatch from a developer pane.
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
- [ ] Use [docs/TERMINOLOGY.md](docs/TERMINOLOGY.md) in new first-party instructions and user-visible text.
- [ ] Assess Grok Bot deployment impact for every new feature or changed contract, and update `templates/grok-bot/` references and tests or record an explicit no-impact or deferred rationale.
  Remainder integration is advisory quota only (`sumctl quota`). It does not bind Grok Bot, change `templates/grok-bot/`, or alter Bot commands.
  Default TOON stdout does not bind Grok Bot templates. Agents read TOON. jq callers pass `--format json`.
  Remote-machine returns use Herdr `machine add`. They do not bind Grok Bot, change `templates/grok-bot/`, or add a Sum SSH enroll path.
  Making Go Mesh the live `bin/herdr-mesh` server does not bind Grok Bot, change `templates/grok-bot/`, or alter Bot commands.
  Keeping overlay sources so a prior helper can stage this tree does not bind Grok Bot.
  This dictionary binds `templates/grok-bot/`. A platform Bot stays a Bot. Its sum role is coordinator or project agent.
- [ ] Update [ATTRIBUTIONS.md](ATTRIBUTIONS.md) when credit is due.

## Questions

Open a [GitHub issue](https://github.com/douglasjarquin/sum/issues).
