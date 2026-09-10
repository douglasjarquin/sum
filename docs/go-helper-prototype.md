# Go helper prototype: not adopted

The unused Go helper experiment is retired; production `bin/sumctl` continues to run `lib/sumctl.py`.
No command, state operation, callback, or running process moves to a new implementation.

The [decision report and reproduction instructions](../benchmarks/issue-38/report.md) preserve the corrected measurements and the exact historical experiment revision.
The [raw benchmark](../benchmarks/issue-38/raw.json) records the frozen Python reference, actual compiled invocations, timings, allocation results, and unmet gates.

Native version/help handling was faster, but every stateful operation still invoked Python.
The required frequency-weighted opportunity was not established, behavior parity was not evaluated, and whole-operation memory was not comparable.
Expected exit codes did not prove zero behavior regressions.
The unchanged issue #37 thresholds therefore do not justify adoption.

Issue #39 is deferred/not planned because its prerequisite demonstrated helper win is absent.
This is a measured no-go, not an incomplete runtime replacement presented as complete.

The independently delivered [Go Herdr Mesh](go-mesh.md) remains available and retains Cobra and the official MCP Go SDK.
New releases build that artifact, not the unused helper.
Existing immutable releases and running processes are not removed or retargeted by this source change.
