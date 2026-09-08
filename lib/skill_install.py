from __future__ import annotations

import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import tempfile
from contextlib import contextmanager

from skill_source import JsonObject, PreparedSelection, Selection, SKILL_NAME, SUPPORTED_ROUTES, SkillError, prepare, validate_selection


LOCK_SCHEMA = 1


def _read_lock(path: Path) -> JsonObject:
    if not path.exists():
        return {"schema": LOCK_SCHEMA, "selections": []}
    if path.is_symlink():
        raise SkillError(f"refusing symlinked selection lock: {path}")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, ValueError) as exc:
        raise SkillError(f"cannot read selection lock {path}: {exc}") from exc
    if value.get("schema") != LOCK_SCHEMA or not isinstance(value.get("selections"), list):
        raise SkillError(f"unsupported selection lock: {path}")
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


def _ensure_directory(path: Path) -> None:
    if path.is_symlink():
        raise SkillError(f"refusing symlinked destination directory: {path}")
    if not path.exists():
        if path.parent != path:
            _ensure_directory(path.parent)
        path.mkdir()
    if not path.is_dir():
        raise SkillError(f"destination is not a directory: {path}")


def _write_snapshot(root: Path, prepared: PreparedSelection) -> None:
    for item in prepared.files:
        relative = Path(item.path) if item.path.startswith("licenses/") else Path("skill") / prepared.name / item.path
        destination = root / relative
        _ensure_directory(destination.parent)
        if item.link_target is not None:
            os.symlink(item.link_target, destination)
        else:
            destination.write_bytes(item.data)
            destination.chmod(item.mode)


def _selection_key(value: dict) -> tuple[str, str, str, str]:
    return tuple(value.get(field, "") for field in ("repository", "ref", "path", "route"))


def _valid_record(value: JsonObject) -> bool:
    return (isinstance(value.get("repository"), str) and isinstance(value.get("ref"), str) and isinstance(value.get("path"), str)
            and isinstance(value.get("route"), str) and value["route"] in SUPPORTED_ROUTES
            and isinstance(value.get("name"), str) and SKILL_NAME.fullmatch(value["name"]) is not None and not value["name"].startswith("sum-")
            and isinstance(value.get("snapshot"), str) and re.fullmatch(r"snapshots/[0-9a-f]{32}", value["snapshot"]) is not None)


def _snapshot_id(prepared: PreparedSelection) -> str:
    value = "\0".join((prepared.origin, prepared.commit, prepared.selection.path, prepared.selection.route))
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:32]


def _same_snapshot(record: dict, target: Path) -> bool:
    snapshot = record.get("snapshot")
    if not isinstance(snapshot, str) or not re.fullmatch(r"snapshots/[0-9a-f]{32}", snapshot):
        return False
    snapshot_root = target / ".sum-skills" / snapshot
    if snapshot_root.is_symlink():
        return False
    path = snapshot_root / "skill" / record.get("name", "")
    return path.is_dir() and not path.is_symlink()


def _projection(target: Path, record: dict) -> Path:
    return target / record["route"] / record["name"]


def _reused(target: Path, record: dict) -> bool:
    projection = _projection(target, record)
    if not _same_snapshot(record, target) or not projection.is_symlink():
        return False
    return projection.resolve() == (target / ".sum-skills" / record["snapshot"] / "skill" / record["name"]).resolve()


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
        _ensure_directory(store)
        _ensure_directory(store / "snapshots")
        lock_path = store / "selection-lock.json"
        lock = _read_lock(lock_path)
        records = list(lock["selections"])
        if any(not isinstance(record, dict) or not _valid_record(record) for record in records):
            raise SkillError("selection lock contains an invalid record")
        by_key = {_selection_key(record): record for record in records}
        reused = []
        prepared = []
        for selection in parsed:
            prior = by_key.get((selection.repository, selection.ref, selection.path, selection.route))
            if prior and re.fullmatch(r"[0-9a-f]{40}", selection.ref) and _reused(target, prior):
                reused.append(prior["name"])
                continue
            prepared.append(prepare(selection))
        names = {record.get("name") for record in records}
        new_names = set()
        for item in prepared:
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
                snapshot_id = _snapshot_id(item)
                snapshot = store / "snapshots" / snapshot_id
                if snapshot.exists() or snapshot.is_symlink():
                    raise SkillError(f"snapshot collision: {snapshot}")
                temporary = Path(tempfile.mkdtemp(prefix=".snapshot-", dir=store / "snapshots"))
                try:
                    _write_snapshot(temporary, item)
                    os.rename(temporary, snapshot)
                finally:
                    if temporary.exists():
                        shutil.rmtree(temporary)
                created_snapshots.append(snapshot)
                destination = target / item.selection.route / item.name
                _ensure_directory(destination.parent)
                relative = os.path.relpath(snapshot / "skill" / item.name, destination.parent)
                os.symlink(relative, destination)
                created_links.append(destination)
                record = item.record(f"snapshots/{snapshot_id}")
                records.append(record)
                by_key[_selection_key(record)] = record
            if prepared:
                _write_json(lock_path, {"schema": LOCK_SCHEMA, "selections": records})
        except (OSError, SkillError, ValueError):
            for link in created_links:
                if link.is_symlink():
                    link.unlink()
            for snapshot in created_snapshots:
                if snapshot.exists():
                    shutil.rmtree(snapshot)
            raise
    return {"target": str(target), "installed": [item.name for item in prepared], "reused": reused,
            "lock": str(target / ".sum-skills/selection-lock.json"),
            "note": "Upstream bytes remain in the commit-addressed snapshot; installation does not invoke skills or alter model, MCP, permission, or global settings."}


def check(target: str | Path) -> JsonObject:
    target = Path(target).expanduser().resolve()
    store = target / ".sum-skills"
    errors = []
    if not store.exists():
        return {"ok": True, "target": str(target), "selections": [], "errors": []}
    try:
        lock = _read_lock(store / "selection-lock.json")
    except SkillError as exc:
        return {"ok": False, "target": str(target), "selections": [], "errors": [str(exc)]}
    seen = set()
    for record in lock["selections"]:
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
        expected = set()
        for item in record.get("files", []):
            relative = item.get("path") if isinstance(item, dict) else None
            if not isinstance(relative, str) or not relative or Path(relative).is_absolute() or ".." in Path(relative).parts:
                errors.append(f"invalid selected skill resource path: {relative!r}")
                continue
            relative_path = Path(relative) if relative.startswith("licenses/") else Path("skill") / name / relative
            expected.add(relative_path.as_posix())
            member = snapshot_root / relative_path
            try:
                if not member.is_file() and not member.is_symlink():
                    raise OSError("missing")
                digest = hashlib.sha256(os.readlink(member).encode() if member.is_symlink() else member.read_bytes()).hexdigest()
            except OSError as exc:
                errors.append(f"missing selected skill resource {member}: {exc}")
                continue
            if digest != item.get("sha256"):
                errors.append(f"selected skill resource hash mismatch: {member}")
            if member.is_symlink() and not member.resolve().is_relative_to(snapshot_root.resolve()):
                errors.append(f"selected skill resource escapes its snapshot: {member}")
        actual = {path.relative_to(snapshot_root).as_posix() for path in snapshot_root.rglob("*") if path.is_file() or path.is_symlink()} if snapshot_root.is_dir() else set()
        if actual != expected:
            errors.append(f"selected skill snapshot file set changed: {snapshot_root}")
        projection = target / route / name
        expected_projection = snapshot_root / "skill" / name
        if not projection.is_symlink() or projection.resolve() != expected_projection.resolve():
            errors.append(f"selected skill projection changed: {projection}")
    return {"ok": not errors, "target": str(target), "selections": sorted(seen), "errors": errors}
