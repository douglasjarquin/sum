#!/usr/bin/env python3
"""Install pinned tools and native binaries for setup. Not the sumctl CLI."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
TOOLS = ("python3", "node", "herdr", "gh", "quota-axi", "codegraph", "skills")
REMAINDER_REMOTE = "https://github.com/douglasjarquin/remainder/releases/download"


class InstallError(Exception):
    pass


def run(argv, *, cwd=None, timeout=20, check=True, env=None):
    try:
        result = subprocess.run([str(a) for a in argv], cwd=cwd, text=True, env=env,
                                capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        raise InstallError(f"{Path(str(argv[0])).name}: timed out after {timeout}s; its effect is unknown") from exc
    except OSError as exc:
        raise InstallError(f"{Path(str(argv[0])).name}: {exc}") from exc
    if check and result.returncode:
        detail = (result.stderr or result.stdout).strip()[-4000:]
        raise InstallError(f"{Path(str(argv[0])).name} exited {result.returncode}: {detail}")
    return result


def sha256_file(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def read_json(path):
    try:
        return json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise InstallError(f"Cannot read {path}: {exc}") from exc


def mise_env(target):
    return {**os.environ, "MISE_TRUSTED_CONFIG_PATHS": str(Path(target) / "mise.toml")}


def link_destination(link, target):
    try:
        return os.path.relpath(Path(target), start=Path(link).parent)
    except ValueError:
        return str(target)


def link_tool(link, target):
    link = Path(link)
    link.parent.mkdir(parents=True, exist_ok=True)
    dest = link_destination(link, target)
    if link.is_symlink():
        current = os.readlink(link)
        return {"link": str(link), "target": current, "created": False, "differs": current != dest}
    if link.exists():
        raise InstallError(f"Refusing to replace non-symlink {link}")
    link.symlink_to(dest)
    return {"link": str(link), "target": dest, "created": True, "differs": False}


def resolve_tools(target):
    target = Path(target)
    mise = shutil.which("mise")
    if not mise or not (target / "mise.toml").is_file():
        raise InstallError("Missing mise or mise.toml; install mise, then run mise run setup.")
    env = mise_env(target)
    run([mise, "install"], cwd=target, env=env, timeout=900)
    links = {}
    for name in TOOLS:
        resolved = Path(run([mise, "which", name], cwd=target, env=env, timeout=60).stdout.strip()).resolve()
        if not resolved.is_file():
            raise InstallError(f"mise resolved {name} to a missing file {resolved}")
        links[name] = link_tool(target / ".local" / "bin" / name, resolved)
    return links


def native_platform():
    system = {"darwin": "darwin", "linux": "linux"}.get(sys.platform, sys.platform)
    machine = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine().lower(), platform.machine().lower())
    return f"{system}-{machine}"


def build_native_artifact(target):
    target = Path(target)
    source = target / "go"
    if not (source / "go.mod").is_file():
        raise InstallError(f"Native Go source is missing from {source}")
    outputs = {"sumctl": "./cmd/sumctl", "herdr-mesh": "./cmd/herdr-mesh"}
    output_dir = target / ".local" / "bin"
    output_dir.mkdir(parents=True, exist_ok=True)
    pending = []
    for name, package in outputs.items():
        output = output_dir / name
        if output.exists() or output.is_symlink():
            if not output.is_file() or not os.access(output, os.X_OK):
                raise InstallError(f"Existing native artifact {output} is not executable")
        else:
            pending.append((name, package))
    if not pending:
        return output_dir / "sumctl"
    go = os.environ.get("SUM_GO_BIN")
    if not go:
        mise = shutil.which("mise")
        if mise and (target / "mise.toml").is_file():
            result = run([mise, "which", "go"], cwd=target, env=mise_env(target), timeout=60, check=False)
            if result.returncode == 0 and result.stdout.strip():
                go = result.stdout.strip()
    go = go or shutil.which("go")
    if not go:
        raise InstallError("Missing Go 1.25+; install the pinned build tool with mise before staging native artifacts.")
    platform_name = native_platform()
    goos, goarch = platform_name.split("-", 1)
    build_env = {**os.environ, "CGO_ENABLED": "0", "GOENV": "off", "GOOS": goos, "GOARCH": goarch}
    for variable in ("GOROOT", "GOTOOLDIR", "GOTOOLCHAIN"):
        build_env.pop(variable, None)
    for name, package in outputs.items():
        if (name, package) not in pending:
            continue
        output = output_dir / name
        temporary = output.with_name(f".{output.name}.{uuid.uuid4().hex}.tmp")
        try:
            run([go, "build", "-trimpath", "-buildvcs=false", "-o", temporary, package], cwd=source,
                env=build_env, timeout=900)
            if not temporary.is_file() or not os.access(temporary, os.X_OK):
                raise InstallError(f"Go build did not produce an executable at {temporary}")
            try:
                os.link(temporary, output)
            except FileExistsError:
                pass
        finally:
            if temporary.exists():
                temporary.unlink()
    return output_dir / "sumctl"


def remainder_pin(root, platform_name=None):
    platform_name = platform_name or native_platform()
    inventory = read_json(Path(root) / "docs" / "dependency-inventory.json")
    entry = next((item for item in inventory.get("dependencies", []) if isinstance(item, dict) and item.get("id") == "remainder"), None)
    if entry is None:
        raise InstallError("Dependency inventory lacks remainder")
    pins = entry.get("pins")
    if not isinstance(pins, dict):
        raise InstallError("Remainder inventory pins are missing")
    pin = pins.get(platform_name)
    if pin is None:
        return None
    if not all(pin.get(key) for key in ("version", "sha256", "asset")):
        raise InstallError(f"Remainder pin for {platform_name} is incomplete")
    return pin


def remainder_binary(root):
    matches = [path for path in Path(root).rglob("remainder") if path.is_file() and path.name == "remainder"]
    if len(matches) != 1:
        raise InstallError(f"Remainder archive did not contain exactly one remainder executable under {root}")
    return matches[0]


def fetch_url(url, destination, timeout=120):
    if os.environ.get("SUM_REMAINDER_NO_DOWNLOAD"):
        raise InstallError("Remainder download is disabled in this environment.")
    try:
        with urllib.request.urlopen(url, timeout=timeout) as response:
            data = response.read()
    except (OSError, urllib.error.URLError) as exc:
        raise InstallError(f"Cannot download {url}: {exc}") from exc
    Path(destination).write_bytes(data)


def install_remainder(target, *, fetch=None, pin=None, platform_name=None):
    target = Path(target)
    platform_name = platform_name or native_platform()
    inventory_root = target if (target / "docs" / "dependency-inventory.json").is_file() else ROOT
    pin = pin or remainder_pin(inventory_root, platform_name)
    if pin is None:
        return {"installed": False, "rewritten": False, "reason": f"No Remainder pin for {platform_name}"}
    dest = target / ".deps" / "remainder" / f"{pin['version']}-{platform_name}"
    link = target / ".local" / "bin" / "remainder"
    if dest.exists():
        binary = remainder_binary(dest)
        linked = link_tool(link, binary)
        return {**linked, "path": str(dest), "installed": True, "rewritten": False}
    dest.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".remainder-", dir=dest.parent))
    try:
        archive = staging / pin["asset"]
        (fetch or fetch_url)(f"{REMAINDER_REMOTE}/v{pin['version']}/{pin['asset']}", archive)
        digest = sha256_file(archive)
        if digest != pin["sha256"]:
            raise InstallError(f"Remainder checksum mismatch for {pin['asset']}: got {digest}, want {pin['sha256']}")
        extract = staging / "extract"
        extract.mkdir()
        with tarfile.open(archive, "r:gz") as bundle:
            bundle.extractall(extract, filter="data")
        binary = remainder_binary(extract)
        if not os.access(binary, os.X_OK):
            binary.chmod(binary.stat().st_mode | 0o111)
        relative = binary.relative_to(extract)
        try:
            os.rename(extract, dest)
        except OSError:
            if dest.exists():
                binary = remainder_binary(dest)
                linked = link_tool(link, binary)
                return {**linked, "path": str(dest), "installed": True, "rewritten": False}
            raise
        linked = dest / relative
        result = link_tool(link, linked)
        return {**result, "path": str(dest), "installed": True, "rewritten": False}
    finally:
        shutil.rmtree(staging, ignore_errors=True)


def write_herdr_skill(target):
    target = Path(target)
    skill = run([target / ".local" / "bin" / "herdr", "--skill"], timeout=30).stdout
    path = target / ".local" / "skills" / "herdr" / "SKILL.md"
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.is_file() or path.read_text() != skill + "\n":
        path.write_text(skill + "\n")
    for parent in (target / ".agents" / "skills", target / ".claude" / "skills"):
        link_tool(parent / "herdr", "../../.local/skills/herdr")
    return path
