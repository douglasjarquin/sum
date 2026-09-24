# Fast coordination pass measurements (issue #206)

These are measurements of the current Go `sumctl` executable.
They are separate from the historical Python-helper benchmark in `benchmarks/issue-37/`, which measured a different program.

- Candidate: `db49852` (this change). Base: `34e5098` (merge base, before this change), built the same way from a Git archive.
- Host: Linux x86_64, Go 1.25.0. Both binaries ran against the same fixture homes with the repository's fake Herdr (`tests/fixtures/herdr.py`) and fake `gh` (`tests/fixtures/gh.py`), which are Python scripts. Each fake call costs about 40-70 ms of interpreter startup, so the healthy wall times overstate what a real Herdr costs; the call counts do not depend on it.
- Driver: `go/internal/cli/coordination_measure_test.go`. Run it with `SUM_MEASURE_OUT=<dir>` (optionally `SUM_MEASURE_BINS=base=<path>`). It is skipped in the ordinary suite.
- Fixtures: 1, 12, and 100 synthesized tasks. Every third task is archived. Active tasks cycle through an open question owed to the coordinator, an unapplied answer owed to an idle worker, a submitted report, a recorded open PR, and a merged PR with cleanup pending. Each sample starts from a fresh copy of the same home.
- Scenarios: `healthy` fakes; `slow`, where every Herdr `agent list`/`agent get` takes 6 s (longer than the 5 s observation bound) and every `gh` call takes 5 s.
- Wall time comes from 11 samples (healthy) or 3 samples (slow; 1 for base at 100 tasks, where one sample takes about ten minutes). p95 is the nearest-rank value.
- Task reads (`task.json` opens by the sumctl process) and subprocesses (helper processes sumctl started) come from one `strace -f` run per cell. Herdr and `gh` calls come from the fakes' call logs.

## Findings

- No fast path calls GitHub any more. The base's `init` and `pump` made one `gh` call per recorded open PR plus the evidence and pipeline calls of each reconcile (104 at 100 tasks) and applied cleanup. The candidate makes none; the same work is listed under `maintenance` and runs only through `sumctl sweep`, `pr reconcile`, or `cleanup`.
- A slow dependency no longer multiplies. With a hung Herdr observation and slow `gh`, base `init` took 50.7 s at 12 tasks and 596.7 s at 100 tasks. The candidate took 5.2 s and 5.6 s: one timed-out `agent list` trips the session, and the rest of that session's recipients are `not-delivered` without further calls. The remaining time is local work.
- Herdr calls per pass are one `agent list` per session plus a re-observation and a prompt for each settled recipient. At 12 tasks two idle workers are owed an answer: 1 list + 2 gets + 2 prompts = 5 calls (base 7, plus 8 `gh`). Returns owed to the calling coordinator are presented inline with no call; busy or absent recipients cost no call of their own.
- Task reads are linear. `status` and `inbox --live` read each task once (100 reads for 100 tasks; base 267 and 254). `init` and `pump` read the snapshot once plus one re-read per task they deliver to (182 at 100 tasks; base 658 and 358).
- `inbox --live` now performs the one bounded `agent list` per session that the rundown documents (base `--live` observed nothing). It still writes nothing.

## Results

### Candidate (`db49852`)

| Tasks (active) | Scenario | Command | Samples | p50 ms | p95 ms | Task reads | Subprocesses | Herdr calls | gh calls |
| ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 (1) | healthy | `init` | 11 | 126 | 138 | 3 | 2 | 1 | 0 |
| 1 (1) | healthy | `inbox --live` | 11 | 57 | 68 | 1 | 1 | 1 | 0 |
| 1 (1) | healthy | `pump` | 11 | 26 | 26 | 3 | 0 | 0 | 0 |
| 1 (1) | healthy | `status` | 11 | 7 | 8 | 1 | 0 | 0 | 0 |
| 1 (1) | slow | `init` | 3 | 122 | 124 | 3 | 2 | 1 | 0 |
| 1 (1) | slow | `inbox --live` | 3 | 5013 | 5014 | 1 | 1 | 1 | 0 |
| 1 (1) | slow | `pump` | 3 | 27 | 28 | 3 | 0 | 0 | 0 |
| 1 (1) | slow | `status` | 3 | 7 | 7 | 1 | 0 | 0 | 0 |
| 12 (8) | healthy | `init` | 11 | 423 | 478 | 24 | 7 | 6 | 0 |
| 12 (8) | healthy | `inbox --live` | 11 | 62 | 74 | 12 | 1 | 1 | 0 |
| 12 (8) | healthy | `pump` | 11 | 337 | 381 | 24 | 5 | 5 | 0 |
| 12 (8) | healthy | `status` | 11 | 9 | 10 | 12 | 0 | 0 | 0 |
| 12 (8) | slow | `init` | 3 | 5210 | 5218 | 24 | 3 | 2 | 0 |
| 12 (8) | slow | `inbox --live` | 3 | 5014 | 5016 | 12 | 1 | 1 | 0 |
| 12 (8) | slow | `pump` | 3 | 5088 | 5091 | 24 | 1 | 1 | 0 |
| 12 (8) | slow | `status` | 3 | 10 | 11 | 12 | 0 | 0 | 0 |
| 100 (67) | healthy | `init` | 11 | 2095 | 2185 | 182 | 31 | 30 | 0 |
| 100 (67) | healthy | `inbox --live` | 11 | 76 | 79 | 100 | 1 | 1 | 0 |
| 100 (67) | healthy | `pump` | 11 | 1996 | 2166 | 182 | 29 | 29 | 0 |
| 100 (67) | healthy | `status` | 11 | 29 | 30 | 100 | 0 | 0 | 0 |
| 100 (67) | slow | `init` | 3 | 5579 | 5591 | 182 | 3 | 2 | 0 |
| 100 (67) | slow | `inbox --live` | 3 | 5032 | 5037 | 100 | 1 | 1 | 0 |
| 100 (67) | slow | `pump` | 3 | 5496 | 5506 | 182 | 1 | 1 | 0 |
| 100 (67) | slow | `status` | 3 | 29 | 30 | 100 | 0 | 0 | 0 |

### Base (`34e5098`, before this change)

| Tasks (active) | Scenario | Command | Samples | p50 ms | p95 ms | Task reads | Subprocesses | Herdr calls | gh calls |
| ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 (1) | healthy | `init` | 11 | 130 | 141 | 6 | 2 | 1 | 0 |
| 1 (1) | healthy | `inbox --live` | 11 | 9 | 10 | 3 | 0 | 0 | 0 |
| 1 (1) | healthy | `pump` | 11 | 33 | 39 | 3 | 0 | 0 | 0 |
| 1 (1) | healthy | `status` | 11 | 8 | 9 | 3 | 0 | 0 | 0 |
| 1 (1) | slow | `init` | 3 | 149 | 154 | 6 | 2 | 1 | 0 |
| 1 (1) | slow | `inbox --live` | 3 | 9 | 9 | 3 | 0 | 0 | 0 |
| 1 (1) | slow | `pump` | 3 | 34 | 37 | 3 | 0 | 0 | 0 |
| 1 (1) | slow | `status` | 3 | 8 | 8 | 3 | 0 | 0 | 0 |
| 12 (8) | healthy | `init` | 11 | 826 | 841 | 75 | 17 | 8 | 8 |
| 12 (8) | healthy | `inbox --live` | 11 | 12 | 14 | 31 | 0 | 0 | 0 |
| 12 (8) | healthy | `pump` | 11 | 722 | 786 | 39 | 15 | 7 | 8 |
| 12 (8) | healthy | `status` | 11 | 15 | 16 | 32 | 0 | 0 | 0 |
| 12 (8) | slow | `init` | 3 | 50656 | 50703 | 75 | 15 | 6 | 8 |
| 12 (8) | slow | `inbox --live` | 3 | 12 | 13 | 31 | 0 | 0 | 0 |
| 12 (8) | slow | `pump` | 3 | 50561 | 50578 | 39 | 13 | 5 | 8 |
| 12 (8) | slow | `status` | 3 | 12 | 12 | 32 | 0 | 0 | 0 |
| 100 (67) | healthy | `init` | 11 | 7733 | 7809 | 658 | 173 | 68 | 104 |
| 100 (67) | healthy | `inbox --live` | 11 | 47 | 51 | 254 | 0 | 0 | 0 |
| 100 (67) | healthy | `pump` | 11 | 7554 | 7717 | 358 | 171 | 67 | 104 |
| 100 (67) | healthy | `status` | 11 | 49 | 52 | 267 | 0 | 0 | 0 |
| 100 (67) | slow | `init` | 1 | 596688 | 596688 | 658 | 159 | 54 | 104 |
| 100 (67) | slow | `inbox --live` | 1 | 48 | 48 | 254 | 0 | 0 | 0 |
| 100 (67) | slow | `pump` | 1 | 596553 | 596553 | 358 | 157 | 53 | 104 |
| 100 (67) | slow | `status` | 1 | 51 | 51 | 267 | 0 | 0 | 0 |
