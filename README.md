# sum

**Many agents.
One finished task.**

<img width="1280" height="640" alt="sum-social-light-1280x640" src="https://github.com/user-attachments/assets/2d094c5d-7cf1-40f2-b994-887639610a2e" />

Site: <https://douglasjarquin.github.io/sum/>

## What it is

sum is a small, HC-native agent distro.
Start a qualified coordinator through HC: it dispatches every approved request to a worker, stays free for inbox notices, gathers results, and brings decisions back to you.

It is for someone who already has mise, Git, and an authenticated coding harness.
The default path is one finished task.
A factory lane (`sumctl factory` plus the factory procedures in `sum-dispatch` and the worker procedure) can run sequential ready issues on an enrolled project without a sum daemon: tick is a one-shot helper, dispatch stays with the coordinator, and merge happens only at high confidence on authorized repositories.
Instructions and skills do the reasoning.
HC owns terminal processes; Git owns worktrees.
A small synchronous helper preserves task records.
A scoped non-agent HC observer reconciles native events with the ledger.
There is no task scheduler, database server, or permanent hierarchy of managers.

Roles and work objects use the dictionary in [docs/terminology.md](docs/terminology.md).

## Features

* **A separate worktree per worker.** The coordinator prepares a Git worktree, launches the selected worker, and returns control to you.
  A worker is explicitly given its own role; it does not become another coordinator.

* **Nine delivery gates.** Intent, Rebase, Review, Test, Document, Lint, Push, PR, and CI are recorded on the PR.
  The rules are in [docs/verification.md](docs/verification.md).

* **Evidence on the PR.** Before/after evidence publishes on `pr reconcile` unless you turn it off.
  See [docs/verification.md](docs/verification.md).

* **Seven bundled skills.** /sum-dispatch, /sum-work, /sum-deliver, /sum-status, /sum-develop, /sum-factory, and /sum-update with rolling session refresh and code-only rollback.

* **Native HC delivery.** Coordinator initialization ensures a proven owned observer unless the user disabled delivery.
  Startup failure is explicitly degraded; saved obligations and explicit `pump` remain available.
  See [docs/herdr-backend.md](docs/herdr-backend.md).

* **Portable verification.** A project-root `VERIFY.md` and `mise run verify` work in any clone without Sum or HC.
  The rest of the component list is in [docs/architecture.md](docs/architecture.md).

## Quick Start

### Requirements

Prerequisites: **mise**, Git, and an authenticated coding harness available on the execution host.
Setup installs the other dependencies locally through mise.
On a Mac without mise, install it through your normal package manager first (`brew install mise`).

### Recommended harnesses

Automatic terminal input is qualified for Codex 0.166.0 with its default keymap and the namespaced HC Tab-submit template.
Other harness launch selections remain explicit, but automatic input requires actual qualification evidence.

Keep normal trust and permission gates.
Fresh setup adds no custom Sum MCP server; unrelated MCP integrations remain intact.

Harness choice, model flags, and presets are in [docs/configuration.md](docs/configuration.md).

### Install and launch

Clone and stage the runtime, then explicitly start the selected HC core using its release-matched native manual.
Setup does not start an authenticated agent or silently claim coordination.

```sh
git clone https://github.com/douglasjarquin/sum.git
cd sum
mise trust
mise run setup
./bin/sumctl coordinator start --socket /absolute/path/to/hc.sock \
  --session sum --hc-config-home ~/.config --harness codex
```

`coordinator start` seeds the bound environment and matching initialization receipt.
Use `--harness grok`, `--harness claude` or `--harness devin` for the supported versions and input configurations in [configuration](docs/configuration.md#qualified-input-and-coordinator-startup).
A refused launch is retained; `coordinator recover --attempt ATTEMPT` archives only a proven uncreated attempt before another explicit start.
Every agent follows the exact procedure path returned by `init`.
Without a qualified HC context, `init` defaults to developer.
A manually launched qualified existing terminal requires explicit coordinator initialization; model values stay unknown unless established by its launch record.
Never infer an exact model through `--same-as-you`.
See [architecture](docs/architecture.md#session-roles) and [configuration](docs/configuration.md).

### Talk to it

Then delegate normally:

> Use Codex to fix the failing login test in `/Users/me/projects/myapp`. Keep scope to the bug, run the existing checks, arrange independent review, and bring me a PR. Do not merge it. Ask before changing the public API.

The coordinator creates a task brief, prepares a separate Git worktree, launches the selected worker, submits its brief, and returns control to you.

Every session starts with `./bin/sumctl init` and follows the role it returns.
Coordinator, worker, and developer rules are in [docs/architecture.md](docs/architecture.md#session-roles).

### HC terminals

HC owns terminal processes; Git owns worktrees.
Env start and stop, native event delivery, and native metadata are in [docs/herdr-backend.md](docs/herdr-backend.md).

## How It Works

```
you
 │  approved requests and decisions
 ▼
coordinator (this directory)
 │  brief, Git worktree, HC launch
 ▼
worker
 │
 └─ questions, report, PR
```

A question is saved before notification.
Qualified Codex input defers working/blocked/unknown, guards idle with `if:idle`, and submits observed Done without `if` or `force`.
A raced busy turn queues the Tab submission for the next turn; this deliberately relaxed Done guard is not atomic.
A successful send is submitted, not acknowledged.
Native idle or done is not task completion, and a worker's report is not verified success.

Existing schema-one installations cannot cut over without enforceable legacy writer/sender exclusion; current legacy APIs cannot prove that prerequisite.
Normal initialization and status never migrate state.
No production migration or automatic hot adoption is claimed.
See [update](docs/update.md).

State files, returns, and manual dispatch are in [docs/architecture.md](docs/architecture.md).

## Built-in skills

| Skill         | What it does                                                             | Also answers to                          |
| :------------ | :----------------------------------------------------------------------- | :--------------------------------------- |
| /sum-dispatch | Send one approved request to a worker                                    | /sum-delegate, /sum-assign               |
| /sum-work     | Do that task in its own Git worktree                                   | /sum-worker                              |
| /sum-deliver  | Run the project's checks and prepare the PR                              | /sum-ship, /sum-pr, /sum-delivery        |
| /sum-status   | Show task status, pending returns, and maintenance                       | /sum-inbox, /sum-sweep, /sum-recover, /sum-rundown |
| /sum-develop  | Change sum from a development checkout that cannot claim the coordinator | /sum-dev                                 |
| /sum-factory  | Start or run a factory from a markdown settings file                     |                                          |
| /sum-update   | Update or roll back the installation, then refresh running sessions      | /sum-upgrade, /sum-rollback              |

An alias is a one-line skill that names the real one, so the word you reach for loads the same procedure; `bin/sumctl skills check` lists them under `aliases`.

`bin/sumctl skills install` delegates explicit skill and agent selections to the pinned Vercel Skills CLI in project copy mode.
See [docs/sum-skills.md](docs/sum-skills.md).

Agent-loaded procedures ship with the distro: `AGENTS.md` is a short role bootstrap every session loads, `COORDINATOR.md` is the coordinator core `init` names, and each action's detail stays in the bundled skills, loaded only for that action.

## Documentation

* [docs/architecture.md](docs/architecture.md) — session roles, the no-harness install, what's included, and state and communication.

* [docs/configuration.md](docs/configuration.md) — worker harness, presets, and the capacity schema.

* [docs/herdr-backend.md](docs/herdr-backend.md) — env start and stop, native event delivery, and native metadata.

* [docs/verification.md](docs/verification.md) — evidence publication, the nine gates, and brief revisions.

* [docs/repairs.md](docs/repairs.md) — in-scope and expansion repairs.

* [docs/update.md](docs/update.md) — update, rollback, refresh, and runtime releases.

* [docs/recovery.md](docs/recovery.md) — backup, cleanup after a merge, and limits.


* [docs/terminology.md](docs/terminology.md) — user, coordinator, agent, reviewer, task, brief, question, inbox, result.

* [AGENTS.md](AGENTS.md) — the role bootstrap every session loads.

* [COORDINATOR.md](COORDINATOR.md) — the coordinator core.

## Contributing

Tests, the isolated development checkout, and publication are in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. sum stands on projects it did not write — Firstmate, Herdr, Oh My Pi, Solo, Unpeel, and others.
See [ATTRIBUTIONS.md](ATTRIBUTIONS.md) for the complete credit and what each one shaped.
