from __future__ import annotations

from dataclasses import dataclass
import hashlib
from pathlib import Path, PurePosixPath
import re
import tempfile
from typing import TypeAlias

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
from skill_git import (
    MAX_FILE_BYTES,
    MAX_TREE_FILES,
    GitRepository,
    ResourceBudget,
    TreeEntry,
    open_repository,
)


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
    ref = selection.ref
    ref_parts = ref.split("/")
    unsafe_ref = (
        not ref or ref == "@" or ref.startswith(("-", "+")) or ref.endswith(("/", "."))
        or "//" in ref or ".." in ref or "@{" in ref
        or any(ord(character) < 32 or character in " ~^:?*[\\" for character in ref)
        or any(part.startswith(".") or part.endswith(".lock") for part in ref_parts)
    )
    if unsafe_ref and re.fullmatch(r"[0-9a-f]{40}", ref) is None:
        raise SkillError("ref must be an explicit Git revision without option or refspec syntax")
    path = _validate_path(selection.path, "skill path")
    route = _validate_path(selection.route, "destination route")
    if route not in SUPPORTED_ROUTES:
        raise SkillError(f"unsupported destination route {route!r}; use one of {SUPPORTED_ROUTES}")
    return Selection(selection.repository, selection.ref, path, route)


def _license_entries(repository: GitRepository, commit: str, selected: str) -> dict[str, tuple[int, str]]:
    parts = PurePosixPath(selected).parts
    ancestors = ("", *(PurePosixPath(*parts[:index]).as_posix() for index in range(1, len(parts))))
    entries = {}
    for ancestor in ancestors:
        for item in repository.directory(commit, ancestor):
            if item.kind == "blob" and PurePosixPath(item.path).name.lower().startswith(("license", "notice", "copying")):
                entries[item.path] = (item.mode, item.oid)
    return entries


def prepare(selection: Selection, budget: ResourceBudget | None = None) -> PreparedSelection:
    selection = validate_selection(selection)
    budget = ResourceBudget() if budget is None else budget
    with tempfile.TemporaryDirectory(prefix="sum-skill-source-") as directory:
        repository, commit = open_repository(selection.repository, selection.ref, Path(directory))
        rows = repository.selected_tree(commit, selection.path)
        names = {item.path for item in rows if item.kind == "blob"}
        skill_path = f"{selection.path}/SKILL.md"
        if skill_path not in names:
            raise SkillError(f"selected skill is missing {skill_path}")
        data_by_path = {item.path: budget.read(repository, item) for item in rows if item.kind == "blob"}
        name, unsupported_capabilities = frontmatter(data_by_path[skill_path], skill_path)
        if name != PurePosixPath(selection.path).name:
            raise SkillError(f"{skill_path}: native name {name!r} does not match its selected directory")
        files = []
        for item in rows:
            if item.kind == "commit":
                raise SkillError(f"{item.path}: submodules are not imported")
            if item.kind != "blob":
                raise SkillError(f"{item.path}: unsupported Git tree entry {item.kind}")
            data = data_by_path[item.path]
            relative = PurePosixPath(item.path).relative_to(selection.path).as_posix()
            snapshot_path = f"skill/{name}/{relative}"
            link_target = None
            if item.mode == 0o120000:
                link_target = data.decode("utf-8")
                resolved = normal_target(link_target, item.path)
                if resolved not in names:
                    raise SkillError(f"{item.path}: external or missing shared resource {link_target!r}; select it explicitly")
                link_target = snapshot_link_target(item.path, resolved, selection.path)
                data = link_target.encode("utf-8")
            elif item.mode not in (0o100644, 0o100755):
                raise SkillError(f"{item.path}: unsupported Git file mode {item.mode:o}")
            files.append(SourceFile(relative, snapshot_path, item.mode & 0o777, data, hashlib.sha256(data).hexdigest(), link_target))
        validate_markdown_resources(files, names, selection.path)
        root_files = _license_entries(repository, commit, selection.path)

        def read_license(oid: str, path: str) -> bytes:
            return budget.read(repository, TreeEntry(root_files[path][0], "blob", oid, path))

        def read_entry(path: str) -> tuple[int, str] | None:
            entry = repository.entry(commit, path)
            return (entry.mode, entry.oid) if entry is not None and entry.kind == "blob" else None

        licenses = governing_licenses(root_files, read_license, read_entry)
        if not licenses and not any(Path(item.path).name.lower().startswith(("license", "notice", "copying")) for item in files):
            raise SkillError("source has no license, notice, or copying file")
        all_files = files + [item for item in licenses if item.snapshot_path not in {file.snapshot_path for file in files}]
        all_files = [SourceFile(item.path, item.snapshot_path, item.mode, item.data, hashlib.sha256(item.data).hexdigest(), item.link_target) for item in all_files]
        digest_fields = ((item.path, item.snapshot_path, item.mode, item.digest) for item in all_files)
        return PreparedSelection(selection, repository.origin, commit, name, tuple(all_files), content_digest(digest_fields), unsupported_capabilities)


def inspect(selection: Selection) -> JsonObject:
    prepared = prepare(selection)
    return {**prepared.record("not-installed"), "files": [{"path": item.path, "snapshot_path": item.snapshot_path, "mode": item.mode, "sha256": item.digest} for item in prepared.files],
            "capabilities": {"invocation": False, "permissions": False, "settings": False, "submodules": False,
                              "native_projection": not prepared.unsupported_capabilities,
                              "unsupported": list(prepared.unsupported_capabilities)}}
