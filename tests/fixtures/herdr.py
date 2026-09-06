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
    emit({"agent": pane})
if args[:2] == ["agent", "wait"]:
    if "--status" in args or "--until" not in args: fail("obsolete --status flag")
    pane = state["panes"].get(args[2])
    if not pane or pane["agent_status"] != arg("--until"): fail("timeout")
    emit({"agent": pane})
if args[:2] == ["integration", "status"]: emit({"integrations": []})
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
if args[:2] == ["pane", "close"]:
    if args[2] not in state["panes"]: fail("pane_not_found", f"pane {args[2]} not found")
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
