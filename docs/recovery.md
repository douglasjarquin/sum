# Recovery

Backup, cleanup after a merge, and limits. Moved out of the README.

## Recovery and backup

After reopening the coordinator, run `./bin/sumctl init`. If the previous coordinator pane is verifiably gone, run `./bin/sumctl init --role coordinator --reclaim`; it proceeds only when Herdr reports that pane as `pane_not_found`; a pane that still exists (even with its agent exited) or that Herdr cannot observe is refused, and it never rebinds tasks by itself. Then run a rundown. To route an existing task back to the new coordinator:

```sh
./bin/sumctl bind TASK_ID --parent-only
```

If a worker's pane changed, inspect the actual agent and use `bind TASK_ID --worker-pane PANE_ID`. The helper checks the recorded worktree. It never creates a replacement automatically. Herdr remains the sole owner of process/session restoration; sum does not alter its auto-resume setting.

```sh
./bin/sumctl backup ~/backups/sum-records.tar.gz
```

This is deliberately **records-only**. The manifest lists the code worktrees that were **not** captured. Back up source code and unpushed work separately. No credential store is copied, but task text can itself contain sensitive information; protect the archive accordingly.

Restore into a new empty directory, inspect the manifest, and point `sumctl --home /restored/state` at it. Keep the original archive. Unknown schema versions fail without modifying them. Pane IDs are machine-local; cross-machine process recovery is manual in this MVP.

## Limits worth knowing

* Plain-text questions from non-cooperative workers are found during a rundown, not guaranteed to be detected immediately while unattended.

* This is a trusted-local workflow, not an adversarial sandbox. Workers share your execution account unless you supply isolation. Profiles/instructions do not isolate credentials.

* Repository tests run repository-authored code. Use existing safe dev containers/CI and retain normal harness permissions.

* No automatic worker replacement, cross-machine ownership transfer, hard cost enforcement, or exactly-once PR publication.

* Cleanup establishes that an agent and its children exited only through Herdr observation and a process-cwd scan; a writer that runs from outside the checkout is not detected, and cleanup never kills anything.

* Shell-capable harnesses share the task contract. Native instruction, MCP, quota, and resume capabilities still vary. The source does not claim all harnesses have been live-certified.

* Protocol/version mismatches fail visibly. No tmux fallback, terminal-banner classifier, or silent downgrade is installed.

* macOS and Linux are the targets. Windows is not supported by the file-locking helper in this MVP.

### Cleanup after a merge

A merged PR archives nothing by itself.
When you say a task is merged, or a rundown shows a task as `cleanup: pending`, the coordinator runs the guarded cleanup:

```sh
./bin/sumctl cleanup TASK_ID                 # inspect: fresh GitHub observation, identity, occupants, artifacts; persists the plan, removes nothing
./bin/sumctl cleanup TASK_ID --apply         # remove the verified workspace and clean checkout through native Herdr, then archive the record
./bin/sumctl cleanup TASK_ID --reviewer-only # close only a bound reviewer pane that saved its findings and whose agent exited
```

`--apply` proceeds only when every check passes, and every failed check is a named blocker in the output and in `show TASK_ID`:
the exact recorded PR must be observed **merged** with a merge commit and its head must be a recorded candidate (closed is not merged, a network or auth failure is uncertain and blocks);
the checkout HEAD must be that merged head or an ancestor of it, so squash and rebase merges pass without the original commits being on the default branch, while an extra local commit blocks;
all questions must be answered and applied and a structured handoff saved;
the workspace, pane, checkout, branch, and repository must match the task record by identity (never by label), the workspace must not be the coordinator's, and an unknown extra pane in the task workspace blocks;
the agent must be gone from the pane (Herdr `idle` or `done` is not exit), the pane must run only its shell, and no process may have a cwd inside the checkout, observed through `agent get`, `pane process-info`, and one bounded `lsof` pass; when that cannot be established the task stays cleanup-pending;
staged, modified, untracked, and ignored files block, except a fixed list of regenerable caches (`__pycache__`, `*.pyc`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `node_modules`, `.DS_Store`), and nothing is ever `git clean`ed, reset, or force-removed.

The checks run again right before the removal, the removal is one `herdr worktree remove --workspace ID` without `--force`, and the cleanup intent is saved before that call.
Services the worker launched with `env start` (#17) are judged by identity: when the recorded pane, shell pid, pid, and argv still match, and only evidence-complete resource-state blockers remain, `--apply` stops exactly those first with one `ctrl+c`, a bounded exit wait, and a port check, closes the pane sum created, and re-inspects; a `service-unknown` blocker (restarted or replaced outside sum), a `writing` blocker (an owned log modified within the last seconds), or a survivor after the bound keeps the task cleanup-pending with the reason named. sum never kills by name, port, or cwd, never runs a broad compose down, and never closes a pane it did not create.
If sum is interrupted between the removal and the archive, the next `cleanup TASK_ID` or `inbox --live` reconciles from records and observation: verifiably absent resources complete the archive, a still-present workspace returns the task to pending, anything else blocks with the observed state.
Already-absent resources are accepted only after that identity inspection; nothing is recreated.
The task branch, `brief.md`, brief revisions, decisions, reports, handoffs, reviewer findings, and PR evidence always stay.
Cleanup records the released reservation before archiving.
Once cleanup saves destructive intent, worker, verifier, and service launches are refused until cleanup finishes or conclusively refuses removal.
An uncertain removal keeps that exclusion until reconciliation.
Competing cleanup commands for the same task refuse while one owns the operation; live-inbox reconciliation cannot rewrite an active cleanup.
`archive --acknowledge` remains records-only, refuses any held reservation, and never removes anything.
Other workers keep running; there is no global stop, restart, or merge poll daemon.
