---
name: sum-factory
description: Run a per-project software factory: enable a lane, tick for the next ready GitHub issue, dispatch one worker, then merge or leave a human gate. Never a daemon.
---
# Factory

Use this only from the pane registered as coordinator.
Workers do not enable a factory or dispatch other workers.
Do not start a factory unless the user named the project and asked to run it.

A factory is one sequential lane on one enrolled GitHub project.
The coordinator still only routes.
Implementation happens in a dispatched worker that follows `skills/sum-factory-work/SKILL.md`.
Claim procedure is `skills/sum-factory-claim/SKILL.md`.
Merge and human-gate procedure is `skills/sum-factory-merge/SKILL.md`.

## Start

1. Enroll if needed: `./bin/sumctl project enroll owner/repo`.
2. Enable the lane:

```sh
./bin/sumctl factory enable owner/repo --lanes 1 --ready READY_KIND
```

Ready kinds: `label` (default, GitHub label `ready`), `issues` (oldest open issue first), `roadmap` (`--roadmap-issue N`), `project-status` (`--project-number N`).
Add `--strict-cleanup` when the project's intake requires cleanup before the next issue.
Add `--skip N` for owner-gated issues (deploy, DNS, credentials).
NiceBaaS uses `--ready issues`:

```sh
./bin/sumctl factory enable cofactorworks/nicebaas --ready issues --strict-cleanup --lanes 1
```

Completion: `factory status --project owner/repo` shows `enabled: true` and `held: 0`.

## Tick

```sh
./bin/sumctl factory tick --project owner/repo
```

One GitHub observation.
It never sleeps, never dispatches, never merges.

| `action` | Next |
| --- | --- |
| `dispatch` | Claim the named issue, then dispatch one worker |
| `occupied` | A lane is already held; wait for that task's report |
| `idle` | Nothing ready; `next_tick_at` is recorded; return control |
| `blocked` | Ready signal failed; relay the error, do not guess |
| `disabled` | Factory is off |

When idle, do not keep this pane in a loop.
The 5-minute wake is external: cron, launchd, or the harness scheduler calling the same `factory tick`.
A worker report or `hook enable` idle/done edge is the event-driven wake.
On the next inbox pass, tick once if a factory is enabled and a lane is free.

## Dispatch

After `action: dispatch`, follow `sum-factory-claim`, then `sum-dispatch` with `--project owner/repo`.
The brief is the issue body plus factory constraints: use `/lfg`, capture evidence with `.agents/skills/evidence`, do not merge, report with a handoff that lists every `comparison.json`.
Point the worker at `skills/sum-factory-work/SKILL.md` by naming it in the brief.
Then `factory claim owner/repo --issue N --task TASK_ID`.
Return control.

## After the report

Follow `sum-delivery` through independent verify, review, and `pipeline run`.
Then follow `sum-factory-merge`.
Do not pick the next issue until that skill says the lane is free.

## Stop

`./bin/sumctl factory disable owner/repo` when the user ends the factory and no lane is held.
Existing GitHub claims stay until released.

## Capacity

Factory workers consume ordinary worker reservations.
Do not raise capacity.
A refused dispatch names the held slots; park or wait.
