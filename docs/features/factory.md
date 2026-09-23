# Software factory

A per-project sequential lane that picks a ready GitHub issue, claims it, dispatches one worker, runs the existing delivery pipeline, and either merges at high confidence or leaves a human gate.
There is still no sum daemon: `sumctl factory tick` is one observation.

Implementation: `go/internal/factory`, `go/internal/cli/factory.go`, `go/internal/cli/factory_test.go`, `go/internal/helpview/catalog.json`, `go/internal/skills/skills.go`, `go/internal/contextview/contextview.go`, `go/internal/guard/guard.go`, `skills/sum-factory`, `skills/sum-factory-claim`, `skills/sum-factory-work`, `skills/sum-factory-merge`.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `factory.tick-idle` | An enabled factory with no ready issues records `action: idle` and `next_tick_at` five minutes later without starting an agent | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.claim-lane` | Claim occupies the only lane; a second tick is `occupied` and a second claim is refused at the lane limit | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.roadmap-order` | Roadmap ready signal follows the parent issue table, skips closed and `--skip` issues, and picks the next open number | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.merge-authorization` | Merge-check is `human-gate` outside the standing authorized repositories and `high` only when closure, review, CI, and evidence comparisons all pass | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.human-gate-continue` | A gated issue stays occupying the lane until `--continue` or a merged/dropped release | automated: `go/internal/factory/factory_test.go` | offline suite |
| `factory.coordinator-only-writes` | `factory enable` and `factory claim` refuse a non-coordinator pane | automated: `go/internal/cli/factory_test.go` | offline suite |
| `factory.live-nicebaas` | Enabling the NiceBaaS factory, ticking, claiming, and either merging or gating one real issue | manual: operator run after this branch is adopted; first coding issue on the live #124 table is #137 unless the user names another | operator report |
