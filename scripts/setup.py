#!/usr/bin/env python3
"""Install a pinned Mesh checkout and create only repository-local integration files."""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib

ROOT = Path(__file__).resolve().parents[1]
MESH_REV = "54adef519aa6af4dcd0bbd72586d414abab90046"
MESH_REMOTE = "https://github.com/runchr-works/herdr-mesh.git"
CODEX_PACKAGE = "@openai/codex@0.153.4"


def execute(args, cwd=None, capture=False):
    print("+ " + " ".join(str(a) for a in args), file=sys.stderr)
    result = subprocess.run([str(a) for a in args], cwd=cwd, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else None


def symlink(target, link):
    link.parent.mkdir(parents=True, exist_ok=True)
    if link.is_symlink():
        link.unlink()
    elif link.exists():
        raise RuntimeError(f"Refusing to replace non-symlink {link}")
    link.symlink_to(target)


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".sum-tmp")
    with tmp.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2)
        stream.write("\n")
    os.replace(tmp, path)


def merge_mcp(path, root_key, entry):
    data = json.loads(path.read_text()) if path.exists() else {}
    tools = data.setdefault(root_key, {})
    prior = tools.get("sum-herdr")
    if prior is not None and prior != entry:
        raise RuntimeError(f"{path}: sum-herdr already has a different configuration; inspect it before changing it.")
    tools["sum-herdr"] = entry
    write_json(path, data)


def codex_config(path, command):
    begin, end = "# BEGIN sum-managed MCP", "# END sum-managed MCP"
    text = path.read_text() if path.exists() else ""
    parsed = tomllib.loads(text)
    if "sum-herdr" in parsed.get("mcp_servers", {}) and begin not in text:
        raise RuntimeError(f"{path}: existing sum-herdr entry is not owned by this setup")
    block = f'''{begin}
[mcp_servers.sum-herdr]
command = {json.dumps(command)}
startup_timeout_sec = 20
tool_timeout_sec = 70
env_vars = ["HERDR_ENV", "HERDR_PANE_ID", "HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH", "SUM_SESSION"]
{end}'''
    if begin in text:
        if text.count(begin) != 1 or text.count(end) != 1:
            raise RuntimeError("Malformed managed MCP block; inspect before changing it")
        start, finish = text.index(begin), text.index(end) + len(end)
        text = text[:start] + block + text[finish:]
    else:
        text = text.rstrip() + "\n\n" + block + "\n"
    tomllib.loads(text)  # Never write invalid TOML.
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def configure(root=ROOT):
    command = str(root / "bin/herdr-mesh")
    entry = {"command": command, "args": []}
    merge_mcp(root / ".mcp.json", "mcpServers", entry)
    merge_mcp(root / ".cursor/mcp.json", "mcpServers", entry)
    merge_mcp(root / "opencode.json", "mcp", {"type": "local", "command": [command], "enabled": True})
    codex_config(root / ".codex/config.toml", command)
    generic = {"mcpServers": {"sum-herdr": entry}}
    write_json(root / ".local/mcp.example.json", generic)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--configure-only", action="store_true", help="Regenerate local configs without installing dependencies")
    ap.add_argument("--install-codex", action="store_true", help="Also install the pinned Codex CLI locally; never authenticate automatically")
    args = ap.parse_args()
    if not args.configure_only:
        local = ROOT / ".local/bin"
        local.mkdir(parents=True, exist_ok=True)
        # Resolve through mise so a stale generated symlink cannot select the wrong version.
        for name in ("python3", "node", "herdr", "gh", "quota-axi"):
            resolved = execute(["mise", "which", name], capture=True)
            symlink(str(Path(resolved).resolve()), local / name)
        deps = ROOT / ".deps"
        deps.mkdir(exist_ok=True)
        mesh = deps / "herdr-mesh"
        if not mesh.exists():
            staging = Path(tempfile.mkdtemp(prefix="mesh-", dir=deps))
            try:
                execute(["git", "clone", "--filter=blob:none", "--no-checkout", MESH_REMOTE, str(staging / "source")])
                execute(["git", "checkout", "--detach", MESH_REV], cwd=staging / "source")
                os.replace(staging / "source", mesh)
            finally:
                shutil.rmtree(staging)
        actual = execute(["git", "rev-parse", "HEAD"], cwd=mesh, capture=True)
        if actual != MESH_REV:
            raise RuntimeError(f"Unexpected Mesh checkout {actual}; preserve/remove .deps/herdr-mesh explicitly before re-running setup")
        if not (mesh / "package-lock.json").is_file() or not (mesh / "LICENSE").is_file():
            raise RuntimeError("Pinned Mesh checkout is incomplete")
        execute(["npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=mesh)
        shutil.copyfile(ROOT / "patches/herdr-mesh/server.js", mesh / "dist/server.js")
        shutil.copyfile(ROOT / "patches/herdr-mesh/commands.mjs", mesh / "dist/sum-commands.mjs")
        write_json(mesh / ".sum-patched", {"upstream": MESH_REV,
                   "server_sha256": hashlib.sha256((mesh / "dist/server.js").read_bytes()).hexdigest(),
                   "commands_sha256": hashlib.sha256((mesh / "dist/sum-commands.mjs").read_bytes()).hexdigest()})
        skill = execute([str(local / "herdr"), "--skill"], capture=True)
        skill_path = ROOT / ".local/skills/herdr/SKILL.md"
        skill_path.parent.mkdir(parents=True, exist_ok=True)
        skill_path.write_text(skill + "\n")
        for parent in (ROOT / ".agents/skills", ROOT / ".claude/skills"):
            symlink("../../.local/skills/herdr", parent / "herdr")
        if args.install_codex:
            harness = ROOT / ".deps/harnesses"
            harness.mkdir(exist_ok=True)
            if not (harness / "package.json").exists():
                write_json(harness / "package.json", {"private": True, "name": "sum-local-harnesses"})
            execute(["npm", "install", "--prefix", str(harness), "--save-exact", "--ignore-scripts", CODEX_PACKAGE])
            symlink(str(harness / "node_modules/.bin/codex"), local / "codex")
    configure()
    if not args.configure_only:
        execute([str(ROOT / ".local/bin/node"), str(ROOT / "scripts/mcp_smoke.mjs")])
    print("\n" + ("Local configuration generated; dependencies were not installed." if args.configure_only else "Setup complete.") + " No global harness configs, credentials, or Herdr settings were changed.")
    print("Inside Herdr: cd into sum, run ./bin/sumctl doctor, then launch codex, claude, grok, cursor-agent, pi, or another configured harness.")
    print("Accept the harness's project/MCP trust prompt. For a harness without native project instructions, paste: Read AGENTS.md and initialize sum.")
    print("No installed harness? Re-run mise run setup -- --install-codex, then authenticate Codex normally.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.CalledProcessError, ValueError) as exc:
        print(f"setup failed: {exc}", file=sys.stderr)
        sys.exit(1)
