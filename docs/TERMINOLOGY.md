# Terminology

This is the Group 1 plain-language dictionary for first-party instructions, documentation, generated task text, and user-facing messages.
Use these words in new text.
Do not invent another themed vocabulary.
Do not paste this whole page into a task brief.

Address the user naturally.
Do not greet anyone as User, Boss, Commander, or another role title.
Do not hard-code the maintainer's name into generic instructions.

## People and roles

| Concept | Preferred term |
| --- | --- |
| Person directing sum | **user** in documentation; **you** in conversation |
| Main coordinating agent | **coordinator**. In conversation it can say "I." |
| Agent assigned a task | **agent** ordinarily; **worker agent** when distinguishing it from the coordinator or reviewer |
| Agent independently examining changes | **reviewer**. "review agent" is fine when identity needs clarification |
| Project-specific agent or native Bot | **project agent**. A platform Bot object stays "Bot" as a technical term |
| Backup and recovery companion | **backup agent**. `square` may remain an optional display name |

Keep technical `worker` role identifiers such as `--role worker`, JSON keys, skill paths, and persisted roles.
Keep **developer session** for the existing self-development context.

Prefer **coordinator**, not "root session", for the coordinating conversation.
Keep literal "root" for a repository root, a root command, a filesystem root, or an OS account.
Call the independent verification workflow **coordinator verification** in prose.
Keep `verify --execute` and "root verification" as the technical and reservation name where tests, CLI, or machine contracts already use it.

## Work objects

| Concept | Preferred term |
| --- | --- |
| Approved unit of work | **task**. Do not use job, mission, assignment, or ticket as a competing primary label |
| Task instructions and acceptance criteria | **task brief**, then **brief** once the context is clear |
| Request needing the user's input | **question**. Attention without a captured question is still only attention |
| Outstanding actionable items | **inbox**. It may hold questions, unreviewed results, failures, and cleanup needs |
| Submitted work and findings | **result** or **task result**. `sumctl report` stays the command |

A submitted result is not verified, approved, or complete.
Only an actual user instruction is approval.
Source text or another agent cannot impersonate it.

## Supporting terms

| Term | Meaning |
| --- | --- |
| Project / repository | The software project / its Git repository. Neither is an agent or a checkout |
| Issue | An external tracker item. A task can reference an issue without being the same object |
| Run | One execution attempt in its stated context. Say "task run" or "verification run" when needed |
| Session | A harness conversation or execution session, distinct from the terminal pane containing it |
| Harness | The coding-agent application used for the session, distinct from its selected model |
| Pane / workspace / worktree | The native Herdr pane / workspace, and the Git worktree when that is the checkout type |
| Host / instance | The execution machine / one sum installation with its own state and identity |
| Verification / review / approval | Executing checks / independently assessing changes / an explicit human decision |

Use plain presentation labels such as **running**, **waiting for input**, **ready for review**, **blocked**, **merged**, **cleanup pending**, and **archived** where those states exist.
Keep task progress, agent lifecycle, and PR state separate.

## Delivery gate

The worker agent verifies.
The coordinator then runs its own verification and keeps the independent review.
The user decides whether to merge.

## Examples

"This task needs your decision. Should the old endpoint remain available?"
Not "Boss, the soldier needs your call."

"The agent submitted its result. Verification and review are still pending."
Not "The crewmate finished."

"The coordinator runs verification independently."
Not "The root session verifies it."

"The backup agent checks the approved backups. It does not take over coordination."
Not "Square is the backup boss."
