from __future__ import annotations

import hashlib
import os
from pathlib import Path
import re
import stat

from skill_content import content_digest
from skill_source import PreparedSelection, SKILL_NAME, SUPPORTED_ROUTES


LOCK_SCHEMA = 1


def selection_key(value: dict) -> tuple[str, str, str, str]:
    return tuple(value.get(field, "") for field in ("repository", "ref", "path", "route"))


def valid_record(value: dict) -> bool:
    if not isinstance(value, dict):
        return False
    files = value.get("files")
    valid_files = isinstance(files, list) and bool(files) and all(isinstance(item, dict) and isinstance(item.get("path"), str)
                                                  and isinstance(item.get("snapshot_path"), str)
                                                  and isinstance(item.get("mode"), int) and item["mode"] in (0, 0o644, 0o755)
                                                  and isinstance(item.get("sha256"), str) and re.fullmatch(r"[0-9a-f]{64}", item["sha256"]) is not None
                                                  and not Path(item["path"]).is_absolute() and ".." not in Path(item["path"]).parts
                                                  and not Path(item["snapshot_path"]).is_absolute()
                                                  and ".." not in Path(item["snapshot_path"]).parts
                                                  and (item["snapshot_path"].startswith(f"skill/{value.get('name', '')}/") or item["snapshot_path"].startswith("licenses/")) for item in files)
    return (isinstance(value, dict) and isinstance(value.get("repository"), str) and isinstance(value.get("ref"), str) and isinstance(value.get("commit"), str)
            and re.fullmatch(r"[0-9a-f]{40}", value["commit"]) is not None and isinstance(value.get("path"), str)
            and isinstance(value.get("route"), str) and value["route"] in SUPPORTED_ROUTES
            and isinstance(value.get("name"), str) and SKILL_NAME.fullmatch(value["name"]) is not None and not value["name"].startswith("sum-")
            and isinstance(value.get("snapshot"), str) and re.fullmatch(r"snapshots/[0-9a-f]{32}", value["snapshot"]) is not None
            and isinstance(value.get("content_sha256"), str) and re.fullmatch(r"[0-9a-f]{64}", value["content_sha256"]) is not None
            and valid_files and len({item["snapshot_path"] for item in files}) == len(files))


def snapshot_id(prepared: PreparedSelection) -> str:
    value = "\0".join((prepared.origin, prepared.commit, prepared.selection.path, prepared.selection.route))
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:32]


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
                actual_data = member.read_bytes()
            else:
                raise OSError("missing")
            if hashlib.sha256(actual_data).hexdigest() != item["sha256"]:
                errors.append(f"selected skill resource hash mismatch: {member}")
            if not mode_matches(item["mode"], actual_mode):
                errors.append(f"selected skill resource mode mismatch: {member}")
        except OSError as exc:
            errors.append(f"missing selected skill resource {member}: {exc}")
    actual = {path.relative_to(snapshot_root).as_posix() for path in snapshot_root.rglob("*") if path.is_file() or path.is_symlink()}
    if actual != expected:
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
