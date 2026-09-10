# Go helper decision: discard the unused prototype

Do not adopt the Go helper or plan a production cutover under issue #39.
The experiment demonstrates faster native version/help handling, but no stateful operation moved to Go and the required adoption gate is not met.
Production remains on the existing Python helper, and the separately delivered Go Herdr Mesh remains available.

## Measured candidate

The corrected driver and recoverable experiment are at commit `fa9e69f6a9c95c73904e6a7316bfbec7293d0b1a`.
Its Go source is unchanged from the compiled source at `d9d0bd210aa06d4dd87215460a642e1a4a2979aa`.
The Python reference is `b03b8020621e0d417906402a5c7ecc5d63192541`, extracted from local Git and used for fixture setup and compatibility execution.
The build used Go 1.25.0 with `CGO_ENABLED=0`, `GOENV=off`, `GOTOOLCHAIN=local`, `-trimpath`, and `-buildvcs=false`.
The runtime used Python 3.13.5 on macOS arm64.
No model, native Herdr pane, live installation state, or installed runtime was used.

The executable was 6,109,458 bytes with SHA-256 `1faf6062b68d2c9d3d06557f8f4e519e90e6169fe13d4bbd0195a2ffd6de66c4`.
The retained [raw result](raw.json) is the exact 15-sample output of the corrected benchmark, with SHA-256 `6aeb8054523f69d7e58a30236e41c7759c1c43a1e60663546ec75831aebef9a5`.
Recorded temporary paths identify the measured run; those fixtures were removed after it completed.

| Compiled entrypoint | Median | Implementation |
| --- | ---: | --- |
| `--version` | 5.919 ms | Native Go |
| Native root `--help` | 5.801 ms | Native Cobra rendering, not Python help parity |
| Fixture `status` | 133.056 ms | Python compatibility subprocess |
| Missing-task `show` | 132.928 ms | Python compatibility subprocess, expected exit 1 |

Fresh Cobra tree construction measured 6,299 ns/op, 33,080 B/op, and 133 allocations/op.
The reported subprocess counts are declared compatibility-boundary counts, not measured totals of nested Python, Git, or Herdr processes.
The memory samples omit the compatibility child's peak usage and cannot establish a whole-operation memory regression bound.

## Unchanged adoption gate

The [issue #37 baseline](../issue-37/report.md) and its thresholds were not rerun or altered.
The required gate remains at least 50 ms and 35% improvement on a measured interactive path, at least 500 ms of serial frequency-weighted opportunity, zero behavior regressions, and at most 10% peak-memory regression.

- Native version/help cross the local latency threshold against the recorded baseline.
- No stateful command is native, so the required frequency-weighted opportunity is not established.
- Expected exit codes are smoke checks, not differential output, state, role, durability, or cancellation parity against Python.
- Whole-operation memory is not comparable.

The gate therefore fails; this is a discard decision, not a claimed successful port.
No command is moved to Go, so no new implementation is accepted under a weaker parity standard.
All domain operations remain in their existing implementation.

## Evidence correction

The earlier driver labeled the Python reference with the frozen commit but invoked the working checkout's helper.
A trace of the actual compiled benchmark observed configured source hash `0391d1dc8cbe8f7b2c06d3ae03e5ba289479c1ac547f761cca378fb3120797fa`, differing from the claimed reference hash `bba3eb2d171da1dfd5193e1ade4f0fb4482c0383856b5f969d21065462ac90da`.
After the repair at `fa9e69f`, the same trace observed the latter hash for the configured helper, and the behavior verdict changed from an unsupported pass to `not-evaluated`.
Earlier labeled results are not used as frozen-reference evidence here.

## Reproducing the historical experiment

Use an isolated checkout of `fa9e69f6a9c95c73904e6a7316bfbec7293d0b1a` with the pinned Go and Python tools already available.
The repository must contain the frozen Python commit locally.
Run these commands in that historical checkout, with an owned temporary output directory replacing `/tmp/sum38-reproduction`:

```sh
mkdir /tmp/sum38-reproduction
(cd go && env -u GOROOT -u GOBIN -u GOTOOLDIR -u GOOS -u GOARCH CGO_ENABLED=0 GOENV=off GOTOOLCHAIN=local GOPROXY=off go build -trimpath -buildvcs=false -o /tmp/sum38-reproduction/sumctl-go ./cmd/sumctl-go)
python3 scripts/benchmark_go.py --binary /tmp/sum38-reproduction/sumctl-go --output /tmp/sum38-reproduction/raw.json --samples 15
```

The driver extracts and cleans its own temporary reference and fake-Herdr fixture.
Retain the output if needed for comparison and remove only the owned reproduction directory afterward.
Timing varies by host and run; this is reproducibility of the experiment and decision inputs, not bit-identical performance or binary output.

## Delivery boundary

The retirement removes the unused helper implementation and its current-tree benchmark driver from new source releases.
New staging retains the independently used Go Mesh artifact and its integrity checks.
Historical releases are checked against their own artifact inventory; this does not delete old installed binaries or change a running process.
Issue #39 is deferred/not planned because its prerequisite demonstrated helper win is absent.
This decision does not block Remainder, Pinchos, or other roadmap work, and it does not authorize installation updates or fleet restarts.
