#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
VERSION = "hc 0.1.14"


def selected_binary(explicit=None):
    if explicit is not None:
        if not Path(explicit).is_file():
            raise RuntimeError(f"explicit HC binary is unavailable: {explicit}")
        candidate = explicit
    else:
        candidate = str(ROOT / ".local/bin/hc")
        if not Path(candidate).is_file():
            candidate = shutil.which("hc")
    if not candidate:
        raise RuntimeError("pinned HC is unavailable; stage HC 0.1.14 before running test-live")
    binary = str(Path(candidate).resolve())
    version = subprocess.check_output([binary, "--version"], text=True, timeout=10).strip()
    if version != VERSION:
        raise RuntimeError(f"expected {VERSION}, got {version!r}")
    return binary


class Events:
    def __init__(self, lab, **params):
        self.connection, self.stream = lab.connect()
        self.stream.write((json.dumps(dict(jsonrpc="2.0", id=2, method="events.subscribe", params=params)) + "\n").encode())
        self.stream.flush()
        self.snapshot = self.read()["result"]

    def read(self):
        line = self.stream.readline(2 * 1024 * 1024)
        if not line.endswith(b"\n"):
            raise RuntimeError("lost or oversized HC event response")
        return json.loads(line)

    def close(self):
        self.stream.close()
        self.connection.close()

    def wait_event(self, address, kind):
        deadline = time.monotonic() + 15
        while True:
            left = deadline - time.monotonic()
            if left <= 0:
                raise RuntimeError(f"no {kind} event for {address} within 15 seconds")
            self.connection.settimeout(left)
            message = self.read()
            params = message.get("params", {})
            if message.get("method") == "event" and params.get("terminal") == address and params.get("type") == kind:
                return params.get("data")

    def wait_foreground(self, address):
        for terminal in self.snapshot["terminals"]:
            if terminal.get("address") == address and terminal.get("foreground"):
                return terminal["foreground"]
        while True:
            foreground = self.wait_event(address, "foreground")
            if foreground:
                return foreground

    def wait_status(self, address, wanted, after_turn=None):
        def matches(terminal):
            return terminal.get("address") == address and (terminal.get("status") or {}).get("state") == wanted and (
                after_turn is None or terminal.get("turn", 0) > after_turn)
        if any(matches(t) for t in self.snapshot["terminals"]):
            return
        deadline = time.monotonic() + 15
        while True:
            left = deadline - time.monotonic()
            if left <= 0:
                raise RuntimeError(f"no {wanted} status for {address} within 15 seconds")
            self.connection.settimeout(left)
            message = self.read()
            params = message.get("params", {})
            if message.get("method") == "missed":
                self.snapshot = params
                if any(matches(t) for t in params["terminals"]):
                    return
            if params.get("terminal") == address and message.get("method") == "event":
                data = params.get("data") or {}
                if params.get("type") == "status" and (data.get("status") or {}).get("state") == wanted and after_turn is None:
                    return
                if params.get("type") == "turn" and data.get("state") == wanted and after_turn is not None and data["turn"] > after_turn:
                    return


class Lab:
    def __init__(self, binary=None, artifact=None):
        self.binary = selected_binary(binary)
        self.root = Path(tempfile.mkdtemp(prefix="shc-", dir="/tmp"))
        self.session = "sum-test-" + uuid.uuid4().hex[:8]
        self.env = {key: value for key, value in os.environ.items() if key in ("PATH", "LANG", "LC_ALL", "TERM", "TMPDIR")}
        self.env.update(HOME=str(self.root / "h"), XDG_CONFIG_HOME=str(self.root / "c"),
                        XDG_STATE_HOME=str(self.root / "s"), XDG_RUNTIME_DIR=str(self.root / "r"),
                        HC_SESSION=self.session, SHELL="/bin/sh")
        for key in ("HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"):
            Path(self.env[key]).mkdir(mode=0o700)
        self.socket = self.root / "r/herdr-core" / (self.session + ".sock")
        self.core_id = None
        self.events = []
        self.artifact = Path(artifact) if artifact else None
        self.summary = {"runtime": VERSION, "session": self.session, "model_execution": False, "outcome": "failed"}
        self.records = []

    def __enter__(self):
        try:
            result = self.cli("server", "start")
            if result.returncode:
                raise RuntimeError(result.stderr)
            deadline = time.monotonic() + 10
            while not self.socket.is_socket():
                if time.monotonic() >= deadline:
                    raise RuntimeError("scratch core failed to create its socket")
                time.sleep(0.02)
            self.core_id = self.call("hello", generation=1)["core"]["id"]
            if not self.core_id:
                raise RuntimeError("scratch core returned an empty identity")
            self.summary["core_id"] = self.core_id
            return self
        except BaseException as error:
            self.summary["startup_error"] = str(error)
            self._finish(error)
            raise

    def cli(self, *args):
        return subprocess.run([self.binary, *args], env=self.env, text=True, capture_output=True, timeout=15)

    def connect(self):
        connection = socket.socket(socket.AF_UNIX)
        connection.settimeout(15)
        connection.connect(str(self.socket))
        return connection, connection.makefile("rwb")

    def rpc(self, method, **params):
        connection, stream = self.connect()
        try:
            stream.write((json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params)) + "\n").encode())
            stream.flush()
            line = stream.readline(2 * 1024 * 1024)
            if not line.endswith(b"\n"):
                raise RuntimeError("lost or oversized HC response")
            response = json.loads(line)
            if response.get("id") != 1 or response.get("jsonrpc") != "2.0":
                raise RuntimeError("mismatched HC response")
            self.records.append({"method": method, "params": params, "response": response})
            return response
        finally:
            stream.close()
            connection.close()

    def call(self, method, **params):
        response = self.rpc(method, **params)
        if "error" in response:
            raise RuntimeError(response["error"])
        return response["result"]

    def subscribe(self, **params):
        events = Events(self, **params)
        self.events.append(events)
        return events

    def fake(self, name, *, parent=None, outlive=True, first=None, keep=False):
        unique = name + "-" + uuid.uuid4().hex[:6]
        control = self.root / (unique + ".fifo")
        os.mkfifo(control)
        receipt = self.root / (unique + ".jsonl")
        program = ROOT / "tests/fixtures/hc_program.py"
        args = [sys.executable, str(program), "--control", str(control), "--receipts", str(receipt)]
        templates = Path(self.env["XDG_CONFIG_HOME"]) / "herdr-core/templates"
        templates.mkdir(parents=True, exist_ok=True)
        if first is not None:
            import shlex
            template = "sum-test-" + name
            (templates / (template + ".md")).write_text("---\ncommand: " + shlex.join(args) + "\n---\n")
            args = [template, first]
        params = dict(argv=args, cwd=str(self.root), name=name, labels={"sum.lab": self.session},
                      outlive_parent=outlive, keep=keep, rm=False, no_notify=True)
        if parent:
            params["parent"] = parent
        result = self.call("terminal.run", **params)
        return result, control, receipt

    @staticmethod
    def command(control, text):
        with control.open("w") as stream:
            stream.write(text + "\n")

    def _confirm_stopped(self):
        deadline = time.monotonic() + 5
        while True:
            with socket.socket(socket.AF_UNIX) as connection:
                connection.settimeout(0.2)
                try:
                    connection.connect(str(self.socket))
                except (FileNotFoundError, ConnectionRefusedError):
                    return
            if time.monotonic() >= deadline:
                raise RuntimeError("scratch socket still reachable after server stop")
            time.sleep(0.02)

    def _finish(self, error):
        failures = []
        self.summary["core_stopped"] = False
        if error is not None:
            self.summary["error"] = str(error)
        for events in self.events:
            try:
                events.close()
            except BaseException as failure:
                failures.append(failure)
        try:
            if not self.core_id:
                raise RuntimeError("scratch core identity not established; refusing teardown")
            if self.call("hello", generation=1)["core"]["id"] != self.core_id:
                raise RuntimeError("scratch core identity changed; refusing teardown")
            result = self.cli("server", "stop")
            if result.returncode:
                raise RuntimeError(result.stderr or "scratch core stop failed")
            self._confirm_stopped()
            self.summary["core_stopped"] = True
        except BaseException as failure:
            failures.append(failure)
        self.summary["outcome"] = "pass" if error is None and not failures else "failed"
        if failures:
            self.summary["teardown_error"] = "; ".join(str(failure) for failure in failures)
        if self.summary["outcome"] != "pass":
            self.summary["retained_scope"] = str(self.root)
        try:
            self._write_evidence()
        except BaseException as failure:
            failures.append(failure)
            self.summary.update(outcome="failed", retained_scope=str(self.root), evidence_error=str(failure))
            try:
                (self.root / "evidence.json").write_text(json.dumps({**self.summary, "records": self.records}, indent=2) + "\n")
            except OSError:
                pass
        if self.summary["outcome"] == "pass":
            try:
                shutil.rmtree(self.root)
            except OSError as failure:
                failures.append(failure)
                self.summary.update(outcome="failed", retained_scope=str(self.root), cleanup_error=str(failure))
                self._write_evidence()
        if self.summary["outcome"] == "failed":
            print(f"HC lab retained diagnostic scope: {self.root}", file=sys.stderr)
        return failures[0] if failures else None

    def _write_evidence(self):
        encoded = json.dumps({**self.summary, "records": self.records}, indent=2) + "\n"
        (self.root / "evidence.json").write_text(encoded)
        if self.artifact:
            self.artifact.parent.mkdir(parents=True, exist_ok=True)
            self.artifact.write_text(encoded)

    def __exit__(self, exc_type, exc, _):
        failure = self._finish(exc)
        if exc is None and failure is not None:
            raise failure
        return False


def arguments(description):
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument("--hc", help="exact staged HC 0.1.14 binary")
    parser.add_argument("--artifact", help="write JSON evidence outside scratch state")
    return parser.parse_args()
