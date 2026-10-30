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
TOOLS = ("python3", "node", "gh", "quota-axi", "codegraph", "skills", "basedpyright-langserver", "gopls")
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
        resolved = which_tool(mise, env, target, name)
        if resolved is None:
            raise InstallError(f"mise resolved {name} to a missing file")
        links[name] = link_tool(target / ".local" / "bin" / name, resolved)
    return links


def which_tool(mise, env, target, name):
    result = run([mise, "which", name], cwd=target, env=env, timeout=60, check=False)
    path = result.stdout.strip()
    if result.returncode == 0 and path:
        resolved = Path(path).resolve()
        if resolved.is_file():
            return resolved
        sibling = resolved.parent / name
        if sibling.is_file():
            return sibling
    if name.endswith("-langserver"):
        primary = name[: -len("-langserver")]
        result = run([mise, "which", primary], cwd=target, env=env, timeout=60, check=False)
        path = result.stdout.strip()
        if result.returncode == 0 and path:
            candidate = Path(path).resolve().parent / name
            if candidate.is_file():
                return candidate
    return None


def native_platform():
    system = {"darwin": "darwin", "linux": "linux"}.get(sys.platform, sys.platform)
    machine = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine().lower(), platform.machine().lower())
    return f"{system}-{machine}"


def build_native_artifact(target):
    target = Path(target)
    source = target / "go"
    if not (source / "go.mod").is_file():
        raise InstallError(f"Native Go source is missing from {source}")
    outputs = {"sumctl": "./cmd/sumctl"}
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


HC_VERSION = "0.1.14"
HC_MANIFEST_URL = f"https://releases.herdr.dev/core/{HC_VERSION}/manifest.json"
HC_TARGETS = {"darwin-arm64": "macos-aarch64", "darwin-amd64": "macos-x86_64",
              "linux-arm64": "linux-aarch64", "linux-amd64": "linux-x86_64"}


def fetch_hc(url, destination):
    if os.environ.get("SUM_HC_NO_DOWNLOAD"):
        raise InstallError("HC download is disabled in this environment.")
    try:
        request = urllib.request.Request(url, headers={"User-Agent": "sum-runtime-installer/0.1"})
        with urllib.request.urlopen(request, timeout=120) as response:
            data = response.read(128 * 1024 * 1024 + 1)
        if len(data) > 128 * 1024 * 1024:
            raise InstallError("HC release download exceeds the artifact size bound")
        Path(destination).write_bytes(data)
    except (OSError, urllib.error.URLError) as exc:
        raise InstallError(f"Cannot download {url}: {exc}") from exc


def hc_binary(root):
    matches = [p for p in Path(root).rglob("*") if p.is_file() and p.name in ("hc", "herdr-core")]
    if len(matches) != 1:
        raise InstallError("HC archive must contain exactly one hc or herdr-core executable")
    return matches[0]


def install_hc(target, *, fetch=None, manifest=None, platform_name=None):
    """Stage an exact published HC without starting a core or changing its config."""
    target = Path(target)
    platform_name = platform_name or native_platform()
    key = HC_TARGETS.get(platform_name)
    if key is None:
        raise InstallError(f"HC is not qualified on Sum host {platform_name}")
    locked = manifest or read_json(target / "config/hc-release.json")
    if locked.get("version") != HC_VERSION or locked.get("generation") != 1:
        raise InstallError("HC manifest does not match the selected version and protocol generation")
    pin = locked.get("targets", {}).get(key)
    expected_url = f"https://releases.herdr.dev/core/{HC_VERSION}/herdr-core-{key}.tar.gz"
    if not isinstance(pin, dict) or pin.get("url") != expected_url or len(pin.get("sha256", "")) != 64:
        raise InstallError(f"HC manifest has no complete exact artifact pin for {key}")
    dest = target / ".deps/hc" / f"{HC_VERSION}-{platform_name}"
    link = target / ".local/bin/hc"
    # Never replace a live tool or conceal an existing customization.
    if link.exists() and not link.is_symlink():
        raise InstallError(f"Refusing to replace non-symlink {link}")
    if not dest.exists():
        dest.parent.mkdir(parents=True, exist_ok=True)
        staging = Path(tempfile.mkdtemp(prefix=".hc-", dir=dest.parent))
        try:
            downloaded_manifest = staging / "manifest.json"
            (fetch or fetch_hc)(HC_MANIFEST_URL, downloaded_manifest)
            if read_json(downloaded_manifest) != locked:
                raise InstallError("Published versioned HC manifest differs from the checked release manifest")
            archive = staging / "hc.tar.gz"
            (fetch or fetch_hc)(pin["url"], archive)
            digest = sha256_file(archive)
            if digest != pin["sha256"]:
                raise InstallError(f"HC checksum mismatch: got {digest}, want {pin['sha256']}")
            extract = staging / "extract"
            extract.mkdir()
            with tarfile.open(archive, "r:gz") as bundle:
                bundle.extractall(extract, filter="data")
            binary = hc_binary(extract)
            if binary.is_symlink() or not os.access(binary, os.X_OK):
                raise InstallError("HC archive executable is not an executable regular file")
            found = run([binary, "--version"]).stdout.strip()
            if found != f"hc {HC_VERSION}":
                raise InstallError(f"HC artifact version mismatch: {found!r}")
            docs = extract / "contract"
            docs.mkdir()
            (docs / "manifest.json").write_text(json.dumps(locked, indent=2) + "\n")
            schema = run([binary, "schema"], timeout=30).stdout
            schema_value = json.loads(schema)
            if not isinstance(schema_value, dict) or not isinstance(schema_value.get("methods"), list):
                raise InstallError("HC binary did not emit its OpenRPC schema")
            (docs / "openrpc.json").write_text(schema)
            (docs / "manual.md").write_text(run([binary, "--skill"], timeout=30).stdout)
            provenance = {"version": HC_VERSION, "generation": 1, "source_commit": "29e6d375623de40cb4820e0894aa131ba057b796",
                          "platform": platform_name, "archive_sha256": digest,
                          "binary": str(binary.relative_to(extract)), "binary_sha256": sha256_file(binary),
                          "schema_sha256": sha256_file(docs / "openrpc.json"), "manual_sha256": sha256_file(docs / "manual.md")}
            (docs / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
            os.rename(extract, dest)
        finally:
            shutil.rmtree(staging, ignore_errors=True)
    provenance = read_json(dest / "contract/provenance.json")
    binary = dest / provenance.get("binary", "")
    if provenance.get("archive_sha256") != pin["sha256"] or not binary.is_file() or sha256_file(binary) != provenance.get("binary_sha256"):
        raise InstallError("Existing HC staging does not match its immutable artifact receipt")
    for name, key_name in (("openrpc.json", "schema_sha256"), ("manual.md", "manual_sha256")):
        if sha256_file(dest / "contract" / name) != provenance.get(key_name):
            raise InstallError(f"Existing HC {name} differs from its matched artifact receipt")
    result = link_tool(link, binary)
    metadata = link_tool(target / ".local/share/hc", dest / "contract")
    if metadata["differs"] != result["differs"]:
        raise InstallError("HC executable and matched contract links point to different releases")
    return {**result, "installed": True, "path": str(dest), "provenance": provenance}


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--hc-only", action="store_true", required=True)
    parser.add_argument("--target", type=Path, required=True)
    args = parser.parse_args()
    try:
        print(json.dumps(install_hc(args.target)))
    except (InstallError, OSError, ValueError, tarfile.TarError) as exc:
        print(f"runtime install failed: {exc}", file=sys.stderr)
        sys.exit(1)
