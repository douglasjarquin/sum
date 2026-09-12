# Real-host acceptance

Do these in a throwaway repository and an isolated Herdr session before trusting a large task.
The offline demo is not a substitute for this check.

## 1. Bootstrap

Run setup twice. Both runs should succeed without replacing unrelated MCP entries or touching global harness/account settings. The setup MCP handshake should discover ten tools.
Run `sumctl init` inside the intended Herdr pane and confirm it reports `coordinator`; `sumctl doctor` is observation only. Confirm the expected session, pinned version, and selected harness executable. Authenticate that harness and GitHub yourself.

## 2. First task

Create a small Git repository with a trivial function and a failing unit test. Launch a harness from the sum directory and say:

> Delegate this fix to [the chosen harness]. Work in a separate checkout, run the test, and report the candidate SHA. Do not create a PR or merge anything. Before implementing, ask me whether to preserve the existing public function name.

Confirm a distinct worktree/branch, a saved question, and the actual primary checkout remaining unchanged.
Answer the question, confirm it remains visible until applied, and inspect the final report and diff.
Record whether MCP or CLI was used, harness version, Herdr version, and any trust prompts encountered.

## 3. Busy or closed coordinator

Repeat while the coordinator is occupied, then while it is closed. The question must remain in `sumctl inbox`; prompt delivery may remain pending.
Reopen the coordinator, run `sumctl init --role coordinator --reclaim` once the old pane is verifiably gone, bind the task's parent to the new pane, and perform a rundown. There must be no duplicate worker and no invented answer.

## 4. Non-cooperative worker

Ask a worker deliberately to print a question without calling `sumctl ask`. Run a rundown. The coordinator should inspect the idle/blocked worker and save the question.
This measures the attended fallback. Instant unattended capture is NOT a passing criterion claimed by this MVP.
With `sumctl hook enable` active, also record whether the worker's idle edge produced an `attention` record with a useful excerpt and how long after the pane settled it appeared; label by hand whether the excerpt contained the question. Report unsupported harness cases (no Herdr status, alternate-screen output) explicitly.

## 5. Uncertain startup

Use a fresh harness trust/authentication prompt. A failed startup must preserve the task and pane, report uncertainty, and refuse an automatic second start. Inspect and resolve the actual prompt rather than creating another agent.

## 6. Cross-harness contract

Repeat the same small task with a different worker harness and, separately, a different coordinator. Compare question capture, result reporting, trust behavior, and startup instructions. Do not label every harness supported merely because two executables launch.

## 7. Backup

Create a records backup. Inspect its manifest: worktree code is explicitly excluded. Extract into a new directory, inspect records via `sumctl --home`, and verify that another machine's bindings are not automatically used.

## 8. Fleet capacity and rolling update

Raise the lab installation to ten or more slots with `sumctl settings set --global 12 --per-repository 1` and dispatch that many tiny approved tasks across throwaway repositories with the authenticated harnesses in use.
Confirm a dispatch beyond the limit creates no worktree and a second task into an occupied repository is refused even with global room.
A reported task must still hold its slot while the worker or an owned service remains active.
Read its attempt ID with `execution show TASK_ID`, wait for the worker to exit, then run `execution park TASK_ID --attempt ID`.
Confirm its slot is free while its questions, report, and checkout remain available.
Resume approved work with `execution resume TASK_ID --attempt ID`, and confirm the old ID cannot release the successor.
With all slots occupied, confirm resume and `verify --execute` refuse before launching anything.
Confirm idle, missing, and unobservable workers remain reserved rather than permitting duplicate execution.
Then follow the fleet canary in `skills/sum-update/SKILL.md`: update, refresh, answer and report through old callbacks, roll back, refresh again.
Record each worker's observed state, the `fanout` counts and wall time of every pass, the receipts that appeared and when, and every worker that kept its process, checkout, and partial work.
Scripted workers in `scripts/demo.py` establish the bookkeeping; only this step says anything about a model acting on a refresh.

For controlled repairs, send two approved corrections to a settled lab worker with `repair send TASK_ID --attempt ID --key KEY --file FILE`, using distinct keys.
Confirm the third correction and an execution resume are refused with one budget question and no native launch or prompt.
Change the candidate, refresh the brief, and update or roll back the lab runtime; confirm the allowance remains exhausted.
Only after an actual human decision, record `repair extend TASK_ID --question ID --additional 1 --approved --file FILE` and confirm exactly one additional correction is permitted.
Confirm an ordinary answer, worker report, and repeated grant do not add iterations.
These steps require a real authorized lab and remain unrun until an operator records them; offline fixtures do not certify a model's internal loop.

## 9. Task-owned development services

In a real task whose repository declares a dev server, have the worker run `sumctl env discover`, then `sumctl env start TASK_ID --command dev --url http://127.0.0.1:PORT`, and confirm in the Herdr UI that a pane split under the worker pane runs the repository's own command while the worker keeps working.
Start a second task in another repository the same way on another port, and keep a shared database (or any listener started by hand) running; record it with `--ownership shared`.
Then: start the first command again (expect `already_running`, no second pane), try a URL whose port is taken (expect a recorded `conflict` and no termination), restart the service by hand inside its pane (expect `env stop` to refuse it as not the recorded instance), and let one service ignore the interrupt (expect `stopping` after the bound, no escalation).
Merge the first task's PR and run `cleanup TASK_ID` then `--apply`: the proven service stops, its pane closes, the checkout is removed, and the second task's server, the shared database, and the coordinator pane are untouched; run `--apply` again and confirm it is a no-op.
Run `update apply` and `refresh request` while a service serves requests and confirm the process, pane, and port are unchanged.

## 10. Native metadata in the real sidebar

With two real tasks running, run `sumctl metadata enable`, merge the printed `metadata snippet` rows into your own `config.toml`, and `herdr server reload-config`.
Confirm that a worker asking through `sumctl ask` shows `needs-decision` beside its `working`/`idle` icon within one helper command, that answering and `resolve` return it to `running`, that a report shows `review-ready` and a merged PR `merged-cleanup-pending` until cleanup, and that `refresh request` shows `instruction-refresh-pending` with `r1>r2` until the worker adopts.
Rename a worker pane by hand and give a workspace your own label; confirm both survive every transition and `metadata disable`.
Enable `--notify` with `[ui.toast] delivery = "herdr"` and confirm one toast per transition naming only task id, state, and repository, none for duplicate events or unchanged rundowns, and none carrying question text.
Run `metadata inbox` and confirm the popup shows the ordinary `sumctl inbox` JSON and closes on Enter.
Record how often you asked the coordinator for a status explanation before and after; the intended result is fewer such turns, not a prettier sidebar.

## 11. Managed project clones and explicit skill delivery

From the coordinator pane, run `./bin/sumctl project enroll <owner>/<repo>` for one repository you own and confirm exactly that repository appears under `projects/<owner>/<repo>`, that `git status --ignored` in the installation lists it as ignored and `git ls-files` does not, and that a second enroll returns `already-enrolled` without a fetch. Enroll a same-named repository of another owner and one on a non-default host with `--host`; confirm three distinct paths.
Enroll one repository you already cloned elsewhere with `--path`, and one left under `.sum/projects/<owner>/<repo>` by the earlier procedure with a worker still running in it; confirm both are registered where they are (`external`, `legacy`), that `project migrate` names the running task as a blocker, and that `--apply` is refused while it runs.
Dispatch a real task with `--project`, open its brief, and confirm the `## Delivered runtime` section names `<installation>/bin/sumctl` and the absolute skill path with the hash of the installed file; confirm the worker's `context --role worker` shows the same and that the worktree is outside the installation.
Start a harness by hand inside the managed clone and run the installation's `bin/sumctl init` from there; it must refuse with "A project session is not a sum session" and leave `.sum/context.json` unchanged. Then, in that clone, run `mise tasks ls` and confirm sum's `test`/`demo` tasks resolve from the parent; run `sumctl env discover` for a task and confirm `task_origins` flags them as inherited rather than as project verification.
Enroll `douglasjarquin/sum` itself and confirm the registration points at the installation with nothing cloned. Stage a release and confirm the bundle contains no `projects/` entry; take a records backup and confirm `state/projects.json` is present and no clone file is.

In a throwaway Git project and temporary `HOME`, run `sumctl skills install` against a local fixture with one explicitly selected skill and agent.
Confirm the pinned Vercel Skills CLI copies the selected directory into the project, creates no skill symlink, and writes no user-level skill directory.
Request a missing skill and confirm the command fails without reporting success, then request a `sum-*` name and confirm Sum refuses it before the upstream CLI runs.
List a real source's skills read-only (`.local/bin/skills add michael-denyer/pstack-claude --list`) and confirm nothing is copied and no selection is made; listing is inspection, not an install decision.
Copy one real skill from a real public source (`vercel-labs/agent-skills`, skill `writing-guidelines`, agent `claude-code`) into a throwaway Git project with `sumctl skills install`; confirm the resulting Git diff is exactly one project-local `SKILL.md` plus `skills-lock.json`, review it as untrusted content per `docs/sum-skills.md`, commit it, and open an ordinary PR for it.
Re-run the identical `sumctl skills install` command and confirm the diff is empty (an unchanged selection); then copy a second explicit skill into the same project and confirm the diff adds only that skill's files and a `skills-lock.json` entry, leaving the first skill's files untouched.
Run the pinned Vercel Skills CLI's own `update -p -y` directly (not through `sumctl`) against that project and confirm it does not preserve the `--agent claude-code` copy made above; do not rely on `update` for a refresh, and re-run the explicit `sumctl skills install` command instead.

## 12. Code graph per checkout with a real harness

After `mise run setup` (or a staged release) on the pinned tools, confirm `./bin/sumctl doctor` lists `codegraph` as available at version 1.5.0 under the runtime's `.local/bin`, and that no global `codegraph install` was run (`~/.claude.json`, `~/.codex/config.toml`, `~/.cursor/mcp.json` unchanged).
Dispatch a real task into a repository with a few source files; confirm the task record's `graph.state` is `ready`, `.codegraph/` exists only in the task worktree (not in the clone), `git status` there is clean, and the brief's `## Code graph` section names the runtime binary and the checkout path.
In the worker pane, run the brief's `explore` and `query` commands, then commit an edit and run `query` for the new symbol before and after the brief's `sync` command; confirm the symbol appears only after the sync and that `graph status TASK_ID` reported `stale` with a moved HEAD in between.
Dispatch a second task into the same repository (raise `--per-repository` first) with a different implementation of one symbol; confirm each worker's query returns its own implementation and `status --json` for the clone reports `initialized: false`.
Run `./bin/sumctl verify TASK_ID --candidate SHA --execute` and confirm the evidence record's `graph.state` is `ready` with an index path under `.sum/tasks/TASK_ID/verification/` that no longer exists afterwards; run `dev prepare --name canary` and confirm its `graph.state`.
Optionally print `./bin/sumctl graph config --harness <yours>`, merge it by hand into the task checkout's local MCP file, restart the harness in that pane, and confirm the graph tools answer for that checkout; confirm cleanup after the merge reports the `codegraph serve` process as an occupant until the session exits, and that the archived task's records keep `graph.json` while the removed checkout took its index with it.
Record init and query wall times from `graph.json` attempts and your shell; upstream's published speedups are not evidence for this host.

## 13. Grok Bot live canary

This section is unrun until an operator records it.
The files under `templates/grok-bot/` are the source-controlled static half.
They are not this canary.

Install from the real native template at https://x.ai/bot/__4FfrkUdvpdMk6-LKg5r.
Do not publish a Bot from this repository.
Fill placeholders for instance, Bot IDs, project, and task from the live install.
Record the installed helper SHA in the operator report.
The source-controlled pin `0.1.0` is not that SHA.

Enroll one scratch project with `./bin/sumctl project enroll owner/repo` for a repository you control.
Dispatch one approved task.
Save a question with `sumctl ask` using a stable key.
Record the user's answer with `sumctl answer` by question ID.
Confirm the worker submits with `sumctl report`.
Run worker verification, then a distinct coordinator `sumctl verify --execute` or `sumctl verify --run`.
Reading worker logs is not the second run.
The user merges if anything is published.
The Bot never merges.

## 14. Codex Remainder quota canary

This section is unrun until an operator records it.
Offline fixtures do not certify a live Codex account.

After `mise run setup` (or a staged release) on a supported platform, confirm `./bin/sumctl doctor` lists `remainder` with `ok` true at the runtime `.local/bin/remainder` link.
Confirm `sumctl quota --provider codex` prints Remainder TOON and exits 0 when the selected Codex account is usable, including a remaining value of 0.
Confirm `sumctl quota --provider codex --format json` prints Remainder JSON and does not wrap it in a sum envelope.
Confirm `sumctl quota --provider codex --format compact` still prints Remainder compact.
Confirm a missing Remainder binary refuses Codex quota with a clear error and does not invoke quota-axi.
Confirm `sumctl quota --provider claude` still runs quota-axi and does not invoke Remainder.
Do not switch accounts, stop workers, or rank providers from this output.
Record the Remainder version, platform archive, and whether remaining, exhausted, or unavailable was observed.

## 15. Remote machine returns

This section is unrun until an operator records it.
Offline fixtures do not certify a real SSH host.

Sum does not enroll SSH hosts.
Herdr `machine add` does.
Follow [Connecting machines](https://herdr.dev/docs/connecting-machines/).
Do not add `sumctl machine` or a Sum SSH daemon.

On a host with Herdr 0.9.0 and SSH to a second machine, run `herdr machine add` for that host.
Keep the coordinator on Local.
Confirm a task whose recorded `machine` is the remote hostname is ignored by the local `hook event` pump.
Confirm a local task can still `sumctl ask` and appear in records-only `sumctl inbox`.
Confirm local rundown (`inbox --live`, `init`, and `bind --parent-only`) remains the degrade path when the remote Herdr session is disconnected.
A real SSH canary is unrun if no second host is available.

## Record results

For each real task, record all human-labeled questions, which were saved by workers, which were found during rundown, which were missed, and time until attention. Also record unnecessary attention items and manual pane inspections.
A useful result is fewer manual inspections and no forgotten decisions in the tested cases, not merely a successful command exit.
