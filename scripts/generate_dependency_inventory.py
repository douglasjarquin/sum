#!/usr/bin/env python3
"""Render dependency metadata with versions resolved from the repository's pins."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import sys
import tomllib

ROOT = Path(__file__).resolve().parents[1]
TOOLS = {"go": "go", "python": "python", "node": "node", "uv": "uv", "aube": "aube",
         "gh": "github:cli/cli", "quota-axi": "npm:quota-axi", "codegraph": "npm:@colbymchenry/codegraph",
         "skills": "npm:skills", "basedpyright": "pipx:basedpyright", "gopls": "go:golang.org/x/tools/gopls"}
MODULES = {"go-toon": "github.com/douglasjarquin/go-toon", "cobra": "github.com/spf13/cobra"}


def render(root: Path) -> str:
    metadata = json.loads((root / "config/dependency-inventory.json").read_text())
    tools = tomllib.loads((root / "mise.toml").read_text())["tools"]
    go_mod = (root / "go/go.mod").read_text()
    hc = json.loads((root / "config/hc-release.json").read_text())
    ids = set()
    for entry in metadata["dependencies"]:
        name = entry["id"]
        if name in ids:
            raise ValueError(f"duplicate dependency metadata: {name}")
        ids.add(name)
        if name in TOOLS:
            pin = tools[TOOLS[name]]
            entry["version"] = pin["version"] if isinstance(pin, dict) else pin
        elif name in MODULES:
            found = re.search(r"^\s*" + re.escape(MODULES[name]) + r"\s+(v\S+)", go_mod, re.MULTILINE)
            if not found:
                raise ValueError(f"missing direct module pin for {name}")
            entry["version"] = found[1]
        elif name == "herdr-core":
            entry["version"] = hc["version"]
    metadata["generated_by"] = "scripts/generate_dependency_inventory.py"
    return json.dumps(metadata, indent=2) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    rendered = render(args.root)
    output = args.root / "docs/dependency-inventory.json"
    if args.check:
        if output.read_text() != rendered:
            print("Dependency inventory is stale; run scripts/generate_dependency_inventory.py", file=sys.stderr)
            return 1
    else:
        output.write_text(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
