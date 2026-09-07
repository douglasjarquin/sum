# Coordination and delegation

Journeys the coordinator, workers, and developers take through `sumctl` and the Herdr bridge.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `roles.claim` | The first pane claims coordinator atomically; later panes become developers or workers | automated: `tests/test_core.py` | offline suite |
| `dispatch.worktree` | Dispatch creates one task checkout and branch and never writes to the primary clone | automated: `tests/test_core.py`, `scripts/demo.py` | offline suite, demo |
| `returns.durable` | A worker question survives a busy or closed coordinator and stays listed until applied | automated: `tests/test_returns.py`, `scripts/demo.py` | offline suite, demo |
| `fleet.mixed-version` | Twelve scripted workers stay usable across a staged update, rolling refresh, and rollback | automated: `tests/test_fleet.py` | offline suite |
| `cleanup.guarded` | Post-merge cleanup removes only a proven idle workspace and archives the record | automated: `tests/test_cleanup.py` | offline suite |
| `projects.enroll` | A named repository is enrolled once under `projects/<owner>/<repo>` and dispatched by name | automated: `tests/test_projects.py` | offline suite |
| `live.herdr-smoke` | A named lab Herdr session runs a real dispatch, question, and refresh | manual: `mise run test-live` on a host with Herdr 0.8.2 | operator report |
| `live.harness-canary` | An authenticated harness worker reads its brief and calls `sumctl ask` and `sumctl report` | manual: `docs/ACCEPTANCE.md` section 2 | operator report |
| `performance.hot-path-baseline` | Opt-in local metrics and `mise run benchmark` measure production helper entrypoints in fake and isolated real-Herdr labs before any rewrite decision | automated: `tests/test_benchmark.py` | `lib/sum_measure.py`, `scripts/benchmark.py`, `scripts/benchmark_fixture.py`, `scripts/benchmark_real.py`, `scripts/benchmark_report.py`, `mise-tasks/benchmark`, and retained `benchmarks/issue-37/raw.json`, `benchmarks/issue-37/report.md`, `benchmarks/issue-37/real-herdr.log` |
| `performance.go-cobra-prototype` | A cgo-free compiled Cobra entrypoint keeps the frozen Python helper as an explicit compatibility boundary while measuring native version/help and representative fixture dispatch | automated: `go/internal/cli/root_test.go` | `docs/go-helper-prototype.md`, `go/go.mod`, `go/go.sum`, `go/internal/cli/root.go`, `go/cmd/sumctl-go/main.go`, `scripts/benchmark_go.py`, and benchmark JSON |
