#!/usr/bin/env python3
"""Benchmark current native helper behavior and isolated HC transport."""
from __future__ import annotations

import argparse
import concurrent.futures
import json
import hashlib
from pathlib import Path
import platform
import random
import resource
import subprocess
import sys
import tempfile
import time

from benchmark_fixture import (BenchmarkError, SUMCTL, actor_env, add_tasks, clone_case, fixture, git_repo,
                               invoke_checked, measured_case, measured_process, rpc, run_plain, tool_version)
from benchmark_real import run_real, transport_sample
from benchmark_report import INVENTORY, decide, frequency_rows, weighted_opportunities, write_report

ROOT = Path(__file__).resolve().parents[1]
ALL_SCENARIOS = (
    "startup.version.cold", "startup.help.warm-fs", "read.settings", "read.status.empty",
    "read.status.12-workers", "read.status.archived-25", "read.status.archived-100", "read.inbox",
    "read.show", "read.context", "write.ask", "write.report", "role.init", "brief.regenerate",
    "dispatch.prepare", "hc.fixture.terminal-list", "hc.real.terminal-list",
    "contention.concurrent-writes", "failure.show-missing", "timeout.hc-response", "question.worker-to-inbox",
)


def command(case, *args):
    return [SUMCTL, "--format", "json", "--home", case["root"], *args]


def process_group(case, calls, parallel=False):
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    started = time.perf_counter_ns()
    def invoke(item):
        role, args = item
        return subprocess.run([str(part) for part in command(case, *args)], env=actor_env(case, role), text=True, capture_output=True, timeout=60)
    if parallel:
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(invoke, calls))
    else:
        results = [invoke(item) for item in calls]
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    if any(result.returncode for result in results):
        raise BenchmarkError("\n".join(result.stderr for result in results if result.returncode))
    if not parallel and case["task"]["id"] not in results[-1].stdout:
        raise BenchmarkError("durable worker question did not reach the inbox view")
    return dict(wall_ms=round((time.perf_counter_ns() - started) / 1_000_000, 3),
                cpu_ms=round(1000 * (after.ru_utime - before.ru_utime + after.ru_stime - before.ru_stime), 3),
                peak_rss_kb=None, stdout_bytes=sum(len(row.stdout.encode()) for row in results),
                stderr_bytes=sum(len(row.stderr.encode()) for row in results), helper_invocations=len(calls),
                exit_codes=[row.returncode for row in results], measurement="process-group",
                cpu_scope="all measured helper children; parallel CPU counted once", internal_phases=None, subprocess_count=None)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "benchmarks/current")
    parser.add_argument("--samples", type=int, default=15)
    parser.add_argument("--skip-real-hc", action="store_true")
    parser.add_argument("--hc-binary", type=Path)
    parser.add_argument("--skip-timeout", action="store_true")
    args = parser.parse_args()
    if not 1 <= args.samples <= 200:
        parser.error("--samples must be between 1 and 200")
    args.output = args.output.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    source = dict(sha=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
                  dirty=bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip()))
    with tempfile.TemporaryDirectory(prefix="sum-benchmark-", dir="/tmp") as temporary:
        base = Path(temporary)
        with fixture(base / "source") as active:
            homes = dict(active=active)
            empty = clone_case(base, active, "empty")
            (empty["home"] / "tasks").rename(empty["home"] / "retained-source-tasks")
            (empty["home"] / "tasks").mkdir()
            homes["empty"] = empty
            for label, count, status in (("workers", 11, "running"), ("archived25", 25, "archived"), ("archived100", 100, "archived")):
                row = clone_case(base, active, label)
                add_tasks(row["home"], active["task"]["id"], count, status)
                homes[label] = row
            task_id = active["task"]["id"]
            specs = [
                ("startup.version.cold", "empty", ["--version"], 0),
                ("startup.help.warm-fs", "empty", ["--help"], 1),
                ("read.settings", "active", ["settings", "show"], 1),
                *[(name, home, ["status"], 1) for name, home in (("read.status.empty", "empty"), ("read.status.12-workers", "workers"), ("read.status.archived-25", "archived25"), ("read.status.archived-100", "archived100"))],
                ("read.inbox", "active", ["inbox"], 1),
                ("read.show", "active", ["show", task_id], 1),
                ("read.context", "active", ["context", task_id, "--role", "worker"], 1),
                ("failure.show-missing", "active", ["show", "t-000000000000"], 1),
            ]
            random.Random(37).shuffle(specs)
            scenarios = []
            for case_id, home_key, argv, warmups in specs:
                case = homes[home_key]
                scenarios.append(measured_case(case_id, args.samples,
                    lambda tag, c=case, a=argv, current=case_id: invoke_checked(command(c, *a), actor_env(c), base / f"{current}-{tag}.json", expected=1 if current == "failure.show-missing" else 0), warmups))
            fresh = {"write.ask": ("worker", lambda _: ["ask", task_id, "--key", "question", "--text", "Benchmark question?"]),
                     "write.report": ("worker", lambda _: ["report", task_id, "--text", "Benchmark report."]),
                     "role.init": ("coordinator", lambda _: ["init", "--role", "developer"]),
                     "brief.regenerate": ("coordinator", lambda _: ["brief", "regenerate", task_id])}
            for case_id, (role, builder) in fresh.items():
                def operation(tag, current=case_id, actor=role, build=builder):
                    case = clone_case(base, active, f"{current}-{tag}")
                    if current == "brief.regenerate":
                        question = run_plain(command(case, "ask", task_id, "--key", "brief", "--text", "Regenerate benchmark brief?"), actor_env(case, "worker"))["question"]
                        run_plain(command(case, "answer", task_id, question["id"], "--text", "Yes."), actor_env(case))
                    argv=command(case, *build(case))
                    if current=="role.init":argv=[SUMCTL,"--format","json","--home",base/"uninitialized-developer-state",*build(case)]
                    return invoke_checked(argv, actor_env(case, actor), base / f"{current}-{tag}.json")
                scenarios.append(measured_case(case_id, args.samples, operation))
            def prepare_operation(tag):
                case = clone_case(base, active, f"dispatch-{tag}")
                repo = base / f"dispatch-repo-{tag}"
                git_repo(repo)
                return invoke_checked(command(case, "prepare", "--repo", repo, "--base", "main", "--brief", active["brief"], "--harness", "codex", "--approved"), actor_env(case), base / f"dispatch-{tag}.json", timeout=120)
            scenarios.append(measured_case("dispatch.prepare", min(args.samples, 5), prepare_operation, 0))
            scenarios.append(measured_case("hc.fixture.terminal-list", args.samples,
                lambda _: transport_sample(lambda: rpc(active["core"], "terminal.list"), cpu_scope="Python client and in-process native fixture threads")))
            def question_operation(tag):
                case = clone_case(base, active, f"question-{tag}")
                return process_group(case, [("worker", ["ask", task_id, "--key", tag, "--text", "Benchmark question?"]), ("coordinator", ["inbox"])])
            scenarios.append(measured_case("question.worker-to-inbox", args.samples, question_operation))
            def concurrent_operation(tag):
                case = clone_case(base, active, f"concurrent-{tag}")
                return process_group(case, [("worker", ["ask", task_id, "--key", f"{tag}-{index}", "--text", f"Concurrent benchmark {index}?"]) for index in range(4)], parallel=True)
            scenarios.append(measured_case("contention.concurrent-writes", min(args.samples, 5), concurrent_operation, 0))
            if args.skip_timeout:
                scenarios.append(dict(id="timeout.hc-response", status="not-run", reason="disabled by --skip-timeout"))
            else:
                def timeout_operation(_):
                    active["core"].queue_fault("terminal.get", delay_seconds=.2)
                    def expected_timeout():
                        try:
                            rpc(active["core"], "terminal.get", timeout=.05, terminal=active["actors"]["worker"]["address"])
                        except TimeoutError:
                            return
                        raise BenchmarkError("native fixture failed to exercise the declared response timeout")
                    sample = transport_sample(expected_timeout, cpu_scope="Python client and in-process native fixture threads")
                    sample["expected_outcome"] = "response-timeout"
                    return sample
                scenarios.append(measured_case("timeout.hc-response", 1, timeout_operation, 0))
            if args.skip_real_hc:
                real, cleanup = [dict(id="hc.real.terminal-list", status="not-run", reason="disabled by --skip-real-hc")], dict(real_lab="not-started", reason="disabled by --skip-real-hc")
            else:
                real, cleanup = run_real(args.output, args.samples, args.hc_binary)
            scenarios.extend(real)
            scenarios.sort(key=lambda row: ALL_SCENARIOS.index(row["id"]))
            overhead = [measured_process([sys.executable, "-c", "pass"], active["env"])[1]["wall_ms"] for _ in range(args.samples)]
            frequency = frequency_rows()
            opportunities = weighted_opportunities(scenarios, frequency)
            raw = dict(schema=2, source=source, environment=dict(platform=platform.platform(), python=platform.python_version(), git=tool_version("git", "--version"), helper_binary=str(SUMCTL), helper_sha256=hashlib.sha256(SUMCTL.read_bytes()).hexdigest(), real_lab_tools=cleanup.get("tools")),
                       methodology=dict(warmups=1, randomization_seed=37, outliers="retained", harness_overhead_ms=sorted(overhead)[len(overhead) // 2],
                                        fixture="schema2 native Unix-socket HC model; seeded public processes and receipts, no harness execution",
                                        measurement="external process wall/child CPU and time-reported helper RSS; native RPC client CPU separately scoped",
                                        cache_conditions=dict(cold="new process; first filesystem sample retained", **{"warm-fs": "unrecorded warmup; no privileged cache eviction"})),
                       inventory=INVENTORY, scenarios=scenarios, frequency=frequency, weighted_opportunities=opportunities, cleanup=cleanup,
                       alternatives=[dict(name=name, mixed_into_language_claim=False) for name in ("bounded task reads", "avoid redundant helper invocations", "use published HC list snapshots")],
                       limitations=["No model, provider, production state, installation runtime path or default HC session is used.",
                                    "Saved workload task counts are ledger fixtures; additional rows are not claimed live native workers.",
                                    "Helper callback cases measure durable local work with notifications pending on unqualified fixture recipients; automatic harness input is not measured.",
                                    "Internal startup/phase timings and total descendant subprocess counts are unavailable in the native CLI.",
                                    "No Python-owned avoidable time or end-to-end product improvement is inferred from wall time.",
                                    "Direct RPC CPU covers the Python client only, and its memory excludes the HC server.",
                                    "Parallel process CPU is counted once across children; parallel wall waits are never added as serial user delay.",
                                    "Cold denotes a new process; operating system caches are not flushed.",
                                    "The shared development host load is uncontrolled; these samples are descriptive, not latency regression guarantees.",
                                    "Historical Python/Go language baselines are not comparable to this changed runtime and workload."])
            raw["decision"] = decide(scenarios, opportunities)
            (args.output / "raw.json").write_text(json.dumps(raw, indent=2) + "\n")
            write_report(raw, args.output / "report.md")
            print(json.dumps(dict(outcome=raw["decision"]["outcome"], raw=str(args.output / "raw.json"), report=str(args.output / "report.md"), scenarios=len(scenarios))))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
