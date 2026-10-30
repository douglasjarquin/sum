#!/usr/bin/env python3
from __future__ import annotations

from collections import defaultdict, deque
from copy import deepcopy
import json
from pathlib import Path
import socket
import tempfile
import threading


class Core:
    def __init__(self, root=None, core_id="sum-test-core"):
        self._temporary = tempfile.TemporaryDirectory(prefix="sum-hc-", dir="/tmp") if root is None else None
        self.root = Path(self._temporary.name if self._temporary else root)
        self.root.mkdir(parents=True, exist_ok=True)
        self.socket = self.root / "hc.sock"
        self.core_id = core_id
        self.terminals = {}
        self.calls = []
        self.inputs = defaultdict(list)
        self.faults = defaultdict(deque)
        self.seq = 0
        self._next = 0
        self._lock = threading.RLock()
        self._connections = set()
        self._subscribers = {}
        self._threads = []
        self._closed = False
        self._listener = None

    def __enter__(self):
        self._listener = socket.socket(socket.AF_UNIX)
        self._listener.bind(str(self.socket))
        self._listener.listen()
        self._listener.settimeout(0.1)
        self._start_thread(self._accept)
        return self

    def __exit__(self, *_):
        self.close()

    def _start_thread(self, target, *args):
        thread = threading.Thread(target=target, args=args, daemon=True)
        self._threads.append(thread)
        thread.start()

    def close(self):
        with self._lock:
            self._closed = True
            if self._listener:
                self._listener.close()
            for connection in list(self._connections):
                try:
                    connection.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                connection.close()
        for thread in self._threads:
            thread.join(timeout=2)
            if thread.is_alive():
                raise RuntimeError("native fixture thread did not stop")
        if self._temporary:
            self._temporary.cleanup()

    def _accept(self):
        while not self._closed:
            try:
                connection, _ = self._listener.accept()
            except TimeoutError:
                continue
            except OSError:
                return
            with self._lock:
                if self._closed:
                    connection.close()
                    return
                self._connections.add(connection)
            self._start_thread(self._serve, connection)

    @staticmethod
    def _write(connection, value):
        connection.sendall((json.dumps(value) + "\n").encode())

    def _serve(self, connection):
        stream = connection.makefile("rb")
        try:
            for line in stream:
                request = json.loads(line)
                with self._lock:
                    self.calls.append(deepcopy(request))
                    method = request["method"]
                    fault = self.faults[method].popleft() if self.faults[method] else {}
                    if fault.get("refuse"):
                        result, kind = None, fault["refuse"]
                    else:
                        result, kind = self._execute(method, request.get("params", {}), fault, connection)
                    if fault.get("lose_response"):
                        return
                    response = {"jsonrpc": "2.0", "id": request["id"]}
                    if kind:
                        response["error"] = {"code": -32000, "message": kind, "data": {"kind": kind}}
                    else:
                        response["result"] = result
                    self._write(connection, response)
                    if method == "events.subscribe" and not kind:
                        self._subscribers[connection] = deepcopy(request.get("params", {}))
        except (OSError, ValueError):
            pass
        finally:
            stream.close()
            connection.close()
            with self._lock:
                self._connections.discard(connection)
                self._subscribers.pop(connection, None)

    def queue_fault(self, method, **fault):
        with self._lock:
            self.faults[method].append(fault)

    def set_status(self, address, state):
        with self._lock:
            terminal = self.terminals[address]
            ended = (terminal.get("status") or {}).get("state") == "working" and state != "working"
            if ended:
                terminal["turn"] += 1
            terminal["status"] = {"state": state} if state else None
            self._event(address, "status", {"status": terminal["status"], "records": {}, "reported": bool(state)})
            if ended:
                self._event(address, "turn", {"turn": terminal["turn"], "state": state})

    def set_foreground(self, address, foreground):
        with self._lock:
            self.terminals[address]["foreground"] = deepcopy(foreground)
            self._event(address, "foreground", foreground)

    @staticmethod
    def _matches(terminal, params):
        return (not params.get("terminal") or terminal["address"] == params["terminal"]) and (
            not params.get("children_of") or terminal.get("parent") == params["children_of"]) and all(
            terminal["labels"].get(key) == value for key, value in params.get("labels", {}).items())

    def _snapshot(self, params):
        return [deepcopy(t) for t in self.terminals.values() if self._matches(t, params)]

    def _notify(self, connection, method, params):
        try:
            self._write(connection, {"jsonrpc": "2.0", "method": method, "params": params})
        except OSError:
            self._subscribers.pop(connection, None)

    def _event(self, address, kind, data):
        self.seq += 1
        for connection, params in list(self._subscribers.items()):
            if self._matches(self.terminals[address], params):
                self._notify(connection, "event", {"seq": self.seq, "type": kind, "terminal": address, "data": deepcopy(data)})

    def missed(self):
        with self._lock:
            for connection, params in list(self._subscribers.items()):
                self._notify(connection, "missed", {"seq": self.seq, "terminals": self._snapshot(params)})

    def disconnect_subscribers(self):
        with self._lock:
            for connection in list(self._subscribers):
                connection.shutdown(socket.SHUT_RDWR)
                connection.close()
            self._subscribers.clear()

    def exit(self, address, code=0, reason="exited"):
        with self._lock:
            terminal = self.terminals[address]
            if terminal["state"] == "finished":
                return
            terminal.update(state="finished", status=None, foreground=None, pid=None, exit_code=code, reason=reason)
            self._event(address, "status", {"status": None, "records": {}, "reported": False})
            self._event(address, "finished", {"reason": reason, "exit_code": code, "signal": None, "finished_ms": self.seq})
            for child in list(self.terminals.values()):
                if child.get("parent") == address and child.get("die_with_parent"):
                    self.exit(child["address"], code, "parent_exit")

    def _execute(self, method, params, fault, connection):
        if method == "hello":
            return {"generation": 1, "version": "0.1.14", "core": {"id": self.core_id, "name": "sum-test"},
                    "framed": True, "methods": ["hello", "events.subscribe"] + ["terminal." + name for name in (
                        "run", "list", "get", "send", "read", "wait", "label", "interrupt", "kill", "rm")],
                    "features": ["framed", "events", "status", "shell"]}, None
        if method == "terminal.run":
            name = params.get("name")
            if name and any(t.get("name") == name and t["state"] == "live" for t in self.terminals.values()):
                return None, "name_taken"
            self._next += 1
            address = f"t{self._next}"
            terminal = dict(address=address, labels=deepcopy(params.get("labels", {})), argv=params.get("argv", []),
                            cwd=params.get("cwd", str(self.root)), state="live", created_ms=self._next, screen="exact",
                            cols=80, rows=24, turn=0, status=None, foreground=None, name=name,
                            parent=params.get("parent"), keep=params.get("keep", False), rm=params.get("rm", False),
                            notify=not params.get("no_notify", False), die_with_parent=not params.get("outlive_parent", False))
            self.terminals[address] = terminal
            self._event(address, "created", terminal)
            result = deepcopy(terminal)
            result["first_message"] = fault.get("first_message")
            return result, None
        if method == "terminal.list":
            selected = self._snapshot(params)
            hidden = sum(t["state"] == "finished" for t in selected)
            return {"terminals": [t for t in selected if params.get("all") or t["state"] == "live"],
                    "hidden_finished": 0 if params.get("all") else hidden}, None
        if method == "events.subscribe":
            return {"seq": self.seq, "terminals": self._snapshot(params)}, None
        address = params.get("terminal")
        terminal = self.terminals.get(address)
        if terminal is None:
            return None, "not_found"
        if method == "terminal.get":
            return deepcopy(terminal), None
        if method == "terminal.send":
            state = (terminal.get("status") or {}).get("state")
            if terminal["state"] != "live":
                return None, "finished"
            if state == "blocked":
                return None, "blocked"
            if params.get("if") and params["if"] != state:
                return None, "status_mismatch"
            self.inputs[address].append(params["text"])
            return fault.get("send_result", {"turn": terminal["turn"], "status": state, "started": None}), None
        if method == "terminal.read":
            return {"text": "\n".join(self.inputs[address]), "complete": True, "how": "screen"}, None
        if method == "terminal.wait":
            state = (terminal.get("status") or {}).get("state")
            exited = terminal["state"] == "finished"
            return {"terminal": address, "ended": exited or state in ("idle", "done", "blocked", "error"),
                    "reason": "exit" if exited else ("timeout" if state in (None, "working") else "status"),
                    "turn": terminal["turn"], "children": [], "status": state, "exit_code": terminal.get("exit_code")}, None
        if method == "terminal.label":
            for key, value in params["labels"].items():
                if value:
                    terminal["labels"][key] = value
                else:
                    terminal["labels"].pop(key, None)
            self._event(address, "labels", {"labels": deepcopy(terminal["labels"]), "record": deepcopy(terminal)})
            return deepcopy(terminal), None
        if method == "terminal.kill":
            self.exit(address, 137, "killed")
            return deepcopy(terminal), None
        if method == "terminal.rm":
            if terminal["state"] == "live":
                return None, "live"
            self._event(address, "removed", {})
            del self.terminals[address]
            return {"removed": address}, None
        if method == "terminal.interrupt":
            if terminal["state"] != "live":
                return None, "finished"
            if terminal.get("status"):
                self.set_status(address, "idle")
                return {"outcome": "stopped", "key": "esc"}, None
            return {"outcome": "unknown", "key": "ctrl+c"}, None
        return None, "method_not_found"
