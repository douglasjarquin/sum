# Sum on Grok Bot

This file is an installer.
Follow it when the user asks to install or update Sum.
The source-controlled recipe is `grok-bots/sum/`; read the files rather than inventing instructions or generating a hidden runtime.
No local Sum installation is required.

## Discover and identify

Discover the native operations actually available for roster lookup, Bot creation, chat/memory, shared-Bot descriptions, skills, routines, messages, files, and connected apps.
Record the observed operation and its supported inputs for each logical step; do not assume literal tool names or schemas.
Missing required access is a precise blocked component, not permission to invent an API.
Use the current [Bots guide](https://docs.x.ai/grok-bot/bots), [shared Bots guide](https://docs.x.ai/grok-bot/shared-bots), and [skills/routines guide](https://docs.x.ai/grok-bot/skills-routines-and-automations) as context; inspect this deployment's capabilities too.

Read the private `/workspace/sum/installation.md` when present.
Keep one installation record with source URL/location/SHA, Bot type and actual native identities, owned skill/routine identities, persistent or temporary worker lifetimes, observed capabilities, and each component's requested/adopted revision and acknowledged/deferred/failed status.
Store no secrets in it and never include it in a shared template.

Inspect the roster by native identity and ownership.
A display name such as Sum is not proof of ownership.
Reuse the recorded Bot only after checking its identity and intended role.
Adopting an unrecorded existing Bot needs the user's confirmation of identity and ownership; leave unrelated matching names alone.
Create one new Bot only when no owned coordinator exists and creation is authorized.
Record its actual returned identity before proceeding.
A repeat installer uses the same owned identities, never a second coordinator.

For a personal Bot, deliver lasting instructions through supported chat and ask it to remember the role, boundaries, canonical source paths, and revision.
Personal Bots have no description/settings page.
For a shared Bot, use the supported description surface only as its owner or Manager; preserve unrelated configuration.
A shared Bot cannot be primary.
Offer guidance for choosing personal Sum as primary; preserve the user's existing primary selection unless they choose to change it.

## Source and component inventory

Discover the recorded source location and validate its Git origin before touching it.
An older installation may use `/home/box/agent-data/sum/src/`; that is not a platform guarantee or a reason to move a working installation.
For a new installation, use a confirmed durable source directory, such as `/workspace/sum/source/`.
Clone `https://github.com/douglasjarquin/sum.git` if absent.
Preserve dirty source and active workers' referenced revisions; do not reset, force-pull, or remove them.
Fetch and resolve the user-selected revision, or the remote default branch, to one exact SHA before applying any component.
Use a separate revision directory when active work still depends on the old source.
Validate `/workspace` durable file access before writing authoritative records.

Use this same inventory for fresh installation and every update:

| Source in `grok-bots/sum/` | Installed component |
| --- | --- |
| `instructions.md`, `memories.md` | Sum coordinator role and memories |
| `worker-procedure.md` | Sum Worker procedure and each owned worker's safe-boundary adoption |
| `permissions.md` | Sum Permissions |
| `decisions.md` | Sum Decision ledger schema; never replace live lines |
| `action-log.md` | Sum Action log schema; never replace live lines |
| `skills/dispatch/SKILL.md` | Sum Dispatch |
| `skills/persist/SKILL.md` | Sum Persist |
| `skills/verify/SKILL.md` | Sum Verify |
| `skills/status/SKILL.md` | Sum Status |
| `skills/recap/SKILL.md` | Sum Recap |
| `skills/deliver/SKILL.md` | Sum Deliver |
| `skills/sweep/SKILL.md` | Sum Sweep |
| `skills/factory/SKILL.md` | Sum Factory |
| `routines.md` | Owned Inbox status routine prompt, when installed |
| `avatar.jpg` | Sum profile image, where supported |
| `../square/` via `GROK_SQUARE.md` | Installed Square role, memories, steward procedure, settings schema, owned routine prompts |

Skills are account-wide: use these Sum-specific names and each source description and six fields.
Read back each installed skill by returned identity and confirm it is available to Sum.
For an older install with no ownership record, inspect native IDs, installed content/revisions, Bot access, and routine owners without changing them.
Show the exact proposed legacy skill/routine identity list to the user for ownership confirmation and record their actual decision before adoption.
Neither a generic name nor similar source text proves ownership.
After confirmed adoption, record those returned identities and resolve active references before retiring superseded owned rules.
Do not create duplicate replacements while an identified owned installation is awaiting reconciliation.
Reconcile older generic names only after proving recorded ownership and resolving references from active work.
Do not overwrite unrelated global skills or leave superseded owned rules active after claiming adoption.
If ownership or safe retirement is uncertain, mark that component deferred and report the conflict.
Do not install extra plugins from this recipe.
The `evals/` files are offline invariant seeds, not installed workflows or live acceptance.
Factory recommendations stay at `skills/sum-factory/factories/`; preserve confirmed private choices under `/workspace/sum/factories/`.
The software factory requires its separately preserved compatible legacy installation, never this HC instruction checkout.

## Apply, acknowledge, and recover

Apply and acknowledge each inventory component separately at the resolved SHA.
Refresh coordinator rules, all eight skills including Factory, permission/schema memories, worker rules, installed Square, and owned routine prompts.
Keep persistent specialist responsibilities, restrictions, learning, connections, preferences, task files, ledger/history, factory choices, and routine enabled/paused state.
Never substitute generic worker rules for Square's steward role.
Do not interrupt busy workers to update their roles; record deferred adoption and refresh at their next safe task boundary.
Keep old referenced source readable until those tasks and their review are finished.
Initialize missing live logs with headers only.
For an older filled log, preserve every line and record its schema boundary; unresolved legacy effects need reconciliation before issuing new writes.

If an update partly fails, keep the component inventory with old and requested revisions and the failure evidence.
Resume only failed/deferred components after inspecting what actually changed; do not replay acknowledged side effects or restore old task/decision history.
There is no atomic all-Bots update, global restart, or completion barrier.
Report updated, deferred, and failed components and their actual revisions, not just the fetched commit.
Do not start a factory in the update turn.

## Credentials and acceptance

Use [secure credential entry](https://docs.x.ai/grok-bot/vault), never tokens in chat or ordinary files.
The [computer is shared](https://docs.x.ai/grok-bot/computer-and-apps): files, browser sessions, CLI credentials, and connected account access are not isolated by Bot roles.
Vault is account-wide; secure API-key cards and shared-Bot Secrets have their own supported behavior.
Verify the intended account before an external write.
Ask the user to connect missing GitHub or Cursor access securely; never switch accounts to make setup pass.

Leave new routines paused.
Inbox status needs two successful manual Status checks and user enablement.
Preserve existing routine identities and enabled/paused choices during update.

Allocate a unique readiness task through Sum Persist and save the nonce, source SHA, intended native sender/recipient, and expected return in its brief.
Send a real native message asking Sum for the task-correlated role/revision acknowledgement and a substantive reversible result, even if empty or blocked.
Save the actual return and compare task, nonce, identity, and revision.
A successful send is not receipt; missing return means configuration installed but operation unverified.
A memory acknowledgement confirms a handoff, not verbatim retention of the bundle.
Complete the question/answer and delivery scenarios in `grok-bots/sum/README.md` before claiming that workflow verified.
Tell the user to talk to Sum; do not delete a starter or existing Bot as installer cleanup.
The user merges.
The Bot never merges.
