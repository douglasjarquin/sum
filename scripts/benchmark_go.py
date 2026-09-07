from __future__ import annotations

import argparse
import json
from pathlib import Path
import statistics
import subprocess
import tempfile
import time

from benchmark_fixture import fixture


ROOT = Path(__file__).resolve().parents[1]


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
        case = fixture(base)
        env = dict(case["env"])
        env["SUM_PYTHON_HELPER"] = str(ROOT / "bin" / "sumctl")
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
            "reference_revision": "b03b8020621e0d417906402a5c7ecc5d63192541",
            "samples": args.samples,
            "scenarios": results,
            "scope": "compiled Cobra entrypoint; status and missing-show use the explicit Python compatibility boundary",
        }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"output": str(args.output), "binary_bytes": record["binary_bytes"], "scenarios": len(results)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
