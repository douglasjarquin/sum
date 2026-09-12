#!/usr/bin/env python3
"""First-install setup: link pinned tools, install the Go Mesh binary, and create only repository-local integration files.

Re-running is safe while sum is in service: an existing native binary, tool link, or configuration is never rewritten.
Newer code and dependencies are staged as an immutable release with `./bin/sumctl release stage` instead.
"""
from __future__ import annotations
import argparse
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tomllib
import uuid

ROOT = Path(__file__).resolve().parents[1]
CODEX_PACKAGE = "@openai/codex@0.153.4"
_spec = importlib.util.spec_from_file_location("sumctl_lib", ROOT / "lib" / "sumctl.py")
sumctl = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(sumctl)


def execute(args, cwd=None, capture=False):
    print("+ " + " ".join(str(a) for a in args), file=sys.stderr)
    result = subprocess.run([str(a) for a in args], cwd=cwd, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else None


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


def designate(root):
    """Mark this checkout as the sum installation: only a home with state.json may host a coordinator."""
    home = root / ".sum"
    if (home / "dev.json").is_file():
        print(f"{root} is a sum development checkout; it stays undesignated (no coordinator can claim it).", file=sys.stderr)
        return
    home.mkdir(parents=True, exist_ok=True, mode=0o700)
    if not (home / "state.json").exists():
        write_json(home / "state.json", {"schema": 1, "sum_version": "0.1.0", "created_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
                                         "instance": uuid.uuid4().hex})


def configure(root=ROOT):
    designate(root)
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
    notes = []
    if not args.configure_only:
        links = sumctl.resolve_tools(ROOT)  # mise install plus create-once links; an existing link is never retargeted.
        for name, link in links.items():
            if link["differs"]:
                notes.append(f"{link['link']} still points at {link['target']}; a running process may use it. Stage a release to pick up the new pin.")
        sumctl.build_native_artifact(ROOT)
        remainder = sumctl.install_remainder(ROOT)
        if remainder.get("differs"):
            notes.append(f"{remainder['link']} still points at {remainder['target']}; a running process may use it. Stage a release to pick up the new pin.")
        if remainder.get("reason"):
            notes.append(remainder["reason"])
        sumctl.write_herdr_skill(ROOT)
        if args.install_codex:
            harness = ROOT / ".deps/harnesses"
            harness.mkdir(parents=True, exist_ok=True)
            if not (harness / "package.json").exists():
                write_json(harness / "package.json", {"private": True, "name": "sum-local-harnesses"})
            execute(["npm", "install", "--prefix", str(harness), "--save-exact", "--ignore-scripts", CODEX_PACKAGE])
            sumctl.link_tool(ROOT / ".local/bin/codex", harness / "node_modules/.bin/codex")
    configure()
    if not args.configure_only:
        execute([str(ROOT / ".local/bin/node"), str(ROOT / "scripts/mcp_smoke.mjs")])
    for note in notes:
        print("note: " + note, file=sys.stderr)
    print("\n" + ("Local configuration generated; dependencies were not installed." if args.configure_only else "Setup complete.") + " No global harness configs, credentials, or Herdr settings were changed.")
    print("Inside Herdr: cd into sum, optionally run ./bin/sumctl doctor (observation only), then launch codex, claude, grok, cursor-agent, pi, or another configured harness.")
    print("The harness runs ./bin/sumctl init itself: the first pane claims coordinator; later panes here are developers unless dispatched.")
    print("Accept the harness's project/MCP trust prompt. For a harness without native project instructions, paste: Read AGENTS.md and initialize sum.")
    print("No installed harness? Re-run mise run setup -- --install-codex, then authenticate Codex normally.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.CalledProcessError, ValueError, sumctl.SumError) as exc:
        print(f"setup failed: {exc}", file=sys.stderr)
        sys.exit(1)
