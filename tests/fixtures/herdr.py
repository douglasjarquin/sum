#!/usr/bin/env python3
"""A strict fake for documented Herdr 0.8.2 calls. Uses real Git worktrees."""
import fcntl, json, os, pathlib, subprocess, sys, uuid
root = pathlib.Path(os.environ["FAKE_HERDR_ROOT"])
root.mkdir(exist_ok=True, parents=True)
_lock = (root / ".lock").open("a")  # Like Herdr's server, one invocation at a time mutates the store; concurrent callers never lose each other's writes.
fcntl.flock(_lock, fcntl.LOCK_EX)
args = sys.argv[1:]
if args == ["--version"]:
    print(os.environ.get("FAKE_HERDR_VERSION", "herdr 0.8.2")); sys.exit(0)
if len(args) < 3 or args[0] != "--session":
    print("explicit session required", file=sys.stderr); sys.exit(2)
session, args = args[1], args[2:]
if session != os.environ.get("FAKE_SESSION", "sum-test"):
    print(json.dumps({"error": {"code": "wrong_session", "message": "wrong session"}}), file=sys.stderr); sys.exit(2)
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps({"session": session, "args": args}) + "\n")
state_path = root / "state.json"
state = json.loads(state_path.read_text()) if state_path.exists() else {"panes": {}}
state.setdefault("workspaces", {})
parent = os.environ.get("HERDR_PANE_ID", "w-parent:p1")
state["panes"][parent] = {"pane_id": parent, "cwd": os.environ.get("FAKE_PARENT_CWD", "/tmp"), "workspace_id": parent.split(":")[0],
  "agent_status": os.environ.get("FAKE_PARENT_STATUS", "idle"), "agent": os.environ.get("FAKE_PARENT_KIND", "claude")}
state["workspaces"].setdefault(parent.split(":")[0], {"workspace_id": parent.split(":")[0], "label": "coordinator", "worktree": None})

def save():
    tmp = state_path.with_name("state.%d.tmp" % os.getpid())  # Atomic like Herdr's own store; concurrent CLI calls must not see partial JSON.
    tmp.write_text(json.dumps(state)); os.replace(tmp, state_path)

def fail(code, message=None):
    print(json.dumps({"error": {"code": code, "message": message or code}}), file=sys.stderr); sys.exit(1)

def emit(result):
    save()
    print(json.dumps({"result": result})); sys.exit(0)

def arg(name):
    return args[args.index(name) + 1]

if args[:2] == ["worktree", "create"]:
    if "--no-focus" not in args or "--cwd" not in args: fail("wrong worktree contract")
    path = root / "worktrees with spaces" / arg("--branch").replace("/", "-")
    path.parent.mkdir(exist_ok=True)
    result = subprocess.run(["git", "-C", arg("--cwd"), "worktree", "add", "-b", arg("--branch"), str(path), arg("--base")], capture_output=True, text=True)
    if result.returncode: fail(result.stderr)
    workspace = "w-" + uuid.uuid4().hex[:6]
    pane = workspace + ":p1"
    state["panes"][pane] = {"pane_id": pane, "cwd": str(path), "workspace_id": workspace, "agent_status": "unknown", "agent": None}
    state["workspaces"][workspace] = {"workspace_id": workspace, "label": arg("--label") if "--label" in args else "",
                                      "worktree": {"checkout_path": str(path), "repo_root": arg("--cwd"), "is_linked_worktree": True}}
    response = {"root_pane": {"pane_id": pane}, "workspace": {"workspace_id": workspace},
                "worktree": {"path": str(path), "branch": arg("--branch")}}
    if os.environ.get("FAKE_BAD_WORKTREE"): response["worktree"]["path"] = arg("--cwd")
    emit(response)
if args[:2] == ["workspace", "create"]:
    if "--no-focus" not in args or "--cwd" not in args: fail("wrong workspace contract")
    workspace = "w-" + uuid.uuid4().hex[:6]
    pane = workspace + ":p1"
    state["panes"][pane] = {"pane_id": pane, "cwd": arg("--cwd"), "workspace_id": workspace, "agent_status": "unknown", "agent": None}
    state["workspaces"][workspace] = {"workspace_id": workspace, "label": arg("--label") if "--label" in args else "", "worktree": None}
    emit({"workspace": {"workspace_id": workspace}, "tab": {"tab_id": workspace + ":t1"}, "root_pane": {"pane_id": pane}})
if args[:2] == ["agent", "start"]:
    if "--pane" not in args or "--kind" not in args or "--cwd" in args: fail("obsolete agent-start syntax")
    pane = arg("--pane")
    if pane not in state["panes"] or state["panes"][pane]["agent"]: fail("pane is not an available shell")
    state["panes"][pane].update(agent=arg("--kind"), name=args[2], agent_status="idle")
    save()
    if os.environ.get("FAKE_START_UNCERTAIN"): fail("agent_not_ready: simulated trust prompt")
    emit({"agent": state["panes"][pane]})
if args[:2] in (["agent", "get"], ["pane", "get"]):
    pane = state["panes"].get(args[2])
    if not pane: fail("pane_not_found" if args[0] == "pane" else "agent_not_found")
    if args[0] == "agent" and not pane.get("agent"): fail("agent_not_found")
    emit({args[0]: pane})
if args[:2] == ["agent", "list"]: emit({"agents": [p for p in state["panes"].values() if p["agent"]]})
if args[:2] == ["agent", "prompt"]:
    pane = state["panes"].get(args[2])
    if not pane or not pane.get("agent"): fail("agent_not_running")
    if pane["agent_status"] == "blocked": fail("agent_blocked")
    if os.environ.get("FAKE_FAIL_PROMPT") or args[2] in os.environ.get("FAKE_FAIL_PROMPT_PANES", "").split(","): fail("simulated uncertain prompt")
    pane["last_prompt"] = args[3]
    pane["agent_status"] = "working"
    if os.environ.get("FAKE_PROMPT_HANG"):  # The prompt reached the pane, then the CLI never answered: the caller sees only a timeout.
        save(); import time; time.sleep(float(os.environ["FAKE_PROMPT_HANG"]))
    emit({"agent": pane})
if args[:2] == ["agent", "wait"]:
    if "--status" in args or "--until" not in args: fail("obsolete --status flag")
    pane = state["panes"].get(args[2])
    if not pane or pane["agent_status"] != arg("--until"): fail("timeout")
    emit({"agent": pane})
if args[:2] == ["integration", "status"]: emit({"integrations": []})
if args[:2] == ["agent", "read"]:
    pane = state["panes"].get(args[2])
    if not pane or not pane.get("agent"): fail("agent_not_found")
    if "--source" not in args or "--lines" not in args: fail("explicit read source and line count required")
    print(pane.get("screen", "")); save(); sys.exit(0)  # Real 0.8.2 prints the text itself, not a JSON envelope.
# --- issue #14 plugin registry: user-global like Herdr's, so every session sees the same rows -------------------------------
import tomllib
registry_path = root / "plugins.json"
registry = json.loads(registry_path.read_text()) if registry_path.exists() else {}
def save_registry():
    tmp = registry_path.with_name("plugins.%d.tmp" % os.getpid()); tmp.write_text(json.dumps(registry)); os.replace(tmp, registry_path)
def plugin_row(plugin_id):
    row = registry.get(plugin_id)
    if not row: fail("plugin_not_found", f"plugin {plugin_id} not found")
    return row
if args[:2] == ["plugin", "link"]:
    path = pathlib.Path(args[2])
    manifest = path / "herdr-plugin.toml"
    if not manifest.is_file(): fail("manifest_missing", str(manifest))
    doc = tomllib.loads(manifest.read_text())
    known = {"pane.agent_status_changed", "pane.agent_detected", "pane.exited", "pane.closed", "workspace.closed", "worktree.created"}
    events = [{"on": e["on"], "command": e["command"]} for e in doc.get("events", [])]
    previous = registry.get(doc["id"], {})
    registry[doc["id"]] = {"plugin_id": doc["id"], "name": doc["name"], "version": doc["version"], "min_herdr_version": doc["min_herdr_version"],
                           "manifest_path": str(manifest), "plugin_root": str(path), "enabled": "--disabled" not in args and previous.get("enabled", True),
                           "events": events, "startup": [{"command": s["command"]} for s in doc.get("startup", [])], "source": {"kind": "local"},
                           "warnings": [f"unknown event '{e['on']}'" for e in events if e["on"] not in known]}
    save_registry(); emit({"plugin": registry[doc["id"]], "type": "plugin_linked"})
if args[:2] == ["plugin", "list"]:
    if "--json" not in args: fail("text_output", "the fake only speaks --json; sum must never parse the human listing")
    rows = list(registry.values()) if "--plugin" not in args else [r for r in registry.values() if r["plugin_id"] == arg("--plugin")]
    emit({"plugins": rows, "type": "plugin_list"})
if args[:2] in (["plugin", "enable"], ["plugin", "disable"]):
    row = plugin_row(args[2]); row["enabled"] = args[1] == "enable"; save_registry(); emit({"plugin": row, "type": f"plugin_{args[1]}d"})
if args[:2] == ["plugin", "unlink"]:
    plugin_row(args[2]); del registry[args[2]]; save_registry(); emit({"plugin_id": args[2], "removed": True, "type": "plugin_unlinked"})
# --- issue #10 cleanup surface: observation and native removal without force ---------------------------------
if args[:2] == ["workspace", "get"]:
    workspace = state["workspaces"].get(args[2])
    if not workspace: fail("workspace_not_found", f"workspace {args[2]} not found")
    panes = [p for p in state["panes"].values() if p.get("workspace_id") == args[2]]
    emit({"workspace": {**workspace, "pane_count": len(panes), "agent_status": "unknown"}})
if args[:2] == ["pane", "list"]:
    if "--workspace" not in args: fail("explicit workspace required")
    if arg("--workspace") not in state["workspaces"]: fail("workspace_not_found", "workspace not found")
    emit({"panes": [p for p in state["panes"].values() if p.get("workspace_id") == arg("--workspace")]})
if args[:2] == ["pane", "process-info"]:
    if "--pane" not in args: fail("explicit pane required")
    pane = state["panes"].get(arg("--pane"))
    if not pane: fail("pane_not_found", "pane not found")
    shell = pane.get("shell_pid", 4242)
    # A pane's process list is scenario data written by the test; the default is an idle shell in the pane cwd.
    foreground = pane.get("processes", [{"pid": shell, "name": "bash", "argv0": "bash", "argv": ["-bash"], "cwd": pane["cwd"]}])
    if pane.get("agent") and "processes" not in pane:  # A live agent occupies the foreground until the scenario clears it.
        foreground = [{"pid": shell + 1, "name": pane["agent"], "argv0": pane["agent"], "argv": [pane["agent"]], "cwd": pane["cwd"]}]
    emit({"process_info": {"pane_id": pane["pane_id"], "shell_pid": shell, "foreground_process_group_id": foreground[0]["pid"] if foreground else shell,
                           "foreground_processes": foreground}})
# --- issue #17 service surface: split, run, interrupt, wait-output. Processes are scenario data; a run makes one appear ---------
def lsof_scenario():
    """The fake lsof shares its scenario file so a launched process can be observed as a listener (FAKE_RUN_LISTEN=host:port)."""
    root_ = os.environ.get("FAKE_LSOF_ROOT")
    if not root_: return None, None
    path_ = pathlib.Path(root_) / "cwds.json"
    data_ = json.loads(path_.read_text()) if path_.exists() else {"processes": [], "listeners": []}
    return path_, data_
if args[:2] == ["pane", "split"]:
    if "--direction" not in args or "--cwd" not in args or "--no-focus" not in args: fail("wrong split contract")
    target = state["panes"].get(args[2])
    if not target: fail("pane_not_found", f"pane {args[2]} not found")
    if os.environ.get("FAKE_SPLIT_CRASH"):  # Real Herdr created the pane, the caller died before recording the id.
        workspace = target["workspace_id"]; pane = f"{workspace}:p{len(state['panes']) + 10}"
        state["panes"][pane] = {"pane_id": pane, "cwd": arg("--cwd"), "workspace_id": workspace, "agent_status": "unknown", "agent": None, "shell_pid": 5000 + len(state["panes"]), "processes": []}
        save(); sys.exit(137)
    workspace = target["workspace_id"]
    pane = f"{workspace}:p{len(state['panes']) + 10}"
    state["panes"][pane] = {"pane_id": pane, "cwd": arg("--cwd"), "workspace_id": workspace, "agent_status": "unknown", "agent": None, "shell_pid": 5000 + len(state["panes"]), "processes": []}
    emit({"pane": state["panes"][pane]})
if args[:2] == ["pane", "run"]:
    pane = state["panes"].get(args[2])
    if not pane: fail("pane_not_found", f"pane {args[2]} not found")
    command = args[3]
    pane["last_command"] = command
    if pane.get("processes") or pane.get("agent"):  # Text typed into a busy program: nothing new starts.
        emit({"sent": command})
    behavior = os.environ.get("FAKE_RUN_BEHAVIOR", "run")
    if behavior != "exit":
        import shlex
        argv = shlex.split(command)
        pid = 6000 + (abs(hash(pane["pane_id"] + command)) % 900)
        pane["processes"] = [{"pid": pid, "name": argv[0], "argv0": argv[0], "argv": argv, "cwd": pane["cwd"]}]
        if behavior == "stubborn": pane["stubborn"] = True
        path_, data_ = lsof_scenario()
        if path_ is not None and os.environ.get("FAKE_RUN_LISTEN"):
            data_.setdefault("processes", []).append({"pid": pid, "cwd": pane["cwd"]})
            if os.environ.get("FAKE_RUN_LISTEN") != "none":
                data_.setdefault("listeners", []).append({"pid": pid, "address": os.environ["FAKE_RUN_LISTEN"]})
            path_.write_text(json.dumps(data_))
    emit({"sent": command})
if args[:2] == ["pane", "send-keys"]:
    pane = state["panes"].get(args[2])
    if not pane: fail("pane_not_found", f"pane {args[2]} not found")
    keys = args[3:]
    if not keys: fail("keys required")
    if "ctrl+c" in keys and not pane.get("stubborn"):
        gone = [p["pid"] for p in pane.get("processes", [])]
        pane["processes"] = []
        path_, data_ = lsof_scenario()
        if path_ is not None and gone:
            data_["processes"] = [p for p in data_.get("processes", []) if p["pid"] not in gone]
            data_["listeners"] = [l for l in data_.get("listeners", []) if l["pid"] not in gone]
            path_.write_text(json.dumps(data_))
    emit({"sent": keys})
if args[:2] == ["pane", "wait-output"]:
    pane = state["panes"].get(args[2])
    if not pane: fail("pane_not_found", f"pane {args[2]} not found")
    if "--match" not in args or "--timeout" not in args: fail("explicit match and timeout required")
    if arg("--match") in pane.get("screen", ""): emit({"matched": True})
    fail("timeout", "wait-output timed out")
if args[:2] == ["pane", "close"]:
    if args[2] not in state["panes"]: fail("pane_not_found", f"pane {args[2]} not found")
    gone = [p["pid"] for p in state["panes"][args[2]].get("processes", [])]
    path_, data_ = lsof_scenario()
    if path_ is not None and gone:  # Closing a pane ends its process tree, as the real server does.
        data_["processes"] = [p for p in data_.get("processes", []) if p["pid"] not in gone]
        data_["listeners"] = [l for l in data_.get("listeners", []) if l["pid"] not in gone]
        path_.write_text(json.dumps(data_))
    del state["panes"][args[2]]
    emit({"closed": args[2]})
if args[:2] == ["worktree", "list"]:
    rows = []
    for workspace in state["workspaces"].values():
        if workspace.get("worktree"):
            rows.append({"path": workspace["worktree"]["checkout_path"], "open_workspace_id": workspace["workspace_id"], "is_linked_worktree": True})
    emit({"worktrees": rows})
if args[:2] == ["worktree", "remove"]:
    if "--workspace" not in args: fail("explicit workspace required")
    if "--force" in args: fail("forbidden_force", "the fake never accepts --force")
    workspace = state["workspaces"].get(arg("--workspace"))
    if not workspace: fail("workspace_not_found", f"workspace {arg('--workspace')} not found")
    if not workspace.get("worktree"): fail("worktree_not_found", "workspace has no worktree")
    path = workspace["worktree"]["checkout_path"]
    if os.environ.get("FAKE_REMOVE_CRASH"):  # Real Herdr 0.8.2 removed the checkout and closed the workspace; the caller crashed before recording it.
        subprocess.run(["git", "-C", workspace["worktree"]["repo_root"], "worktree", "remove", path], capture_output=True, text=True)
        for pane_id in [p for p, v in state["panes"].items() if v.get("workspace_id") == workspace["workspace_id"]]: del state["panes"][pane_id]
        del state["workspaces"][workspace["workspace_id"]]
        save(); sys.exit(137)
    result = subprocess.run(["git", "-C", workspace["worktree"]["repo_root"], "worktree", "remove", path], capture_output=True, text=True)
    if result.returncode:  # Herdr 0.8.2: plain `git worktree remove` failure surfaces as this code; nothing was removed.
        fail("dirty_worktree_requires_force", result.stderr.strip())
    for pane_id in [p for p, v in state["panes"].items() if v.get("workspace_id") == workspace["workspace_id"]]: del state["panes"][pane_id]
    del state["workspaces"][workspace["workspace_id"]]
    emit({"forced": False, "path": path, "workspace_id": workspace["workspace_id"]})
fail("unsupported fake operation: " + repr(args))
