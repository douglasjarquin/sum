from __future__ import annotations

from dataclasses import dataclass
import json
import os
from pathlib import Path
import resource
import shutil
import subprocess
import time

from benchmark_report import summarize


ROOT = Path(__file__).resolve().parents[1]
SUMCTL = ROOT / "bin" / "sumctl"
FAKE_HERDR = ROOT / "tests" / "fixtures" / "herdr.py"


@dataclass(frozen=True, slots=True)
class BenchmarkError(Exception):
    detail: str

    def __str__(self) -> str:
        return self.detail


def clean_environment(base: Path, source_root: Path = ROOT) -> dict[str, str]:
    env = {key: value for key, value in os.environ.items() if not key.startswith(("SUM_", "HERDR_"))}
    env.update({
        "HOME": str(base / "home"),
        "XDG_CONFIG_HOME": str(base / "config"),
        "HERDR_ENV": "1",
        "HERDR_PANE_ID": "w-parent:p1",
        "HERDR_SESSION": "sum-benchmark",
        "SUM_SESSION": "sum-benchmark",
        "SUM_HERDR_BIN": str(source_root / "tests" / "fixtures" / "herdr.py"),
        "FAKE_HERDR_ROOT": str(base / "fake"),
        "FAKE_PARENT_CWD": str(source_root),
        "FAKE_SESSION": "sum-benchmark",
    })
    Path(env["HOME"]).mkdir(parents=True, exist_ok=True)
    Path(env["XDG_CONFIG_HOME"]).mkdir(parents=True, exist_ok=True)
    return env


def sample(records, wall_ms):
    phases = {}
    for record in records:
        for name, phase in record["phases"].items():
            phases[name] = phases.get(name, 0) + phase["total_ms"]
    external = phases.get("subprocess", 0)
    state_io = phases.get("io.read_json", 0) + phases.get("io.atomic_json", 0)
    lock_wait = sum(value for name, value in phases.items() if name.startswith("lock."))
    return {
        "wall_ms": round(wall_ms, 3),
        "startup_ms": round(sum(record["startup_ms"] for record in records), 3),
        "cpu_ms": round(sum(record["cpu_user_ms"] + record["cpu_system_ms"] for record in records), 3),
        "peak_rss_kb": max(record["peak_rss_kb"] for record in records),
        "subprocess_count": sum(record["subprocess_count"] for record in records),
        "state_lock_wait_ms": round(sum(record["state_lock_wait_ms"] for record in records), 3),
        "python_owned_ms": round(max(0, wall_ms - external - state_io - lock_wait), 3),
        "external_ms": round(external, 3),
        "state_io_ms": round(state_io, 3),
        "commands": [record["command"] for record in records],
        "exit_codes": [record["exit_code"] for record in records],
        "phases": {name: round(value, 3) for name, value in sorted(phases.items())},
    }


def invoke(argv: list[str], env: dict[str, str], trace: Path, timeout: int = 60):
    trace.unlink(missing_ok=True)
    measured_env = {**env, "SUM_MEASURE_FILE": str(trace), "SUM_MEASURE_PARENT_NS": str(time.perf_counter_ns())}
    cpu_before = resource.getrusage(resource.RUSAGE_CHILDREN)
    started = time.perf_counter_ns()
    result = subprocess.run([str(item) for item in argv], env=measured_env, text=True, capture_output=True, timeout=timeout)
    outer_ms = (time.perf_counter_ns() - started) / 1_000_000
    cpu_after = resource.getrusage(resource.RUSAGE_CHILDREN)
    records = [json.loads(line) for line in trace.read_text(encoding="utf-8").splitlines()]
    measured = sample(records, outer_ms)
    measured["cpu_ms"] = round(((cpu_after.ru_utime - cpu_before.ru_utime) + (cpu_after.ru_stime - cpu_before.ru_stime)) * 1000, 3)
    return result, measured


def invoke_checked(argv: list[str], env: dict[str, str], trace: Path, expected: int = 0, timeout: int = 60):
    result, measured = invoke(argv, env, trace, timeout)
    if result.returncode != expected:
        raise BenchmarkError(f"expected exit {expected}, got {result.returncode}: {(result.stderr or result.stdout)[-2000:]}")
    return measured


def run_plain(argv: list[str], env: dict[str, str], timeout: int = 60):
    result = subprocess.run([str(item) for item in argv], env=env, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise BenchmarkError(result.stderr[-2000:])
    return json.loads(result.stdout) if result.stdout.strip().startswith("{") else result.stdout


def git_repo(path: Path) -> None:
    path.mkdir(parents=True)
    subprocess.run(["git", "-C", str(path), "init", "-q", "-b", "main"], check=True)
    subprocess.run(["git", "-C", str(path), "config", "user.name", "sum benchmark"], check=True)
    subprocess.run(["git", "-C", str(path), "config", "user.email", "benchmark@example.invalid"], check=True)
    (path / "README.md").write_text("benchmark fixture\n", encoding="utf-8")
    subprocess.run(["git", "-C", str(path), "add", "."], check=True)
    subprocess.run(["git", "-C", str(path), "commit", "-q", "-m", "fixture"], check=True)


def fixture(base: Path, source_root: Path = ROOT):
    env = clean_environment(base, source_root)
    repo = base / "repo"
    git_repo(repo)
    brief = base / "brief.md"
    brief.write_text("Benchmark fixture only. No model, publication, or merge.\n", encoding="utf-8")
    home = base / "state"
    home.mkdir(mode=0o700)
    (home / "state.json").write_text('{"schema":1,"sum_version":"0.1.0","created_at":"2026-09-07T00:00:00+00:00"}\n', encoding="utf-8")
    prefix = [source_root / "bin" / "sumctl", "--home", home]
    run_plain([*prefix, "init"], env)
    run_plain([*prefix, "settings", "set", "--global", "64", "--per-repository", "64"], env)
    task = run_plain([*prefix, "dispatch", "--repo", repo, "--brief", brief, "--harness", "codex", "--approved"], env)
    return {"env": env, "repo": repo, "brief": brief, "home": home, "task": task}


def clone_case(base: Path, source, name: str):
    root = base / name
    shutil.copytree(source["home"], root / "state")
    shutil.copytree(Path(source["env"]["FAKE_HERDR_ROOT"]), root / "fake")
    env = {**source["env"], "FAKE_HERDR_ROOT": str(root / "fake")}
    return {**source, "env": env, "home": root / "state"}


def add_tasks(home: Path, source_id: str, count: int, status: str) -> None:
    source = home / "tasks" / source_id
    for index in range(count):
        task_id = f"t-{index + 1:012x}"
        target = home / "tasks" / task_id
        shutil.copytree(source, target)
        task_path = target / "task.json"
        task = json.loads(task_path.read_text(encoding="utf-8"))
        task.update(id=task_id, status=status, pane=f"w-{index + 10}:p1", repository=f"/fixture/repo-{index}")
        task_path.write_text(json.dumps(task) + "\n", encoding="utf-8")
        versions_path = target / "versions.json"
        if versions_path.exists():
            versions = json.loads(versions_path.read_text(encoding="utf-8"))
            versions["task"] = task_id
            versions_path.write_text(json.dumps(versions) + "\n", encoding="utf-8")


def measured_case(case_id: str, runs: int, operation, warmups: int = 1):
    for index in range(warmups):
        operation(f"warmup-{index}")
    samples = [operation(f"sample-{index}") for index in range(runs)]
    return {"id": case_id, "status": "measured", "statistics": summarize(samples), "samples": samples}


def tool_version(command: str, *args: str) -> str:
    found = shutil.which(command)
    if not found:
        return "unavailable"
    result = subprocess.run([found, *args], text=True, capture_output=True, timeout=10)
    return (result.stdout or result.stderr).strip().splitlines()[0]
