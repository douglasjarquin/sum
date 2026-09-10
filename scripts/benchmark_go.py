from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile
import tarfile
import time
import re

from benchmark_fixture import fixture


ROOT = Path(__file__).resolve().parents[1]
REFERENCE_REVISION = "b03b8020621e0d417906402a5c7ecc5d63192541"
BASELINE = {
    "startup.version.cold": 136.568,
    "startup.help.warm-fs": 138.560,
    "read.status.empty": 145.848,
    "failure.show-missing": 146.063,
}


def reference_snapshot(destination: Path) -> Path:
    destination.mkdir()
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    with tempfile.TemporaryFile() as archive:
        subprocess.run(["git", "--no-replace-objects", "-C", str(ROOT), "archive", "--format=tar", REFERENCE_REVISION],
                       env=env, stdout=archive, check=True)
        archive.seek(0)
        with tarfile.open(fileobj=archive) as source:
            source.extractall(destination, filter="data")
    return destination


def allocations() -> dict[str, object]:
    command = ["go", "test", "-run", "^$", "-bench", "BenchmarkNewRoot", "-benchmem", "./internal/cli"]
    result = subprocess.run(command, cwd=ROOT / "go", text=True, capture_output=True)
    match = re.search(r"BenchmarkNewRoot-\S+\s+\d+\s+([\d.]+) ns/op\s+([\d.]+) B/op\s+([\d.]+) allocs/op", result.stdout)
    if result.returncode or not match:
        raise RuntimeError(f"allocation benchmark failed: {(result.stderr or result.stdout)[-2000:]}")
    return {
        "command": command,
        "exit": result.returncode,
        "ns_per_op": float(match.group(1)),
        "bytes_per_op": float(match.group(2)),
        "allocs_per_op": float(match.group(3)),
    }


def gate(scenarios: list[dict[str, object]], allocation: dict[str, object]) -> dict[str, object]:
    by_id = {row["id"]: row for row in scenarios}
    version = by_id["startup.version.cold"]
    help_row = by_id["startup.help.cobra"]
    comparisons = {}
    for candidate_id, baseline_id in (("startup.version.cold", "startup.version.cold"), ("startup.help.cobra", "startup.help.warm-fs")):
        baseline = BASELINE[baseline_id]
        candidate = float(by_id[candidate_id]["p50_ms"])
        comparisons[candidate_id] = {
            "baseline_p50_ms": baseline,
            "candidate_p50_ms": candidate,
            "absolute_improvement_ms": round(baseline - candidate, 3),
            "relative_improvement_percent": round((baseline - candidate) / baseline * 100, 2),
        }
    interactive = {
        "required_absolute_ms": 50,
        "required_relative_percent": 35,
        "observed_absolute_improvement_ms": round(max(row["absolute_improvement_ms"] for row in comparisons.values()), 3),
        "observed_relative_improvement_percent": round(max(row["relative_improvement_percent"] for row in comparisons.values()), 2),
        "pass": any(row["absolute_improvement_ms"] >= 50 and row["relative_improvement_percent"] >= 35 for row in comparisons.values()),
    }
    frequency = {"required_ms": 500, "observed_ms": 0, "pass": False, "reason": "No stateful command is native; status remains a compatibility subprocess."}
    behavior = {"required_regressions": 0, "observed_regressions": None, "pass": False, "status": "not-evaluated", "basis": "Expected exit codes are smoke checks, not differential output or effect parity."}
    memory = {"required_regression_percent": 10, "pass": False, "status": "not-comparable", "reason": "The compatibility child is outside the compiled parent's /usr/bin/time memory sample."}
    return {
        "outcome": "defer",
        "pass": False,
        "comparisons": comparisons,
        "interactive_hot_path": interactive,
        "frequency_weighted": frequency,
        "behavior": behavior,
        "memory": memory,
        "allocations": allocation,
        "binary_and_entrypoint": {"version": version["id"], "help": help_row["id"]},
        "reasons": ["The native startup/help path crosses the latency gate." if interactive["pass"] else "The native startup/help path does not cross the latency gate.", "No stateful command is native, so the 500 ms frequency-weighted gate is not established.", "Behavior parity has not been evaluated.", "Compatibility memory is not comparable to the Python child process."],
    }


def measure(command: list[str], env: dict[str, str], samples: int, expected: int = 0, subprocesses: int = 0) -> dict[str, object]:
    values = []
    exit_codes = []
    peak_memory = 0
    for _ in range(samples):
        started = time.perf_counter_ns()
        measured = ["/usr/bin/time", "-l", *command]
        result = subprocess.run(measured, cwd=ROOT, env=env, text=True, capture_output=True)
        values.append((time.perf_counter_ns() - started) / 1_000_000)
        exit_codes.append(result.returncode)
        for line in result.stderr.splitlines():
            if line.strip().endswith("peak memory footprint"):
                peak_memory = max(peak_memory, int(line.split()[0]))
        if result.returncode != expected:
            raise RuntimeError(f"{command} exited {result.returncode}, expected {expected}: {(result.stderr or result.stdout)[-2000:]}")
    ordered = sorted(values)
    return {
        "command": command,
        "samples": samples,
        "p50_ms": round(statistics.median(values), 3),
        "p95_ms": round(ordered[min(len(ordered) - 1, int(len(ordered) * 0.95))], 3),
        "min_ms": round(min(values), 3),
        "max_ms": round(max(values), 3),
        "exit_codes": exit_codes,
        "peak_memory_bytes": peak_memory,
        "subprocesses": subprocesses,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--samples", type=int, default=15)
    args = parser.parse_args()
    if not 1 <= args.samples <= 200:
        parser.error("--samples must be between 1 and 200")

    binary = args.binary.resolve()
    if not binary.is_file():
        raise SystemExit(f"binary does not exist: {binary}")
    with tempfile.TemporaryDirectory(prefix="sum-go-benchmark-") as temporary:
        base = Path(temporary)
        reference = reference_snapshot(base / "reference")
        case = fixture(base, source_root=reference)
        env = dict(case["env"])
        env["SUM_PYTHON_HELPER"] = str(reference / "bin" / "sumctl")
        commands = [
            ("startup.version.cold", [str(binary), "--version"], dict(env)),
            ("startup.help.cobra", [str(binary), "--help"], {**env, "SUM_PYTHON_HELPER": str(base / "missing-reference")}),
            ("read.status.fixture", [str(binary), "--home", str(case["home"]), "status"], dict(env)),
            ("failure.show-missing", [str(binary), "--home", str(case["home"]), "show", "t-000000000000"], dict(env)),
        ]
        results = []
        for case_id, command, command_env in commands:
            expected = 1 if case_id == "failure.show-missing" else 0
            subprocesses = 0 if case_id.startswith("startup.") else 1
            row = measure(command, command_env, args.samples, expected, subprocesses)
            row["id"] = case_id
            if case_id == "failure.show-missing":
                row["expected_exit"] = expected
            results.append(row)
        record = {
            "schema": 1,
            "binary": str(binary),
            "binary_bytes": binary.stat().st_size,
            "reference_revision": REFERENCE_REVISION,
            "reference_helper": str(reference / "bin" / "sumctl"),
            "samples": args.samples,
            "scenarios": results,
            "scope": "compiled Cobra entrypoint; status and missing-show use the explicit Python compatibility boundary",
            "allocations": allocations(),
        }
        record["gate"] = gate(results, record["allocations"])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"output": str(args.output), "binary_bytes": record["binary_bytes"], "scenarios": len(results)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
