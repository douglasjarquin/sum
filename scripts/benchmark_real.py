from __future__ import annotations

import json
import os
from pathlib import Path
import selectors
import shutil
import subprocess
import tempfile
import time
import uuid

from benchmark_fixture import BenchmarkError, invoke_checked, sample
from benchmark_report import summarize


ROOT = Path(__file__).resolve().parents[1]
SUMCTL = ROOT / "bin" / "sumctl"


def runtime_tools():
    common = Path(subprocess.check_output(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=ROOT, text=True).strip())
    installation = common.parent if common.name == ".git" else ROOT
    current = installation / ".local" / "current"
    runtime = current.resolve() if current.is_symlink() else installation
    return {
        "runtime": runtime,
        "herdr": runtime / ".local" / "bin" / "herdr",
        "mesh": runtime / ".local" / "bin" / "herdr-mesh",
    }


def rpc(process, selector, request_id: int, method: str, params):
    request = {"jsonrpc": "2.0", "id": request_id, "method": method, "params": params}
    process.stdin.write(json.dumps(request) + "\n")
    process.stdin.flush()
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        events = selector.select(deadline - time.monotonic())
        if not events:
            break
        line = process.stdout.readline()
        if not line:
            raise BenchmarkError(f"MCP exited {process.poll()}")
        response = json.loads(line)
        if response.get("id") == request_id:
            if "error" in response:
                raise BenchmarkError(json.dumps(response["error"]))
            return response["result"]
    raise BenchmarkError(f"MCP response {request_id} timed out")


def run_real(base: Path, runs: int, include_mcp: bool):
    tools = runtime_tools()
    missing = [name for name in ("herdr", "mesh") if not tools[name].is_file()]
    if missing:
        reason = "installed pinned runtime missing " + ", ".join(missing)
        return [
            {"id": "herdr.real.workspace-list", "status": "not-run", "reason": reason},
            {"id": "mcp.real.agent-list", "status": "not-run", "reason": reason},
        ], {"real_lab": "not-started", "reason": reason}
    name = "sum-benchmark-" + uuid.uuid4().hex[:10]
    if name == "default":
        raise BenchmarkError("benchmark session name must not be default")
    lab_root = Path(tempfile.mkdtemp(prefix="sum-bench-real-", dir="/tmp")).resolve()
    env = {key: value for key, value in os.environ.items() if not key.startswith(("SUM_", "HERDR_"))}
    env.update({
        "HOME": str(lab_root / "home"),
        "XDG_CONFIG_HOME": str(lab_root / "config"),
        "HERDR_SESSION": name,
        "SUM_SESSION": name,
        "SHELL": "/bin/bash",
        "SUM_HERDR_BIN": str(tools["herdr"]),
    })
    Path(env["HOME"]).mkdir()
    Path(env["XDG_CONFIG_HOME"]).mkdir()
    log_path = base / "real-herdr.log"
    log = log_path.open("w", encoding="utf-8")
    server = subprocess.Popen([str(tools["herdr"]), "--session", name, "server"], env=env, stdout=log, stderr=log)
    mesh = None
    selector = None
    scenarios = []
    try:
        deadline = time.monotonic() + 15
        while True:
            ready = subprocess.run([str(tools["herdr"]), "--session", name, "workspace", "list"], env=env, text=True, capture_output=True)
            if ready.returncode == 0:
                break
            if server.poll() is not None or time.monotonic() >= deadline:
                log.flush()
                raise BenchmarkError(log_path.read_text(encoding="utf-8")[-2000:])
            time.sleep(0.05)
        created = subprocess.run([str(tools["herdr"]), "--session", name, "workspace", "create", "--cwd", str(ROOT), "--label", "sum-benchmark", "--no-focus"], env=env, text=True, capture_output=True, check=True)
        pane = json.loads(created.stdout)["result"]["root_pane"]["pane_id"]
        env.update({"HERDR_ENV": "1", "HERDR_PANE_ID": pane})
        state = lab_root / "state"
        state.mkdir(mode=0o700)
        (state / "state.json").write_text('{"schema":1,"sum_version":"0.1.0","created_at":"2026-09-07T00:00:00+00:00"}\n', encoding="utf-8")
        initialized = subprocess.run([str(SUMCTL), "--home", str(state), "init"], env=env, text=True, capture_output=True)
        if initialized.returncode:
            raise BenchmarkError(initialized.stderr)
        herdr_samples = []
        for index in range(runs + 1):
            measured = invoke_checked([SUMCTL, "--home", state, "herdr", "--", "workspace", "list"], env, base / f"real-herdr-{index}.jsonl")
            if index:
                herdr_samples.append(measured)
        scenarios.append({"id": "herdr.real.workspace-list", "status": "measured", "statistics": summarize(herdr_samples), "samples": herdr_samples})
        if include_mcp:
            mcp_trace = base / "real-mcp.jsonl"
            mesh_env = {**env, "SUM_HERDR_BIN": str(tools["herdr"]), "SUM_HOME": str(state), "SUM_MEASURE_FILE": str(mcp_trace)}
            mesh = subprocess.Popen([str(tools["mesh"])], env=mesh_env, text=True, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=log)
            selector = selectors.DefaultSelector()
            selector.register(mesh.stdout, selectors.EVENT_READ)
            rpc(mesh, selector, 1, "initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "sum-benchmark", "version": "0.1.0"}})
            mesh.stdin.write('{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
            mesh.stdin.flush()
            mcp_samples = []
            prior_lines = 0
            for index in range(runs + 1):
                started = time.perf_counter_ns()
                rpc(mesh, selector, index + 2, "tools/call", {"name": "herdr_agent_list", "arguments": {}})
                wall_ms = (time.perf_counter_ns() - started) / 1_000_000
                lines = mcp_trace.read_text(encoding="utf-8").splitlines()
                records = [json.loads(line) for line in lines[prior_lines:]]
                prior_lines = len(lines)
                if index:
                    mcp_samples.append(sample(records, wall_ms))
            scenarios.append({"id": "mcp.real.agent-list", "status": "measured", "statistics": summarize(mcp_samples), "samples": mcp_samples})
        else:
            scenarios.append({"id": "mcp.real.agent-list", "status": "not-run", "reason": "disabled by --skip-mcp"})
    finally:
        if selector is not None:
            selector.close()
        if mesh is not None:
            mesh.terminate()
            try:
                mesh.wait(timeout=5)
            except subprocess.TimeoutExpired:
                mesh.kill()
                mesh.wait(timeout=5)
        subprocess.run([str(tools["herdr"]), "--session", name, "server", "stop"], env=env, text=True, capture_output=True)
        if server.poll() is None:
            server.terminate()
        try:
            server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait(timeout=5)
        log.close()
        shutil.rmtree(lab_root)
    cleanup = {
        "real_lab": "removed",
        "session": name,
        "server_exit": server.returncode,
        "mcp_exit": mesh.returncode if mesh else None,
        "path_absent": not lab_root.exists(),
        "log": str(log_path),
        "tools": {
            "runtime": tools["runtime"].name,
            "node": subprocess.check_output([str(tools["node"]), "--version"], text=True).strip(),
            "herdr": subprocess.check_output([str(tools["herdr"]), "--version"], text=True).strip(),
        },
    }
    return scenarios, cleanup
