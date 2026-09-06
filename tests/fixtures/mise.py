#!/usr/bin/env python3
"""A strict fake for the one mise call sum makes: `mise tasks ls --json`. Resolves tasks the way mise does, walking parent directories
from the working directory: every `mise.toml` `[tasks]` entry and every executable file under a sibling `mise-tasks/` directory, nearer definitions winning.
Never runs a task. Set FAKE_MISE_MISSING=1 to behave as an absent binary."""
import json, os, pathlib, sys, tomllib
if os.environ.get("FAKE_MISE_MISSING"):
    print("fake mise disabled", file=sys.stderr); sys.exit(127)
args = sys.argv[1:]
run_name = None
if len(args) == 2 and args[0] == "run":
    run_name = args[1]  # `mise run NAME`: the one execution the portable verify runner needs; resolved exactly like `tasks ls`.
elif args != ["tasks", "ls", "--json"]:
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
            for task, spec in tasks.items():
                run = spec if isinstance(spec, str) else (spec.get("run") if isinstance(spec, dict) else None)
                found.setdefault(task, {"name": task, "source": str(config), "dir": str(directory), "run": run})
    tasks_dir = directory / "mise-tasks"
    if tasks_dir.is_dir():
        for path in sorted(tasks_dir.iterdir()):
            if path.is_file():
                found.setdefault(path.name, {"name": path.name, "source": str(path), "file": str(path), "dir": str(directory)})
    if directory == stop:
        break
if run_name is not None:
    task = found.get(run_name)
    if task is None:
        print("[fake mise] no task %s" % run_name, file=sys.stderr); sys.exit(1)
    if task.get("file"):
        result = __import__("subprocess").run([task["file"]], cwd=task["dir"])
    else:
        run = task["run"]
        script = " && ".join(run) if isinstance(run, list) else (run or "exit 1")  # A `run = [...]` list executes in order and stops at the first failure, as mise does.
        result = __import__("subprocess").run(["sh", "-c", script], cwd=task["dir"])
    sys.exit(result.returncode)
print(json.dumps([{k: v for k, v in r.items() if k != "run"} for r in sorted(found.values(), key=lambda r: r["name"])]))
