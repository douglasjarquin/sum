"""Measure a selected native helper binary without a retired compatibility mode."""
from __future__ import annotations

import argparse
import json
import hashlib
from pathlib import Path
import re
import subprocess
import tempfile

from benchmark_fixture import actor_env, fixture, measured_process
from benchmark_report import summarize

ROOT = Path(__file__).resolve().parents[1]
HISTORICAL_REFERENCE = "b03b8020621e0d417906402a5c7ecc5d63192541"


def allocations():
    command = ["mise", "exec", "--", "go", "test", "-run", "^$", "-bench", "BenchmarkNewRoot", "-benchmem", "./internal/cli"]
    result = subprocess.run(command, cwd=ROOT / "go", text=True, capture_output=True, timeout=180)
    match = re.search(r"BenchmarkNewRoot-\S+\s+\d+\s+([\d.]+) ns/op\s+([\d.]+) B/op\s+([\d.]+) allocs/op", result.stdout)
    if result.returncode or not match:
        raise RuntimeError(f"allocation benchmark failed: {(result.stderr or result.stdout)[-2000:]}")
    return dict(command=command, exit=result.returncode, ns_per_op=float(match[1]), bytes_per_op=float(match[2]), allocs_per_op=float(match[3]))


def measure(command, env, samples, expected=0):
    measured = []
    for _ in range(samples):
        result, sample = measured_process(command, env)
        if result.returncode != expected:
            raise RuntimeError(f"binary returned {result.returncode}, expected {expected}: {(result.stderr or result.stdout)[-2000:]}")
        measured.append(sample)
    return dict(statistics=summarize(measured), samples=measured, expected_exit=expected)


def gate(scenarios, allocation):
    return dict(outcome="defer", passed=False, status="not-comparable", allocations=allocation,
                reason="Historical Python/Go prototype measurements used a different runtime and behavior; current native samples establish no comparable rewrite or memory-regression claim.",
                historical_reference=dict(revision=HISTORICAL_REFERENCE, applied=False),
                behavior=dict(declared_exit_codes_observed=True, limitation="exit codes alone do not establish complete product equivalence"))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--samples", type=int, default=15)
    args = parser.parse_args()
    if not 1 <= args.samples <= 200:
        parser.error("--samples must be between 1 and 200")
    binary = args.binary.resolve()
    if not binary.is_file():
        parser.error(f"binary does not exist: {binary}")
    with tempfile.TemporaryDirectory(prefix="sum-go-benchmark-", dir="/tmp") as temporary:
        with fixture(Path(temporary)) as case:
            prefix = [str(binary), "--format", "json", "--home", str(case["root"])]
            scenarios = []
            for case_id, argv, expected in (("startup.version.cold", ["--version"], 0), ("startup.help.cobra", ["--help"], 0),
                                           ("read.status.fixture", ["status"], 0), ("failure.show-missing", ["show", "t-000000000000"], 1)):
                row = measure([*prefix, *argv], actor_env(case), args.samples, expected)
                row["id"] = case_id
                scenarios.append(row)
            record = dict(schema=2, binary=str(binary), binary_bytes=binary.stat().st_size, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), scenarios=scenarios,
                          scope="selected native helper; schema2 ledger and private native socket fixture, no model or installation state",
                          host_load="uncontrolled shared development host; descriptive measurements, not latency guarantees",
                          allocations=allocations())
            record["gate"] = gate(scenarios, record["allocations"])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps(dict(output=str(args.output), binary_bytes=record["binary_bytes"], scenarios=len(scenarios))))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
