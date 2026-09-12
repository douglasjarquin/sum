from __future__ import annotations

from datetime import datetime
import os
import re
import uuid


def valid_key(value):
    return isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", value) is not None


def timestamp(value):
    if not isinstance(value, str):
        return False
    try:
        return datetime.fromisoformat(value).utcoffset() is not None
    except ValueError:
        return False


def ledger(api, task):
    value = task.get("repairs")
    if "repairs" not in task:
        value = {"schema": 1, "default_allowance": 2, "consumed": 0, "operations": [], "grants": []}
        task["repairs"] = value
    valid = (isinstance(value, dict) and type(value.get("schema")) is int and value["schema"] == 1
             and value.get("default_allowance") == 2
             and type(value.get("consumed")) is int
             and isinstance(value.get("operations"), list)
             and isinstance(value.get("grants"), list)
             and value["consumed"] == len(value["operations"]))
    if not valid:
        raise api.SumError("Malformed repair accounting; corrective work is refused.")
    keys = set()
    ids = set()
    for operation in value["operations"]:
        if (not isinstance(operation, dict)
                or not valid_key(operation.get("key"))
                or not isinstance(operation.get("kind"), str) or operation["kind"] not in {"send", "resume"}
                or (operation["kind"], operation["key"]) in keys
                or not isinstance(operation.get("state"), str)
                or operation.get("state") not in {"reserved", "in-flight", "submitted", "uncertain"}
                or not timestamp(operation.get("created_at"))
                or type(operation.get("pid")) is not int or operation["pid"] <= 0
                or not all(isinstance(operation.get(field), str) and operation[field]
                           for field in ("id", "attempt", "text", "created_at"))
                or not re.fullmatch(r"r-[0-9a-f]{12}", operation["id"])
                or not re.fullmatch(r"x-[0-9a-f]{12}", operation["attempt"])
                or operation["id"] in ids):
            raise api.SumError("Malformed repair operation; corrective work is refused.")
        keys.add((operation["kind"], operation["key"]))
        ids.add(operation["id"])
    questions = set()
    for grant in value["grants"]:
        if (not isinstance(grant, dict) or type(grant.get("additional")) is not int
                or grant["additional"] <= 0
                or not all(isinstance(grant.get(field), str) and grant[field] for field in ("question", "text", "at"))
                or not timestamp(grant.get("at"))
                or grant["question"] in questions
                or not isinstance(grant.get("by"), dict)
                or not all(isinstance(grant["by"].get(field), str) and grant["by"][field] for field in ("machine", "session", "pane"))):
            raise api.SumError("Malformed repair grant; corrective work is refused.")
        question = next((q for q in task["questions"] if isinstance(q, dict) and q.get("id") == grant["question"]), None)
        if (not question or not isinstance(question.get("decision"), dict)
                or question["decision"].get("kind") != "repair-allowance"
                or question.get("answer") != grant["text"]
                or not isinstance(question.get("status"), str) or question["status"] not in {"answered", "applied"}):
            raise api.SumError("Repair grant has no matching recorded decision.")
        questions.add(grant["question"])
    return value


def allowance(value):
    return value["default_allowance"] + sum(grant["additional"] for grant in value["grants"])


def refuse_active(api, task, *, launch=False):
    if "repairs" not in task:
        return
    for operation in ledger(api, task)["operations"]:
        if launch and operation["state"] == "reserved" and api.reservations.worker(task).get("repair") == operation["id"]:
            continue
        if operation["state"] in {"reserved", "in-flight"} and api._reservation_process_running(operation["pid"]):
            raise api.SumError("A corrective instruction is in flight; execution changes are refused.")


def check_allowance(api, store, task):
    value = ledger(api, task)
    if value["consumed"] >= allowance(value):
        error = exhaustion(api, task)
        store.save(task)
        raise error


def record_resume(api, task, successor):
    value = ledger(api, task)
    operation = {"id": "r-" + uuid.uuid4().hex[:12], "kind": "resume", "key": "resume-" + successor["resumes"],
                 "attempt": successor["id"], "text": "Resume the approved task.", "created_at": api.now(),
                 "state": "reserved", "pid": os.getpid()}
    value["operations"].append(operation)
    value["consumed"] += 1
    successor["repair"] = operation["id"]


def launch_record(api, task):
    worker = api.reservations.worker(task)
    if not worker.get("resumes"):
        return None
    operation = next((op for op in ledger(api, task)["operations"] if op["id"] == worker.get("repair")), None)
    if not operation or operation["attempt"] != worker["id"] or operation["state"] != "reserved":
        raise api.SumError("Resumed worker has no matching charged repair operation; start is refused.")
    return operation


def exhaustion(api, task):
    limit = allowance(ledger(api, task))
    decision = {"kind": "repair-allowance", "allowance": limit}
    question = next((item for item in task["questions"] if item.get("decision") == decision), None)
    if question is None:
        question = {"id": "q-" + uuid.uuid4().hex[:10], "key": None,
                    "text": "The controlled repair allowance is exhausted. Decide whether to authorize additional iterations.",
                    "status": "open", "created_at": api.now(), "answer": None,
                    "decision": decision}
        task["questions"].append(question)
        task["status"] = "waiting"
        api.supersede_attention(task, "question " + question["id"])
    return api.SumError(f"Repair allowance is exhausted; human decision {question['id']} is required.")


def extend(api, store, args):
    ctx = api.context()
    api.require_coordinator(store, ctx)
    if not args.approved or type(args.additional) is not int or args.additional <= 0:
        raise api.SumError("A positive additional allowance and --approved actual human decision are required.")
    text = api.text_input(args)
    with store.lock():
        task = store.read(args.task)
        store.check_machine(task)
        value = ledger(api, task)
        prior = next((grant for grant in value["grants"] if grant["question"] == args.question), None)
        if prior:
            if prior["additional"] != args.additional or prior["text"] != text:
                raise api.SumError("This budget decision already has a different grant.")
            return {"task": args.task, "grant": prior, "duplicate": True}
        question = next((q for q in task["questions"] if q["id"] == args.question), None)
        if (not question or question.get("status") not in {"open", "answered", "applied"}
                or question.get("decision") != {"kind": "repair-allowance", "allowance": allowance(value)}
                or value["consumed"] < allowance(value)):
            raise api.SumError("A decision for the current exhausted allowance is required.")
        if question["status"] != "open" and question["answer"] != text:
            raise api.SumError("Explicit confirmation must preserve the already recorded decision.")
        grant = {"question": args.question, "additional": args.additional, "text": text,
                 "at": api.now(), "by": {field: ctx[field] for field in ("machine", "session", "pane")}}
        if question["status"] == "open":
            question.update(answer=text, answered_at=grant["at"], status="answered")
        value["grants"].append(grant)
        store.save(task)
        return {"task": args.task, "grant": grant, "duplicate": False}


def send(api, store, args):
    api.require_coordinator(store, api.context())
    text = api.text_input(args)
    if not valid_key(args.key):
        raise api.SumError("Repair key must be a bounded stable identifier.")
    with store.delivery_lock():
        with store.lock():
            task = store.read(args.task)
            store.check_machine(task)
            value = ledger(api, task)
            previous = next((op for op in value["operations"] if op["kind"] == "send" and op["key"] == args.key), None)
            if previous:
                if previous["attempt"] != args.attempt or previous["text"] != text:
                    raise api.SumError("Repair key already identifies another instruction or attempt.")
                return {"task": task["id"], "operation": previous, "duplicate": True}
            check_allowance(api, store, task)
            api.refuse_execution_during_cleanup(task, "Repair")
            worker = api.reservations.worker(task)
            if worker["id"] != args.attempt or worker["state"] != "running":
                raise api.SumError("Repair requires the exact running worker attempt.")
            route = api.return_route(task, "worker")
            generation = worker["generation"]
        api.observe_recipient(route, task["worktree"])
        with store.lock():
            current = store.read(args.task)
            api.refuse_execution_during_cleanup(current, "Repair")
            worker = api.reservations.worker(current)
            registration = store.registration(route)
            if (worker["id"] != args.attempt or worker["generation"] != generation
                    or worker["state"] != "running"
                    or api.return_route(current, "worker") != route
                    or not registration or registration.get("role") != "worker"
                    or registration.get("task") != args.task):
                raise api.SumError("Worker identity changed before corrective delivery; nothing sent.")
            value = ledger(api, current)
            operation = {"id": "r-" + uuid.uuid4().hex[:12], "kind": "send", "key": args.key,
                         "attempt": args.attempt, "text": text, "created_at": api.now(),
                         "state": "in-flight", "pid": os.getpid()}
            value["operations"].append(operation)
            value["consumed"] += 1
            store.save(current)
        error = None
        try:
            api.herdr(["agent", "prompt", route["pane"], text], session=route["session"], timeout=api.RECIPIENT_TIMEOUT)
        except api.SumError as exc:
            error = exc
        with store.lock():
            current = store.read(args.task)
            saved = next(op for op in ledger(api, current)["operations"] if op["id"] == operation["id"])
            saved["state"] = "uncertain" if error else "submitted"
            store.save(current)
        if error:
            raise api.SumError(f"Corrective delivery is uncertain and remains charged: {error}") from error
        return {"task": args.task, "operation": saved, "duplicate": False}
