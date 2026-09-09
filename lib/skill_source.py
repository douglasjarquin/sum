from __future__ import annotations

from dataclasses import dataclass
import hashlib
from pathlib import Path, PurePosixPath
import re
import subprocess
import tempfile
from typing import TypeAlias
from urllib.parse import unquote, urlparse, urlsplit


MAX_FILE_BYTES = 8 * 1024 * 1024
MAX_TREE_FILES = 2048
SKILL_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
SUPPORTED_ROUTES = (".agents/skills", ".claude/skills")
UNSUPPORTED_CAPABILITIES = frozenset({"allowed-tools", "command", "commands", "context", "hooks", "mcp", "mcp-servers", "model", "permission", "permissions", "tools"})
JsonValue: TypeAlias = str | int | bool | None | list["JsonValue"] | dict[str, "JsonValue"]
JsonObject: TypeAlias = dict[str, JsonValue]


class SkillError(Exception):
    pass


@dataclass(frozen=True, slots=True)
class Selection:
    repository: str
    ref: str
    path: str
    route: str = ".agents/skills"


@dataclass(frozen=True, slots=True)
class SourceFile:
    path: str
    snapshot_path: str
    mode: int
    data: bytes
    digest: str
    link_target: str | None = None


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
        result = subprocess.run(["git", "-C", str(repo), *args], capture_output=True, timeout=120, text=not binary)
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


def _tree(repo: Path, commit: str, selected: str) -> list[tuple[int, str, str]]:
    raw = _git(repo, "ls-tree", "-r", "-z", "--full-tree", commit, "--", selected, binary=True)
    assert isinstance(raw, bytes)
    rows = []
    prefix = selected + "/"
    for record in raw.split(b"\0"):
        if not record:
            continue
        header, name_bytes = record.split(b"\t", 1)
        mode_bytes, kind, oid = header.split(b" ", 2)
        name = name_bytes.decode("utf-8")
        if not name.startswith(prefix) or kind.decode() not in ("blob", "commit"):
            raise SkillError(f"Git selection contains an unsupported tree entry: {name}")
        rows.append((int(mode_bytes, 8), kind.decode(), name))
    if not rows:
        raise SkillError(f"selected skill directory does not exist at {selected!r}")
    if len(rows) > MAX_TREE_FILES:
        raise SkillError(f"selected skill has more than {MAX_TREE_FILES} files")
    return rows


def _blob(repo: Path, commit: str, name: str) -> bytes:
    raw = _git(repo, "show", f"{commit}:{name}", binary=True)
    assert isinstance(raw, bytes)
    if len(raw) > MAX_FILE_BYTES:
        raise SkillError(f"selected resource is larger than {MAX_FILE_BYTES} bytes: {name}")
    return raw


def _scalar(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
        return value[1:-1]
    quoted = False
    escaped = False
    for index, character in enumerate(value):
        if character == "\\" and not escaped:
            escaped = True
            continue
        if character in "'\"" and not escaped:
            quoted = not quoted
        if character == "#" and not quoted and (index == 0 or value[index - 1].isspace()):
            return value[:index].rstrip()
        escaped = False
    return value


def _frontmatter(data: bytes, source: str) -> tuple[str, tuple[str, ...]]:
    try:
        lines = data.decode("utf-8").splitlines()
    except UnicodeDecodeError as exc:
        raise SkillError(f"{source}: SKILL.md is not UTF-8") from exc
    if not lines or lines[0].strip() != "---":
        raise SkillError(f"{source}: malformed frontmatter opening")
    try:
        end = next(index for index, line in enumerate(lines[1:], 1) if line.strip() == "---")
    except StopIteration as exc:
        raise SkillError(f"{source}: malformed frontmatter closing") from exc
    fields = {}
    index = 1
    while index < end:
        line = lines[index]
        if not line.strip() or line.lstrip().startswith("#"):
            index += 1
            continue
        if line[:1].isspace():
            raise SkillError(f"{source}: malformed frontmatter indentation")
        key, separator, value = line.partition(":")
        key = key.strip()
        value = value.strip()
        if not separator or not re.fullmatch(r"[A-Za-z][A-Za-z0-9_-]*", key):
            raise SkillError(f"{source}: malformed frontmatter field")
        if key in fields:
            raise SkillError(f"{source}: duplicate frontmatter field {key!r}")
        if value in (">", ">-", ">+", "|", "|-", "|+"):
            block = []
            index += 1
            while index < end and (not lines[index].strip() or lines[index][:1].isspace()):
                block.append(lines[index][2:] if lines[index].startswith("  ") else lines[index].lstrip())
                index += 1
            if not block:
                raise SkillError(f"{source}: empty YAML block scalar")
            if value.startswith(">"):
                parsed = " ".join(part.strip() for part in block if part.strip())
            else:
                parsed = "\n".join(block)
            fields[key] = parsed if value.endswith("+") else parsed.rstrip()
            continue
        if not value:
            fields[key] = "structured"
            index += 1
            while index < end and (not lines[index].strip() or lines[index][:1].isspace()):
                index += 1
            continue
        fields[key] = _scalar(value)
        index += 1
    name = fields.get("name")
    if not name or not SKILL_NAME.fullmatch(name):
        raise SkillError(f"{source}: frontmatter has no valid native name")
    return name, tuple(sorted(UNSUPPORTED_CAPABILITIES.intersection(fields)))


def _normal_target(link: str, source_path: str) -> str:
    if not link or "\x00" in link or link.startswith("/") or "\\" in link:
        raise SkillError(f"{source_path}: unsafe symlink target")
    parts = []
    for part in (PurePosixPath(source_path).parent / link).parts:
        if part in ("", "."):
            continue
        if part == "..":
            if not parts:
                raise SkillError(f"{source_path}: symlink escapes the selected skill directory")
            parts.pop()
        else:
            parts.append(part)
    return "/".join(parts)


_MARKDOWN_LINK = re.compile(r"!?\[[^\]]*\]\(\s*(?:<([^>]+)>|([^\s)]+))")


def _markdown_targets(data: bytes) -> tuple[str, ...]:
    text = data.decode("utf-8")
    targets = []
    for match in _MARKDOWN_LINK.finditer(text):
        raw = unquote(match.group(1) or match.group(2) or "")
        parsed = urlsplit(raw)
        if not parsed.path or parsed.scheme or parsed.netloc or raw.startswith(("#", "/")):
            continue
        targets.append(parsed.path)
    return tuple(targets)


def _relative_path(source_path: str, link: str) -> PurePosixPath | None:
    parts = list(PurePosixPath(source_path).parent.parts)
    for part in PurePosixPath(link).parts:
        if part in ("", "."):
            continue
        if part == "..":
            if not parts:
                return None
            parts.pop()
        else:
            parts.append(part)
    return PurePosixPath(*parts)


def _validate_markdown_resources(files: list[SourceFile], names: set[str], selected_root: str) -> None:
    selected = PurePosixPath(selected_root)
    for item in files:
        if not item.path.lower().endswith(".md"):
            continue
        source_path = f"{selected_root}/{item.path}"
        for link in _markdown_targets(item.data):
            resolved = _relative_path(source_path, link)
            if resolved is None or not resolved.is_relative_to(selected) or resolved.as_posix() not in names:
                raise SkillError(f"{source_path}: missing explicit resource {link!r}; select it explicitly")


def prepare(selection: Selection) -> PreparedSelection:
    selection = validate_selection(selection)
    with tempfile.TemporaryDirectory(prefix="sum-skill-source-") as directory:
        repo, origin = _source_repo(selection.repository, Path(directory))
        commit = _resolve_commit(repo, selection.ref)
        rows = _tree(repo, commit, selection.path)
        names = {name for _, kind, name in rows if kind == "blob"}
        skill_path = f"{selection.path}/SKILL.md"
        if skill_path not in names:
            raise SkillError(f"selected skill is missing {skill_path}")
        name, unsupported_capabilities = _frontmatter(_blob(repo, commit, skill_path), skill_path)
        if name != PurePosixPath(selection.path).name:
            raise SkillError(f"{skill_path}: native name {name!r} does not match its selected directory")
        files = []
        for mode, kind, path in rows:
            if kind == "commit":
                raise SkillError(f"{path}: submodules are not imported")
            data = _blob(repo, commit, path)
            link_target = None
            if mode == 0o120000:
                link_target = data.decode("utf-8")
                resolved = _normal_target(link_target, path)
                if resolved not in names:
                    raise SkillError(f"{path}: external or missing shared resource {link_target!r}; select it explicitly")
            elif mode not in (0o100644, 0o100755):
                raise SkillError(f"{path}: unsupported Git file mode {mode:o}")
            relative = path.removeprefix(selection.path + "/")
            files.append(SourceFile(relative, f"skill/{name}/{relative}", mode & 0o777, data, hashlib.sha256(data).hexdigest(), link_target))
        _validate_markdown_resources(files, names, selection.path)
        root_rows = _git(repo, "ls-tree", "-r", "-z", "--full-tree", commit, binary=True)
        assert isinstance(root_rows, bytes)
        licenses = []
        ancestor_paths = {PurePosixPath(".")}
        selected_parts = PurePosixPath(selection.path).parts
        ancestor_paths.update(PurePosixPath(*selected_parts[:index]) for index in range(1, len(selected_parts)))
        for record in root_rows.split(b"\0"):
            if not record:
                continue
            header, name_bytes = record.split(b"\t", 1)
            mode_bytes, kind, oid = header.split(b" ", 2)
            path = name_bytes.decode("utf-8")
            path_object = PurePosixPath(path)
            if kind.decode() != "blob" or path_object.parent not in ancestor_paths or not path_object.name.lower().startswith(("license", "notice", "copying")):
                continue
            licenses.append(SourceFile(f"licenses/{path}", f"licenses/{path}", int(mode_bytes, 8) & 0o777, _blob(repo, commit, path), "", None))
        if not licenses and not any(Path(item.path).name.lower().startswith(("license", "notice", "copying")) for item in files):
            raise SkillError("source has no license, notice, or copying file")
        all_files = files + [item for item in licenses if item.snapshot_path not in {file.snapshot_path for file in files}]
        all_files = [SourceFile(item.path, item.snapshot_path, item.mode, item.data, hashlib.sha256(item.data).hexdigest(), item.link_target) for item in all_files]
        digest_input = "\n".join(f"{item.path}\0{item.snapshot_path}\0{item.mode:o}\0{item.digest}" for item in sorted(all_files, key=lambda value: value.snapshot_path)).encode()
        return PreparedSelection(selection, origin, commit, name, tuple(all_files), hashlib.sha256(digest_input).hexdigest(), unsupported_capabilities)


def inspect(selection: Selection) -> JsonObject:
    prepared = prepare(selection)
    return {**prepared.record("not-installed"), "files": [{"path": item.path, "snapshot_path": item.snapshot_path, "mode": item.mode, "sha256": item.digest} for item in prepared.files],
            "capabilities": {"invocation": False, "permissions": False, "settings": False, "submodules": False,
                              "native_projection": not prepared.unsupported_capabilities,
                              "unsupported": list(prepared.unsupported_capabilities)}}
