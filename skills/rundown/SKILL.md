---
name: sum-rundown
description: Reconcile saved tasks with bounded Herdr observations, surface unresolved questions, and recover without duplicate workers.
---
# Rundown

Run `./bin/sumctl inbox --live`. This makes one bounded status lookup per active recorded task. It is not a background monitor.
Surface unanswered decisions first, then reports ready for review, then failures/uncertainty. Keep unchanged status silent unless the boss asked for it.

For a worker that is idle/done/blocked without a report, read bounded relevant output with MCP `herdr_agent_read` or `bin/herdr-scoped agent read PANE --source visible --lines 120`.
Do not assume idle means done. Inspect ambiguous prose; ask for a file report when screen output is incomplete. If a question was never saved, capture it using `sumctl ask` before relaying it to the boss.
This is the MVP fallback for non-compliant agents. It runs during a rundown, not continuously; do not promise instant unattended detection.

Record the boss's actual answer with:

```sh
./bin/sumctl answer TASK_ID QUESTION_ID --text 'The authorized answer'
```

An answer stays visible until the worker marks it applied. A pending notice can be tried once with `sumctl notice TASK_ID --to worker` after checking the recipient is available. Do not loop.

## Restart

Run doctor in the new coordinator pane, then inspect saved tasks and actual Herdr inventory.
To make an existing task report to this coordinator, explicitly run `sumctl bind TASK_ID --parent-only`.
To adopt a known existing worker after a pane ID change, use `sumctl bind TASK_ID --worker-pane PANE` after verifying its cwd and task identity.
Never launch a replacement just because a pane cannot be observed. Missing/uncertain workers need inspection; sum has no automatic retry or process-fencing service.
Do not alter Herdr's global auto-resume policy. Herdr is the sole process/restore owner in this MVP.

## Backup

`sumctl backup /path/outside/state/sum-records.tar.gz` saves versioned task records, briefs, reports, and decisions with a manifest.
It is explicitly **records-only**. Worktree code, unpushed commits, dirty files, credentials, and live processes are not captured.
Back up code separately through the repo/host's existing process. A backup of this directory is not a full machine-crash recovery guarantee.
Restore into a new directory, retain the original, and inspect the manifest. Schema mismatches fail rather than attempting an in-place migration.
Do not reuse another machine's pane IDs. The helper refuses cross-machine interaction until explicitly rebound to recovered work.
