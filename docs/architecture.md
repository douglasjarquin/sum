# Architecture

Sum owns working instructions; HC owns live terminals and native communication, Git owns source isolation, and the target project owns verification.
There is no Sum executable, registry, task database, receipt protocol, lifecycle engine, release staging, collector, or background service.
The `sum-verify` binary under `tools/verify/` is a separate, optional developer tool for the [verification contract](../VERIFY.md); ordinary instruction use never builds, installs, or runs it.

The approved task selects the role: an explicit bounded helper stays bounded, maintenance of Sum itself uses [Develop](../skills/sum-develop/SKILL.md), and operational coordination uses the native HC lead workflow.
Role selection precedes HC prerequisites and survives verification, review, finish, and resume.
Maintainers can work directly and use supported helpers in their current environment; actual HC operations retain native identity and lifecycle requirements.
In the operational workflow, the lead session stays free for messages.
It launches a native HC worker for implementation, investigation, builds, and CI, and steers that worker when a report arrives.
See [Work](../skills/sum-work/SKILL.md), [Review](../skills/sum-review/SKILL.md), [Finish](../skills/sum-finish/SKILL.md), and [Resume](../skills/sum-resume/SKILL.md).

```mermaid
flowchart TB
  Lead[Current native lead] -->|run --bg| Check[Project check]
  Lead -->|run --bg| Helper[Interactive helper]
  Check -->|Native exit notification| Lead
  Helper -->|Saved report and hc send| Lead
  Lead -->|hc send correction| Helper
  Lead --> Note[Ordinary task note]
```

The note and per-helper reports live outside disposable worktrees.
Native addresses are current observations, not durable conversation identity.
One lead per workspace and one writer per checkout are instructions, not hard locking.
Keep source stable for checks/review and use separate worktrees for concurrent writers.
[Native prerequisites](native-hc.md), [independent adoption](update.md), and [legacy boundaries](legacy.md) define operational limits.

## Delivery artifacts

Every role takes a candidate through the [candidate lifecycle](../skills/sum-finish/SKILL.md#candidate-lifecycle); operational helpers follow the [assignment lifecycle](../skills/sum-work/SKILL.md#assignment-lifecycle).
Maintainer delivery uses the current environment's supported tools and reporting route, without mandatory HC dispatch or `hc send`.
The [PR template](../templates/pull-request.md) standardizes review summaries, prescribed-check status tables, and before/after evidence across harnesses.
Screenshots show visual changes; validated transcripts or HTTP responses show CLI/API changes.
The authorized delivery role publishes and reads back the PR artifacts against its current head.
These are delivery requirements implemented by the existing procedures, not a Sum runtime or a second task ledger.
