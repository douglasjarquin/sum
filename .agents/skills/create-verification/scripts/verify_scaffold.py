#!/usr/bin/env python3
"""Inspect a repository and scaffold its project-local verification: VERIFY.md, the feature-map index and seed feature files, the vendored
`verify` and `maintain-verification` skills with thin harness aliases, and a `verify` mise task built from the checks the project already declares.

Nothing here invents commands, selectors, ports, or credentials: every generated line names something the inspection found, and everything it
could not find is written as an explicit `TODO(verify): ...` placeholder that `verify_audit.py` reports until a person resolves it. Generation is
idempotent and never overwrites: an existing file without placeholders is the user's and is kept; an existing draft (still carrying placeholders)
that the inspection would now generate differently is kept too, with the proposal written under the Git-ignored artifact directory and the
conflict listed. A vendored skill file that differs from the shipped copy is a conflict as well. An explicitly declared `feature_maps` location in an existing VERIFY.md is preserved.

Usage:
  verify_scaffold.py [--root DIR] [--inspect] [--write] [--surface web|cli|service]... [--json]
  --inspect (default) prints what was found and what would be generated; --write creates the missing files.
Exit codes: 0 ok, 1 conflicts or nothing verifiable found (the repository still needs a person), 2 not a repository, 3 usage.
Works in any clone with Git and Python 3.11+; no sum, Herdr, or path outside this skill directory and the target repository.
"""
from __future__ import annotations

import sys

if sys.version_info < (3, 11):
    sys.stderr.write("verify_scaffold.py needs Python 3.11 or newer (tomllib).\n")
    sys.exit(2)

import argparse
import datetime as _dt
import json
import os
import re
import shutil
import subprocess
import tomllib
from pathlib import Path

SKILL_DIR = Path(__file__).resolve().parents[1]
SKILLS_ROOT = SKILL_DIR.parent  # .agents/skills of the checkout that ships this skill; siblings `verify` and `maintain-verification` are vendored.
VENDORED = ("verify", "maintain-verification")
HARNESS_ALIAS_DIR = ".claude/skills"
DEFAULT_MAPS = "docs/features/README.md"
ARTIFACTS = ".artifacts/verification"
FENCE = re.compile(r"^```verify[ \t]*\n(.*?)^```[ \t]*$", re.S | re.M)
TODO = "TODO(verify)"
CHECK_TASK_NAMES = ("test", "tests", "check", "lint", "typecheck", "build", "unit", "e2e", "spec")
SERVE_TASK_NAMES = ("serve", "dev", "start", "run", "server", "preview")
# Capabilities are real installed commands; a surface whose driver is absent stays `manual`, never a guessed recipe.
CAPABILITIES = {
    "python3": "run Python checks and the portable runner",
    "node": "run Node checks",
    "curl": "drive HTTP endpoints from a shell",
    "docker": "start compose services declared by the repository",
    "chrome-devtools-axi": "drive a real browser for web surfaces",
    "playwright": "drive a real browser for web surfaces",
    "tmux": "drive an interactive CLI/TUI in a scripted pane",
}
BROWSER_DRIVERS = ("chrome-devtools-axi", "playwright")


def utc_now():
    return _dt.datetime.now(_dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def resolve_root(start: Path):
    result = subprocess.run(["git", "-C", str(start), "rev-parse", "--show-toplevel"], text=True, capture_output=True)
    if result.returncode:
        raise SystemExit(f"{start} is not inside a Git work tree: {(result.stderr or '').strip()[:200]}")
    return Path(result.stdout.strip()).resolve()


def read(path: Path):
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return ""


def mise_tasks(root: Path):
    """Tasks mise resolves from the root, split into this repository's own and inherited ones. Never runs a task."""
    binary = shutil.which("mise")
    if binary is None:
        return {"available": False, "own": [], "inherited": []}
    result = subprocess.run([binary, "tasks", "ls", "--json"], cwd=str(root), text=True, capture_output=True, env={**os.environ, "MISE_QUIET": "1"})
    try:
        rows = json.loads(result.stdout or "[]")
    except ValueError:
        rows = []
    own, inherited = [], []
    for row in rows:
        if not isinstance(row, dict) or not row.get("name"):
            continue
        source = Path(row.get("source") or row.get("file") or "").resolve() if (row.get("source") or row.get("file")) else None
        entry = {"name": row["name"], "source": str(source) if source else None, "description": row.get("description") or ""}
        (own if source and (source == root or root in source.parents) else inherited).append(entry)
    return {"available": True, "own": own, "inherited": inherited}


def package_scripts(root: Path):
    data = {}
    try:
        data = json.loads(read(root / "package.json") or "{}")
    except ValueError:
        pass
    return {k: v for k, v in (data.get("scripts") or {}).items() if isinstance(v, str)}


def make_targets(root: Path):
    targets = []
    for name in ("Makefile", "makefile", "justfile", "Justfile"):
        text = read(root / name)
        for match in re.finditer(r"^([A-Za-z0-9_.-]+)\s*:(?!=)", text, re.M):
            targets.append({"runner": "just" if "just" in name.lower() else "make", "name": match.group(1)})
    return targets


def declared_containers(root: Path):
    found = []
    for name in ("compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml", "Dockerfile", ".devcontainer/devcontainer.json"):
        if (root / name).is_file():
            found.append(name)
    return found


def readiness_hints(root: Path, text_blobs):
    """Health/readiness endpoints and ports the repository already mentions; nothing is invented from them."""
    hints = {"endpoints": [], "ports": [], "env": []}
    blob = "\n".join(text_blobs)
    hints["endpoints"] = sorted(set(re.findall(r"(?<![\w/])(/(?:health|healthz|ready|readyz|status|ping|live|version))\b", blob)))
    hints["ports"] = sorted({int(p) for p in re.findall(r"(?:127\.0\.0\.1|localhost|0\.0\.0\.0):(\d{2,5})", blob) if 1 <= int(p) <= 65535})
    hints["env"] = sorted(set(re.findall(r"\b([A-Z][A-Z0-9_]{3,}_(?:DIR|PORT|URL|HOME|PATH))\b", blob)))
    return hints


def service_commands(root: Path):
    """Run strings of the tasks and scripts that start a server; the files they launch are services, not CLI entrypoints."""
    words = set()
    for name in ("mise.toml", ".mise.toml"):
        try:
            tasks = tomllib.loads(read(root / name) or "").get("tasks") or {}
        except tomllib.TOMLDecodeError:
            tasks = {}
        for task, spec in tasks.items():
            if task in SERVE_TASK_NAMES:
                run = spec if isinstance(spec, str) else (spec.get("run") if isinstance(spec, dict) else None)
                for item in (run if isinstance(run, list) else [run]):
                    words.update(str(item or "").split())
    for script, command in package_scripts(root).items():
        if script in SERVE_TASK_NAMES:
            words.update(command.split())
    return words


def cli_entrypoints(root: Path):
    """Scripts a user runs directly: executable files at the root/bin with a shebang, and console_scripts / package.json bin entries."""
    entries = []
    served = service_commands(root)
    for candidate in [*root.glob("*.py"), *root.glob("bin/*"), *root.glob("cli/*")]:
        relative = str(candidate.relative_to(root))
        if candidate.is_file() and not candidate.name.startswith((".", "_")) and read(candidate).startswith("#!") and relative not in served and candidate.name not in served:
            entries.append(relative)
    try:
        data = json.loads(read(root / "package.json") or "{}")
        bins = data.get("bin")
        if isinstance(bins, str):
            entries.append(bins)
        elif isinstance(bins, dict):
            entries.extend(str(v) for v in bins.values())
    except ValueError:
        pass
    pyproject = read(root / "pyproject.toml")
    if pyproject:
        try:
            scripts = tomllib.loads(pyproject).get("project", {}).get("scripts", {})
            entries.extend(f"{k} (console script)" for k in scripts)
        except tomllib.TOMLDecodeError:
            pass
    return sorted(set(entries))


def test_locations(root: Path):
    found = []
    for pattern in ("tests", "test", "spec", "__tests__", "e2e"):
        path = root / pattern
        if path.is_dir():
            files = [p for p in path.rglob("*") if p.is_file() and re.search(r"(test|spec)", p.name) and p.suffix in (".py", ".js", ".mjs", ".ts", ".tsx", ".go", ".rs", ".rb")]
            if files:
                found.append({"dir": pattern, "files": len(files), "examples": sorted(str(p.relative_to(root)) for p in files)[:5]})
    return found


def http_routes(root: Path):
    """Route declarations the source already contains (Flask/FastAPI/Express/http.server style); only literal paths are kept."""
    routes = set()
    patterns = (r"""@\w+\.(?:route|get|post|put|delete|patch)\(\s*["']([^"']+)["']""",
                r"""\b(?:app|router)\.(?:get|post|put|delete|patch|use)\(\s*["']([^"']+)["']""",
                r"""self\.path\s*(?:==|in|\.startswith\()\s*\(?\s*["']([^"']+)["']""",
                r"""(?:path|url)\s*==\s*["'](/[^"']*)["']""")
    for path in root.rglob("*"):
        if not path.is_file() or path.suffix not in (".py", ".js", ".mjs", ".ts", ".go", ".rb") or any(part in ("node_modules", ".git", "dist", "build", ".venv", "tests", "test") for part in path.parts):
            continue
        text = read(path)
        for pattern in patterns:
            routes.update(m for m in re.findall(pattern, text) if m.startswith("/"))
    return sorted(routes)


def existing_contract(root: Path):
    path = root / "VERIFY.md"
    if not path.is_file():
        return None
    match = FENCE.search(read(path))
    config = {}
    if match:
        try:
            config = tomllib.loads(match.group(1))
        except tomllib.TOMLDecodeError:
            config = {}
    return {"path": "VERIFY.md", "feature_maps": config.get("feature_maps"), "artifacts": config.get("artifacts"), "parsed": bool(match)}


def guidance_files(root: Path):
    return [n for n in ("README.md", "AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", "VERIFY.md", "docs/README.md") if (root / n).is_file()]


def inspect(root: Path, surfaces_requested):
    tasks = mise_tasks(root)
    scripts = package_scripts(root)
    targets = make_targets(root)
    guidance = guidance_files(root)
    blobs = [read(root / g) for g in guidance] + [read(root / n) for n in ("mise.toml", ".mise.toml", "package.json", "Procfile") if (root / n).is_file()]
    blobs += [read(p) for p in list(root.glob("*.py"))[:20]]
    entrypoints = cli_entrypoints(root)
    routes = http_routes(root)
    checks = [t for t in tasks["own"] if t["name"] in CHECK_TASK_NAMES]
    serves = [t for t in tasks["own"] if t["name"] in SERVE_TASK_NAMES]
    capabilities = {name: shutil.which(name) is not None for name in CAPABILITIES}
    web_assets = any((root / n).exists() for n in ("src/index.html", "src/index.html.in", "public/index.html", "index.html", "dist/index.html"))
    surfaces = []
    if routes or serves or readiness_hints(root, blobs)["endpoints"]:
        surfaces.append("service")
    if web_assets:
        surfaces.append("web")
    if entrypoints:
        surfaces.append("cli")
    for requested in surfaces_requested or []:
        if requested not in surfaces:
            surfaces.append(requested)
    return {
        "root": str(root), "guidance": guidance, "mise": tasks, "package_scripts": scripts, "make_targets": targets,
        "containers": declared_containers(root), "readiness": readiness_hints(root, blobs), "cli_entrypoints": entrypoints, "routes": routes,
        "tests": test_locations(root), "check_tasks": [t["name"] for t in checks], "serve_tasks": [t["name"] for t in serves],
        "capabilities": capabilities, "surfaces": surfaces, "existing_contract": existing_contract(root),
        "has_verify_task": any(t["name"] == "verify" for t in tasks["own"]),
        "mentions_dist": "dist" in "\n".join(blobs),
        "inherited_verify": any(t["name"] == "verify" for t in tasks["inherited"]),
    }


def verify_task_command(info):
    """The aggregate `verify` task reuses checks the repository declares. With nothing to reuse there is no task: a command that merely succeeds is refused."""
    commands = []
    for name in CHECK_TASK_NAMES:  # Stable order: test first, then check, lint, typecheck, ...; build is a prerequisite, not a check.
        if name in info["check_tasks"] and name != "build":
            commands.append(f"mise run {name}")
    if not commands:
        for name in ("test", "lint", "check", "typecheck"):
            if name in info["package_scripts"]:
                commands.append(f"npm run {name}")
        for target in info["make_targets"]:
            if target["name"] in ("test", "check", "lint"):
                commands.append(f"{target['runner']} {target['name']}")
    if not commands:
        for location in info["tests"]:
            if any(e.endswith(".py") for e in location["examples"]):
                commands.append(f"python3 -m unittest discover -s {location['dir']} -p 'test_*.py'")
                break
    return commands


def fence(feature_maps, requires, freshness):
    lines = ['entrypoint = "mise run verify"', f'feature_maps = "{feature_maps}"', f'artifacts = "{ARTIFACTS}"', "", "[requires]",
             "commands = [" + ", ".join(f'"{c}"' for c in requires) + "]"]
    if freshness:
        lines += ["", "[freshness]", f'inputs = ["{freshness[0]}"]', f'outputs = ["{freshness[1]}"]']
    return "\n".join(lines)


def render_verify_md(info, feature_maps, commands, freshness):
    name = Path(info["root"]).name
    root = Path(info["root"])
    policy_names = ", ".join(f"`{n}`" for n in ("mise.toml", ".mise.toml", "mise-tasks/") if (root / n).exists()) or "`mise.toml`"
    requires = ["git", "mise"]
    if any("python" in c for c in commands) or any(e.endswith(".py") for e in info["cli_entrypoints"]):
        requires.append("python3")
    if any(c.startswith("npm") or c.startswith("node") for c in commands):
        requires.append("node")
    rows = "\n".join(f"| `{c}` | `{c}` | {TODO}: state what this check proves | " for c in commands) or f"| {TODO}: no existing check was found | - | - |"
    setup = "\n".join(f"- `{g}` describes setup; follow it." for g in info["guidance"] if g != "VERIFY.md") or f"{TODO}: how a fresh clone is prepared (no setup guidance file was found)."
    readiness = []
    for task in info["serve_tasks"]:
        readiness.append(f"- `mise run {task}` starts the service; it is not part of `mise run verify`.")
    for endpoint in info["readiness"]["endpoints"]:
        readiness.append(f"- `{endpoint}` is a readiness endpoint the repository mentions.")
    readiness.append("- `mise tasks ls` lists `verify` with a source inside this repository.")
    readiness.append("- `python3 .agents/skills/verify/scripts/verify_run.py --check` validates this contract without running anything.")
    containers = "\n".join(f"- `{c}` is declared; start it only through its own task, task-scoped." for c in info["containers"])
    return f"""# Verification contract

This file is the repository-local verification convention for `{name}`.
It works in an ordinary clone with Git, mise, and the commands listed below; no sum installation, Herdr session, or absolute path outside this checkout is required.
The portable procedure and the runner that records evidence live in `.agents/skills/verify/`; `.agents/skills/maintain-verification/` keeps this contract and the feature maps honest.
A harness without skill discovery follows this file directly.

```verify
{fence(feature_maps, requires, freshness)}
```

## Setup

{setup}

## Readiness

{chr(10).join(readiness)}
{containers}

## Automated checks

`mise run verify` is the canonical aggregate entrypoint. It runs the checks this repository already declares and stops at the first failure:

| Check | Command | Proves |
| --- | --- | --- |
{rows}

## Scenarios

The feature maps linked from `{feature_maps}` describe the user-facing features and which scenarios `mise run verify` exercises.
Rows marked `automated` are covered by the aggregate; rows marked `manual` need a person or an interactive driver and are recorded `not-run` unless reported with `--scenario`.
A green `mise run verify` is evidence for the automated rows only, never for a scenario that was not exercised.

## Isolation

{TODO}: name the temporary directories, ephemeral ports, or lab data this repository's checks use, and what they must never touch.
Never point a check at production data, a personal browser profile, or an application instance you did not start.

## Artifacts

Each run writes `{ARTIFACTS}/<run-id>/run.json` and `verify.log`; `{ARTIFACTS}/latest.json` mirrors the newest record.
Evidence captured with `python3 .agents/skills/verify/scripts/verify_capture.py` lands under the same run directory and survives cleanup.
The directory is Git-ignored; reference records by path in reports.

## Teardown

{TODO}: how anything a check started is stopped. Stop only what the run started; never kill by process name.

## Policy

Edits to this file, {policy_names}, the feature maps, or `.agents/skills/verify/` are policy changes.
Run the runner with `--base <merge-base>` so such a candidate is flagged `requires_root_review`; it cannot certify its own new standard.
"""


def feature_id(text):
    slug = re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")
    return slug[:40] or "feature"


def seed_features(info):
    """Top real user-facing features the inspection found, at most five; every other surface stays listed as unmapped inventory."""
    features = []
    for route in info["routes"]:
        if len(features) >= 5:
            break
        fid = "service." + (feature_id(route) or "root")
        features.append({"id": fid, "title": f"HTTP `{route}`", "surface": "service", "entry": route})
    for entry in info["cli_entrypoints"]:
        if len(features) >= 5:
            break
        features.append({"id": "cli." + feature_id(Path(entry.split(" ")[0]).stem), "title": f"Command `{entry}`", "surface": "cli", "entry": entry})
    if "web" in info["surfaces"] and len(features) < 5:
        features.append({"id": "web.home", "title": "Web page", "surface": "web", "entry": "the served index page"})
    return features


def driver_text(info, feature):
    """A driver is named only when its capability is installed; otherwise the row is explicitly manual."""
    caps = info["capabilities"]
    if feature["surface"] == "service":
        return ("automated: " + TODO + ": name the test that exercises it") if info["tests"] else ("manual: " + ("`curl` against a service you started" if caps["curl"] else "no HTTP driver installed"))
    if feature["surface"] == "cli":
        return ("automated: " + TODO + ": name the test that exercises it") if info["tests"] else "manual: run the command in a terminal"
    browser = next((b for b in BROWSER_DRIVERS if caps.get(b)), None)
    return f"manual: browser via `{browser}`" if browser else "manual: no browser driver installed; open the page by hand"


def render_feature(info, feature):
    drive = {
        "service": f"Start the service with its own task ({', '.join(f'`mise run {t}`' for t in info['serve_tasks']) or TODO + ': no serve task found'}), then request `{feature['entry']}` on the URL it prints. Capture with `python3 .agents/skills/verify/scripts/verify_capture.py --feature {feature['id']} http GET <url>{feature['entry']}`.",
        "cli": f"Run `{feature['entry']}` with a real argument. Capture with `python3 .agents/skills/verify/scripts/verify_capture.py --feature {feature['id']} run -- {feature['entry'].split(' ')[0]} <args>`.",
        "web": f"Serve the built page and open it. {TODO}: name the exact URL and what the page must show.",
    }[feature["surface"]]
    return f"""# {feature['title']}

{TODO}: one paragraph on what this feature does for the user and why it matters. Seeded from `{feature['entry']}` found in the repository.

## Entry points

- `{feature['entry']}`
- {TODO}: every other way a user reaches this feature (UI control, keyboard, CLI flag, API route), or state that this is the only one.

## Scenarios

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `{feature['id']}` | {TODO}: the success path a user sees | {driver_text(info, feature)} | run record |

Add one row per variant and per applicable case: error, cancel, empty, persistence. Mark a case not applicable in prose rather than silently omitting it.

## Driving it

{drive}

## Expected states and side effects

- {TODO}: what is observable when it works (output, status code, rendered text, file written, row inserted).
- {TODO}: what the error path shows.

## Gotchas and manual gaps

- {TODO}: traps that waste or invalidate a run, and anything only a person can check.
"""


def render_index(features, info, existing_index_text=None):
    lines = ["# Feature maps", "",
             "Each file maps one user-facing feature: its entry points, scenarios with stable IDs, how to drive it, the observable states that prove it, and the manual gaps.",
             "The runner in `.agents/skills/verify/` reads the scenario tables from the files linked here; a scenario ID must be unique across maps.",
             "A row's Driver column starts with `automated` (covered by `mise run verify`) or `manual`; anything after that names the test or the procedure.", "",
             f"Inventory: **incomplete**. {len(features)} feature(s) seeded from the repository inspection on {utc_now()[:10]}; the rest of the product is not mapped yet.",
             "Do not read this index as full coverage until the incomplete label is removed by a person who inventoried the product.", ""]
    for f in features:
        lines.append(f"- [{f['title']}]({f['id']}.md)")
    unmapped = [r for r in info["routes"] if not any(x["entry"] == r for x in features)] + [e for e in info["cli_entrypoints"] if not any(x["entry"] == e for x in features)]
    if unmapped:
        lines += ["", "Found but not mapped yet:", *[f"- `{u}`" for u in unmapped]]
    return "\n".join(lines) + "\n"


def mise_toml_with_verify(root: Path, commands):
    path = root / "mise.toml"
    text = read(path)
    run = json.dumps(commands[0]) if len(commands) == 1 else "[\n" + ",\n".join("  " + json.dumps(c) for c in commands) + "\n]"
    block = f'\n[tasks.verify]\ndescription = "Canonical aggregate verification (see VERIFY.md)"\nrun = {run}\n'
    if not text:
        return block.lstrip("\n")
    if "[tasks.verify]" in text or re.search(r"^verify\s*=", text, re.M):
        return None
    return text.rstrip("\n") + "\n" + block


def plan(root: Path, info):
    """Every file the scaffold would produce, with its content. Existing files are compared, never rewritten."""
    contract = info["existing_contract"]
    feature_maps = (contract or {}).get("feature_maps") or DEFAULT_MAPS
    if Path(feature_maps).is_absolute() or ".." in Path(feature_maps).parts:
        feature_maps = DEFAULT_MAPS
    maps_dir = Path(feature_maps).parent
    commands = verify_task_command(info)
    freshness = None
    if "web" in info["surfaces"] and (root / "src").is_dir() and any(t["name"] == "build" for t in info["mise"]["own"]):
        freshness = ("src", "dist") if (root / "dist").exists() or info["mentions_dist"] else None
    files = {}
    if commands and not info["has_verify_task"]:
        new_toml = mise_toml_with_verify(root, commands)
        if new_toml is not None:
            files["mise.toml"] = {"content": new_toml, "kind": "append" if (root / "mise.toml").is_file() else "create"}
    files["VERIFY.md"] = {"content": render_verify_md(info, feature_maps, commands, freshness), "kind": "create"}
    features = seed_features(info)
    files[feature_maps] = {"content": render_index(features, info), "kind": "create"}
    for feature in features:
        files[str(maps_dir / f"{feature['id']}.md")] = {"content": render_feature(info, feature), "kind": "create"}
    gitignore = read(root / ".gitignore")
    if ".artifacts/" not in gitignore and ".artifacts" not in gitignore.split():
        files[".gitignore"] = {"content": (gitignore.rstrip("\n") + "\n" if gitignore else "") + "\n# Verification run records written by .agents/skills/verify (see VERIFY.md).\n.artifacts/\n", "kind": "append" if gitignore else "create"}
    return {"feature_maps": feature_maps, "commands": commands, "features": features, "files": files, "freshness": freshness}


def apply(root: Path, planned, write: bool):
    """Create what is missing; keep what exists. Differing existing files are conflicts whose proposal is written under the artifact directory."""
    results = []
    proposals = root / ARTIFACTS / "scaffold" / f"{_dt.datetime.now(_dt.timezone.utc):%Y%m%dT%H%M%SZ}"
    for relative, spec in planned["files"].items():
        target = root / relative
        if spec["kind"] == "append" and target.is_file():
            current = read(target)
            if current == spec["content"]:
                results.append({"path": relative, "status": "unchanged"})
                continue
            if relative == "mise.toml" and ("[tasks.verify]" in current or re.search(r"^verify\s*=", current, re.M)):
                results.append({"path": relative, "status": "unchanged"})
                continue
            if relative == ".gitignore" and ".artifacts/" in current:
                results.append({"path": relative, "status": "unchanged"})
                continue
            if write:
                target.write_text(spec["content"], encoding="utf-8")
            results.append({"path": relative, "status": "appended" if write else "would-append"})
            continue
        if target.exists():
            current = read(target)
            if current == spec["content"]:
                results.append({"path": relative, "status": "unchanged"})
            elif TODO not in current:
                results.append({"path": relative, "status": "kept", "note": "exists without placeholders: yours, never regenerated"})
            else:
                if write:
                    proposal = proposals / relative
                    proposal.parent.mkdir(parents=True, exist_ok=True)
                    proposal.write_text(spec["content"], encoding="utf-8")
                results.append({"path": relative, "status": "conflict", "proposal": str((proposals / relative).relative_to(root)) if write else None,
                                "note": "still a draft (has placeholders) but the inspection now generates it differently; yours is kept, the proposal is written beside the run records"})
            continue
        if write:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(spec["content"], encoding="utf-8")
        results.append({"path": relative, "status": "created" if write else "would-create"})
    # Vendored skills: copied byte for byte from this skill's siblings; a differing existing copy is kept and reported.
    for name in VENDORED:
        source = SKILLS_ROOT / name
        if not source.is_dir():
            results.append({"path": f".agents/skills/{name}", "status": "unavailable", "note": f"{source} is not beside this skill; vendor it by hand"})
            continue
        dest = root / ".agents/skills" / name
        if source.resolve() == dest.resolve():
            results.append({"path": f".agents/skills/{name}", "status": "unchanged"})
            continue
        differs, missing = [], []
        for src_file in sorted(p for p in source.rglob("*") if p.is_file() and "__pycache__" not in p.parts):
            dst_file = dest / src_file.relative_to(source)
            if not dst_file.exists():
                missing.append(src_file)
                if write:
                    dst_file.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(src_file, dst_file)
            elif dst_file.read_bytes() != src_file.read_bytes():
                differs.append(str(dst_file.relative_to(root)))
        status = "conflict" if differs else ("unchanged" if not missing else ("vendored" if write else "would-vendor"))
        results.append({"path": f".agents/skills/{name}", "status": status, **({"differs": differs} if differs else {})})
        alias = root / HARNESS_ALIAS_DIR / name
        if alias.is_symlink() or alias.exists():
            results.append({"path": f"{HARNESS_ALIAS_DIR}/{name}", "status": "unchanged"})
        elif write:
            alias.parent.mkdir(parents=True, exist_ok=True)
            alias.symlink_to(Path("../..") / ".agents/skills" / name)
            results.append({"path": f"{HARNESS_ALIAS_DIR}/{name}", "status": "linked"})
        else:
            results.append({"path": f"{HARNESS_ALIAS_DIR}/{name}", "status": "would-link"})
    return results


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=".", help="Any directory inside the target repository")
    parser.add_argument("--inspect", action="store_true", help="Report findings and the plan without writing (default)")
    parser.add_argument("--write", action="store_true", help="Create missing files; never overwrite existing ones")
    parser.add_argument("--surface", action="append", choices=("web", "cli", "service"), help="Declare a surface the inspection cannot see")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)
    root = resolve_root(Path(args.root).resolve())
    info = inspect(root, args.surface)
    planned = plan(root, info)
    results = apply(root, planned, write=args.write)
    problems = []
    if not planned["commands"] and not info["has_verify_task"]:
        problems.append("no existing check was found (no test/lint/check task, package script, make target, or test directory); VERIFY.md names no runnable entrypoint until you declare one. "
                        "A `verify` task that merely succeeds is refused.")
    if info["inherited_verify"] and not info["has_verify_task"]:
        problems.append("a `verify` task is inherited from a parent directory; it belongs to another project and the runner will block on it until this repository defines its own.")
    if not info["mise"]["available"]:
        problems.append("mise is not on PATH; task ownership could not be checked.")
    conflicts = [r for r in results if r["status"] == "conflict"]
    record = {"schema": 1, "at": utc_now(), "mode": "write" if args.write else "inspect", "inspection": info, "plan": {k: v for k, v in planned.items() if k != "files"},
              "planned_files": sorted(planned["files"]), "results": results, "conflicts": conflicts, "problems": problems,
              "next": ["python3 .agents/skills/verify/scripts/verify_run.py --check", "resolve every TODO(verify) placeholder from what you observed, not from guesses",
                       "python3 .agents/skills/maintain-verification/scripts/verify_audit.py", "python3 .agents/skills/verify/scripts/verify_run.py --base <merge-base>",
                       "drive one mapped feature and capture evidence with verify_capture.py, then run the teardown and confirm the evidence is still there"]}
    if args.json:
        print(json.dumps(record, indent=2))
    else:
        print(f"scaffold: {record['mode']} at {root}")
        print(f"surfaces: {', '.join(info['surfaces']) or 'none detected'}; own mise tasks: {', '.join(t['name'] for t in info['mise']['own']) or 'none'}; "
              f"checks reused: {', '.join(planned['commands']) or 'none'}")
        print("capabilities: " + ", ".join(f"{k}={'yes' if v else 'no'}" for k, v in info["capabilities"].items()))
        for r in results:
            print(f"  {r['status']:>14}  {r['path']}" + (f"  ({r.get('note') or ', '.join(r.get('differs', []))})" if r.get("note") or r.get("differs") else ""))
        for c in conflicts:
            print(f"conflict: {c['path']} differs from what would be generated; kept yours" + (f", proposal at {c['proposal']}" if c.get("proposal") else ""))
        for p in problems:
            print(f"problem: {p}")
        print("next: " + " ; ".join(record["next"]))
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
