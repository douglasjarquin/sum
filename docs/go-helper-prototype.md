# Go helper prototype

This prototype freezes the Python reference at `b03b8020621e0d417906402a5c7ecc5d63192541`.

The production entrypoint remains `bin/sumctl` and continues to run `lib/sumctl.py`.

The compiled prototype is `go/cmd/sumctl-go` and uses Cobra `v1.9.1` for command construction, help, version handling, and the outer exit boundary.

The prototype's retained native behavior is:

- `--version` prints the reference-compatible `sum 0.1.0` value without opening state or invoking another process.
- Root `--help` is rendered by the fresh Cobra tree when no reference helper is configured, and forwards to the frozen reference when one is available so the command inventory and help text stay aligned.
- Command construction, root help, unknown commands, context cancellation, repeated options, leading-dash values, `--`, and compatibility exit codes are covered by `go/internal/cli/root_test.go`.

The following existing command paths are present in the Cobra tree and remain explicit Python compatibility commands in this prototype:

`doctor`, `init`, `status`, `inbox`, `prepare`, `dispatch`, `start`, `help`, `context`, `notes`, `env`, `show`, `notice`, `archive`, `ask`, `answer`, `report`, `resolve`, `review`, `verify`, `pr`, `cleanup`, `pump`, `hook`, `metadata`, `attention`, `bind`, `backup`, `settings`, `preset`, `project`, `herdr`, `graph`, `dev`, `brief`, `refresh`, `release`, and `update`.

No stateful domain operation is claimed as ported.

The compatibility boundary forwards the original argument tokens to `bin/sumctl` with `exec.CommandContext`, preserving stdout, stderr, exit status, cancellation, environment, and subprocess ordering.

The benchmark command is:

```sh
(cd go && go build -trimpath -buildvcs=false -o /tmp/sumctl-go ./cmd/sumctl-go)
python3 scripts/benchmark_go.py --binary /tmp/sumctl-go --output /tmp/sum-go-benchmark.json
```

The benchmark includes compiled version and help paths plus representative fixture reads and an expected failure through the compatibility boundary.

It records binary size, latency, exit codes, and the exact reference revision.

The #37 gate remains 50 ms and 35% on a measured interactive hot path, 500 ms of serial frequency-weighted opportunity, zero behavior regressions, and at most 10% peak-memory regression.

The prototype decision is defer production adoption.

The native `--version` path demonstrates a process-local reduction, but the measured stateful workloads still use the compatibility subprocess and therefore do not establish the required end-to-end gate.

Issue #39 owns any production cutover after an independently verified native state boundary demonstrates the gate.
