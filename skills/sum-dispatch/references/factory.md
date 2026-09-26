# Coordinator procedure: factory lane

Part of `sum-dispatch`. Read it when the user asks to run, tick, or stop a factory on an enrolled project. Run a per-project software factory: enable a lane, tick for the next ready GitHub issue, dispatch one worker, then merge or leave a human gate. Never a daemon.

Use this only from the pane registered as coordinator.
Workers do not enable a factory or dispatch other workers.
Do not start a factory unless the user named the project and asked to run it.

A factory is one sequential lane on one enrolled GitHub project.
The coordinator still only routes.
Implementation happens in a dispatched worker that reads the pinned on-demand worker file `skills/sum-worker/references/factory.md`.
Claim procedure is `skills/sum-dispatch/references/factory-claim.md`.
Merge and human-gate procedure is `skills/sum-dispatch/references/factory-merge.md`.

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

## Digest

`./bin/sumctl factory status --project owner/repo` adds a `digest` to the lane summary: a view of saved records only.
It never ticks, claims, merges, cleans up, calls GitHub, or writes; tick, claim and merge are unchanged.
Each row gives the current issue and task from the held lane (never guessed), the pipeline stage, `action_owner` (`human decision`, `coordinator`, `worker`, `none`), a `blocker` only when a saved question, attention or failed gate record exists, and outcomes with their source record, candidate and time: `reported`, `verified`, `review-accepted`, `pr-open`, `observed-merged`, `cleanup-pending`, `lane-free`.
A `factory merge` result is a request, never `observed-merged`; only a saved `pr reconcile` observation with a merge commit is.
An outcome is attributed to the factory only while a lane record names its task; after release it is a project outcome labelled `factory linkage not recorded`.
`next_tick_at` is the time the last idle tick recorded, not a scheduled wake. `pr_observed_at` and `ci_observed_at` are saved observation times; `ci_stale: true` means a later record exists.
`next.intake` stays `unknown (not yet observed)` unless a record names it; nothing here promises the lane will advance without a tick.
Pass the returned `cursor` back with `--since` to label outcomes not yet rendered `new`; `--limit` bounds the page and `page.continuation` says the rest is uncovered.
A foreign, stale, truncated or shrunk-source cursor returns `resync` with the full digest and labels nothing new.

## Dispatch

After `action: dispatch`, follow the factory claim procedure, then `sum-dispatch` with `--project owner/repo`.
The brief is the issue body plus factory constraints: use `/lfg`, capture evidence with `.agents/skills/evidence`, do not merge, report with a handoff that lists every `comparison.json`.
Say in the brief that this is a claimed factory issue, so the worker reads its pinned factory file.
Then `factory claim owner/repo --issue N --task TASK_ID`.
Return control.

## After the report

Follow `sum-delivery` through independent verify, review, and `pipeline run`.
Then follow the factory merge procedure.
Do not pick the next issue until that skill says the lane is free.

## Stop

`./bin/sumctl factory disable owner/repo` when the user ends the factory and no lane is held.
Existing GitHub claims stay until released.

## Capacity

Factory workers consume ordinary worker reservations.
Do not raise capacity.
A refused dispatch names the held slots; park or wait.
