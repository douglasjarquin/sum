# Ordinary entry and HC worker recovery

Run `mise run setup`, start your usual coding agent in this directory, and ask for work.
The agent runs `./bin/sumctl init`, retains its claim and reads the validated procedure paths.
Initialization starts or queries no HC core.
A worker harness must be selected explicitly or saved through `settings set --claim TOKEN --worker-harness NAME`.
It can differ from the coordinating harness.

## Default runtime route

Every genuinely new worker or reviewer terminal is created on HC's default route, the one Herdr shows, resolved from the user's XDG homes and `herdr-core/config.toml` rather than from inherited `HC_SOCKET`, `HC_SESSION` or `SUM_SESSION` values.
The first launch pins the permanent core ID it observed there in `.sum/runtime-route.json`; a route an earlier release saved for a private core selects nothing.
A changed core at the pinned socket, another herdr-core version or a core without a current holder is refused; sum starts a core only when nothing listens and never restarts the user's core, and a route failure never silently selects another core.
Existing attempts keep the socket, core and session their own records name.
Template options come from settings, not the coordinator's environment or launch record.
Creation omits HC parent and caller; workers and reviewers outlive the coordinating conversation.
The pinned HC dependency and its release-matched manual define native behavior.
Observation and collection never start a core.

## Inspect before recovery

```sh
./bin/sumctl inspect TASK --role worker --attempt ID
./bin/sumctl inspect TASK --role reviewer --attempt ID
```

Inspection works from an ordinary shell for live, unready and failed attempts.
It returns the saved intent, exact address, bounded output and native attach route without sending input.
A missing endpoint remains ambiguous; inspect and reconcile instead of repeating launch.
Creation and opening delivery have separate durable outcomes.
The endpoint is saved before readiness is awaited, and only a matching role-init receipt establishes native authority.

| Saved outcome | Explicit action with `--claim TOKEN` |
| --- | --- |
| Worker creation reply lost | `execution reconcile TASK --attempt ID` |
| Reviewer creation reply lost | `reviewer reconcile TASK --attempt ID` |
| Herdr label reply lost or visibility pending | The same `execution reconcile` or `reviewer reconcile` reads the exact terminal and republishes the saved labels for the proven original only |
| Created with opening proven unsent | `execution redeliver TASK --attempt ID` or `reviewer redeliver TASK --attempt ID` |
| Worker matching init receipt pending | Inspect, then `execution confirm TASK --attempt ID` |
| Worker proven stopped | `execution park TASK --attempt ID`, then explicit resume |
| Reviewer refusal or exit | Inspect, then `reviewer recover TASK --attempt ID` before explicit launch |
| Submitted or uncertain input | Reconcile exact application evidence; never blindly replay |

Recovery uses the original saved launch route even after workspace settings or claim transfer change.
A stale claim refuses before HC effects.
Original-process, creation-time, candidate and receipt checks remain mandatory.
Native exit, original PID death and absence of owned survivors establish stop; idle status alone releases nothing.

## Trust and native input

Grok worker/reviewer creation records workspace trust in the selected trust file before native creation.
Linked Git worktrees share their common repository trust entry.
An explicit refusal, invalid trust file or failed write prevents creation.
Authentication and permission dialogs remain human actions; no tool approval, account/model switch or global-keymap change is implied.
Qualified input defers working, blocked and unknown recipients and requires its supported native guard.
Native send success, status and turn counters are diagnostics, never task application receipts.
Actual authenticated harness combinations and desktop behavior require separate acceptance evidence.

## Desktop boundary

There is no coordinator placement or parent desktop terminal.
New workers and reviewers appear in Herdr in the installation's Sum space, grouped in one tab per task, once their original program is corroborated; the labels are published after creation, never requested with it, and they grant nothing.
Closing the Herdr window leaves the agents running in the core.
Herdr enables keep on adoption, so a lost program's address survives as a shell: that shell cannot receive the opening, be adopted again or inherit the original worker's receipt authority, and a stop proof still needs the original program's conclusive exit.
Closing a desktop pane ends its terminal.
Use worker/reviewer inspection's exact attach route for an attempt whose visibility is still pending or uncertain.
Do not interact with terminals this task does not own.

## Preserved installations

Schema-one state stays untouched.
Keep its processes, worktrees and records intact, use `sumctl history` for saved reads, and prepare an independent fresh clone with `mise run setup`.
A supported native installation uses the explicit one-time retirement procedure in [update](update.md).
That root-format fence preserves task/native-owner/sealed-procedure bytes and is not universal OS/Git exclusion.
Development fixtures do not authorize a live cutover.
