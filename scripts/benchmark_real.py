from __future__ import annotations

import resource
import time

from benchmark_report import summarize
from hc_lab import Lab, selected_binary


def transport_sample(call, *, cpu_scope="Python client only; HC server excluded"):
    before = resource.getrusage(resource.RUSAGE_SELF)
    started = time.perf_counter_ns()
    call()
    after = resource.getrusage(resource.RUSAGE_SELF)
    return dict(wall_ms=round((time.perf_counter_ns() - started) / 1_000_000, 3),
                cpu_ms=round(1000 * (after.ru_utime - before.ru_utime + after.ru_stime - before.ru_stime), 3),
                peak_rss_kb=None, stdout_bytes=0, stderr_bytes=0, helper_invocations=0,
                measurement="native-socket-client", cpu_scope=cpu_scope, internal_phases=None, subprocess_count=None)


def run_real(base, runs, binary=None):
    try:
        selected = selected_binary(binary)
    except RuntimeError as error:
        return [dict(id="hc.real.terminal-list", status="not-run", reason=str(error))], dict(real_lab="not-started", reason=str(error))
    evidence = base / "real-hc-evidence.json"
    lab = Lab(selected, artifact=evidence)
    with lab:
        terminal, _, _ = lab.fake("benchmark-program")
        if terminal["state"] != "live":
            raise RuntimeError("private benchmark program did not start")
        samples = []
        for index in range(runs + 1):
            measured = transport_sample(lambda: lab.call("terminal.list", labels={"sum.lab": lab.session}))
            if index:
                samples.append(measured)
    return [dict(id="hc.real.terminal-list", status="measured", statistics=summarize(samples), samples=samples,
                 scope="published HC plain JSON-RPC; one private Python program, no model or Sum authority")], dict(
                     real_lab="removed" if lab.summary["core_stopped"] and not lab.root.exists() else "retained",
                     core_stopped=lab.summary["core_stopped"], session=lab.session, core_id=lab.core_id,
                     evidence=str(evidence), tools=dict(hc=lab.summary["runtime"]))
