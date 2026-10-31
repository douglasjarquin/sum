# HC benchmark evidence

`mise run benchmark` measures the current terminal runtime with schema-two records.
The default output is `benchmarks/current/`, with exact commands, dependency/candidate identity, availability and limitations in the retained artifacts.
A benchmark is performance evidence for its measured operation, never end-to-end acceptance, task completion or production readiness.

## Isolated runs

```sh
mise run benchmark -- --skip-real-hc --skip-timeout
mise run benchmark -- --hc-binary /absolute/path/to/hc
```

`--skip-real-hc` explicitly omits the native HC slice.
`--hc-binary PATH` selects the actual pinned HC binary for the isolated native slice.
`--skip-timeout` omits the intentionally bounded response-timeout measurement.
The retired `--skip-real-herdr` and `--skip-mcp` flags are not supported.
A native run uses a unique named HC session and scratch state/config/runtime paths, never a live installation or the user's default core.
It does not start a model or change accounts, permissions or global keymaps.

## Rows and comparability

| Row | What it measures |
| --- | --- |
| `hc.fixture.terminal-list` | Native-shaped fixture terminal listing |
| `hc.real.terminal-list` | The actual selected HC binary's isolated terminal listing |
| `timeout.hc-response` | A bounded incomplete or absent HC response |

The old `herdr.fake.agent-list`, `herdr.real.workspace` and `mcp.real.agent` rows are retired.
The old `timeout.herdr-prompt` row is replaced by `timeout.hc-response`.
Schema-two evidence reports external wall time and child CPU/RSS where available.
Internal telemetry is unavailable; do not describe missing counters as zero cost or imply complete internal timing coverage.
Historical Python rewrite thresholds and legacy runtime artifacts are not comparable to the HC measurements.
Record omitted slices, unsupported metrics and base/candidate command failures explicitly.
A favorable isolated timing does not establish application receipt, exactly-once delivery, authenticated harness qualification or a working production migration.
