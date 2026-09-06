#!/usr/bin/env python3
"""Publish before/after evidence into one pull request's marked block: validated media, receipts per content hash, scoped body replacement.

Capture (`evidence_capture.py`), this publisher, and the PR body format stay separate. Nothing here starts a browser, converts video,
or hosts media: the only upload path is the GitHub CLI's own `gh pr edit --attach` (GitHub CLI 2.99+), running under whoever invokes
this script. A comparison manifest is a claim about files; every file is re-read, hash-checked, and copied into an approved publish
copy before it is offered to `gh`. Anything that cannot be proven leaves the local evidence and the previous PR body exactly as they were.

The block markup (before/after tables, Preview labels, own-line videos, HTML video tables, the `<!-- before-and-after:start/end -->`
markers) follows vercel-labs/before-and-after `skill/scripts/format.mjs` at 8306d34f459b6704e08e6adb5829fcddb0dc3557 (PolyForm Shield
1.0.0, copyright (c) 2026 James Clements); see ../references/ATTRIBUTION.md and ../references/LICENSE-before-and-after.txt.
"""
from __future__ import annotations

import argparse
import datetime as _dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import evidence_capture as capture  # noqa: E402  (same directory; stdlib only)

SCHEMA = 1
MARKER_START = "<!-- before-and-after:start -->"
MARKER_END = "<!-- before-and-after:end -->"
MEDIA_MARK = re.compile(r"<!-- sum-media: ([^>]*?) -->")
ATTACHMENT_URL = re.compile(r"https://github\.com/user-attachments/assets/[A-Za-z0-9._-]+")
LOCAL_REF = re.compile(r"\]\(\./[^)\s]+\)")
IMAGE_TYPES = {"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}
VIDEO_TYPES = {"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm"}
IMAGE_LIMIT = 10 * 1024 * 1024   # GitHub rejects larger images and GIFs.
VIDEO_LIMIT = 100 * 1024 * 1024  # GitHub's video attachment ceiling.
ATTACH_BATCH = 50                 # `gh --attach` accepts at most 50 files per command.
GH_ATTACH_MIN = (2, 99, 0)
WRITE_PERMISSIONS = {"WRITE", "MAINTAIN", "ADMIN"}
EXIT = {"published": 0, "unchanged": 0, "planned": 0, "refused": 1, "failed": 1, "deferred": 2, "uncertain": 4}


class Refused(Exception):
    """Nothing external was written; the reason names what could not be proven."""


class GhTimeout(Exception):
    pass


def utc_now():
    return _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def sha256_bytes(data: bytes):
    return hashlib.sha256(data).hexdigest()


def normalize(text: str):
    return "\n".join(line.rstrip() for line in text.replace("\r\n", "\n").split("\n")).strip()


def block_hash(text: str):
    return sha256_bytes(normalize(text).encode("utf-8"))


def write_json(path: Path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


# -- gh -------------------------------------------------------------------------------------------------------------------
class Gh:
    def __init__(self, binary: str | None, timeout=60):
        self.binary = binary or shutil.which("gh")
        self.timeout = timeout

    def run(self, args, *, cwd=None, timeout=None, check=True):
        if not self.binary:
            raise Refused("GitHub CLI (gh) is not installed or not on PATH")
        try:
            result = subprocess.run([self.binary, *args], cwd=cwd, text=True, capture_output=True, timeout=timeout or self.timeout)
        except subprocess.TimeoutExpired as exc:
            raise GhTimeout(f"gh {args[0]} {args[1] if len(args) > 1 else ''} did not finish within {timeout or self.timeout}s; its effect is unknown") from exc
        if check and result.returncode:
            raise Refused(f"gh {' '.join(args[:2])} exited {result.returncode}: {(result.stderr or result.stdout).strip()[-600:]}")
        return result

    def json(self, args, **kw):
        out = self.run(args, **kw).stdout
        try:
            return json.loads(out)
        except ValueError as exc:
            raise Refused(f"gh {' '.join(args[:2])} did not return JSON: {out[:200]}") from exc


def gh_capabilities(gh: Gh):
    """What the installed GitHub CLI can do, read from the binary itself; a version number alone never proves `--attach` exists."""
    info = {"gh": gh.binary, "version": None, "attach": False, "authenticated": None, "reason": None}
    if not gh.binary:
        info["reason"] = "gh is not installed or not on PATH"
        return info
    try:
        version = gh.run(["--version"], timeout=20).stdout
        match = re.search(r"gh version (\d+)\.(\d+)\.(\d+)", version)
        info["version"] = ".".join(match.groups()) if match else version.strip().splitlines()[0][:60]
        help_text = gh.run(["pr", "edit", "--help"], timeout=20, check=False).stdout
        info["attach"] = "--attach" in help_text and bool(match) and tuple(int(g) for g in match.groups()) >= GH_ATTACH_MIN
        if not info["attach"]:
            info["reason"] = f"gh {info['version']} has no `pr edit --attach`; GitHub CLI {'.'.join(map(str, GH_ATTACH_MIN))}+ is required. Local capture works; publication is deferred."
        auth = gh.run(["auth", "status"], timeout=30, check=False)
        info["authenticated"] = auth.returncode == 0
    except (Refused, GhTimeout) as exc:
        info["reason"] = str(exc)
    return info


# -- plan ------------------------------------------------------------------------------------------------------------------
def load_json(path: Path, what):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise Refused(f"{what} unreadable at {path}: {exc}") from exc


def same_sha(a: str | None, b: str | None):
    if not a or not b or len(a) < 7 or len(b) < 7:
        return False
    return a.startswith(b) or b.startswith(a)


def contained_file(run_dir: Path, *parts: str):
    """A path built only from single, non-dot components under the run directory, resolved without following any symlink out of it."""
    for part in parts:
        if not part or part in (".", "..") or "/" in part or "\\" in part or part != part.strip() or re.search(r"\s", part):
            raise Refused(f"manifest names an unsafe path component {part!r}")
    path = run_dir.joinpath(*parts)
    real = Path(os.path.realpath(path))
    if real != path.absolute() or run_dir not in real.parents:
        raise Refused(f"{path} escapes the evidence run directory (symlink or relative component); nothing outside it is ever read")
    if not real.is_file():
        raise Refused(f"{path} is not a regular file")
    return real


def publish_name(scenario, role, sha, ext):
    return f"{capture.safe_name(scenario)}--{role}--{sha[:12]}{ext}"


def stage_copy(media_dir: Path, name: str, data: bytes):
    """An approved publish copy is written once, verified after the write, and never replaced with different content."""
    digest = sha256_bytes(data)
    media_dir.mkdir(parents=True, exist_ok=True)
    copy = media_dir / name
    if copy.exists():
        if sha256_bytes(copy.read_bytes()) != digest:
            raise Refused(f"publish copy {copy.name} exists with different content; remove it deliberately")
        return copy
    tmp = copy.with_name(copy.name + ".tmp")
    tmp.write_bytes(data)
    if sha256_bytes(tmp.read_bytes()) != digest:
        tmp.unlink()
        raise Refused(f"publish copy {name} did not verify after copying")
    os.replace(tmp, copy)
    return copy


def select_media(comparison, role, findings):
    """The screenshot and the renderable screencast of one role, chosen from the manifest; AVI originals are named, never uploaded."""
    entry = comparison.get(role)
    chosen = []
    if not entry or entry.get("outcome") in (None, "unavailable", "missing"):
        return chosen
    media = entry.get("media") or []
    shot = next((m for m in media if m.get("role") == "screenshot" and m.get("type") in IMAGE_TYPES), None)
    if shot:
        chosen.append({"role": role, "kind": "image", "manifest": shot})
    converted = next((m for m in media if m.get("role") == "screencast-converted" and m.get("type") in VIDEO_TYPES), None)
    original = next((m for m in media if m.get("role") == "screencast"), None)
    if converted:
        chosen.append({"role": role, "kind": "video", "manifest": converted})
    elif original:
        findings.append(f"{role}: screencast {original.get('file')} is {original.get('type') or 'an AVI'}; GitHub renders mp4, mov, and webm only. "
                        "Recapture with --convert (ffmpeg) to publish the video; the screenshot is published without it.")
    return chosen


def plan_scenario(run_dir: Path, scenario: str, args, redactor: capture.Redactor, media_dir: Path):
    directory = run_dir / capture.safe_name(scenario)
    comparison = load_json(directory / "comparison.json", f"comparison manifest for {scenario}")
    row = {"id": scenario, "feature": None, "verdict": comparison.get("verdict"), "label": comparison.get("label"), "kind": comparison.get("kind"),
           "proves_claim": comparison.get("proves_claim"), "candidate": (comparison.get("candidate") or {}).get("sha"), "base": (comparison.get("base") or {}).get("sha"),
           "publishable": False, "findings": [], "media": [], "unpublished": []}
    findings = row["findings"]
    if comparison.get("scenario") != scenario:
        findings.append(f"manifest is for scenario {comparison.get('scenario')!r}, not {scenario!r}")
    if not same_sha(row["candidate"], args.candidate):
        findings.append(f"comparison candidate {row['candidate']!r} is not the candidate {args.candidate[:12]} being published; evidence from another build is never attached")
    if args.base and row["base"] and not same_sha(row["base"], args.base):
        findings.append(f"comparison base {row['base']!r} differs from --base {args.base[:12]}")
    if row["verdict"] in ("mismatch", "capture-failed", "after-fails", None):
        findings.append(f"verdict {row['verdict']!r} does not show a candidate passing; {comparison.get('label') or 'nothing'} is not published as evidence")
    before_out, after_out = (comparison.get("outcomes") or {}).get("before"), (comparison.get("outcomes") or {}).get("after")
    texts = [scenario, str(comparison.get("label") or "")] + [str(f) for f in comparison.get("findings") or []]
    for role in capture.ROLES:
        entry = comparison.get(role) or {}
        texts += [str(x) for x in entry.get("limitations") or []] + [str(entry.get("blocked_reason") or "")]
    before_mark = redactor.count
    for text in texts:
        redactor(text)
    if redactor.count > before_mark:
        findings.append(f"{redactor.count - before_mark} secret-shaped span(s) in the manifest text that would reach the PR body; publish nothing from this scenario until the text is clean")
    if findings:
        return row
    pending = []
    for role in capture.ROLES:
        entry = comparison.get(role)
        if not entry or entry.get("outcome") in (None, "unavailable", "missing"):
            continue
        record_dir = run_dir / capture.safe_name(scenario) / str(entry.get("dir") or "")
        try:
            record = load_json(contained_file(run_dir, capture.safe_name(scenario), str(entry.get("dir") or ""), "capture.json"), f"{role} capture manifest")
        except Refused as exc:
            findings.append(str(exc))
            continue
        row["feature"] = row["feature"] or record.get("feature")
        redaction = (record.get("redaction") or {}).get("count") or 0
        for item in select_media(comparison, role, findings):
            manifest = item["manifest"]
            if item["kind"] == "image" and redaction:
                findings.append(f"{role}: {redaction} secret-shaped span(s) were redacted from this capture's text; a screenshot cannot be redacted, so it is treated as containing them and is not published")
                continue
            try:
                real = contained_file(run_dir, capture.safe_name(scenario), str(entry.get("dir") or ""), str(manifest.get("file") or ""))
                data = real.read_bytes()
                digest = sha256_bytes(data)
                declared = manifest.get("sha256")
                recorded = (record.get("content_hashes") or {}).get(manifest["file"])
                if digest != declared or digest != recorded:
                    raise Refused(f"{role}: {manifest['file']} hash {digest[:12]} does not match the comparison ({str(declared)[:12]}) and capture ({str(recorded)[:12]}) manifests; the original was altered")
                limit = IMAGE_LIMIT if item["kind"] == "image" else VIDEO_LIMIT
                if len(data) > limit:
                    raise Refused(f"{role}: {manifest['file']} is {len(data)} bytes, over GitHub's {limit} byte limit for {item['kind']}s")
                info = capture.inspect_media(real)
                if item["kind"] == "image" and (info.get("width"), info.get("height")) != (manifest.get("width"), manifest.get("height")):
                    raise Refused(f"{role}: {manifest['file']} reads as {info.get('width')}x{info.get('height')}, manifest says {manifest.get('width')}x{manifest.get('height')}")
                ext = (IMAGE_TYPES | VIDEO_TYPES)[manifest["type"]]
                pending.append((publish_name(scenario, role, digest, ext), data))
                row["media"].append({"role": role, "kind": item["kind"], "sha256": digest, "bytes": len(data), "type": manifest["type"], "width": info.get("width"),
                                     "height": info.get("height"), "duration_ms": info.get("duration_ms") or manifest.get("duration_ms"),
                                     "source": str(real.relative_to(run_dir)), "publish_copy": publish_name(scenario, role, digest, ext)})
            except (Refused, ValueError, OSError) as exc:
                findings.append(f"{exc}")
    row["outcomes"] = {"before": before_out, "after": after_out}
    row["publishable"] = not findings and any(m["role"] == "after" for m in row["media"])
    if row["publishable"]:
        try:
            for name, data in pending:
                stage_copy(media_dir, name, data)
        except Refused as exc:
            row["publishable"] = False
            findings.append(str(exc))
    if not row["publishable"] and not findings:
        findings.append("no publishable after media (screenshot or converted screencast) in this comparison")
    return row


def cmd_plan(args):
    if args.evidence_root:
        root_dir = Path(args.evidence_root).expanduser().resolve()
    else:
        root_dir, _ = capture.evidence_root(capture.project_root())
    run_dir = Path(os.path.realpath(root_dir / capture.safe_name(args.run)))
    if not run_dir.is_dir():
        raise Refused(f"run {args.run} not found under {root_dir}")
    if not re.fullmatch(r"[0-9a-f]{40}", args.candidate or ""):
        raise Refused("--candidate must be the full 40-hex candidate SHA recorded for the PR head")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repo or ""):
        raise Refused("--repo must be owner/name")
    publish_dir = Path(args.publish_dir).expanduser().resolve() if args.publish_dir else run_dir / "publish"
    media_dir = publish_dir / "media"
    scenarios = args.scenario or sorted(p.parent.name for p in run_dir.glob("*/comparison.json"))
    if not scenarios:
        raise Refused(f"run {args.run} has no comparison manifests")
    redactor = capture.Redactor(args.redact)
    rows = [plan_scenario(run_dir, s, args, redactor, media_dir) for s in scenarios]
    plan = {"schema": SCHEMA, "created_at": utc_now(), "run": args.run, "evidence_root": str(root_dir), "run_dir": str(run_dir), "publish_dir": str(publish_dir), "media_dir": str(media_dir),
            "destination": {"repository": args.repo, "number": args.pr}, "candidate": args.candidate, "base": args.base, "verification_runs": args.verification_run,
            "captured_by": args.captured_by, "scenarios": rows, "publishable_media": sum(len(r["media"]) for r in rows if r["publishable"]),
            "publishable_scenarios": [r["id"] for r in rows if r["publishable"]], "refused_scenarios": {r["id"]: r["findings"] for r in rows if not r["publishable"]}}
    plan["block_preview"] = render_block(plan, lambda sha: None, final=False) if plan["publishable_scenarios"] else None
    if plan["block_preview"]:
        mark = redactor.count
        redactor(plan["block_preview"])
        if redactor.count > mark:
            plan["publishable_scenarios"], plan["publishable_media"] = [], 0
            for row in rows:
                if row["publishable"]:
                    row["publishable"] = False
                    row["findings"].append("the rendered block contained secret-shaped text")
            plan["block_preview"] = None
            plan["refused_scenarios"] = {r["id"]: r["findings"] for r in rows}
    write_json(publish_dir / "plan.json", plan)
    if args.json:
        print(json.dumps(plan, indent=2))
    else:
        print(f"plan: {publish_dir / 'plan.json'} publishable={plan['publishable_scenarios']} media={plan['publishable_media']}")
        for sid, findings in plan["refused_scenarios"].items():
            print(f"  {sid}: not published: " + "; ".join(findings))
    return 0 if plan["publishable_scenarios"] else 1


# -- block ----------------------------------------------------------------------------------------------------------------
def media_ref(entry, url, alt):
    return f"![{alt}]({url or './' + entry['publish_copy']})"


def render_block(plan, url_for, final: bool):
    """The marked block. `url_for(sha)` gives a known attachment URL or None (then a local publish-copy reference `gh --attach` rewrites).
    `final` renders video pairs as an HTML table once every URL is known; before that videos stay on their own lines so `gh` can upload them."""
    lines = [MARKER_START, f"<!-- sum-evidence: run={plan['run']} candidate={plan['candidate']} base={plan.get('base') or 'unknown'} scenarios={len(plan['publishable_scenarios'])} -->",
             "### Before/after evidence", ""]
    who = plan.get("captured_by") or "the task worker"
    runs = ", ".join(f"`{r}`" for r in plan.get("verification_runs") or []) or "none recorded"
    lines += [f"Captured by {who} from candidate `{plan['candidate'][:12]}`" + (f" against base `{plan['base'][:12]}`" if plan.get("base") else "")
              + f"; evidence run `{plan['run']}`; verification run(s) {runs}. Worker media is a claim about the candidate build; it is not the coordinator's root verification.", ""]
    for row in plan["scenarios"]:
        if not row["publishable"]:
            continue
        label = f"`{row['id']}`" + (f" (feature `{row['feature']}`)" if row.get("feature") else "")
        lines += [f"#### {label}: {row['verdict']}", f"_{row['label']}_", ""]
        by = {(m["role"], m["kind"]): m for m in row["media"]}
        for kind in ("image", "video"):
            before, after = by.get(("before", kind)), by.get(("after", kind))
            if not after:
                continue
            noun = "screenshot" if kind == "image" else "screencast"
            marks = " ".join(f"{m['role']}={m['sha256']}" for m in (before, after) if m)
            if kind == "image":
                if before:
                    lines += [f"<!-- sum-media: {marks} -->", "| Before | After |", "|:---:|:---:|",
                              f"| {media_ref(before, url_for(before['sha256']), f'before {noun} {before['sha256'][:12]}')} | {media_ref(after, url_for(after['sha256']), f'after {noun} {after['sha256'][:12]}')} |", ""]
                else:
                    lines += [f"<!-- sum-media: {marks} -->", "| Preview (after only) |", "|:---:|", f"| {media_ref(after, url_for(after['sha256']), f'preview {noun} {after['sha256'][:12]}')} |", ""]
                continue
            urls = [url_for(m["sha256"]) if m else None for m in (before, after)]
            if final and urls[1] and (not before or urls[0]):
                lines += [f"<!-- sum-media: {marks} -->", "<table>", "  <tr>"]
                if before:
                    lines.append("    <th>Before (screencast)</th>")
                lines += [f"    <th>{'After' if before else 'Preview'} (screencast)</th>", "  </tr>", "  <tr>"]
                if before:
                    lines.append(f'    <td><video src="{urls[0]}" width="100%" controls></video></td>')
                lines += [f'    <td><video src="{urls[1]}" width="100%" controls></video></td>', "  </tr>", "</table>", ""]
                continue
            if before:
                lines += [f"**Before (screencast)**", f"<!-- sum-media: before={before['sha256']} -->", media_ref(before, urls[0], f"before {noun} {before['sha256'][:12]}"), ""]
            lines += [f"**{'After' if before else 'Preview (after only)'} (screencast)**", f"<!-- sum-media: after={after['sha256']} -->",
                      media_ref(after, urls[1], f"{'after' if before else 'preview'} {noun} {after['sha256'][:12]}"), ""]
        for note in row.get("unpublished") or []:
            lines += [f"_{note}_", ""]
    while lines and lines[-1] == "":
        lines.pop()
    lines.append(MARKER_END)
    return "\n".join(lines) + "\n"


def find_block(body: str):
    """Exactly one marked block, or none. Two starts, a missing end, or an inverted pair is malformed and refused."""
    start, end = body.find(MARKER_START), body.find(MARKER_END)
    if start == -1 and end == -1:
        return None
    if start == -1 or end == -1 or end < start:
        raise Refused("the PR body has an incomplete before-and-after marker block; fix it by hand before publishing")
    if body.find(MARKER_START, start + len(MARKER_START)) != -1 or body.find(MARKER_END, end + len(MARKER_END)) != -1:
        raise Refused("the PR body has more than one before-and-after marker block; publication owns exactly one")
    return start, end + len(MARKER_END)


def splice(body: str, span, block: str):
    """Replace only the marked span (or append); every other byte of the body is kept."""
    if span is None:
        prefix = body.rstrip()
        return f"{prefix}\n\n{block}" if prefix else block
    inner = block.rstrip("\n")
    return body[:span[0]] + inner + body[span[1]:]


def harvest_urls(block: str):
    """Attachment URLs GitHub wrote for each `<!-- sum-media: role=sha ... -->` marker, read from the lines that follow it in order."""
    found = {}
    lines = block.replace("\r\n", "\n").split("\n")
    for index, line in enumerate(lines):
        match = MEDIA_MARK.search(line)
        if not match:
            continue
        shas = [kv.split("=", 1)[1] for kv in match.group(1).split() if "=" in kv]
        urls = []
        for following in lines[index + 1:]:
            if MEDIA_MARK.search(following) or following.startswith("#### ") or following == MARKER_END:
                break
            urls += ATTACHMENT_URL.findall(following)
            if len(urls) >= len(shas):
                break
        for sha, url in zip(shas, urls):
            found.setdefault(sha, url)
    return found


# -- receipts ---------------------------------------------------------------------------------------------------------------
def load_receipts(path: Path):
    if path.is_file():
        value = load_json(path, "receipts")
        if value.get("schema") != SCHEMA:
            raise Refused(f"receipts at {path} have schema {value.get('schema')!r}, expected {SCHEMA}")
        return value
    return {"schema": SCHEMA, "attachments": {}, "blocks": [], "events": []}


def receipt_key(sha, destination):
    return f"{sha}@{destination['repository']}#{destination['number']}"


def record_event(receipts, **event):
    receipts["events"].append({"at": utc_now(), **event})
    receipts["events"] = receipts["events"][-200:]


# -- publish ----------------------------------------------------------------------------------------------------------------
class Publisher:
    def __init__(self, args):
        self.args = args
        self.plan = load_json(Path(args.plan), "publish plan")
        if self.plan.get("schema") != SCHEMA:
            raise Refused(f"plan schema {self.plan.get('schema')!r} is not {SCHEMA}")
        self.dest = self.plan["destination"]
        self.repo, self.number = self.dest["repository"], int(self.dest["number"])
        self.publish_dir = Path(self.plan["publish_dir"])
        self.media_dir = Path(self.plan["media_dir"])
        self.receipts_path = Path(args.receipts).expanduser().resolve() if args.receipts else self.publish_dir / "receipts.json"
        self.receipts = load_receipts(self.receipts_path)
        self.gh = Gh(args.gh, timeout=args.timeout)
        self.result = {"schema": SCHEMA, "at": utc_now(), "destination": self.dest, "candidate": self.plan["candidate"], "run": self.plan["run"], "outcome": None, "reason": None,
                       "uploaded": [], "reused": [], "unpublished": dict(self.plan.get("refused_scenarios") or {}), "edits": 0, "video_table": None, "pr_url": None,
                       "receipts": str(self.receipts_path), "plan": str(Path(args.plan).resolve()), "dry_run": bool(args.dry_run)}

    def url_for(self, sha):
        entry = self.receipts["attachments"].get(receipt_key(sha, self.dest))
        return entry["url"] if entry else None

    def save_receipts(self):
        write_json(self.receipts_path, self.receipts)

    def fetch(self):
        data = self.gh.json(["pr", "view", str(self.number), "--repo", self.repo, "--json", "body,headRefOid,state,url,number"], timeout=60)
        return data

    def media(self):
        return [m for row in self.plan["scenarios"] if row["publishable"] for m in row["media"]]

    def finish(self, outcome, reason=None, **extra):
        self.result.update(outcome=outcome, reason=reason, **extra)
        results = self.publish_dir / "results"
        results.mkdir(parents=True, exist_ok=True)
        path = results / f"{utc_now().replace(':', '')}-{outcome}.json"
        write_json(path, self.result)
        self.result["record"] = str(path)
        return self.result

    def run(self):
        if not self.plan.get("publishable_scenarios"):
            return self.finish("refused", "the plan has nothing publishable; see unpublished")
        caps = gh_capabilities(self.gh)
        self.result["gh"] = caps
        if not caps["attach"]:
            return self.finish("deferred", caps["reason"] or "gh --attach unavailable")
        if caps["authenticated"] is False:
            return self.finish("refused", "gh is not authenticated; nothing was uploaded or edited")
        repo = self.gh.json(["repo", "view", self.repo, "--json", "nameWithOwner,visibility,viewerPermission"], timeout=60)
        if repo.get("nameWithOwner") != self.repo:
            return self.finish("refused", f"gh resolved {repo.get('nameWithOwner')!r}, not the planned destination {self.repo}")
        visibility = str(repo.get("visibility") or "").lower()
        self.result["visibility"] = visibility
        if visibility != self.args.visibility:
            return self.finish("refused", f"destination {self.repo} is {visibility}; --visibility {self.args.visibility} was declared. Nothing was uploaded.")
        if str(repo.get("viewerPermission") or "").upper() not in WRITE_PERMISSIONS:
            return self.finish("refused", f"gh has {repo.get('viewerPermission') or 'no'} permission on {self.repo}; editing the PR needs write access. Nothing was uploaded.")
        pr = self.fetch()
        self.result["pr_url"] = pr.get("url")
        if str(pr.get("state", "")).upper() != "OPEN":
            return self.finish("refused", f"PR #{self.number} is {pr.get('state')}; evidence is published only to an open PR")
        if pr.get("headRefOid") != self.plan["candidate"]:
            self.result["head_sha"] = pr.get("headRefOid")
            if not self.args.allow_head_mismatch:
                return self.finish("refused", f"PR head {str(pr.get('headRefOid'))[:12]} is not the candidate {self.plan['candidate'][:12]} the evidence was captured from; pass --allow-head-mismatch to publish it labelled as such")
        # Rebase onto the latest body: compute, refetch, and only proceed when nothing moved in between (bounded).
        known_blocks = {b["sha256"] for b in self.receipts["blocks"] if b.get("destination") == f"{self.repo}#{self.number}"}
        body = pr["body"] or ""
        for attempt in range(3):
            try:
                span = find_block(body)
            except Refused as exc:
                return self.finish("refused", f"{exc}. Nothing was uploaded or edited.")
            if span is not None:
                existing = body[span[0]:span[1]]
                if block_hash(existing) not in known_blocks and not self.args.replace_foreign_block:
                    self.result["existing_block_sha256"] = block_hash(existing)
                    return self.finish("refused", "the PR already carries a before-and-after block this publisher did not write (someone edited it, or another tool wrote it); "
                                                  "inspect it and pass --replace-foreign-block to take it over. Prose outside the block is never touched either way.")
            videos_known = all(self.url_for(m["sha256"]) for m in self.media() if m["kind"] == "video")
            block = render_block(self.plan, self.url_for, final=videos_known)
            new_body = splice(body, span, block)
            again = self.fetch()
            if (again["body"] or "") == body:
                break
            body = again["body"] or ""
        else:
            return self.finish("refused", "the PR body changed three times while preparing the edit; retry when it is quiet")
        to_upload = [m for m in self.media() if not self.url_for(m["sha256"])]
        self.result["reused"] = [m["sha256"] for m in self.media() if self.url_for(m["sha256"])]
        if not to_upload and span is not None and normalize(body[span[0]:span[1]]) == normalize(render_block(self.plan, self.url_for, final=True)):
            return self.finish("unchanged", "the PR already carries this exact block with every attachment; nothing to upload or edit")
        if len(to_upload) > ATTACH_BATCH:
            return self.finish("refused", f"{len(to_upload)} files to attach exceeds gh's {ATTACH_BATCH} per command; publish fewer scenarios per run")
        for m in to_upload:
            copy = self.media_dir / m["publish_copy"]
            if not copy.is_file() or sha256_bytes(copy.read_bytes()) != m["sha256"]:
                return self.finish("refused", f"publish copy {m['publish_copy']} is missing or altered; run plan again")
        self.result["to_upload"] = [m["sha256"] for m in to_upload]
        if self.args.dry_run:
            self.result["body_preview"] = new_body
            return self.finish("planned", "dry run: nothing uploaded or edited")
        body_file = self.publish_dir / "body-next.md"
        body_file.write_text(new_body, encoding="utf-8")
        (self.publish_dir / "body-previous.md").write_text(body, encoding="utf-8")
        attach = []
        for m in to_upload:
            attach += ["--attach", f"./{m['publish_copy']}"]
        record_event(self.receipts, kind="edit-attempt", destination=f"{self.repo}#{self.number}", attachments=[m["sha256"] for m in to_upload], previous_body_sha256=sha256_bytes(body.encode()))
        self.save_receipts()
        outcome = None
        try:
            edit = self.gh.run(["pr", "edit", str(self.number), "--repo", self.repo, "--body-file", str(body_file), *attach], cwd=self.media_dir, timeout=self.args.timeout, check=False)
            self.result["edits"] += 1
            if edit.returncode:
                outcome = ("partial", f"gh pr edit exited {edit.returncode}: {(edit.stderr or edit.stdout).strip()[-600:]}")
        except GhTimeout as exc:
            self.result["edits"] += 1
            outcome = ("timeout", str(exc))
        return self.reconcile(body, span, to_upload, outcome)

    def reconcile(self, previous_body, previous_span, to_upload, failure):
        """After a write that may or may not have landed: read GitHub, record whatever attachments exist, and either finish the block or restore the body."""
        try:
            now = self.fetch()
        except (Refused, GhTimeout) as exc:
            record_event(self.receipts, kind="uncertain", reason=str(exc))
            self.save_receipts()
            return self.finish("uncertain", f"the edit {'failed' if failure else 'was sent'} and the PR could not be read back: {exc}. Inspect the PR; receipts may be incomplete; do not retry blindly.")
        body = now["body"] or ""
        if body == previous_body:
            record_event(self.receipts, kind="edit-not-applied", reason=failure[1] if failure else "body unchanged")
            self.save_receipts()
            return self.finish("failed", (failure[1] if failure else "gh reported success but the PR body did not change") + ". The PR body is intact; local evidence is untouched"
                               + ("; an upload may have completed without being referenced, which GitHub keeps harmlessly." if failure and failure[0] == "timeout" else "."))
        try:
            span = find_block(body)
        except Refused as exc:
            span = None
            self.result["malformed"] = str(exc)
        block = body[span[0]:span[1]] if span else ""
        harvested = harvest_urls(block)
        for m in to_upload:
            url = harvested.get(m["sha256"])
            if url:
                self.receipts["attachments"][receipt_key(m["sha256"], self.dest)] = {"url": url, "sha256": m["sha256"], "kind": m["kind"], "file": m["publish_copy"], "published_at": utc_now(),
                                                                                    "destination": f"{self.repo}#{self.number}", "candidate": self.plan["candidate"], "run": self.plan["run"]}
                self.result["uploaded"].append(m["sha256"])
        self.save_receipts()
        missing = [m["sha256"] for m in to_upload if not self.url_for(m["sha256"])]
        prose_ok = span is not None and normalize(body[:span[0]] + body[span[1]:]) == normalize(previous_body[:previous_span[0]] + previous_body[previous_span[1]:] if previous_span else previous_body)
        leftovers = LOCAL_REF.findall(block)
        if failure and not (missing or leftovers) and prose_ok:
            self.result["gh_warning"] = failure[1]
        if missing or leftovers or not prose_ok:
            reason = "; ".join(filter(None, [failure[1] if failure else None, f"{len(missing)} attachment(s) have no URL" if missing else None,
                                             f"local references remain in the block: {', '.join(leftovers[:5])}" if leftovers else None,
                                             None if prose_ok else "prose outside the block differs from the body read before the edit (concurrent edit or a malformed block)"]))
            restored = self.restore(previous_body)
            return self.finish("failed" if restored else "uncertain", reason + (". The previous PR body was restored" if restored else ". The PR body could NOT be restored; inspect it")
                               + "; uploaded attachments are kept in receipts for reuse.", restored=restored)
        self.receipts["blocks"].append({"destination": f"{self.repo}#{self.number}", "sha256": block_hash(block), "at": utc_now(), "candidate": self.plan["candidate"], "run": self.plan["run"]})
        record_event(self.receipts, kind="published", destination=f"{self.repo}#{self.number}", uploaded=self.result["uploaded"], reused=self.result["reused"])
        self.save_receipts()
        # Second step: video pairs become an HTML table now that every URL is known (own-line players stay when this step cannot be applied).
        final = render_block(self.plan, self.url_for, final=True)
        if normalize(final) != normalize(block) and any(m["kind"] == "video" for m in self.media()):
            self.result["video_table"] = self.upgrade(body, span, final)
        return self.finish("published", f"{len(self.result['uploaded'])} uploaded, {len(self.result['reused'])} reused, block replaced in place" if previous_span else
                           f"{len(self.result['uploaded'])} uploaded, {len(self.result['reused'])} reused, block appended")

    def upgrade(self, body, span, final):
        new_body = splice(body, span, final)
        body_file = self.publish_dir / "body-final.md"
        body_file.write_text(new_body, encoding="utf-8")
        try:
            edit = self.gh.run(["pr", "edit", str(self.number), "--repo", self.repo, "--body-file", str(body_file)], cwd=self.media_dir, timeout=self.args.timeout, check=False)
            self.result["edits"] += 1
            after = self.fetch()
            landed = after["body"] or ""
            new_span = find_block(landed)
            if edit.returncode or not new_span or normalize(landed[new_span[0]:new_span[1]]) != normalize(final):
                return "not-applied: own-line videos stay (" + ((edit.stderr or edit.stdout).strip()[-200:] if edit.returncode else "block differs after the edit") + ")"
            self.receipts["blocks"].append({"destination": f"{self.repo}#{self.number}", "sha256": block_hash(landed[new_span[0]:new_span[1]]), "at": utc_now(), "candidate": self.plan["candidate"], "run": self.plan["run"]})
            self.save_receipts()
            return "applied"
        except (Refused, GhTimeout) as exc:
            return f"uncertain: {exc}"

    def restore(self, previous_body):
        path = self.publish_dir / "body-restore.md"
        path.write_text(previous_body, encoding="utf-8")
        try:
            self.gh.run(["pr", "edit", str(self.number), "--repo", self.repo, "--body-file", str(path)], timeout=self.args.timeout)
            record_event(self.receipts, kind="restored", destination=f"{self.repo}#{self.number}")
            self.save_receipts()
            return (self.fetch()["body"] or "") == previous_body
        except (Refused, GhTimeout) as exc:
            record_event(self.receipts, kind="restore-failed", reason=str(exc))
            self.save_receipts()
            return False


def cmd_publish(args):
    publisher = Publisher(args)
    result = publisher.run()
    if args.json:
        print(json.dumps(result, indent=2))
    else:
        print(f"publish: {result['outcome']} {result['destination']['repository']}#{result['destination']['number']}: {result['reason']}")
        for sid, findings in (result.get("unpublished") or {}).items():
            print(f"  unpublished {sid}: " + "; ".join(findings))
    return EXIT[result["outcome"]] if result["outcome"] in EXIT else 1


def cmd_block(args):
    plan = load_json(Path(args.plan), "publish plan")
    receipts = load_receipts(Path(args.receipts)) if args.receipts else {"attachments": {}}
    dest = plan["destination"]
    url_for = lambda sha: (receipts["attachments"].get(receipt_key(sha, dest)) or {}).get("url")  # noqa: E731
    if args.attach_list:
        for row in plan["scenarios"]:
            if row["publishable"]:
                for m in row["media"]:
                    if not url_for(m["sha256"]):
                        print(f"./{m['publish_copy']}")
        return 0
    print(render_block(plan, url_for, final=args.final), end="")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)
    caps = sub.add_parser("capabilities", help="Whether the installed gh can attach media (read from the binary), and whether it is authenticated")
    caps.add_argument("--gh", help="GitHub CLI binary to inspect (default: gh on PATH)")
    caps.add_argument("--json", action="store_true")
    plan = sub.add_parser("plan", help="Validate one run's comparison manifests and stage approved publish copies; writes plan.json, uploads nothing")
    plan.add_argument("--run", required=True)
    plan.add_argument("--scenario", action="append", help="Scenario id (default: every comparison in the run)")
    plan.add_argument("--repo", required=True, help="owner/name of the destination repository, exactly as recorded")
    plan.add_argument("--pr", type=int, required=True, help="Pull request number in that repository")
    plan.add_argument("--candidate", required=True, help="Full SHA the evidence must have been captured from (the PR head)")
    plan.add_argument("--base", help="Base SHA the before captures must come from")
    plan.add_argument("--verification-run", action="append", default=[], help="Verification run id(s) to cite in the block")
    plan.add_argument("--captured-by", help="Who captured the evidence, for the provenance line (default: the task worker)")
    plan.add_argument("--evidence-root", help="Evidence root directory (default: `evidence` from VERIFY.md or VERIFY_EVIDENCE_ROOT)")
    plan.add_argument("--publish-dir", help="Where plan.json, publish copies, and results go (default: <run>/publish)")
    plan.add_argument("--redact", action="append", default=[], metavar="REGEX", help="Extra secret pattern that must not appear in published text")
    plan.add_argument("--json", action="store_true")
    block = sub.add_parser("block", help="Render the marked block from a plan (and receipts) without touching GitHub")
    block.add_argument("--plan", required=True)
    block.add_argument("--receipts")
    block.add_argument("--final", action="store_true", help="Render video pairs as an HTML table (needs receipts for every video)")
    block.add_argument("--attach-list", action="store_true", help="Print the publish copies that still need uploading, one per line")
    pub = sub.add_parser("publish", help="Upload new media with `gh pr edit --attach`, replace only the marked block, verify, and record receipts")
    pub.add_argument("--plan", required=True)
    pub.add_argument("--receipts", help="Receipt store (default: <publish-dir>/receipts.json); keep it where it outlives the checkout")
    pub.add_argument("--visibility", required=True, choices=("public", "private", "internal"), help="The destination visibility you intend; a mismatch refuses")
    pub.add_argument("--gh", help="GitHub CLI binary (default: gh on PATH)")
    pub.add_argument("--timeout", type=int, default=300, help="Seconds allowed for one gh call (uploads included)")
    pub.add_argument("--allow-head-mismatch", action="store_true", help="Publish although the PR head moved past the captured candidate")
    pub.add_argument("--replace-foreign-block", action="store_true", help="Take over a marked block this publisher did not write")
    pub.add_argument("--dry-run", action="store_true", help="Compute the body and attachments; edit nothing")
    pub.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.command == "capabilities":
            info = gh_capabilities(Gh(args.gh))
            print(json.dumps(info, indent=2) if args.json else "\n".join(f"{k}: {v}" for k, v in info.items()))
            return 0 if info["attach"] else 2
        return {"plan": cmd_plan, "block": cmd_block, "publish": cmd_publish}[args.command](args)
    except Refused as exc:
        print(f"refused: {exc}", file=sys.stderr)
        return 1
    except GhTimeout as exc:
        print(f"uncertain: {exc}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
