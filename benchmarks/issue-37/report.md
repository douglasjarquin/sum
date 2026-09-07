# Sum helper latency and call-frequency decision

Measured source: `a4b61bbe748d00b36e7e0e9f4e80a5ebdd4373cc` on macOS-26.6.2-arm64-arm-64bit-Mach-O.
Runner Python 3.13.5; the real lab used runtime `abbe058b7b856e5e40d0acc8df061a4e4a114d37`, Node v22.19.0, and herdr 0.8.2.
Decision: **PROCEED** to a bounded #38 prototype; do not commit to a rewrite yet.
The gate is crossed; a bounded replacement prototype may be justified.

## Decision gate

The gate was fixed before any Go prototype: at least 50 ms and 35% on a measured interactive hot path, at least 500 ms of serial frequency-weighted opportunity in a busy session, no behavior regression, and at most 10% peak-memory regression.
No Go code was built in this issue.

## Results

| Scenario | Samples | p50 wall ms | p95 wall ms | MAD ms | Python-owned p50 ms | External p50 ms | Peak RSS p50 KiB | Subprocesses p50 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `startup.version.cold` | 15 | 136.568 | 137.926 | 0.508 | 136.568 | 0.0 | 71888 | 0 |
| `startup.help.warm-fs` | 15 | 138.56 | 141.427 | 0.766 | 138.56 | 0.0 | 71888 | 0 |
| `read.settings` | 15 | 146.147 | 148.7 | 1.299 | 138.295 | 7.97 | 71872 | 1 |
| `read.status.empty` | 15 | 145.848 | 149.617 | 1.063 | 137.923 | 7.846 | 71872 | 1 |
| `read.status.12-workers` | 15 | 148.416 | 150.831 | 1.041 | 138.815 | 7.801 | 71920 | 1 |
| `read.status.archived-25` | 15 | 149.484 | 151.801 | 1.319 | 139.982 | 7.947 | 71872 | 1 |
| `read.status.archived-100` | 15 | 156.755 | 164.137 | 1.493 | 143.361 | 8.196 | 72144 | 1 |
| `read.inbox` | 15 | 149.418 | 152.544 | 1.451 | 141.499 | 7.838 | 71888 | 1 |
| `read.show` | 15 | 155.682 | 164.689 | 2.003 | 140.246 | 14.805 | 71904 | 2 |
| `read.context` | 15 | 152.294 | 155.273 | 1.55 | 137.648 | 14.576 | 72000 | 2 |
| `write.ask` | 15 | 225.587 | 228.313 | 1.079 | 140.668 | 82.502 | 71936 | 4 |
| `write.report` | 15 | 223.016 | 226.865 | 1.438 | 138.98 | 81.1 | 71952 | 4 |
| `role.init` | 15 | 211.042 | 216.647 | 2.35 | 137.453 | 73.12 | 71872 | 3 |
| `brief.regenerate` | 15 | 145.651 | 148.75 | 0.972 | 137.562 | 7.681 | 72032 | 1 |
| `dispatch.prepare` | 5 | 325.473 | 327.442 | 1.54 | 145.281 | 177.278 | 72128 | 14 |
| `herdr.fake.agent-list` | 15 | 180.68 | 186.689 | 1.351 | 138.386 | 42.165 | 71920 | 2 |
| `herdr.real.workspace-list` | 15 | 153.916 | 157.671 | 1.739 | 139.005 | 14.311 | 71936 | 2 |
| `mcp.real.agent-list` | 15 | 153.032 | 157.141 | 2.212 | 139.234 | 14.317 | 71888 | 2 |
| `contention.concurrent-writes` | 5 | 373.182 | 377.355 | 2.671 | 0 | 241.219 | 72800 | 13 |
| `failure.show-missing` | 15 | 146.063 | 150.056 | 1.275 | 137.897 | 8.021 | 71904 | 1 |
| `timeout.herdr-prompt` | 1 | 5201.027 | 5201.027 | 0.0 | 142.994 | 5054.872 | 71440 | 4 |
| `question.worker-to-inbox` | 15 | 368.914 | 374.827 | 1.388 | 276.712 | 90.197 | 71968 | 5 |

p99 is omitted unless a scenario has at least 100 samples.
All samples are retained; MAD, min, and max remain in `raw.json` so outliers are visible rather than deleted.

## Frequency-weighted upper bounds

| Command | Basis | Serial calls in busy session | Avoidable upper bound each ms | Weighted upper bound ms |
| --- | --- | ---: | ---: | ---: |
| `ask` | observed-lab | 1 | 140.668 | 140.668 |
| `report` | observed-lab | 1 | 138.98 | 138.98 |
| `inbox` | simulated-fixture | 4 | 141.499 | 565.996 |
| `status` | simulated-fixture | 4 | 138.815 | 555.26 |
| `context` | simulated-fixture | 4 | 137.648 | 550.592 |
| `show` | simulated-fixture | 2 | 140.246 | 280.492 |
| `prepare` | observed-lab | 1 | 145.281 | 145.281 |
| `herdr/MCP` | simulated-fixture | 4 | 139.234 | 556.936 |

Parallel waits are excluded rather than added as serial user delay.
Observed-lab counts are actual calls made by the named scenarios; coordinator read counts and busy-session MCP counts are explicitly simulated estimates because Sum did not previously retain read telemetry.
Model, network, and provider latency are excluded.

## Phase attribution and simpler alternatives

`raw.json` retains each sample's Python-owned upper bound, subprocess time and count, state JSON read/write time, lock-wait time, CPU, and peak RSS.
The Python-owned figure subtracts measured external subprocess, state-I/O, and lock-wait phases but still includes shell and harness overhead, so it is an upper bound rather than a promised rewrite saving.
No simpler optimization was mixed into this language baseline.

- **avoid repeated full task scans**: compare empty, 12-worker, and archived-task status scenarios.
- **prefer bounded context reads over full show**: compare read.context and read.show.
- **batch only reads the native APIs already support**: one agent-list snapshot already replaces per-worker observation.
- **remove redundant subprocess hops before porting**: subprocess counts and external phase time in raw samples.

## Limitations

- Read-call frequency was not retained before this opt-in instrumentation, so coordinator read counts are simulated and labelled.
- Python-owned time is an upper bound after subtracting measured subprocess, state-I/O, and lock-wait phases; it includes shell and harness overhead.
- No model, network provider, live production state, default Herdr session, or provider quota was exercised.
- No benchmark number is described as end-to-end product improvement.

## Inventory

| Production entrypoint | Call sites | Frequency | Role |
| --- | --- | --- | --- |
| `bin/sumctl -> lib/sumctl.py` | operator shell, generated worker briefs, tests, demo | interactive | stable CLI for status/inbox/context/show and ask/report |
| `bin/herdr-scoped -> lib/sumctl.py herdr` | HERDR_BIN used by the Mesh server | interactive | one scoped Python process per MCP tool call |
| `bin/herdr-mesh` | MCP client configuration | interactive | long-lived Node transport; calls bin/herdr-scoped |
| `lib/sumctl.py hook event` | generated Herdr plugin event handler | interactive | bounded native event reconciliation |
| `lib/sumctl.py prepare/dispatch/brief` | coordinator task lifecycle | lifecycle | real Git, Herdr worktree, brief, and graph preparation |
| `scripts/demo.py and scripts/live_smoke.py` | mise demo/test-live and verification | lifecycle | isolated end-to-end fixtures |
| `scripts/setup.py and release/update commands` | mise setup/publish and operator upgrades | rare | installation and immutable release paths |
| `mise-tasks/test, verify, demo, doctor, test-live` | development and release checks | rare | repository-owned command surface |

## Method

Warmup count was 1 and measured scenario order used deterministic seed 37.
Median no-op process harness overhead was 14.633 ms and is reported separately, not subtracted from raw samples.
Cold means a new Python process; warm-fs means the same fixture was read after an unrecorded warmup without attempting privileged OS cache eviction.
The trace separates Python-owned time from state JSON/fsync/locking and Git/Herdr/Node subprocess time; network and model work are absent.

## Reproduce

```sh
mise run benchmark
```

Raw samples and environment/tool metadata are in `raw.json` beside this report.
