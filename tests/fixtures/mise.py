#!/usr/bin/env python3
"""A strict fake for the one mise call sum makes: `mise tasks ls --json`. Resolves tasks the way mise does, walking parent directories
from the working directory: every `mise.toml` `[tasks]` entry and every executable file under a sibling `mise-tasks/` directory, nearer definitions winning.
Never runs a task. Set FAKE_MISE_MISSING=1 to behave as an absent binary."""
import json, os, pathlib, sys, tomllib
if os.environ.get("FAKE_MISE_MISSING"):
    print("fake mise disabled", file=sys.stderr); sys.exit(127)
args = sys.argv[1:]
if args != ["tasks", "ls", "--json"]:
    print("unsupported fake mise call: %r" % (args,), file=sys.stderr); sys.exit(2)
cwd = pathlib.Path(os.getcwd()).resolve()
stop = pathlib.Path(os.environ.get("FAKE_MISE_STOP", "/")).resolve()  # The fake never walks above the lab root, so a real parent config on this host cannot leak in.
found = {}
for directory in [cwd, *cwd.parents]:
    if stop not in (directory, *directory.parents):
        break
    for name in ("mise.toml", ".mise.toml"):
        config = directory / name
        if config.is_file():
            try:
                tasks = tomllib.loads(config.read_text()).get("tasks") or {}
            except tomllib.TOMLDecodeError:
                tasks = {}
            for task in tasks:
                found.setdefault(task, {"name": task, "source": str(config), "dir": str(directory)})
    tasks_dir = directory / "mise-tasks"
    if tasks_dir.is_dir():
        for path in sorted(tasks_dir.iterdir()):
            if path.is_file():
                found.setdefault(path.name, {"name": path.name, "source": str(path), "file": str(path), "dir": str(directory)})
    if directory == stop:
        break
print(json.dumps(sorted(found.values(), key=lambda r: r["name"])))
