from __future__ import annotations

from urllib.parse import unquote, urlsplit


def _escaped(text: str, index: int) -> bool:
    backslashes = 0
    index -= 1
    while index >= 0 and text[index] == "\\":
        backslashes += 1
        index -= 1
    return backslashes % 2 == 1


def _closing(text: str, start: int, opener: str, closer: str) -> int:
    depth = 0
    index = start
    while index < len(text):
        if text[index] == opener and not _escaped(text, index):
            depth += 1
        elif text[index] == closer and not _escaped(text, index):
            depth -= 1
            if depth == 0:
                return index
        index += 1
    return -1


def _unescape(value: str) -> str:
    output = []
    index = 0
    while index < len(value):
        if value[index] == "\\" and index + 1 < len(value):
            index += 1
        output.append(value[index])
        index += 1
    return "".join(output)


def _local_target(raw: str) -> str | None:
    raw = unquote(_unescape(raw))
    parsed = urlsplit(raw)
    if not parsed.path or parsed.scheme or parsed.netloc or raw.startswith(("#", "/")):
        return None
    return parsed.path


def _label(value: str) -> str:
    return " ".join(_unescape(value).split()).casefold()


def _destination(text: str, start: int) -> str | None:
    while start < len(text) and text[start] in " \t":
        start += 1
    if start >= len(text):
        return None
    if text[start] == "<":
        end = start + 1
        while end < len(text):
            if text[end] == "\\" and end + 1 < len(text):
                end += 2
                continue
            if text[end] == ">":
                return text[start + 1:end]
            if text[end] in "\r\n":
                return None
            end += 1
        return None
    output = []
    depth = 0
    index = start
    while index < len(text):
        character = text[index]
        if character == "\\" and index + 1 < len(text):
            output.extend((character, text[index + 1]))
            index += 2
            continue
        if character == "(":
            depth += 1
        elif character == ")":
            if depth == 0:
                break
            depth -= 1
        elif character.isspace() and depth == 0:
            break
        output.append(character)
        index += 1
    return "".join(output) or None


def _prose(text: str) -> str:
    output = []
    fence: tuple[str, int] | None = None
    for line in text.splitlines(keepends=True):
        content = line.lstrip(" ")
        indent = len(line) - len(content)
        marker = content[:1]
        width = len(content) - len(content.lstrip(marker)) if marker in ("`", "~") else 0
        if fence is not None:
            if marker == fence[0] and width >= fence[1] and not content[width:].strip():
                fence = None
            output.append("".join(character if character in "\r\n" else " " for character in line))
            continue
        if indent <= 3 and width >= 3:
            fence = (marker, width)
            output.append("".join(character if character in "\r\n" else " " for character in line))
            continue
        visible = list(line)
        cursor = 0
        while (start := line.find("`", cursor)) >= 0:
            width = len(line[start:]) - len(line[start:].lstrip("`"))
            token = "`" * width
            end = line.find(token, start + width)
            if end < 0:
                break
            visible[start:end + width] = " " * (end + width - start)
            cursor = end + width
        output.append("".join(visible))
    return "".join(output)


def _definitions(text: str) -> tuple[dict[str, str | None], list[tuple[int, int]]]:
    definitions = {}
    spans = []
    offset = 0
    for line in text.splitlines(keepends=True):
        content = line.lstrip(" ")
        indent = len(line) - len(content)
        if indent <= 3 and content.startswith("["):
            end = _closing(content, 0, "[", "]")
            if end >= 0 and content[end + 1:end + 2] == ":":
                definitions.setdefault(_label(content[1:end]), _local_target(_destination(content, end + 2) or ""))
                spans.append((offset + indent, offset + len(line)))
        offset += len(line)
    return definitions, spans


def markdown_targets(data: bytes) -> tuple[str, ...]:
    text = _prose(data.decode("utf-8"))
    definitions, spans = _definitions(text)
    targets = []
    index = 0
    span_index = 0
    while index < len(text):
        start = text.find("[", index)
        if start < 0:
            break
        if _escaped(text, start):
            index = start + 1
            continue
        end = _closing(text, start, "[", "]")
        if end < 0:
            break
        index = end + 1
        while span_index < len(spans) and spans[span_index][1] <= start:
            span_index += 1
        if span_index < len(spans) and spans[span_index][0] <= start < spans[span_index][1]:
            continue
        target = None
        if text[index:index + 1] == "(":
            target = _local_target(_destination(text, index + 1) or "")
        elif text[index:index + 1] == "[":
            reference_end = _closing(text, index, "[", "]")
            if reference_end >= 0:
                reference = text[index + 1:reference_end] or text[start + 1:end]
                target = definitions.get(_label(reference))
                index = reference_end + 1
        else:
            target = definitions.get(_label(text[start + 1:end]))
        if target is not None:
            targets.append(target)
    return tuple(dict.fromkeys(targets))
