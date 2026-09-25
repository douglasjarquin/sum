# Recovery

Backup, cleanup after a merge, and limits. Moved out of the README.

## Recovery and backup

After reopening the coordinator, run `./bin/sumctl init`. If the previous coordinator pane is verifiably gone, run `./bin/sumctl init --role coordinator --reclaim`. It proceeds only when Herdr reports that pane as `pane_not_found`, or when a different occupant now holds that pane ID (`replaced`, see [Pane incarnation](#pane-incarnation)). A pane still held by the recorded occupant (even with its agent exited), or one whose occupant Herdr cannot establish, is refused, and reclaim never rebinds tasks by itself. Then run a rundown. To route an existing task back to the new coordinator:

```sh
./bin/sumctl bind TASK_ID --parent-only
```

If a worker's pane changed, inspect the actual agent and use `bind TASK_ID --worker-pane PANE_ID`. The helper checks the recorded worktree. It never creates a replacement automatically. Herdr remains the sole owner of process/session restoration; sum does not alter its auto-resume setting.

```sh
./bin/sumctl backup ~/backups/sum-records.tar.gz
```

This is deliberately **records-only**. The manifest lists the code worktrees that were **not** captured. Back up source code and unpushed work separately. No credential store is copied, but task text can itself contain sensitive information; protect the archive accordingly.

Restore into a new empty directory, inspect the manifest, and point `sumctl --home /restored/state` at it. Keep the original archive. Unknown schema versions fail without modifying them. Pane IDs are machine-local; cross-machine process recovery is manual in this MVP. The manifest's `machine` is the stable identity of the host that made it and `hostname` its name at the time.

## Machine identity

Herdr pane IDs such as `w5K:p1` are unique only within one Herdr server, so every recorded endpoint carries a `machine`, and sum refuses to reuse another machine's pane IDs. That value is `m-` and 32 hex digits: an HMAC of the operating system's machine ID (`/etc/machine-id` or `/var/lib/dbus/machine-id` on Linux, the `IOPlatformUUID` on macOS) under a sum-specific key. The raw machine ID is never stored. A Linux host with neither file gets a random ID created once at `~/.local/state/sum/machine-id`, outside any installation. On macOS, a failed `ioreg` read is an error, never a different identity.

Renaming the host changes nothing: the coordinator stays coordinator, registrations and return routes keep resolving, and `--reclaim` is not needed. A different host, including one that shares the hostname, is still another machine.

Records written before this identity existed carry the hostname instead. No manual step migrates them. Such a value names this host when it is the current hostname, or when this host recorded it under its own identity in `.sum/machine.json`; each registration (`sumctl init`, and dispatch for the worker pane) records the current hostname there. The coordinator's next `init` rewrites its own record and session file to the stable identity, and a session file keyed by the old hostname is still found and re-keyed when that pane registers again. A name recorded under a different identity never names this host.

A name this host carried only before this release is not provable. If the host was renamed under the older release and this release first runs under the new name, the coordinator record still names the old one: `init` returns `developer`, and reclaim is refused as other-machine. Recover without editing records: set the old hostname again (`hostnamectl set-hostname OLD`), run `./bin/sumctl init` in the coordinator pane, which records the name and rewrites the coordinator record, then set the current hostname back. Tasks recorded under the old name resolve from then on.

A malformed `.sum/machine.json` stops every command that needs the machine identity and names the file. Restore it from the copy you trust. Removing it only forgets the recorded names, so records under a name the host no longer carries become other-machine again.

* A VM clone is a different machine only if it regenerates `/etc/machine-id`, as cloned systemd hosts must. Such a clone does not inherit the original's coordinator, tasks, or hostname records, even with the whole `.sum/` copied and the hostname kept. A clone that keeps the machine ID is the same machine to sum.
* A records backup does not include `.sum/machine.json` or the coordinator claim. Restored on another host, its tasks and pane bindings are other-machine until recovered and bound explicitly. The exception is a record from before this identity whose hostname the restoring host happens to share, which resolves there as it did before.
* A reinstalled operating system with a new machine ID is a new machine: reclaim the coordinator and rebind tasks explicitly.
* A release from before this identity compares `machine` to the raw hostname. Once this release has written records, rolling back below it leaves those records other-machine to the older code, and its reclaim refuses them.

## Pane incarnation

A Herdr pane ID names an address, not an occupant. After a Herdr restart the saved layout comes back under the same pane IDs with new shells, and a pane that was never saved hands its ID to whatever is created next. So sum records what occupied a pane when it bound it and checks that occupant again before any authority or prompt. The record, `incarnation`, sits on the pane's registration (`sessions/*.json`) and, for the coordinator, on `context.json`. It holds the `terminal_id` and native `agent_session` Herdr reports, plus the pane shell's pid and OS start time. `init`, `prepare`, `dispatch`, `bind --worker-pane`, and `reclaim` write it; a verified delivery refreshes a worker's, and a verified coordinator command refreshes the coordinator's after a restore or handoff. Coordinator commands, the bridge and MCP (for anything but observation), delivery, `repair send`, and refresh check it.

| Outcome | What Herdr shows | Result |
| --- | --- | --- |
| `same` | the recorded `terminal_id` | the role stands |
| `new-conversation` | the same terminal, a different native session (for example a new harness conversation in the pane) | the role stands; the new session is recorded |
| `handoff` | a new `terminal_id`, the same shell process (pid and start time), as after a live handoff | the role stands; the new terminal is recorded |
| `restored` | a new terminal and shell, but Herdr resumed the recorded native conversation (same agent, same session id) | the role stands |
| `legacy-verified` | a record written before this release, whose pane shell already ran more than 2 s before the record's first occupancy time | the role stands, and this record is adopted at its next write |
| `replaced` | a new terminal, a different shell, no matching native session; or a legacy record older than the pane's shell | no role; the returns stay pending |
| `unrecorded` | a legacy record that cannot be proven either way | no role; the registration is left untouched |
| `unobservable` | Herdr cannot report the terminal or shell (unreachable server, stale socket) | no role; the registration is left untouched |

A refusal changes no task, question, answer, reservation, or delivery record. A `submitted` or `uncertain` delivery keeps its state: a recycled address never makes an ambiguous prompt safe to repeat. The recovery is always deliberate:

* Coordinator `replaced`, or pane gone: if the user confirms the pane should coordinate, run `./bin/sumctl init --role coordinator --reclaim` there.
* Coordinator `unrecorded`: if the user confirms the recorded coordinator is gone, close that pane so Herdr reports `pane_not_found`, then reclaim.
* Worker not verified: after inspecting the pane, the coordinator runs `./bin/sumctl bind TASK_ID --worker-pane PANE`. That records the worker registration for the inspected occupant.
* `unobservable`: rerun once Herdr answers.

Nothing is relaunched, and no replacement worker is started.

Limits:

* Herdr 0.9.0 has no server generation and no prompt precondition. A restart in the moment between sum's last observation and `agent prompt` can still deliver one prompt to the new occupant.
* `restored` trusts the native session an integration reports. Any process in the pane can report one: role bookkeeping is not an OS security sandbox.
* The legacy proof assumes the wall clock was not stepped backwards by more than the gap between a record and a later Herdr restart.
* An older sum helper neither records nor checks incarnation. While one runs (for example during a refresh), protection covers only this release's paths, and a record it rewrites becomes legacy again.

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

A merged PR archives nothing by itself, and nothing observes a merge on its own: `init`, `pump`, `bind`, hook events, and rundowns never call GitHub or apply cleanup; they list recorded open PRs (with the time each was last observed) and pending cleanup under `maintenance`.
`cleanup: pending` appears only after `sweep`, `pr reconcile`, or `cleanup` observed the merge.
When you say a task is merged, or a rundown shows a task as `cleanup: pending`, the coordinator runs the guarded cleanup:

```sh
./bin/sumctl cleanup TASK_ID                 # inspect: fresh GitHub observation, identity, occupants, artifacts; persists the plan, removes nothing
./bin/sumctl cleanup TASK_ID --apply         # remove the verified workspace and clean checkout through native Herdr, then archive the record
./bin/sumctl cleanup TASK_ID --reviewer-only # close only a bound reviewer pane that saved its findings and whose agent exited
./bin/sumctl sweep                           # every recorded open PR observed once, settled panes closed, then one guarded cleanup apply per pending task
```

`sweep` is the batch form: least recently maintained tasks first, each re-read before it acts, no further PR observed in that pass after a `gh` timeout, and no task started after its budget (`--budget SECONDS`, default 60; `0` starts nothing).
A started task finishes under its helpers' own bounds (each `gh` call up to 120 s), so the budget bounds admission, not wall time; tasks it did not reach are listed under `deferred` with their exact next command.
The same pass closes a worker pane after a report for the current candidate (or a terminal task state) and a reviewer pane after a verdict for the current candidate, via native `pane close`, after the pane cwd matches the recorded checkout.
Unanswered questions do not keep those panes open: `answer` writes the decision, and `execution resume` launches a fresh session that reads `context --role worker --section decisions`.
A closed pane is recorded so later delivery is `pane-closed`; `repair send` records the instruction and names resume.
A second sweep with nothing pending does nothing.

`--apply` proceeds only when every check passes, and every failed check is a named blocker in the output and in `show TASK_ID`:
the exact recorded PR must be observed **merged** with a merge commit and its head must be a recorded candidate (closed is not merged, a network or auth failure is uncertain and blocks);
the checkout HEAD must be that merged head or an ancestor of it, so squash and rebase merges pass without the original commits being on the default branch, while an extra local commit blocks;
all questions must be answered and applied and a structured handoff saved;
the workspace, pane, checkout, branch, and repository must match the task record by identity (never by label), the workspace must not be the coordinator's, and an unknown extra pane in the task workspace blocks;
the agent must be gone from the pane (Herdr `idle` or `done` is not exit), the pane must run only its shell, and no process may have a cwd inside the checkout, observed through `agent get`, `pane process-info`, and one bounded `lsof` pass; when that cannot be established the task stays cleanup-pending;
staged, modified, untracked, and ignored files block, except a fixed list of regenerable caches (`__pycache__`, `*.pyc`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `node_modules`, `.DS_Store`, `.artifacts`, `.codegraph`) and an ignored `.local/` that holds only the `.local/bin/sumctl` binary `mise run test`, `verify`, or `demo` builds from a checkout that tracks `go/cmd/sumctl` (any other file there, or a symlink in its place, still blocks), and nothing is ever `git clean`ed, reset, or force-removed.

The checks run again right before the removal, the removal is one `herdr worktree remove --workspace ID` without `--force`, and the cleanup intent is saved before that call.
Services the worker launched with `env start` (#17) are judged by identity: when the recorded pane, shell pid, pid, and argv still match, and only evidence-complete resource-state blockers remain, `--apply` stops exactly those first with one `ctrl+c`, a bounded exit wait, and a port check, closes the pane sum created, and re-inspects; a `service-unknown` blocker (restarted or replaced outside sum), a `writing` blocker (an owned log modified within the last seconds), or a survivor after the bound keeps the task cleanup-pending with the reason named. sum never kills by name, port, or cwd, never runs a broad compose down, and never closes a pane it did not create.
If sum is interrupted between the removal and the archive, the task shows `removing` and the next `cleanup TASK_ID` or `sweep` reconciles from records and observation (a rundown does not): verifiably absent resources complete the archive, a still-present workspace returns the task to pending, anything else blocks with the observed state.
Already-absent resources are accepted only after that identity inspection; nothing is recreated.
The task branch, `brief.md`, brief revisions, decisions, reports, handoffs, reviewer findings, and PR evidence always stay.
Cleanup records the released reservation before archiving.
Once cleanup saves destructive intent, worker, verifier, and service launches are refused until cleanup finishes or conclusively refuses removal.
An uncertain removal keeps that exclusion until reconciliation.
Competing cleanup commands for the same task, including the one a `sweep` runs, refuse while one owns the operation.
`archive --acknowledge` remains records-only, refuses any held reservation, and never removes anything.
Other workers keep running; there is no global stop, restart, or merge poll daemon.
