from __future__ import annotations

from copy import deepcopy
import re
import uuid


SCHEMA = 1
HELD_STATES = frozenset({"held", "observing", "starting", "running", "uncertain"})
STATES = HELD_STATES | {"released"}
KINDS = frozenset({"worker", "verifier"})
SHA40 = re.compile(r"[0-9a-f]{40}\Z")


class ReservationFormatError(Exception):
    pass


def new_attempt(kind, owner, checkout, stamp, *, state="held", candidate=None):
    if kind not in KINDS or state not in STATES:
        raise ReservationFormatError("invalid new reservation")
    return {
        "id": "x-" + uuid.uuid4().hex[:12],
        "kind": kind,
        "state": state,
        "generation": 1,
        "owner": deepcopy(owner),
        "checkout": checkout,
        "candidate": candidate,
        "created_at": stamp,
        "updated_at": stamp,
        "observations": [],
    }


def new_execution(worker):
    return {"schema": SCHEMA, "worker": worker, "verifiers": []}


def _pid(value):
    return type(value) is int and value > 0


def _argv(value):
    return isinstance(value, list) and value and all(isinstance(item, str) and item for item in value)


def _occupant(value, kind):
    if not isinstance(value, dict):
        return False
    if kind == "worker":
        return set(value) == {"machine", "session", "pane", "checkout", "harness", "name", "shell_pid", "pid", "argv"} and \
            all(isinstance(value[field], str) and value[field] for field in ("machine", "session", "pane", "checkout")) and \
            (value["harness"] is None or isinstance(value["harness"], str)) and \
            (value["name"] is None or isinstance(value["name"], str)) and \
            (value["shell_pid"] is None or _pid(value["shell_pid"])) and \
            (value["pid"] is None or _pid(value["pid"])) and \
            (value["argv"] is None or _argv(value["argv"]))
    return set(value) == {"machine", "pid", "argv", "checkout"} and \
        isinstance(value["machine"], str) and value["machine"] and _pid(value["pid"]) and \
        _argv(value["argv"]) and isinstance(value["checkout"], str) and value["checkout"]


def _observation(value):
    if not isinstance(value, dict) or not isinstance(value.get("at"), str) or not value["at"] or \
            not isinstance(value.get("outcome"), str) or not value["outcome"]:
        return False
    if "pid" in value and value["pid"] is not None and not _pid(value["pid"]):
        return False
    if "argv" in value and not _argv(value["argv"]):
        return False
    for field in ("reason", "checkout", "pane", "workspace", "descendant_error"):
        if field in value and value[field] is not None and not isinstance(value[field], str):
            return False
    return all(isinstance(value[field], bool) for field in ("checkout_present",) if field in value)


def _attempt(value, expected_kind):
    if not isinstance(value, dict):
        raise ReservationFormatError(f"{expected_kind} reservation is not an object")
    required = {"id", "kind", "state", "generation", "owner", "checkout", "created_at", "updated_at", "observations"}
    missing = sorted(required - set(value))
    if missing:
        raise ReservationFormatError(f"{expected_kind} reservation lacks {missing}")
    if not isinstance(value["id"], str) or not value["id"].startswith("x-"):
        raise ReservationFormatError(f"{expected_kind} reservation has an invalid id")
    if not isinstance(value["kind"], str) or not isinstance(value["state"], str) or value["kind"] != expected_kind or value["state"] not in STATES:
        raise ReservationFormatError(f"{expected_kind} reservation has an invalid kind or state")
    if isinstance(value["generation"], bool) or not isinstance(value["generation"], int) or value["generation"] < 1:
        raise ReservationFormatError(f"{expected_kind} reservation has an invalid generation")
    if not isinstance(value["owner"], dict) or set(value["owner"]) != {"machine", "session", "pane"} or \
            not isinstance(value["owner"].get("machine"), str) or not isinstance(value["owner"].get("session"), str) or \
            (value["owner"].get("pane") is not None and not isinstance(value["owner"].get("pane"), str)) or not isinstance(value["observations"], list):
        raise ReservationFormatError(f"{expected_kind} reservation has invalid owner or observations")
    if value["checkout"] is not None and not isinstance(value["checkout"], str):
        raise ReservationFormatError(f"{expected_kind} reservation has an invalid checkout")
    if value.get("candidate") is not None and (not isinstance(value["candidate"], str) or not SHA40.fullmatch(value["candidate"])):
        raise ReservationFormatError(f"{expected_kind} reservation has an invalid candidate")
    if "occupant" in value and not _occupant(value["occupant"], expected_kind):
        raise ReservationFormatError(f"{expected_kind} reservation has an invalid occupant")
    if not all(_observation(row) for row in value["observations"]):
        raise ReservationFormatError(f"{expected_kind} reservation has an invalid observation")
    for field in ("operation_pid", "observer_pid"):
        if field in value and not _pid(value[field]):
            raise ReservationFormatError(f"{expected_kind} reservation has an invalid {field}")
    return value


def execution(task):
    if "execution" not in task:
        return None
    value = task["execution"]
    if not isinstance(value, dict) or value.get("schema") != SCHEMA or set(value) != {"schema", "worker", "verifiers"}:
        raise ReservationFormatError("execution record has an invalid shape")
    worker = _attempt(value["worker"], "worker")
    verifiers = value["verifiers"]
    if not isinstance(verifiers, list):
        raise ReservationFormatError("verifier reservations are not a list")
    for verifier in verifiers:
        _attempt(verifier, "verifier")
    ids = [worker["id"], *(row["id"] for row in verifiers)]
    if len(ids) != len(set(ids)):
        raise ReservationFormatError("execution record contains duplicate attempt ids")
    return {"schema": SCHEMA, "worker": worker, "verifiers": verifiers}


def held(task):
    value = execution(task)
    if value is None:
        return [] if task.get("status") == "archived" else [{"id": f"legacy:{task.get('id')}", "kind": "worker", "state": "held"}]
    return [row for row in [value["worker"], *value["verifiers"]] if row["state"] in HELD_STATES]


def worker(task):
    value = execution(task)
    if value is None:
        raise ReservationFormatError("legacy task has no adoptable worker reservation")
    return value["worker"]


def transition(task, attempt_id, state, stamp, *, observation=None, expected_generation=None):
    if state not in STATES:
        raise ReservationFormatError("invalid reservation transition state")
    value = execution(task)
    if value is None:
        raise ReservationFormatError("legacy task has no execution reservation")
    rows = [value["worker"], *value["verifiers"]]
    matches = [row for row in rows if row["id"] == attempt_id]
    if len(matches) != 1:
        raise ReservationFormatError(f"attempt {attempt_id} is not current")
    row = matches[0]
    if expected_generation is not None and row["generation"] != expected_generation:
        raise ReservationFormatError(f"attempt {attempt_id} changed during observation")
    row["state"] = state
    row["generation"] += 1
    row["updated_at"] = stamp
    if observation is not None:
        row["observations"] = row["observations"][-19:] + [deepcopy(observation)]
    return row


def replace_worker(task, attempt):
    value = execution(task)
    if value is None:
        raise ReservationFormatError("legacy task has no execution reservation")
    value["worker"] = attempt
    task["execution"] = value


def add_verifier(task, attempt):
    value = execution(task)
    if value is None:
        raise ReservationFormatError("legacy task has no execution reservation")
    value["verifiers"].append(attempt)
    task["execution"] = value
