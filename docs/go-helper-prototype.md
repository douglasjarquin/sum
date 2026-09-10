# Go helper prototype

The benchmark freezes the Python reference at `b03b8020621e0d417906402a5c7ecc5d63192541`.

The production entrypoint remains `bin/sumctl` and continues to run `lib/sumctl.py`.

The compiled prototype is `go/cmd/sumctl-go` and uses Cobra `v1.9.1` for command construction, help, version handling, and the outer exit boundary.

The prototype's retained native behavior is:

- `--version` prints the reference-compatible `sum 0.1.0` value without opening state or invoking another process.
- Root `--help` is rendered by the fresh Cobra tree when no reference helper is configured, and forwards to the configured reference when one is available.
- Command construction, root help, unknown commands, context cancellation, repeated options, leading-dash values, `--`, and compatibility exit codes are covered by `go/internal/cli/root_test.go`.

The following existing command paths are present in the Cobra tree and remain explicit Python compatibility commands in this prototype:

`doctor`, `init`, `status`, `inbox`, `prepare`, `dispatch`, `start`, `help`, `context`, `notes`, `env`, `show`, `notice`, `archive`, `ask`, `answer`, `report`, `resolve`, `review`, `verify`, `pr`, `cleanup`, `pump`, `hook`, `metadata`, `attention`, `bind`, `backup`, `settings`, `preset`, `project`, `herdr`, `graph`, `dev`, `brief`, `refresh`, `release`, and `update`.

No stateful domain operation is claimed as ported.

The compatibility boundary forwards argument tokens to the helper selected by `SUM_PYTHON_HELPER`, or the working directory's `bin/sumctl`, with `exec.CommandContext`.
The Go tests exercise forwarding with shell fixtures; they do not establish differential parity against the Python implementation.

The benchmark command is:

```sh
(cd go && go build -trimpath -buildvcs=false -o /tmp/sumctl-go ./cmd/sumctl-go)
python3 scripts/benchmark_go.py --binary /tmp/sumctl-go --output /tmp/sum-go-benchmark.json
```

The benchmark includes compiled version and help paths plus representative fixture reads and an expected failure through the compatibility boundary.
Before timing, it extracts the frozen commit from local Git into a temporary directory and uses that source for both fixture setup and compatibility calls.
The reference commit must exist locally; the benchmark does not download it or use an installed runtime in its place.
The temporary source and fixture are removed when the run exits.

It records binary size, latency, peak memory, exit codes, declared compatibility-subprocess counts, allocation results, and the reference revision and helper path.
The subprocess counts describe the wrapper boundary, not an observed total of nested Python, Git, or Herdr processes.

The JSON evaluates the #37 latency gate and records the explicit `defer` decision with its comparison inputs.
Frequency-weighted benefit remains unestablished, behavior parity is not evaluated, and compatibility-process memory is not comparable.
Expected exit codes are smoke checks, not evidence of zero behavior regressions.

The #37 gate remains 50 ms and 35% on a measured interactive hot path, 500 ms of serial frequency-weighted opportunity, zero behavior regressions, and at most 10% peak-memory regression.

The prototype decision is defer production adoption.

The native `--version` path demonstrates a process-local reduction, but the measured stateful workloads still use the compatibility subprocess and therefore do not establish the required end-to-end gate.

Issue #39 owns any production cutover after an independently verified native state boundary demonstrates the gate.
