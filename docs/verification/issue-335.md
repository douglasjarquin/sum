# Issue 335 workflow repair

This record covers the repair following main `934085e0056300af8c9009c25cbebc43ea65da42`.
The installed coordinator's saved launch selects Grok; its saved model/reasoning fields are unset.
No account, model, permission, live installation or production core was changed during this work.
Excluded Grok Bot files and shared factory contracts remain outside the diff.

## Source repair ledger

| Finding | Repair | Executed evidence and limits |
| --- | --- | --- |
| F1 | Respect the caller deadline, give launch its own budget, and persist creation before readiness/opening. | Deadline regression fails at 20 seconds on base; candidate passes at 21 seconds within a 25-second context. Real-HC scripted application composition also passes with 21-second readiness delays per role. |
| F2 | Ordinary-shell exact-attempt inspection, native attach/read output and explicit coordinator space placement after original-process observation. | Delayed real-HC composition inspects the coordinator before status. Saved intent includes exact-attempt recovery command arguments. Actual desktop visibility remains unrun because Herdr Desktop and herdr-app are unavailable here. |
| F3 | Keep/placement changes no longer revoke a proven unchanged process; creation/PID/foreground/receipt checks continue to reject replacement. | Receipt/promotion regression, original-foreground regression and real-HC kept-shell replacement canary pass. Desktop clicks remain unrun. |
| F4 | Save incomplete PID evidence, establish foreground through production observation, and retain stronger receipt-bound evidence against late snapshots. | Worker redelivery no longer manually repairs the ledger. Synthetic composition exercises actual creation snapshots with initially absent foreground. |
| F5 | Preserve typed no-effect refusal, add exact-role opening redelivery, lost-response corroboration and safe early-exit stop handling. | Focused worker/reviewer/coordinator recovery tests pass. Lost-response candidates without corroborating creation/process evidence remain explicitly ambiguous rather than replaying. |
| F6 | Resolve and persist one HC route on explicit start; preserve selected scope and subprocess errors; report expected/observed protocol identity. | Named-route and failed-start regressions pass. Shared-core compatibility remains pinned HC 0.1.14/protocol generation 1; no foreign-core downgrade/restart is attempted. |
| F7 | Detect unsupported state before setup effects, preserve it and expose a read-only historical reader plus independent fresh-start instructions. | Setup regressions preserve schema-one bytes. There is no claimed live legacy migration. |
| F8 | Refuse and preserve stale/unverifiable setup helpers; embed staged build revision and distinguish actual helper checksum/build metadata from checkout source selection. | Stale-helper preservation and identity-stamp regressions pass. Unverifiable older helpers are explicitly labelled; modified builds retain a source digest and dirty marker. |
| F9 | Use the stable state root for installation/project roots and report preserved release-local enrollments. | Physical-generation/staged-runtime project-root regression passes. Dirty/active misplaced checkouts are never automatically moved. |
| F10 | Version new qualification records, preserve sealed provenance and compare normalized relevant settings for running recipients; separately qualify new executables. | Harmless model/config drift and on-disk binary replacement retain recipient qualification; permission/input drift refuses. Codex profile selectors and project trust levels are included. Historical records with changed raw config and no effective provenance require their original configuration or explicit qualified handoff. |

## Native runs

`mise run test-live -- --hc <exact published 0.1.14 binary> --artifact .artifacts/issue-335/native.json` passed both incarnation and smoke canaries with distinct JSON artifacts.
Both private cores confirmed stop.
`live_sum_canary.py` passed the full scripted Sum application lifecycle over the actual published core, with local strict fake GitHub.
A second run with `--ready-delay 21` also passed and recorded a durable inspectable coordinator endpoint while native status was absent and startup remained pending.
These runs cover approved brief/init, saved question/answer/application, report, separately addressed scripted reviewer, verification, private fixture publication and guarded cleanup.
They do not qualify real agent reasoning, external publication or a production update.

An isolated actual Grok process probe used `grok 1.0.67 (2fe064390d07) [stable]`.
Its foreground was the actual installed Grok executable, and its native status was `blocked`, kind `auth`, with “Sign in to Grok”.
No input, authentication, account change or model turn was attempted.
The probe retained its executable hashes and original process observation, then confirmed owned core stop.
Authenticated Grok first/follow-up turns and real multi-agent delivery remain unrun.

Artifacts are under the development checkout's ignored `.artifacts/issue-335/` directory.
The JSON records retain exact helper/source/runtime identities, commands, native addresses, attempt IDs, callbacks and teardown.
A new candidate requires a new verifier record; earlier native runs identify their actual source snapshot by hash rather than claiming a later commit automatically.

## Independent review and failure-boundary regressions

The full independent review of the first committed candidate used nine local lenses and a separately attributable Claude/Anthropic adversarial pass.
Its run `20261101-224012-issue335` found ten concrete gaps.
The repair preserves reconciled opening claims against late replies, records unsent state atomically with creation, handles stale sockets with no listener, adds coordinator/reviewer pre-init stop recovery, includes Codex profile/trust selectors, marks modified helper builds, and inspects reservations whose creation reply was lost.
The executable release contract now requires HC template schema 2, so selecting an older helper that cannot read these persisted templates is refused before activation or rollback.
This is an explicit incompatible-target refusal, not a claim that the baseline helper can manage new v2 agents.

Public-command regressions use a real local harness-shaped process while holding the native creation response.
Worker, reviewer and coordinator reconciliation submit one opening and retain it after the original creation call resumes.
The same worker/reviewer race regression fails against `ad08b7d` with duplicate openings and passes after the fix; its private baseline adds only the new test and native fixture boundary controls.
Worker/reviewer tests also refuse conflicting candidates and replaced foregrounds, and terminate the owned startup CLI after durable creation to prove later explicit redelivery needs no ledger editing.
Coordinator and reviewer startup-exit tests preserve failed-attempt history and permit an explicit successor only after stop proof.
Absent/blocked readiness submits no input; uninitialized worker stop tests reject a surviving original PID, surviving checkout process, missing exit metadata and native absence without conclusive exit.
These are automated boundary regressions, separate from authenticated agent or desktop acceptance.

## Remaining operator acceptance

Keep issue 335 open until the selected authenticated harness combination and actual desktop workflow are accepted.
Use a unique named lab HC session and private XDG roots, preserving the user's default/live core.
Start the selected coordinator, dispatch two approved workers in isolated worktrees, and launch a separately addressed reviewer for the exact candidate.
Exercise init, a saved question, one recorded answer, its application receipt, report, independent verification and review.
Do not answer trust/auth dialogs automatically.
Inspect an unready launch from an ordinary shell, resolve its dialog as the human, then run only the exact recorded legal recovery action.
In the associated desktop, verify the coordinator root, each worker/reviewer, peek, Keep-as-tab, window close/reopen and pane closure.
Confirm ordinary promotion retains the same original receipt/process, and actual shell recovery fails old authority.
Perform an explicitly authorized compatible HC-to-HC release A to B update and compatible rollback with pending work; retain exact helper identities, observer handoff and stable project paths.
No source-only check or fixture pass substitutes for these receipts.
