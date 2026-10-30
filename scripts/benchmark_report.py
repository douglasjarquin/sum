"""Current benchmark reporting; historical language baselines are not current proof."""
from __future__ import annotations

import math
import statistics

INVENTORY = [
    dict(entrypoint="bin/sumctl", call_sites="shell, generated briefs, tests", frequency_class="interactive", role="native Go helper and durable task ledger"),
    dict(entrypoint="HC plain JSON-RPC", call_sites="typed Go HC client", frequency_class="interactive", role="existing-core native terminal requests; no broker"),
    dict(entrypoint="bin/sumctl hook observer", call_sites="installation-scoped HC event subscription", frequency_class="interactive", role="bounded reconciliation and delivery"),
    dict(entrypoint="bin/sumctl prepare/start/brief", call_sites="coordinator lifecycle", frequency_class="lifecycle", role="Git worktrees, native role receipts and sealed procedures"),
    dict(entrypoint="scripts/hc_lab.py", call_sites="explicit private benchmark/test scope", frequency_class="lifecycle", role="published HC transport without a model or installation state"),
    dict(entrypoint="scripts/setup.py and release/update commands", call_sites="operator upgrades", frequency_class="rare", role="immutable runtime packaging and explicit activation"),
]


def percentile(values, quantile):
    ordered = sorted(values)
    position = (len(ordered) - 1) * quantile
    lower, upper = math.floor(position), math.ceil(position)
    return ordered[lower] if lower == upper else ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower)


def distribution(values):
    if not values:
        return None
    center = statistics.median(values)
    row = dict(p50=round(center, 3), p95=round(percentile(values, .95), 3),
               mad=round(statistics.median(abs(value - center) for value in values), 3),
               min=round(min(values), 3), max=round(max(values), 3))
    if len(values) >= 100:
        row["p99"] = round(percentile(values, .99), 3)
    return row


def summarize(samples):
    fields = ("wall_ms", "cpu_ms", "peak_rss_kb", "stdout_bytes", "stderr_bytes", "helper_invocations")
    return dict(samples=len(samples), **{field: distribution([sample[field] for sample in samples if sample.get(field) is not None]) for field in fields})


def frequency_rows():
    return [
        dict(command="ask", busy_session_calls=1, basis="observed-lab", source="question-to-inbox case"),
        dict(command="report", busy_session_calls=1, basis="observed-lab", source="durable worker callback case"),
        dict(command="prepare", busy_session_calls=1, basis="observed-lab", source="actual Git preparation case"),
        *[dict(command=name, busy_session_calls=count, basis="simulated-fixture", source="explicit serial coordinator workload estimate; no retained read telemetry")
          for name, count in (("inbox", 4), ("status", 4), ("context", 4), ("show", 2))],
    ]


def weighted_opportunities(scenarios, frequency):
    mapping = dict(ask="write.ask", report="write.report", prepare="dispatch.prepare", inbox="read.inbox",
                   status="read.status.12-workers", context="read.context", show="read.show")
    indexed = {row["id"]: row for row in scenarios}
    return [dict(command=row["command"], basis=row["basis"], serial_calls=row["busy_session_calls"],
                 observed_serial_wall_ms=round(indexed[mapping[row["command"]]]["statistics"]["wall_ms"]["p50"] * row["busy_session_calls"], 3),
                 avoidable_ms_upper_bound=None, parallel_waits_added=False)
            for row in frequency if indexed[mapping[row["command"]]]["status"] == "measured"]


def decide(scenarios, opportunities):
    return dict(outcome="defer", status="not-comparable", reason="Current native wall-time measurements do not identify avoidable helper time or establish a language-rewrite saving.",
                minimum_meaningful_improvement=dict(absolute_ms=50, relative_percent=35, frequency_weighted_ms_per_busy_session=500,
                                                    peak_memory_regression_percent=10, behavior_regressions=0),
                avoided_time_proven=False, historical_gate_applied=False)


def render(raw):
    lines = ["# Sum helper and HC transport benchmark", "", f"Measured source: `{raw['source']['sha']}` on {raw['environment']['platform']}.",
             "Current measurements use the native Go helper and HC plain JSON-RPC.",
             raw["decision"]["reason"], "", "| Scenario | Samples | p50 wall ms | p95 wall ms | MAD ms | CPU p50 ms | RSS p50 KiB |", "| --- | ---: | ---: | ---: | ---: | ---: | ---: |"]
    for row in raw["scenarios"]:
        if row["status"] != "measured":
            lines.append(f"| `{row['id']}` | not-run | - | - | - | - | - |")
            continue
        stats = row["statistics"]
        rss = stats["peak_rss_kb"]["p50"] if stats["peak_rss_kb"] else "unavailable"
        lines.append(f"| `{row['id']}` | {stats['samples']} | {stats['wall_ms']['p50']} | {stats['wall_ms']['p95']} | {stats['wall_ms']['mad']} | {stats['cpu_ms']['p50']} | {rss} |")
    lines += ["", "All samples and outliers are retained; p99 is available only with at least 100 samples.",
              "CPU and RSS scopes are identified on every sample; published transport CPU covers the Python client, while fixture transport includes its in-process fixture threads.",
              "Internal helper phases and total descendant subprocess counts are unavailable; no zero or Python-owned saving is invented.",
              "", "## Workload estimates", "", "| Command | Basis | Serial calls | Observed serial wall ms |", "| --- | --- | ---: | ---: |"]
    for row in raw["weighted_opportunities"]:
        lines.append(f"| `{row['command']}` | {row['basis']} | {row['serial_calls']} | {row['observed_serial_wall_ms']} |")
    lines += ["", "These are explicit serial workload estimates, not avoidable time; parallel waits are not added.",
              "", "## Method and limitations", ""]
    lines += [f"- {item}" for item in raw["limitations"]]
    lines += ["", f"No-op process harness p50: {raw['methodology']['harness_overhead_ms']} ms, reported separately without subtraction.",
              f"Private HC lab teardown: `{raw['cleanup']['real_lab']}`.", "", "## Inventory", "", "| Entrypoint | Call sites | Frequency | Role |", "| --- | --- | --- | --- |"]
    lines += [f"| `{item['entrypoint']}` | {item['call_sites']} | {item['frequency_class']} | {item['role']} |" for item in raw["inventory"]]
    lines += ["", "## Reproduce", "", "```sh", "mise run benchmark", "```", "", "Raw samples, scope metadata and missing-measurement reasons are retained in `raw.json`."]
    return "\n".join(lines) + "\n"


def write_report(raw, path):
    path.write_text(render(raw), encoding="utf-8")
