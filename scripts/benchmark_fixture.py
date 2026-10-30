from __future__ import annotations

from contextlib import contextmanager
from copy import deepcopy
from dataclasses import dataclass
from functools import lru_cache
import hashlib
import hmac
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import resource
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import uuid

from benchmark_report import summarize

ROOT = Path(__file__).resolve().parents[1]
SUMCTL = ROOT / ".local/bin/sumctl"
spec = importlib.util.spec_from_file_location("sum_benchmark_hc_fixture", ROOT / "tests/fixtures/hc.py")
hc_fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hc_fixture)


@dataclass(frozen=True, slots=True)
class BenchmarkError(Exception):
    detail: str

    def __str__(self):
        return self.detail


class Core(hc_fixture.Core):
    def _execute(self, method, params, fault, connection):
        if delay := fault.get("delay_seconds"):
            time.sleep(delay)
        return super()._execute(method, params, fault, connection)


def clean_environment(base):
    env = {key: value for key, value in os.environ.items() if not key.startswith(("SUM_", "HERDR_", "HC_", "FAKE_"))}
    env.update(HOME=str(base / "home"), XDG_CONFIG_HOME=str(base / "config"),
               XDG_STATE_HOME=str(base / "xdg-state"), XDG_RUNTIME_DIR=str(base / "runtime"))
    for key in ("HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"):
        Path(env[key]).mkdir(parents=True, exist_ok=True)
    return env


def rpc(core, method, timeout=5, **params):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(timeout)
        connection.connect(str(core.socket))
        with connection.makefile("rwb") as stream:
            stream.write((json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params)) + "\n").encode())
            stream.flush()
            response = json.loads(stream.readline(2 * 1024 * 1024))
    if response.get("error"):
        raise BenchmarkError(json.dumps(response["error"]))
    return response["result"]


@lru_cache(maxsize=1)
def local_machine():
    if platform.system() == "Darwin":
        output = subprocess.check_output(["/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice"], text=True, timeout=10)
        match = re.search(r'"IOPlatformUUID"\s*=\s*"([^"]+)"', output)
        raw = match.group(1).strip() if match else ""
    else:
        raw = next((path.read_text().strip() for path in (Path("/etc/machine-id"), Path("/var/lib/dbus/machine-id")) if path.is_file()), "")
    if not raw:
        raise BenchmarkError("benchmark requires a readable OS machine identity")
    return "m-" + hmac.new(raw.encode(), b"sum machine identity v1", hashlib.sha256).hexdigest()[:32]


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as stream:
        os.fchmod(stream.fileno(), 0o600)
        stream.write(json.dumps(value, indent=2) + "\n")


def initialize_state(root):
    generation = "g-" + uuid.uuid4().hex[:24]
    home = root / "generations" / generation
    for name in ("tasks", "sessions"):
        (home / name).mkdir(parents=True, mode=0o700)
    stamp = "2026-10-30T00:00:00+00:00"
    write_json(root / "state.json", dict(schema=2, backend="hc", generation=generation))
    write_json(home / "state.json", dict(schema=2, backend="hc", generation=generation, root=str(root),
                                         instance=uuid.uuid4().hex, sum_version="0.1.0", created_at=stamp))
    return home


def registration_key(endpoint):
    identity = "\n".join(endpoint[key] for key in ("machine", "core_id", "address"))
    return hashlib.sha256(identity.encode()).hexdigest()[:16]


def actor(core, home, role, cwd, task="", attempt="benchmark-bootstrap"):
    terminal = rpc(core, "terminal.run", argv=["fixture-program"], cwd=str(cwd), no_notify=True, outlive_parent=True)
    address = terminal["address"]
    pid = 100000 + terminal["created_ms"] * 2
    foreground = dict(pid=pid + 1, argv=["fixture-program"], cwd=str(cwd), name="fixture-program")
    with core._lock:
        core.terminals[address].update(pid=pid, foreground=foreground, status={"state": "idle"})
    endpoint = dict(schema=2, backend="hc", machine=local_machine(), core_id=core.core_id,
                    session="sum-test-benchmark", socket=str(core.socket), address=address,
                    task=task, role=role, attempt=attempt, binding_generation=1, created_ms=terminal["created_ms"], cwd=str(cwd))
    evidence = dict(schema=2, endpoint={key: value for key, value in endpoint.items() if key != "cwd"}, pid=pid, foreground=foreground,
                    observed_at="2026-10-30T00:00:00+00:00")
    receipt = dict(endpoint=evidence["endpoint"], kind=role + "-init", attempt=attempt)
    record = dict(endpoint, incarnation=evidence, native_receipt=receipt, registered_at="2026-10-30T00:00:00+00:00")
    record["task"] = task or None
    write_json(home / "sessions" / (registration_key(endpoint) + ".json"), record)
    return endpoint, evidence, receipt


def actor_env(case, role="coordinator"):
    endpoint = case["actors"][role]
    return dict(case["env"], SUM_HOME=str(case["root"]), SUM_SESSION=endpoint["session"], SUM_CORE_ID=endpoint["core_id"],
                HC_SOCKET=endpoint["socket"], HC_TERMINAL_ID=endpoint["address"], SUM_ROLE=role,
                SUM_TASK=endpoint["task"], SUM_ATTEMPT=endpoint["attempt"], SUM_BINDING_GENERATION=str(endpoint["binding_generation"]))


def run_plain(argv, env, timeout=60):
    result = subprocess.run([str(item) for item in argv], env=env, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise BenchmarkError((result.stderr or result.stdout)[-2000:])
    return json.loads(result.stdout) if result.stdout.strip().startswith("{") else result.stdout


def git_repo(path):
    path.mkdir(parents=True)
    commands = (("init", "-q", "-b", "main"), ("config", "user.name", "sum benchmark"),
                ("config", "user.email", "benchmark@example.invalid"))
    for args in commands:
        subprocess.run(["git", "-C", str(path), *args], check=True, capture_output=True)
    (path / "README.md").write_text("benchmark fixture\n")
    subprocess.run(["git", "-C", str(path), "add", "."], check=True)
    subprocess.run(["git", "-C", str(path), "commit", "-q", "-m", "fixture"], check=True)
    remote = path.parent / (path.name + "-origin.git")
    subprocess.run(["git", "clone", "--bare", "-q", str(path), str(remote)], check=True)
    subprocess.run(["git", "-C", str(path), "remote", "add", "origin", str(remote)], check=True)


@contextmanager
def fixture(base):
    if not SUMCTL.is_file() or SUMCTL.is_symlink():
        raise BenchmarkError("build the development checkout own .local/bin/sumctl executable before benchmarking; runtime links are not followed")
    base.mkdir(parents=True, exist_ok=True)
    env = clean_environment(base)
    repo = base / "repo"
    git_repo(repo)
    brief = base / "brief.md"
    brief.write_text("Benchmark fixture only. No model, publication, or merge.\n")
    stub = base / "codex"
    stub.write_text("#!/bin/sh\n[ \"$1\" = --version ] || exit 99\nprintf '%s\\n' 'codex-cli 0.166.0'\n")
    stub.chmod(0o700)
    env["SUM_CODEX_BIN"] = str(stub)
    env["SUM_CODEGRAPH_BIN"] = str(ROOT / "tests/fixtures/codegraph.py")
    root = base / "state"
    home = initialize_state(root)
    with Core() as core:
        coordinator, evidence, receipt = actor(core, home, "coordinator", ROOT)
        write_json(home / "context.json", dict(coordinator, incarnation=evidence, native_receipt=receipt))
        case = dict(root=root, home=home, repo=repo, brief=brief, env=env, core=core, actors={"coordinator": coordinator})
        prefix = [SUMCTL, "--format", "json", "--home", root]
        run_plain([*prefix, "settings", "set", "--global", "64", "--per-repository", "64"], actor_env(case))
        task = run_plain([*prefix, "prepare", "--repo", repo, "--base", "main", "--brief", brief, "--harness", "codex", "--approved"], actor_env(case))
        run_plain([*prefix, "prepare", "--reconcile", task["id"]], actor_env(case))
        task_path = home / "tasks" / task["id"] / "task.json"
        task = json.loads(task_path.read_text())
        worker, evidence, receipt = actor(core, home, "worker", task["worktree"], task["id"], task["execution"]["worker"]["id"])
        task.update(status="running", worker_endpoint=worker)
        task["execution"]["worker"].update(state="running", incarnation=evidence, native_receipt=receipt)
        write_json(task_path, task)
        case["task"] = task
        case["actors"]["worker"] = worker
        yield case


def clone_case(base, source, name):
    root = base / name / "state"
    shutil.copytree(source["root"], root)
    state = json.loads((root / "state.json").read_text())
    home = root / "generations" / state["generation"]
    state = json.loads((home / "state.json").read_text())
    state["root"] = str(root)
    write_json(home / "state.json", state)
    return dict(source, root=root, home=home)


def add_tasks(home, source_id, count, status):
    source = home / "tasks" / source_id
    for index in range(count):
        task_id = f"t-{index + 1:012x}"
        target = home / "tasks" / task_id
        shutil.copytree(source, target)
        task_path = target / "task.json"
        task = json.loads(task_path.read_text())
        task.update(id=task_id, status=status, repository=f"/fixture/repo-{index}")
        endpoint = task["worker_endpoint"]
        endpoint.update(task=task_id, address=f"t{index + 100}", attempt=f"x-benchmark-{index}")
        task["execution"]["worker"].update(id=endpoint["attempt"])
        task["execution"]["worker"].pop("native_receipt", None)
        task["execution"]["worker"].pop("incarnation", None)
        write_json(task_path, task)
        versions_path = target / "versions.json"
        if versions_path.exists():
            versions = json.loads(versions_path.read_text())
            versions["task"] = task_id
            write_json(versions_path, versions)


def measured_process(argv, env, timeout=60):
    command = [str(item) for item in argv]
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    started = time.perf_counter_ns()
    with tempfile.TemporaryFile(mode="w+", encoding="utf-8") as diagnostics:
        fd = diagnostics.fileno()
        launch = ["/bin/sh", "-c", f'exec "$@" 2>&{fd}', "sum-benchmark", *command]
        timed = ["/usr/bin/time", "-l", *launch] if platform.system() == "Darwin" else ["/usr/bin/time", "-f", "SUM_BENCH_RSS_KB=%M", *launch]
        result = subprocess.run(timed, env=env, text=True, capture_output=True, timeout=timeout, pass_fds=(fd,))
        timing = result.stderr
        diagnostics.seek(0)
        result.stderr = diagnostics.read()
    wall_ms = (time.perf_counter_ns() - started) / 1_000_000
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    rss = None
    for line in timing.splitlines():
        if platform.system() == "Darwin" and re.match(r"\s*\d+\s+maximum resident set size\s*$", line):
            rss = int(line.split()[0]) / 1024
        elif platform.system() != "Darwin" and line.startswith("SUM_BENCH_RSS_KB="):
            rss = int(line.split("=", 1)[1])
    sample = dict(wall_ms=round(wall_ms, 3), cpu_ms=round(1000 * (after.ru_utime - before.ru_utime + after.ru_stime - before.ru_stime), 3),
                  peak_rss_kb=rss, stdout_bytes=len(result.stdout.encode()), stderr_bytes=len(result.stderr.encode()),
                  exit_codes=[result.returncode], measurement="external-process", helper_invocations=1, cpu_scope="measured command children; process-time wrapper overhead included", internal_phases=None, subprocess_count=None)
    return result, sample


def invoke_checked(argv, env, trace, expected=0, timeout=60):
    result, sample = measured_process(argv, env, timeout)
    write_json(trace, sample)
    if result.returncode != expected:
        raise BenchmarkError(f"expected exit {expected}, got {result.returncode}: {(result.stderr or result.stdout)[-2000:]}")
    return sample


def measured_case(case_id, runs, operation, warmups=1):
    for index in range(warmups):
        operation(f"warmup-{index}")
    samples = [operation(f"sample-{index}") for index in range(runs)]
    return dict(id=case_id, status="measured", statistics=summarize(samples), samples=samples)


def tool_version(command, *args):
    found = shutil.which(command)
    if not found:
        return "unavailable"
    result = subprocess.run([found, *args], text=True, capture_output=True, timeout=10)
    return (result.stdout or result.stderr).strip().splitlines()[0]
