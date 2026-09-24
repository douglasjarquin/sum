# sum

**Many agents. One finished task.**

<img width="1280" height="640" alt="eBXSf" src="https://github.com/user-attachments/assets/97ef5898-7ff1-4405-bf5a-413738c2af62" />

## What it is

sum is a small, Herdr-native agent distro. Launch a coding harness in this directory and it becomes the coordinator: it dispatches every approved request to a worker, stays free for inbox notices, gathers results, and brings decisions back to you.

It is for someone who already has mise, Git, and an authenticated coding harness, and who wants one finished task rather than an unattended factory. Instructions and skills do the reasoning. Herdr owns processes and worktrees. A small synchronous helper preserves task records. There is no sum daemon, scheduler, database server, or permanent hierarchy of managers.

Roles and work objects use the dictionary in [docs/terminology.md](docs/terminology.md).

## Features

* **A separate worktree per worker.** The coordinator prepares a Herdr worktree, launches the selected worker, and returns control to you. A worker is explicitly given its own role; it does not become another coordinator.

* **Nine delivery gates.** Intent, Rebase, Review, Test, Document, Lint, Push, PR, and CI are recorded on the PR. The rules are in [docs/verification.md](docs/verification.md).

* **Evidence on the PR.** Before/after evidence publishes on `pr reconcile` unless you turn it off. See [docs/verification.md](docs/verification.md).

* **Six bundled skills.** /sum-dispatch, /sum-worker, /sum-delivery, /sum-rundown, /sum-develop, and /sum-update with rolling session refresh and code-only rollback.

* **Optional Herdr hook and metadata.** Native event delivery and sidebar tokens stay off until you enable them. See [docs/herdr-backend.md](docs/herdr-backend.md).

* **Portable verification.** A project-root `VERIFY.md` and `mise run verify` work in any clone without sum or Herdr. The rest of the component list is in [docs/architecture.md](docs/architecture.md).

## Quick Start

### Requirements

Prerequisites: **mise**, Git, and an authenticated coding harness available on the execution host. Setup installs the other dependencies locally through mise. On a Mac without mise, install it through your normal package manager first (`brew install mise`).

### Recommended harnesses

`codex` can be `claude`, `grok`, `cursor-agent`, `pi`, `opencode`, or `devin`.

Accept the harness's normal project/MCP trust prompt. Do not bypass permissions. Codex, Claude, and Cursor receive repository-local MCP configuration; OpenCode receives `opencode.json`. Other shell-capable harnesses can use the same native Herdr CLI skill without MCP.

Harness choice, model flags, and presets are in [docs/configuration.md](docs/configuration.md).

### Install and launch

The shortest start, once this repository is published, is a clone and then a Herdr pane:

```sh
git clone https://github.com/douglasjarquin/sum.git
herdr
# In a Herdr pane:
cd /path/to/sum
mise trust
mise run setup
codex                         # or claude, grok, cursor-agent, pi, opencode, devin, ...
```

If Herdr is not installed yet, run `mise trust && mise run setup` in the clone first, then `mise exec -- herdr`. If mise is not activated in your shell, launch the harness with `mise exec -- codex` instead of bare `codex`.

A harness that does not auto-load `AGENTS.md` needs this first prompt:

> Read AGENTS.md in this directory, initialize sum, and act as my coordinator.

The optional pinned Codex install, and the rule for a harness installed after Herdr is already running, are in [docs/architecture.md](docs/architecture.md#no-installed-harness).

### Talk to it

Then delegate normally:

> Use Codex to fix the failing login test in `/Users/me/projects/myapp`. Keep scope to the bug, run the existing checks, arrange independent review, and bring me a PR. Do not merge it. Ask before changing the public API.

The coordinator creates a task brief, prepares a separate Herdr worktree, launches the selected worker, submits its brief, and returns control to you.

Every session starts with `./bin/sumctl init` and follows the role it returns. Coordinator, worker, and developer rules are in [docs/architecture.md](docs/architecture.md#session-roles).

### Herdr

Herdr owns processes and worktrees. Env start and stop, native event delivery, and native metadata are in [docs/herdr-backend.md](docs/herdr-backend.md).

## How It Works

```
you
 │  approved requests and decisions
 ▼
coordinator (this directory)
 │  brief, Herdr worktree, launch
 ▼
worker
 │
 └─ questions, report, PR
```

A question is saved before notification. A successful send is submitted, not acknowledged. Native idle or done is not task completion, and a worker's report is not verified success.

State files, returns, and manual dispatch are in [docs/architecture.md](docs/architecture.md).

## Built-in skills

| Skill         | What it does                                                             |
| :------------ | :----------------------------------------------------------------------- |
| /sum-dispatch | Send one approved request to a worker                                    |
| /sum-worker   | Do that task in its own Herdr worktree                                   |
| /sum-delivery | Run the project's checks and prepare the PR                              |
| /sum-rundown  | Reconcile saved tasks and pending returns                                |
| /sum-develop  | Change sum from a development checkout that cannot claim the coordinator |
| /sum-update   | Update or roll back the installation, then refresh running sessions      |

`bin/sumctl skills install` delegates explicit skill and agent selections to the pinned Vercel Skills CLI in project copy mode. See [docs/sum-skills.md](docs/sum-skills.md).

Agent-loaded procedures ship with the distro: `AGENTS.md` is a short role bootstrap every session loads, `COORDINATOR.md` is the coordinator core `init` names, and each action's detail stays in the bundled skills, loaded only for that action.

## Documentation

* [docs/architecture.md](docs/architecture.md) — session roles, the no-harness install, what's included, and state and communication.

* [docs/configuration.md](docs/configuration.md) — worker harness, presets, and the capacity schema.

* [docs/herdr-backend.md](docs/herdr-backend.md) — env start and stop, native event delivery, and native metadata.

* [docs/verification.md](docs/verification.md) — evidence publication, the nine gates, and brief revisions.

* [docs/repairs.md](docs/repairs.md) — in-scope and expansion repairs.

* [docs/update.md](docs/update.md) — update, rollback, refresh, and runtime releases.

* [docs/recovery.md](docs/recovery.md) — backup, cleanup after a merge, and limits.

*

* [docs/terminology.md](docs/terminology.md) — user, coordinator, agent, reviewer, task, brief, question, inbox, result.

* [AGENTS.md](AGENTS.md) — the role bootstrap every session loads.

* [COORDINATOR.md](COORDINATOR.md) — the coordinator core.

## Contributing

Tests, the isolated development checkout, and publication are in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. sum stands on projects it did not write — Firstmate, Herdr, Oh My Pi, Solo, Unpeel, and others. See [ATTRIBUTIONS.md](ATTRIBUTIONS.md) for the complete credit and what each one shaped.
