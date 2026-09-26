# Update

Update, rollback, refresh, and runtime releases. Moved out of the README.

## Update and roll back

```sh
./bin/sumctl update check          # fetch origin, resolve the merged revision, report default/active/compatibility
./bin/sumctl update apply          # stage the release, validate coexistence, switch .local/current, fast-forward a clean clone
./bin/sumctl update status         # old/new SHA, default versus active runtime, staged releases, recent selections
./bin/sumctl update rollback       # reselect the recorded previous approved runtime; records, questions, reports, and worktrees stay
./bin/sumctl update recover --generation GENERATION  # resolve one interrupted activation, without repeating the update
```

An update activates only a revision merged on the sum `origin` default branch, resolved to an immutable SHA.
It fast-forwards a clean installation clone to the selected SHA.
It never resets, stashes, or force-updates a dirty or diverged tree, never edits a development or task checkout, never restarts Herdr, agents, dev services, or a connected MCP server, and never upgrades Herdr.
Network, build, and dependency work happen before the activation lock; validation covers the release manifest, the state and brief schemas of the recorded tasks, the stable machine identity once the records carry it, the installed Herdr, the pinned tools, and a read-only run of the candidate helper against the real records.
Staged files are not approval: `.local/approvals.json` binds approved revisions to this installation, while `.local/activation.json` records the known-good runtime and any pending activation.
Before switching, SUM records `pending.recovery.argv` against the previous known-good helper and checks that helper with `--version`.
If the new stable entrypoint fails its check, SUM restores that exact previous target and checks it.
If recovery also fails, it preserves the pending operation and reports both failures.
An interrupted update requires explicit generation-bound recovery before another apply or rollback; the atomic pointer change alone does not prove activation completed.
Rollback requires an approved compatible target, and checkout rollback also requires a clean checkout with a matching approval.
Once the coordinator's `context.json`, a session registration, or a task records a stable `m-` machine identity, apply, rollback, and recover refuse a target that predates it: a staged release whose own tree lacks `go/internal/machine/machine.go`, the file #202 added with the identity (read from its verified `files` list, never from a field the staging runtime writes), or a checkout whose prebuilt `.local/bin/sumctl` does not report `supports.machine_identity` 1 in its `release-contract` (that helper is the code a checkout target runs, and nothing binds it to HEAD; rebuild it with `mise run test` when HEAD is newer). That older code compares recorded machines to the raw hostname, so it would demote the coordinator and refuse its reclaim as other-machine.
`--allow-pre-machine-identity` overrides the refusal when the user decides to select such a release anyway, and is recorded in `.local/updates.jsonl` as `machine_identity_override`.
The `wake_protocol` compatibility row refuses a target that does not keep the coordinator wake sidecar (a release whose tree lacks `go/internal/returns/wake.go`, or a checkout whose prebuilt helper does not report `supports.wake_protocol`) while any sidecar has an outstanding, uncertain, prepared, or unreadable episode: that code would prompt the coordinator over the episode and could not reconcile it. There is no override flag. The way out is the protocol's own settlement from the serving runtime: `sumctl wake consume` after the coordinator has read its wake, or `sumctl wake reconcile` for a prepared, interrupted, or replaced-occupant episode (`sumctl wake show` inspects them). An unreadable or foreign sidecar is different: sum never rewrites it, so neither command settles it; the refusal names the file by path, and the way out is to inspect it (`sumctl wake show`) and move or remove the file by hand, then retry. A closed episode, the runtime already serving, or a capable target passes.
Commands already running finish on the runtime they resolved; worker briefs carry stable `<installation>/bin/sumctl` commands, so their callbacks keep working across the switch; new dispatches use the new default; connected MCP clients keep their tool set until the client itself restarts.
A refusal names the exact incompatibility and leaves the old installation serving.
See `skills/sum-update/SKILL.md` for bootstrap, canary, and rollback steps.

### Refresh running sessions

```sh
./bin/sumctl refresh request               # coordinator contract plus every non-archived task; --task TASK_ID or --coordinator narrows it
./bin/sumctl refresh status                # confirmed / submitted-unconfirmed / pending-busy / pending-unreachable / unreachable / capability-deferred
./bin/sumctl refresh adopt --coordinator rN
```

An update changes which code new commands run; a refresh asks the sessions that are already running to reread their operating instructions, one at a time, without restarting anyone.
For each target the coordinator stages the next immutable revision from the current runtime (a worker brief `briefs/rN.md`, or a coordinator contract snapshot under `.sum/coordinator/` carrying `AGENTS.md` and `COORDINATOR.md`), records it as requested, and then makes one delivery attempt through Herdr's agent boundary: only a pane that exists, runs in the recorded checkout, and is reported idle or done receives a short fixed message naming the revision, the machine-generated change summary, the file, and the exact adopt command.
No question, answer, or report text is ever placed in that message.
A busy, blocked, unknown, or refusing session keeps working on its current brief and shows as pending with the exact reason; nothing polls, sleeps, or relaunches it, and a later `refresh request` or `inbox` rechecks it.
A worker pane Herdr reports gone (`agent_not_found` / `pane_not_found`) is `unreachable` for that revision: delivery is terminal, not a looping worker inbox item.
A worker adopts at its next safe point with `brief adopt`, keeping its process, checkout, partial edits, commits, report, and repair count; the coordinator adopts its own contract with `refresh adopt --coordinator`.
Four things stay separate: the installation default, the runtime a process actually resolved, the revision requested of a session, and the revision that session reports it has read.
A submitted prompt is not a receipt and a receipt is not proof of compliance.
Two requests before a receipt coalesce to the newest revision, a stale receipt is refused, a rollback stages the next revision from the rolled-back runtime, and already-connected MCP clients keep their tool set (`capability-deferred`) until they restart.
Developer sessions are excluded from the fan-out.

## Runtime releases

The checkout where setup ran is the installation: it owns `.sum/`, the generated MCP settings, and the absolute `bin/sumctl` path in every worker brief. Code and dependencies can be staged separately as an immutable, commit-addressed release without touching anything a running coordinator, worker, or MCP server uses:

```sh
./bin/sumctl release stage            # builds .local/releases/<sha> for HEAD; nothing is activated
./bin/sumctl release list
```

A release holds the committed sum tree, its own pinned tool links, its own Go Mesh binary, and a `release.json` manifest with source SHA, content hashes, dependency pins, and contract versions. It is validated before it appears, kept read-only, and never contains state. Re-running `mise run setup` never rewrites an existing native binary or retargets a tool link either. Switching a live installation onto a staged release is a separate, explicit step that this version does not perform. See [dependencies](DEPENDENCIES.md).
