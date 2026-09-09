from __future__ import annotations

import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import stat
import tempfile
from contextlib import contextmanager

from skill_source import JsonObject, PreparedSelection, Selection, SkillError, prepare, validate_selection
from skill_git import ResourceBudget
from skill_snapshot import LOCK_SCHEMA, reused as snapshot_reused, selection_key, snapshot_errors, snapshot_id, symlink_ancestor, valid_record


MAX_LOCK_BYTES = 8 * 1024 * 1024
MAX_LOCK_RECORDS = 2048


def _read_lock(path: Path) -> JsonObject:
    if path.is_symlink():
        raise SkillError(f"refusing symlinked selection lock: {path}")
    if not path.exists():
        return {"schema": LOCK_SCHEMA, "selections": []}
    try:
        if not stat.S_ISREG(path.stat().st_mode) or path.stat().st_size > MAX_LOCK_BYTES:
            raise SkillError(f"selection lock must be a bounded regular file: {path}")
        with path.open("rb") as source:
            data = source.read(MAX_LOCK_BYTES + 1)
        if len(data) > MAX_LOCK_BYTES:
            raise SkillError(f"selection lock must be a bounded regular file: {path}")
        value = json.loads(data.decode("utf-8"))
    except (OSError, UnicodeDecodeError, ValueError) as exc:
        raise SkillError(f"cannot read selection lock {path}: {exc}") from exc
    if not isinstance(value, dict) or value.get("schema") != LOCK_SCHEMA or not isinstance(value.get("selections"), list):
        raise SkillError(f"unsupported selection lock: {path}")
    records = value["selections"]
    if (len(records) > MAX_LOCK_RECORDS
            or any(not isinstance(record, dict) or not valid_record(record) for record in records)):
        raise SkillError(f"selection lock contains an invalid record: {path}")
    keys = [selection_key(record) for record in records]
    names = [record["name"] for record in records]
    if len(set(keys)) != len(keys) or len(set(names)) != len(names):
        raise SkillError(f"selection lock contains an invalid record: {path}")
    return value


def _write_json(path: Path, value: dict) -> None:
    fd, temporary = tempfile.mkstemp(prefix=".selection-", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            json.dump(value, output, indent=2, ensure_ascii=True)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@contextmanager
def _lock(path: Path):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a+") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


def _ensure_directory(path: Path, boundary: Path | None = None) -> None:
    ancestor = symlink_ancestor(path, boundary) if boundary is not None else (path if path.is_symlink() else None)
    if ancestor:
        raise SkillError(f"refusing symlinked destination directory: {ancestor}")
    if not path.exists():
        if path.parent != path:
            _ensure_directory(path.parent, boundary)
        path.mkdir()
    if not path.is_dir():
        raise SkillError(f"destination is not a directory: {path}")


def _write_snapshot(root: Path, prepared: PreparedSelection) -> None:
    for item in prepared.files:
        destination = root / item.snapshot_path
        _ensure_directory(destination.parent, root)
        if item.link_target is not None:
            os.symlink(item.link_target, destination)
        else:
            destination.write_bytes(item.data)
            destination.chmod(item.mode)


def _set_snapshot_read_only(root: Path, read_only: bool) -> None:
    for member in (root, *root.rglob("*")):
        if member.is_symlink():
            continue
        mode = member.stat().st_mode
        member.chmod(mode & ~0o222 if read_only else mode | 0o200)


def install(target: str | Path, selections: tuple[Selection, ...] | list[Selection]) -> JsonObject:
    target = Path(target).expanduser().resolve()
    if not target.is_dir():
        raise SkillError(f"target worktree is not a directory: {target}")
    parsed = []
    for selection in selections:
        value = validate_selection(selection)
        if value not in parsed:
            parsed.append(value)
    if not parsed:
        raise SkillError("at least one explicit skill selection is required")
    store = target / ".sum-skills"
    if store.exists() and store.is_symlink():
        raise SkillError(f"refusing symlinked skill store: {store}")
    with _lock(store / "install.lock"):
        _ensure_directory(store, target)
        _ensure_directory(store / "snapshots", target)
        lock_path = store / "selection-lock.json"
        lock = _read_lock(lock_path)
        records = list(lock["selections"])
        if any(not isinstance(record, dict) or not valid_record(record) for record in records):
            raise SkillError("selection lock contains an invalid record")
        by_key = {selection_key(record): record for record in records}
        reused = []
        prepared = []
        resource_budget = ResourceBudget()
        for selection in parsed:
            prior = by_key.get((selection.repository, selection.ref, selection.path, selection.route))
            if prior and re.fullmatch(r"[0-9a-f]{40}", selection.ref) and snapshot_reused(target, prior):
                reused.append(prior["name"])
                continue
            candidate = prepare(selection, resource_budget)
            if (prior and candidate.commit == prior.get("commit") and candidate.name == prior.get("name")
                    and candidate.content_sha256 == prior.get("content_sha256") and snapshot_reused(target, prior)):
                reused.append(prior["name"])
                continue
            prepared.append(candidate)
        names = {record.get("name") for record in records}
        new_names = set()
        for item in prepared:
            if item.unsupported_capabilities:
                raise SkillError(f"skill {item.name!r} requests unsupported native capabilities: {', '.join(item.unsupported_capabilities)}; use a reviewed Sum wrapper")
            if item.name.startswith("sum-"):
                raise SkillError(f"reserved Sum skill name is not installable: {item.name}")
            if item.name in names or item.name in new_names:
                raise SkillError(f"skill name collision: {item.name}")
            destination = target / item.selection.route / item.name
            if destination.exists() or destination.is_symlink():
                raise SkillError(f"skill destination already exists: {destination}")
            new_names.add(item.name)
        created_snapshots = []
        created_links = []
        try:
            for item in prepared:
                snapshot_id_value = snapshot_id(item)
                snapshot = store / "snapshots" / snapshot_id_value
                if snapshot.exists() or snapshot.is_symlink():
                    raise SkillError(f"snapshot collision: {snapshot}")
                temporary = Path(tempfile.mkdtemp(prefix=".snapshot-", dir=store / "snapshots"))
                record = item.record(f"snapshots/{snapshot_id_value}")
                if not valid_record(record):
                    raise SkillError("prepared selection produced an invalid snapshot record")
                try:
                    _write_snapshot(temporary, item)
                    os.rename(temporary, snapshot)
                    created_snapshots.append(snapshot)
                    errors = snapshot_errors(record, target)
                    if errors:
                        raise SkillError("; ".join(errors))
                    _set_snapshot_read_only(snapshot, True)
                finally:
                    if temporary.exists():
                        _set_snapshot_read_only(temporary, False)
                        shutil.rmtree(temporary)
                destination = target / item.selection.route / item.name
                _ensure_directory(destination.parent, target)
                relative = os.path.relpath(snapshot / "skill" / item.name, destination.parent)
                os.symlink(relative, destination)
                created_links.append(destination)
                records.append(record)
                by_key[selection_key(record)] = record
            if prepared:
                _write_json(lock_path, {"schema": LOCK_SCHEMA, "selections": records})
        except (OSError, SkillError, ValueError):
            for link in created_links:
                if link.is_symlink():
                    link.unlink()
            for snapshot in created_snapshots:
                if snapshot.exists():
                    _set_snapshot_read_only(snapshot, False)
                    shutil.rmtree(snapshot)
            raise
    return {"target": str(target), "installed": [item.name for item in prepared], "reused": reused,
            "lock": str(target / ".sum-skills/selection-lock.json"),
            "note": "Upstream bytes remain in the commit-addressed snapshot; installation does not invoke skills or alter model, MCP, permission, or global settings."}


def check(target: str | Path) -> JsonObject:
    target = Path(target).expanduser().resolve()
    store = target / ".sum-skills"
    errors = []
    if store.is_symlink():
        return {"ok": False, "target": str(target), "selections": [], "errors": [f"refusing symlinked skill store: {store}"]}
    if not store.exists():
        return {"ok": True, "target": str(target), "selections": [], "errors": []}
    try:
        lock = _read_lock(store / "selection-lock.json")
    except SkillError as exc:
        return {"ok": False, "target": str(target), "selections": [], "errors": [str(exc)]}
    seen = set()
    for record in lock["selections"]:
        if not isinstance(record, dict) or not valid_record(record):
            errors.append("selection lock contains an invalid record")
            continue
        name = record.get("name")
        route = record.get("route")
        snapshot = record.get("snapshot")
        if not isinstance(name, str) or name.startswith("sum-") or name in seen:
            errors.append(f"invalid or duplicate selected skill name: {name!r}")
            continue
        seen.add(name)
        if not isinstance(route, str) or route not in (".agents/skills", ".claude/skills"):
            errors.append(f"unsupported selected skill route: {route!r}")
            continue
        if not isinstance(snapshot, str) or not re.fullmatch(r"snapshots/[0-9a-f]{32}", snapshot):
            errors.append(f"invalid selected skill snapshot: {snapshot!r}")
            continue
        snapshot_root = store / snapshot
        if snapshot_root.is_symlink():
            errors.append(f"selected skill snapshot is symlinked: {snapshot_root}")
            continue
        errors.extend(snapshot_errors(record, target))
        projection = target / route / name
        expected_projection = snapshot_root / "skill" / name
        ancestor = symlink_ancestor(projection.parent, target)
        if ancestor:
            errors.append(f"selected skill projection has a symlinked destination ancestor: {ancestor}")
        if not projection.is_symlink():
            errors.append(f"selected skill projection changed: {projection}")
            continue
        try:
            projection_matches = projection.resolve() == expected_projection.resolve()
        except (OSError, RuntimeError) as exc:
            errors.append(f"selected skill projection is corrupt: {projection}: {exc}")
            continue
        if not projection_matches:
            errors.append(f"selected skill projection changed: {projection}")
    return {"ok": not errors, "target": str(target), "selections": sorted(seen), "errors": errors}
