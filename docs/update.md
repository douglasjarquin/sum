# Updating Sum

Only the user authorizes update or rollback.
The logical coordinator uses the installation's trusted helper with its captured claim.
Marked development and task checkouts cannot activate the installation.
An ordinary conversation whose old helper reports `developer` may use the explicitly authorized bootstrap operation below.
Staging a candidate, passing local tests or opening a PR grants no activation authority.

## Inspect and stage

```sh
./bin/sumctl update check --claim TOKEN [--ref REF] [--no-fetch]
./bin/sumctl update stage --claim TOKEN [--ref REF] [--no-fetch]
./bin/sumctl update status
```

The target must be merged on the origin default branch.
A staged release contains its own verified helper, exact HC dependency, matched manual/templates and checksummed manifest.
Check and staging do not select it.
`build-info` identifies the executing binary independently of checkout HEAD.

## Quiescent activation

```sh
./bin/sumctl update apply --claim TOKEN [--ref REF] [--no-fetch]
./bin/sumctl update rollback --claim TOKEN [--to SHA|checkout]
./bin/sumctl update recover --claim TOKEN --generation GENERATION
```

Apply blocks on running, starting or uncertain attempts and unresolved launch, delivery or maintenance effects.
Saved questions and unapplied answers alone do not block.
Inspect and settle each blocker through its existing receipt or recovery command; do not edit records to bypass it.
Dirty or divergent installation checkout alignment refuses before selection.
No reset, stash, forced checkout, core restart or running-agent contract replacement completes an update.

The activation journal records durable pending intent, previous approved identity, atomic selection and entrypoint verification.
Recover an interrupted activation by its exact generation rather than repeating apply or switching links manually.
After success, checkout HEAD, selected runtime and entry instructions agree.
The next ordinary harness start reads those instructions and initializes with its own retained claim or the explicit transfer procedure.
Workers retain their sealed procedures; revised approved worker briefs use the existing task brief request/adopt commands.

## One-time retirement of a native coordinator

An ordinary conversation can retire a supported HC-native installation and upgrade it even after its registered coordinator exits.
The user must explicitly authorize the update and native retirement.
Keep the current Herdr pane and agent conversation; no special launch or native coordinator receipt is required.

On a helper that supports bootstrap, inspect first, then perform the authorized operation:

```sh
./bin/sumctl update bootstrap --ref MERGED_SHA
./bin/sumctl update bootstrap --ref MERGED_SHA --approve
```

When the selected helper predates bootstrap, extract the bootstrap driver from the approved merged target without selecting it:

```sh
git fetch origin main
bootstrap_script=$(mktemp -t sum-upgrade)
git show MERGED_SHA:scripts/upgrade.py > "$bootstrap_script"
python3 "$bootstrap_script" --installation "$PWD" --ref MERGED_SHA
python3 "$bootstrap_script" --installation "$PWD" --ref MERGED_SHA --approve
```

The driver verifies origin-default-branch ancestry before building and running the target's temporary helper.
It stages with the target-aware stager, preserving its complete executable capability metadata instead of using the old helper's lossy decoder.
An existing release with differing metadata is refused, never patched into acceptance.
Bootstrap inspects the native owner, all registered workers and reviewers, task endpoints, observer launch state and unresolved launch, delivery, preparation and maintenance effects under the final delivery and state locks.
A finished coordinator alone is insufficient: program exit, recorded PID death and available survivor evidence are required for the owner and task programs.
A live observer may remain, but retirement drains admitted delivery and fences its subsequent ledger writes and native sends.
An unresolved observer launch blocks retirement.

A stopped worker may still have a receipt-confirmed `running` reservation after its native coordinator exits.
Bootstrap inspection previews the exact task, attempt and generation that can be released using fresh native exit, original process, checkout survivor and owned-service evidence.
Inspection changes no task records.
Under `--approve`, the journal binds those releases and the final delivery/state locks protect fresh validation and recording before the writer fence.
No old coordinator receipt or separate `execution park` command is required for those proven-stopped workers.
The ordinary `execution park` authorization remains unchanged.

Only settled, receipt-confirmed worker launches in running, waiting or reported tasks qualify.
Live or unobservable programs, surviving checkout processes, unresolved creation or delivery effects, preparation, active maintenance and service reservations remain blockers.
Archived tasks are never reopened.
A released reservation means stopped capacity, not accepted work: reports, questions, briefs, checkouts and native history remain available, with task status waiting or reported as appropriate.
A partial release is recorded with its bootstrap operation; exact-operation recovery rechecks stop evidence and generations without releasing an attempt twice.
If a survivor appears after an interruption, recovery refuses until it is settled.

Bootstrap stops no process or terminal.
An isolated reproduction core or other unrecorded checkout process must be settled through an explicitly authorized action against that exact resource before approval.
The live Herdr core is not a substitute target.

The private `.local/bootstrap.json` journal records installation, state generation, prior runtime, historical owner, target manifest hash and planned logical session before raising the writer fence.
Interruption requires the exact operation and target printed in the refusal, using the same driver with `--approve --operation OPERATION --ref MERGED_SHA`.
Recovery moves forward through the compatible target; it never reselects the obsolete writer after the format fence.
It recovers interruptions before the fence, between fence and ownership, after ownership, after observer preference retirement, after runtime selection and after checkout alignment.
The same planned session yields the same claim, including when the original successful response was lost.

Success returns `role: coordinator` and `claim` directly to this conversation.
Continue here with `./bin/sumctl init --claim TOKEN`, read its validated coordinator procedure, and carry that claim on subsequent mutations.
Bootstrap creates no agent or terminal and sends no prompt to another conversation.
The ordinary Herdr launch procedure stays unchanged.

Retirement drains delivery and state locks and refuses unresolved current launch, delivery or maintenance effects.
It raises the root state pointer to format 3 before publishing logical ownership so old helpers reject both the root and selected physical generation path.
A process paused at final native authorization also fails its locked recheck.
The generation header remains format 2; task records, native owner record and sealed procedures retain their bytes.
The retirement record bridges historical native parents to the logical session.
It archives native-event preference/health and leaves existing `.wake.json` sidecars unchanged.
The format fence proves ledger-write and native-send exclusion by the old helper, not universal exclusion of preauthorized process or Git I/O.
This development change does not itself authorize or prove a live cutover.

## Compatibility and rollback floor

The release contract retains `supports.wake_protocol` and `supports.observer_protocol` solely to mean that historical sidecars are readable and never rewritten.
Those fields allow the previous updater to stage this release; they do not advertise active wake or observer commands.
The new updater has no wake/observer activation gate.
A target's actual helper must support logical owner schema 2 and root state format 3 once either the root format fence or `coordinator.json` raises that floor.
A crash after the fence but before session publication does not permit selecting an older writer.
Rollback below that floor is refused before activation and names the record that requires it.
Compatible rollback preserves all task history, obligations and uncertain effects.

## Schema-one state and preserved records

Normal setup, Open, init and status never convert schema-one state.
Keep the original installation, processes and worktrees intact and inspect saved records with `sumctl history`.
Prepare an independent fresh clone and run `mise run setup` there.
A records-only backup restore is quarantined and transfers no runtime authority.
Never rewrite historical bytes or adopt old records as live authority.

## Evidence boundary

Local verification, model-free native canaries, authenticated harness journeys, hosted CI and production activation are separate evidence.
Record the exact candidate and checks; mark unrun combinations as not run.
Only the user merges or activates the result.
See [acceptance](ACCEPTANCE.md) and [recovery](recovery.md).

## HC compatibility and known-good promotion

Each candidate release verifies its bundled HC binary and matched manual against its own pin without querying a running core.
Running cores must report protocol generation 1 and a version at or above 0.1.14.
Sum never starts, restarts or replaces the user's core.
When a launch has no compatible listener, its refusal names the socket, task and attempt and gives a Herdr repair hint.

Apply and rollback promote the serving default to known-good using its recorded path and SHA, its helper's owner-schema and state-schema floors, and a stable-entrypoint post-check.
Promotion does not re-verify the outgoing HC binary, manual or retired schema receipt.
Candidate artifact validation remains required.

The first activation of this behavior from a helper that still verifies `hc schema` uses the bootstrap driver above, extracted from the merged release.
The user merges and separately authorizes activation; preparing a PR changes no installation runtime.
Rolling back to an older release may succeed while restoring a helper that refuses launches on a newer core because it still requires exactly HC 0.1.14.
