from __future__ import annotations

from contextlib import suppress
from dataclasses import dataclass
import os
from pathlib import Path, PurePosixPath
import re
import selectors
import signal
import subprocess
import time
from typing import Final
from urllib.parse import urlparse

from skill_content import SkillError


MAX_FILE_BYTES: Final = 8 * 1024 * 1024
MAX_SELECTED_BYTES: Final = 32 * 1024 * 1024
MAX_TREE_FILES: Final = 2048
MAX_GIT_OUTPUT_BYTES: Final = 16 * 1024 * 1024
MAX_GIT_STDERR_BYTES: Final = 64 * 1024
MAX_REMOTE_REPOSITORY_BYTES: Final = 64 * 1024 * 1024


@dataclass(frozen=True, slots=True)
class TreeEntry:
    mode: int
    kind: str
    oid: str
    path: str


@dataclass(frozen=True, slots=True)
class GitCommand:
    repo: Path | None
    args: tuple[str, ...]
    stdout_limit: int = MAX_GIT_OUTPUT_BYTES
    timeout: int = 120
    footprint: Path | None = None


def _repository_bytes(root: Path) -> int:
    total = 0
    for directory, _, files in os.walk(root, followlinks=False):
        for name in files:
            try:
                total += (Path(directory) / name).lstat().st_size
            except FileNotFoundError:
                continue
            if total > MAX_REMOTE_REPOSITORY_BYTES:
                return total
    return total


def _run_git(command: GitCommand) -> bytes:
    argv = ["git"]
    if command.repo is not None:
        argv.extend(("-C", str(command.repo)))
    argv.extend(command.args)
    try:
        process = subprocess.Popen(
            argv,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env={**os.environ, "GIT_NO_REPLACE_OBJECTS": "1"},
            start_new_session=True,
        )
    except OSError as exc:
        raise SkillError(f"git source operation failed: {exc}") from exc
    assert process.stdout is not None and process.stderr is not None
    output = {"stdout": bytearray(), "stderr": bytearray()}
    streams = {process.stdout.fileno(): process.stdout, process.stderr.fileno(): process.stderr}
    limits = {process.stdout.fileno(): command.stdout_limit, process.stderr.fileno(): MAX_GIT_STDERR_BYTES}
    labels = {process.stdout.fileno(): "stdout", process.stderr.fileno(): "stderr"}
    failure = None
    started = time.monotonic()
    selector = selectors.DefaultSelector()
    try:
        for descriptor, stream in streams.items():
            selector.register(stream, selectors.EVENT_READ, descriptor)
        while selector.get_map():
            if time.monotonic() - started > command.timeout:
                failure = f"git source operation timed out after {command.timeout} seconds"
                break
            if command.footprint is not None and _repository_bytes(command.footprint) > MAX_REMOTE_REPOSITORY_BYTES:
                failure = f"Git temporary repository footprint is larger than {MAX_REMOTE_REPOSITORY_BYTES} bytes"
                break
            for key, _ in selector.select(0.1):
                descriptor = key.data
                chunk = os.read(descriptor, 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                label = labels[descriptor]
                if len(output[label]) + len(chunk) > limits[descriptor]:
                    failure = f"git source operation exceeded {label} limit of {limits[descriptor]} bytes"
                    break
                output[label].extend(chunk)
            if failure is not None:
                break
    finally:
        selector.close()
        if failure is None:
            try:
                process.wait(timeout=max(0.1, command.timeout - (time.monotonic() - started)))
            except subprocess.TimeoutExpired:
                failure = f"git source operation timed out after {command.timeout} seconds"
        if failure is not None:
            with suppress(ProcessLookupError, PermissionError):
                os.killpg(process.pid, signal.SIGKILL)
            try:
                process.wait(timeout=1)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        process.stdout.close()
        process.stderr.close()
    if failure is None and command.footprint is not None and _repository_bytes(command.footprint) > MAX_REMOTE_REPOSITORY_BYTES:
        failure = f"Git temporary repository footprint is larger than {MAX_REMOTE_REPOSITORY_BYTES} bytes"
    if failure is not None:
        raise SkillError(failure)
    if process.returncode:
        detail = bytes(output["stderr"] or output["stdout"])[-2000:].decode("utf-8", "replace").strip()
        raise SkillError(f"git source operation failed: {detail}")
    return bytes(output["stdout"])


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


def _parse_tree(raw: bytes, prefix: str = "", selected: PurePosixPath | None = None) -> list[TreeEntry]:
    rows = []
    for record in raw.split(b"\0"):
        if not record:
            continue
        try:
            header, name_bytes = record.split(b"\t", 1)
            mode_bytes, kind_bytes, oid_bytes = header.split(b" ", 2)
            name = name_bytes.decode("utf-8")
            path = _safe_tree_path(f"{prefix}{name}", selected)
            kind = kind_bytes.decode("ascii")
            oid = oid_bytes.decode("ascii")
            mode = int(mode_bytes, 8)
        except (UnicodeDecodeError, ValueError) as exc:
            raise SkillError("unsafe Git tree entry encoding") from exc
        if kind not in ("blob", "commit", "tree") or re.fullmatch(r"[0-9a-f]{40}", oid) is None:
            raise SkillError(f"Git selection contains an unsupported tree entry: {path}")
        rows.append(TreeEntry(mode, kind, oid, path))
    return rows


class GitRepository:
    def __init__(self, path: Path, origin: str, footprint: Path | None = None) -> None:
        self.path = path
        self.origin = origin
        self.footprint = footprint

    def _run(self, *args: str, stdout_limit: int = MAX_GIT_OUTPUT_BYTES, timeout: int = 120) -> bytes:
        return _run_git(GitCommand(self.path, args, stdout_limit, timeout, self.footprint))

    def resolve(self, ref: str) -> str:
        value = self._run("rev-parse", "--verify", "--end-of-options", f"{ref}^{{commit}}", stdout_limit=128).decode().strip()
        if re.fullmatch(r"[0-9a-f]{40}", value) is None:
            raise SkillError(f"Git ref {ref!r} did not resolve to a full commit")
        return value

    def selected_tree(self, commit: str, selected: str) -> list[TreeEntry]:
        raw = self._run("ls-tree", "-r", "-z", "--full-tree", commit, "--", selected)
        rows = _parse_tree(raw, selected=PurePosixPath(selected))
        if not rows:
            raise SkillError(f"selected skill directory does not exist at {selected!r}")
        if len(rows) > MAX_TREE_FILES:
            raise SkillError(f"selected skill has more than {MAX_TREE_FILES} files")
        return rows

    def directory(self, commit: str, path: str) -> list[TreeEntry]:
        suffix = (f"{path}/",) if path else ()
        return _parse_tree(self._run("ls-tree", "-z", "--full-tree", commit, "--", *suffix))

    def entry(self, commit: str, path: str) -> TreeEntry | None:
        rows = _parse_tree(self._run("ls-tree", "-z", "--full-tree", commit, "--", path))
        return rows[0] if len(rows) == 1 and rows[0].path == path else None

    def object_size(self, entry: TreeEntry) -> int:
        raw = self._run("cat-file", "-s", entry.oid, stdout_limit=32)
        try:
            size = int(raw)
        except ValueError as exc:
            raise SkillError(f"Git object has an invalid size: {entry.path}") from exc
        if size < 0 or size > MAX_FILE_BYTES:
            raise SkillError(f"selected resource is larger than {MAX_FILE_BYTES} bytes: {entry.path}")
        return size

    def blob(self, entry: TreeEntry, size: int) -> bytes:
        raw = self._run("cat-file", "blob", entry.oid, stdout_limit=size)
        if len(raw) != size:
            raise SkillError(f"Git object size changed while reading: {entry.path}")
        return raw


class ResourceBudget:
    def __init__(self) -> None:
        self.used = 0

    def read(self, repository: GitRepository, entry: TreeEntry) -> bytes:
        size = repository.object_size(entry)
        if self.used + size > MAX_SELECTED_BYTES:
            raise SkillError(f"selected resources exceed aggregate resource limit of {MAX_SELECTED_BYTES} bytes")
        data = repository.blob(entry, size)
        self.used += size
        return data


def open_repository(repository: str, ref: str, scratch: Path) -> tuple[GitRepository, str]:
    candidate = Path(repository).expanduser()
    parsed = urlparse(repository)
    if candidate.exists() or parsed.scheme == "file":
        if parsed.scheme == "file" and (parsed.netloc or parsed.query or parsed.fragment):
            raise SkillError("file Git sources must not contain a host, query, or fragment")
        local = candidate if candidate.exists() else Path(parsed.path)
        if not local.exists():
            raise SkillError(f"Git repository does not exist: {repository}")
        top = _run_git(GitCommand(local.resolve(), ("rev-parse", "--show-toplevel"), 4096)).decode().strip()
        source = GitRepository(Path(top).resolve(), repository if repository.startswith("file://") else str(Path(top).resolve()))
        return source, source.resolve(ref)
    remote = ((parsed.scheme in ("https", "ssh") and not parsed.username and not parsed.password and bool(parsed.hostname))
              or re.fullmatch(r"[^/@:\s]+@[^/:\s]+:.+", repository) is not None)
    if not remote:
        raise SkillError("unsupported Git transport; use a local path, file://, https://, ssh://, or scp-style Git remote")
    clone = scratch / "source"
    clone.mkdir()
    source = GitRepository(clone, repository, clone)
    _run_git(GitCommand(None, ("init", "--quiet", str(clone)), 4096, footprint=clone))
    source._run("config", "core.hooksPath", "/dev/null", stdout_limit=4096)
    source._run("remote", "add", "origin", repository, stdout_limit=4096)
    source._run("fetch", "--no-tags", "--no-recurse-submodules", "--depth=1", "--filter=blob:none",
                "--end-of-options", "origin", ref, stdout_limit=1024 * 1024, timeout=300)
    return source, source.resolve("FETCH_HEAD")
