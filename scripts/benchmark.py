#!/usr/bin/env python3
from __future__ import annotations

import argparse
import concurrent.futures
import json
from pathlib import Path
import platform
import random
import shutil
import subprocess
import sys
import tempfile
import time
import uuid

from benchmark_fixture import BenchmarkError, add_tasks, clone_case, fixture, git_repo, invoke_checked, measured_case, run_plain, sample, tool_version
from benchmark_report import INVENTORY, decide, frequency_rows, weighted_opportunities, write_report
from benchmark_real import run_real


ROOT = Path(__file__).resolve().parents[1]
SUMCTL = ROOT / "bin" / "sumctl"
ALL_SCENARIOS = (
    "startup.version.cold", "startup.help.warm-fs", "read.settings", "read.status.empty",
    "read.status.12-workers", "read.status.archived-25", "read.status.archived-100", "read.inbox",
    "read.show", "read.context", "write.ask", "write.report", "role.init", "brief.regenerate",
    "dispatch.prepare", "herdr.fake.agent-list", "herdr.real.workspace-list", "mcp.real.agent-list",
    "contention.concurrent-writes", "failure.show-missing", "timeout.herdr-prompt", "question.worker-to-inbox",
)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=ROOT / "benchmarks" / "issue-37")
    parser.add_argument("--samples", type=int, default=15)
    parser.add_argument("--skip-real-herdr", action="store_true")
    parser.add_argument("--skip-mcp", action="store_true")
    parser.add_argument("--skip-timeout", action="store_true")
    args = parser.parse_args()
    if not 1 <= args.samples <= 200:
        parser.error("--samples must be between 1 and 200")
    source_sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    source_dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())
    args.output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="sum-benchmark-", dir="/tmp") as tmp:
        base = Path(tmp).resolve()
        source = fixture(base / "source")
        homes = {"active": source}
        empty = clone_case(base, source, "empty")
        shutil.rmtree(empty["home"] / "tasks")
        (empty["home"] / "tasks").mkdir()
        homes["empty"] = empty
        for label, count, status in (("workers", 11, "running"), ("archived25", 25, "archived"), ("archived100", 100, "archived")):
            row = clone_case(base, source, label)
            add_tasks(row["home"], source["task"]["id"], count, status)
            homes[label] = row
        task_id = source["task"]["id"]
        specs = [
            ("startup.version.cold", [SUMCTL, "--version"], "empty", "w-parent:p1", 0),
            ("startup.help.warm-fs", [SUMCTL, "--help"], "empty", "w-parent:p1", 1),
            ("read.settings", [SUMCTL, "--home", homes["active"]["home"], "settings", "show"], "active", "w-parent:p1", 1),
            ("read.status.empty", [SUMCTL, "--home", homes["empty"]["home"], "status"], "empty", "w-parent:p1", 1),
            ("read.status.12-workers", [SUMCTL, "--home", homes["workers"]["home"], "status"], "workers", "w-parent:p1", 1),
            ("read.status.archived-25", [SUMCTL, "--home", homes["archived25"]["home"], "status"], "archived25", "w-parent:p1", 1),
            ("read.status.archived-100", [SUMCTL, "--home", homes["archived100"]["home"], "status"], "archived100", "w-parent:p1", 1),
            ("read.inbox", [SUMCTL, "--home", homes["active"]["home"], "inbox"], "active", "w-parent:p1", 1),
            ("read.show", [SUMCTL, "--home", homes["active"]["home"], "show", task_id], "active", "w-parent:p1", 1),
            ("read.context", [SUMCTL, "--home", homes["active"]["home"], "context", task_id, "--role", "worker"], "active", "w-parent:p1", 1),
            ("herdr.fake.agent-list", [SUMCTL, "--home", homes["active"]["home"], "herdr", "--", "agent", "list"], "active", "w-parent:p1", 1),
            ("failure.show-missing", [SUMCTL, "--home", homes["active"]["home"], "show", "t-000000000000"], "active", "w-parent:p1", 1),
        ]
        random.Random(37).shuffle(specs)
        scenarios = []
        for case_id, command, home_key, pane, warmups in specs:
            env = {**homes[home_key]["env"], "HERDR_PANE_ID": pane}
            expected = 1 if case_id == "failure.show-missing" else 0
            scenarios.append(measured_case(case_id, args.samples, lambda tag, c=command, e=env, i=case_id, x=expected: invoke_checked(c, e, base / "traces" / f"{i}-{tag}.jsonl", x), warmups))
        fresh_cases = {
            "write.ask": lambda case: [SUMCTL, "--home", case["home"], "ask", task_id, "--key", uuid.uuid4().hex[:8], "--text", "Benchmark question?"],
            "write.report": lambda case: [SUMCTL, "--home", case["home"], "report", task_id, "--text", "Benchmark report."],
            "role.init": lambda case: [SUMCTL, "--home", case["home"], "init"],
            "brief.regenerate": lambda case: [SUMCTL, "--home", case["home"], "brief", "regenerate", task_id],
        }
        for case_id, command_builder in fresh_cases.items():
            def operation(tag, current=case_id, builder=command_builder):
                case = clone_case(base, source, f"{current}-{tag}")
                pane = "w-dev:p1" if current == "role.init" else (source["task"]["pane"] if current in {"write.ask", "write.report"} else "w-parent:p1")
                if current == "brief.regenerate":
                    worker_env = {**case["env"], "HERDR_PANE_ID": source["task"]["pane"]}
                    question = run_plain([SUMCTL, "--home", case["home"], "ask", task_id, "--key", tag[-8:], "--text", "Regenerate benchmark brief?"], worker_env)["question"]
                    run_plain([SUMCTL, "--home", case["home"], "answer", task_id, question["id"], "--text", "Yes."], case["env"])
                command = builder(case)
                return invoke_checked(command, {**case["env"], "HERDR_PANE_ID": pane}, base / "traces" / f"{current}-{tag}.jsonl")
            scenarios.append(measured_case(case_id, args.samples, operation))
        def prepare_operation(tag):
            case = clone_case(base, source, f"dispatch-{tag}")
            repo = base / f"dispatch-repo-{tag}"
            git_repo(repo)
            command = [SUMCTL, "--home", case["home"], "prepare", "--repo", repo, "--brief", source["brief"], "--harness", "codex", "--approved"]
            return invoke_checked(command, case["env"], base / "traces" / f"dispatch-{tag}.jsonl", timeout=120)
        scenarios.append(measured_case("dispatch.prepare", min(args.samples, 5), prepare_operation, 0))
        def question_operation(tag):
            case = clone_case(base, source, f"question-{tag}")
            trace = base / "traces" / f"question-{tag}.jsonl"
            trace.parent.mkdir(parents=True, exist_ok=True)
            started = time.perf_counter_ns()
            first_env = {**case["env"], "HERDR_PANE_ID": source["task"]["pane"], "SUM_MEASURE_FILE": str(trace), "SUM_MEASURE_PARENT_NS": str(started)}
            ask = subprocess.run([str(SUMCTL), "--home", str(case["home"]), "ask", task_id, "--key", tag[-8:], "--text", "Benchmark question?"], env=first_env, text=True, capture_output=True, timeout=60)
            inbox_env = {**case["env"], "HERDR_PANE_ID": "w-parent:p1", "SUM_MEASURE_FILE": str(trace), "SUM_MEASURE_PARENT_NS": str(time.perf_counter_ns())}
            inbox = subprocess.run([str(SUMCTL), "--home", str(case["home"]), "inbox"], env=inbox_env, text=True, capture_output=True, timeout=60)
            if ask.returncode or inbox.returncode or task_id not in inbox.stdout:
                raise BenchmarkError(ask.stderr + inbox.stderr)
            records = [json.loads(line) for line in trace.read_text(encoding="utf-8").splitlines()]
            return sample(records, (time.perf_counter_ns() - started) / 1_000_000)
        scenarios.append(measured_case("question.worker-to-inbox", args.samples, question_operation))
        def concurrent_operation(tag):
            case = clone_case(base, source, f"concurrent-{tag}")
            trace = base / "traces" / f"concurrent-{tag}.jsonl"
            started = time.perf_counter_ns()
            def ask(index):
                env = {**case["env"], "HERDR_PANE_ID": source["task"]["pane"], "SUM_MEASURE_FILE": str(trace), "SUM_MEASURE_PARENT_NS": str(time.perf_counter_ns())}
                return subprocess.run([str(SUMCTL), "--home", str(case["home"]), "ask", task_id, "--key", f"{tag}-{index}", "--text", f"Concurrent benchmark {index}?"], env=env, text=True, capture_output=True, timeout=60)
            with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
                results = list(pool.map(ask, range(4)))
            if any(result.returncode for result in results):
                raise BenchmarkError("\n".join(result.stderr for result in results))
            records = [json.loads(line) for line in trace.read_text(encoding="utf-8").splitlines()]
            return sample(records, (time.perf_counter_ns() - started) / 1_000_000)
        scenarios.append(measured_case("contention.concurrent-writes", min(args.samples, 5), concurrent_operation, 0))
        if args.skip_timeout:
            scenarios.append({"id": "timeout.herdr-prompt", "status": "not-run", "reason": "disabled by --skip-timeout"})
        else:
            def timeout_operation(tag):
                case = clone_case(base, source, f"timeout-{tag}")
                env = {**case["env"], "HERDR_PANE_ID": source["task"]["pane"], "FAKE_PROMPT_HANG": "6"}
                command = [SUMCTL, "--home", case["home"], "ask", task_id, "--key", tag[-8:], "--text", "Bounded timeout benchmark?"]
                return invoke_checked(command, env, base / "traces" / f"timeout-{tag}.jsonl", timeout=15)
            scenarios.append(measured_case("timeout.herdr-prompt", 1, timeout_operation, 0))
        if args.skip_real_herdr:
            real = [
                {"id": "herdr.real.workspace-list", "status": "not-run", "reason": "disabled by --skip-real-herdr"},
                {"id": "mcp.real.agent-list", "status": "not-run", "reason": "disabled because the real-Herdr lab is off"},
            ]
            cleanup = {"real_lab": "not-started", "reason": "disabled by --skip-real-herdr"}
        else:
            real, cleanup = run_real(base, args.samples, not args.skip_mcp)
            real_log = Path(cleanup.get("log", ""))
            if real_log.is_file():
                retained_log = args.output / "real-herdr.log"
                shutil.copy2(real_log, retained_log)
                cleanup["log"] = str(retained_log)
        scenarios.extend(real)
        scenarios.sort(key=lambda row: ALL_SCENARIOS.index(row["id"]))
        overhead = []
        for _ in range(args.samples):
            started = time.perf_counter_ns()
            subprocess.run([sys.executable, "-c", "pass"], check=True)
            overhead.append((time.perf_counter_ns() - started) / 1_000_000)
        frequency = frequency_rows()
        opportunities = weighted_opportunities(scenarios, frequency)
        raw = {
            "schema": 1,
            "source": {"sha": source_sha, "dirty": source_dirty},
            "environment": {"machine": platform.node(), "platform": platform.platform(), "python": platform.python_version(), "git": tool_version("git", "--version"), "node": tool_version("node", "--version"), "herdr": tool_version("herdr", "--version")},
            "methodology": {"warmups": 1, "randomization_seed": 37, "outliers": "retained", "harness_overhead_ms": round(sorted(overhead)[len(overhead) // 2], 3), "cache_conditions": {"cold": "new process; first filesystem sample retained", "warm-fs": "unrecorded warmup before new-process samples"}},
            "inventory": INVENTORY,
            "scenarios": scenarios,
            "frequency": frequency,
            "weighted_opportunities": opportunities,
            "cleanup": cleanup,
            "alternatives": [
                {"name": "avoid repeated full task scans", "basis": "compare empty, 12-worker, and archived-task status scenarios", "mixed_into_language_claim": False},
                {"name": "prefer bounded context reads over full show", "basis": "compare read.context and read.show", "mixed_into_language_claim": False},
                {"name": "batch only reads the native APIs already support", "basis": "one agent-list snapshot already replaces per-worker observation", "mixed_into_language_claim": False},
                {"name": "remove redundant subprocess hops before porting", "basis": "subprocess counts and external phase time in raw samples", "mixed_into_language_claim": False},
            ],
            "limitations": [
                "Read-call frequency was not retained before this opt-in instrumentation, so coordinator read counts are simulated and labelled.",
                "Python-owned time is an upper bound after subtracting measured subprocess, state-I/O, and lock-wait phases; it includes shell and harness overhead.",
                "No model, network provider, live production state, default Herdr session, or provider quota was exercised.",
                "No benchmark number is described as end-to-end product improvement.",
            ],
        }
        raw["decision"] = decide(scenarios, opportunities)
        (args.output / "raw.json").write_text(json.dumps(raw, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        write_report(raw, args.output / "report.md")
        print(json.dumps({"outcome": raw["decision"]["outcome"], "raw": str(args.output / "raw.json"), "report": str(args.output / "report.md"), "scenarios": len(scenarios)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
