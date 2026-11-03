# Updating Sum

Only the user authorizes update or rollback.
The logical coordinator uses the installation's trusted helper with its captured claim.
Developers and task checkouts cannot activate the installation.
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

This procedure applies to a supported HC-native installation, only when the user authorizes its update and retirement.
Run its existing update procedure once from the last native coordinator after settling active work.
Before selecting this release, use that old installed helper's `hook disable` command so its post-selection observer handoff is skipped.
Then use the old helper's authorized `update apply` to select the verified release.
Start the user's usual agent in the installation and run `./bin/sumctl init --retire-native` under the explicit retirement decision.
No manifest repair or manual runtime-link change is needed.

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
