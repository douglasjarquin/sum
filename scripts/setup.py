#!/usr/bin/env python3
"""First-install setup: stage pinned tools and the native HC binary, and create only repository-local integration files.

Re-running is safe while sum is in service: an existing native binary, tool link, or configuration is never rewritten.
Newer code and dependencies are staged as an immutable release with `./bin/sumctl release stage` instead.
"""
from __future__ import annotations
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import tomllib
import uuid

ROOT = Path(__file__).resolve().parents[1]
CODEX_PACKAGE = "@openai/codex@0.166.0"
sys.path.insert(0, str(Path(__file__).resolve().parent))
import install_runtime as runtime_install


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


def mcp_retirement(path, root_key, entry):
    if not path.exists():
        return None
    data = json.loads(path.read_text())
    if not isinstance(data, dict) or not isinstance(data.get(root_key, {}), dict):
        raise RuntimeError(f"{path}: MCP ownership conflict in malformed configuration")
    tools = data.get(root_key, {})
    prior = tools.get("sum-herdr")
    if prior is None:
        return None
    if prior != entry:
        raise RuntimeError(f"{path}: sum-herdr ownership conflict; its customized entry was preserved")
    del tools["sum-herdr"]
    return data


def codex_retirement(path, command):
    if not path.exists():
        return None
    begin, end = "# BEGIN sum-managed MCP", "# END sum-managed MCP"
    text = path.read_text()
    parsed = tomllib.loads(text)
    prior = parsed.get("mcp_servers", {}).get("sum-herdr")
    if prior is None and begin not in text and end not in text:
        return None
    expected = {"command": command, "startup_timeout_sec": 20, "tool_timeout_sec": 70,
                "env_vars": ["HERDR_ENV", "HERDR_PANE_ID", "HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH", "SUM_SESSION"]}
    if prior != expected or text.count(begin) != 1 or text.count(end) != 1 or text.index(begin) > text.index(end):
        raise RuntimeError(f"{path}: sum-herdr ownership conflict; inspect the managed block before retirement")
    start, finish = text.index(begin), text.index(end) + len(end)
    block = tomllib.loads(text[start + len(begin):text.index(end)])
    if block != {"mcp_servers": {"sum-herdr": expected}}:
        raise RuntimeError(f"{path}: sum-herdr ownership conflict; managed block contains unrelated settings")
    result = text[:start] + text[finish:]
    if "sum-herdr" in tomllib.loads(result).get("mcp_servers", {}):
        raise RuntimeError(f"{path}: sum-herdr ownership conflict outside managed block")
    return result


def remove_codex_mcp(path, command):
    result = codex_retirement(path, command)
    if result is not None:
        path.write_text(result)


def designate(root):
    """Mark this checkout as the sum installation: only a home with state.json may host a coordinator."""
    home = root / ".sum"
    if (home / "dev.json").is_file():
        print(f"{root} is a sum development checkout; it stays undesignated (no coordinator can claim it).", file=sys.stderr)
        return
    home.mkdir(parents=True, exist_ok=True, mode=0o700)
    pointer = home / "state.json"
    if pointer.exists():
        current = json.loads(pointer.read_text())
        if current.get("schema") != 2 or current.get("backend") != "hc":
            raise RuntimeError("Existing Sum state requires explicit HC migration; setup never converts live state")
        return
    generation = "g-" + uuid.uuid4().hex[:24]
    data = home / "generations" / generation
    data.mkdir(parents=True, mode=0o700)
    (data / "tasks").mkdir(mode=0o700)
    (data / "sessions").mkdir(mode=0o700)
    write_json(data / "state.json", {"schema": 2, "backend": "hc", "generation": generation,
                                     "root": str(home.resolve()), "sum_version": "0.1.0",
                                     "created_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
                                     "instance": uuid.uuid4().hex})
    write_json(pointer, {"schema": 2, "backend": "hc", "generation": generation})


def configure(root=ROOT, *, retire_legacy=False):
    command = str(root / "bin/herdr-mesh")
    entry = {"command": command, "args": []}
    changes = []
    for name, key, expected in ((".mcp.json", "mcpServers", entry),
                                (".cursor/mcp.json", "mcpServers", entry),
                                ("opencode.json", "mcp", {"type": "local", "command": [command], "enabled": True}),
                                (".local/mcp.example.json", "mcpServers", entry)):
        path = root / name
        value = mcp_retirement(path, key, expected)
        if value is not None:
            changes.append((path, value))
    codex = root / ".codex/config.toml"
    text = codex_retirement(codex, command)
    # Validate ownership for the complete retirement before changing any config.
    designate(root)
    if retire_legacy:
        for path, value in changes:
            write_json(path, value)
        if text is not None:
            codex.write_text(text)
    elif changes or text is not None:
        print("Owned legacy MCP entries retained until explicit post-cutover --retire-legacy-config.", file=sys.stderr)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--configure-only", action="store_true", help="Regenerate local configs without installing dependencies")
    ap.add_argument("--install-codex", action="store_true", help="Also install the pinned Codex CLI locally; never authenticate automatically")
    ap.add_argument("--retire-legacy-config", action="store_true", help="After explicit HC cutover, remove only exactly owned legacy MCP entries")
    args = ap.parse_args()
    notes = []
    if not args.configure_only:
        links = runtime_install.resolve_tools(ROOT)
        for name, link in links.items():
            if link["differs"]:
                notes.append(f"{link['link']} still points at {link['target']}; a running process may use it. Stage a release to pick up the new pin.")
        runtime_install.build_native_artifact(ROOT)
        remainder = runtime_install.install_remainder(ROOT)
        if remainder.get("differs"):
            notes.append(f"{remainder['link']} still points at {remainder['target']}; a running process may use it. Stage a release to pick up the new pin.")
        if remainder.get("reason"):
            notes.append(remainder["reason"])
        hc = runtime_install.install_hc(ROOT)
        if hc.get("differs"):
            notes.append("Existing HC link retained; stage a release to pick up the selected pin.")
        if args.install_codex:
            harness = ROOT / ".deps/harnesses"
            harness.mkdir(parents=True, exist_ok=True)
            if not (harness / "package.json").exists():
                write_json(harness / "package.json", {"private": True, "name": "sum-local-harnesses"})
            execute(["npm", "install", "--prefix", str(harness), "--save-exact", "--ignore-scripts", CODEX_PACKAGE])
            runtime_install.link_tool(ROOT / ".local/bin/codex", harness / "node_modules/.bin/codex")
    configure(retire_legacy=args.retire_legacy_config)

    for note in notes:
        print("note: " + note, file=sys.stderr)
    print("\n" + ("Local configuration generated; dependencies were not installed." if args.configure_only else "Setup complete.") + " No global harness configs, credentials, or HC settings were changed.")
    print("Inside the selected HC terminal: cd into sum, optionally run ./bin/sumctl doctor (observation only), then launch codex, claude, grok, cursor-agent, pi, devin, or another configured harness.")
    print("The harness runs ./bin/sumctl init itself: the first pane claims coordinator; later panes here are developers unless dispatched.")
    print("Read any harness trust or authentication prompt yourself. For a harness without native project instructions, paste: Read AGENTS.md and initialize sum.")
    print("No installed harness? Re-run mise run setup -- --install-codex, then authenticate Codex normally.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.CalledProcessError, ValueError, runtime_install.InstallError) as exc:
        print(f"setup failed: {exc}", file=sys.stderr)
        sys.exit(1)
