from __future__ import annotations

from dataclasses import dataclass
import hashlib
from pathlib import PurePosixPath
import posixpath
import re
from typing import Iterable
from urllib.parse import unquote, urlsplit


SKILL_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
UNSUPPORTED_CAPABILITIES = frozenset({"allowed-tools", "command", "commands", "context", "hooks", "mcp", "mcp-servers", "model", "permission", "permissions", "tools"})


class SkillError(Exception):
    pass


@dataclass(frozen=True, slots=True)
class SourceFile:
    path: str
    snapshot_path: str
    mode: int
    data: bytes
    digest: str
    link_target: str | None = None


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


def frontmatter(data: bytes, source: str) -> tuple[str, tuple[str, ...]]:
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


def normal_target(link: str, source_path: str) -> str:
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
_MARKDOWN_REFERENCE_DEFINITION = re.compile(
    r"(?m)^[ \t]{0,3}\[([^\]\n]+)\]:[ \t]*(?:<([^>\n]+)>|([^\s\n]+))"
)
_MARKDOWN_REFERENCE_USE = re.compile(r"!?\[([^\]\n]+)\](?:\[([^\]\n]*)\])?")


def snapshot_link_target(source_path: str, resolved_target: str, selected_root: str) -> str:
    source = PurePosixPath(source_path).relative_to(selected_root)
    target = PurePosixPath(resolved_target).relative_to(selected_root)
    return posixpath.relpath(target.as_posix(), start=source.parent.as_posix())


def _local_markdown_target(raw: str) -> str | None:
    raw = unquote(raw)
    parsed = urlsplit(raw)
    if not parsed.path or parsed.scheme or parsed.netloc or raw.startswith(("#", "/")):
        return None
    return parsed.path


def _reference_label(value: str) -> str:
    return " ".join(value.split()).casefold()


def _markdown_targets(data: bytes) -> tuple[str, ...]:
    text = data.decode("utf-8")
    targets = []
    for match in _MARKDOWN_LINK.finditer(text):
        target = _local_markdown_target(match.group(1) or match.group(2) or "")
        if target is not None:
            targets.append(target)
    definitions = {}
    spans = []
    for match in _MARKDOWN_REFERENCE_DEFINITION.finditer(text):
        label = _reference_label(match.group(1))
        definitions.setdefault(label, _local_markdown_target(match.group(2) or match.group(3) or ""))
        spans.append(match.span())
    for match in _MARKDOWN_REFERENCE_USE.finditer(text):
        if any(start <= match.start() < end for start, end in spans):
            continue
        if match.end() < len(text) and text[match.end()] == "(":
            continue
        label = match.group(2) or match.group(1)
        target = definitions.get(_reference_label(label))
        if target is not None:
            targets.append(target)
    return tuple(dict.fromkeys(targets))


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


def validate_markdown_resources(files: list[SourceFile], names: set[str], selected_root: str) -> None:
    selected = PurePosixPath(selected_root)
    for item in files:
        if not item.path.lower().endswith(".md"):
            continue
        source_path = f"{selected_root}/{item.path}"
        for link in _markdown_targets(item.data):
            resolved = _relative_path(source_path, link)
            if resolved is None or not resolved.is_relative_to(selected) or resolved.as_posix() not in names:
                raise SkillError(f"{source_path}: missing explicit resource {link!r}; select it explicitly")


def content_digest(files: Iterable[tuple[str, str, int, str]]) -> str:
    value = "\n".join(
        f"{path}\0{snapshot_path}\0{mode:o}\0{digest}"
        for path, snapshot_path, mode, digest in sorted(files, key=lambda item: item[1])
    )
    return hashlib.sha256(value.encode()).hexdigest()
