---
name: sum-develop
description: Improve sum itself from an isolated development checkout and lab state, without touching the installation that serves the live coordinator.
---
# Develop sum

Use this when the boss asks a second thread in the sum installation to change sum, or when you registered as `developer` and have approved work on sum.
The installation checkout serves the live coordinator and its workers. Nobody edits, builds, or tests there while it is in service; one writer per checkout.

## Prepare the checkout

From the installation directory, with the installed helper:

```sh
./bin/sumctl dev prepare --name short-topic          # add --pane for an ordinary Herdr pane starting there
```

This runs plain `git worktree add` into `.sum/dev/<name>` on branch `sum-dev/<name>` from `HEAD` (or `--base REF`) and writes `.sum/dev.json` inside the new checkout.
Read the returned `path`, `branch`, `role`, and `note`; tell the boss the path you will work in and that you remain a developer there.
Rerunning the same name reopens the existing checkout with its uncommitted work intact.
The helper refuses symlinked or overlapping paths and never nests a development checkout inside another one.
`--pane` creates a Herdr workspace whose root pane starts in the checkout; it starts no agent and copies no coordinator identity.

Move all edits, builds, and tests into that path. Run `./bin/sumctl init` there; it reports `developer` and writes nothing.

## Keep production separate

- The checkout's own `.sum`, `.local`, `.deps`, generated MCP configs, and artifacts live inside the checkout. `mise run setup` there installs its own dependencies and leaves the checkout undesignated because of `.sum/dev.json`; it can never host a coordinator.
- Do not share a writable `.deps` or `.local` tree with the installation. mise's read-only tool installs are fine to reuse.
- The candidate `bin/sumctl` refuses every write aimed at the installation's state home, including through an inherited `SUM_HOME`. Only `show`, `status`, `inbox`, and `doctor` are allowed there. Use lab state (`--home` under a temporary directory) for everything else.
- Tests and demos blank inherited `SUM_HOME`, `SUM_SESSION`, and `HERDR_*` values and use a fake Herdr or a named lab session such as `sum-test-<id>`; never the user's `default` session.
- If your work is a dispatched task on sum, its brief's callbacks use the installed trusted helper `<installation>/bin/sumctl`; keep using exactly those commands.

## Verify and ship

Run the offline suite and demo in the checkout, then the live smoke test when a real Herdr is available:

```sh
python3 -m unittest discover -s tests -p 'test_*.py' -v
node --test tests/mesh.test.mjs
python3 scripts/demo.py
mise run test-live
```

Commit on the `sum-dev/<name>` branch, then follow `skills/delivery/SKILL.md` like any other project: verification, a reviewable PR, no merge.
Candidate code becomes installation code only when a human merges it and the coordinator picks it up; a checked-out branch is not an update.

## Clean up

`./bin/sumctl dev list` shows checkouts and whether they are dirty.
`./bin/sumctl dev remove --name short-topic` uses plain `git worktree remove` and `git branch -d`: it refuses dirty trees and keeps unmerged branches. There is no forced removal.

## Bootstrap without `sumctl dev`

On an installation running a release before this feature, use the same Git primitives by hand from the installation directory:

```sh
git worktree add -b sum-dev/<name> .sum/dev/<name> HEAD
mkdir -m 700 .sum/dev/<name>/.sum
printf '{"schema": 1, "kind": "development", "name": "<name>", "installation": "%s", "installation_home": "%s/.sum", "branch": "sum-dev/<name>"}\n' "$PWD" "$PWD" > .sum/dev/<name>/.sum/dev.json
cd .sum/dev/<name>
./bin/sumctl init --role developer
```

Pass `--role developer` explicitly on the older release so a setup-designated checkout cannot claim a coordinator.
Install nothing into the live checkout to get started.
