# Sum Grok Bot pack

This directory is the source-controlled recipe for a Sum coordinator Bot.
It is not a live share URL.

Tell any Bot in Grok Bot to follow [`GROK_SUM.md`](../../GROK_SUM.md) at the repository root.
That file is the installer.
The Bot clones this public repository onto the shared Grok Bot computer and loads the files below.
You do not paste them by hand.
You do not need a SUM installation.

## Pack files

| File | Role |
| --- | --- |
| `instructions.md` | Canonical role for personal chat/memory or managed shared description |
| `memories.md` | Memories included in the template |
| `worker-procedure.md` | Memory named Worker procedure |
| `skills/*/SKILL.md` | Skills named Sum Dispatch, Sum Persist, Sum Verify, Sum Status, Sum Recap, Sum Deliver, Sum Sweep, and Sum Factory |
| `routines.md` | Weekday Inbox and ops brief, enabled after two successful manual Status checks |
| `decisions.md` | Decision ledger schema; live lines stay in `/workspace/sum/decisions.md` |
| `action-log.md` | Action log schema; live lines stay in `/workspace/sum/action-log.md` |
| `permissions.md` | Auto, draft, and gated tiers |
| `evals/` | Coordinator invariant seed; not a workflow |
| `avatar.jpg` | GrokBot profile image |

Factory settings are not workflows. They live at [`skills/sum-factory/factories/`](../../skills/sum-factory/factories/).
`copy.md` is the copy factory. `software.md` points at the existing software lane.

Keep private native identities and settings in installation.md, never in shareable source files.
This recipe is native to Grok Bot.

## Prove it once

After the installer finishes, ask Sum to handle one reversible task in a repository you control.
Confirm it dispatches a worker Bot and does not do the work in the coordinator chat.
Save a question as a file under `/workspace/sum/` with a stable key.
Answer that question by id.
Confirm the worker writes a report file.
Confirm the coordinator runs a distinct verification and records it.
Open a PR if the task ships code.
Merge it yourself.
The Bot never merges.

## Share as a template

After one good run, Sum can be shared as a Grok Bot template.
Inspect the draft.
Strip API keys, internal URLs, and anything you would not put in a public document.
Confirm the draft does not attach another author's share URL.
This recipe is Sum.
Keep this directory as the source of truth when the contract changes.

## Suggested routine

After two successful manual Status checks, the user may enable the weekday Inbox status in `routines.md`.
This repository does not enable it.
Stay quiet when the inbox is empty.
A digest is one line in chat plus the saved link.

## Installation and acceptance records

`GROK_SUM.md` defines the single install/update component inventory, ownership checks, and personal/shared configuration branches.
Private `/workspace/sum/installation.md` records actual identities and adopted/deferred/failed component revisions without secrets.
The canonical action-log schema defines operation/attempt identity, retry/reconciliation, and daily caps.
Use the Sum-specific skill names and recorded identities, never an unrelated account-wide skill with a matching generic name.

For isolated native acceptance, record each of these with source/candidate revision, actual native sender/recipient, task/operation ID, saved report, verification, and receipts:

- Fresh personal install; shared install when in scope; repeated installation preserving owned identities and unrelated skills.
- Full or partial update including all eight skills, memories, persistent worker rules, Square, and owned routine prompts; preserve active source and enabled/paused state.
- Specialist reuse with original restrictions intact, then persistent return to idle.
- Worker question save/notify, actual user answer, correlated resume, acknowledgement, and applied evidence or blocker.
- Explicit empty and failed tasks returning substantive results.
- Confirmed failure retry, lost acknowledgement read-back, separate tasks on one worker, and logical daily cap.
- New task allocation with a historical directory gap and durable referenced evidence.
- Cancellation of native worker/Cloud Agent execution with confirmed versus unknown state and late replies unable to restart delivery.
- Code with distinct coordinator checks and independent native review of the exact frozen candidate, then a matching PR head.
- Research, rendered documents, and operational results with their appropriate independent evidence and useful final responses.
- Scope/permission conflicts stopping before expansion; unsupported capabilities reported precisely.
- Square failed-push recovery and isolated restore as documented in its README.

These are manual acceptance requirements, not claims of execution.
The offline invariant seed checks selected saved traces/text anchors only.
No static check proves authenticated native handoffs, deployment, or backup/restore.
