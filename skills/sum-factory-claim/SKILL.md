---
name: sum-factory-claim
description: Find the next ready GitHub issue for a sum factory lane and claim it with label sum-claimed plus a host and pane comment.
---
# Factory claim

Coordinator only.
Call this after `sumctl factory tick` returns `action: dispatch`, or when the user names the next issue.

## Ready signal

Configured at `factory enable`.
Do not invent another kind.

| kind | Sequential source |
| --- | --- |
| `label` | Open issues with that label, lowest number first |
| `issues` | All open issues, oldest number first |
| `roadmap` | Issue numbers in order from the parent issue body table, first still open |
| `project-status` | GitHub Projects Status option; a missing `read:project` scope is `blocked`, not a guess |

Skip issues listed in `--skip`, issues labeled `sum-claimed` or `sum-gated`, and issues the intake marks as owner-gated.

NiceBaaS uses `--ready issues`.
No ready label and no Projects status.

## Claim

```sh
./bin/sumctl factory claim owner/repo --issue N [--task TASK_ID]
```

The helper adds label `sum-claimed` and an issue comment:

```
<!-- sum-factory-claim -->
host: <hostname>
pane: <HERDR_PANE_ID>
task: <task id or none>
at: <timestamp>
```

That comment is the cross-agent lock.
Do not also assign unless the user asked for an assignee.
After dispatch, run claim again with `--task TASK_ID` so the lane names the worker.

Completion: `factory status` shows the issue in `lanes_held`.
A second claim on another issue is refused at the lane limit.

## Conflicts

If GitHub already has `sum-claimed` from another host or pane, do not steal it.
Tick will skip it.
Relay the existing claim comment to the user.
