# HC startup and recovery

A fresh installation starts with `mise run setup`, followed by `./bin/sumctl coordinator start --harness grok` for a selected Grok coordinator, or the explicitly chosen supported terminal profile.
Sum records the selected HC socket, session and XDG configuration/state route in `.sum/runtime-route.json`.
Explicit `--socket`, `--session` and `--hc-config-home` options override selection; a selected-session failure never falls back to the default core.
Startup checks the selected live core against published HC 0.1.14, protocol generation 1 and the operation's required capabilities.
It never silently downgrades a shared desktop core.
A newer shared core needs independent compatibility qualification before use with this release.
Read-only `doctor`, `inspect`, `history` and status commands never prepare a core.

`./bin/sumctl build-info` identifies the actual executable, checksum and available embedded source provenance.
The checkout revision and selected release describe source selection; they do not identify an unverifiable older helper.
Setup preserves a stale or unverifiable existing helper and refuses with a staged-update instruction.
Use the installed helper's `release stage HEAD`, inspect the staged release, then use the existing explicitly authorized update procedure.
An update never relocates managed projects from the stable installation root into a release generation.
`project list` warns about preserved projects already enrolled under a release; inspect and migrate them explicitly while retaining dirty or active work.

## Inspect before recovery

`./bin/sumctl inspect` reads the saved coordinator attempt and its output from an ordinary shell, including before init or native status.
Use `inspect TASK --role worker` or `inspect TASK --role reviewer` for a task attempt.
`--attempt ID` guards against inspecting a different successor.
The result includes an exact native attach argv and socket/session environment.
A missing endpoint remains ambiguous; inspect the saved intent and use the corresponding reconcile command rather than repeating launch.
No inspection creates a terminal, grants authority or sends an opening.

Creation and opening delivery are separate.
New namespaced templates have an empty body; the approved installed-helper/procedure/brief pointer is saved in the launch record before creation.
The endpoint is saved before readiness is awaited.
Native submission is diagnostic; only the matching role init receipt establishes authority.
Before creating a Grok worker, reviewer or coordinator terminal, Sum records trust for its workspace in the selected `GROK_HOME/trusted_folders.toml`.
Git worktrees share the common repository's trust entry; a workspace outside Git uses its own resolved directory.
Concurrent Sum launches serialize trust writes so they cannot append duplicate entries.
An explicit `trusted = false`, invalid trust file or failed write refuses creation and records a not-submitted attempt.
This grants workspace trust for repository configuration, MCP, LSP and hooks; it does not enable Grok's tool auto-approval or bypass permission mode.
Authentication remains a human action.
Existing terminals launched by an older release keep their original process and pending prompt; installing this change does not answer or relaunch them.

| Saved outcome | Explicit next action |
| --- | --- |
| Worker creation response lost | `execution reconcile TASK --attempt ID`; unprovable candidates remain ambiguous |
| Coordinator creation response lost | `coordinator reconcile --attempt ID` from an ordinary shell |
| Reviewer creation response lost | `reviewer reconcile TASK --attempt ID` |
| Created, opening proven unsent | `execution redeliver TASK --attempt ID`, `coordinator redeliver --attempt ID`, or `reviewer redeliver TASK --attempt ID` |
| Worker init receipt pending | Inspect, then `execution confirm TASK --attempt ID` after the matching init |
| Proven no-effect refusal | Correct the prerequisite; `execution park TASK --attempt ID` then resume the worker, `coordinator recover --attempt ID`, or `reviewer recover TASK --attempt ID` then explicitly launch |
| Original process exited before init | Inspect exit/output, then `execution park TASK --attempt ID`, `coordinator recover --attempt ID`, or `reviewer recover TASK --attempt ID`; native exit, original PID death and checkout survivor evidence must prove stop |
| Send submitted or ambiguous | Inspect and reconcile the application receipt; never blindly replay |
| Program replaced by a recovery shell | Stop using old authority; inspect and perform an explicit new bootstrap or verified rebind |

## Desktop and input compatibility

For a running associated desktop, pass the existing space ID with `coordinator start --space SPACE`.
Placement is requested only after original process evidence is saved; it is separate from launch and dispatch authority.
Confirm the selected core and space in the actual app before treating placement as visible.
Workers and reviewers remain linked to that root and are always reachable through `inspect`, including unready or finished attempts.
If the app is unavailable, use the native read/attach route; no desktop visibility is claimed.
Closing the desktop window preserves programs; closing a pane/tab can end its terminal.
Keep-as-tab changes presentation and recovery policy, not an unchanged agent's authority.
A recovery shell or changed PID/foreground still fails the original receipt/process checks.
Promotion before any original foreground observation cannot establish the original process; inspect without sending when that evidence is unavailable.

Running-recipient qualification preserves immutable launch/template provenance while checking current input semantics.
Comments, formatting and unrelated configuration bytes, or a replaced executable on disk, do not identify replacement of an already-running program.
New launches separately qualify the executable actually selected.
A changed submit key, disabled native status or other unsupported input semantics is refused.
Codex 0.166.0 uses Tab; Grok Build stable 1.0.67 or newer, Claude Code 2.1.316 and Devin 3000.11.234 use Enter with default input settings.
Actual authenticated qualification, launcher/shim layouts, desktop interaction and compatible update acceptance must be recorded for the selected combination; offline fixtures do not prove them.

## Independent fresh start for legacy installations

Setup detects unsupported/legacy state before dependency staging or configuration retirement and preserves the original bytes.
There is no production live legacy migration command.
Use the old runtime for old active work and `./bin/sumctl history` for read-only preserved task records.
Clone `https://github.com/douglasjarquin/sum.git` into a separate directory, then launch setup there with old identity removed only from that child environment:

```sh
env -u SUM_HOME -u SUM_ROLE -u SUM_TASK -u SUM_ATTEMPT -u SUM_SESSION \
  -u SUM_CORE_ID -u SUM_BINDING_GENERATION -u SUM_INSTALL_ROOT \
  -u HC_SOCKET -u HC_SESSION -u HC_TERMINAL_ID mise run setup
```

Select the intended core/session for the fresh instance explicitly when it differs from native defaults.
Keep the old installation, processes, task worktrees and records intact.
A historical reader or records restore never grants new runtime authority.
