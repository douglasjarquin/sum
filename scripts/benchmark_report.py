from __future__ import annotations

import math
from pathlib import Path
import statistics


INVENTORY = [
    {"entrypoint": "bin/sumctl -> lib/sumctl.py", "call_sites": "operator shell, generated worker briefs, tests, demo", "frequency_class": "interactive", "role": "stable CLI for status/inbox/context/show and ask/report"},
    {"entrypoint": "bin/herdr-scoped -> lib/sumctl.py herdr", "call_sites": "HERDR_BIN used by the Mesh server", "frequency_class": "interactive", "role": "one scoped Python process per MCP tool call"},
    {"entrypoint": "bin/herdr-mesh", "call_sites": "MCP client configuration", "frequency_class": "interactive", "role": "long-lived Node transport; calls bin/herdr-scoped"},
    {"entrypoint": "lib/sumctl.py hook event", "call_sites": "generated Herdr plugin event handler", "frequency_class": "interactive", "role": "bounded native event reconciliation"},
    {"entrypoint": "lib/sumctl.py prepare/dispatch/brief", "call_sites": "coordinator task lifecycle", "frequency_class": "lifecycle", "role": "real Git, Herdr worktree, brief, and graph preparation"},
    {"entrypoint": "scripts/demo.py and scripts/live_smoke.py", "call_sites": "mise demo/test-live and verification", "frequency_class": "lifecycle", "role": "isolated end-to-end fixtures"},
    {"entrypoint": "scripts/setup.py and release/update commands", "call_sites": "mise setup/publish and operator upgrades", "frequency_class": "rare", "role": "installation and immutable release paths"},
    {"entrypoint": "mise-tasks/test, verify, demo, doctor, test-live", "call_sites": "development and release checks", "frequency_class": "rare", "role": "repository-owned command surface"},
]


def percentile(values, quantile):
    ordered = sorted(values)
    position = (len(ordered) - 1) * quantile
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    return ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower)


def distribution(values):
    center = statistics.median(values)
    row = {
        "p50": round(center, 3),
        "p95": round(percentile(values, 0.95), 3),
        "mad": round(statistics.median(abs(value - center) for value in values), 3),
        "min": round(min(values), 3),
        "max": round(max(values), 3),
    }
    if len(values) >= 100:
        row["p99"] = round(percentile(values, 0.99), 3)
    return row


def summarize(samples):
    fields = (
        "wall_ms",
        "startup_ms",
        "cpu_ms",
        "peak_rss_kb",
        "subprocess_count",
        "state_lock_wait_ms",
        "python_owned_ms",
        "external_ms",
        "state_io_ms",
    )
    return {
        "samples": len(samples),
        **{field: distribution([sample[field] for sample in samples]) for field in fields},
    }


def frequency_rows():
    return [
        {"command": "ask", "calls_per_task": 1, "busy_session_calls": 1, "basis": "observed-lab", "source": "worker question-to-inbox scenario"},
        {"command": "report", "calls_per_task": 1, "busy_session_calls": 1, "basis": "observed-lab", "source": "durable worker callback scenario"},
        {"command": "inbox", "calls_per_task": 1, "busy_session_calls": 4, "basis": "simulated-fixture", "source": "representative coordinator rundown"},
        {"command": "status", "calls_per_task": 1, "busy_session_calls": 4, "basis": "simulated-fixture", "source": "representative coordinator checks; no background poll"},
        {"command": "context", "calls_per_task": 2, "busy_session_calls": 4, "basis": "simulated-fixture", "source": "bounded worker/coordinator reads"},
        {"command": "show", "calls_per_task": 1, "busy_session_calls": 2, "basis": "simulated-fixture", "source": "full-record fallback"},
        {"command": "prepare", "calls_per_task": 1, "busy_session_calls": 1, "basis": "observed-lab", "source": "task lifecycle scenario"},
        {"command": "herdr/MCP", "calls_per_task": 1, "busy_session_calls": 4, "basis": "simulated-fixture", "source": "interactive tool-call estimate; model/network time excluded"},
    ]


def weighted_opportunities(scenarios, frequency):
    by_command = {
        "ask": "write.ask",
        "report": "write.report",
        "inbox": "read.inbox",
        "status": "read.status.12-workers",
        "context": "read.context",
        "show": "read.show",
        "prepare": "dispatch.prepare",
        "herdr/MCP": "mcp.real.agent-list",
    }
    indexed = {row["id"]: row for row in scenarios}
    rows = []
    for item in frequency:
        scenario = indexed[by_command[item["command"]]]
        if scenario["status"] != "measured":
            continue
        avoidable = scenario["statistics"]["python_owned_ms"]["p50"]
        calls = item["busy_session_calls"]
        rows.append({
            "command": item["command"],
            "basis": item["basis"],
            "serial_calls": calls,
            "avoidable_ms_each_upper_bound": avoidable,
            "weighted_ms_upper_bound": round(calls * avoidable, 3),
            "parallel_waits_added": False,
        })
    return rows


def decide(scenarios, opportunities):
    envelope = {
        "absolute_ms": 50,
        "relative_percent": 35,
        "frequency_weighted_ms_per_busy_session": 500,
        "peak_memory_regression_percent": 10,
        "behavior_regressions": 0,
    }
    indexed = {row["id"]: row for row in scenarios}
    interactive = [indexed[name] for name in ("read.inbox", "read.status.12-workers", "read.context", "read.show")]
    measured = [row for row in interactive if row["status"] == "measured"]
    best_absolute = max((row["statistics"]["python_owned_ms"]["p50"] for row in measured), default=0)
    best_relative = max(
        (
            100 * row["statistics"]["python_owned_ms"]["p50"] / row["statistics"]["wall_ms"]["p50"]
            for row in measured
            if row["statistics"]["wall_ms"]["p50"]
        ),
        default=0,
    )
    weighted = sum(row["weighted_ms_upper_bound"] for row in opportunities if row["command"] != "herdr/MCP")
    proceed = best_absolute >= envelope["absolute_ms"] and best_relative >= envelope["relative_percent"] and weighted >= envelope["frequency_weighted_ms_per_busy_session"]
    return {
        "outcome": "proceed" if proceed else "defer",
        "next_issue": 38 if proceed else None,
        "minimum_meaningful_improvement": envelope,
        "observed_upper_bound": {
            "best_interactive_python_owned_ms": round(best_absolute, 3),
            "best_interactive_python_owned_percent": round(best_relative, 1),
            "frequency_weighted_ms_per_busy_session": round(weighted, 3),
        },
        "reason": "The gate is crossed; a bounded replacement prototype may be justified." if proceed else "The measured Python-owned upper bound does not cross every gate; keep Python and avoid a language rewrite.",
    }


def render(raw):
    lines = [
        "# Sum helper latency and call-frequency decision",
        "",
        f"Measured source: `{raw['source']['sha']}` on {raw['environment']['platform']}.",
        f"Decision: **{raw['decision']['outcome'].upper()}** for a Go replacement.",
        raw["decision"]["reason"],
        "",
        "## Decision gate",
        "",
        "The gate was fixed before any Go prototype: at least 50 ms and 35% on a measured interactive hot path, at least 500 ms of serial frequency-weighted opportunity in a busy session, no behavior regression, and at most 10% peak-memory regression.",
        "No Go code was built in this issue.",
        "",
        "## Results",
        "",
        "| Scenario | Samples | p50 wall ms | p95 wall ms | MAD ms | Python-owned p50 ms | External p50 ms | Peak RSS p50 KiB | Subprocesses p50 |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for row in raw["scenarios"]:
        if row["status"] != "measured":
            lines.append(f"| `{row['id']}` | {row['status']} | - | - | - | - | - | - | - |")
            continue
        stats = row["statistics"]
        lines.append(f"| `{row['id']}` | {stats['samples']} | {stats['wall_ms']['p50']} | {stats['wall_ms']['p95']} | {stats['wall_ms']['mad']} | {stats['python_owned_ms']['p50']} | {stats['external_ms']['p50']} | {stats['peak_rss_kb']['p50']} | {stats['subprocess_count']['p50']} |")
    lines.extend([
        "",
        "p99 is omitted unless a scenario has at least 100 samples.",
        "All samples are retained; MAD, min, and max remain in `raw.json` so outliers are visible rather than deleted.",
        "",
        "## Frequency-weighted upper bounds",
        "",
        "| Command | Basis | Serial calls in busy session | Avoidable upper bound each ms | Weighted upper bound ms |",
        "| --- | --- | ---: | ---: | ---: |",
    ])
    for row in raw["weighted_opportunities"]:
        lines.append(f"| `{row['command']}` | {row['basis']} | {row['serial_calls']} | {row['avoidable_ms_each_upper_bound']} | {row['weighted_ms_upper_bound']} |")
    lines.extend([
        "",
        "Parallel waits are excluded rather than added as serial user delay.",
        "Observed-lab counts are actual calls made by the named scenarios; coordinator read counts and busy-session MCP counts are explicitly simulated estimates because Sum did not previously retain read telemetry.",
        "Model, network, and provider latency are excluded.",
        "",
        "## Phase attribution and simpler alternatives",
        "",
        "`raw.json` retains each sample's Python-owned upper bound, subprocess time and count, state JSON read/write time, lock-wait time, CPU, and peak RSS.",
        "The Python-owned figure subtracts measured external subprocess, state-I/O, and lock-wait phases but still includes shell and harness overhead, so it is an upper bound rather than a promised rewrite saving.",
        "No simpler optimization was mixed into this language baseline.",
        "",
    ])
    for alternative in raw["alternatives"]:
        lines.append(f"- **{alternative['name']}**: {alternative['basis']}.")
    lines.extend([
        "",
        "## Limitations",
        "",
    ])
    for limitation in raw["limitations"]:
        lines.append(f"- {limitation}")
    lines.extend([
        "",
        "## Inventory",
        "",
        "| Production entrypoint | Call sites | Frequency | Role |",
        "| --- | --- | --- | --- |",
    ])
    for row in raw["inventory"]:
        lines.append(f"| `{row['entrypoint']}` | {row['call_sites']} | {row['frequency_class']} | {row['role']} |")
    lines.extend([
        "",
        "## Method",
        "",
        f"Warmup count was {raw['methodology']['warmups']} and measured scenario order used deterministic seed {raw['methodology']['randomization_seed']}.",
        f"Median no-op process harness overhead was {raw['methodology']['harness_overhead_ms']} ms and is reported separately, not subtracted from raw samples.",
        "Cold means a new Python process; warm-fs means the same fixture was read after an unrecorded warmup without attempting privileged OS cache eviction.",
        "The trace separates Python-owned time from state JSON/fsync/locking and Git/Herdr/Node subprocess time; network and model work are absent.",
        "",
        "## Simpler alternatives before a rewrite",
        "",
        "- Keep one Herdr snapshot per session rather than restoring per-worker scans.",
        "- Prefer bounded `context` reads over repeated full `show` reads.",
        "- Batch only reads the native API already supports; retain durability fsyncs and every safety check.",
        "- Remove a proven redundant subprocess hop only after measuring the exact caller; do not mix that gain into a language-only claim.",
        "",
        "## Reproduce",
        "",
        "```sh",
        "mise run benchmark",
        "```",
        "",
        "Raw samples and environment/tool metadata are in `raw.json` beside this report.",
    ])
    return "\n".join(lines) + "\n"


def write_report(raw, path):
    path.write_text(render(raw), encoding="utf-8")
