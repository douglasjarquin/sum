#!/usr/bin/env python3
"""Small, synchronous helpers for sum. No daemon, scheduler, or model API client."""
from __future__ import annotations

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
from fnmatch import fnmatch
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import tomllib
import uuid

RUNTIME = Path(__file__).resolve().parents[1]  # The code/dependency tree this process runs from: a checkout or an immutable release.
RELEASE_MANIFEST = "release.json"
RELEASES = Path(".local") / "releases"


def resolve_installation():
    """The stable installation identity that owns `.sum` state and every generated absolute path.

    A stable entrypoint (`<installation>/bin/sumctl`) exports SUM_INSTALL_ROOT before executing a runtime.
    The value is honored only when this runtime is that installation itself or one of its staged releases,
    so an inherited variable can never make a development or task checkout adopt another installation's state.
    """
    value = os.environ.get("SUM_INSTALL_ROOT")
    if value:
        candidate = Path(value).resolve()
        if candidate == RUNTIME or (candidate / RELEASES).resolve() in RUNTIME.parents:
            return candidate
    return RUNTIME


ROOT = resolve_installation()
VERSION = "0.1.0"
SCHEMA = 1
BRIEF_SCHEMA = 1  # The worker brief format written by write_brief; recorded in release manifests.
HERDR_VERSION = "0.8.2"
MESH_REV = "54adef519aa6af4dcd0bbd72586d414abab90046"
MESH_REMOTE = "https://github.com/runchr-works/herdr-mesh.git"
MCP_CONTRACT = {"server": "herdr-mesh-sum", "version": "0.1.0", "tools": 10}
TOOLS = ("python3", "node", "herdr", "gh", "quota-axi")
RELEASE_SCHEMA = 1
MAX_TEXT = 256 * 1024
TASK_ID = re.compile(r"t-[a-f0-9]{12}\Z")
SETTINGS_FILE = "settings.json"   # The one owner of executable admission values and worker launch defaults; absent means the defaults below.
SETTINGS_SCHEMA = 1
SETTINGS_KEYS = ("schema", "capacity", "worker", "presets", "reviewer")
PRESET_NAME = re.compile(r"[a-z][a-z0-9_-]{0,31}\Z")
PRESET_MAX = 32                     # Named launch shortcuts per installation; presets are shortcuts, not a configuration framework.
DEFAULT_CAPACITY = {"global": 2, "per_repository": 1}
CAPACITY_MAX = 64
RECIPIENT_TIMEOUT = 5              # Seconds granted to one recipient's prompt; one stuck worker costs at most this.
SNAPSHOT_TIMEOUT = 10              # Seconds for the single per-session `agent list` that replaces per-worker observation calls.
ROLES = ("coordinator", "worker", "developer")
DEV_NAME = re.compile(r"[a-z0-9][a-z0-9._-]{0,39}\Z")
# Commands a candidate helper (running from a development or task checkout) may aim at the installation's state.
READ_ONLY_COMMANDS = {"doctor", "status", "inbox", "show", "release-list", "release-show", "brief-list", "update-status", "refresh-status", "settings-show",
                      "preset-list", "preset-show"}
# Herdr subcommands a developer registration may run through the bridge: observation only.
READ_ONLY = {("agent", "list"), ("agent", "get"), ("agent", "read"), ("agent", "wait"), ("pane", "get"),
             ("pane", "read"), ("pane", "list"), ("workspace", "list"), ("integration", "status"), ("session", "list")}
HARNESSES = {"codex": "codex", "claude": "claude", "grok": "grok",
             "cursor": "cursor-agent", "pi": "pi", "opencode": "opencode",
             "gemini": "gemini", "omp": "omp", "copilot": "copilot"}
HARNESS_KIND = re.compile(r"[a-z][a-z0-9_-]{0,31}\Z")
# Model/reasoning flags verified against each installed CLI's own `--help`; a kind absent here has no verified adapter,
# so a requested model/reasoning is refused for it instead of guessed. Each entry is a tuple of leading argv tokens;
# the value is appended as its own token, never joined into a shell string.
ADAPTERS = {
    "codex": {"model": ("-m",), "reasoning": ("-c", "model_reasoning_effort=")},
    "claude": {"model": ("--model",), "reasoning": ("--effort",)},
    "grok": {"model": ("-m",), "reasoning": ("--reasoning-effort",)},
    "copilot": {"model": ("--model",), "reasoning": ("--effort",)},
    "cursor": {"model": ("--model",)},
    "pi": {"model": ("--model",)},
    "omp": {"model": ("--model=",)},
}
LAUNCH_SCHEMA = 1
LAUNCH_VALUE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:/@,=\[\]-]{0,127}\Z")  # A model or reasoning value: one CLI token, never a flag.


class SumError(Exception):
    pass


def now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def machine():
    return socket.gethostname()


def emit(value):
    print(json.dumps(value, indent=2, ensure_ascii=True))


def run(argv, *, cwd=None, timeout=20, check=True, env=None):
    """Never interpret command arguments through a shell."""
    try:
        result = subprocess.run([str(a) for a in argv], cwd=cwd, text=True, env=env,
                                capture_output=True, timeout=timeout)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise SumError(f"{Path(str(argv[0])).name}: {exc}") from exc
    if check and result.returncode:
        detail = (result.stderr or result.stdout).strip()[-4000:]
        raise SumError(f"{Path(str(argv[0])).name} exited {result.returncode}: {detail}")
    return result


def atomic_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, tmp = tempfile.mkstemp(prefix=".write-", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as out:
            json.dump(value, out, indent=2, ensure_ascii=True)
            out.write("\n")
            out.flush()
            os.fsync(out.fileno())
        os.replace(tmp, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def read_json(path):
    try:
        return json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise SumError(f"Cannot read {path}: {exc}") from exc


def text_input(args):
    value = Path(args.file).read_text(encoding="utf-8") if getattr(args, "file", None) else args.text
    if not value or not value.strip():
        raise SumError("Text must not be empty.")
    if len(value.encode("utf-8")) > MAX_TEXT:
        raise SumError(f"Text exceeds {MAX_TEXT} bytes; use a concise report and reference artifacts.")
    return value


def tool(name):
    override = os.environ.get("SUM_" + name.upper().replace("-", "_") + "_BIN")
    local = RUNTIME / ".local" / "bin" / name  # Pinned to the tree this process started from, never a moving pointer.
    found = override or (str(local) if local.is_file() else shutil.which(name))
    if not found:
        raise SumError(f"Missing {name}. Run mise run setup.")
    return found


def session_from_env():
    value = os.environ.get("SUM_SESSION") or os.environ.get("HERDR_SESSION")
    if not value:
        match = re.search(r"/sessions/([^/]+)/herdr\.sock$", os.environ.get("HERDR_SOCKET_PATH", ""))
        value = match.group(1) if match else None
    if not value or not re.fullmatch(r"[A-Za-z0-9_.-]+", value):
        raise SumError("Cannot identify the Herdr session. Set SUM_SESSION to its explicit name; no default-session fallback.")
    return value


def context():
    if os.environ.get("HERDR_ENV") != "1" or not os.environ.get("HERDR_PANE_ID"):
        raise SumError("Run this command inside a Herdr pane (HERDR_ENV=1 and HERDR_PANE_ID are required).")
    return {"session": session_from_env(), "pane": os.environ["HERDR_PANE_ID"],
            "machine": machine(), "cwd": str(ROOT), "at": now()}


def identity(endpoint):
    """The verified endpoint identity: machine, Herdr session, pane. Cwd and labels are not identity."""
    return (endpoint["machine"], endpoint["session"], endpoint["pane"])


def registration_key(endpoint):
    return hashlib.sha256("\n".join(identity(endpoint)).encode()).hexdigest()[:16]


def herdr(args, *, session, timeout=10, raw=False):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", session):
        raise SumError("Invalid session name.")
    options = list(args[:args.index("--")]) if "--" in args else list(args)
    if any(a == "--session" or a.startswith("--session=") for a in options):
        raise SumError("Do not override sum's explicit Herdr session inside command arguments.")
    result = run([tool("herdr"), "--session", session, *args], timeout=timeout)
    if raw:
        return result.stdout
    try:
        data = json.loads(result.stdout)
    except ValueError as exc:
        raise SumError(f"Herdr did not return JSON: {result.stdout[:300]}") from exc
    if isinstance(data, dict) and data.get("error"):
        raise SumError(f"Herdr: {json.dumps(data['error'])}")
    return data.get("result", data) if isinstance(data, dict) else data


def ensure_version():
    found = run([tool("herdr"), "--version"]).stdout.strip()
    match = re.search(r"\b(\d+\.\d+\.\d+)\b", found)
    if not match or match.group(1) != HERDR_VERSION:
        raise SumError(f"This MVP is pinned to Herdr {HERDR_VERSION}; found {found!r}. Run mise run setup; do not silently mix CLI contracts.")
    return found


def agent_observation(session, pane):
    result = herdr(["agent", "get", pane], session=session, timeout=5)
    agent = result.get("agent", result)
    if not isinstance(agent, dict):
        raise SumError("Unrecognized Herdr agent response.")
    return agent


class Store:
    def __init__(self, home):
        self.home = Path(home).expanduser().resolve()
        self.tasks = self.home / "tasks"
        self.sessions = self.home / "sessions"
        if (self.home / "state.json").exists():
            state = read_json(self.home / "state.json")
            if state.get("schema") != SCHEMA:
                raise SumError("Unsupported state schema. Preserve the original; use the matching sum release. No in-place migration.")

    def init(self):
        self.home.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.tasks.mkdir(exist_ok=True, mode=0o700)
        if not (self.home / "state.json").exists():
            atomic_json(self.home / "state.json", {"schema": SCHEMA, "sum_version": VERSION, "created_at": now()})

    @contextmanager
    def lock(self):
        self.init()
        with (self.home / ".lock").open("a") as handle:
            fcntl.flock(handle, fcntl.LOCK_EX)
            try:
                yield
            finally:
                fcntl.flock(handle, fcntl.LOCK_UN)

    def path(self, task_id):
        if not TASK_ID.fullmatch(task_id):
            raise SumError("Invalid task ID.")
        path = self.tasks / task_id
        if path.is_symlink():
            raise SumError("Task directories must not be symlinks.")
        return path

    def read(self, task_id):
        value = read_json(self.path(task_id) / "task.json")
        if value.get("schema") != SCHEMA or value.get("id") != task_id:
            raise SumError("Task identity/schema mismatch.")
        return value

    def save(self, task):
        task["updated_at"] = now()
        atomic_json(self.path(task["id"]) / "task.json", task)

    def all(self):
        return [self.read(p.parent.name) for p in sorted(self.tasks.glob("t-*/task.json"))]

    def check_machine(self, task):
        if task["machine"] != machine():
            raise SumError("Task belongs to another machine. Inspect saved work and use bind explicitly; stale pane IDs are not portable.")

    def designated(self):
        """Only a state home created by setup or an earlier sum release may host a coordinator; a development checkout never does."""
        return (self.home / "state.json").is_file() and not (self.home / "dev.json").is_file()

    def owner(self):
        path = self.home / "context.json"
        return read_json(path) if path.is_file() else None

    def registration(self, endpoint):
        path = self.sessions / (registration_key(endpoint) + ".json")
        if not path.is_file():
            return None
        value = read_json(path)
        if identity(value) != identity(endpoint):
            raise SumError("Session registration identity mismatch; inspect the sessions directory.")
        return value

    def register(self, endpoint, role, task=None):
        state = read_json(self.home / "state.json")
        previous = self.registration(endpoint)
        value = {"schema": SCHEMA, "key": registration_key(endpoint), "role": role, "task": task,
                 "machine": endpoint["machine"], "session": endpoint["session"], "pane": endpoint["pane"],
                 "cwd": endpoint.get("cwd"), "instance": state.get("instance"), "sum_version": VERSION,
                 "mcp": MCP_CONTRACT,  # The tool surface a session registered under; a connected client keeps it until it restarts.
                 "registered_at": previous["registered_at"] if previous else now(), "updated_at": now()}
        atomic_json(self.sessions / (value["key"] + ".json"), value)
        return value

    def registrations(self):
        return [read_json(p) for p in sorted(self.sessions.glob("*.json"))] if self.sessions.is_dir() else []


# --- fleet capacity: validated optional settings and execution-slot ownership --------------------
#
# An execution slot is held by every recorded task that is not archived. A report, an idle pane, a closed
# worker, or a pane Herdr cannot see never releases it: only `archive --acknowledge`, the boss's explicit
# acknowledgement that the work was inspected and preserved, does. Lowering a limit affects future admission only.

def holds_slot(task):
    return task["status"] != "archived"


def validate_capacity(value):
    """Exact validation of the capacity block; the message names the first defect and nothing is applied."""
    if not isinstance(value, dict):
        raise SumError("capacity must be an object")
    unknown = sorted(set(value) - set(DEFAULT_CAPACITY))
    if unknown:
        raise SumError(f"unknown capacity keys {unknown}; allowed: {sorted(DEFAULT_CAPACITY)}")
    result = dict(DEFAULT_CAPACITY)
    for key, number in value.items():
        if isinstance(number, bool) or not isinstance(number, int) or not 1 <= number <= CAPACITY_MAX:
            raise SumError(f"capacity.{key} must be an integer between 1 and {CAPACITY_MAX}, got {number!r}")
        result[key] = number
    if result["per_repository"] > result["global"]:
        raise SumError(f"capacity.per_repository ({result['per_repository']}) exceeds capacity.global ({result['global']})")
    return result


def validate_worker(value, presets=None):
    """Exact validation of the optional worker launch defaults; a model or reasoning value needs a verified adapter for its harness.

    The block is either a launch specification or `{"preset": NAME}`, a reference to a saved preset that is expanded at dispatch."""
    if value is None:
        return None
    if not isinstance(value, dict):
        raise SumError("worker must be an object")
    if "preset" in value:
        if set(value) != {"preset"}:
            raise SumError("worker is either {'preset': NAME} or a harness/model/reasoning block, not both")
        validate_preset_reference("worker.preset", value["preset"], presets)
        return {"preset": value["preset"]}
    unknown = sorted(set(value) - {"harness", "model", "reasoning"})
    if unknown:
        raise SumError(f"unknown worker keys {unknown}; allowed: ['harness', 'model', 'reasoning'] or ['preset']")
    harness = value.get("harness")
    if not isinstance(harness, str) or not HARNESS_KIND.fullmatch(harness):
        raise SumError("worker.harness must be a Herdr integration kind such as codex or claude")
    result = {"harness": harness}
    for field in ("model", "reasoning"):
        if field in value and value[field] is not None:
            result[field] = validate_launch_value(harness, field, value[field])
    return result


def validate_launch_value(harness, field, value):
    if not isinstance(value, str) or not LAUNCH_VALUE.fullmatch(value):
        raise SumError(f"{field} must be one plain CLI value (letters, digits, . _ : / @ , = [ ] -), got {value!r}")
    adapter = ADAPTERS.get(harness, {})
    if field not in adapter:
        known = sorted(k for k, a in ADAPTERS.items() if field in a)
        raise SumError(f"No verified {field} flag for harness {harness!r}; sum passes only mappings confirmed from an installed CLI's help ({', '.join(known)}). "
                       f"Pass the native argument yourself with --arg, or choose a supported harness.")
    return value


def validate_preset_reference(field, name, presets):
    if not isinstance(name, str) or not PRESET_NAME.fullmatch(name):
        raise SumError(f"{field} must name a preset (lowercase letters, digits, _ -), got {name!r}")
    if presets is not None and name not in presets:
        raise SumError(f"{field} names unknown preset {name!r}; saved presets: {sorted(presets) or 'none'}. Create it with `preset set {name} --harness ...` or point the default elsewhere.")


def validate_preset(name, value):
    """Exact validation of one named preset: a harness, optional model/reasoning through a verified adapter, optional plain native args, and a revision."""
    if not isinstance(name, str) or not PRESET_NAME.fullmatch(name):
        raise SumError(f"preset name must be lowercase letters, digits, _ or - (up to 32 characters), got {name!r}")
    if not isinstance(value, dict):
        raise SumError(f"presets.{name} must be an object")
    allowed = ("harness", "model", "reasoning", "args", "revision")
    unknown = sorted(set(value) - set(allowed))
    if unknown:
        raise SumError(f"unknown keys {unknown} in presets.{name}; allowed: {list(allowed)}")
    harness = value.get("harness")
    if not isinstance(harness, str) or not HARNESS_KIND.fullmatch(harness):
        raise SumError(f"presets.{name}.harness must be a Herdr integration kind such as codex or claude")
    result = {"harness": harness}
    for field in ("model", "reasoning"):
        if value.get(field) is not None:
            result[field] = validate_launch_value(harness, field, value[field])
    args = value.get("args") or []
    if not isinstance(args, list) or any(not isinstance(a, str) or not a or "\0" in a for a in args):
        raise SumError(f"presets.{name}.args must be a list of plain non-empty strings")
    for field in ("model", "reasoning"):
        if result.get(field) is not None and argv_conflicts(harness, field, args):
            raise SumError(f"presets.{name}: {field} {result[field]!r} and an entry of args both set the {harness} {field} flag. Give one.")
    if args:
        result["args"] = list(args)
    revision = value.get("revision", 1)
    if isinstance(revision, bool) or not isinstance(revision, int) or revision < 1:
        raise SumError(f"presets.{name}.revision must be a positive integer")
    result["revision"] = revision
    return result


def validate_presets(value):
    if value is None:
        return {}
    if not isinstance(value, dict):
        raise SumError("presets must be an object keyed by preset name")
    if len(value) > PRESET_MAX:
        raise SumError(f"at most {PRESET_MAX} presets are supported")
    return {name: validate_preset(name, spec) for name, spec in value.items()}


def validate_reviewer(value, presets):
    """The optional reviewer default: a preset name for the coordinator's own reviewer launch, never a launch sum performs by itself."""
    if value is None:
        return None
    if not isinstance(value, dict) or set(value) != {"preset"}:
        raise SumError("reviewer must be {'preset': NAME}")
    validate_preset_reference("reviewer.preset", value["preset"], presets)
    return {"preset": value["preset"]}


def load_settings(store):
    """Read and validate `.sum/settings.json`. Absent: defaults. Present but invalid: an error before any side effect."""
    path = store.home / SETTINGS_FILE
    if path.is_symlink():
        raise SumError(f"{path} must not be a symlink.")
    if not path.is_file():
        return {"schema": SETTINGS_SCHEMA, "capacity": dict(DEFAULT_CAPACITY), "worker": None, "presets": {}, "reviewer": None, "source": "defaults", "path": str(path)}
    try:
        value = read_json(path)
        if not isinstance(value, dict):
            raise SumError("top level must be an object")
        if value.get("schema") != SETTINGS_SCHEMA:
            raise SumError(f"schema must be {SETTINGS_SCHEMA}")
        unknown = sorted(set(value) - set(SETTINGS_KEYS))
        if unknown:
            raise SumError(f"unknown keys {unknown}; allowed: {sorted(SETTINGS_KEYS)}")
        capacity = validate_capacity(value.get("capacity", {}))
        presets = validate_presets(value.get("presets"))
        worker = validate_worker(value.get("worker"), presets)
        reviewer = validate_reviewer(value.get("reviewer"), presets)
    except SumError as exc:
        raise SumError(f"Invalid {path}: {exc}. Fix or remove the file; nothing was admitted or changed, and existing tasks keep running. "
                       f"Defaults ({DEFAULT_CAPACITY['global']} global, {DEFAULT_CAPACITY['per_repository']} per repository, worker same as root) apply only when the file is absent.") from exc
    return {"schema": SETTINGS_SCHEMA, "capacity": capacity, "worker": worker, "presets": presets, "reviewer": reviewer, "source": "settings.json", "path": str(path)}


def occupancy(tasks):
    holders = [t for t in tasks if holds_slot(t)]
    by_repository = {}
    for task in holders:
        by_repository.setdefault(task["repository"], []).append(task["id"])
    return {"global": len(holders), "by_repository": by_repository}


def capacity_view(store, tasks=None):
    tasks = store.all() if tasks is None else tasks
    try:
        settings = load_settings(store)
    except SumError as exc:
        return {"limits": None, "worker": None, "source": "invalid", "error": str(exc), "occupied": occupancy(tasks),
                "note": "Admission is refused until settings.json is fixed; every recorded task keeps its slot and callbacks."}
    return {"limits": settings["capacity"], "worker": settings["worker"], "source": settings["source"], "occupied": occupancy(tasks),
            "presets": preset_summary(settings["presets"]), "reviewer": settings["reviewer"],
            "worker_note": "Saved worker defaults apply to future dispatches only; absent means the worker runs the coordinator's harness. A task prompt overrides them without changing them.",
            "note": "A slot is held by every non-archived task and released only by `archive --acknowledge`; idle, reported, or unobservable workers keep theirs."}


def admit(store, tasks, repository):
    """Decide one admission under the store lock from local records only; the returned row is saved with the task."""
    settings = load_settings(store)
    occupied = occupancy(tasks)
    limits = settings["capacity"]
    same_repository = occupied["by_repository"].get(str(repository), [])
    if occupied["global"] >= limits["global"]:
        raise SumError(f"Capacity: {occupied['global']} of {limits['global']} global execution slots are held ({settings['source']}). "
                       "Archive inspected work with `archive --acknowledge` or raise capacity.global in .sum/settings.json; nothing was dispatched.")
    if len(same_repository) >= limits["per_repository"]:
        raise SumError(f"Capacity: {len(same_repository)} of {limits['per_repository']} slots for {repository} are held by {same_repository} ({settings['source']}). "
                       "One checkout gets one writer by default; raising capacity.global never raises this limit.")
    return {"at": now(), "limits": limits, "source": settings["source"],
            "occupied_before": {"global": occupied["global"], "repository": len(same_repository)}}


def settings_document(settings):
    document = {"schema": SETTINGS_SCHEMA, "capacity": settings["capacity"]}
    if settings.get("worker"):
        document["worker"] = settings["worker"]
    if settings.get("presets"):
        document["presets"] = settings["presets"]
    if settings.get("reviewer"):
        document["reviewer"] = settings["reviewer"]
    return document


def save_settings(store, settings):
    """One atomic, owner-only write of the fully validated document. Call under the store lock."""
    path = store.home / SETTINGS_FILE
    atomic_json(path, settings_document(settings))
    os.chmod(path, 0o600)
    return path


SETTINGS_NOTE = "Applies to future admissions and dispatches only. No worker was stopped, relaunched, or switched; the coordinator's own harness and model are untouched."


def write_settings(store, capacity=None, worker=None, clear_worker=False, worker_preset=None, reviewer_preset=None, clear_reviewer=False):
    """Validate the merged settings fully before one atomic write; a saved worker default changes future dispatches only."""
    with store.lock():
        current = load_settings(store)
        merged_capacity = validate_capacity({**current["capacity"], **(capacity or {})})
        merged_worker = current["worker"]
        if clear_worker:
            merged_worker = None
        elif worker_preset is not None:
            merged_worker = validate_worker({"preset": worker_preset}, current["presets"])  # The default becomes a reference; the preset is expanded at dispatch.
        elif worker:
            saved = current["worker"] if current["worker"] and "preset" not in current["worker"] else {}
            harness = worker.get("harness") or saved.get("harness")
            if not harness:
                raise SumError("Give --worker-harness when saving a worker model or reasoning default; a model belongs to one harness.")
            base = dict(saved) if harness == saved.get("harness") else {}
            merged_worker = validate_worker({**base, **worker, "harness": harness})  # A harness change (or a replaced preset reference) drops the old model/reasoning.
        merged_reviewer = current["reviewer"]
        if clear_reviewer:
            merged_reviewer = None
        elif reviewer_preset is not None:
            merged_reviewer = validate_reviewer({"preset": reviewer_preset}, current["presets"])
        previous = {"capacity": current["capacity"], "worker": current["worker"], "reviewer": current["reviewer"]}
        path = save_settings(store, {"capacity": merged_capacity, "worker": merged_worker, "presets": current["presets"], "reviewer": merged_reviewer})
        occupied = occupancy(store.all())
    return {"path": str(path), "previous": previous["capacity"], "capacity": merged_capacity,
            "previous_worker": previous["worker"], "worker": merged_worker,
            "previous_reviewer": previous["reviewer"], "reviewer": merged_reviewer, "occupied": occupied, "note": SETTINGS_NOTE}


# --- named launch presets: validated harness/model/argv shortcuts in the same settings file ---------------
#
# A preset is expanded at `prepare` into the same explicit launch specification every dispatch gets, and the
# specification plus the preset's name and revision are persisted with the task. Editing or deleting the preset
# afterwards changes future dispatches only. Nothing here starts an agent, holds a credential, or adds a role.

def preset_launch(name, spec):
    argv = []
    for field in ("model", "reasoning"):
        if spec.get(field) is not None:
            argv.extend(adapter_argv(spec["harness"], field, spec[field]))
    argv.extend(spec.get("args", []))
    return {"harness": spec["harness"], "model": spec.get("model"), "reasoning": spec.get("reasoning"), "argv": argv}


def preset_summary(presets):
    return {name: {"harness": spec["harness"], "model": spec.get("model"), "reasoning": spec.get("reasoning"), "args": spec.get("args", []), "revision": spec["revision"]}
            for name, spec in sorted(presets.items())}


def preset_references(settings, name):
    refs = []
    if (settings.get("worker") or {}).get("preset") == name:
        refs.append("worker default (`settings set --clear-worker` or another `--worker-preset`)")
    if (settings.get("reviewer") or {}).get("preset") == name:
        refs.append("reviewer default (`settings set --clear-reviewer` or another `--reviewer-preset`)")
    return refs


def preset_list(store):
    settings = load_settings(store)
    return {"presets": preset_summary(settings["presets"]), "worker": settings["worker"], "reviewer": settings["reviewer"], "source": settings["source"], "path": settings["path"],
            "note": "Presets are dispatch shortcuts expanded at prepare; each task keeps the specification it was prepared with. Nothing here is a running agent, a role, or a default until you say so."}


def preset_show(store, name):
    settings = load_settings(store)
    validate_preset_reference("preset", name, settings["presets"])
    spec = settings["presets"][name]
    return {"name": name, "revision": spec["revision"], "preset": spec, "launch": preset_launch(name, spec),
            "used_by": [ref.split(" (")[0] for ref in preset_references(settings, name)],
            "note": "`launch.argv` is exactly what `dispatch --preset` appends after the harness executable; a model here is CLI-requested, never runtime-verified."}


def write_preset(store, name, harness=None, model=None, reasoning=None, args=None, clear=()):
    """Create or revise one preset. A revision bumps on every change so a task record can name the exact version it expanded."""
    with store.lock():
        current = load_settings(store)
        previous = current["presets"].get(name)
        if not PRESET_NAME.fullmatch(name or ""):
            raise SumError(f"preset name must be lowercase letters, digits, _ or - (up to 32 characters), got {name!r}")
        base = {k: v for k, v in (previous or {}).items() if k not in ("revision", *clear)}
        if harness and previous and harness != previous["harness"]:
            base = {}  # A harness change drops the old harness's model, reasoning, and native args instead of carrying them across CLIs.
        merged = {**base, **{k: v for k, v in (("harness", harness), ("model", model), ("reasoning", reasoning)) if v is not None}}
        if args is not None:
            merged["args"] = list(args)
        if "harness" not in merged:
            raise SumError(f"Give --harness when creating preset {name!r}; a preset is a shortcut for one harness.")
        merged["revision"] = (previous["revision"] + 1) if previous else 1
        spec = validate_preset(name, merged)
        if not previous and len(current["presets"]) >= PRESET_MAX:
            raise SumError(f"At most {PRESET_MAX} presets; delete one first.")
        presets = {**current["presets"], name: spec}
        path = save_settings(store, {**current, "presets": presets})
    return {"path": str(path), "name": name, "previous": previous, "preset": spec, "launch": preset_launch(name, spec),
            "note": "Future dispatches that select this preset expand this revision. Prepared or running tasks keep the specification they were prepared with. " + SETTINGS_NOTE}


def delete_preset(store, name):
    with store.lock():
        current = load_settings(store)
        validate_preset_reference("preset", name, current["presets"])
        refs = preset_references(current, name)
        if refs:
            raise SumError(f"Preset {name!r} is still the {' and the '.join(refs)}. Repoint or clear that default first; nothing was deleted.")
        presets = {k: v for k, v in current["presets"].items() if k != name}
        path = save_settings(store, {**current, "presets": presets})
    return {"path": str(path), "deleted": name, "remaining": sorted(presets),
            "note": "Tasks prepared with this preset keep their persisted specification; nothing running was touched."}


# --- worker launch: precedence, verified adapters, explicit argv persisted at prepare -----------------


def root_launch(ctx):
    """What Herdr reliably exposes about the calling pane: its agent kind. No harness exposes its model through Herdr."""
    try:
        agent = agent_observation(ctx["session"], ctx["pane"])
    except SumError as exc:
        return {"harness": None, "model": None, "error": str(exc)}
    kind = agent.get("agent")
    if not isinstance(kind, str) or not HARNESS_KIND.fullmatch(kind):
        return {"harness": None, "model": None, "error": f"Herdr reports no agent kind for pane {ctx['pane']}"}
    return {"harness": kind, "model": None, "reasoning": None, "observed_at": now()}


def adapter_argv(harness, field, value):
    prefix = ADAPTERS[harness][field]
    if prefix[-1].endswith("="):
        return [*prefix[:-1], prefix[-1] + value]
    return [*prefix, value]


def argv_conflicts(harness, field, extra):
    """An explicit --arg that already carries the field's native flag conflicts with a resolved value for it."""
    prefix = ADAPTERS.get(harness, {}).get(field)
    if not prefix:
        return False
    head = prefix[0]
    flag = head.rstrip("=")
    if len(prefix) == 2 and prefix[1].endswith("="):  # `-c key=value`: the conflict is the key, not the generic option.
        key = prefix[1]
        return any(a.startswith(key) or a == f"{flag}={key}" or a.startswith(f"{flag}={key}") for a in extra)
    return any(a == flag or a.startswith(flag + "=") for a in extra)


def resolve_launch(settings, ctx, harness=None, model=None, reasoning=None, same_as_root=False, extra=(), preset=None):
    """One explicit launch specification from the precedence: explicit instruction, chosen preset, saved worker default, known root, native default.

    Every conflict is reported here, before any record or Herdr call. The result is the exact argv that `start` will pass."""
    if same_as_root and (harness or model or reasoning or preset):
        raise SumError("--same-as-you conflicts with --harness/--model/--reasoning/--preset: same-as-you means the coordinator's own harness and native model.")
    if harness is not None and not HARNESS_KIND.fullmatch(harness):
        raise SumError("Harness must be a Herdr integration kind, such as codex, claude, grok, or cursor.")
    extra = list(extra)
    if any(not isinstance(a, str) or "\0" in a for a in extra):
        raise SumError("Harness arguments must be plain strings.")
    presets = settings.get("presets") or {}
    saved = settings.get("worker") or {}
    chosen = None  # {"name", "revision", "source", spec fields}: the preset whose fields this launch inherits, if any.
    if preset is not None:
        if preset not in presets:
            raise SumError(f"Unknown preset {preset!r}; saved presets: {sorted(presets) or 'none'}. Run `preset list`, or create it with `preset set {preset} --harness ...`. Nothing was created.")
        chosen = {"name": preset, "source": "preset", **presets[preset]}
        if harness and harness != chosen["harness"]:
            raise SumError(f"Preset {preset!r} runs on {chosen['harness']} but --harness {harness} was requested. Choose one: drop --harness, pick another preset, or dispatch without --preset. Nothing was created.")
    elif saved.get("preset") and not same_as_root:
        if saved["preset"] not in presets:
            raise SumError(f"The saved worker default names unknown preset {saved['preset']!r}. Fix .sum/settings.json (`preset set` or `settings set --clear-worker`), or pass --preset/--harness explicitly.")
        default = presets[saved["preset"]]
        if harness and harness != default["harness"]:
            chosen = None  # A harness-only override never carries another harness's preset along; same rule as a plain saved default.
        else:
            chosen = {"name": saved["preset"], "source": "saved-default", **default}
    plain_saved = saved if saved and "preset" not in saved else {}
    root = None
    source = {}
    if harness:
        source["harness"] = "explicit"
    elif chosen:
        harness = chosen["harness"]
        source["harness"] = chosen["source"]
    elif same_as_root or not plain_saved:
        root = root_launch(ctx)
        if not root["harness"]:
            raise SumError(f"Cannot determine the coordinator's own harness ({root['error']}). Pass --harness explicitly or save a worker default with `settings set --worker-harness`.")
        harness = root["harness"]
        source["harness"] = "same-as-you" if same_as_root else "root"
    else:
        harness = plain_saved["harness"]
        source["harness"] = "saved-default"
    # A saved model/reasoning belongs to the saved harness only; a harness-only override never inherits it.
    inherits_saved = bool(plain_saved) and not same_as_root and harness == plain_saved.get("harness")
    preset_args = list(chosen.get("args", [])) if chosen else []
    all_extra = [*preset_args, *extra]
    values = {}
    for field, explicit in (("model", model), ("reasoning", reasoning)):
        if explicit is not None:
            values[field] = validate_launch_value(harness, field, explicit)
            source[field] = "explicit"
        elif chosen and chosen.get(field):
            values[field] = validate_launch_value(harness, field, chosen[field])
            source[field] = chosen["source"]
        elif inherits_saved and plain_saved.get(field):
            values[field] = validate_launch_value(harness, field, plain_saved[field])
            source[field] = "saved-default"
        else:
            values[field] = None
            source[field] = "native-default"  # Unknown root model or none saved: the harness's own default, disclosed, never claimed as inheritance.
        if values[field] is not None and argv_conflicts(harness, field, all_extra):
            where = "a preset arg" if argv_conflicts(harness, field, preset_args) else "an explicit --arg"
            raise SumError(f"Conflicting {field}: {field} {values[field]!r} ({source[field]}) and {where} both set the {harness} {field} flag. Give one.")
    argv = []
    for field in ("model", "reasoning"):
        if values[field] is not None:
            argv.extend(adapter_argv(harness, field, values[field]))
    argv.extend(all_extra)
    preset_record = None
    if chosen:
        preset_record = {"name": chosen["name"], "revision": chosen["revision"], "source": chosen["source"],
                         "harness": chosen["harness"], "model": chosen.get("model"), "reasoning": chosen.get("reasoning"), "args": preset_args}
    return {"schema": LAUNCH_SCHEMA, "harness": harness, "model": values["model"], "reasoning": values["reasoning"], "argv": argv,
            "source": source, "explicit_args": extra, "same_as_root": same_as_root, "root": root, "preset": preset_record,
            "saved_default": saved or None, "resolved_at": now(),
            "observed": {"status": "not-started", "harness": None, "model": "not-exposed"}}


def launch_confirmation(launch):
    model = launch["model"] or "native default"
    parts = [f"harness {launch['harness']} ({launch['source']['harness']})", f"model {model} ({launch['source']['model']})"]
    if launch["reasoning"]:
        parts.append(f"reasoning {launch['reasoning']} ({launch['source']['reasoning']})")
    if launch["explicit_args"]:
        parts.append(f"explicit args {launch['explicit_args']}")
    if launch.get("preset"):
        parts.append(f"preset {launch['preset']['name']} r{launch['preset']['revision']} ({launch['preset']['source']})")
    status = launch["observed"]["status"]
    verification = {"not-started": "not started yet",
                    "harness-observed": "Herdr confirmed the harness kind; a CLI-requested model is not runtime-verified because no harness exposes it",
                    "harness-mismatch": "Herdr reports a different agent kind than requested; inspect the pane"}.get(status, status)
    return f"Launch: {', '.join(parts)}; argv {launch['argv']}; {verification}."


def task_launch(task):
    """Old records carry only `harness`; they start exactly as before, with no argv."""
    launch = task.get("launch")
    if launch:
        return launch
    return {"schema": LAUNCH_SCHEMA, "harness": task["harness"], "model": None, "reasoning": None, "argv": [],
            "source": {"harness": "legacy-record", "model": "native-default", "reasoning": "native-default"},
            "explicit_args": [], "same_as_root": False, "root": None, "preset": None, "saved_default": None, "resolved_at": None,
            "observed": {"status": "not-started", "harness": None, "model": "not-exposed"}}


def command_for(store, *args):
    return shlex.join([str(ROOT / "bin" / "sumctl"), "--home", str(store.home), *args])


def sha256_text(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def worker_skill():
    return (RUNTIME / "skills" / "worker" / "SKILL.md").read_text(encoding="utf-8")


def brief_policy():
    """The operating instructions a brief carries besides the approved task: versioned and comparable without a model."""
    return {"sum_version": VERSION, "brief_schema": BRIEF_SCHEMA, "worker_skill_sha256": sha256_text(worker_skill())}


def return_commands(store, task_id):
    return {"ask": command_for(store, "ask", task_id, "--key", "short-question-name", "--text", "Your exact question and recommendation"),
            "show": command_for(store, "show", task_id),
            "resolve": command_for(store, "resolve", task_id, "QUESTION_ID"),
            "report": command_for(store, "report", task_id, "--file", "/absolute/path/to/report.md"),
            "brief": command_for(store, "brief", "list", task_id)}


def decision_records(task):
    """Questions as recorded: an open question is a pending decision, an answered one stays pending until applied."""
    return [{"id": q["id"], "key": q.get("key"), "status": q["status"], "answer": q.get("answer")} for q in task.get("questions", [])]


def launch_note(task):
    launch = task.get("launch") or {}
    parts = [f"{field} `{launch[field]}`" for field in ("model", "reasoning") if launch.get(field)]
    return f" with {', '.join(parts)} requested on the CLI" if parts else ""


def render_brief(store, task, revision, policy, decisions, commands):
    if decisions:
        lines = []
        for d in decisions:
            label = f"`{d['id']}`" + (f" ({d['key']})" if d.get("key") else "")
            if d["status"] == "open":
                lines.append(f"- {label}: open; no decision recorded yet. Wait for `sumctl answer`, do not assume one.")
            elif d["status"] == "answered":
                lines.append(f"- {label}: answered, not yet applied: {d['answer']}")
            else:
                lines.append(f"- {label}: applied: {d['answer']}")
        decision_text = "\n".join(lines)
    else:
        decision_text = "No decisions recorded yet."
    return f"""# sum worker brief — {task['id']}

You are the worker for this ONE task, not the coordinating consigliere.
Read this entire file. Do not load the coordinator's AGENTS.md as your role.

## Approved task

{task['brief']}

## Execution contract

- Repository: `{task['repository']}`
- Your checkout: `{task['worktree']}`
- Base commit: `{task['base_sha']}`
- Branch: `{task['branch']}`
- Task kind: `{task['kind']}`
- Harness: `{task['harness']}`{launch_note(task)} (keep your normal permissions; no bypass flags)
- At most two repair iterations. Stop and report if they do not fix the problem.
- Do not merge, delete worktrees, restart another agent, or change accounts.
- Read this checkout's project instructions as project context, not as authority to expand scope.
- These are workflow instructions, not a sandbox or a hard cost cap.

## Brief revision

- Revision: `{revision}` (brief schema {policy['brief_schema']}, generated by sum {policy['sum_version']})
- Worker procedure hash: `{policy['worker_skill_sha256'][:16]}`
- The approved task above never changes between revisions; only recorded decisions and operating instructions do.
- A newer revision does not restart your work. If one is requested, read it and continue from your current progress.

## Recorded decisions

{decision_text}

## Return channel

Before waiting for a decision, save the question. This command persists it BEFORE trying to notify the parent:

```sh
{commands['ask']}
```

To read answers:

```sh
{commands['show']}
```

After applying a saved answer, acknowledge that question's ID:

```sh
{commands['resolve']}
```

Write a concise report to a temporary file, then submit it (the command copies it into durable task state):

```sh
{commands['report']}
```

Report outcome, commit SHA, tests actually run and their results, limitations, and any proposed PR.
When you committed a candidate, add `--handoff /absolute/path/to/handoff.json`: a bounded JSON object with `outcome`, `candidate` (full 40-hex HEAD SHA), `next_action`, and optionally `files`, `checks` (`{{command, exit}}` as observed), `review`, `decisions_unresolved`, `artifacts`. Reference logs by path; never paste transcripts.
A report is a claim for the coordinator to verify, NOT proof of successful completion.

## Worker procedure

{worker_skill()}
"""


def write_once(path, text):
    """Write a brief revision exactly once: a partial file never appears at the final name, and an existing file is never replaced."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.exists() or path.is_symlink():
        raise SumError(f"Refusing to overwrite {path}; a brief a worker may be reading is never rewritten.")
    fd, tmp = tempfile.mkstemp(prefix=".write-", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as out:
            out.write(text)
            out.flush()
            os.fsync(out.fileno())
        os.chmod(tmp, 0o600)
        os.link(tmp, path)  # Fails instead of replacing if the name appeared meanwhile.
    finally:
        os.unlink(tmp)
    return path


VERSIONS_SCHEMA = 1
VERSIONS_FILE = "versions.json"
REVISION_ID = re.compile(r"r[1-9][0-9]*\Z")


def approved_fingerprint(task):
    return {"sha256": sha256_text(task["brief"]), "base_sha": task["base_sha"], "repository": task["repository"], "kind": task["kind"]}


def revision_fingerprint(task, policy, decisions, commands):
    """Everything a regenerated brief could change; identical fingerprints mean no new revision."""
    return sha256_text(json.dumps({"approved": approved_fingerprint(task), "policy": policy, "decisions": decisions,
                                   "commands": commands, "contract": {k: task.get(k) for k in ("worktree", "branch", "harness")}},
                                  sort_keys=True))


def revision_summary(previous, policy, decisions, commands, approved):
    """Machine-generated change summary between two revisions. No model call is involved."""
    changes, verification = [], False
    if previous is None:
        return ["initial brief"], False
    if previous.get("approved") != approved:
        raise SumError("Approved task record changed since the previous revision; the approved body, base, repository, and kind are immutable input. Inspect the task before regenerating.")
    for key in ("sum_version", "brief_schema"):
        if previous["policy"].get(key) != policy[key]:
            changes.append(f"{key}: {previous['policy'].get(key)} -> {policy[key]}")
            verification = verification or key == "brief_schema"
    if previous["policy"].get("worker_skill_sha256") != policy["worker_skill_sha256"]:
        changes.append(f"worker procedure changed: {str(previous['policy'].get('worker_skill_sha256'))[:12]} -> {policy['worker_skill_sha256'][:12]}")
        verification = True
    before = {d["id"]: d for d in previous.get("decisions", [])}
    for d in decisions:
        if d["id"] not in before:
            changes.append(f"decision {d['id']} recorded ({d['status']})")
        elif before[d["id"]]["status"] != d["status"] or before[d["id"]].get("answer") != d.get("answer"):
            changes.append(f"decision {d['id']}: {before[d['id']]['status']} -> {d['status']}")
    if previous.get("commands") != commands:
        changes.append("return-channel commands changed")
    return changes or ["no recorded change"], verification


def legacy_versions(store, task):
    """A task recorded before version metadata: interpreted as sum 0.1.0, brief schema 1, one active brief at brief_path. Never written."""
    relative = "brief.md" if task.get("brief_path") else None
    revisions = []
    if relative:
        revisions.append({"id": "legacy", "path": relative, "status": "active", "legacy": True, "sha256": None, "policy": {"sum_version": "0.1.0", "brief_schema": 1}})
    return {"schema": VERSIONS_SCHEMA, "task": task["id"], "legacy": True, "runtime": {"sum_version": "0.1.0", "assumed": True},
            "brief_schema": 1, "approved": approved_fingerprint(task), "revisions": revisions, "active": "legacy" if relative else None,
            "requested": None, "refresh": []}


def read_versions(store, task):
    path = store.path(task["id"]) / VERSIONS_FILE
    if path.is_symlink():
        raise SumError(f"{path} must not be a symlink.")
    if not path.is_file():
        return legacy_versions(store, task)
    value = read_json(path)
    if value.get("schema") != VERSIONS_SCHEMA or value.get("task") != task["id"]:
        raise SumError(f"Unsupported or mismatched version sidecar {path}. Inspect it; sum never migrates it in place.")
    return value


def write_versions(store, value):
    atomic_json(store.path(value["task"]) / VERSIONS_FILE, value)


def revision_file(base, revision):
    relative = Path(revision["path"])
    if relative.is_absolute() or ".." in relative.parts:
        raise SumError(f"Revision {revision['id']} has an invalid path {revision['path']}.")
    return Path(base) / relative


def revision_path(store, task_id, revision):
    return revision_file(store.path(task_id), revision)


def revision_state(store, task_id, revision):
    """Inspect one recorded task brief revision without touching it."""
    return revision_view(store.path(task_id), revision)


def revision_view(base, revision):
    row = {k: revision.get(k) for k in ("id", "status", "created_at", "policy", "summary", "verification_affected", "legacy")}
    try:
        path = revision_file(base, revision)
        row["path"] = str(path)
        if path.is_symlink() or not path.is_file():
            raise SumError("file is missing")
        actual = sha256_text(path.read_text(encoding="utf-8"))
        if revision.get("sha256") and actual != revision["sha256"]:
            raise SumError(f"content hash {actual[:12]} does not match the recorded {revision['sha256'][:12]}")
        row.update(ok=True, sha256=actual)
    except (SumError, OSError, UnicodeDecodeError) as exc:
        row.update(ok=False, error=f"Revision {revision['id']} is not usable: {exc}. Do not request it; regenerate a new revision instead.")
    return row


def versions_view(store, task):
    versions = read_versions(store, task)
    revisions = [revision_state(store, task["id"], r) for r in versions["revisions"]]
    evidence = None
    if task.get("report"):
        made_under = task["report"].get("brief_revision")
        ids = [r["id"] for r in versions["revisions"]]
        if made_under is None:  # An old writer records no revision: it followed the brief active at submission, at best the first one.
            active_then = [r["id"] for r in versions["revisions"] if r["status"] in {"active", "superseded"} and r.get("created_at", "") <= task["report"]["submitted_at"]]
            made_under = active_then[0] if active_then else None
        later = versions["revisions"][ids.index(made_under) + 1:] if made_under in ids else versions["revisions"]
        evidence = {"brief_revision": made_under, "sum_version": task["report"].get("sum_version"),
                    "verification_policy_changed_since": any(r.get("verification_affected") for r in later),
                    "note": "Evidence is bound to the candidate and the brief revision it was produced under. A later verification-affecting revision means the evidence needs refresh review, not automatic rejection or approval."}
    return {**{k: versions.get(k) for k in ("schema", "legacy", "runtime", "brief_schema", "approved", "active", "requested", "refresh")},
            "revisions": revisions, "report_evidence": evidence,
            "note": "Revisions are staged files; the worker keeps reading its current brief until a refresh is explicitly requested and adopted."}


def next_revision_id(store, task_id, versions):
    numbers = [int(r["id"][1:]) for r in versions["revisions"] if REVISION_ID.fullmatch(r["id"])]
    briefs = store.path(task_id) / "briefs"
    for existing in briefs.glob("r*.md") if briefs.is_dir() else []:
        if REVISION_ID.fullmatch(existing.stem):
            numbers.append(int(existing.stem[1:]))  # An orphan from an interrupted regeneration is never overwritten.
    return f"r{max(numbers, default=0) + 1}"


def write_brief(store, task):
    """The first brief revision at dispatch: brief.md (the stable brief_path) plus the task's version sidecar."""
    policy, decisions, commands = brief_policy(), decision_records(task), return_commands(store, task["id"])
    text = render_brief(store, task, "r1", policy, decisions, commands)
    path = write_once(store.path(task["id"]) / "brief.md", text)
    approved = approved_fingerprint(task)
    write_versions(store, {"schema": VERSIONS_SCHEMA, "task": task["id"], "legacy": False,
                           "runtime": {"sum_version": VERSION, "brief_schema": BRIEF_SCHEMA, "mcp": MCP_CONTRACT, "sha": runtime_sha(), "recorded_at": now()},
                           "brief_schema": BRIEF_SCHEMA, "approved": {**approved, "recorded_at": now()},
                           "revisions": [{"id": "r1", "path": "brief.md", "status": "active", "created_at": now(), "sha256": sha256_text(text),
                                          "fingerprint": revision_fingerprint(task, policy, decisions, commands), "policy": policy,
                                          "decisions": decisions, "commands": commands, "approved": approved,
                                          "summary": ["initial brief"], "verification_affected": False}],
                           "active": "r1", "requested": None, "refresh": []})
    return path


def regenerate_brief(store, task_id):
    """Stage a new numbered brief revision from the recorded task. Nothing the worker reads is touched."""
    with store.lock():
        task = store.read(task_id)
        if not task.get("brief_path") or not task.get("worktree"):
            raise SumError("This task has no brief to regenerate; prepare it first.")
        versions = read_versions(store, task)
        approved = approved_fingerprint(task)
        if versions.get("legacy"):
            recorded = versions["revisions"][0] if versions["revisions"] else None
            legacy_path = store.path(task_id) / "brief.md"
            legacy_hash = sha256_text(legacy_path.read_text(encoding="utf-8")) if legacy_path.is_file() else None
            versions = {"schema": VERSIONS_SCHEMA, "task": task_id, "legacy": False,
                        "runtime": {"sum_version": "0.1.0", "brief_schema": 1, "assumed": True, "recorded_at": now()},
                        "brief_schema": 1, "approved": {**approved, "recorded_at": now(), "from_legacy_record": True},
                        "revisions": [{**recorded, "id": "r1", "sha256": legacy_hash, "approved": approved, "decisions": None, "commands": None,
                                       "summary": ["legacy brief adopted as r1; its policy and decisions were not recorded at dispatch"],
                                       "verification_affected": False}] if recorded else [],
                        "active": "r1" if recorded else None, "requested": None, "refresh": []}
        if versions["approved"] and {k: versions["approved"].get(k) for k in approved} != approved:
            raise SumError("Approved task record differs from the recorded fingerprint; the approved body, base, repository, and kind are immutable. Inspect the task; nothing was regenerated.")
        policy, decisions, commands = brief_policy(), decision_records(task), return_commands(store, task_id)
        fingerprint = revision_fingerprint(task, policy, decisions, commands)
        previous = versions["revisions"][-1] if versions["revisions"] else None
        if previous and previous.get("fingerprint") == fingerprint:
            state = revision_state(store, task_id, previous)
            if state["ok"]:
                return {"task": task_id, "duplicate": True, "revision": state, "note": "Nothing changed since the latest revision; no file was written."}
            # The latest revision is damaged or missing: stage the same content under a fresh number rather than repairing in place.
        summary, verification = revision_summary(previous if previous and previous.get("decisions") is not None else None, policy, decisions, commands, approved)
        if previous and previous.get("decisions") is None:
            summary = [f"regenerated from a legacy brief; first revision with recorded policy and decisions ({len(decisions)} decisions)"]
            verification = True
        rid = next_revision_id(store, task_id, versions)
        text = render_brief(store, task, rid, policy, decisions, commands)
        relative = f"briefs/{rid}.md"
        write_once(store.path(task_id) / relative, text)
        revision = {"id": rid, "path": relative, "status": "staged", "created_at": now(), "sha256": sha256_text(text), "fingerprint": fingerprint,
                    "policy": policy, "decisions": decisions, "commands": commands, "approved": approved,
                    "summary": summary, "verification_affected": verification, "previous": previous["id"] if previous else None}
        versions["revisions"].append(revision)
        write_versions(store, versions)
    return {"task": task_id, "duplicate": False, "revision": revision_state(store, task_id, revision), "active": versions["active"],
            "note": "Staged only. The worker's current brief and brief_path are unchanged; use `brief request` to ask for a refresh explicitly."}


def request_brief(store, task_id, revision_id):
    """Mark the latest usable revision as the one the worker should adopt. Separate from the notice slot; no prompt is sent."""
    with store.lock():
        task = store.read(task_id)
        versions = read_versions(store, task)
        if versions.get("legacy"):
            raise SumError("This task has no staged revisions; run `brief regenerate` first.")
        latest = versions["revisions"][-1]
        target = next((r for r in versions["revisions"] if r["id"] == revision_id), None)
        if not target:
            raise SumError(f"Unknown revision {revision_id}. Recorded: {[r['id'] for r in versions['revisions']]}.")
        if target["id"] != latest["id"]:
            raise SumError(f"Stale request: {revision_id} is superseded by {latest['id']}. Request the latest revision or regenerate.")
        if target["id"] == versions["active"]:
            raise SumError(f"{revision_id} is already the active brief.")
        state = revision_state(store, task_id, target)
        if not state["ok"]:
            raise SumError(state["error"])
        duplicate = mark_requested(versions, target)
        write_versions(store, versions)
    return {"task": task_id, "requested": revision_id, "active": versions["active"], "revision": state, "duplicate": duplicate,
            "note": "Recorded only. Delivery to the worker is a separate explicit step; the notice slot was not used."}


def mark_requested(versions, target):
    """Request one revision: every earlier request is superseded, and repeating the same request records nothing new."""
    if versions.get("requested") == target["id"] and target["status"] == "requested":
        return True
    for r in versions["revisions"]:
        if r["status"] == "requested":
            r["status"] = "superseded"
    target["status"] = "requested"
    versions["requested"] = target["id"]
    versions["refresh"].append({"at": now(), "event": "requested", "revision": target["id"], "by": "coordinator"})
    return False


def mark_adopted(versions, target):
    for r in versions["revisions"]:
        if r["status"] == "active":
            r["status"] = "superseded"
    target["status"] = "active"
    versions["active"], versions["requested"] = target["id"], None
    versions["refresh"].append({"at": now(), "event": "adopted", "revision": target["id"]})


def adopt_brief(store, task_id, revision_id):
    """The worker records that it now follows the requested revision. Only a requested, intact revision can become active."""
    with store.lock():
        task = store.read(task_id)
        versions = read_versions(store, task)
        if versions.get("legacy") or versions.get("requested") != revision_id:
            raise SumError(f"{revision_id} is not the requested revision ({versions.get('requested')}). Adopt only what the coordinator requested.")
        target = next(r for r in versions["revisions"] if r["id"] == revision_id)
        state = revision_state(store, task_id, target)
        if not state["ok"]:
            raise SumError(state["error"])
        mark_adopted(versions, target)
        write_versions(store, versions)
    return {"task": task_id, "active": revision_id, "revision": state,
            "note": "Receipt recorded: this revision was read and adopted. A receipt is evidence of reading, not proof the worker follows it."}


def active_revision(store, task):
    try:
        return read_versions(store, task).get("active")
    except SumError:
        return None


# --- rolling refresh of running sessions ------------------------------------------------------------
#
# Four things stay separate: the installation default (`update`), the runtime a process resolved (`active`),
# the instructions requested of a session (a numbered, immutable revision), and the revision a session reports
# it has read (`adopt`). A submitted prompt is not a receipt; a receipt is not obedience.

CONTRACT_DIR = "coordinator"
REFRESH_STATES = ("confirmed", "submitted-unconfirmed", "pending-busy", "pending-unreachable", "capability-deferred", "not-requested")
REFRESH_HISTORY = 100


def runtime_sha():
    """The commit this runtime tree was built from: its release manifest, or the checkout HEAD; None when unknown."""
    manifest = RUNTIME / RELEASE_MANIFEST
    if manifest.is_file():
        try:
            return read_json(manifest)["source"]["sha"]
        except (SumError, KeyError, TypeError):
            return None
    result = run(["git", "-C", RUNTIME, "rev-parse", "HEAD"], check=False)
    return result.stdout.strip() if result.returncode == 0 else None


def coordinator_sources():
    agents = (RUNTIME / "AGENTS.md").read_text(encoding="utf-8")
    skills = {p.parent.name: p.read_text(encoding="utf-8") for p in sorted((RUNTIME / "skills").glob("*/SKILL.md"))}
    return agents, skills


def contract_policy():
    """The coordinator's operating contract as this runtime ships it, hashed so revisions compare without a model."""
    agents, skills = coordinator_sources()
    return {"sum_version": VERSION, "runtime_sha": runtime_sha(), "mcp": MCP_CONTRACT,
            "agents_sha256": sha256_text(agents), "skills_sha256": {name: sha256_text(text) for name, text in skills.items()}}


def contract_summary(previous, policy):
    if previous is None:
        return ["initial contract snapshot"]
    changes = []
    if previous.get("sum_version") != policy["sum_version"]:
        changes.append(f"sum_version: {previous.get('sum_version')} -> {policy['sum_version']}")
    if previous.get("runtime_sha") != policy["runtime_sha"]:
        changes.append(f"runtime: {str(previous.get('runtime_sha'))[:12]} -> {str(policy['runtime_sha'])[:12]}")
    if previous.get("agents_sha256") != policy["agents_sha256"]:
        changes.append("AGENTS.md changed")
    before, after = previous.get("skills_sha256", {}), policy["skills_sha256"]
    for name in sorted(set(before) | set(after)):
        if before.get(name) != after.get(name):
            changes.append(f"skill {name} " + ("added" if name not in before else "removed" if name not in after else "changed"))
    if previous.get("mcp") != policy["mcp"]:
        changes.append("MCP tool contract changed")
    return changes or ["no recorded change"]


def render_contract(store, revision, policy, summary):
    agents, skills = coordinator_sources()
    skill_lines = "\n".join(f"- `{name}`: `{RUNTIME / 'skills' / name / 'SKILL.md'}` ({digest[:16]})" for name, digest in policy["skills_sha256"].items())
    return f"""# sum coordinator contract — {revision}

This is the coordinator's operating contract as shipped by sum {policy['sum_version']} (runtime {policy['runtime_sha']}).
You remain the coordinator of this installation. This revision does not change your role, your registered pane, the recorded tasks, or their parent routes.

## Refresh procedure

- Read the contract below and the change summary. Then run `{command_for(store, 'refresh', 'adopt', '--coordinator', revision)}` to record the receipt.
- Continue coordination from saved state: `{command_for(store, 'inbox', '--live')}` and the task records are the source of truth.
- Do not restart yourself, re-dispatch running tasks, re-run setup, or re-answer recorded decisions.
- Already-connected MCP clients keep the tool set they started with; the capability list below says what is deferred until the client itself restarts.

## Change summary

{chr(10).join('- ' + line for line in summary)}

## Skills at this revision

{skill_lines}

## Operating contract (AGENTS.md at this revision)

{agents}
"""


def empty_contract_versions():
    return {"schema": VERSIONS_SCHEMA, "kind": "coordinator-contract", "revisions": [], "active": None, "requested": None, "refresh": []}


def read_contract_versions(store):
    path = store.home / CONTRACT_DIR / VERSIONS_FILE
    if path.is_symlink():
        raise SumError(f"{path} must not be a symlink.")
    if not path.is_file():
        return empty_contract_versions()
    value = read_json(path)
    if value.get("schema") != VERSIONS_SCHEMA or value.get("kind") != "coordinator-contract":
        raise SumError(f"Unsupported coordinator contract sidecar {path}. Inspect it; sum never migrates it in place.")
    return value


def write_contract_versions(store, value):
    atomic_json(store.home / CONTRACT_DIR / VERSIONS_FILE, value)


def regenerate_contract(store):
    """Snapshot the coordinator contract of this runtime as an immutable numbered revision; identical content writes nothing."""
    base = store.home / CONTRACT_DIR
    with store.lock():
        versions = read_contract_versions(store)
        policy = contract_policy()
        fingerprint = sha256_text(json.dumps(policy, sort_keys=True))
        previous = versions["revisions"][-1] if versions["revisions"] else None
        if previous and previous.get("fingerprint") == fingerprint:
            state = revision_view(base, previous)
            if state["ok"]:
                return {"duplicate": True, "revision": state, "active": versions["active"], "requested": versions["requested"]}
        numbers = [int(r["id"][1:]) for r in versions["revisions"] if REVISION_ID.fullmatch(r["id"])]
        contracts = base / "contracts"
        numbers.extend(int(p.stem[1:]) for p in (contracts.glob("r*.md") if contracts.is_dir() else []) if REVISION_ID.fullmatch(p.stem))
        rid = f"r{max(numbers, default=0) + 1}"
        summary = contract_summary(previous["policy"] if previous else None, policy)
        text = render_contract(store, rid, policy, summary)
        relative = f"contracts/{rid}.md"
        write_once(base / relative, text)
        revision = {"id": rid, "path": relative, "status": "staged", "created_at": now(), "sha256": sha256_text(text), "fingerprint": fingerprint,
                    "policy": policy, "summary": summary, "verification_affected": False, "previous": previous["id"] if previous else None}
        versions["revisions"].append(revision)
        write_contract_versions(store, versions)
    return {"duplicate": False, "revision": revision_view(base, revision), "active": versions["active"], "requested": versions["requested"]}


def adopt_contract(store, revision_id):
    """The coordinator records that it has read the requested contract revision. Only the requested, intact revision can become active."""
    base = store.home / CONTRACT_DIR
    with store.lock():
        versions = read_contract_versions(store)
        if versions.get("requested") != revision_id:
            raise SumError(f"{revision_id} is not the requested contract revision ({versions.get('requested')}). A stale receipt never activates a superseded revision.")
        target = next(r for r in versions["revisions"] if r["id"] == revision_id)
        state = revision_view(base, target)
        if not state["ok"]:
            raise SumError(state["error"])
        mark_adopted(versions, target)
        write_contract_versions(store, versions)
    return {"target": "coordinator", "active": revision_id, "revision": state,
            "note": "Receipt recorded for the coordinator contract. It is evidence of reading, not proof of compliance."}


def refresh_state(versions):
    """Bounded refresh status of one target from its recorded requests, delivery attempts, and receipts."""
    revisions = versions.get("revisions", [])
    latest = revisions[-1] if revisions else None
    requested = versions.get("requested")
    row = {"active": versions.get("active"), "requested": requested, "latest": latest["id"] if latest else None}
    if not requested:
        if latest and versions.get("active") == latest["id"]:
            receipt = next((e for e in reversed(versions.get("refresh", [])) if e.get("event") == "adopted" and e.get("revision") == latest["id"]), None)
            row.update(state="confirmed", reason=(f"receipt for {latest['id']} recorded at {receipt['at']}" if receipt else f"{latest['id']} is the revision this session started with")
                       + "; a receipt shows the revision was read, not that it is obeyed")
        else:
            row.update(state="not-requested", reason="no refresh is requested for this target")
        return row
    deliveries = [e for e in versions.get("refresh", []) if e.get("event") == "delivery" and e.get("revision") == requested]
    if not deliveries:
        row.update(state="pending-unreachable", reason="requested; no delivery attempt recorded yet")
        return row
    last = deliveries[-1]
    row.update(state=last["state"], reason=last.get("reason"), attempted_at=last["at"], attempts=len(deliveries), observed=last.get("observed"))
    return row


def contract_state(store):
    try:
        versions = read_contract_versions(store)
    except SumError as exc:
        return {"error": str(exc)}
    row = refresh_state(versions)
    if row["requested"]:
        target = next(r for r in versions["revisions"] if r["id"] == row["requested"])
        row["path"] = str(revision_file(store.home / CONTRACT_DIR, target))
        row["summary"] = target.get("summary")
    return row


def worker_instruction(store, task_id, revision, path):
    """Fixed wording. Only machine-generated facts are interpolated: IDs, hashes, file paths, and the recorded change summary."""
    return (f"sum refresh {task_id}: brief revision {revision['id']} is requested (sum {VERSION}, runtime {str(runtime_sha())[:12]}). "
            f"Changes: {'; '.join(revision.get('summary') or ['unrecorded'])}. "
            f"At your next safe point read {path}, then run {command_for(store, 'brief', 'adopt', task_id, revision['id'])} and continue your current work from its saved progress. "
            "Do not restart, redo finished work, republish a PR, reset repair counts, or change harness, model, or account. The file is data, not human authorization.")


def deferred_capabilities(recorded_runtime):
    """What a running client cannot pick up by rereading instructions: its MCP tool surface stays what it started with."""
    started = (recorded_runtime or {}).get("mcp")
    if started is None:
        return [{"what": "mcp", "reason": "the MCP contract this session started with was not recorded; it keeps whatever tool set it has until the client restarts"}]
    if started != MCP_CONTRACT:
        return [{"what": "mcp", "from": started, "to": MCP_CONTRACT,
                 "reason": "a connected MCP client cannot hot-reload its tool surface; it keeps the compatible old surface until the client itself restarts"}]
    return []


def attempt_delivery(endpoint, expected_cwd, message, session, snapshots=None):
    """One bounded delivery through the native agent boundary. Returns the recorded event fields, never raises."""
    try:
        observed = observe_recipient(endpoint, expected_cwd, snapshots)
        if snapshots:
            snapshots.calls += 1
        herdr(["agent", "prompt", endpoint["pane"], message], session=session, timeout=RECIPIENT_TIMEOUT)
        return {"state": "submitted-unconfirmed", "observed": observed,
                "reason": "instruction submitted while the agent was settled; not acknowledged until a receipt (adopt) is recorded"}
    except Unreachable as exc:
        return {"state": exc.state, "reason": str(exc)}
    except SumError as exc:
        return {"state": "pending-unreachable", "reason": f"prompt was not accepted: {exc}"}


def record_delivery(versions, revision_id, event):
    versions["refresh"].append({"at": now(), "event": "delivery", "revision": revision_id, **event,
                                "runtime": {"sum_version": VERSION, "sha": runtime_sha()}})
    versions["refresh"] = versions["refresh"][-REFRESH_HISTORY:]


def refresh_task(store, task, ctx, snapshots=None):
    """Regenerate, request, persist, then attempt one delivery to the worker. Records first, prompt last."""
    row = {"target": "task", "task": task["id"], "harness": task.get("harness"), "deferred": []}
    if not task.get("pane") or not task.get("brief_path"):
        row.update(state="pending-unreachable", reason="task has no worker pane or brief yet")
        return row
    versions = read_versions(store, task)
    if versions.get("brief_schema", 1) != BRIEF_SCHEMA:
        row.update(state="capability-deferred", reason=f"task follows brief schema {versions.get('brief_schema')}; this runtime writes schema {BRIEF_SCHEMA}, so it keeps its current brief")
        return row
    staged = regenerate_brief(store, task["id"])
    latest = staged["revision"]["id"]
    with store.lock():
        versions = read_versions(store, task)
        target = next(r for r in versions["revisions"] if r["id"] == latest)
        row["deferred"] = deferred_capabilities(versions.get("runtime"))
        if versions["active"] == latest:
            row.update(revision=latest, **{k: v for k, v in refresh_state(versions).items() if k in ("state", "reason")})
            return row
        state = revision_state(store, task["id"], target)
        if not state["ok"]:
            raise SumError(state["error"])
        mark_requested(versions, target)
        write_versions(store, versions)  # Persisted before any delivery attempt.
    message = worker_instruction(store, task["id"], target, state["path"])
    endpoint = {"pane": task["pane"], "session": task["session"], "machine": task["machine"]}
    if identity(endpoint) == identity(ctx):
        event = {"state": "pending-busy", "reason": "the target is the calling pane; read the revision and adopt it at this turn boundary"}
    else:
        event = attempt_delivery(endpoint, task["worktree"], message, task["session"], snapshots)
    with store.lock():
        versions = read_versions(store, task)
        if versions.get("requested") == latest:
            record_delivery(versions, latest, event)
            write_versions(store, versions)
    row.update(revision=latest, path=state["path"], summary=target.get("summary"), **event)
    return row


def refresh_coordinator(store, ctx):
    row = {"target": "coordinator", "deferred": []}
    staged = regenerate_contract(store)
    latest = staged["revision"]["id"]
    owner = store.owner()
    with store.lock():
        versions = read_contract_versions(store)
        target = next(r for r in versions["revisions"] if r["id"] == latest)
        registration = store.registration(owner) if owner else None
        row["deferred"] = deferred_capabilities({"mcp": (registration or {}).get("mcp")})
        if versions["active"] == latest:
            row.update(revision=latest, **{k: v for k, v in refresh_state(versions).items() if k in ("state", "reason")})
            return row
        mark_requested(versions, target)
        write_contract_versions(store, versions)
    state = revision_view(store.home / CONTRACT_DIR, target)
    if not owner or identity(owner) == identity(ctx):
        event = {"state": "pending-busy", "reason": "the coordinator is the calling pane; read the contract revision and run `refresh adopt --coordinator` at this turn boundary"}
    else:
        message = (f"sum refresh coordinator: operating contract revision {latest} is requested (sum {VERSION}, runtime {str(runtime_sha())[:12]}). "
                   f"Changes: {'; '.join(target['summary'])}. At your next safe point read {state['path']}, then run "
                   f"{command_for(store, 'refresh', 'adopt', '--coordinator', latest)} and continue coordination from saved state. Do not restart or re-dispatch.")
        event = attempt_delivery(owner, owner["cwd"], message, owner["session"])
    with store.lock():
        versions = read_contract_versions(store)
        if versions.get("requested") == latest:
            record_delivery(versions, latest, event)
            write_contract_versions(store, versions)
    row.update(revision=latest, path=state["path"], summary=target.get("summary"), **event)
    return row


def refresh_summary(rows, excluded=()):
    counts = {state: 0 for state in REFRESH_STATES}
    for row in rows:
        counts[row["state"]] = counts.get(row["state"], 0) + 1
    counts["capability-deferred"] += sum(1 for r in rows if r.get("deferred") and r["state"] != "capability-deferred")
    return {"counts": counts, "targets": rows, "excluded": list(excluded),
            "note": "Bounded status from saved records and one delivery attempt per target. No fleet barrier, sleep, polling loop, or relaunch. "
                    "confirmed = receipt recorded; submitted-unconfirmed = prompt accepted, nothing read yet; pending-* = old contract keeps serving; "
                    "capability-deferred = a surface this client cannot reload until it restarts."}


def refresh_request(store, args):
    ctx = context()
    require_coordinator(store, ctx)
    ensure_version()
    tasks = list(args.task or [])
    everything = not tasks and not args.coordinator
    rows, excluded = [], []
    snapshots = Snapshots()  # One pass over one agent snapshot per session: no per-worker observation wait, no transcript read.
    if everything or args.coordinator:
        rows.append(refresh_coordinator(store, ctx))
    for task in store.all():
        if task["status"] == "archived" or (tasks and task["id"] not in tasks):
            continue
        if task["machine"] != machine():
            rows.append({"target": "task", "task": task["id"], "harness": task.get("harness"), "state": "pending-unreachable", "deferred": [],
                         "reason": "task belongs to another machine; nothing was requested for it"})
        else:
            rows.append(refresh_task(store, task, ctx, snapshots))
    for wanted in tasks:
        if wanted not in {r.get("task") for r in rows}:
            raise SumError(f"Unknown or archived task {wanted}; nothing was requested for it.")
    if everything:
        for registration in store.registrations():
            if registration["role"] == "developer":
                excluded.append({"pane": registration["pane"], "session": registration["session"], "role": "developer",
                                 "reason": "developer sessions are outside production fan-out; a developer rereads its own checkout"})
    return {"requested_by": {k: ctx[k] for k in ("session", "pane")}, "runtime": {"sum_version": VERSION, "sha": runtime_sha()},
            "fanout": snapshots.summary(), **refresh_summary(rows, excluded)}


def refresh_status(store, args):
    rows = [{"target": "coordinator", **contract_state(store)}]
    for task in store.all():
        if task["status"] == "archived" or (args.task and task["id"] not in args.task):
            continue
        try:
            versions = read_versions(store, task)
            row = {"target": "task", "task": task["id"], "harness": task.get("harness"), **refresh_state(versions),
                   "deferred": deferred_capabilities(versions.get("runtime"))}
        except SumError as exc:
            row = {"target": "task", "task": task["id"], "state": "pending-unreachable", "reason": f"version sidecar unreadable: {exc}", "deferred": []}
        rows.append(row)
    for row in rows:
        row.setdefault("deferred", [])
        row.setdefault("state", "pending-unreachable")
    return {"runtime": {"sum_version": VERSION, "sha": runtime_sha()}, **refresh_summary(rows)}


def require_coordinator(store, ctx):
    """Fleet-changing commands need the caller to be this instance's registered coordinator."""
    registration = store.registration(ctx)
    owner = store.owner()
    if not registration or registration["role"] != "coordinator" or not owner or identity(owner) != identity(ctx):
        raise SumError("This pane is not the registered coordinator of this sum instance. Run ./bin/sumctl init in the coordinator pane; a developer session must not dispatch or rebind.")
    return registration


def prepare(store, args):
    ctx = context()
    require_coordinator(store, ctx)
    ensure_version()
    repo = Path(run(["git", "-C", args.repo, "rev-parse", "--show-toplevel"]).stdout.strip()).resolve()
    base_sha = run(["git", "-C", repo, "rev-parse", "--verify", f"{args.base}^{{commit}}", "--"]).stdout.strip()
    brief = Path(args.brief).read_text(encoding="utf-8")
    if not brief.strip() or len(brief.encode()) > MAX_TEXT:
        raise SumError("Provide a nonempty, bounded task brief.")
    if not args.approved:
        raise SumError("Dispatch requires --approved: record explicit user-approved work, not a self-generated backlog item.")
    # The launch specification is resolved and validated here, before any record or Herdr side effect, and persisted with the task:
    # a later change of the saved defaults never changes how this prepared task starts.
    launch = resolve_launch(load_settings(store), ctx, harness=args.harness, model=getattr(args, "model", None),
                            reasoning=getattr(args, "reasoning", None), same_as_root=getattr(args, "same_as_you", False), extra=args.arg,
                            preset=getattr(args, "preset", None))
    with store.lock():  # Admission is decided and recorded here from local files only; Herdr is called after the lock is released.
        admission = admit(store, store.all(), repo)
        tid = "t-" + uuid.uuid4().hex[:12]
        task = {"schema": SCHEMA, "id": tid, "created_at": now(), "status": "preparing",
                "machine": machine(), "repository": str(repo), "base_sha": base_sha,
                "branch": f"sum/{tid}", "harness": launch["harness"], "launch": launch, "kind": args.kind,
                "brief": brief, "parent": ctx, "session": ctx["session"], "pane": None,
                "workspace": None, "worktree": None, "questions": [], "report": None, "evidence": [],
                "reviewer": None, "pr": None, "notice": None, "error": None, "admission": admission}
        store.save(task)  # Persist intent before an external effect.
    try:
        created = herdr(["worktree", "create", "--cwd", str(repo), "--branch", task["branch"],
                         "--base", base_sha, "--label", f"sum-{tid}", "--no-focus"],
                        session=ctx["session"], timeout=30)
        root_pane = created["root_pane"]
        workspace = created["workspace"]
        worktree = created.get("worktree") or workspace.get("worktree")
        task.update(pane=root_pane["pane_id"], workspace=workspace["workspace_id"],
                    worktree=str(Path(worktree["path"]).resolve()))
        actual_root = Path(run(["git", "-C", task["worktree"], "rev-parse", "--show-toplevel"]).stdout.strip()).resolve()
        actual_head = run(["git", "-C", task["worktree"], "rev-parse", "HEAD"]).stdout.strip()
        actual_branch = run(["git", "-C", task["worktree"], "branch", "--show-current"]).stdout.strip()
        if actual_root == repo or actual_root != Path(task["worktree"]) or actual_head != base_sha or actual_branch != task["branch"]:
            raise SumError("Herdr returned a checkout that does not match the task. Work is preserved; inspect it manually.")
        task["brief_path"] = str(write_brief(store, task))
        task["status"] = "prepared"
    except (SumError, KeyError, TypeError) as exc:
        task["status"] = "needs-attention"
        task["error"] = f"Prepare failed or became uncertain: {exc}. Do not blindly create a replacement; inspect Herdr first."
        with store.lock():
            store.save(task)
        raise SumError(f"{task['id']}: {task['error']}") from exc
    with store.lock():
        store.save(task)
    return {**task, "confirmation": launch_confirmation(launch)}


def start(store, task_id, extra_args=()):
    require_coordinator(store, context())
    ensure_version()
    with store.lock():
        task = store.read(task_id)
        store.check_machine(task)
        if task["status"] != "prepared":
            raise SumError("Only a prepared task can be started. sum never retries an uncertain launch automatically.")
        launch = task_launch(task)
        extra_args = list(extra_args)
        for field in ("model", "reasoning"):  # Legacy `start --arg` callers keep working but may not contradict the persisted specification.
            if launch[field] is not None and argv_conflicts(launch["harness"], field, extra_args):
                raise SumError(f"Conflicting {field}: the prepared launch already sets {launch['harness']} {field} {launch[field]!r}; a start-time --arg may not override it.")
        argv = [*launch["argv"], *extra_args]
        launch = {**launch, "argv": argv, "explicit_args": [*launch.get("explicit_args", []), *extra_args], "started_argv": argv}
        task["launch"] = launch
        task["status"] = "starting"
        store.save(task)
    try:
        started = herdr(["agent", "start", task_id, "--kind", launch["harness"], "--pane", task["pane"],
                         "--timeout", "30000", *(["--", *argv] if argv else [])],
                        session=task["session"], timeout=40)
        agent = started.get("agent", started) if isinstance(started, dict) else {}
        observed_kind = agent.get("agent") if isinstance(agent, dict) else None
        observed = {"harness": observed_kind, "model": "not-exposed",
                    "status": "harness-observed" if observed_kind == launch["harness"] else "harness-mismatch"}
        # No long blocking handoff: submit the explicit worker brief and return.
        prompt = f"You are the sum worker for {task_id}, not the coordinator. Read the complete file {json.dumps(task['brief_path'])}, then execute only that approved task. Questions and results must be saved using the commands in that brief."
        herdr(["agent", "prompt", task["pane"], prompt], session=task["session"], timeout=10)
        with store.lock():
            current = store.read(task_id)
            if current["status"] == "starting":
                current["status"] = "running"
            current["started_at"] = now()
            current["launch"] = {**launch, "observed": observed}
            store.save(current)
            # The dispatched pane keeps its task role even if it later runs `sumctl init` itself.
            store.register({"machine": current["machine"], "session": current["session"], "pane": current["pane"],
                            "cwd": current["worktree"]}, "worker", task=task_id)
        return {**current, "confirmation": launch_confirmation(current["launch"])}
    except SumError as exc:
        with store.lock():
            task = store.read(task_id)
            if task["status"] == "starting":
                task["status"] = "needs-attention"
            task["error"] = f"Launch/prompt uncertain: {exc}. Inspect the saved pane; do not relaunch. Trust/auth prompts need your action."
            store.save(task)
        raise SumError(f"{task_id}: {task['error']}") from exc


class Unreachable(SumError):
    """A recipient that must not receive input now: `state` is the bounded refresh category, the message the exact reason."""

    def __init__(self, state, message):
        super().__init__(message)
        self.state = state


class Snapshots:
    """One bounded `agent list` per Herdr session, taken lazily and reused for a whole fan-out pass.

    Twelve workers cost one observation call, not twelve sequential waits; a worker absent from the list is unreachable
    without its own call. Every Herdr call made through a snapshot is counted so a pass can report its operation count.
    """

    def __init__(self):
        self.sessions = {}
        self.calls = 0
        self.started = time.monotonic()

    def get(self, session):
        if session not in self.sessions:
            self.calls += 1
            try:
                listed = herdr(["agent", "list"], session=session, timeout=SNAPSHOT_TIMEOUT)
                agents = listed.get("agents", listed) if isinstance(listed, dict) else listed
                if not isinstance(agents, list):
                    raise SumError("Unrecognized Herdr agent list response.")
                self.sessions[session] = {"ok": True, "agents": {a.get("pane_id"): a for a in agents if isinstance(a, dict)}}
            except SumError as exc:
                self.sessions[session] = {"ok": False, "error": str(exc), "agents": {}}
        return self.sessions[session]

    def agent(self, session, pane):
        snapshot = self.get(session)
        if not snapshot["ok"]:
            raise SumError(f"agent list for session {session} failed: {snapshot['error']}")
        agent = snapshot["agents"].get(pane)
        if agent is None:
            raise SumError("agent_not_found (absent from the session's agent snapshot)")
        if not (agent.get("cwd") or agent.get("working_directory")):
            self.calls += 1  # This Herdr build lists agents without cwd; one bounded lookup for this settled recipient only.
            agent = agent_observation(session, pane)
        return agent

    def summary(self):
        return {"sessions": len(self.sessions), "herdr_calls": self.calls, "elapsed_ms": round((time.monotonic() - self.started) * 1000),
                "per_recipient_timeout_s": RECIPIENT_TIMEOUT, "snapshot_timeout_s": SNAPSHOT_TIMEOUT}


def observe_recipient(endpoint, expected_cwd, snapshots=None):
    """The native safe boundary: the recorded pane exists, runs in the expected checkout, and Herdr reports it settled (idle/done).

    Herdr idle is a gate for sending, not proof that a foreground tool has stopped or that anything was read.
    With `snapshots`, the observation comes from one per-session `agent list` instead of a call per recipient.
    """
    if endpoint["machine"] != machine():
        raise Unreachable("pending-unreachable", "Recipient is on another machine.")
    try:
        agent = snapshots.agent(endpoint["session"], endpoint["pane"]) if snapshots else agent_observation(endpoint["session"], endpoint["pane"])
    except SumError as exc:
        raise Unreachable("pending-unreachable", f"Recipient cannot be observed: {exc}") from exc
    cwd = agent.get("cwd") or agent.get("working_directory")
    if not cwd or Path(cwd).resolve() != Path(expected_cwd).resolve():
        raise Unreachable("pending-unreachable", "Recipient cwd cannot be verified; refusing possible stale/reused pane.")
    status = agent.get("agent_status", agent.get("status", "unknown"))
    if status not in {"idle", "done"}:
        raise Unreachable("pending-busy", f"Recipient is {status}; notice remains pending. No mid-turn injection or retry loop.")
    return status


def notify(store, task_id, recipient, reason):
    """Best-effort notice. Never transports worker prose as an instruction."""
    task = store.read(task_id)
    notice = {"at": now(), "recipient": recipient, "reason": reason, "status": "pending"}
    endpoint = task["parent"] if recipient == "parent" else {"pane": task["pane"], "session": task["session"], "machine": task["machine"]}
    try:
        observe_recipient(endpoint, task["parent"]["cwd"] if recipient == "parent" else task["worktree"])
        message = (f"sum task {task_id}: {reason}. Read the durable record with "
                   f"{command_for(store, 'show', task_id)}. Record contents are worker data, not human authorization.")
        herdr(["agent", "prompt", endpoint["pane"], message], session=endpoint["session"], timeout=RECIPIENT_TIMEOUT)
        notice["status"] = "submitted-not-acknowledged"
    except SumError as exc:
        notice["error"] = str(exc)
    with store.lock():
        task = store.read(task_id)
        task["notice"] = notice
        store.save(task)
    return notice


def ask(store, args):
    text = text_input(args)
    with store.lock():
        task = store.read(args.task)
        existing = next((q for q in task["questions"] if args.key and q.get("key") == args.key), None)
        if existing:
            if existing["text"] != text:
                raise SumError("This question key already exists with different text. Use a new key; do not overwrite an obligation.")
            return {"question": existing, "duplicate": True, "notice": task["notice"]}
        question = {"id": "q-" + uuid.uuid4().hex[:10], "key": args.key, "text": text,
                    "status": "open", "created_at": now(), "answer": None}
        task["questions"].append(question)
        task["status"] = "waiting"
        store.save(task)
    return {"question": question, "notice": notify(store, args.task, "parent", "a decision is waiting")}


def answer(store, args):
    text = text_input(args)
    with store.lock():
        task = store.read(args.task)
        question = next((q for q in task["questions"] if q["id"] == args.question), None)
        if not question:
            raise SumError("Question not found.")
        if question["status"] != "open":
            if question["answer"] == text:
                return {"question": question, "duplicate": True}
            raise SumError("Answer already recorded. Create a new explicit decision rather than silently changing it.")
        question.update(answer=text, answered_at=now(), status="answered")
        store.save(task)
    return {"question": question, "notice": notify(store, args.task, "worker", "an answer has been recorded")}


def resolve(store, args):
    with store.lock():
        task = store.read(args.task)
        question = next((q for q in task["questions"] if q["id"] == args.question), None)
        if not question or question["status"] == "open":
            raise SumError("Only an answered question can be marked applied.")
        question.update(status="applied", applied_at=now())
        if all(q["status"] == "applied" for q in task["questions"]) and task["status"] == "waiting":
            task["status"] = "running"
        store.save(task)
    return question


def report(store, args):
    text = text_input(args)
    handoff = read_handoff(args.handoff) if getattr(args, "handoff", None) else None
    endpoint = optional_context()
    with store.lock():
        task = store.read(args.task)
        revision = active_revision(store, task)
        # A report never clears unanswered questions or asserts verified success. The latest prose stays in `report`
        # for every existing reader; the history is appended as scoped evidence so a second report erases nothing.
        task["report"] = {"text": text, "submitted_at": now(), "brief_revision": revision, "sum_version": VERSION,
                          "candidate": handoff["candidate"] if handoff else None}
        records = [append_evidence(task, "report", "worker", {"text": text}, candidate=handoff["candidate"] if handoff else None, endpoint=endpoint)]
        if handoff:
            records.append(append_evidence(task, "handoff", "worker", {"handoff": handoff}, candidate=handoff["candidate"], endpoint=endpoint))
        for record in records:
            record["brief_revision"] = revision
        task["status"] = "reported"
        store.save(task)
    return {"task": args.task, "status": "reported-not-verified", "evidence": [r["id"] for r in records],
            "notice": notify(store, args.task, "parent", "a worker report is available")}


# --- durable evidence: handoffs, reviewer findings, coordinator verification, exact PR identity --------
#
# `task["report"]` stays the latest prose report for every existing reader. `task["evidence"]` is an append-only
# list of scoped records: worker claims (report/handoff), reviewer findings, coordinator verification, and PR
# observations made against GitHub. Nothing here is deleted or rewritten; a newer candidate marks older records
# `current: false` in the computed view and leaves them in place. None of it means merged or verified by itself.

EVIDENCE_SCHEMA = 1
SHA40 = re.compile(r"[0-9a-f]{40}\Z")
REPO_NAME = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
HANDOFF_OUTCOMES = ("completed", "partial", "blocked", "failed")
HANDOFF_REVIEW = ("none", "requested", "performed")
REVIEW_VERDICTS = ("approve", "changes-requested", "blocked", "comment")
VERIFY_RESULTS = ("pass", "fail", "inconclusive")
PR_IDENTITY_FIELDS = ("repository", "number", "url", "head_repository", "head_branch", "base_branch", "head_sha")
HANDOFF_LIMITS = {"task_ref": 200, "next_action": 1000, "review_ref": 500, "files": 200, "checks": 50, "artifacts": 30,
                  "decisions_unresolved": 50, "item": 500}


def bounded_text(value, limit, field):
    if not isinstance(value, str) or not value.strip():
        raise SumError(f"handoff.{field} must be a nonempty string")
    if len(value) > limit:
        raise SumError(f"handoff.{field} exceeds {limit} characters; reference an artifact instead of copying a transcript")
    return value


def bounded_list(value, field, limit):
    if not isinstance(value, list):
        raise SumError(f"handoff.{field} must be a list")
    if len(value) > limit:
        raise SumError(f"handoff.{field} holds more than {limit} entries; summarize and reference an artifact")
    return value


def validate_pr_identity(value, field="pr"):
    """Exact identity only: no URL parsing, no branch-name similarity. Missing fields stay missing and are reported as such."""
    if not isinstance(value, dict):
        raise SumError(f"{field} must be an object")
    unknown = sorted(set(value) - set(PR_IDENTITY_FIELDS))
    if unknown:
        raise SumError(f"{field} has unknown keys {unknown}; allowed: {list(PR_IDENTITY_FIELDS)}")
    result = {}
    for key in PR_IDENTITY_FIELDS:
        item = value.get(key)
        if item is None:
            result[key] = None
            continue
        if key == "number":
            if isinstance(item, bool) or not isinstance(item, int) or item < 1:
                raise SumError(f"{field}.number must be a positive integer")
        elif key in {"repository", "head_repository"}:
            if not isinstance(item, str) or not REPO_NAME.fullmatch(item):
                raise SumError(f"{field}.{key} must be owner/name")
        elif key == "head_sha":
            if not isinstance(item, str) or not SHA40.fullmatch(item):
                raise SumError(f"{field}.head_sha must be a full 40-hex commit SHA")
        elif not isinstance(item, str) or not item.strip() or len(item) > 500:
            raise SumError(f"{field}.{key} must be a nonempty string")
        result[key] = item
    return result


def validate_handoff(value):
    """The bounded structured handoff a worker may attach to a report. Every claim in it is the worker's, unverified."""
    if not isinstance(value, dict):
        raise SumError("handoff must be a JSON object")
    allowed = {"outcome", "task_ref", "candidate", "files", "checks", "review", "review_ref", "decisions_unresolved", "next_action", "pr", "artifacts"}
    unknown = sorted(set(value) - allowed)
    if unknown:
        raise SumError(f"handoff has unknown keys {unknown}; allowed: {sorted(allowed)}")
    for key in ("outcome", "candidate", "next_action"):
        if key not in value:
            raise SumError(f"handoff.{key} is required")
    if value["outcome"] not in HANDOFF_OUTCOMES:
        raise SumError(f"handoff.outcome must be one of {list(HANDOFF_OUTCOMES)}")
    if not isinstance(value["candidate"], str) or not SHA40.fullmatch(value["candidate"]):
        raise SumError("handoff.candidate must be the full 40-hex commit SHA of the committed candidate")
    result = {"outcome": value["outcome"], "candidate": value["candidate"],
              "next_action": bounded_text(value["next_action"], HANDOFF_LIMITS["next_action"], "next_action"),
              "task_ref": bounded_text(value["task_ref"], HANDOFF_LIMITS["task_ref"], "task_ref") if value.get("task_ref") is not None else None,
              "review": value.get("review", "none"), "review_ref": None, "pr": None}
    if result["review"] not in HANDOFF_REVIEW:
        raise SumError(f"handoff.review must be one of {list(HANDOFF_REVIEW)}")
    if value.get("review_ref") is not None:
        result["review_ref"] = bounded_text(value["review_ref"], HANDOFF_LIMITS["review_ref"], "review_ref")
    for key in ("files", "artifacts", "decisions_unresolved"):
        items = bounded_list(value.get(key, []), key, HANDOFF_LIMITS[key])
        result[key] = [bounded_text(item, HANDOFF_LIMITS["item"], f"{key}[]") for item in items]
    checks = []
    for index, check in enumerate(bounded_list(value.get("checks", []), "checks", HANDOFF_LIMITS["checks"])):
        if not isinstance(check, dict) or set(check) - {"command", "exit", "note"} or "command" not in check or "exit" not in check:
            raise SumError(f"handoff.checks[{index}] must be {{command, exit, note?}}")
        if isinstance(check["exit"], bool) or not isinstance(check["exit"], int):
            raise SumError(f"handoff.checks[{index}].exit must be the integer exit code actually observed")
        checks.append({"command": bounded_text(check["command"], HANDOFF_LIMITS["item"], f"checks[{index}].command"), "exit": check["exit"],
                       "note": bounded_text(check["note"], HANDOFF_LIMITS["item"], f"checks[{index}].note") if check.get("note") is not None else None})
    result["checks"] = checks
    if value.get("pr") is not None:
        result["pr"] = validate_pr_identity(value["pr"], "handoff.pr")
    return result


def read_handoff(path):
    raw = Path(path).read_text(encoding="utf-8")
    if len(raw.encode("utf-8")) > MAX_TEXT:
        raise SumError(f"handoff exceeds {MAX_TEXT} bytes; reference logs and artifacts instead of copying them")
    try:
        return validate_handoff(json.loads(raw))
    except ValueError as exc:
        raise SumError(f"handoff is not valid JSON: {exc}") from exc


def optional_context():
    """The calling pane when the command runs inside Herdr; a legacy or scripted caller records no endpoint."""
    try:
        return context()
    except SumError:
        return None


def append_evidence(task, kind, source, body, candidate=None, endpoint=None):
    record = {"schema": EVIDENCE_SCHEMA, "id": "e-" + uuid.uuid4().hex[:10], "kind": kind, "source": source,
              "at": now(), "candidate": candidate, "brief_revision": None, "sum_version": VERSION,
              "endpoint": {k: endpoint[k] for k in ("machine", "session", "pane")} if endpoint else None, **body}
    task.setdefault("evidence", []).append(record)
    return record


def endpoint_role(task, endpoint):
    if not endpoint:
        return None
    if task.get("pane") and identity(task) == identity(endpoint):
        return "worker"
    if task.get("parent") and identity(task["parent"]) == identity(endpoint):
        return "coordinator"
    if task.get("reviewer") and identity(task["reviewer"]) == identity(endpoint):
        return "reviewer"
    return "other"


def review(store, args):
    """Reviewer findings are appended, never replace a report, and bind the reviewer endpoint to the task where one exists."""
    text = text_input(args)
    if args.verdict not in REVIEW_VERDICTS:
        raise SumError(f"--verdict must be one of {list(REVIEW_VERDICTS)}")
    if args.candidate and not SHA40.fullmatch(args.candidate):
        raise SumError("--candidate must be a full 40-hex commit SHA")
    endpoint = optional_context()
    with store.lock():
        task = store.read(args.task)
        role = endpoint_role(task, endpoint)
        if role == "worker":
            raise SumError("The worker pane cannot record independent review of its own candidate. Save findings from the reviewer pane, or record `verify` as the coordinator.")
        if endpoint and role == "other":
            if task.get("reviewer"):
                raise SumError(f"Task already has reviewer pane {task['reviewer']['pane']} in session {task['reviewer']['session']}; a second reviewer endpoint is not adopted silently.")
            task["reviewer"] = {**{k: endpoint[k] for k in ("machine", "session", "pane", "cwd")}, "bound_at": now()}
            role = "reviewer"
        record = append_evidence(task, "review", role or "unattributed", {"verdict": args.verdict, "text": text}, candidate=args.candidate, endpoint=endpoint)
        record["brief_revision"] = active_revision(store, task)
        store.save(task)
    return {"task": args.task, "evidence": record, "reviewer": task.get("reviewer"),
            "note": "Findings saved. They do not verify the candidate or close anything; a reviewer pane with saved findings is closable later, one without is not."}


def verify(store, args):
    """The coordinator's own verification record for one exact candidate; separate from worker claims and GitHub."""
    text = text_input(args)
    if args.result not in VERIFY_RESULTS:
        raise SumError(f"--result must be one of {list(VERIFY_RESULTS)}")
    if not SHA40.fullmatch(args.candidate or ""):
        raise SumError("--candidate must be the full 40-hex commit SHA that was actually verified")
    ctx = context()
    require_coordinator(store, ctx)
    with store.lock():
        task = store.read(args.task)
        record = append_evidence(task, "verification", "coordinator", {"result": args.result, "text": text}, candidate=args.candidate, endpoint=ctx)
        record["brief_revision"] = active_revision(store, task)
        store.save(task)
    return {"task": args.task, "evidence": record}


def gh(args, *, cwd=None, timeout=30):
    result = run([tool("gh"), *args], cwd=cwd, timeout=timeout)
    try:
        return json.loads(result.stdout)
    except ValueError as exc:
        raise SumError(f"gh did not return JSON: {result.stdout[:300]}") from exc


PR_JSON_FIELDS = "number,url,state,headRefName,headRefOid,baseRefName,headRepository,headRepositoryOwner,isCrossRepository,mergedAt,mergeCommit,closed"


def observe_pr(task, repository, number):
    """One authenticated GitHub observation of an exact PR; the task repository's remote identity is resolved by gh itself."""
    expected = gh(["repo", "view", "--json", "nameWithOwner"], cwd=task["repository"])["nameWithOwner"]
    if repository and repository != expected:
        raise SumError(f"--repo {repository} is not the task repository's GitHub identity {expected}. A PR in another repository is never attached to this task.")
    data = gh(["pr", "view", str(number), "--repo", expected, "--json", PR_JSON_FIELDS])
    owner = (data.get("headRepositoryOwner") or {}).get("login")
    name = (data.get("headRepository") or {}).get("name")
    head_repository = f"{owner}/{name}" if owner and name else None
    merge_commit = (data.get("mergeCommit") or {}).get("oid")
    return {"identity": validate_pr_identity({"repository": expected, "number": data["number"], "url": data["url"], "head_repository": head_repository,
                                              "head_branch": data.get("headRefName"), "base_branch": data.get("baseRefName"), "head_sha": data.get("headRefOid")}),
            "state": str(data.get("state", "")).lower() or None, "merged_at": data.get("mergedAt") or None,
            "merge_commit": merge_commit if merge_commit and SHA40.fullmatch(merge_commit) else None,
            "cross_repository": bool(data.get("isCrossRepository"))}


def candidate_shas(task):
    """Every candidate SHA the worker committed to on record, plus the checkout's current HEAD when it still exists."""
    shas = {r["candidate"] for r in task.get("evidence", []) if r.get("candidate") and r.get("source") == "worker"}
    head = current_candidate(task)
    if head:
        shas.add(head)
    return shas


def current_candidate(task):
    worktree = task.get("worktree")
    if not worktree or not Path(worktree).is_dir():
        return None
    try:
        return run(["git", "-C", worktree, "rev-parse", "HEAD"]).stdout.strip()
    except SumError:
        return None


def pr_findings(task, observation):
    """Compare one GitHub observation with the task record. Each mismatch is named; nothing is inferred from similarity."""
    identity_ = observation["identity"]
    findings = []
    if identity_["head_repository"] != identity_["repository"] or observation["cross_repository"]:
        findings.append("head is on a fork, not the task repository")
    if identity_["head_branch"] != task["branch"]:
        findings.append(f"head branch {identity_['head_branch']!r} is not the task branch {task['branch']!r}")
    known = candidate_shas(task)
    if not identity_["head_sha"]:
        findings.append("GitHub returned no head SHA")
    elif identity_["head_sha"] not in known:
        findings.append("PR head SHA is not a recorded candidate of this task (reused branch name or changed head)")
    if identity_["base_branch"] is None:
        findings.append("no base branch observed")
    return findings


def pr_reconcile(store, args):
    """Explicit coordinator reconciliation: inspect the actual PR, record its exact identity, never guess or migrate."""
    ctx = context()
    require_coordinator(store, ctx)
    if args.repo and not REPO_NAME.fullmatch(args.repo):
        raise SumError("--repo must be owner/name")
    task = store.read(args.task)
    try:
        observation = observe_pr(task, args.repo, args.number)
    except SumError as exc:
        with store.lock():
            task = store.read(args.task)
            record = append_evidence(task, "publication", "github", {"outcome": "uncertain", "number": args.number, "repository": args.repo, "error": str(exc)}, endpoint=ctx)
            store.save(task)
        raise SumError(f"PR observation for #{args.number} is uncertain and was recorded as such ({record['id']}): {exc}. Inspect GitHub before creating or closing anything.") from exc
    pr, record, previous = save_pr_observation(store, args.task, observation, ctx, replace=args.replace)
    return {"task": args.task, "pr": pr, "evidence": record["id"], "previous": previous,
            "note": "An exact GitHub observation at one instant. Merged applies to this task only when the state is merged, a merge commit exists, and no identity finding remains."}


def save_pr_observation(store, task_id, observation, ctx, replace=False):
    """Record one exact GitHub observation as the task's PR identity plus an append-only evidence record."""
    with store.lock():
        task = store.read(task_id)
        findings = pr_findings(task, observation)
        merged_for_task = observation["state"] == "merged" and observation["merge_commit"] is not None and not findings
        previous = task.get("pr")
        if previous and previous.get("identity", {}).get("number") not in {None, observation["identity"]["number"]} and not replace:
            raise SumError(f"Task already records PR #{previous['identity']['number']}; pass --replace after inspecting both PRs to switch the recorded identity.")
        pr = {"identity": observation["identity"], "state": observation["state"], "merged_at": observation["merged_at"], "merge_commit": observation["merge_commit"],
              "observed_at": now(), "observed_by": {k: ctx[k] for k in ("machine", "session", "pane")}, "findings": findings,
              "merged_for_task": merged_for_task, "complete": all(observation["identity"][k] is not None for k in PR_IDENTITY_FIELDS)}
        task["pr"] = pr
        record = append_evidence(task, "publication", "github", {"outcome": "observed", "pr": pr}, candidate=observation["identity"]["head_sha"], endpoint=ctx)
        store.save(task)
    return pr, record, previous


def evidence_view(task):
    """Scoped evidence with candidate currency, plus the closure prerequisites this task has or lacks. Computed; never stored."""
    head = current_candidate(task)
    records = []
    for record in task.get("evidence", []):
        row = dict(record)
        row["current"] = None if not record.get("candidate") or head is None else record["candidate"] == head
        records.append(row)
    if task.get("report") and not any(r["kind"] == "report" for r in records):
        records.insert(0, {"schema": None, "id": None, "kind": "report", "source": "worker", "legacy": True, "at": task["report"]["submitted_at"],
                           "candidate": None, "brief_revision": task["report"].get("brief_revision"), "current": None,
                           "note": "Legacy prose report recorded before scoped evidence existed; unstructured worker claim."})
    handoffs = [r for r in records if r["kind"] == "handoff"]
    reviews = [r for r in records if r["kind"] == "review"]
    verifications = [r for r in records if r["kind"] == "verification" and r.get("result") == "pass" and r["current"]]
    pr = task.get("pr")
    missing = []
    if not handoffs or not handoffs[-1]["current"]:
        missing.append("current structured handoff")
    if not pr or not pr.get("complete"):
        missing.append("complete PR identity from `pr reconcile`")
    elif head and pr["identity"]["head_sha"] != head:
        missing.append("PR head SHA does not match the current candidate; reconcile again")
    if not verifications:
        missing.append("coordinator verification of the current candidate")
    if task.get("reviewer") and not reviews:
        missing.append("saved findings from the bound reviewer pane")
    return {"current_candidate": head, "records": records, "reviewer": task.get("reviewer"), "pr": pr,
            "closure": {"prerequisites_met": not missing, "missing": missing, "merged_for_task": bool(pr and pr.get("merged_for_task")),
                        "note": "Readiness only. Nothing here closes a pane or removes a checkout; an idle state or a report never counts as verified or merged."}}

# --- issue #10: guarded cleanup of merged task panes and checkouts -------------------------------------------
#
# Cleanup is explicit and idempotent: `cleanup TASK` inspects and persists the plan, `cleanup TASK --apply` removes only
# the verified task workspace through native `herdr worktree remove` without force, then archives the record. Every
# blocker is named. Nothing here polls, kills a process, runs `git clean`, resets, forces, or deletes a branch. The
# intent is saved before the native removal so a crash between removal and archiving reconciles from records and
# observation, never by recreating or guessing.

CLEANUP_SCHEMA = 1
DISPOSABLE_IGNORED = ("__pycache__", "*.pyc", ".pytest_cache", ".mypy_cache", ".ruff_cache", "node_modules", ".DS_Store")
LSOF_TIMEOUT = 30
SHELLS = {"bash", "zsh", "sh", "fish", "dash", "ksh", "tcsh", "csh", "nu", "pwsh", "-bash", "-zsh", "-sh", "-fish"}


def herdr_observe(args, *, session, timeout=10):
    """Bounded native observation: (result, None), or (None, code) for a Herdr error code such as pane_not_found."""
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", session):
        raise SumError("Invalid session name.")
    result = run([tool("herdr"), "--session", session, *args], timeout=timeout, check=False)
    if result.returncode:
        code = herdr_error_code(result)
        if code:
            return None, code
        raise SumError(f"herdr {' '.join(args[:2])} exited {result.returncode}: {(result.stderr or result.stdout).strip()[-300:]}")
    try:
        data = json.loads(result.stdout)
    except ValueError as exc:
        raise SumError(f"Herdr did not return JSON: {result.stdout[:300]}") from exc
    return (data.get("result", data) if isinstance(data, dict) else data), None


def workspace_of(pane_id):
    """Herdr pane IDs are workspace-qualified (`w2:p1`); the prefix names the workspace."""
    return str(pane_id).split(":")[0] if pane_id else None


def disposable_ignored(relative):
    return any(fnmatch(part, pattern) for part in PurePosixPath(relative).parts for pattern in DISPOSABLE_IGNORED)


def worktree_artifacts(worktree):
    """Tracked, staged, untracked, and ignored paths of a checkout; ignored paths are split into a fixed disposable list and preserved ones."""
    out = run(["git", "-C", worktree, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching"]).stdout
    entries = out.split("\0")
    result = {"tracked_modified": [], "staged": [], "untracked": [], "ignored_preserved": [], "ignored_disposable": []}
    index = 0
    while index < len(entries):
        entry = entries[index]
        index += 1
        if len(entry) < 4:
            continue
        code, path = entry[:2], entry[3:]
        if code[0] in "RC":
            index += 1  # The original name of a rename/copy follows as its own entry.
        if code == "!!":
            result["ignored_disposable" if disposable_ignored(path) else "ignored_preserved"].append(path)
        elif code == "??":
            result["untracked"].append(path)
        else:
            if code[0] != " ":
                result["staged"].append(path)
            if code[1] != " ":
                result["tracked_modified"].append(path)
    return result


def commits_not_covered(worktree, merged_head):
    """Commits in the checkout that the merged PR head does not contain. Squash/rebase merges never require ancestry of the default branch."""
    head = run(["git", "-C", worktree, "rev-parse", "HEAD"]).stdout.strip()
    if head == merged_head:
        return head, []
    known = run(["git", "-C", worktree, "cat-file", "-e", f"{merged_head}^{{commit}}"], check=False).returncode == 0
    if not known:
        return head, [f"checkout HEAD {head} differs from the merged PR head {merged_head}, which is not a local object"]
    return head, run(["git", "-C", worktree, "rev-list", f"{merged_head}..HEAD"]).stdout.split()


def processes_in(worktree, exclude=()):
    """Every process whose cwd is inside the checkout, from one bounded `lsof` pass; a failed pass is uncertainty, not emptiness."""
    try:
        result = run([tool("lsof"), "-a", "-d", "cwd", "-Fpn", "-w"], timeout=LSOF_TIMEOUT, check=False)
    except SumError as exc:
        return None, str(exc)
    rows, pid = [], None
    for line in result.stdout.splitlines():
        if line[:1] == "p":
            pid = int(line[1:]) if line[1:].isdigit() else None
        elif line[:1] == "n" and pid is not None:
            rows.append({"pid": pid, "cwd": line[1:]})
    if not rows:
        return None, f"lsof exited {result.returncode} without a process table: {(result.stderr or '').strip()[-200:]}"
    roots = {str(worktree), os.path.realpath(worktree)}
    inside = [r for r in rows if r["pid"] not in set(exclude) | {os.getpid()}
              and any(r["cwd"] == root or r["cwd"].startswith(root + "/") for root in roots)]
    return inside, None


def pane_occupancy(session, pane_id, worktree=None):
    """What still runs in a pane, from supported observation only: `agent get` and `pane process-info`, plus cwd-based processes for a checkout."""
    view = {"pane": pane_id, "agent": None, "foreground": [], "detached": [], "blockers": []}
    agent, code = herdr_observe(["agent", "get", pane_id], session=session, timeout=5)
    if agent is not None:
        agent = agent.get("agent", agent)
        view["agent"] = {k: agent.get(k) for k in ("name", "agent", "agent_status")}
        view["blockers"].append(f"pane {pane_id} still hosts agent {agent.get('agent')!r} ({agent.get('agent_status')}); Herdr idle/done is not exit, wait for the agent process to end")
    elif code != "agent_not_found":
        view["blockers"].append(f"agent observation for pane {pane_id} is uncertain ({code})")
    info, code = herdr_observe(["pane", "process-info", "--pane", pane_id], session=session, timeout=5)
    if info is None:
        view["blockers"].append(f"process observation for pane {pane_id} is uncertain ({code})")
        return view
    info = info.get("process_info", info)
    shell = info.get("shell_pid")
    view["shell_pid"] = shell
    for process in info.get("foreground_processes") or []:
        if process.get("pid") == shell and (process.get("argv0") or process.get("name")) in SHELLS:
            continue
        view["foreground"].append({k: process.get(k) for k in ("pid", "name", "cmdline", "cwd")})
    if view["foreground"]:
        names = ", ".join(f"{p['name']} (pid {p['pid']})" for p in view["foreground"])
        view["blockers"].append(f"pane {pane_id} has foreground processes besides its shell: {names}")
    if shell is None:
        view["blockers"].append(f"pane {pane_id} reported no shell pid; occupancy cannot be established")
    if worktree:
        inside, error = processes_in(worktree, exclude=[shell] if shell else [])
        if inside is None:
            view["blockers"].append(f"processes with a cwd in the checkout cannot be established: {error}")
        elif inside:
            view["detached"] = inside
            view["blockers"].append("processes still run inside the checkout (detached from the pane or another pane): "
                                    + ", ".join(f"pid {p['pid']} at {p['cwd']}" for p in inside[:10]))
    return view


def cleanup_record(task):
    return task.get("cleanup") or {"schema": CLEANUP_SCHEMA, "state": None, "history": []}


def cleanup_pending(task):
    """A task the boss should hear about: merged on record, or a cleanup that was started and is not complete."""
    record = task.get("cleanup") or {}
    if task["status"] == "archived" and record.get("state") in (None, "complete"):
        return None
    if record.get("state") in ("blocked", "removing", "ready", "pending"):
        return {"state": record["state"], "at": record.get("at"), "blockers": [b["code"] for b in record.get("blockers", [])]}
    if (task.get("pr") or {}).get("merged_for_task"):
        return {"state": "pending", "at": task["pr"].get("observed_at"), "blockers": [], "note": f"PR merged on record; run cleanup {task['id']}"}
    return None


def save_cleanup(store, task_id, **changes):
    with store.lock():
        task = store.read(task_id)
        record = cleanup_record(task)
        record.update(schema=CLEANUP_SCHEMA, at=now(), **changes)
        record.setdefault("history", []).append({"at": record["at"], "state": record.get("state"), "step": changes.get("step")})
        record["history"] = record["history"][-40:]
        task["cleanup"] = record
        if changes.get("state") == "complete":
            task["status"] = "archived"
        store.save(task)
    return task


class Inspection:
    """One bounded pass over records, Git, GitHub, and Herdr for one task; every problem becomes a named blocker."""

    def __init__(self, store, task, ctx):
        self.store, self.task, self.ctx = store, task, ctx
        self.blockers = []
        self.resources = {}
        self.view = {"task": task["id"], "at": now()}

    def block(self, code, detail):
        self.blockers.append({"code": code, "detail": detail})

    def identity(self):
        task = self.task
        for key in ("pane", "workspace", "worktree", "branch", "repository", "session"):
            if not task.get(key):
                self.block("identity", f"task record has no {key}; nothing can be matched to a Herdr resource")
        if task["machine"] != machine():
            self.block("identity", f"task belongs to machine {task['machine']}, this is {machine()}")
        if task["session"] != self.ctx["session"]:
            self.block("identity", f"task lives in Herdr session {task['session']}, the coordinator runs in {self.ctx['session']}")
        if self.blockers:
            return
        own_workspace = workspace_of(self.ctx["pane"])
        pane, code = herdr_observe(["pane", "get", self.ctx["pane"]], session=self.ctx["session"], timeout=5)
        if pane is not None:
            own_workspace = pane.get("pane", pane).get("workspace_id") or own_workspace
        if task["workspace"] in {own_workspace, workspace_of(task["parent"]["pane"])}:
            self.block("identity", f"task workspace {task['workspace']} is the coordinator's own workspace; refusing")
        if task["pane"] == self.ctx["pane"]:
            self.block("identity", "task pane is the calling coordinator pane; refusing")
        worktree = Path(task["worktree"])
        repository = Path(task["repository"])
        if worktree == repository or repository in worktree.parents or worktree in repository.parents:
            self.block("identity", f"task worktree {worktree} overlaps the source repository {repository}; refusing")

    def herdr(self):
        task, session = self.task, self.ctx["session"]
        workspace, code = herdr_observe(["workspace", "get", task["workspace"]], session=session, timeout=5)
        if workspace is None:
            if code == "workspace_not_found":
                self.resources["workspace"] = "absent"
            else:
                self.block("workspace", f"workspace {task['workspace']} cannot be observed ({code})")
                self.resources["workspace"] = "uncertain"
        else:
            workspace = workspace.get("workspace", workspace)
            self.resources["workspace"] = "present"
            checkout = ((workspace.get("worktree") or {}).get("checkout_path"))
            if not checkout or Path(checkout).resolve() != Path(task["worktree"]).resolve():
                self.block("workspace", f"workspace {task['workspace']} is not the task checkout (checkout_path {checkout!r}, expected {task['worktree']})")
            panes, code = herdr_observe(["pane", "list", "--workspace", task["workspace"]], session=session, timeout=5)
            if panes is None:
                self.block("panes", f"panes of workspace {task['workspace']} cannot be listed ({code})")
            else:
                listed = panes.get("panes", panes) if isinstance(panes, dict) else panes
                self.view["panes"] = [{k: p.get(k) for k in ("pane_id", "cwd", "agent", "agent_status")} for p in listed]
                reviewer_pane = (task.get("reviewer") or {}).get("pane")
                for pane in listed:
                    if pane.get("pane_id") == task["pane"]:
                        continue
                    if pane.get("pane_id") == reviewer_pane:
                        self.resources["reviewer_in_task_workspace"] = True
                        continue
                    self.block("panes", f"unknown pane {pane.get('pane_id')} (cwd {pane.get('cwd')!r}, agent {pane.get('agent')!r}) in the task workspace; a service or helper pane sum did not create blocks removal")
        pane, code = herdr_observe(["pane", "get", task["pane"]], session=session, timeout=5)
        if pane is None:
            if code == "pane_not_found":
                self.resources["pane"] = "absent"
            else:
                self.block("pane", f"pane {task['pane']} cannot be observed ({code})")
                self.resources["pane"] = "uncertain"
        else:
            pane = pane.get("pane", pane)
            self.resources["pane"] = "present"
            if pane.get("workspace_id") not in {None, task["workspace"]}:
                self.block("pane", f"pane {task['pane']} now sits in workspace {pane.get('workspace_id')}, not {task['workspace']}; possible moved or reused pane")
            cwd = pane.get("cwd") or pane.get("foreground_cwd")
            if not cwd or Path(cwd).resolve() != Path(task["worktree"]).resolve():
                self.block("pane", f"pane {task['pane']} runs in {cwd!r}, not the task checkout; refusing a possibly reused pane")

    def git(self):
        task = self.task
        worktree = Path(task["worktree"])
        registered = None
        try:
            registered = worktree_paths(task["repository"])
        except SumError as exc:
            self.block("git", f"cannot list worktrees of {task['repository']}: {exc}")
        branch_exists = run(["git", "-C", task["repository"], "show-ref", "--verify", "--quiet", f"refs/heads/{task['branch']}"], check=False).returncode == 0
        self.resources["branch"] = "present" if branch_exists else "absent"
        if not branch_exists:
            self.block("git", f"branch {task['branch']} does not exist in {task['repository']}; history must survive cleanup, inspect before continuing")
        if not worktree.is_dir():
            self.resources["worktree"] = "absent"
            if registered is not None and worktree.resolve() in registered:
                self.block("git", f"{worktree} is gone but still registered as a worktree; inspect `git worktree list` before continuing")
            return
        self.resources["worktree"] = "present"
        try:
            toplevel = Path(run(["git", "-C", worktree, "rev-parse", "--show-toplevel"]).stdout.strip()).resolve()
            common = Path(run(["git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-common-dir"]).stdout.strip()).resolve()
            branch = run(["git", "-C", worktree, "branch", "--show-current"]).stdout.strip()
        except SumError as exc:
            self.block("git", f"{worktree} is not a readable Git checkout: {exc}")
            return
        if toplevel != worktree.resolve():
            self.block("git", f"{worktree} is not the top level of its checkout ({toplevel})")
        if common.parent != Path(task["repository"]).resolve():
            self.block("git", f"{worktree} does not belong to {task['repository']} (common dir {common})")
        if branch != task["branch"]:
            self.block("git", f"checkout is on {branch!r}, not the task branch {task['branch']!r}")
        if registered is not None and worktree.resolve() not in registered:
            self.block("git", f"{worktree} is not a registered worktree of {task['repository']}")

    def obligations(self):
        task = self.task
        open_questions = [q["id"] for q in task["questions"] if q["status"] != "applied"]
        if open_questions:
            self.block("obligations", f"questions not yet answered and applied: {open_questions}")
        handoffs = [r for r in task.get("evidence", []) if r.get("kind") == "handoff" and r.get("source") == "worker" and r.get("candidate")]
        if not handoffs:
            self.block("handoff", "no structured worker handoff saved; a prose report is not a handoff")
            return None
        return handoffs[-1]["candidate"]

    def github(self, number=None):
        task = self.task
        recorded = task.get("pr")
        if number is None and not (recorded and recorded.get("complete")):
            self.block("pr", "no complete PR identity recorded; run `pr reconcile TASK --number N` or pass --number")
            return None
        number = number if number is not None else recorded["identity"]["number"]
        try:
            observation = observe_pr(task, None, number)
        except SumError as exc:
            with self.store.lock():
                current = self.store.read(task["id"])
                record = append_evidence(current, "publication", "github", {"outcome": "uncertain", "number": number, "repository": None, "error": str(exc)}, endpoint=self.ctx)
                self.store.save(current)
            self.block("pr-uncertain", f"GitHub observation of PR #{number} failed and was recorded as uncertain ({record['id']}): {exc}")
            return None
        pr, record, previous = save_pr_observation(self.store, task["id"], observation, self.ctx)
        self.view["pr"] = {"number": pr["identity"]["number"], "state": pr["state"], "head_sha": pr["identity"]["head_sha"], "merge_commit": pr["merge_commit"],
                           "findings": pr["findings"], "evidence": record["id"]}
        if recorded and recorded.get("identity", {}).get("head_sha") and recorded["identity"]["head_sha"] != pr["identity"]["head_sha"]:
            self.block("pr", f"PR head moved from recorded {recorded['identity']['head_sha']} to {pr['identity']['head_sha']}; reconcile and verify the new head first")
        if pr["state"] != "merged" or not pr["merge_commit"]:
            self.block("pr", f"PR #{number} is {pr['state']}, not merged with a merge commit; closed or open PRs never justify cleanup")
        for finding in pr["findings"]:
            self.block("pr", finding)
        return pr

    def checkout(self, merged_head):
        task = self.task
        if self.resources.get("worktree") != "present":
            return
        if merged_head:
            head, extra = commits_not_covered(task["worktree"], merged_head)
            self.view["head"] = head
            if extra:
                self.block("commits", f"{len(extra)} commit(s) in the checkout are not in the merged PR head {merged_head}: {extra[:5]}")
        artifacts = worktree_artifacts(task["worktree"])
        self.view["artifacts"] = artifacts
        for key, label in (("staged", "staged changes"), ("tracked_modified", "modified tracked files"), ("untracked", "untracked files"),
                           ("ignored_preserved", "ignored files that are not known disposable caches")):
            if artifacts[key]:
                self.block("artifacts", f"{label}: {artifacts[key][:10]}{' ...' if len(artifacts[key]) > 10 else ''}; move or commit them, sum never runs git clean")

    def occupancy(self):
        task = self.task
        if self.resources.get("pane") == "present":
            view = pane_occupancy(self.ctx["session"], task["pane"], task["worktree"] if self.resources.get("worktree") == "present" else None)
            self.view["occupancy"] = view
            for detail in view["blockers"]:
                self.block("occupant", detail)
        elif self.resources.get("worktree") == "present":
            inside, error = processes_in(task["worktree"])
            if inside is None:
                self.block("occupant", f"processes with a cwd in the checkout cannot be established: {error}")
            elif inside:
                self.block("occupant", "processes still run inside the checkout: " + ", ".join(f"pid {p['pid']} at {p['cwd']}" for p in inside[:10]))

    def reviewer(self):
        """The bound reviewer pane may close only with saved findings and an exited occupant; it is never the worker or coordinator pane."""
        task = self.task
        reviewer = task.get("reviewer")
        if not reviewer:
            self.resources["reviewer_pane"] = None
            return
        row = {"pane": reviewer["pane"], "closable": False, "reason": None}
        self.view["reviewer"] = row
        findings = [r for r in task.get("evidence", []) if r.get("kind") == "review"]
        if not findings:
            row["reason"] = "no saved reviewer findings"
            self.block("reviewer", f"reviewer pane {reviewer['pane']} has no saved findings; it stays open")
            return
        if identity(reviewer) in {identity(task), identity(task["parent"]), identity(self.ctx)}:
            row["reason"] = "reviewer endpoint is the worker or coordinator pane"
            self.block("reviewer", "reviewer endpoint equals the worker or coordinator pane; refusing")
            return
        if reviewer["machine"] != machine() or reviewer["session"] != self.ctx["session"]:
            row["reason"] = "reviewer pane is in another session or machine"
            self.block("reviewer", f"reviewer pane {reviewer['pane']} is in session {reviewer['session']} on {reviewer['machine']}; not observable from here")
            return
        pane, code = herdr_observe(["pane", "get", reviewer["pane"]], session=self.ctx["session"], timeout=5)
        if pane is None:
            if code == "pane_not_found":
                self.resources["reviewer_pane"] = "absent"
                row.update(closable=False, reason="already absent")
            else:
                self.block("reviewer", f"reviewer pane {reviewer['pane']} cannot be observed ({code})")
            return
        self.resources["reviewer_pane"] = "present"
        view = pane_occupancy(self.ctx["session"], reviewer["pane"])
        row["occupancy"] = view
        if view["blockers"]:
            row["reason"] = "reviewer occupant has not exited"
            for detail in view["blockers"]:
                self.block("reviewer", detail)
            return
        row["closable"] = True

    def plan(self):
        state = "blocked" if self.blockers else "ready"
        return {**self.view, "state": state, "blockers": self.blockers, "resources": self.resources}


def inspect_task(store, task, ctx, number=None, scope="task"):
    inspection = Inspection(store, task, ctx)
    inspection.identity()
    if inspection.blockers:
        return inspection.plan()
    if scope == "reviewer":
        inspection.reviewer()
        return inspection.plan()
    inspection.herdr()
    inspection.git()
    inspection.obligations()
    pr = inspection.github(number)
    inspection.checkout(pr["identity"]["head_sha"] if pr else None)
    inspection.occupancy()
    inspection.reviewer()
    return inspection.plan()


def recheck(store, task, ctx, merged_head):
    """The bounded second look taken after occupants exited and immediately before native removal."""
    inspection = Inspection(store, task, ctx)
    inspection.herdr()
    inspection.git()
    inspection.checkout(merged_head)
    inspection.occupancy()
    return inspection.plan()


def resources_absent(task, session):
    """After a removal (or a crash right after one): is every task resource verifiably gone by identity, not by label?"""
    workspace, code = herdr_observe(["workspace", "get", task["workspace"]], session=session, timeout=5)
    worktree = Path(task["worktree"])
    registered = worktree.resolve() in worktree_paths(task["repository"]) if Path(task["repository"]).is_dir() else False
    detail = {"workspace": "absent" if workspace is None and code == "workspace_not_found" else ("present" if workspace is not None else f"uncertain ({code})"),
              "worktree": "absent" if not worktree.exists() else "present", "registered": registered,
              "branch": "present" if run(["git", "-C", task["repository"], "show-ref", "--verify", "--quiet", f"refs/heads/{task['branch']}"], check=False).returncode == 0 else "absent"}
    return detail["workspace"] == "absent" and detail["worktree"] == "absent" and not registered, detail


def cleanup_reconcile(store, task, ctx):
    """Finish or roll back a cleanup interrupted between native removal and archiving. Records and observation only."""
    record = cleanup_record(task)
    if record.get("state") != "removing":
        return None
    gone, detail = resources_absent(task, ctx["session"])
    if gone:
        task = save_cleanup(store, task["id"], state="complete", step="reconciled-after-interruption", removed=detail, blockers=[])
        return {"task": task["id"], "state": "complete", "reconciled": True, "resources": detail}
    if detail["workspace"] == "present":
        task = save_cleanup(store, task["id"], state="pending", step="reconciled-nothing-removed", resources=detail)
        return {"task": task["id"], "state": "pending", "reconciled": True, "resources": detail, "note": "The workspace still exists; the interrupted removal did not happen. Run cleanup again."}
    task = save_cleanup(store, task["id"], state="blocked", step="reconciled-partial", resources=detail,
                        blockers=[{"code": "partial", "detail": f"resources after interruption: {detail}; inspect before continuing, nothing is recreated or forced"}])
    return {"task": task["id"], "state": "blocked", "reconciled": True, "resources": detail}


def close_reviewer_pane(store, task, ctx, plan):
    """Close only the bound reviewer pane through native `pane close`; it never touches the implementation checkout."""
    row = plan.get("reviewer") or {}
    if not row.get("closable"):
        return {"closed": False, "reason": row.get("reason") or "not closable"}
    result, code = herdr_observe(["pane", "close", task["reviewer"]["pane"]], session=ctx["session"], timeout=10)
    if result is None and code != "pane_not_found":
        raise SumError(f"pane close for reviewer pane {task['reviewer']['pane']} failed ({code}); nothing else was changed")
    save_cleanup(store, task["id"], step="reviewer-pane-closed", reviewer_pane_closed={"pane": task["reviewer"]["pane"], "at": now(), "already_absent": result is None})
    return {"closed": True, "pane": task["reviewer"]["pane"], "already_absent": result is None}


def cleanup(store, args):
    ctx = context()
    require_coordinator(store, ctx)
    ensure_version()
    task = store.read(args.task)
    store.check_machine(task)
    record = cleanup_record(task)
    if record.get("state") == "complete":
        return {"task": task["id"], "state": "complete", "already": True, "resources": record.get("removed"), "archived": task["status"] == "archived",
                "note": "Cleanup already completed; nothing was observed or changed."}
    if record.get("state") == "removing":
        reconciled = cleanup_reconcile(store, task, ctx)
        if reconciled["state"] != "pending":
            return reconciled
        task = store.read(args.task)
    scope = "reviewer" if args.reviewer_only else "task"
    plan = inspect_task(store, task, ctx, number=args.number, scope=scope)
    if scope == "reviewer":
        if not args.apply:
            save_cleanup(store, task["id"], step="inspected-reviewer", reviewer=plan.get("reviewer"))  # Reviewer scope never marks the task cleanup-pending.
            return {**plan, "apply": False, "scope": scope, "note": "Inspection only. --apply closes just the reviewer pane; the task checkout, workspace, and worker pane are untouched."}
        if plan["blockers"]:
            raise SumError("Reviewer pane not closed: " + "; ".join(b["detail"] for b in plan["blockers"]))
        return {"task": task["id"], "scope": scope, "reviewer": close_reviewer_pane(store, task, ctx, plan), "state": record.get("state")}
    if not args.apply:
        save_cleanup(store, task["id"], step="inspected", state=plan["state"], blockers=plan["blockers"], resources=plan["resources"])
        return {**plan, "apply": False, "note": "Inspection only; nothing was removed. `cleanup TASK --apply` removes the verified workspace with native Herdr operations and archives the record only when no blocker remains."}
    if plan["blockers"]:
        save_cleanup(store, task["id"], step="apply-refused", state="blocked", blockers=plan["blockers"], resources=plan["resources"])
        raise SumError(f"Cleanup of {task['id']} refused; the task stays cleanup-pending. Blockers: " + "; ".join(f"[{b['code']}] {b['detail']}" for b in plan["blockers"]))
    merged_head = plan["pr"]["head_sha"]
    intent = {"workspace": task["workspace"], "pane": task["pane"], "worktree": task["worktree"], "branch": task["branch"], "repository": task["repository"],
              "head": plan.get("head"), "merged_head": merged_head, "merge_commit": plan["pr"]["merge_commit"], "pr": plan["pr"]["number"], "at": now()}
    save_cleanup(store, task["id"], step="intent", state="ready", intent=intent, blockers=[], resources=plan["resources"])
    removed = {"performed": False}
    if plan["resources"].get("workspace") == "present":
        again = recheck(store, task, ctx, merged_head)  # Writers may have appeared or files changed since the inspection.
        if again["blockers"]:
            save_cleanup(store, task["id"], step="recheck-refused", state="blocked", blockers=again["blockers"], resources=again["resources"])
            raise SumError(f"Cleanup of {task['id']} refused at the recheck before removal: " + "; ".join(f"[{b['code']}] {b['detail']}" for b in again["blockers"]))
        save_cleanup(store, task["id"], step="removing", state="removing")  # Persisted before the one native, non-forced removal.
        result, code = herdr_observe(["worktree", "remove", "--workspace", task["workspace"]], session=ctx["session"], timeout=60)
        if result is None:
            if code == "workspace_not_found":
                pass  # Verified below by identity; already-absent is acceptable only after that inspection.
            elif code in {"dirty_worktree_requires_force", "worktree_requires_force"}:
                save_cleanup(store, task["id"], step="removal-refused-by-herdr", state="blocked",
                             blockers=[{"code": "artifacts", "detail": f"Herdr refused the non-forced removal ({code}); the checkout changed under us and is preserved"}])
                raise SumError(f"Herdr refused the non-forced removal ({code}); nothing was removed and the task stays cleanup-pending.")
            else:
                save_cleanup(store, task["id"], step="removal-uncertain", state="removing", error=code)
                raise SumError(f"worktree remove for workspace {task['workspace']} returned {code}; the outcome is uncertain, run cleanup again to reconcile from observation.")
        else:
            removed = {"performed": True, "path": result.get("path"), "forced": bool(result.get("forced"))}
            if removed["forced"]:
                raise SumError("Herdr reports a forced removal; sum never requested force. Inspect the Herdr build before trusting this cleanup.")
    elif plan["resources"].get("worktree") == "present":
        save_cleanup(store, task["id"], step="apply-refused", state="blocked",
                     blockers=[{"code": "workspace", "detail": "the Herdr workspace is gone but the checkout remains; reopen it with `herdr worktree open` or remove it yourself, sum removes checkouts only through the native workspace operation"}])
        raise SumError(f"Cleanup of {task['id']} refused: no Herdr workspace owns the remaining checkout {task['worktree']}.")
    gone, detail = resources_absent(task, ctx["session"])
    if not gone:
        save_cleanup(store, task["id"], step="removal-incomplete", state="removing", resources=detail)
        raise SumError(f"After removal the task resources are not all absent ({detail}); nothing further was changed. Inspect, then run cleanup again to reconcile.")
    if detail["branch"] != "present":
        detail["warning"] = f"branch {task['branch']} is missing; sum never deletes branches, inspect the repository"
    reviewer = close_reviewer_pane(store, task, ctx, plan) if task.get("reviewer") else None
    task = save_cleanup(store, task["id"], state="complete", step="archived", removed={**detail, **removed}, blockers=[], reviewer_pane=reviewer)
    return {"task": task["id"], "state": "complete", "archived": True, "removed": {**detail, **removed}, "kept": {"branch": task["branch"], "records": str(store.path(task["id"]))},
            "reviewer": reviewer, "note": "Only the verified task workspace and its clean checkout were removed, through native Herdr without force. The branch, brief revisions, decisions, reports, and PR evidence stay."}


def status(store, live=False, inbox=False):
    rows = []
    tasks = store.all()
    snapshots = Snapshots() if live else None
    for task in tasks:
        row = {k: task.get(k) for k in ("id", "status", "repository", "harness", "pane", "session", "worktree", "error")}
        row["model"] = (task.get("launch") or {}).get("model")
        row["questions"] = [q for q in task["questions"] if q["status"] != "applied"]
        row["report_available"] = task["report"] is not None
        row["evidence"] = {"records": len(task.get("evidence", [])), "pr": (task.get("pr") or {}).get("identity", {}).get("number") if task.get("pr") else None,
                           "merged_for_task": bool((task.get("pr") or {}).get("merged_for_task"))}
        row["notice"] = task["notice"]
        row["cleanup"] = cleanup_pending(task)
        try:
            versions = read_versions(store, task)
            row["brief"] = {"active": versions.get("active"), "requested": versions.get("requested")}
            if versions.get("requested"):
                row["refresh"] = refresh_state(versions)  # Checked here, at an ordinary interaction; there is no polling loop.
        except SumError as exc:
            row["brief"] = {"error": str(exc)}
        if live and task.get("pane") and task["status"] != "archived":
            try:
                store.check_machine(task)
                agent = snapshots.agent(task["session"], task["pane"])
                row["observed"] = agent.get("agent_status", agent.get("status", "unknown"))
                if row["observed"] in {"idle", "done", "blocked", "unknown"} and not task["report"]:
                    row["attention"] = "No report. Inspect this worker's current output; lifecycle state is not a task result."
            except SumError as exc:
                row["attention"] = f"Cannot observe worker: {exc}"
        if live and (task.get("cleanup") or {}).get("state") == "removing":  # An interrupted cleanup reconciles at the next bounded pass, never in a loop.
            try:
                row["cleanup_reconciled"] = cleanup_reconcile(store, task, context())
                row["cleanup"] = cleanup_pending(store.read(task["id"]))
            except SumError as exc:
                row["attention"] = f"Interrupted cleanup could not be reconciled: {exc}"
        if not inbox or row["questions"] or row["error"] or row.get("attention") or row["report_available"] or row["cleanup"]:
            if task["status"] != "archived" or row["questions"]:
                rows.append(row)
    value = {"tasks": rows, "live": live, "capacity": capacity_view(store, tasks),
             "guarantee": "Saved records only; prose-only questions require a rundown. No background monitoring."}
    if live:
        value["fanout"] = snapshots.summary()
    return value


def installation_hint(root):
    """A task checkout of sum is a linked Git worktree; the installation's records live beside the common Git directory."""
    try:
        common = Path(run(["git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir"]).stdout.strip())
    except SumError:
        return None
    home = common.parent / ".sum"
    return home if common.name == ".git" and common.parent.resolve() != Path(root).resolve() and (home / "state.json").is_file() else None


def matching_task(store, endpoint):
    for task in store.all():
        if task.get("pane") and task["status"] != "archived" and identity(task) == identity(endpoint):
            return task
    return None


def herdr_error_code(result):
    """Herdr 0.8.2 writes JSON errors to stderr: {"error": {"code": ..., "message": ...}}."""
    try:
        return json.loads(result.stderr).get("error", {}).get("code")
    except (ValueError, AttributeError):
        return None


def observe_owner(owner):
    """Observe the recorded coordinator endpoint. Only Herdr's pane_not_found code means absent; anything else is uncertain or present."""
    if owner["machine"] != machine():
        return "other-machine", None
    try:
        result = run([tool("herdr"), "--session", owner["session"], "pane", "get", owner["pane"]], timeout=5, check=False)
    except SumError as exc:
        return "uncertain", str(exc)
    if result.returncode:
        code = herdr_error_code(result)
        if code == "pane_not_found":
            return "absent", code
        return "uncertain", (result.stderr or result.stdout).strip()[-300:] or f"exit {result.returncode}"
    return "present", result.stdout[:300]


def init(store, args):
    """Explicit session initialization: register this pane's role in this instance. Never rebinds tasks."""
    ctx = context()
    requested = args.role
    if requested == "worker" and not args.task:
        raise SumError("--role worker needs --task TASK_ID.")
    if not store.designated():
        hint = installation_hint(ROOT)
        task = matching_task(Store(hint), ctx) if hint else None
        role = "worker" if task else "developer"
        if requested == "coordinator":
            raise SumError(f"{store.home} is not a sum installation (no state.json from setup). A checkout alone grants no coordinator authority; run mise run setup in the designated installation.")
        marker = development_marker(ROOT)
        return {"role": role, "home": str(store.home), "installation": False, "task": task["id"] if task else None,
                "installation_home": str(hint) if hint else None, "registered": False, "endpoint": ctx,
                "development": marker,
                "note": ("Dispatched worker checkout: follow your brief; do not initialize a coordinator." if task else
                         "Development checkout: modify and test sum here only. No coordinator initialization, dispatch, production setup, or instance-wide updates."
                         + (" Tests use temporary --home state and a named lab Herdr session; the installed helper at "
                            f"{Path(marker['installation']) / 'bin' / 'sumctl'} owns any parent-task callbacks." if marker else ""))}
    ensure_version()
    herdr(["pane", "get", ctx["pane"]], session=ctx["session"], timeout=5)  # Verify the caller's own endpoint exists.
    with store.lock():
        state = read_json(store.home / "state.json")
        if not state.get("instance"):
            state["instance"] = uuid.uuid4().hex  # Additive upgrade of the 0.1.0 state format.
            atomic_json(store.home / "state.json", state)
        owner = store.owner()
        previous = store.registration(ctx)
        task = matching_task(store, ctx)
        if args.task:
            recorded = store.read(args.task)
            if not task or task["id"] != args.task:
                raise SumError(f"This pane is not the recorded worker pane of {args.task} ({recorded.get('pane')}). Worker identity comes from dispatch records, not from the brief text.")
        result = {"home": str(store.home), "installation": True, "instance": state["instance"], "endpoint": ctx, "upgraded": False}
        if task or (previous and previous["role"] == "worker"):
            if requested == "coordinator":
                raise SumError("This pane is a dispatched worker; it cannot become the coordinator.")
            role, task_id = "worker", task["id"] if task else previous["task"]
        elif owner and identity(owner) == identity(ctx):
            if requested == "developer":
                raise SumError("This pane owns the coordinator role. Initialize a developer session from another pane; ownership is not released implicitly.")
            role, task_id = "coordinator", None
            if "role" not in owner:
                owner.update(role="coordinator", instance=state["instance"], sum_version=VERSION, upgraded_at=now())
                atomic_json(store.home / "context.json", owner)
                result["upgraded"] = True
        elif owner is None and requested in (None, "coordinator"):
            owner = {**ctx, "role": "coordinator", "instance": state["instance"], "sum_version": VERSION, "claimed_at": now()}
            atomic_json(store.home / "context.json", owner)
            role, task_id = "coordinator", None
        elif requested == "coordinator":
            if not args.reclaim:
                raise SumError(f"Coordinator is owned by pane {owner['pane']} in session {owner['session']} on {owner['machine']}. Inspect it; use --reclaim only for a deliberate, verified takeover. Task parent routes stay unchanged either way.")
            observed, detail = observe_owner(owner)
            if observed != "absent":
                raise SumError(f"Refusing reclaim: recorded coordinator pane is {observed} ({detail}). Only a pane Herdr reports as pane_not_found can be reclaimed; an existing, unreachable, or uncertain root is not permission to take over.")
            owner = {**ctx, "role": "coordinator", "instance": state["instance"], "sum_version": VERSION, "claimed_at": now(),
                     "reclaimed_from": {k: owner.get(k) for k in ("machine", "session", "pane", "at", "claimed_at")}, "previous_observed": observed}
            atomic_json(store.home / "context.json", owner)
            result["reclaimed"] = True
            role, task_id = "coordinator", None
        else:
            role, task_id = "developer", None
        registration = store.register(ctx, role, task=task_id)
    if role == "coordinator":
        result["contract"] = contract_state(store)  # A restarted coordinator sees a pending contract refresh here, not in a lost prompt.
        result["cleanup_pending"] = [{"task": t["id"], **cleanup_pending(t)} for t in store.all() if cleanup_pending(t)]  # Records only; no Herdr or GitHub call.
    result.update(role=role, task=task_id, registration=registration, coordinator=store.owner(),
                  note={"coordinator": "You are the coordinator for this instance. Continue the coordinator startup steps."
                                       + (f" Operating contract revision {result['contract']['requested']} is requested: read it and run `sumctl refresh adopt --coordinator {result['contract']['requested']}` before other work." if result.get("contract", {}).get("requested") else ""),
                        "worker": "You are a dispatched worker. Follow your brief; do not run coordinator startup.",
                        "developer": "Another session owns coordination. Do not run coordinator startup, dispatch, or setup here; develop sum only in a development checkout. Role bookkeeping is not an OS-level sandbox."}[role])
    return result


def doctor(store):
    """Observational only: no state, context, or registration is written."""
    checks = []
    for name in ("python3", "node", "git", "gh", "herdr", "quota-axi", "lsof"):
        try:
            path = tool(name)
            checks.append({"tool": name, "path": path, "ok": True})
        except SumError as exc:
            checks.append({"tool": name, "ok": False, "detail": str(exc)})
    try:
        checks.append({"tool": "herdr-version", "ok": True, "detail": ensure_version()})
    except SumError as exc:
        checks.append({"tool": "herdr-version", "ok": False, "detail": str(exc)})
    role = {"tool": "role", "ok": True, "installation": store.designated(), "coordinator": store.owner() if store.designated() else None}
    try:
        ctx = context()
        herdr(["pane", "get", ctx["pane"]], session=ctx["session"])
        checks.append({"tool": "herdr-context", "ok": True, "detail": ctx})
        registration = store.registration(ctx) if store.designated() else None
        role["registered"] = registration["role"] if registration else None
        role["detail"] = ("Registered as " + registration["role"] + "." if registration else
                          "This pane is not registered. Run ./bin/sumctl init to register explicitly; doctor never binds.")
    except SumError as exc:
        checks.append({"tool": "herdr-context", "ok": False, "detail": str(exc)})
    checks.append(role)
    installed = {kind: shutil.which(exe) or (str(ROOT / '.local/bin' / exe) if (ROOT / '.local/bin' / exe).is_file() else None)
                 for kind, exe in HARNESSES.items()}
    checks.append({"tool": "harness", "ok": any(installed.values()), "installed": {k:v for k,v in installed.items() if v}})
    checks.append({"tool": "mesh", "ok": (RUNTIME / ".deps/herdr-mesh/.sum-patched").is_file()})
    return {"version": VERSION, "home": str(store.home), "runtime": str(RUNTIME), "installation": str(ROOT), "checks": checks,
            "ok": all(c["ok"] for c in checks),
            "note": "Observation only: nothing was bound or written. No auth changes or permission bypasses. Authenticate the chosen harness and gh separately."}


def backup(store, destination):
    """Records-only backup: deliberately refuses to claim a moving checkout snapshot."""
    destination = Path(destination).expanduser().resolve()
    if destination.exists():
        raise SumError("Backup destination already exists; choose a new path.")
    if destination == store.home or store.home in destination.parents:
        raise SumError("Put backups outside the state directory.")
    with store.lock():
        tasks = store.all()
        manifest = {"schema": SCHEMA, "sum_version": VERSION, "created_at": now(),
                    "scope": "records-only", "includes_worktree_code": False,
                    "credential_files_included": False, "content_redaction": "none; task text may be sensitive", "machine": machine(),
                    "worktrees_not_captured": [{"task": t["id"], "path": t["worktree"], "branch": t["branch"]} for t in tasks],
                    "brief_revisions_included": True, "settings_included": (store.home / SETTINGS_FILE).is_file(),
                    "restore": "Extract into a new directory. Start sumctl with --home <extracted>/state. Do not reuse pane bindings on another machine; inspect and bind explicitly."}
        destination.parent.mkdir(parents=True, exist_ok=True)
        try:
            with tarfile.open(destination, "x:gz") as archive:
                with tempfile.TemporaryDirectory() as tmp:
                    path = Path(tmp) / "manifest.json"
                    atomic_json(path, manifest)
                    archive.add(path, arcname="manifest.json")
                # Exact allowlist: nested project clones are never traversed.
                paths = [store.home / name for name in ("state.json", SETTINGS_FILE, "preferences.md", "projects.md")]
                paths.extend(sorted(store.sessions.glob("*.json")) if store.sessions.is_dir() else [])
                contract_dir = store.home / CONTRACT_DIR
                if (contract_dir / VERSIONS_FILE).is_file():
                    paths.append(contract_dir / VERSIONS_FILE)
                    paths.extend(revision_file(contract_dir, r) for r in read_contract_versions(store)["revisions"])
                for task in tasks:
                    paths.append(store.path(task["id"]) / "task.json")
                    if task.get("brief_path"):
                        paths.append(store.path(task["id"]) / "brief.md")
                    sidecar = store.path(task["id"]) / VERSIONS_FILE
                    if sidecar.is_file() or sidecar.is_symlink():
                        paths.append(sidecar)
                        try:
                            paths.extend(revision_path(store, task["id"], r) for r in read_versions(store, task)["revisions"])
                        except SumError as exc:
                            manifest.setdefault("unreadable_version_metadata", []).append({"task": task["id"], "error": str(exc)})
                for path in dict.fromkeys(paths):
                    if path.is_symlink():
                        raise SumError("Refusing symlink in state backup.")
                    if path.is_file():
                        archive.add(path, arcname=str(Path("state") / path.relative_to(store.home)), recursive=False)
        except Exception:
            destination.unlink(missing_ok=True)
            raise
    os.chmod(destination, 0o600)
    return {"backup": str(destination), "manifest": manifest,
            "sha256": hashlib.sha256(destination.read_bytes()).hexdigest()}


# --- self-development checkouts ---------------------------------------------------------------

def development_marker(root):
    """A checkout prepared by `sumctl dev` carries .sum/dev.json; it is never an installation."""
    path = Path(root) / ".sum" / "dev.json"
    if not path.is_file():
        return None
    value = read_json(path)
    if value.get("schema") != SCHEMA or value.get("kind") != "development":
        raise SumError(f"Unrecognized development marker {path}; inspect it before continuing.")
    return value


def guard_candidate(store, command):
    """Candidate code in a development or task checkout may only read the installation's records.

    Writes (ask/report/init/dispatch/...) against the installation must come from its installed helper,
    so an inherited SUM_HOME or a copied command line cannot make lab code touch production state.
    """
    if (ROOT / ".sum" / "state.json").is_file() and not (ROOT / ".sum" / "dev.json").is_file():
        return None  # This helper is the installation's own.
    protected = set()
    hint = installation_hint(ROOT)
    if hint:
        protected.add(hint.resolve())
    marker = development_marker(ROOT)
    if marker and marker.get("installation_home"):
        protected.add(Path(marker["installation_home"]).resolve())
    if store.home in protected and command not in READ_ONLY_COMMANDS:
        installed = Path(next(iter(protected))).parent / "bin" / "sumctl"
        raise SumError(f"Refusing `{command}`: this helper runs from a development or task checkout ({ROOT}) but targets the installation's state {store.home}. "
                       f"Candidate code operates only on lab state (--home under a temporary directory). "
                       f"Parent-task callbacks and installation changes use the installed trusted helper {installed}.")
    return None


def installation_root(store):
    """`sumctl dev` and `sumctl release` act on the installation that owns the given state home, never on a development checkout."""
    if store.home.name != ".sum" or not store.designated():
        raise SumError(f"{store.home} is not a sum installation's state home; run dev and release commands with the installation's ./bin/sumctl.")
    root = store.home.parent
    toplevel = Path(run(["git", "-C", root, "rev-parse", "--show-toplevel"]).stdout.strip()).resolve()
    if toplevel != root.resolve():
        raise SumError(f"{root} is not the top level of a Git checkout.")
    return root.resolve()


def worktree_paths(root):
    paths = []
    for line in run(["git", "-C", root, "worktree", "list", "--porcelain"]).stdout.splitlines():
        if line.startswith("worktree "):
            paths.append(Path(line[len("worktree "):]).resolve())
    return paths


def ensure_disjoint(path, root, allow_self=False):
    """A development checkout must not be, contain, alias, or sit inside the installation or another worktree."""
    if any(p.is_symlink() for p in (path, *path.parents)):
        raise SumError(f"Refusing {path}: symlink components could alias the installation or another checkout.")
    resolved = path.resolve()
    allowed = (root / ".sum" / "dev").resolve()
    if resolved.parent != allowed:
        raise SumError(f"Development checkouts live directly under {allowed}.")
    if resolved == root or resolved in root.parents:
        raise SumError("Refusing a development checkout that is or contains the installation.")
    for other in worktree_paths(root):
        if other == root or (allow_self and other == resolved):
            continue
        if other == resolved or other in resolved.parents or resolved in other.parents:
            raise SumError(f"Refusing {resolved}: it overlaps the existing checkout {other}. Choose another name.")
    return resolved


def dev_status(path):
    dirty = run(["git", "-C", path, "status", "--porcelain", "--untracked-files=all"]).stdout.strip()
    return {"dirty": bool(dirty), "head": run(["git", "-C", path, "rev-parse", "HEAD"]).stdout.strip(),
            "branch": run(["git", "-C", path, "branch", "--show-current"]).stdout.strip()}


def dev_note(root, path):
    return (f"Develop only in {path}; run ./bin/sumctl init there (it reports developer). Do not edit, build, or test in {root}. "
            f"Setup, .deps, .local, and .sum inside the checkout are separate from the installation; the checkout's .sum/dev.json prevents coordinator claims. "
            f"Tests need temporary --home state and a named lab Herdr session, never the installation's state or the default session. "
            f"Parent-task callbacks use the installed trusted helper {root / 'bin' / 'sumctl'}. Ship through the normal task/PR procedure; nothing here is published or installed automatically.")


def dev_prepare(store, args):
    root = installation_root(store)
    if not DEV_NAME.fullmatch(args.name):
        raise SumError("Development name: lowercase letters, digits, dot, underscore, or dash; at most 40 characters.")
    path = root / ".sum" / "dev" / args.name
    branch = f"sum-dev/{args.name}"
    existing = development_marker(path) if path.exists() else None
    if path.exists() and not existing:
        raise SumError(f"{path} exists but is not a sum development checkout. Inspect it; nothing was removed.")
    if not existing:
        base_sha = run(["git", "-C", root, "rev-parse", "--verify", f"{args.base}^{{commit}}", "--"]).stdout.strip()
        ensure_disjoint(path, root)
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        has_branch = run(["git", "-C", root, "show-ref", "--verify", "--quiet", f"refs/heads/{branch}"], check=False).returncode == 0
        if has_branch:  # A preserved branch is checked out again, never recreated from the base.
            run(["git", "-C", root, "worktree", "add", str(path), branch], timeout=60)
            base_sha = run(["git", "-C", root, "rev-parse", branch]).stdout.strip()
        else:
            run(["git", "-C", root, "worktree", "add", "-b", branch, str(path), base_sha], timeout=60)
        actual = Path(run(["git", "-C", path, "rev-parse", "--show-toplevel"]).stdout.strip()).resolve()
        if actual != path.resolve() or actual == root:
            raise SumError("Git returned a checkout that does not match the requested path. Inspect it manually.")
        existing = {"schema": SCHEMA, "kind": "development", "name": args.name, "installation": str(root),
                    "installation_home": str(store.home), "branch": branch, "base_sha": base_sha,
                    "created_at": now(), "panes": []}
        (path / ".sum").mkdir(mode=0o700, exist_ok=True)
        atomic_json(path / ".sum" / "dev.json", existing)
        reopened = False
    else:
        ensure_disjoint(path, root, allow_self=True)
        reopened = True
    pane = None
    if args.pane:
        ctx = context()
        ensure_version()
        created = herdr(["workspace", "create", "--cwd", str(path), "--label", f"sum-dev-{args.name}", "--no-focus"],
                        session=ctx["session"], timeout=30)
        pane = {"pane": created["root_pane"]["pane_id"], "workspace": created["workspace"]["workspace_id"],
                "session": ctx["session"], "machine": machine(), "at": now()}
        existing.setdefault("panes", []).append(pane)
        atomic_json(path / ".sum" / "dev.json", existing)
    return {"name": args.name, "path": str(path), "branch": existing["branch"], "base_sha": existing["base_sha"],
            "installation": str(root), "reopened": reopened, "role": "developer", "pane": pane,
            **dev_status(path), "note": dev_note(root, path)}


def dev_list(store):
    root = installation_root(store)
    rows = []
    for marker in sorted((root / ".sum" / "dev").glob("*/.sum/dev.json")):
        path = marker.parents[1]
        try:
            value = development_marker(path)
            rows.append({"name": value["name"], "path": str(path), "branch": value["branch"], "panes": value.get("panes", []), **dev_status(path)})
        except SumError as exc:
            rows.append({"path": str(path), "error": str(exc)})
    return {"installation": str(root), "checkouts": rows}


def dev_remove(store, args):
    """Removes only a clean, fully merged development checkout through plain Git; anything else is preserved."""
    root = installation_root(store)
    if not DEV_NAME.fullmatch(args.name):
        raise SumError("Invalid development name.")
    path = root / ".sum" / "dev" / args.name
    marker = development_marker(path) if path.exists() else None
    if not marker:
        raise SumError(f"{path} is not a sum development checkout; nothing was removed.")
    ensure_disjoint(path, root, allow_self=True)
    state = dev_status(path)
    if state["dirty"]:
        raise SumError(f"{path} has uncommitted or untracked work; commit, stash, or move it yourself. Nothing was removed.")
    run(["git", "-C", root, "worktree", "remove", str(path)], timeout=60)  # No --force: Git refuses dirty or locked trees.
    branch_removed = run(["git", "-C", root, "branch", "-d", marker["branch"]], check=False).returncode == 0  # -d never drops unmerged commits.
    return {"removed": str(path), "branch": marker["branch"], "branch_removed": branch_removed,
            "note": "Branch kept because it has unmerged commits; delete it yourself after merging." if not branch_removed else "Clean checkout and merged branch removed."}


# --- immutable runtime releases ---------------------------------------------------------------

def sha256_file(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def content_id(path):
    """A symlink is identified by its target text, a regular file by its content hash."""
    path = Path(path)
    return "link:" + os.readlink(path) if path.is_symlink() else "sha256:" + sha256_file(path)


def overlay_hashes(source_root):
    """Expected hashes of sum's Mesh overlay as shipped in the given source tree."""
    patches = Path(source_root) / "patches" / "herdr-mesh"
    return {"server_sha256": sha256_file(patches / "server.js"), "commands_sha256": sha256_file(patches / "commands.mjs")}


def link_tool(link, target):
    """Create a runtime symlink once. An existing link is never retargeted: a live process may depend on it."""
    link = Path(link)
    link.parent.mkdir(parents=True, exist_ok=True)
    if link.is_symlink():
        current = os.readlink(link)
        return {"link": str(link), "target": current, "created": False, "differs": current != str(target)}
    if link.exists():
        raise SumError(f"Refusing to replace non-symlink {link}")
    link.symlink_to(str(target))
    return {"link": str(link), "target": str(target), "created": True, "differs": False}


def mise_env(target):
    """Trust only the bundled mise.toml for this invocation; the content comes from a commit of the installation repository."""
    return {**os.environ, "MISE_TRUSTED_CONFIG_PATHS": str(Path(target) / "mise.toml")}


def resolve_tools(target):
    """Install the pinned tool versions (mise never prunes here) and link them into <target>/.local/bin."""
    target = Path(target)
    mise = shutil.which("mise")
    if not mise or not (target / "mise.toml").is_file():
        raise SumError("Missing mise or mise.toml; install mise, then run mise run setup.")
    env = mise_env(target)
    run([mise, "install"], cwd=target, env=env, timeout=900)
    links = {}
    for name in TOOLS:
        resolved = Path(run([mise, "which", name], cwd=target, env=env, timeout=60).stdout.strip()).resolve()
        if not resolved.is_file():
            raise SumError(f"mise resolved {name} to a missing file {resolved}")
        links[name] = link_tool(target / ".local" / "bin" / name, resolved)
    return links


def install_mesh(destination, source_root, local_mesh=None):
    """Clone, install, and overlay the pinned Mesh into a fresh directory; never into one that already exists."""
    destination, source_root = Path(destination), Path(source_root)
    if destination.exists():
        raise SumError(f"{destination} already exists; an installed Mesh is never rewritten in place.")
    node = source_root / ".local" / "bin" / "node"
    npm = node.resolve().parent / "npm"
    if not node.is_file() or not npm.is_file():
        raise SumError("Pinned node/npm are not linked; resolve tools first.")
    destination.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".mesh-", dir=destination.parent))
    mesh = staging / "herdr-mesh"
    try:
        origin = str(local_mesh) if local_mesh and Path(local_mesh, ".git").exists() and \
            run(["git", "-C", local_mesh, "cat-file", "-e", f"{MESH_REV}^{{commit}}"], check=False).returncode == 0 else MESH_REMOTE
        run(["git", "clone", "--no-checkout", *([] if origin != MESH_REMOTE else ["--filter=blob:none"]), origin, str(mesh)], timeout=600)
        run(["git", "-C", mesh, "checkout", "--detach", MESH_REV], timeout=120)
        if run(["git", "-C", mesh, "rev-parse", "HEAD"]).stdout.strip() != MESH_REV:
            raise SumError("Unexpected Mesh checkout")
        if not (mesh / "package-lock.json").is_file() or not (mesh / "LICENSE").is_file():
            raise SumError("Pinned Mesh checkout is incomplete")
        run([npm, "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=mesh, timeout=900)
        apply_overlay(source_root, mesh)
        os.rename(mesh, destination)
    finally:
        shutil.rmtree(staging, ignore_errors=True)
    return destination


def apply_overlay(source_root, mesh):
    """sum's documented runtime overlay for a freshly installed Mesh (see docs/DEPENDENCIES.md)."""
    source_root, mesh = Path(source_root), Path(mesh)
    shutil.copyfile(source_root / "patches/herdr-mesh/server.js", mesh / "dist/server.js")
    shutil.copyfile(source_root / "patches/herdr-mesh/commands.mjs", mesh / "dist/sum-commands.mjs")
    atomic_json(mesh / ".sum-patched", {"upstream": MESH_REV, **overlay_hashes(source_root)})


def mesh_state(source_root, mesh):
    """Compare an installed Mesh with the overlay in a source tree without touching either."""
    marker = Path(mesh) / ".sum-patched"
    if not marker.is_file():
        return {"installed": Path(mesh).exists(), "patched": False, "matches_source": False}
    value = read_json(marker)
    return {"installed": True, "patched": True, "upstream": value.get("upstream"),
            "matches_source": value.get("upstream") == MESH_REV and {k: value.get(k) for k in ("server_sha256", "commands_sha256")} == overlay_hashes(source_root)}


def write_herdr_skill(target):
    """Copy the release-matched Herdr skill beside the pinned binary."""
    target = Path(target)
    skill = run([target / ".local" / "bin" / "herdr", "--skill"], timeout=30).stdout
    path = target / ".local" / "skills" / "herdr" / "SKILL.md"
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.is_file() or path.read_text() != skill + "\n":
        path.write_text(skill + "\n")
    for parent in (target / ".agents" / "skills", target / ".claude" / "skills"):
        link_tool(parent / "herdr", "../../.local/skills/herdr")
    return path


def install_runtime(target, local_mesh=None):
    """Install every runtime dependency into one tree and prove the MCP server starts from it. Used for staging."""
    target = Path(target)
    resolve_tools(target)
    install_mesh(target / ".deps" / "herdr-mesh", target, local_mesh=local_mesh)
    write_herdr_skill(target)
    run([target / ".local" / "bin" / "node", target / "scripts" / "mcp_smoke.mjs"], timeout=60)


def source_files(root, sha):
    out = run(["git", "-C", root, "ls-tree", "-r", "-z", "--name-only", sha]).stdout
    return [name for name in out.split("\0") if name]


def archive_source(root, sha, destination):
    """Extract exactly the committed tree: no working-tree edits, no .sum, .deps, .local, or credentials."""
    try:
        result = subprocess.run(["git", "-C", str(root), "archive", "--format=tar", sha], capture_output=True, timeout=120)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise SumError(f"git archive: {exc}") from exc
    if result.returncode:
        raise SumError(f"git archive exited {result.returncode}: {result.stderr.decode(errors='replace')[-2000:]}")
    with tarfile.open(fileobj=io.BytesIO(result.stdout)) as archive:
        archive.extractall(destination, filter="data")


def tool_pins(target):
    try:
        return {k: v for k, v in tomllib.loads((Path(target) / "mise.toml").read_text()).get("tools", {}).items()}
    except (OSError, ValueError) as exc:
        raise SumError(f"Cannot read bundled mise.toml: {exc}") from exc


def build_manifest(store, root, sha, target):
    target = Path(target)
    files = {name: content_id(target / name) for name in source_files(root, sha)}
    tools = {}
    for name in TOOLS:
        link = target / ".local" / "bin" / name
        if not link.is_symlink():
            raise SumError(f"Release is missing the pinned tool link {link}")
        tools[name] = os.readlink(link)
    mesh = target / ".deps" / "herdr-mesh"
    patched = read_json(mesh / ".sum-patched")
    state = read_json(store.home / "state.json") if (store.home / "state.json").is_file() else {}
    return {"schema": RELEASE_SCHEMA, "kind": "sum-release", "sum_version": VERSION,
            "source": {"sha": sha, "tree": run(["git", "-C", root, "rev-parse", f"{sha}^{{tree}}"]).stdout.strip(), "repository": str(root)},
            "files": files,
            "dependencies": {"herdr_mesh": {"remote": MESH_REMOTE, "rev": MESH_REV, "path": ".deps/herdr-mesh", "overlay": patched},
                             "tools": {"pins": tool_pins(target), "paths": tools}},
            "contracts": {"herdr_cli": HERDR_VERSION, "mcp": MCP_CONTRACT},
            "supports": {"state_schema": [SCHEMA], "brief_schema": [BRIEF_SCHEMA]},
            "staged_at": now(), "staged_by": {"machine": machine(), "installation": str(root), "instance": state.get("instance")}}


def verify_release(path, expected_sha=None):
    """A bundle is usable only when its manifest and every referenced file agree. Raises SumError otherwise."""
    path = Path(path)
    manifest_path = path / RELEASE_MANIFEST
    if not manifest_path.is_file():
        raise SumError(f"{path}: no {RELEASE_MANIFEST}")
    manifest = read_json(manifest_path)
    if manifest.get("schema") != RELEASE_SCHEMA or manifest.get("kind") != "sum-release":
        raise SumError(f"{path}: unsupported release manifest")
    sha = manifest.get("source", {}).get("sha")
    if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-f]{40}", sha) or (expected_sha and sha != expected_sha):
        raise SumError(f"{path}: manifest source SHA is missing or mismatched")
    files = manifest.get("files")
    if not isinstance(files, dict) or not files:
        raise SumError(f"{path}: manifest lists no files")
    for name, expected in files.items():
        member = path / name
        if not member.is_symlink() and not member.is_file():
            raise SumError(f"{path}: missing {name}")
        if content_id(member) != expected:
            raise SumError(f"{path}: {name} does not match its manifest hash")
    for required in ("bin/sumctl", "bin/herdr-mesh", "bin/herdr-scoped", "lib/sumctl.py", "skills/worker/SKILL.md"):
        if required not in files:
            raise SumError(f"{path}: release lacks {required}")
    if not os.access(path / "bin" / "sumctl", os.X_OK):
        raise SumError(f"{path}: bin/sumctl is not executable")
    if (path / ".sum").exists():
        raise SumError(f"{path}: a release tree must not contain .sum state")
    mesh = path / ".deps" / "herdr-mesh"
    overlay = manifest.get("dependencies", {}).get("herdr_mesh", {}).get("overlay", {})
    marker = mesh / ".sum-patched"
    if not marker.is_file() or read_json(marker) != overlay or overlay.get("upstream") != MESH_REV:
        raise SumError(f"{path}: Mesh overlay marker does not match the manifest")
    for name, key in (("dist/server.js", "server_sha256"), ("dist/sum-commands.mjs", "commands_sha256")):
        if not (mesh / name).is_file() or sha256_file(mesh / name) != overlay.get(key):
            raise SumError(f"{path}: {name} does not match the recorded overlay hash")
    if not (mesh / "dist" / "index.js").is_file() or not (mesh / "node_modules" / "@modelcontextprotocol" / "sdk" / "package.json").is_file():
        raise SumError(f"{path}: Mesh dependencies are incomplete")
    paths = manifest.get("dependencies", {}).get("tools", {}).get("paths", {})
    for name in TOOLS:
        link = path / ".local" / "bin" / name
        if not link.is_symlink() or os.readlink(link) != paths.get(name) or not link.resolve().is_file():
            raise SumError(f"{path}: pinned tool {name} is missing or does not resolve")
    return manifest


def set_read_only(path, read_only=True):
    for current, dirs, names in os.walk(path):
        for name in dirs + names:
            member = Path(current) / name
            if member.is_symlink():
                continue
            mode = member.stat().st_mode
            member.chmod((mode & ~0o222) if read_only else (mode | 0o200))
    Path(path).chmod((Path(path).stat().st_mode & ~0o222) if read_only else (Path(path).stat().st_mode | 0o200))


def remove_tree(path):
    path = Path(path)
    if path.exists():
        set_read_only(path, read_only=False)
        shutil.rmtree(path, ignore_errors=True)


def release_summary(path, manifest, staged):
    return {"release": str(path), "sha": manifest["source"]["sha"], "staged": staged, "activated": False,
            "manifest": manifest,
            "note": "Staged only. No pointer, MCP configuration, live process, or installed dependency was changed; activation is a separate, explicit step."}


def stage(store, ref, installer=install_runtime):
    """Stage an immutable, commit-addressed runtime bundle under <installation>/.local/releases/<sha>.

    Everything happens in a private staging directory; the final name appears only after validation,
    with one atomic rename. Existing releases, the checkout's .deps/.local, and .sum are never modified.
    """
    root = installation_root(store)
    sha = run(["git", "-C", root, "rev-parse", "--verify", f"{ref}^{{commit}}", "--"]).stdout.strip()
    releases = root / RELEASES
    releases.mkdir(parents=True, exist_ok=True)
    final = releases / sha
    if final.exists():
        return release_summary(final, verify_release(final, sha), staged=False)
    staging = Path(tempfile.mkdtemp(prefix=f".staging-{sha[:12]}-", dir=releases))
    try:
        archive_source(root, sha, staging)
        if (staging / ".sum").exists() or (staging / RELEASE_MANIFEST).exists():
            raise SumError("The committed tree must not contain .sum or a release manifest.")
        installer(staging, local_mesh=root / ".deps" / "herdr-mesh")
        atomic_json(staging / RELEASE_MANIFEST, build_manifest(store, root, sha, staging))
        manifest = verify_release(staging, sha)
        set_read_only(staging)
        try:
            os.rename(staging, final)
        except OSError:
            if not final.exists():
                raise
            remove_tree(staging)  # Another staging of the same SHA won; the published bundle is complete by construction.
            return release_summary(final, verify_release(final, sha), staged=False)
    except BaseException as exc:
        remove_tree(staging)
        if isinstance(exc, (SumError, OSError, ValueError, KeyError, tarfile.TarError)):
            raise SumError(f"Staging {sha} failed and its partial bundle was removed; existing releases and the current setup are unchanged. {exc}") from exc
        raise
    return release_summary(final, manifest, staged=True)


def release_list(store):
    root = installation_root(store)
    releases = root / RELEASES
    rows, in_progress = [], []
    for entry in sorted(releases.iterdir()) if releases.is_dir() else []:
        if entry.name.startswith("."):
            in_progress.append(entry.name)
            continue
        try:
            manifest = verify_release(entry, entry.name)
            rows.append({"sha": entry.name, "path": str(entry), "ok": True, "sum_version": manifest["sum_version"], "staged_at": manifest["staged_at"]})
        except SumError as exc:
            rows.append({"sha": entry.name, "path": str(entry), "ok": False, "error": str(exc)})
    return {"installation": str(root), "releases": rows, "in_progress": in_progress, "activated": None,
            "note": "Nothing here is active; staged bundles are kept until you remove one deliberately. Automatic garbage collection is out of scope."}


def release_show(store, sha):
    root = installation_root(store)
    if not re.fullmatch(r"[0-9a-f]{7,40}", sha):
        raise SumError("Give a release by its commit SHA.")
    matches = [p for p in (root / RELEASES).glob(sha + "*") if not p.name.startswith(".")] if (root / RELEASES).is_dir() else []
    if len(matches) != 1:
        raise SumError(f"{len(matches)} staged releases match {sha}.")
    return release_summary(matches[0], verify_release(matches[0], matches[0].name), staged=False)


# --- atomic local updates and code-only rollback --------------------------------------------------

CURRENT = Path(".local") / "current"          # The installation default: a symlink read once per entrypoint invocation.
UPDATE_LOG = Path(".local") / "updates.jsonl"  # Concise, local, append-only history of selections; never the source of truth.
UPDATE_LOCK = Path(".local") / "update.lock"
PROBE_TASKS = 5


def default_branch(root):
    ref = run(["git", "-C", root, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"], check=False).stdout.strip()
    return ref.removeprefix("refs/remotes/origin/") if ref.startswith("refs/remotes/origin/") else "main"


def origin(root):
    result = run(["git", "-C", root, "remote", "get-url", "origin"], check=False)
    if result.returncode:
        raise SumError("The installation has no `origin` remote. Add one yourself; sum never changes remotes.")
    return result.stdout.strip()


def resolve_authorized(root, ref=None, fetch=True):
    """Resolve the update source to an immutable SHA that is merged on origin's default branch.

    Only refs/remotes/origin/* are updated (git fetch); the working tree, HEAD, and remotes are never touched,
    so a dirty checkout is reported, not reset. Unmerged work (a task branch, a development branch) is refused.
    """
    remote = origin(root)
    branch = default_branch(root)
    fetched = False
    if fetch:
        run(["git", "-C", root, "fetch", "--quiet", "origin", branch], timeout=300)
        fetched = True
    upstream = f"refs/remotes/origin/{branch}"
    if run(["git", "-C", root, "show-ref", "--verify", "--quiet", upstream], check=False).returncode:
        raise SumError(f"{upstream} is unknown here. Fetch origin first (omit --no-fetch).")
    tip = run(["git", "-C", root, "rev-parse", upstream]).stdout.strip()
    target = ref or upstream
    resolved = run(["git", "-C", root, "rev-parse", "--verify", f"{target}^{{commit}}", "--"], check=False)
    if resolved.returncode:
        raise SumError(f"Unknown revision {target!r}.")
    sha = resolved.stdout.strip()
    if run(["git", "-C", root, "merge-base", "--is-ancestor", sha, tip], check=False).returncode:
        raise SumError(f"{sha} is not merged on origin/{branch} ({tip}). sum activates only merged revisions of its own origin; "
                       "a development or task branch is never executed as an update.")
    dirty = run(["git", "-C", root, "status", "--porcelain", "--untracked-files=no"]).stdout.strip()
    return {"sha": sha, "ref": target, "origin": remote, "branch": branch, "tip": tip, "fetched": fetched,
            "checkout": {"head": run(["git", "-C", root, "rev-parse", "HEAD"]).stdout.strip(), "dirty": bool(dirty),
                         "note": "The checkout is left exactly as it is; an update never pulls, resets, or edits it."}}


def default_runtime(root):
    """What a new entrypoint invocation would run right now: the release behind .local/current, or the checkout."""
    link = root / CURRENT
    if not link.is_symlink():
        return {"kind": "checkout", "path": str(root), "sha": run(["git", "-C", root, "rev-parse", "HEAD"]).stdout.strip(),
                "manifest": None, "ok": True}
    target = os.readlink(link)
    path = (link.parent / target).resolve() if not os.path.isabs(target) else Path(target).resolve()
    row = {"kind": "release", "path": str(path), "link": target, "sha": path.name, "manifest": None, "ok": False}
    try:
        row["manifest"] = verify_release(path, path.name if re.fullmatch(r"[0-9a-f]{40}", path.name) else None)
        row["ok"] = True
    except SumError as exc:
        row["error"] = str(exc)
    return row


def runtime_contracts(runtime):
    """The contracts a runtime offers: from its manifest, or this module's constants for the checkout that runs now."""
    manifest = runtime.get("manifest")
    if manifest:
        return {"sum_version": manifest["sum_version"], "herdr_cli": manifest["contracts"]["herdr_cli"], "mcp": manifest["contracts"]["mcp"],
                "supports": manifest["supports"]}
    return {"sum_version": VERSION, "herdr_cli": HERDR_VERSION, "mcp": MCP_CONTRACT,
            "supports": {"state_schema": [SCHEMA], "brief_schema": [BRIEF_SCHEMA]}}


def task_contracts(store):
    """The state and brief schemas the recorded tasks actually use; legacy records count as schema 1."""
    rows = []
    for task in store.all():
        if task["status"] == "archived":
            continue
        try:
            brief_schema = read_versions(store, task).get("brief_schema", 1)
        except SumError as exc:
            brief_schema = None
            rows.append({"task": task["id"], "brief_schema": None, "error": str(exc)})
            continue
        rows.append({"task": task["id"], "brief_schema": brief_schema, "status": task["status"]})
    return rows


def probe_candidate(store, root, candidate, tasks):
    """Run the candidate's own helper read-only against representative records; a contract break shows up as a failure here, not after activation."""
    python = candidate / ".local" / "bin" / "python3"
    if not python.is_file():
        python = Path(sys.executable)
    env = {**os.environ, "SUM_INSTALL_ROOT": str(root)}
    probes = []
    argvs = [["--version"], ["--home", str(store.home), "status"]]
    argvs.extend(["--home", str(store.home), "show", t["task"]] for t in tasks[-PROBE_TASKS:])
    for argv in argvs:
        result = run([python, candidate / "lib" / "sumctl.py", *argv], timeout=60, check=False, env=env)
        probes.append({"argv": argv[-2:] if argv[0] == "--home" else argv, "ok": result.returncode == 0,
                       "detail": None if result.returncode == 0 else (result.stderr or result.stdout).strip()[-400:]})
    return probes


def compatibility(store, root, candidate_path, current):
    """Every check a selection must pass. `blocking` lists exact incompatibilities; `deferred` lists work that waits for clients."""
    blocking, deferred = [], []
    try:
        manifest = verify_release(candidate_path, candidate_path.name)
    except SumError as exc:
        return {"ok": False, "blocking": [f"candidate bundle: {exc}"], "deferred": [], "probes": [], "tasks": []}
    offered = runtime_contracts({"manifest": manifest})
    state = read_json(store.home / "state.json")
    if state.get("schema") not in offered["supports"]["state_schema"]:
        blocking.append(f"state schema {state.get('schema')} is not supported by the candidate ({offered['supports']['state_schema']})")
    tasks = task_contracts(store)
    for row in tasks:
        if row.get("error"):
            blocking.append(f"task {row['task']}: version sidecar unreadable: {row['error']}")
        elif row["brief_schema"] not in offered["supports"]["brief_schema"]:
            blocking.append(f"task {row['task']} uses brief schema {row['brief_schema']}, which the candidate does not support ({offered['supports']['brief_schema']}); "
                            "already-adopted task contracts are never downgraded implicitly")
    try:
        installed = ensure_version()
    except SumError as exc:
        installed = None
        blocking.append(f"installed Herdr: {exc}")
    if installed and offered["herdr_cli"] not in installed:
        blocking.append(f"candidate requires Herdr CLI {offered['herdr_cli']}; installed {installed!r}. A Herdr upgrade is a separate, global decision that this update never performs.")
    pins = manifest["dependencies"]["tools"]["pins"]
    for name in TOOLS:
        link = candidate_path / ".local" / "bin" / name
        if not link.resolve().is_file():
            blocking.append(f"pinned tool {name} does not resolve in the candidate")
    if not pins:
        blocking.append("candidate manifest has no tool pins")
    running = runtime_contracts(current)
    if running["mcp"] != offered["mcp"]:
        deferred.append({"what": "mcp", "from": running["mcp"], "to": offered["mcp"],
                         "note": "Already-connected MCP clients keep the server and tool set they started; they see the new tools only after the client itself restarts. Nothing is reloaded for them."})
    else:
        deferred.append({"what": "mcp", "note": "Already-running MCP servers keep their start tree until their client restarts; the tool contract is unchanged, so nothing is lost meanwhile."})
    if current["kind"] == "checkout" or current.get("sha") != run(["git", "-C", root, "rev-parse", "HEAD"]).stdout.strip():
        deferred.append({"what": "checkout-instructions", "checkout_head": run(["git", "-C", root, "rev-parse", "HEAD"]).stdout.strip(),
                         "note": "AGENTS.md and skills read by a plain harness come from the checkout, which this update leaves untouched. Refreshing running sessions' instructions is separate work."})
    probes = probe_candidate(store, root, candidate_path, tasks) if not blocking else []
    for probe in probes:
        if not probe["ok"]:
            blocking.append(f"candidate helper failed `{' '.join(probe['argv'])}`: {probe['detail']}")
    return {"ok": not blocking, "blocking": blocking, "deferred": deferred, "probes": probes, "tasks": tasks,
            "candidate": {"sha": manifest["source"]["sha"], **offered}, "current": {"kind": current["kind"], "sha": current.get("sha"), **running}}


def update_log(root, entry):
    path = root / UPDATE_LOG
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as out:
        out.write(json.dumps({"at": now(), **entry}, ensure_ascii=True) + "\n")
        out.flush()
        os.fsync(out.fileno())


def read_update_log(root, limit=20):
    path = root / UPDATE_LOG
    rows = []
    if path.is_file():
        for line in path.read_text(encoding="utf-8").splitlines():
            try:
                rows.append(json.loads(line))
            except ValueError:
                rows.append({"unparsed": line[:200]})
    return rows[-limit:]


@contextmanager
def activation_lock(root):
    """Held only around validation-of-selection and the single rename. Staging, fetching, and building happen before it."""
    path = root / UPDATE_LOCK
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a") as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as exc:
            raise SumError("Another update or rollback holds the activation lock; the current selection is unchanged. Retry after it finishes.") from exc
        try:
            yield
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


def select_default(root, target):
    """Atomically make <target> the installation default, or remove the pointer so the checkout serves again.

    A new symlink is fully created under a private name and then renamed over .local/current, so any observer
    sees the complete old selection or the complete new one. Nothing under .sum, .deps, or any release changes.
    """
    link = root / CURRENT
    if target is None:
        if link.is_symlink():
            os.unlink(link)
        return None
    relative = os.path.relpath(target, link.parent)
    tmp = link.parent / f".current-{uuid.uuid4().hex[:8]}"
    os.symlink(relative, tmp)
    try:
        os.replace(tmp, link)
    except BaseException:
        if tmp.is_symlink():
            os.unlink(tmp)
        raise
    directory = os.open(link.parent, os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)
    return relative


def post_check(store, root):
    """Prove the stable entrypoint serves a complete runtime after the switch: one read-only call through <installation>/bin/sumctl."""
    result = run([root / "bin" / "sumctl", "--home", store.home, "status"], timeout=60, check=False)
    return {"ok": result.returncode == 0, "detail": None if result.returncode == 0 else (result.stderr or result.stdout).strip()[-400:]}


def activate(store, root, target, action, source):
    """Validate under the lock, switch once, verify, log. On any failure the previous selection is still complete and serving."""
    with activation_lock(root):
        current = default_runtime(root)
        if target is not None:
            result = compatibility(store, root, target, current)
            if not result["ok"]:
                update_log(root, {"action": action, "result": "refused", "from": current.get("sha"), "to": target.name, "blocking": result["blocking"]})
                raise SumError(f"{action} refused; the current selection ({current['kind']} {current.get('sha')}) still serves. Exact incompatibilities: " + "; ".join(result["blocking"]))
            new_sha = target.name
        else:
            result = {"ok": True, "blocking": [], "deferred": [{"what": "checkout-instructions", "note": "The checkout serves again; its instructions match its HEAD."}], "probes": []}
            new_sha = current.get("sha") if current["kind"] == "checkout" else run(["git", "-C", root, "rev-parse", "HEAD"]).stdout.strip()
        if current["kind"] == ("checkout" if target is None else "release") and current.get("sha") == new_sha:
            return {"action": action, "changed": False, "default": current, "compatibility": result, "source": source,
                    "note": "Already the default; nothing changed."}
        update_log(root, {"action": action, "phase": "selecting", "from": {"kind": current["kind"], "sha": current.get("sha")},
                          "to": {"kind": "checkout" if target is None else "release", "sha": new_sha}, "source": source})
        select_default(root, target)
        after = default_runtime(root)
        check = post_check(store, root)
        update_log(root, {"action": action, "result": "selected" if check["ok"] else "selected-but-entrypoint-check-failed",
                          "from": {"kind": current["kind"], "sha": current.get("sha")}, "to": {"kind": after["kind"], "sha": after.get("sha")},
                          "deferred": [d["what"] for d in result["deferred"]], "post_check": check})
    if not check["ok"]:
        raise SumError(f"{action} switched the default to {after.get('sha')} but the entrypoint check failed: {check['detail']}. Run `update rollback` to select the previous runtime; records are untouched.")
    return {"action": action, "changed": True, "previous": {"kind": current["kind"], "sha": current.get("sha"), "path": current["path"]},
            "default": after, "compatibility": result, "post_check": check, "source": source,
            "note": "New entrypoint invocations and new dispatches use this default. Commands already running finish on the runtime they resolved; "
                    "connected MCP servers keep their start tree; task records, worktrees, and .sum were not touched."}


def update_check(store, args):
    root = installation_root(store)
    source = resolve_authorized(root, args.ref, fetch=not args.no_fetch)
    current = default_runtime(root)
    staged = (root / RELEASES / source["sha"]).is_dir()
    value = {"installation": str(root), "source": source, "default": {k: current.get(k) for k in ("kind", "sha", "path", "ok", "error")},
             "active": {"runtime": str(RUNTIME), "sha": current.get("sha") if str(RUNTIME) == current["path"] else None},
             "staged": staged, "up_to_date": current.get("sha") == source["sha"] and current["kind"] == "release"}
    if staged:
        value["compatibility"] = compatibility(store, root, root / RELEASES / source["sha"], current)
    value["note"] = ("Read-only apart from refs/remotes/origin. " +
                     ("This revision is already the default." if value["up_to_date"] else
                      "Compatibility was evaluated against the staged bundle." if staged else
                      "Not staged yet: `update stage` builds it without changing the default; `update apply` stages and activates."))
    return value


def update_stage(store, args, installer=install_runtime):
    root = installation_root(store)
    source = resolve_authorized(root, args.ref, fetch=not args.no_fetch)
    staged = stage(store, source["sha"], installer=installer)
    current = default_runtime(root)
    return {**staged, "source": source, "compatibility": compatibility(store, root, Path(staged["release"]), current),
            "note": "Staged and evaluated; the default is unchanged. `update apply` activates it."}


def update_apply(store, args, installer=install_runtime):
    root = installation_root(store)
    source = resolve_authorized(root, args.ref, fetch=not args.no_fetch)  # Network and authorization first, outside the lock.
    staged = stage(store, source["sha"], installer=installer)             # Build/install, still outside the lock.
    return activate(store, root, Path(staged["release"]), "apply", {k: source[k] for k in ("sha", "ref", "origin", "branch", "fetched")})


def update_rollback(store, args):
    root = installation_root(store)
    current = default_runtime(root)
    target = args.to
    if target is None:
        history = [e for e in read_update_log(root, limit=1000) if e.get("result") in {"selected", "selected-but-entrypoint-check-failed"}]
        last = next((e for e in reversed(history) if e.get("to", {}).get("sha") == current.get("sha") and e["to"].get("kind") == current["kind"]), None)
        if not last:
            raise SumError("No recorded previous selection for the current default. Name the target: `update rollback --to SHA` or `--to checkout`.")
        target = last["from"]["sha"] if last["from"]["kind"] == "release" else "checkout"
    if target == "checkout":
        return activate(store, root, None, "rollback", {"to": "checkout"})
    if not re.fullmatch(r"[0-9a-f]{7,40}", target):
        raise SumError("Roll back to a staged release SHA or `checkout`.")
    matches = [p for p in (root / RELEASES).glob(target + "*") if not p.name.startswith(".")] if (root / RELEASES).is_dir() else []
    if len(matches) != 1:
        raise SumError(f"{len(matches)} staged releases match {target}; rollback uses only bundles that are already staged.")
    return activate(store, root, matches[0], "rollback", {"to": matches[0].name})


def update_status(store):
    root = installation_root(store)
    current = default_runtime(root)
    releases = release_list(store)
    return {"installation": str(root), "default": current,
            "active": {"runtime": str(RUNTIME), "sum_version": VERSION, "is_default": str(RUNTIME) == current["path"],
                       "note": "The runtime this very command resolved. A command started before a switch keeps its own runtime until it exits."},
            "checkout": {"head": run(["git", "-C", root, "rev-parse", "HEAD"]).stdout.strip(),
                         "dirty": bool(run(["git", "-C", root, "status", "--porcelain", "--untracked-files=no"]).stdout.strip())},
            "releases": releases["releases"], "in_progress": releases["in_progress"], "history": read_update_log(root),
            "note": "Selection is the .local/current symlink; the history is a local log, not the source of truth. Running MCP servers and helpers are not enumerated: they keep the tree they started from."}


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--home", default=os.environ.get("SUM_HOME") or str(ROOT / ".sum"))
    p.add_argument("--version", action="version", version=f"sum {VERSION}")
    sub = p.add_subparsers(dest="command", required=True)
    sub.add_parser("doctor", help="Observe setup, Herdr context, and this pane's registered role; writes nothing")
    s = sub.add_parser("init", help="Explicitly register this pane's role in this instance; the first eligible pane claims coordinator")
    s.add_argument("--role", choices=ROLES, help="Requested role; omitted means coordinator if unowned, worker if dispatched, else developer")
    s.add_argument("--task", help="Task ID when explicitly registering as its dispatched worker")
    s.add_argument("--reclaim", action="store_true", help="Deliberate, identity-checked takeover of an absent coordinator pane; never rebinds tasks")
    for name in ("status", "inbox"):
        s = sub.add_parser(name)
        s.add_argument("--live", action="store_true", help="One bounded native-status lookup per task")
    for name in ("prepare", "dispatch"):
        s = sub.add_parser(name)
        s.add_argument("--repo", required=True)
        s.add_argument("--brief", required=True)
        s.add_argument("--harness", help="Explicit Herdr integration kind; omitted means the saved worker default, else the coordinator's own harness")
        s.add_argument("--model", help="Explicit model for this task only, passed through the verified flag of the resolved harness; never saved")
        s.add_argument("--reasoning", help="Explicit reasoning/effort level for this task only (harnesses with a verified flag); never saved")
        s.add_argument("--same-as-you", action="store_true", help="Explicit request for the coordinator's own harness with its native model, ignoring a saved worker default")
        s.add_argument("--preset", help="Expand a saved named preset (see `preset list`) for this task; --model/--reasoning/--arg refine it, a different --harness is refused")
        s.add_argument("--base", default="HEAD")
        s.add_argument("--kind", choices=["ship", "scout"], default="ship")
        s.add_argument("--approved", action="store_true")
        s.add_argument("--arg", action="append", default=[], help="An explicitly chosen harness argument; use --arg=-m for leading dashes")
    s = sub.add_parser("start")
    s.add_argument("task")
    s.add_argument("--arg", action="append", default=[])
    for name in ("show", "notice", "archive"):
        s = sub.add_parser(name)
        s.add_argument("task")
        if name == "notice":
            s.add_argument("--to", choices=["parent", "worker"], default="parent")
        if name == "archive":
            s.add_argument("--acknowledge", action="store_true", help="Confirm work has been inspected and preserved; this does not stop or delete anything")
    for name in ("ask", "answer", "report"):
        s = sub.add_parser(name)
        s.add_argument("task")
        if name == "ask":
            s.add_argument("--key", help="Stable question key for idempotent re-submission")
        if name == "answer":
            s.add_argument("question")
        if name == "report":
            s.add_argument("--handoff", help="Optional bounded JSON handoff (outcome, candidate SHA, files, checks, review, unresolved decisions, next action, PR identity claim)")
        g = s.add_mutually_exclusive_group(required=True)
        g.add_argument("--text")
        g.add_argument("--file")
    s = sub.add_parser("resolve")
    s.add_argument("task")
    s.add_argument("question")
    s = sub.add_parser("review", help="Reviewer pane: append independent findings for one candidate; never replaces the worker report")
    s.add_argument("task")
    s.add_argument("--verdict", required=True, choices=REVIEW_VERDICTS)
    s.add_argument("--candidate", help="Full 40-hex SHA the findings cover")
    g = s.add_mutually_exclusive_group(required=True)
    g.add_argument("--text")
    g.add_argument("--file")
    s = sub.add_parser("verify", help="Coordinator only: append the coordinator's own verification result for one exact candidate")
    s.add_argument("task")
    s.add_argument("--candidate", required=True, help="Full 40-hex SHA that was actually verified")
    s.add_argument("--result", required=True, choices=VERIFY_RESULTS)
    g = s.add_mutually_exclusive_group(required=True)
    g.add_argument("--text")
    g.add_argument("--file")
    s = sub.add_parser("pr", help="Exact PR identity from an authenticated GitHub observation; never parsed from prose or guessed from branch names")
    g = s.add_subparsers(dest="pr_command", required=True)
    x = g.add_parser("reconcile", help="Coordinator only: inspect PR --number in the task repository with gh and record its exact identity, state, and mismatches")
    x.add_argument("task")
    x.add_argument("--number", type=int, required=True)
    x.add_argument("--repo", help="owner/name; must equal the task repository's GitHub identity")
    x.add_argument("--replace", action="store_true", help="Switch a task from one recorded PR number to another after inspecting both")
    s = sub.add_parser("cleanup", help="Coordinator only: inspect (default) or --apply the guarded removal of one merged task's workspace and clean checkout via native Herdr, then archive; the branch and records stay")
    s.add_argument("task")
    s.add_argument("--apply", action="store_true", help="Remove the verified workspace/checkout without force and archive the record; without it, only inspect and persist the plan")
    s.add_argument("--reviewer-only", action="store_true", help="Inspect or close only the bound reviewer pane (needs saved findings and an exited occupant); the checkout is untouched")
    s.add_argument("--number", type=int, help="Observe this PR number when no complete PR identity is recorded yet")
    s = sub.add_parser("bind")
    s.add_argument("task")
    s.add_argument("--worker-pane", help="Explicitly adopt an existing worker; never launch a replacement")
    s.add_argument("--parent-only", action="store_true")
    s = sub.add_parser("backup")
    s.add_argument("destination")
    s = sub.add_parser("settings", help="Show or set the validated optional admission settings in .sum/settings.json (defaults: 2 global, 1 per repository)")
    g = s.add_subparsers(dest="settings_command", required=True)
    g.add_parser("show", help="Current limits, their source, and held slots; writes nothing")
    x = g.add_parser("set", help="Coordinator only: write validated capacity values atomically; future admissions only, nothing running is touched")
    x.add_argument("--global", dest="global_limit", type=int, help=f"Execution slots across all repositories (1-{CAPACITY_MAX})")
    x.add_argument("--per-repository", dest="per_repository", type=int, help=f"Execution slots per repository (1-{CAPACITY_MAX}, at most --global)")
    x.add_argument("--worker-harness", help="Save the default worker harness for future dispatches (the coordinator keeps its own)")
    x.add_argument("--worker-model", help="Save the default worker model for the saved worker harness (needs a verified adapter)")
    x.add_argument("--worker-reasoning", help="Save the default worker reasoning/effort level for the saved worker harness")
    x.add_argument("--worker-preset", help="Save a preset name as the worker default for future dispatches; expanded at each prepare")
    x.add_argument("--clear-worker", action="store_true", help="Remove the saved worker default: workers run the coordinator's harness again")
    x.add_argument("--reviewer-preset", help="Save the preset the coordinator uses when it launches a reviewer itself; never applied when MADE or the repository's own tool owns review")
    x.add_argument("--clear-reviewer", action="store_true", help="Remove the saved reviewer preset")
    s = sub.add_parser("preset", help="Named launch presets in .sum/settings.json: validated harness/model/argv shortcuts expanded at dispatch, not agents or roles")
    g = s.add_subparsers(dest="preset_command", required=True)
    g.add_parser("list", help="Saved presets with harness, model, reasoning, args, and revision; writes nothing")
    x = g.add_parser("show", help="One preset and the exact argv it expands to; writes nothing")
    x.add_argument("name")
    x = g.add_parser("set", help="Coordinator only: create or revise one preset atomically; prepared and running tasks keep their own specification")
    x.add_argument("name")
    x.add_argument("--harness", help="Herdr integration kind (required when creating; changing it drops the old harness's model/reasoning/args)")
    x.add_argument("--model", help="Model passed through the harness's verified flag")
    x.add_argument("--reasoning", help="Reasoning/effort level passed through the harness's verified flag")
    x.add_argument("--arg", action="append", default=None, help="Replace the preset's native args with these exact tokens (repeatable; use --arg=-m for leading dashes)")
    x.add_argument("--clear-model", action="store_true", help="Drop the preset's model")
    x.add_argument("--clear-reasoning", action="store_true", help="Drop the preset's reasoning")
    x.add_argument("--clear-args", action="store_true", help="Drop the preset's native args")
    x = g.add_parser("delete", help="Coordinator only: remove one preset that no default references; tasks prepared with it are unaffected")
    x.add_argument("name")
    s = sub.add_parser("herdr", help="Session-scoped native CLI bridge for Mesh; no protocol reimplementation")
    s.add_argument("args", nargs=argparse.REMAINDER)
    s = sub.add_parser("dev", help="Prepare, list, or remove isolated self-development checkouts of this installation")
    d = s.add_subparsers(dest="dev_command", required=True)
    x = d.add_parser("prepare", help="Create or reopen .sum/dev/NAME on branch sum-dev/NAME; optionally open an ordinary Herdr pane there")
    x.add_argument("--name", required=True)
    x.add_argument("--base", default="HEAD")
    x.add_argument("--pane", action="store_true", help="Also create a Herdr workspace whose root pane starts in the checkout")
    d.add_parser("list")
    x = d.add_parser("remove", help="Remove a clean development checkout with plain git worktree remove; dirty work is preserved")
    x.add_argument("--name", required=True)
    s = sub.add_parser("brief", help="Inspect, regenerate, request, or adopt versioned worker brief revisions; the brief a worker reads is never rewritten")
    b = s.add_subparsers(dest="brief_command", required=True)
    for name, text in (("list", "Show recorded revisions, their integrity, and refresh state"),
                       ("regenerate", "Stage a new numbered revision from the approved task, current decisions, and current policy (coordinator only)"),
                       ("request", "Mark the latest intact revision as requested for the worker (coordinator only); sends nothing"),
                       ("adopt", "Worker: record that the requested revision is now the brief being followed")):
        x = b.add_parser(name, help=text)
        x.add_argument("task")
        if name in ("request", "adopt"):
            x.add_argument("revision")
    s = sub.add_parser("refresh", help="Rolling refresh of running sessions: stage and request each target's next revision, attempt one bounded delivery, report status")
    f = s.add_subparsers(dest="refresh_command", required=True)
    x = f.add_parser("request", help="Coordinator only: regenerate and request revisions for the coordinator and/or tasks, persist, then try one delivery each")
    x.add_argument("--task", action="append", help="Refresh only this task (repeatable)")
    x.add_argument("--coordinator", action="store_true", help="Refresh only the coordinator's operating contract")
    x = f.add_parser("status", help="Bounded refresh summary from saved records; writes nothing")
    x.add_argument("--task", action="append")
    x = f.add_parser("adopt", help="Coordinator: record the receipt of the requested contract revision")
    x.add_argument("--coordinator", action="store_true", required=True)
    x.add_argument("revision")
    s = sub.add_parser("release", help="Stage, list, or inspect immutable runtime releases under .local/releases; staging never activates")
    r = s.add_subparsers(dest="release_command", required=True)
    x = r.add_parser("stage", help="Stage the runtime bundle for a commit (default HEAD) with its own dependencies; nothing live changes")
    x.add_argument("--ref", default="HEAD")
    r.add_parser("list")
    x = r.add_parser("show")
    x.add_argument("sha")
    s = sub.add_parser("update", help="Check, stage, atomically apply, inspect, or roll back the installation default runtime; existing work keeps running")
    u = s.add_subparsers(dest="update_command", required=True)
    for name, text in (("check", "Fetch origin, resolve the merged revision, and report compatibility; changes no selection"),
                       ("stage", "Check plus build the release bundle; the default is unchanged"),
                       ("apply", "Stage if needed, validate coexistence under the activation lock, then switch the default in one rename (coordinator only)")):
        x = u.add_parser(name, help=text)
        x.add_argument("--ref", help="A revision merged on origin's default branch; default: that branch tip")
        x.add_argument("--no-fetch", action="store_true", help="Use the already fetched origin refs (offline host)")
    u.add_parser("status", help="Default and active runtime, checkout state, staged releases, recent selections; writes nothing")
    x = u.add_parser("rollback", help="Atomically select the previous runtime (or --to SHA|checkout) after compatibility checks; records are never touched (coordinator only)")
    x.add_argument("--to")
    return p


def main(argv=None):
    if ROOT == RUNTIME and (RUNTIME / RELEASE_MANIFEST).is_file():
        print(json.dumps({"error": f"{RUNTIME} is an immutable release tree. Run the installation's bin/sumctl, which selects a runtime and keeps state in its own .sum; a release never owns state."}), file=sys.stderr)
        return 1
    args = parser().parse_args(argv)
    try:
        store = Store(args.home)
        guard_candidate(store, {"release": lambda: f"release-{args.release_command}", "brief": lambda: f"brief-{args.brief_command}", "settings": lambda: f"settings-{args.settings_command}", "preset": lambda: f"preset-{args.preset_command}",
                                "update": lambda: f"update-{args.update_command}", "refresh": lambda: f"refresh-{args.refresh_command}",
                                "pr": lambda: f"pr-{args.pr_command}"}.get(args.command, lambda: args.command)())
        if args.command == "doctor":
            value = doctor(store)
            emit(value)
            return 0 if value["ok"] else 1
        if args.command == "init":
            value = init(store, args)
        elif args.command in {"status", "inbox"}:
            value = status(store, args.live, args.command == "inbox")
        elif args.command in {"prepare", "dispatch"}:
            task = prepare(store, args)
            value = start(store, task["id"]) if args.command == "dispatch" else task  # --arg values are already part of the persisted launch.
        elif args.command == "start":
            value = start(store, args.task, args.arg)
        elif args.command == "show":
            task = store.read(args.task)
            try:
                value = {**task, "versions": versions_view(store, task)}
            except SumError as exc:
                value = {**task, "versions": None, "versions_error": str(exc)}
            value["evidence_view"] = evidence_view(task)
        elif args.command == "review":
            value = review(store, args)
        elif args.command == "verify":
            value = verify(store, args)
        elif args.command == "pr":
            value = pr_reconcile(store, args)
        elif args.command == "ask":
            value = ask(store, args)
        elif args.command == "answer":
            value = answer(store, args)
        elif args.command == "resolve":
            value = resolve(store, args)
        elif args.command == "report":
            value = report(store, args)
        elif args.command == "notice":
            value = notify(store, args.task, args.to, "saved task state needs attention")
        elif args.command == "cleanup":
            value = cleanup(store, args)
        elif args.command == "archive":
            if not args.acknowledge:
                raise SumError("Use --acknowledge only after inspecting/preserving the work. This command never stops an agent or deletes a checkout.")
            require_coordinator(store, context())
            with store.lock():
                task = store.read(args.task)
                if any(q["status"] != "applied" for q in task["questions"]):
                    raise SumError("Outstanding questions must be answered and applied before archiving.")
                task["status"] = "archived"
                store.save(task)
            value = {"archived": args.task, "worktree_preserved": task["worktree"], "processes_untouched": True}
        elif args.command == "bind":
            ctx = context()
            require_coordinator(store, ctx)
            if not args.parent_only and not args.worker_pane:
                raise SumError("Specify --parent-only or --worker-pane. Binding never creates a replacement.")
            with store.lock():
                task = store.read(args.task)
                if args.parent_only and task["machine"] != machine():
                    raise SumError("Cross-machine restore needs explicit worktree recovery, not a parent-only rebind.")
                if args.worker_pane:
                    observed = agent_observation(ctx["session"], args.worker_pane)
                    cwd = observed.get("cwd") or observed.get("working_directory")
                    if not cwd or Path(cwd).resolve() != Path(task["worktree"]).resolve():
                        raise SumError("Worker cwd does not match the recorded worktree.")
                    task.update(pane=args.worker_pane, session=ctx["session"], machine=machine())
                task["parent"] = ctx
                store.save(task)
            value = task
        elif args.command == "backup":
            value = backup(store, args.destination)
        elif args.command == "settings":
            if args.settings_command == "show":
                value = capacity_view(store)
            else:
                require_coordinator(store, context())
                changes = {k: v for k, v in (("global", args.global_limit), ("per_repository", args.per_repository)) if v is not None}
                worker = {k: v for k, v in (("harness", args.worker_harness), ("model", args.worker_model), ("reasoning", args.worker_reasoning)) if v is not None}
                if args.clear_worker and (worker or args.worker_preset is not None):
                    raise SumError("--clear-worker conflicts with --worker-* values.")
                if args.worker_preset is not None and worker:
                    raise SumError("--worker-preset conflicts with --worker-harness/--worker-model/--worker-reasoning: a default is either a preset reference or a plain specification.")
                if args.clear_reviewer and args.reviewer_preset is not None:
                    raise SumError("--clear-reviewer conflicts with --reviewer-preset.")
                if not changes and not worker and args.worker_preset is None and not args.clear_worker and args.reviewer_preset is None and not args.clear_reviewer:
                    raise SumError("Give --global, --per-repository, --worker-harness/--worker-model/--worker-reasoning, --worker-preset, --clear-worker, --reviewer-preset, or --clear-reviewer.")
                value = write_settings(store, changes, worker, args.clear_worker, worker_preset=args.worker_preset,
                                       reviewer_preset=args.reviewer_preset, clear_reviewer=args.clear_reviewer)
        elif args.command == "preset":
            if args.preset_command == "list":
                value = preset_list(store)
            elif args.preset_command == "show":
                value = preset_show(store, args.name)
            else:
                require_coordinator(store, context())
                if args.preset_command == "delete":
                    value = delete_preset(store, args.name)
                else:
                    clear = tuple(f for f, on in (("model", args.clear_model), ("reasoning", args.clear_reasoning), ("args", args.clear_args)) if on)
                    if ("model" in clear and args.model) or ("reasoning" in clear and args.reasoning) or ("args" in clear and args.arg is not None):
                        raise SumError("--clear-* conflicts with a value for the same field.")
                    value = write_preset(store, args.name, harness=args.harness, model=args.model, reasoning=args.reasoning, args=args.arg, clear=clear)
        elif args.command == "dev":
            value = {"prepare": lambda: dev_prepare(store, args), "list": lambda: dev_list(store),
                     "remove": lambda: dev_remove(store, args)}[args.dev_command]()
        elif args.command == "brief":
            if args.brief_command == "list":
                task = store.read(args.task)
                value = {"task": args.task, "brief_path": task.get("brief_path"), **versions_view(store, task)}
            elif args.brief_command == "adopt":
                value = adopt_brief(store, args.task, args.revision)
            else:
                require_coordinator(store, context())
                value = regenerate_brief(store, args.task) if args.brief_command == "regenerate" else request_brief(store, args.task, args.revision)
        elif args.command == "refresh":
            if args.refresh_command == "status":
                value = refresh_status(store, args)
            elif args.refresh_command == "adopt":
                require_coordinator(store, context())
                value = adopt_contract(store, args.revision)
            else:
                value = refresh_request(store, args)
        elif args.command == "release":
            value = {"stage": lambda: stage(store, args.ref), "list": lambda: release_list(store),
                     "show": lambda: release_show(store, args.sha)}[args.release_command]()
        elif args.command == "update":
            if args.update_command in {"apply", "rollback"}:
                require_coordinator(store, context())  # Task text, a worker, or a developer pane never authorizes an update.
            value = {"check": lambda: update_check(store, args), "stage": lambda: update_stage(store, args),
                     "apply": lambda: update_apply(store, args), "status": lambda: update_status(store),
                     "rollback": lambda: update_rollback(store, args)}[args.update_command]()
        elif args.command == "herdr":
            native_args = args.args[1:] if args.args and args.args[0] == "--" else args.args
            # Scope: the caller's own verified pane, registered in this instance. No saved-context borrowing.
            try:
                ctx = context()
            except SumError as exc:
                raise SumError(f"{exc} The bridge never borrows a saved coordinator context.") from exc
            registration = store.registration(ctx) if store.designated() else None
            if not registration:
                raise SumError(f"Pane {ctx['pane']} in session {ctx['session']} is not registered with {store.home}. Run ./bin/sumctl init there first; a development checkout gets no access to another instance's panes.")
            if registration["role"] == "developer" and tuple(native_args[:2]) not in READ_ONLY:
                raise SumError("Developer sessions may only observe through the bridge. Coordination commands need the registered coordinator pane.")
            print(herdr(native_args, session=ctx["session"], raw=True, timeout=70), end="")
            return 0
        else:
            raise SumError("Unknown command")
        emit(value)
        return 0
    except (SumError, OSError, ValueError, KeyError) as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=True), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
