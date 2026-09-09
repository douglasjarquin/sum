from __future__ import annotations

import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import stat

from skill_content import content_digest
from skill_source import (
    JsonValue,
    MAX_FILE_BYTES,
    MAX_LICENSE_FILES,
    MAX_SELECTED_BYTES,
    MAX_TREE_FILES,
    PreparedSelection,
    SKILL_NAME,
    Selection,
    SkillError,
    validate_selection,
)


LOCK_SCHEMA = 1
MAX_SNAPSHOT_FILES = MAX_TREE_FILES + MAX_LICENSE_FILES
MAX_SNAPSHOT_ENTRIES = MAX_SNAPSHOT_FILES * 4


def safe_record_path(value: JsonValue) -> bool:
    if not isinstance(value, str) or not value or "\\" in value:
        return False
    path = PurePosixPath(value)
    return (not path.is_absolute() and path.as_posix() == value
            and all(part not in ("", ".", "..") and part.casefold() != ".git" for part in path.parts))


def selection_key(value: dict) -> tuple[str, str, str, str]:
    return tuple(value.get(field, "") for field in ("repository", "ref", "path", "route"))


def _snapshot_id(origin: str, commit: str, path: str, route: str) -> str:
    value = "\0".join((origin, commit, path, route))
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:32]


def valid_record(value: dict) -> bool:
    if not isinstance(value, dict):
        return False
    files = value.get("files")
    repository, origin, ref, commit = (value.get(field) for field in ("repository", "origin", "ref", "commit"))
    path, route, name, snapshot = (value.get(field) for field in ("path", "route", "name", "snapshot"))
    if not all(isinstance(item, str) for item in (repository, origin, ref, commit, path, route, name, snapshot)):
        return False
    try:
        stored = Selection(repository, ref, path, route)
        if validate_selection(stored) != stored:
            return False
    except SkillError:
        return False
    if (not origin.startswith("git:") or len(origin) == 4 or re.fullmatch(r"[0-9a-f]{40}", commit) is None
            or (re.fullmatch(r"[0-9a-f]{40}", ref) is not None and ref != commit)
            or SKILL_NAME.fullmatch(name) is None or name.startswith("sum-")
            or PurePosixPath(path).name != name
            or snapshot != f"snapshots/{_snapshot_id(origin[4:], commit, path, route)}"):
        return False
    valid_files = isinstance(files, list) and 0 < len(files) <= MAX_SNAPSHOT_FILES and all(
        isinstance(item, dict) and isinstance(item.get("path"), str)
        and isinstance(item.get("snapshot_path"), str)
        and isinstance(item.get("mode"), int) and item["mode"] in (0, 0o644, 0o755)
        and isinstance(item.get("sha256"), str) and re.fullmatch(r"[0-9a-f]{64}", item["sha256"]) is not None
        and safe_record_path(item["path"])
        and safe_record_path(item["snapshot_path"])
        and (item["snapshot_path"].startswith(f"skill/{name}/") or item["snapshot_path"].startswith("licenses/"))
        for item in files
    )
    return (valid_files and isinstance(value.get("content_sha256"), str)
            and re.fullmatch(r"[0-9a-f]{64}", value["content_sha256"]) is not None
            and len({item["path"] for item in files}) == len(files)
            and len({item["snapshot_path"] for item in files}) == len(files))


def snapshot_id(prepared: PreparedSelection) -> str:
    return _snapshot_id(prepared.origin, prepared.commit, prepared.selection.path, prepared.selection.route)


def _snapshot_files(root: Path) -> set[str] | None:
    paths = set()
    entries = 0
    pending = [root]
    while pending:
        directory = pending.pop()
        with os.scandir(directory) as members:
            for entry in members:
                member = Path(entry.path)
                entries += 1
                if entries > MAX_SNAPSHOT_ENTRIES:
                    return None
                if entry.is_symlink() or entry.is_file(follow_symlinks=False):
                    paths.add(member.relative_to(root).as_posix())
                elif entry.is_dir(follow_symlinks=False):
                    pending.append(member)
    return paths


def snapshot_errors(record: dict, target: Path) -> list[str]:
    errors = []
    if not valid_record(record):
        return ["selection lock record is malformed"]
    digest_fields = (
        (item["path"], item["snapshot_path"], item["mode"], item["sha256"])
        for item in record["files"]
    )
    if content_digest(digest_fields) != record["content_sha256"]:
        errors.append("selected skill content hash mismatch")
    snapshot = record.get("snapshot")
    assert isinstance(snapshot, str)
    snapshot_root = target / ".sum-skills" / snapshot
    ancestor = symlink_ancestor(snapshot_root, target)
    if ancestor:
        return [f"selected skill snapshot has a symlinked ancestor: {ancestor}"]
    if snapshot_root.is_symlink() or not snapshot_root.is_dir():
        return [f"selected skill snapshot is missing or symlinked: {snapshot_root}"]
    expected = set()
    bytes_read = 0
    for item in record["files"]:
        relative = item["snapshot_path"]
        expected.add(relative)
        member = snapshot_root / relative
        try:
            if member.is_symlink():
                actual_mode = 0
                actual_data = os.readlink(member).encode()
                if not member.resolve().is_relative_to(snapshot_root.resolve()):
                    errors.append(f"selected skill resource escapes its snapshot: {member}")
            elif member.is_file():
                actual_mode = stat.S_IMODE(member.stat().st_mode)
                with member.open("rb") as source:
                    actual_data = source.read(MAX_FILE_BYTES + 1)
                if len(actual_data) > MAX_FILE_BYTES:
                    errors.append(f"installed resource exceeds file limit of {MAX_FILE_BYTES} bytes: {member}")
                    continue
            else:
                raise OSError("missing")
            bytes_read += len(actual_data)
            if bytes_read > MAX_SELECTED_BYTES:
                errors.append(f"installed resources exceed aggregate limit of {MAX_SELECTED_BYTES} bytes")
                return errors
            if hashlib.sha256(actual_data).hexdigest() != item["sha256"]:
                errors.append(f"selected skill resource hash mismatch: {member}")
            if not mode_matches(item["mode"], actual_mode):
                errors.append(f"selected skill resource mode mismatch: {member}")
        except (OSError, RuntimeError) as exc:
            errors.append(f"missing selected skill resource {member}: {exc}")
    try:
        actual = _snapshot_files(snapshot_root)
    except OSError as exc:
        errors.append(f"cannot inspect selected skill snapshot {snapshot_root}: {exc}")
        return errors
    if actual is None:
        errors.append(f"selected skill snapshot exceeds entry limit of {MAX_SNAPSHOT_ENTRIES}: {snapshot_root}")
    elif actual != expected:
        errors.append(f"selected skill snapshot file set changed: {snapshot_root}")
    return errors


def same_snapshot(record: dict, target: Path) -> bool:
    return not snapshot_errors(record, target)


def mode_matches(expected: int, actual: int) -> bool:
    if actual == expected:
        return True
    return expected in (0o644, 0o755) and actual == expected & ~0o222


def projection(target: Path, record: dict) -> Path:
    return target / record["route"] / record["name"]


def symlink_ancestor(path: Path, boundary: Path) -> Path | None:
    current = path
    while current != boundary:
        if current.is_symlink():
            return current
        if current == current.parent:
            break
        current = current.parent
    return boundary if boundary.is_symlink() else None


def reused(target: Path, record: dict) -> bool:
    destination = projection(target, record)
    if symlink_ancestor(destination.parent, target) or not same_snapshot(record, target) or not destination.is_symlink():
        return False
    return destination.resolve() == (target / ".sum-skills" / record["snapshot"] / "skill" / record["name"]).resolve()
