# Software factory

A per-project sequential lane that picks a ready GitHub issue, claims it, dispatches one worker, runs the existing delivery pipeline, and either merges at high confidence or leaves a human gate.
There is still no sum daemon: `sumctl factory tick` is one observation.

Implementation: `go/internal/factory`, `go/internal/cli/factory.go`, `go/internal/cli/factory_test.go`, `go/internal/evidenceview/evidenceview.go`, `go/internal/helpview/catalog.json`, `go/internal/guard/guard.go`, `go/internal/procedure/procedure.go`, `skills/sum-dispatch/references/factory.md`, `skills/sum-dispatch/references/factory-claim.md`, `skills/sum-dispatch/references/factory-merge.md`, `skills/sum-worker/references/factory.md`.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `factory.tick-idle` | An enabled factory with no ready issues records `action: idle` and `next_tick_at` five minutes later without starting an agent | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.claim-lane` | Claim occupies the only lane; a second tick is `occupied` and a second claim is refused at the lane limit | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.roadmap-order` | Roadmap ready signal follows the parent issue table, skips closed and `--skip` issues, and picks the next open number | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.issues-oldest` | Issues ready signal picks the lowest-number open issue, skipping claimed and `--skip` issues | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.project-status-repo` | A GitHub Project Ready column that spans repositories yields only items whose repository matches the enrolled owner/repo | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.project-status-scan` | A GitHub Project Ready item at board position 103+ on a 155-item board is still listed; item-list uses a limit above the 100-item GraphQL page | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.merge-authorization` | Merge-check is `human-gate` outside the standing authorized repositories and `high` only when closure, SHA-bound review approve, all pipeline gates, and a real passing comparison.json for this candidate all pass | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.merge-open-pr-identity` | Merge-check closure passes for an open PR whose reconcile observation records identity (`number`, `url`, `head_sha`, `head_branch`, `base_branch`) with empty findings, and stays human-gate with no reconcile observation; `complete`/`merged_for_task` still mean merged | automated: `go/internal/factory/factory_test.go`, `go/internal/evidenceview/evidenceview_test.go` | offline suite |
| `factory.merge-evidence-file` | A listed comparison.json that is missing, outside the checkout, unbound to this SHA, or not `red-green`/`before-after` (`after-only`, `before-also-passes`, and other verdicts) is human-gate | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.merge-review-sha` | An approve with an empty candidate, or a newer unbound review, does not authorize the current HEAD | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.merge-pipeline-gates` | Fail or blocked Test/Review/Push is human-gate; lint and CI may be not_declared | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.merge-lane` | `factory merge` refuses a high-confidence task that occupies no factory lane, and never calls `gh pr merge` on human-gate | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.merge-promotes` | High-confidence `factory merge` marks the PR ready then squash-merges in the same action; human-gate and lane refusal call neither ready nor merge | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.human-gate-continue` | A gated issue stays occupying the lane until `--continue` or a merged/dropped release | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.coordinator-only-writes` | `factory enable` and `factory claim` refuse a non-coordinator pane | automated: `go/internal/cli/factory_test.go` | offline suite |
| `factory.live-nicebaas` | Enabling the NiceBaaS factory, ticking, claiming, and either merging or gating one real issue | manual: operator run after this branch is adopted; `--ready issues` picks the oldest open issue | operator report |
