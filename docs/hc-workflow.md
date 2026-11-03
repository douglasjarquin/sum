# Ordinary entry and HC worker recovery

Run `mise run setup`, start your usual coding agent in this directory, and ask for work.
The agent runs `./bin/sumctl init`, retains its claim and reads the validated procedure paths.
Initialization starts or queries no HC core.
A worker harness must be selected explicitly or saved through `settings set --claim TOKEN --worker-harness NAME`.
It can differ from the coordinating harness.

## Workspace runtime route

The first worker/reviewer launch ensures the workspace route lazily and saves socket, session, config/state homes and permanent core ID in `.sum/runtime-route.json`.
A changed core at the same socket is refused; a route failure never silently selects another core.
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
Use worker/reviewer inspection's exact attach route; no desktop visibility follows from a label or successful native launch.
Closing a desktop window and closing a pane can have different process effects.
A recovery shell or changed process cannot inherit the original worker's receipt authority.
Do not interact with terminals this task does not own.

## Preserved installations

Schema-one state stays untouched.
Keep its processes, worktrees and records intact, use `sumctl history` for saved reads, and prepare an independent fresh clone with `mise run setup`.
A supported native installation uses the explicit one-time retirement procedure in [update](update.md).
That root-format fence preserves task/native-owner/sealed-procedure bytes and is not universal OS/Git exclusion.
Development fixtures do not authorize a live cutover.
