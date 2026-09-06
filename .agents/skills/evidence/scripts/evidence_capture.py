#!/usr/bin/env python3
"""Capture truthful before/after evidence for one mapped scenario: screenshots, short screencasts, CLI transcripts, or HTTP responses, each bound to
the exact checkout it was taken from, then compare the two roles into one manifest.

Portable: Python 3.11+ standard library only. The browser recipe additionally needs Node 22+ (built-in WebSocket) and a Chromium-family binary;
`capabilities` says what this machine supports, and a recipe without its driver is recorded `blocked` with diagnostics, never a pass and never a
fabricated image. Nothing here starts, stops, or resets an application: you run the base and the candidate in their own checkouts and point each
capture at the one you drove. Originals are never edited; every derived file says what it is.

Usage:
  evidence_capture.py capabilities [--json]
  evidence_capture.py capture --scenario ID --role before|after --kind bugfix|feature|visual|nonvisual [--run ID] [--checkout DIR]
                              [--expect-sha SHA] [--build-id TEXT] [--note TEXT] [--redact REGEX]... [--intentional KEY=VALUE]... RECIPE ...
      RECIPE browser --url URL [--step ACTION=ARG]... [--viewport WxH] [--theme light|dark] [--locale TAG] [--timezone TZ]
                     [--expect-text T]... [--expect-selector SEL=TEXT]... [--observe SEL] [--no-video] [--max-seconds N] [--max-frames N]
                     [--side-effect METHOD URL [--side-effect-text T]...]
             steps: goto=URL click=SELECTOR type=SELECTOR=TEXT press=Enter|Tab|Escape wait-text=TEXT wait=MS
      RECIPE cli [--expect-exit N] [--expect-text T]... [--timeout N] -- COMMAND [ARGS...]
      RECIPE http [--data JSON] [--expect-status N] [--expect-text T]... METHOD URL
  evidence_capture.py unavailable --scenario ID --role before --reason TEXT [--run ID] [--kind KIND]
  evidence_capture.py compare --scenario ID --run ID [--base SHA] [--candidate SHA] [--json]
  evidence_capture.py inspect PATH [--json]
  evidence_capture.py promote --run ID --to DIR [--json]
Exit codes: 0 expectations met / comparison shows what was asked, 1 an expectation was not met or the comparison does not prove the claim,
2 blocked (driver, build, or recorder problem; diagnostics retained), 3 usage.
"""
from __future__ import annotations

import sys

if sys.version_info < (3, 11):
    sys.stderr.write("evidence_capture.py needs Python 3.11 or newer. Blocked, not passed.\n")
    sys.exit(2)

import argparse
import datetime as _dt
import hashlib
import json
import os
import platform
import re
import secrets
import shutil
import struct
import subprocess
import time
import tomllib
import urllib.error
import urllib.request
from pathlib import Path

SCHEMA = 1
HERE = Path(__file__).resolve().parent
BROWSER_DRIVER = HERE / "evidence_browser.mjs"
FENCE = re.compile(r"^```verify[ \t]*\n(.*?)^```[ \t]*$", re.S | re.M)
ROLES = ("before", "after")
KINDS = ("bugfix", "feature", "visual", "nonvisual")
TEXT_LIMIT = 20000
VIDEO_FPS = 10
MAX_SECONDS_CAP = 60
MAX_PIXELS = (1920, 1080)
DEFAULT_MAX_BYTES = 25 * 1024 * 1024
BROWSER_NAMES = ("chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "microsoft-edge", "brave-browser")
BROWSER_MAC = ("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium",
               "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser")
REDACT_DEFAULT = [r"(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}", r"(?i)\b((?:api[_-]?key|token|secret|password|passwd|authorization)\b[\"']?\s*[:=]\s*[\"']?)[^\s\"',;]+",
                  r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b", r"\b(?:gh[pousr]|sk|xox[abp])[_-][A-Za-z0-9_-]{10,}\b"]
ENV_COMPARED = ("recipe", "kind", "viewport", "theme", "locale", "timezone")


class Blocked(Exception):
    """The capture or comparison cannot be trusted; the reason is recorded, never converted into a pass."""


def utc_now():
    return _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def sha256_file(path: Path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(cwd: Path, *args):
    result = subprocess.run(["git", "-C", str(cwd), *args], text=True, capture_output=True)
    return result.returncode, result.stdout.strip(), result.stderr.strip()


def project_root():
    code, out, err = git(Path.cwd(), "rev-parse", "--show-toplevel")
    if code:
        raise Blocked(f"not inside a Git work tree: {err[:200]}")
    return Path(out).resolve()


def evidence_root(root: Path):
    """`evidence` from VERIFY.md (default `.artifacts/evidence`), or VERIFY_EVIDENCE_ROOT for a location outside a disposable checkout."""
    override = os.environ.get("VERIFY_EVIDENCE_ROOT")
    if override:
        return Path(override).expanduser().resolve(), "VERIFY_EVIDENCE_ROOT"
    declared = ".artifacts/evidence"
    contract = root / "VERIFY.md"
    if contract.is_file():
        match = FENCE.search(contract.read_text(encoding="utf-8"))
        if match:
            try:
                value = tomllib.loads(match.group(1)).get("evidence")
                if isinstance(value, str) and value and not Path(value).is_absolute() and ".." not in Path(value).parts:
                    declared = value
            except tomllib.TOMLDecodeError:
                pass
    return (root / declared).resolve(), "VERIFY.md"


def new_run_id():
    return f"{_dt.datetime.now(_dt.timezone.utc):%Y%m%dT%H%M%SZ}-{secrets.token_hex(4)}"


def safe_name(value: str):
    return re.sub(r"[^A-Za-z0-9._-]+", "-", value)[:80]


def find_browser():
    explicit = os.environ.get("EVIDENCE_BROWSER")
    if explicit:
        return explicit if Path(explicit).is_file() and os.access(explicit, os.X_OK) else None
    for name in BROWSER_NAMES:
        found = shutil.which(name)
        if found:
            return found
    for candidate in BROWSER_MAC:
        if Path(candidate).is_file():
            return candidate
    return None


def node_version():
    node = shutil.which("node")
    if not node:
        return None, None
    result = subprocess.run([node, "--version"], text=True, capture_output=True)
    return node, result.stdout.strip()


def ffmpeg_binary():
    """ffmpeg only counts when it actually runs here; a version-manager shim without an active version is not a converter."""
    found = shutil.which("ffmpeg")
    if not found:
        return None
    try:
        return found if subprocess.run([found, "-version"], capture_output=True, timeout=10).returncode == 0 else None
    except (OSError, subprocess.TimeoutExpired):
        return None


def capabilities():
    node, version = node_version()
    major = int(version[1:].split(".")[0]) if version and version.startswith("v") else 0
    browser = find_browser()
    ffmpeg = ffmpeg_binary()
    return {
        "browser": {"supported": bool(browser and major >= 22), "binary": browser, "node": version,
                    "reason": None if browser and major >= 22 else ("no Chromium-family browser found; set EVIDENCE_BROWSER" if not browser else f"node 22+ with global WebSocket required, found {version}")},
        "cli": {"supported": True, "reason": None},
        "http": {"supported": True, "reason": None},
        "desktop": {"supported": False, "reason": "no desktop automation driver is shipped; report such scenarios manually"},
        "video": {"native": "mjpeg-avi (Python standard library, originals are JPEG frames)", "ffmpeg": ffmpeg, "convert": bool(ffmpeg)},
    }


# -- media validation (read the bytes, not just the file name) ----------------------------------------------------------
def png_size(data: bytes):
    if data[:8] != b"\x89PNG\r\n\x1a\n" or data[12:16] != b"IHDR":
        raise ValueError("not a PNG")
    width, height = struct.unpack(">II", data[16:24])
    return width, height


def jpeg_size(data: bytes):
    if data[:2] != b"\xff\xd8":
        raise ValueError("not a JPEG")
    i = 2
    while i + 9 < len(data):
        if data[i] != 0xFF:
            i += 1
            continue
        marker = data[i + 1]
        if marker in (0xD8, 0x01) or 0xD0 <= marker <= 0xD7:
            i += 2
            continue
        length = struct.unpack(">H", data[i + 2:i + 4])[0]
        if marker in (0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF):
            height, width = struct.unpack(">HH", data[i + 5:i + 9])
            return width, height
        i += 2 + length
    raise ValueError("JPEG has no frame header")


def write_mjpeg_avi(frames, out: Path, fps=VIDEO_FPS):
    """An AVI container holding the original JPEG frames (MJPEG). Each source frame is held until the next frame's timestamp at a constant
    frame rate; the held repeats are container timing, the pixels are the originals. Returns (video_frames, held_frames)."""
    if not frames:
        raise ValueError("no frames")
    width, height = jpeg_size(Path(frames[0]["path"]).read_bytes())  # From the bytes, not the recorder's metadata.
    payloads = []
    for index, frame in enumerate(frames):
        data = Path(frame["path"]).read_bytes()
        next_ms = frames[index + 1]["ms"] if index + 1 < len(frames) else frame["ms"] + 1000 // fps
        repeats = max(1, round((next_ms - frame["ms"]) * fps / 1000))
        payloads.extend([data] * repeats)
    total = len(payloads)
    movi = bytearray(b"LIST" + struct.pack("<I", 0) + b"movi")
    index_entries = bytearray()
    for data in payloads:
        offset = len(movi) - 8
        padded = data + (b"\x00" if len(data) % 2 else b"")
        movi += b"00dc" + struct.pack("<I", len(data)) + padded
        index_entries += b"00dc" + struct.pack("<III", 0x10, offset, len(data))
    struct.pack_into("<I", movi, 4, len(movi) - 8)
    avih = struct.pack("<IIIIIIIIIIIIII", 1000000 // fps, 0, 0, 0x10, total, 0, 1, 0, width, height, 0, 0, 0, 0)
    strh = b"vids" + b"MJPG" + struct.pack("<IHHIIIIIIIIhhhh", 0, 0, 0, 0, 1, fps, 0, total, 0, 0, 0, 0, 0, width, height)
    strf = struct.pack("<IiiHH", 40, width, height, 1, 24) + b"MJPG" + struct.pack("<IiiII", width * height * 3, 0, 0, 0, 0)  # BITMAPINFOHEADER
    strl = b"LIST" + struct.pack("<I", 4 + 8 + len(strh) + 8 + len(strf)) + b"strl" + b"strh" + struct.pack("<I", len(strh)) + strh + b"strf" + struct.pack("<I", len(strf)) + strf
    hdrl_body = b"hdrl" + b"avih" + struct.pack("<I", len(avih)) + avih + strl
    hdrl = b"LIST" + struct.pack("<I", len(hdrl_body)) + hdrl_body
    idx1 = b"idx1" + struct.pack("<I", len(index_entries)) + bytes(index_entries)
    body = b"AVI " + hdrl + bytes(movi) + idx1
    out.write_bytes(b"RIFF" + struct.pack("<I", len(body)) + body)
    return total, total - len(frames)


def inspect_avi(data: bytes):
    if data[:4] != b"RIFF" or data[8:12] != b"AVI ":
        raise ValueError("not an AVI")
    pos = data.find(b"avih")
    if pos < 0:
        raise ValueError("AVI has no avih header")
    fields = struct.unpack("<IIIIIIIIIIIIII", data[pos + 8:pos + 8 + 56])
    micro_per_frame, total_frames, width, height = fields[0], fields[4], fields[8], fields[9]
    chunks = 0
    i = data.find(b"movi")
    end = data.find(b"idx1")
    i += 4
    while i + 8 <= (end if end > 0 else len(data)):
        tag, size = data[i:i + 4], struct.unpack("<I", data[i + 4:i + 8])[0]
        if tag == b"00dc":
            chunks += 1
            if data[i + 8:i + 10] != b"\xff\xd8":
                raise ValueError(f"frame {chunks} is not a JPEG")
        i += 8 + size + (size % 2)
    if chunks != total_frames:
        raise ValueError(f"avih says {total_frames} frames, container holds {chunks}")
    fps = 1_000_000 / micro_per_frame if micro_per_frame else 0
    return {"type": "video/x-msvideo", "codec": "MJPG", "width": width, "height": height, "frames": chunks, "fps": round(fps, 3),
            "duration_ms": round(chunks * micro_per_frame / 1000) if micro_per_frame else 0}


def inspect_media(path: Path):
    data = path.read_bytes()
    info = {"file": path.name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()}
    suffix = path.suffix.lower()
    if suffix == ".png":
        width, height = png_size(data)
        info.update(type="image/png", width=width, height=height)
    elif suffix in (".jpg", ".jpeg"):
        width, height = jpeg_size(data)
        info.update(type="image/jpeg", width=width, height=height)
    elif suffix == ".avi":
        info.update(inspect_avi(data))
    elif suffix == ".mp4" and shutil.which("ffprobe"):
        probe = subprocess.run(["ffprobe", "-v", "error", "-select_streams", "v:0", "-count_frames", "-show_entries", "stream=width,height,nb_read_frames:format=duration", "-of", "json", str(path)],
                               text=True, capture_output=True)
        try:
            parsed = json.loads(probe.stdout)
            stream = parsed["streams"][0]
            info.update(type="video/mp4", width=int(stream["width"]), height=int(stream["height"]), frames=int(stream["nb_read_frames"]),
                        duration_ms=round(float(parsed["format"]["duration"]) * 1000))
        except (ValueError, KeyError, IndexError):
            raise ValueError(f"ffprobe could not read {path.name}: {probe.stderr.strip()[:200]}")
    else:
        info.update(type="text/plain" if suffix in (".txt", ".json", ".log", ".html") else "application/octet-stream")
    if info.get("width") == 0 or info.get("height") == 0 or info.get("frames") == 0 or info["bytes"] == 0:
        raise ValueError(f"{path.name} is empty ({info})")
    return info


# -- redaction -------------------------------------------------------------------------------------------------------------
class Redactor:
    def __init__(self, extra):
        self.patterns = [re.compile(p) for p in [*REDACT_DEFAULT, *extra]]
        self.spans = set()  # Distinct redacted values; the same secret in a transcript and in the manifest counts once.

    @property
    def count(self):
        return len(self.spans)

    def __call__(self, text):
        if not text:
            return text
        for pattern in self.patterns:
            def replace(match):
                self.spans.add(match.group(0))
                keep = match.group(1) if match.re.groups else ""
                return f"{keep}[REDACTED]"
            text = pattern.sub(replace, text)
        return text


def clip(text):
    return text if len(text) <= TEXT_LIMIT else text[:TEXT_LIMIT] + f"\n... [{len(text) - TEXT_LIMIT} more characters not recorded]"


# -- build identity ------------------------------------------------------------------------------------------------------
def checkout_identity(checkout: str | None, expect_sha: str | None, build_id: str | None):
    record = {"path": None, "sha": None, "dirty": None, "branch": None, "build_id": build_id, "expected_sha": expect_sha, "matches_expected": None}
    if checkout:
        path = Path(checkout).resolve()
        code, top, err = git(path, "rev-parse", "--show-toplevel")
        if code:
            raise Blocked(f"--checkout {checkout} is not a Git work tree: {err[:200]}")
        record["path"] = top
        record["sha"] = git(path, "rev-parse", "HEAD")[1]
        record["dirty"] = bool(git(path, "status", "--porcelain", "--untracked-files=normal")[1])
        record["branch"] = git(path, "rev-parse", "--abbrev-ref", "HEAD")[1] or None
    if expect_sha:
        if record["sha"] is None:
            record["matches_expected"] = None
        else:
            record["matches_expected"] = record["sha"].startswith(expect_sha) or expect_sha.startswith(record["sha"])
    return record


# -- capture ------------------------------------------------------------------------------------------------------------------
def parse_step(value):
    action, sep, arg = value.partition("=")
    if action == "goto":
        return {"action": "goto", "url": arg}
    if action == "click":
        return {"action": "click", "selector": arg}
    if action == "type":
        selector, _, text = arg.partition("=")
        return {"action": "type", "selector": selector, "text": text}
    if action == "press":
        return {"action": "press", "key": arg}
    if action == "wait-text":
        return {"action": "wait-text", "text": arg}
    if action == "wait":
        return {"action": "wait", "ms": int(arg)}
    raise SystemExit(f"--step {value!r}: unknown action; use goto=, click=, type=SEL=TEXT, press=, wait-text=, wait=MS")


def environment_facts():
    return {"platform": platform.platform(), "python": platform.python_version(), "node": node_version()[1], "hostname_recorded": False, "user_profile": "none (ephemeral)"}


def capture_browser(args, capture_dir: Path, record, redact: Redactor):
    caps = capabilities()["browser"]
    record["environment"].update(viewport=args.viewport, theme=args.theme, locale=args.locale, timezone=args.timezone, browser=caps["binary"])
    if not caps["supported"]:
        raise Blocked(f"browser recipe unavailable: {caps['reason']}")
    width, height = (int(v) for v in args.viewport.lower().split("x"))
    if width > MAX_PIXELS[0] or height > MAX_PIXELS[1] or width < 200 or height < 200:
        raise SystemExit(f"--viewport must be between 200x200 and {MAX_PIXELS[0]}x{MAX_PIXELS[1]}")
    seconds = min(args.max_seconds, MAX_SECONDS_CAP)
    steps = [{"action": "goto", "url": args.url}, *[parse_step(s) for s in args.step]]
    frame_dir = capture_dir / "frames"
    frame_dir.mkdir()
    diagnostics = capture_dir / "browser-diagnostics.log"
    job = {"browser": caps["binary"], "viewport": {"width": width, "height": height}, "locale": args.locale, "theme": args.theme, "timezone": args.timezone, "steps": steps,
           "screencast": not args.no_video, "jpeg_quality": 75, "max_frames": min(args.max_frames, seconds * 30), "max_bytes": DEFAULT_MAX_BYTES, "frame_dir": str(frame_dir),
           "screenshot": str(capture_dir / "screenshot.png"), "diagnostics": str(diagnostics), "step_timeout_ms": args.step_timeout * 1000, "settle_ms": 300,
           "text_limit": TEXT_LIMIT, "observe_selector": args.observe}
    record["steps"] = steps
    record["diagnostics"] = diagnostics.name
    started = time.monotonic()
    try:
        proc = subprocess.run([shutil.which("node"), str(BROWSER_DRIVER)], input=json.dumps(job), text=True, capture_output=True, timeout=seconds + args.step_timeout * (len(steps) + 2))
    except subprocess.TimeoutExpired:
        raise Blocked("browser driver exceeded its time bound; diagnostics retained")
    try:
        result = json.loads(proc.stdout.strip().splitlines()[-1]) if proc.stdout.strip() else {}
    except ValueError:
        result = {}
    if proc.stderr.strip():
        with diagnostics.open("a", encoding="utf-8") as handle:
            handle.write(proc.stderr[-4000:])
    record["driver"] = {"script": BROWSER_DRIVER.name, "exit": proc.returncode, "browser_version": result.get("observations", {}).get("browser_version"),
                        "protocol": result.get("observations", {}).get("protocol_version")}
    record["environment"]["browser_version"] = record["driver"]["browser_version"]
    record["step_results"] = result.get("steps", [])
    record["console"] = [{**c, "text": redact(c.get("text", ""))} for c in result.get("console", [])][:50]
    observations = result.get("observations", {})
    record["observations"] = {"url": observations.get("url"), "title": observations.get("title"), "text": redact(clip(observations.get("text") or "")),
                              "selector": args.observe, "selector_text": redact(observations.get("selector_text")) if observations.get("selector_text") is not None else None,
                              "viewport": observations.get("viewport")}
    if not result.get("ok"):
        errors = result.get("errors") or [f"driver exited {proc.returncode} without a result"]
        raise Blocked("browser capture failed: " + "; ".join(errors))
    seen = observations.get("viewport") or {}
    if seen and (seen.get("width"), seen.get("height")) != (width, height):
        record["limitations"].append(f"requested viewport {width}x{height} but the page saw {seen.get('width')}x{seen.get('height')}")
    if seen and bool(seen.get("dark")) != (args.theme == "dark"):
        record["limitations"].append(f"requested theme {args.theme} but the page reports dark={seen.get('dark')}")
    text = observations.get("text") or ""
    for expected in args.expect_text:
        record["assertions"].append({"expectation": f"page text contains {expected!r}", "met": expected in text})
    for pair in args.expect_selector:
        selector, _, expected = pair.partition("=")
        observed = observations.get("selector_text") if selector == args.observe else None
        record["assertions"].append({"expectation": f"{selector} text is {expected!r}", "met": observed is not None and observed.strip() == expected,
                                     "observed": redact(observed) if observed is not None else "(selector not observed; pass the same selector to --observe)"})
    if args.side_effect:
        method, url = args.side_effect
        status, body, error = http_request(method, url, None, 10)
        record["side_effect"] = {"method": method.upper(), "url": url, "status": status, "body": redact(clip(body)), "error": error}
        for expected in args.side_effect_text:
            record["assertions"].append({"expectation": f"side effect {method.upper()} {url} body contains {expected!r}", "met": expected in body})
        if error:
            record["assertions"].append({"expectation": f"side effect {method.upper()} {url} answers", "met": False, "observed": error})
    media = []
    shot = capture_dir / "screenshot.png"
    if shot.is_file():
        media.append({**inspect_media(shot), "role": "screenshot", "derived": False})
    frames = sorted(frame_dir.glob("frame-*.jpg"))
    frame_meta = {f["file"]: f for f in result.get("frames", [])}
    if not args.no_video:
        if len(frames) < 1:
            record["limitations"].append("screencast produced no frames; the recorder did not deliver any (see diagnostics)")
        else:
            listed = [{"path": str(f), "ms": frame_meta.get(f.name, {}).get("ms", i * 100), "width": frame_meta.get(f.name, {}).get("width", width), "height": frame_meta.get(f.name, {}).get("height", height)}
                      for i, f in enumerate(frames)]
            video = capture_dir / "screencast.avi"
            total, held = write_mjpeg_avi(listed, video)
            info = inspect_media(video)
            media.append({**info, "role": "screencast", "derived": False, "source_frames": len(frames), "held_frames": held,
                          "encoding": f"MJPEG in AVI at {VIDEO_FPS} fps; original JPEG frames under frames/ with their capture times; held repeats are timing only"})
            for f in frames:
                media.append({**inspect_media(f), "role": "frame", "derived": False, "ms": frame_meta.get(f.name, {}).get("ms")})
            if result.get("observations", {}).get("screencast_limit"):
                record["limitations"].append(f"screencast stopped at bound {result['observations']['screencast_limit']}")
            ffmpeg = ffmpeg_binary() if args.convert else None
            if ffmpeg:
                mp4 = capture_dir / "screencast.mp4"
                cmd = [ffmpeg, "-v", "error", "-y", "-i", str(video), "-c:v", "libx264", "-pix_fmt", "yuv420p", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", str(mp4)]
                conv = subprocess.run(cmd, text=True, capture_output=True)
                version = subprocess.run([ffmpeg, "-version"], text=True, capture_output=True).stdout.splitlines()[:1]
                if conv.returncode == 0 and mp4.is_file():
                    media.append({**inspect_media(mp4), "role": "screencast-converted", "derived": True, "source": video.name, "tool": version[0] if version else "ffmpeg", "command": cmd[1:]})
                else:
                    record["limitations"].append(f"ffmpeg conversion failed ({conv.returncode}); original AVI kept: {conv.stderr.strip()[:200]}")
            elif args.convert:
                record["limitations"].append("no working ffmpeg on PATH; original AVI kept, no converted copy")
    record["frames_dir"] = "frames"
    return media


def http_request(method, url, data, timeout):
    if not re.match(r"^https?://", url):
        raise SystemExit(f"http needs an http(s):// URL, got {url!r}")
    body = data.encode() if data is not None else None
    request = urllib.request.Request(url, data=body, method=method.upper(), headers={"Content-Type": "application/json"} if body else {})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.read().decode("utf-8", "replace"), None
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace"), None
    except (urllib.error.URLError, OSError) as exc:
        return None, "", str(exc)


def write_text(capture_dir: Path, stem: str, text: str, redact: Redactor, before: int):
    redacted = redact.count > before
    path = capture_dir / (f"{stem}.redacted.txt" if redacted else f"{stem}.txt")
    path.write_text(text, encoding="utf-8")
    return path


def capture_cli(args, capture_dir: Path, record, redact: Redactor):
    command = args.argv[1:] if args.argv[:1] == ["--"] else args.argv
    if not command:
        raise SystemExit("cli needs -- COMMAND [ARGS...]")
    record["steps"] = [{"action": "run", "command": command, "cwd": str(Path.cwd())}]
    record["environment"]["env_names"] = sorted(os.environ)[:200]  # Names only; values are never recorded.
    record["visual_proof"] = "not-applicable" if args.kind == "nonvisual" else "not-captured (cli recipe)"
    try:
        proc = subprocess.run(command, text=True, capture_output=True, timeout=args.timeout)
    except FileNotFoundError:
        raise Blocked(f"command not found: {command[0]}")
    except subprocess.TimeoutExpired:
        raise Blocked(f"command exceeded {args.timeout}s; recorded as blocked, not failed")
    before = redact.count
    transcript = f"$ {' '.join(command)}\n--- stdout ---\n{clip(proc.stdout)}\n--- stderr ---\n{clip(proc.stderr)}\n--- exit {proc.returncode} ---\n"
    path = write_text(capture_dir, "transcript", redact(transcript), redact, before)
    record["observations"] = {"exit": proc.returncode, "stdout": redact(clip(proc.stdout)), "stderr": redact(clip(proc.stderr))}
    record["assertions"].append({"expectation": f"exit == {args.expect_exit}", "met": proc.returncode == args.expect_exit, "observed": proc.returncode})
    for expected in args.expect_text:
        record["assertions"].append({"expectation": f"stdout contains {expected!r}", "met": expected in proc.stdout})
    return [{**inspect_media(path), "role": "transcript", "derived": False, "redacted": path.name.endswith(".redacted.txt")}]


def capture_http(args, capture_dir: Path, record, redact: Redactor):
    record["steps"] = [{"action": "http", "method": args.method.upper(), "url": args.url, "data": args.data}]
    record["visual_proof"] = "not-applicable" if args.kind == "nonvisual" else "not-captured (http recipe)"
    status, body, error = http_request(args.method, args.url, args.data, args.timeout)
    before = redact.count
    path = write_text(capture_dir, "response", redact(f"{args.method.upper()} {args.url}\nstatus: {status}\n{'error: ' + error if error else ''}\n--- body ---\n{clip(body)}\n"), redact, before)
    record["observations"] = {"status": status, "body": redact(clip(body)), "error": error}
    if error:
        raise Blocked(f"request failed: {error}")
    if args.expect_status is not None:
        record["assertions"].append({"expectation": f"status == {args.expect_status}", "met": status == args.expect_status, "observed": status})
    for expected in args.expect_text:
        record["assertions"].append({"expectation": f"body contains {expected!r}", "met": expected in body})
    return [{**inspect_media(path), "role": "response", "derived": False, "redacted": path.name.endswith(".redacted.txt")}]


def scenario_dir(root_dir: Path, run_id: str, scenario: str):
    return root_dir / safe_name(run_id) / safe_name(scenario)


def cmd_capture(args):
    root = project_root()
    root_dir, source = evidence_root(root)
    run_id = args.run or os.environ.get("EVIDENCE_RUN") or new_run_id()
    redact = Redactor(args.redact)
    build = checkout_identity(args.checkout, args.expect_sha, args.build_id)
    stem = f"{args.role}-{(build['sha'] or 'nobuild')[:12]}"
    capture_dir = scenario_dir(root_dir, run_id, args.scenario) / stem
    if capture_dir.exists():
        raise SystemExit(f"{capture_dir} already exists; a capture is never overwritten. Use a new --run or remove it deliberately.")
    capture_dir.mkdir(parents=True)
    record = {"schema": SCHEMA, "run": run_id, "scenario": args.scenario, "feature": args.feature or args.scenario.split(".")[0], "role": args.role, "kind": args.kind,
              "recipe": args.recipe, "note": args.note, "started_at": utc_now(), "checkout": build, "environment": environment_facts(), "steps": [],
              "assertions": [], "limitations": [], "intentional_differences": dict(kv.split("=", 1) for kv in args.intentional if "=" in kv),
              "media": [], "outcome": None, "blocked_reason": None, "evidence_root": {"path": str(root_dir), "declared_by": source}}
    clock = time.monotonic()
    try:
        if build["matches_expected"] is False:
            raise Blocked(f"build mismatch: --checkout HEAD {build['sha']} is not the expected {args.expect_sha}; this is a stale or wrong build")
        if args.recipe == "browser":
            record["media"] = capture_browser(args, capture_dir, record, redact)
        elif args.recipe == "cli":
            record["media"] = capture_cli(args, capture_dir, record, redact)
        else:
            record["media"] = capture_http(args, capture_dir, record, redact)
        met = all(a["met"] for a in record["assertions"])
        record["outcome"] = "pass" if met else "fail"
    except Blocked as exc:
        record["outcome"], record["blocked_reason"] = "blocked", str(exc)
        for path in sorted(capture_dir.iterdir()):
            if path.is_file() and path.name != "capture.json":
                try:
                    record["media"].append({**inspect_media(path), "role": "diagnostic", "derived": False})
                except ValueError:
                    record["media"].append({"file": path.name, "bytes": path.stat().st_size, "role": "diagnostic", "derived": False, "sha256": sha256_file(path)})
    if build["dirty"]:
        record["limitations"].append("the driven checkout had uncommitted changes; the capture is bound to its HEAD but the running code may differ")
    if redact.count:
        record["limitations"].append(f"{redact.count} text span(s) redacted; screenshots cannot be redacted, use synthetic data")
    record["redaction"] = {"patterns": len(redact.patterns), "count": redact.count, "labelled": redact.count > 0}
    record["ended_at"] = utc_now()
    record["seconds"] = round(time.monotonic() - clock, 3)
    record["content_hashes"] = {m["file"]: m["sha256"] for m in record["media"]}
    (capture_dir / "capture.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    shown = capture_dir
    try:
        shown = capture_dir.relative_to(root)
    except ValueError:
        pass
    unmet = [a["expectation"] for a in record["assertions"] if not a["met"]]
    print(f"evidence: {shown}/capture.json run={run_id} outcome={record['outcome']}" + (f" ({record['blocked_reason']})" if record["blocked_reason"] else "")
          + (f" UNMET: {'; '.join(unmet)}" if unmet else ""))
    return {"pass": 0, "fail": 1, "blocked": 2}[record["outcome"]]


def cmd_unavailable(args):
    root = project_root()
    root_dir, source = evidence_root(root)
    run_id = args.run or os.environ.get("EVIDENCE_RUN") or new_run_id()
    capture_dir = scenario_dir(root_dir, run_id, args.scenario) / f"{args.role}-unavailable"
    if capture_dir.exists():
        raise SystemExit(f"{capture_dir} already exists")
    capture_dir.mkdir(parents=True)
    record = {"schema": SCHEMA, "run": run_id, "scenario": args.scenario, "feature": args.scenario.split(".")[0], "role": args.role, "kind": args.kind, "recipe": None,
              "started_at": utc_now(), "ended_at": utc_now(), "checkout": {"sha": None}, "outcome": "unavailable", "blocked_reason": None, "reason": args.reason,
              "assertions": [], "media": [], "limitations": [f"{args.role} state not captured: {args.reason}"], "content_hashes": {},
              "evidence_root": {"path": str(root_dir), "declared_by": source}}
    (capture_dir / "capture.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    print(f"evidence: {capture_dir}/capture.json run={run_id} outcome=unavailable ({args.reason})")
    return 0


# -- compare -------------------------------------------------------------------------------------------------------------------
def load_captures(directory: Path):
    found = {}
    for child in sorted(directory.iterdir()) if directory.is_dir() else []:
        manifest = child / "capture.json"
        if not manifest.is_file():
            continue
        record = json.loads(manifest.read_text(encoding="utf-8"))
        found.setdefault(record["role"], []).append((child, record))
    return found


def verify_hashes(directory: Path, record):
    mismatched = []
    for name, digest in record.get("content_hashes", {}).items():
        path = directory / name if (directory / name).is_file() else directory / "frames" / name
        if not path.is_file() or sha256_file(path) != digest:
            mismatched.append(name)
    return mismatched


def html_escape(text):
    return str(text).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


def side_by_side_html(comparison, before, after):
    """Presentation follows the labelled two-up convention of vercel-labs/before-and-after (see references/ATTRIBUTION.md): originals are
    referenced, never rewritten; only CSS scaling for display is applied."""
    def panel(label, entry):
        if not entry:
            return f"<section><h2>{label}</h2><p class='missing'>not available</p></section>"
        record, rel = entry["record"], entry["dir"]
        shot = next((m for m in record.get("media", []) if m.get("role") == "screenshot"), None)
        clip_ = next((m for m in record.get("media", []) if m.get("role") in ("screencast-converted", "screencast")), None)
        transcript = next((m for m in record.get("media", []) if m.get("role") in ("transcript", "response")), None)
        parts = [f"<h2>{label} <small>{html_escape(record.get('outcome'))}</small></h2>", f"<p>sha <code>{html_escape((record.get('checkout') or {}).get('sha'))}</code></p>"]
        if shot:
            parts.append(f"<img src='{rel}/{shot['file']}' alt='{label} screenshot {shot['width']}x{shot['height']}' width='{shot['width']}' height='{shot['height']}'>")
        if clip_:
            parts.append(f"<video controls muted src='{rel}/{clip_['file']}'></video><p><small>{html_escape(clip_.get('encoding') or clip_.get('tool') or '')}</small></p>")
        if transcript:
            parts.append(f"<pre>{html_escape((record.get('observations') or {}).get('stdout') or (record.get('observations') or {}).get('body') or '')}</pre>")
        if record.get("outcome") == "unavailable":
            parts.append(f"<p class='missing'>{html_escape(record.get('reason'))}</p>")
        for a in record.get("assertions", []):
            parts.append(f"<p>{'✓' if a['met'] else '✗'} {html_escape(a['expectation'])}</p>")
        return "<section>" + "".join(parts) + "</section>"
    return ("<!doctype html><meta charset='utf-8'><title>" + html_escape(comparison["scenario"]) + " before/after</title>"
            "<style>body{font-family:system-ui;margin:1rem}main{display:grid;grid-template-columns:1fr 1fr;gap:1rem}img,video{max-width:100%;height:auto;border:1px solid #888}"
            "pre{white-space:pre-wrap;background:#f4f4f4;padding:.5rem}.missing{color:#a00}</style>"
            f"<h1>{html_escape(comparison['scenario'])}: {html_escape(comparison['verdict'])}</h1>"
            f"<p>{html_escape(comparison['label'])}. Derived view: originals are referenced unchanged and scaled by CSS for display only.</p>"
            "<main>" + panel("Before", before) + panel("After", after) + "</main>")


def cmd_compare(args):
    root = project_root()
    root_dir, _ = evidence_root(root)
    directory = scenario_dir(root_dir, args.run, args.scenario)
    if not directory.is_dir():
        raise SystemExit(f"no captures for scenario {args.scenario} in run {args.run} under {root_dir}")
    captures = load_captures(directory)
    comparison = {"schema": SCHEMA, "run": args.run, "scenario": args.scenario, "compared_at": utc_now(), "base": {"sha": args.base}, "candidate": {"sha": args.candidate},
                  "before": None, "after": None, "findings": [], "verdict": None, "label": None, "visual_proof": None, "derived": []}
    entries = {}
    for role in ROLES:
        found = captures.get(role, [])
        if len(found) > 1:
            comparison["findings"].append(f"{len(found)} {role} captures in this run; a comparison needs exactly one per role")
            continue
        if found:
            child, record = found[0]
            mismatched = verify_hashes(child, record)
            if mismatched:
                comparison["findings"].append(f"{role}: content hash mismatch for {', '.join(mismatched)}; the originals were altered after capture")
            entries[role] = {"dir": child.name, "record": record}
            comparison[role] = {"dir": child.name, "outcome": record["outcome"], "sha": (record.get("checkout") or {}).get("sha"), "recipe": record.get("recipe"), "kind": record.get("kind"),
                                "assertions": record.get("assertions", []), "limitations": record.get("limitations", []), "blocked_reason": record.get("blocked_reason"),
                                "media": [{k: m.get(k) for k in ("file", "type", "sha256", "bytes", "width", "height", "frames", "duration_ms", "role", "derived")} for m in record.get("media", [])],
                                "started_at": record.get("started_at"), "ended_at": record.get("ended_at")}
    before, after = entries.get("before"), entries.get("after")
    if not after:
        comparison["findings"].append("no after capture; nothing to compare")
    kind = (after or before or {"record": {}})["record"].get("kind")
    comparison["kind"] = kind
    # Build identity: the candidate capture must come from the declared candidate SHA, the before from the base.
    for role, expected in (("before", args.base), ("after", args.candidate)):
        entry = entries.get(role)
        if entry and expected and entry["record"]["outcome"] != "unavailable":
            sha = (entry["record"].get("checkout") or {}).get("sha")
            if not sha:
                comparison["findings"].append(f"{role}: no --checkout was recorded, so the build cannot be tied to {expected[:12]}")
            elif not sha.startswith(expected):
                comparison["findings"].append(f"{role}: captured from {sha[:12]}, not the declared {'base' if role == 'before' else 'candidate'} {expected[:12]} (stale or wrong build)")
    # Environment consistency, unless the difference is declared intentional in either capture.
    if before and after and before["record"]["outcome"] != "unavailable":
        intentional = {**before["record"].get("intentional_differences", {}), **after["record"].get("intentional_differences", {})}
        for key in ENV_COMPARED:
            b = before["record"].get(key) if key in ("recipe", "kind") else before["record"].get("environment", {}).get(key)
            a = after["record"].get(key) if key in ("recipe", "kind") else after["record"].get("environment", {}).get(key)
            if b != a and key not in intentional:
                comparison["findings"].append(f"{key} differs: before {b!r}, after {a!r}; declare it with --intentional {key}=... or recapture consistently")
        comparison["intentional_differences"] = intentional
        for key in ("browser_version", "platform"):
            b, a = before["record"].get("environment", {}).get(key), after["record"].get("environment", {}).get(key)
            if b != a:
                comparison.setdefault("warnings", []).append(f"{key} differs: before {b!r}, after {a!r}")
    visual = any(m.get("role") == "screenshot" for e in (before, after) if e for m in e["record"].get("media", []))
    comparison["visual_proof"] = "captured" if visual else ("not-applicable" if kind == "nonvisual" else "not-captured")
    outcomes = {role: entries[role]["record"]["outcome"] if role in entries else "missing" for role in ROLES}
    comparison["outcomes"] = outcomes
    blocked = [r for r, o in outcomes.items() if o == "blocked"]
    if comparison["findings"]:
        verdict, label = "mismatch", "the captures cannot be compared as taken: " + "; ".join(comparison["findings"])
    elif blocked:
        verdict, label = "capture-failed", f"{', '.join(blocked)} capture blocked: " + "; ".join(entries[r]["record"].get("blocked_reason") or "" for r in blocked)
    elif outcomes["after"] != "pass":
        verdict, label = "after-fails", "the candidate does not pass the scenario's assertions"
    elif outcomes["before"] in ("missing", "unavailable"):
        verdict = "after-only"
        label = "before-unavailable: " + (before["record"].get("reason") if before else "no before capture was recorded") + "; this shows the candidate only, not a red/green pair"
    elif outcomes["before"] == "fail":
        verdict, label = "red-green", "the base fails the user path and the candidate passes it" + (" (transcripts; visual proof not applicable)" if kind == "nonvisual" else "")
    elif kind in ("feature", "visual"):
        verdict, label = "before-after", f"both states captured (before {outcomes['before']}, after pass); for a new feature the before state shows the absence, not a defect"
    else:
        verdict, label = "before-also-passes", "the base already passes the same path; this is not evidence that a defect was fixed"
    comparison["verdict"], comparison["label"] = verdict, label
    comparison["proves_claim"] = verdict in ("red-green", "before-after") or (verdict == "after-only" and kind in ("feature", "visual"))
    html = directory / "comparison.html"
    html.write_text(side_by_side_html(comparison, before, after), encoding="utf-8")
    comparison["derived"].append({"file": html.name, "type": "text/html", "transformation": "labelled two-up page referencing the original files; CSS scaling only", "sha256": sha256_file(html)})
    (directory / "comparison.json").write_text(json.dumps(comparison, indent=2) + "\n", encoding="utf-8")
    if args.json:
        print(json.dumps(comparison, indent=2))
    else:
        print(f"comparison: {directory / 'comparison.json'} verdict={verdict} visual_proof={comparison['visual_proof']}\n{label}")
    return 0 if comparison["proves_claim"] else (2 if verdict in ("mismatch", "capture-failed") else 1)


def cmd_inspect(args):
    path = Path(args.path)
    try:
        info = inspect_media(path)
    except (ValueError, OSError) as exc:
        print(f"inspect: {path} invalid: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(info, indent=2) if args.json else f"inspect: {path} {info['type']} " + " ".join(f"{k}={info[k]}" for k in ("width", "height", "frames", "duration_ms", "bytes") if k in info))
    return 0


def cmd_promote(args):
    root = project_root()
    root_dir, _ = evidence_root(root)
    source = root_dir / safe_name(args.run)
    if not source.is_dir():
        raise SystemExit(f"run {args.run} not found under {root_dir}")
    dest = Path(args.to).expanduser().resolve() / safe_name(args.run)
    if dest.exists():
        raise SystemExit(f"{dest} already exists; promotion never overwrites")
    shutil.copytree(source, dest)
    verified, failed = 0, []
    for manifest in dest.rglob("capture.json"):
        record = json.loads(manifest.read_text(encoding="utf-8"))
        bad = verify_hashes(manifest.parent, record)
        failed += [f"{manifest.parent.name}/{b}" for b in bad]
        verified += len(record.get("content_hashes", {})) - len(bad)
    summary = {"run": args.run, "from": str(source), "to": str(dest), "files_verified": verified, "hash_failures": failed, "promoted_at": utc_now()}
    (dest / "promoted.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(summary, indent=2) if args.json else f"promoted: {dest} verified={verified} failures={len(failed)}")
    return 0 if not failed else 1


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)
    caps = sub.add_parser("capabilities", help="Which recipes this machine can drive")
    caps.add_argument("--json", action="store_true")
    cap = sub.add_parser("capture", help="Capture one role of one scenario")
    cap.add_argument("--scenario", required=True)
    cap.add_argument("--feature")
    cap.add_argument("--role", required=True, choices=ROLES)
    cap.add_argument("--kind", required=True, choices=KINDS)
    cap.add_argument("--run", help="Run id shared by the before and after captures (default: EVIDENCE_RUN or a new id)")
    cap.add_argument("--checkout", help="The checkout whose build you are driving; its HEAD is recorded as the build identity")
    cap.add_argument("--expect-sha", help="SHA the checkout must be at (base for before, candidate for after); a mismatch blocks the capture")
    cap.add_argument("--build-id", help="Additional build identity (image tag, bundle hash)")
    cap.add_argument("--note")
    cap.add_argument("--redact", action="append", default=[], metavar="REGEX", help="Extra pattern to redact from recorded text; group 1 is kept")
    cap.add_argument("--intentional", action="append", default=[], metavar="KEY=VALUE", help="Declare an intended before/after difference (viewport, theme, locale, ...)")
    recipes = cap.add_subparsers(dest="recipe", required=True)
    browser = recipes.add_parser("browser", help="Drive a headless Chromium-family browser over CDP")
    browser.add_argument("--url", required=True)
    browser.add_argument("--step", action="append", default=[], metavar="ACTION=ARG")
    browser.add_argument("--viewport", default="1280x800")
    browser.add_argument("--theme", default="light", choices=("light", "dark"))
    browser.add_argument("--locale", default="en-US")
    browser.add_argument("--timezone", default="UTC")
    browser.add_argument("--expect-text", action="append", default=[])
    browser.add_argument("--expect-selector", action="append", default=[], metavar="SEL=TEXT", help="Exact text of the --observe selector")
    browser.add_argument("--observe", help="Selector whose text is recorded as the observable end state")
    browser.add_argument("--no-video", action="store_true")
    browser.add_argument("--max-seconds", type=int, default=20)
    browser.add_argument("--max-frames", type=int, default=600)
    browser.add_argument("--step-timeout", type=int, default=15)
    browser.add_argument("--convert", action="store_true", help="Also write an mp4 with ffmpeg when available; the AVI original is kept")
    browser.add_argument("--side-effect", nargs=2, metavar=("METHOD", "URL"), help="Verify a side effect after the steps with one HTTP request")
    browser.add_argument("--side-effect-text", action="append", default=[])
    cli = recipes.add_parser("cli", help="Run a command and record its transcript")
    cli.add_argument("--expect-exit", type=int, default=0)
    cli.add_argument("--expect-text", action="append", default=[])
    cli.add_argument("--timeout", type=int, default=120)
    cli.add_argument("argv", nargs=argparse.REMAINDER, metavar="-- COMMAND [ARGS...]")
    http = recipes.add_parser("http", help="Send one HTTP request and record the response")
    http.add_argument("--data")
    http.add_argument("--expect-status", type=int)
    http.add_argument("--expect-text", action="append", default=[])
    http.add_argument("--timeout", type=int, default=10)
    http.add_argument("method")
    http.add_argument("url")
    unavailable = sub.add_parser("unavailable", help="Record that a role could not be captured (for example no baseline access)")
    unavailable.add_argument("--scenario", required=True)
    unavailable.add_argument("--role", default="before", choices=ROLES)
    unavailable.add_argument("--kind", default="bugfix", choices=KINDS)
    unavailable.add_argument("--reason", required=True)
    unavailable.add_argument("--run")
    compare = sub.add_parser("compare", help="Compare the before and after captures of one scenario in one run")
    compare.add_argument("--scenario", required=True)
    compare.add_argument("--run", required=True)
    compare.add_argument("--base", help="Base SHA the before capture must come from")
    compare.add_argument("--candidate", help="Candidate SHA the after capture must come from")
    compare.add_argument("--json", action="store_true")
    inspect = sub.add_parser("inspect", help="Read a media file's real dimensions, frames, and duration")
    inspect.add_argument("path")
    inspect.add_argument("--json", action="store_true")
    promote = sub.add_parser("promote", help="Copy a run outside a disposable checkout and verify every recorded hash")
    promote.add_argument("--run", required=True)
    promote.add_argument("--to", required=True)
    promote.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.command == "capabilities":
            info = capabilities()
            print(json.dumps(info, indent=2) if args.json else "\n".join(f"{k}: {'supported' if v.get('supported') else v.get('reason', v)}" for k, v in info.items()))
            return 0
        return {"capture": cmd_capture, "unavailable": cmd_unavailable, "compare": cmd_compare, "inspect": cmd_inspect, "promote": cmd_promote}[args.command](args)
    except Blocked as exc:
        print(f"blocked: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit as exc:
        if isinstance(exc.code, str):
            print(exc.code, file=sys.stderr)
            sys.exit(3)
        raise
