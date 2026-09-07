from __future__ import annotations

import fcntl
import json
import os
from pathlib import Path
import resource
import sys
import time
from typing import Final, TypedDict


TRACE_SCHEMA: Final = 1
MAX_RECORD_BYTES: Final = 16 * 1024
PHASES: Final = (
    "io.read_json",
    "io.atomic_json",
    "subprocess",
    "lock.state_wait",
    "lock.delivery_wait",
    "lock.metadata_wait",
)


class PhaseRecord(TypedDict):
    count: int
    total_ms: float


class MeasurementRecord(TypedDict):
    schema: int
    command: str
    exit_code: int
    wall_ms: float
    startup_ms: float
    cpu_user_ms: float
    cpu_system_ms: float
    peak_rss_kb: int
    subprocess_count: int
    subprocesses: dict[str, int]
    state_lock_wait_ms: float
    phases: dict[str, PhaseRecord]


class Recorder:
    __slots__ = (
        "_command",
        "_cpu_started",
        "_finished",
        "_parent_started_ns",
        "_phases",
        "_processes",
        "_started_ns",
        "_trace_path",
    )

    def __init__(self, trace_path: Path, parent_started_ns: int | None):
        self._trace_path = trace_path
        self._parent_started_ns = parent_started_ns
        self._started_ns = time.perf_counter_ns()
        self._cpu_started = resource.getrusage(resource.RUSAGE_SELF)
        self._command = self._initial_command()
        self._phases = {name: [0, 0] for name in PHASES}
        self._processes: dict[str, int] = {}
        self._finished = False

    @classmethod
    def from_environment(cls) -> Recorder | None:
        value = os.environ.get("SUM_MEASURE_FILE")
        if not value:
            return None
        raw_parent = os.environ.get("SUM_MEASURE_PARENT_NS")
        try:
            parent = int(raw_parent) if raw_parent else None
        except ValueError:
            parent = None
        return cls(Path(value).expanduser().resolve(), parent)

    def _initial_command(self) -> str:
        if "--version" in sys.argv[1:]:
            return "version"
        if "--help" in sys.argv[1:] or "-h" in sys.argv[1:]:
            return "help"
        return "parse-error"

    def set_command(self, command: str, subcommand: str | None) -> None:
        self._command = f"{command}.{subcommand}" if subcommand else command

    def add_phase(self, name: str, started_ns: int) -> None:
        phase = self._phases[name]
        phase[0] += 1
        phase[1] += time.perf_counter_ns() - started_ns

    def add_subprocess(self, executable: str) -> None:
        name = Path(executable).name
        category = name if name in {"git", "herdr", "node", "python", "python3", "gh", "codegraph"} else "other"
        self._processes[category] = self._processes.get(category, 0) + 1

    def finish(self, exit_code: int) -> None:
        if self._finished:
            return
        self._finished = True
        ended_ns = time.perf_counter_ns()
        cpu = resource.getrusage(resource.RUSAGE_SELF)
        startup_ns = max(0, self._started_ns - self._parent_started_ns) if self._parent_started_ns else 0
        wall_ns = ended_ns - (self._parent_started_ns or self._started_ns)
        peak_rss = cpu.ru_maxrss if sys.platform == "darwin" else cpu.ru_maxrss * 1024
        phases = {
            name: {"count": values[0], "total_ms": round(values[1] / 1_000_000, 3)}
            for name, values in self._phases.items()
        }
        record: MeasurementRecord = {
            "schema": TRACE_SCHEMA,
            "command": self._command,
            "exit_code": exit_code,
            "wall_ms": round(wall_ns / 1_000_000, 3),
            "startup_ms": round(startup_ns / 1_000_000, 3),
            "cpu_user_ms": round((cpu.ru_utime - self._cpu_started.ru_utime) * 1000, 3),
            "cpu_system_ms": round((cpu.ru_stime - self._cpu_started.ru_stime) * 1000, 3),
            "peak_rss_kb": round(peak_rss / 1024),
            "subprocess_count": sum(self._processes.values()),
            "subprocesses": dict(sorted(self._processes.items())),
            "state_lock_wait_ms": phases["lock.state_wait"]["total_ms"],
            "phases": phases,
        }
        payload = (json.dumps(record, ensure_ascii=True, separators=(",", ":")) + "\n").encode()
        if len(payload) > MAX_RECORD_BYTES:
            return
        try:
            self._trace_path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            descriptor = os.open(self._trace_path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
            with os.fdopen(descriptor, "ab", buffering=0) as stream:
                fcntl.flock(stream, fcntl.LOCK_EX)
                stream.write(payload)
                fcntl.flock(stream, fcntl.LOCK_UN)
        except OSError:
            return
