#!/usr/bin/env python3
"""A strict fake for documented Herdr 0.8.2 calls. Uses real Git worktrees."""
import json, os, pathlib, subprocess, sys, uuid
root = pathlib.Path(os.environ["FAKE_HERDR_ROOT"])
root.mkdir(exist_ok=True, parents=True)
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
parent = os.environ.get("HERDR_PANE_ID", "w-parent:p1")
state["panes"][parent] = {"pane_id": parent, "cwd": os.environ.get("FAKE_PARENT_CWD", "/tmp"),
  "agent_status": os.environ.get("FAKE_PARENT_STATUS", "idle"), "agent": "test-coordinator"}

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
    pane, workspace = "w-" + uuid.uuid4().hex[:6] + ":p1", "workspace-" + uuid.uuid4().hex[:6]
    state["panes"][pane] = {"pane_id": pane, "cwd": str(path), "agent_status": "unknown", "agent": None}
    response = {"root_pane": {"pane_id": pane}, "workspace": {"workspace_id": workspace},
                "worktree": {"path": str(path), "branch": arg("--branch")}}
    if os.environ.get("FAKE_BAD_WORKTREE"): response["worktree"]["path"] = arg("--cwd")
    emit(response)
if args[:2] == ["workspace", "create"]:
    if "--no-focus" not in args or "--cwd" not in args: fail("wrong workspace contract")
    pane, workspace = "w-" + uuid.uuid4().hex[:6] + ":p1", "workspace-" + uuid.uuid4().hex[:6]
    state["panes"][pane] = {"pane_id": pane, "cwd": arg("--cwd"), "agent_status": "unknown", "agent": None}
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
    if os.environ.get("FAKE_FAIL_PROMPT"): fail("simulated uncertain prompt")
    pane["last_prompt"] = args[3]
    pane["agent_status"] = "working"
    emit({"agent": pane})
if args[:2] == ["agent", "wait"]:
    if "--status" in args or "--until" not in args: fail("obsolete --status flag")
    pane = state["panes"].get(args[2])
    if not pane or pane["agent_status"] != arg("--until"): fail("timeout")
    emit({"agent": pane})
if args[:2] == ["integration", "status"]: emit({"integrations": []})
fail("unsupported fake operation: " + repr(args))
