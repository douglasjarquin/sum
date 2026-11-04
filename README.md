# sum

Sum is a small distribution of agent instructions and skills for software work in an existing HC-backed Herdr terminal.
Give the usual coding agent a task in the same conversation.
The lead session stays free for messages.
It launches a native HC worker for implementation, investigation, builds, and CI, asks an independent reviewer, receives native reports, and steers follow-up.

## Start

1. Start the supported Herdr app independently and open one of its HC-backed terminals.
2. Clone this repository into a separate path, enter it, and launch your chosen interactive agent there.
3. For Codex use `codex --no-daemon`; preserve your usual account, model, and permissions.
4. Give the agent the task; [AGENTS.md](AGENTS.md) is the shared entry contract.

There is no Sum executable, build, setup, initialization, generated runtime installation, or service.
Read [native prerequisites and qualification limits](docs/native-hc.md) before delegation.
The currently inspected local artifacts are documented, but authenticated native acceptance remains unrun; do not read their version strings as a support guarantee.

## Four procedures

| Procedure | Use it for |
| --- | --- |
| [Work](skills/sum-work/SKILL.md) | Brief and launch a native HC worker, then steer from messages |
| [Review](skills/sum-review/SKILL.md) | Independent inspection of a specified frozen candidate and explicit report to the parent |
| [Finish](skills/sum-finish/SKILL.md) | Required checks, findings, authorized delivery, and conservative cleanup |
| [Resume](skills/sum-resume/SKILL.md) | Inspecting actual work, reports, and recipients after interruption |

The `.agents/skills/` and `.claude/skills/` links expose the same four procedures.
`CLAUDE.md`, `GEMINI.md`, and `GROK.md` point to the same entry contract; a pointer is not harness qualification.
Helpers in other repositories receive the absolute procedure path in their briefs.

Keep a [small task note](templates/task.md) and separate helper reports outside disposable worktrees.
One lead per workspace and one writer per checkout are working conventions, not enforced locks.
Use separate Git worktrees for concurrent writers and freeze candidate inputs for checks/review.

## Adoption and development

Use [independent adoption](docs/update.md) between sessions; preserve any serving legacy installation and its callbacks.
The excluded [Grok Bot packs and legacy software factory](docs/legacy.md) keep their existing contracts and a separate legacy target.

Optional developer tools under `.agents/skills/` provide portable verification and evidence capture.
They do not run at ordinary startup.
See [CONTRIBUTING.md](CONTRIBUTING.md), [VERIFY.md](VERIFY.md), and [acceptance evidence](docs/verification/issue-348.md).
Credits and retained notices are in [ATTRIBUTIONS.md](ATTRIBUTIONS.md).
