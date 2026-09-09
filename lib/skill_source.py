from __future__ import annotations

from dataclasses import dataclass
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tempfile
from typing import TypeAlias
from urllib.parse import urlparse

from skill_content import (
    SKILL_NAME,
    UNSUPPORTED_CAPABILITIES,
    SkillError,
    SourceFile,
    content_digest,
    frontmatter,
    governing_licenses,
    normal_target,
    snapshot_link_target,
    validate_markdown_resources,
)


MAX_FILE_BYTES = 8 * 1024 * 1024
MAX_TREE_FILES = 2048
SUPPORTED_ROUTES = (".agents/skills", ".claude/skills")
JsonValue: TypeAlias = str | int | bool | None | list["JsonValue"] | dict[str, "JsonValue"]
JsonObject: TypeAlias = dict[str, JsonValue]
__all__ = (
    "JsonObject", "JsonValue", "MAX_FILE_BYTES", "MAX_TREE_FILES", "PreparedSelection",
    "SKILL_NAME", "SUPPORTED_ROUTES", "Selection", "SkillError", "SourceFile",
    "UNSUPPORTED_CAPABILITIES", "inspect", "prepare", "validate_selection",
)


@dataclass(frozen=True, slots=True)
class Selection:
    repository: str
    ref: str
    path: str
    route: str = ".agents/skills"


@dataclass(frozen=True, slots=True)
class PreparedSelection:
    selection: Selection
    origin: str
    commit: str
    name: str
    files: tuple[SourceFile, ...]
    content_sha256: str
    unsupported_capabilities: tuple[str, ...]

    def record(self, snapshot: str) -> JsonObject:
        return {
            "repository": self.selection.repository,
            "origin": f"git:{self.origin}",
            "ref": self.selection.ref,
            "commit": self.commit,
            "path": self.selection.path,
            "route": self.selection.route,
            "name": self.name,
            "snapshot": snapshot,
            "content_sha256": self.content_sha256,
            "files": [{"path": item.path, "snapshot_path": item.snapshot_path, "mode": item.mode, "sha256": item.digest} for item in self.files],
        }


def _git(repo: Path, *args: str, binary: bool = False) -> bytes | str:
    try:
        result = subprocess.run(
            ["git", "-C", str(repo), *args],
            capture_output=True,
            timeout=120,
            text=not binary,
            env={**os.environ, "GIT_NO_REPLACE_OBJECTS": "1"},
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise SkillError(f"git source operation failed: {exc}") from exc
    if result.returncode:
        detail = result.stderr if binary else (result.stderr or result.stdout)
        raise SkillError(f"git source operation failed: {str(detail).strip()[-2000:]}")
    return result.stdout


def _validate_path(value: str, label: str) -> str:
    if not value or "\x00" in value or "\\" in value:
        raise SkillError(f"{label} must be a non-empty POSIX relative path")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in ("", ".", "..") for part in path.parts) or str(path) != value.rstrip("/"):
        raise SkillError(f"{label} must not escape its selected root: {value!r}")
    return str(path)


def validate_selection(selection: Selection) -> Selection:
    if not selection.repository or "\x00" in selection.repository:
        raise SkillError("repository is required")
    if not selection.ref or "\x00" in selection.ref or selection.ref.startswith("-") or "^{" in selection.ref:
        raise SkillError("ref must be an explicit Git revision without option syntax")
    path = _validate_path(selection.path, "skill path")
    route = _validate_path(selection.route, "destination route")
    if route not in SUPPORTED_ROUTES:
        raise SkillError(f"unsupported destination route {route!r}; use one of {SUPPORTED_ROUTES}")
    return Selection(selection.repository, selection.ref, path, route)


def _repository_kind(repository: str) -> tuple[str, Path | None]:
    candidate = Path(repository).expanduser()
    if candidate.exists():
        return "local", candidate.resolve()
    parsed = urlparse(repository)
    if parsed.scheme == "file":
        if parsed.netloc or parsed.query or parsed.fragment:
            raise SkillError("file Git sources must not contain a host, query, or fragment")
        path = Path(parsed.path)
        if not path.exists():
            raise SkillError(f"Git repository does not exist: {repository}")
        return "local", path.resolve()
    if parsed.scheme in ("https", "ssh"):
        if parsed.username or parsed.password or not parsed.hostname:
            raise SkillError("Git URL must have a host and no embedded credentials")
        return "remote", None
    if re.fullmatch(r"[^/@:\s]+@[^/:\s]+:.+", repository):
        return "remote", None
    raise SkillError("unsupported Git transport; use a local path, file://, https://, ssh://, or scp-style Git remote")


def _source_repo(repository: str, scratch: Path) -> tuple[Path, str]:
    kind, local = _repository_kind(repository)
    if kind == "local":
        assert local is not None
        top = str(_git(local, "rev-parse", "--show-toplevel")).strip()
        repo = Path(top).resolve()
        origin = repository if repository.startswith("file://") else str(repo)
        return repo, origin
    clone = scratch / "source"
    try:
        result = subprocess.run(["git", "clone", "--no-checkout", "--no-recurse-submodules", "--config", "core.hooksPath=/dev/null", repository, str(clone)],
                                capture_output=True, timeout=300, text=True)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise SkillError(f"Git source fetch failed: {exc}") from exc
    if result.returncode:
        raise SkillError(f"Git source fetch failed: {(result.stderr or result.stdout).strip()[-2000:]}")
    return clone, repository


def _resolve_commit(repo: Path, ref: str) -> str:
    value = str(_git(repo, "rev-parse", "--verify", "--end-of-options", f"{ref}^{{commit}}")).strip()
    if not re.fullmatch(r"[0-9a-f]{40}", value):
        raise SkillError(f"Git ref {ref!r} did not resolve to a full commit")
    return value


def _safe_tree_path(value: str, selected: PurePosixPath | None = None) -> str:
    path = PurePosixPath(value)
    invalid = (
        not value or "\\" in value or path.is_absolute() or path.as_posix() != value
        or any(part in ("", ".", "..") or part.casefold() == ".git" for part in path.parts)
        or any(ord(character) < 32 or ord(character) == 127 for character in value)
        or (selected is not None and (path == selected or not path.is_relative_to(selected)))
    )
    if invalid:
        raise SkillError(f"unsafe Git tree path: {value!r}")
    return value


def _tree(repo: Path, commit: str, selected: str) -> list[tuple[int, str, str, str]]:
    raw = _git(repo, "ls-tree", "-r", "-z", "--full-tree", commit, "--", selected, binary=True)
    assert isinstance(raw, bytes)
    rows = []
    prefix = selected + "/"
    for record in raw.split(b"\0"):
        if not record:
            continue
        header, name_bytes = record.split(b"\t", 1)
        mode_bytes, kind, oid_bytes = header.split(b" ", 2)
        try:
            name = _safe_tree_path(name_bytes.decode("utf-8"), PurePosixPath(selected))
            oid = oid_bytes.decode("ascii")
        except UnicodeDecodeError as exc:
            raise SkillError("unsafe Git tree path or object identifier encoding") from exc
        if not name.startswith(prefix) or kind.decode() not in ("blob", "commit") or re.fullmatch(r"[0-9a-f]{40}", oid) is None:
            raise SkillError(f"Git selection contains an unsupported tree entry: {name}")
        rows.append((int(mode_bytes, 8), kind.decode(), oid, name))
    if not rows:
        raise SkillError(f"selected skill directory does not exist at {selected!r}")
    if len(rows) > MAX_TREE_FILES:
        raise SkillError(f"selected skill has more than {MAX_TREE_FILES} files")
    return rows


def _blob(repo: Path, oid: str, name: str) -> bytes:
    raw = _git(repo, "cat-file", "blob", oid, binary=True)
    assert isinstance(raw, bytes)
    if len(raw) > MAX_FILE_BYTES:
        raise SkillError(f"selected resource is larger than {MAX_FILE_BYTES} bytes: {name}")
    return raw


def prepare(selection: Selection) -> PreparedSelection:
    selection = validate_selection(selection)
    with tempfile.TemporaryDirectory(prefix="sum-skill-source-") as directory:
        repo, origin = _source_repo(selection.repository, Path(directory))
        commit = _resolve_commit(repo, selection.ref)
        rows = _tree(repo, commit, selection.path)
        names = {path for _, kind, _, path in rows if kind == "blob"}
        skill_path = f"{selection.path}/SKILL.md"
        if skill_path not in names:
            raise SkillError(f"selected skill is missing {skill_path}")
        skill_oid = next(oid for _, kind, oid, path in rows if kind == "blob" and path == skill_path)
        name, unsupported_capabilities = frontmatter(_blob(repo, skill_oid, skill_path), skill_path)
        if name != PurePosixPath(selection.path).name:
            raise SkillError(f"{skill_path}: native name {name!r} does not match its selected directory")
        files = []
        for mode, kind, oid, path in rows:
            if kind == "commit":
                raise SkillError(f"{path}: submodules are not imported")
            data = _blob(repo, oid, path)
            relative = PurePosixPath(path).relative_to(selection.path).as_posix()
            snapshot_path = f"skill/{name}/{relative}"
            link_target = None
            if mode == 0o120000:
                link_target = data.decode("utf-8")
                resolved = normal_target(link_target, path)
                if resolved not in names:
                    raise SkillError(f"{path}: external or missing shared resource {link_target!r}; select it explicitly")
                link_target = snapshot_link_target(path, resolved, selection.path)
                data = link_target.encode("utf-8")
            elif mode not in (0o100644, 0o100755):
                raise SkillError(f"{path}: unsupported Git file mode {mode:o}")
            files.append(SourceFile(relative, snapshot_path, mode & 0o777, data, hashlib.sha256(data).hexdigest(), link_target))
        validate_markdown_resources(files, names, selection.path)
        root_rows = _git(repo, "ls-tree", "-r", "-z", "--full-tree", commit, binary=True)
        assert isinstance(root_rows, bytes)
        ancestor_paths = {PurePosixPath(".")}
        selected_parts = PurePosixPath(selection.path).parts
        ancestor_paths.update(PurePosixPath(*selected_parts[:index]) for index in range(1, len(selected_parts)))
        root_files = {}
        for record in root_rows.split(b"\0"):
            if not record:
                continue
            header, name_bytes = record.split(b"\t", 1)
            mode_bytes, kind, oid_bytes = header.split(b" ", 2)
            try:
                path = _safe_tree_path(name_bytes.decode("utf-8"))
                oid = oid_bytes.decode("ascii")
            except UnicodeDecodeError as exc:
                raise SkillError("unsafe Git tree path or object identifier encoding") from exc
            if re.fullmatch(r"[0-9a-f]{40}", oid) is None:
                raise SkillError(f"Git tree contains an invalid object identifier: {path}")
            if kind.decode() == "blob":
                root_files[path] = (int(mode_bytes, 8), oid)
        licenses = governing_licenses(root_files, ancestor_paths, lambda oid, path: _blob(repo, oid, path))
        if not licenses and not any(Path(item.path).name.lower().startswith(("license", "notice", "copying")) for item in files):
            raise SkillError("source has no license, notice, or copying file")
        all_files = files + [item for item in licenses if item.snapshot_path not in {file.snapshot_path for file in files}]
        all_files = [SourceFile(item.path, item.snapshot_path, item.mode, item.data, hashlib.sha256(item.data).hexdigest(), item.link_target) for item in all_files]
        digest_fields = ((item.path, item.snapshot_path, item.mode, item.digest) for item in all_files)
        return PreparedSelection(selection, origin, commit, name, tuple(all_files), content_digest(digest_fields), unsupported_capabilities)


def inspect(selection: Selection) -> JsonObject:
    prepared = prepare(selection)
    return {**prepared.record("not-installed"), "files": [{"path": item.path, "snapshot_path": item.snapshot_path, "mode": item.mode, "sha256": item.digest} for item in prepared.files],
            "capabilities": {"invocation": False, "permissions": False, "settings": False, "submodules": False,
                              "native_projection": not prepared.unsupported_capabilities,
                              "unsupported": list(prepared.unsupported_capabilities)}}
