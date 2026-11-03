---
name: sum-update
description: Inspect and stage merged Sum releases, perform user-authorized quiescent updates and compatible rollback, and preserve task records.
---
# Update sum

Only the user authorizes update or rollback.
Use the installation's trusted helper and this conversation's captured `--claim TOKEN` for coordinator mutations.
A developer or task checkout cannot publish into the installation.
For an old native installation without a live coordinator receipt, use the explicitly authorized bootstrap driver in `docs/update.md` from the same ordinary conversation.
Bootstrap returns its new logical claim here; do not relaunch the agent or require a native coordinator launch.
Its inspect previews exact receipt-confirmed stopped worker releases; `--approve` journals and rechecks them without requiring the dead native coordinator to run `execution park`.
Surviving processes and unresolved effects still block; bootstrap stops nothing and never accepts or discards task work.
Read `docs/update.md` from the repository root for the complete activation and one-time native-retirement procedure.

```sh
./bin/sumctl update check --claim TOKEN [--ref REF] [--no-fetch]
./bin/sumctl update stage --claim TOKEN [--ref REF] [--no-fetch]
./bin/sumctl update status
./bin/sumctl update apply --claim TOKEN [--ref REF] [--no-fetch]
./bin/sumctl update rollback --claim TOKEN [--to SHA|checkout]
./bin/sumctl update recover --claim TOKEN --generation GENERATION
```

Check and staging do not grant activation approval.
Targets must be merged on origin's default branch and verified through their actual helper capabilities.
Apply requires quiescent work: running, starting or uncertain attempts and unresolved launch/delivery/maintenance effects block.
Questions and unapplied answers alone do not block.
Dirty or divergent checkout alignment refuses before runtime selection; never stash, reset or force it.
Recover a pending activation by its exact journal generation.

Success aligns installation HEAD, selected runtime and entry instructions.
The next ordinary agent start adopts them; no live coordinator-contract replacement or agent restart occurs.
Worker brief revisions remain available through `brief request` and `brief adopt`; they never rewrite a sealed revision or grant new approval.
Logical ownership and root state format 3 impose a rollback floor even if retirement stopped before publishing the session record.
Historical task, native-owner and sealed procedure bytes are preserved.
The root format fence excludes old helper ledger writes and native sends, not all preauthorized OS/Git effects.
Schema-one installations remain intact and need an independent fresh workspace; no live conversion is supported.

Keep offline checks, isolated real-HC probes, authenticated harness qualification, hosted CI and production activation separate.
Record exact results and mark missing journeys as not run.
Only the user merges or activates.
