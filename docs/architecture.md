# Architecture

Roles, state, and what ships in the distro. Moved out of the README.

### Session roles

Every harness session starts with `./bin/sumctl init` and follows the role it returns. The first pane in the installation (the checkout where setup ran) claims `coordinator` atomically; exactly one pane wins a simultaneous start. A pane dispatched by the coordinator is registered as that task's `worker` and stays a worker even when its task is editing sum itself. Any other pane, including a second harness you open in the same directory to work on sum, becomes a `developer`: it sees who owns coordination, cannot dispatch or rebind tasks, and can only observe through the Herdr bridge. Identity is the verified machine, Herdr session, and pane, never the working directory. The machine is a stable identity hashed from the operating system's machine ID (`/etc/machine-id`, or the platform UUID on macOS), not the hostname, so renaming the host changes no role, registration, or route; see [Recovery](recovery.md#machine-identity). `sumctl doctor` only observes; it never binds. Role bookkeeping prevents accidental takeover; it is not a security sandbox against code running as your user.

### No installed harness?

```sh
mise run setup -- --install-codex
mise exec -- codex             # authenticate using the normal provider flow
```

This optional path installs a pinned Codex CLI inside `.deps/harnesses`, not globally. Other harnesses are deliberately bring-your-own: no subscription, API key, or account is created or switched by setup.

**New local executable caveat:** worker shells must be able to resolve the selected harness too. If you just installed a local harness after starting Herdr, use a fresh named session launched from the configured environment (`mise exec -- herdr --session sum`) and verify the executable in that session. Do not restart an existing busy server just to refresh its environment. Already-installed host harnesses are the ordinary path.

Herdr's optional native integrations can be installed separately, for example `herdr integration install codex`. Those modify the harness's own configuration, so sum setup does not install them implicitly.

## What's included

| Component | Purpose |
| --- | --- |
| `AGENTS.md` and harness instruction aliases | A short coordinator contract, with a separate worker role |
| Plain-language dictionary | [docs/terminology.md](terminology.md): user, coordinator, agent, reviewer, task, brief, question, inbox, result |
| Six bundled skills | Dispatch, worker execution, verification/PR delivery, rundown/recovery, isolated self-development, and atomic updates with rolling session refresh and code-only rollback |
| Project-local third-party skills | `bin/sumctl skills install` delegates explicit skill and agent selections to the pinned Vercel Skills CLI in project copy mode; see `docs/sum-skills.md` |
| Evidence publication into the PR | On by default: `pr reconcile` runs `.agents/skills/evidence/scripts/evidence_publish.py` for every evidence run the worker's handoff lists, and `sumctl pr evidence` repeats it on demand. Validated before/after media (hashes, containment, size, type, secret checks) is uploaded through `gh pr edit --attach` (GitHub CLI 2.99+) into one marked block of the reconciled PR; receipts per content hash and destination so a second publish uploads nothing; prose outside the block preserved; a foreign or hand-edited block refused; every failure leaves the previous body and the local originals intact; an older `gh` defers. `"evidence": {"auto_publish": false}` in `.sum/settings.json` turns the automatic step off |
| Delivery pipeline in the PR | Nine ordered gates (Intent, Rebase, Review, Test, Document, Lint, Push, PR, CI) derived from the task's own records and rendered as a status table in one marked `<!-- sum-pipeline:* -->` block of the reconciled PR, beside the evidence block. All nine run: Rebase observes the candidate against its base branch without ever rewriting it, Lint runs whatever lint task the project itself declares, Push fast-forward pushes the reported candidate and nothing else, and CI reads the checks GitHub reports on the PR. CI is observed, never watched, because sum runs no daemon and polls nothing: the checks are read at `pr reconcile` and on demand with `sumctl pipeline ci`, and the row is stamped with when the checks last changed, so a green row means green as of the time shown and a re-read that finds nothing moved edits nothing. A feature-map row whose Evidence cell names a screenshot, screencast, or red/green pair blocks Test until a comparison for that candidate exists, so user-visible work cannot reach origin unproven; the worker captures it with `.agents/skills/evidence/`, or the user waives it and the coordinator records that decision with `verify --accept-missing-evidence`. `sumctl pipeline run` drives the coordinator's gates in order through opening the PR and reconciling it, and stops at the first failure or a blocked Test: it reads GitHub for a pull request on the branch first, so a second run creates nothing, and `--no-pr` stops after Push. `show/refresh/rebase/lint/push/document/pr/ci/publish` do the gates one at a time, and `pr reconcile` publishes the table under the same `auto_publish` switch as the evidence block. The table records what has run, never that a change may merge |
| Portable verification contract | A project-root `VERIFY.md`, the canonical `mise run verify` aggregate, feature maps under `docs/features/`, and the distributable `.agents/skills/verify` runner that records candidate-bound run evidence under Git-ignored `.artifacts/verification/`; works in any clone without sum or Herdr |
| Verification authoring skills | `.agents/skills/create-verification` inspects a repository, scaffolds its contract, `verify` task, seed feature maps, and vendored skills without inventing commands, then proves them once; `.agents/skills/maintain-verification` audits stale tasks, paths, links, and coverage claims, names the maps a change touches, and records a no-map-change rationale; both keep authored map edits separate from run results and work in any clone |
| Release-matched Herdr skill | Copied from the installed `herdr --skill` during setup |
| Pinned Herdr Mesh plus a small runtime overlay | Ten relevant MCP tools, current Herdr commands, bounded reads/waits, no swallowed handoff errors |
| `bin/sumctl` | Durable task/decision/report files, native worktree creation and launch, bounded notices, guarded cleanup of merged task workspaces, records backup, staged releases, atomic update/rollback, and per-session refresh bookkeeping |
| Bounded helper runner (`go/internal/proc`) | One implementation for owned helper commands (git, gh, herdr, codegraph, ps, lsof): the tighter of the caller's deadline and the command timeout, stdout and stderr capped while read, overflowed or incomplete structured output rejected, and a timeout, cancel, or descendant still holding the output reported as an unknown effect (never success, never retried, descendants never signaled). Project verification, lint, the evidence publisher, and interactive harness launches keep their own lifecycle owners; every other direct process start is listed with its reason in `go/internal/proc/inventory_test.go` |
| Optional Herdr plugin (`sumctl hook`) | A per-installation manifest under `.sum/hook` whose event handler runs the same bounded pump and records native attention, plus a read-only inbox pane entrypoint; no daemon |
| Optional native metadata (`sumctl metadata`) | sum task state (needs-decision, review-ready, merged-cleanup-pending, instruction-refresh-pending, ...) projected as namespaced `sum_*` tokens on the endpoints sum owns, rendered by sidebar rows the user chooses to add; opt-in coalesced notifications |
| Remainder (Codex TOON) and `quota-axi` (other providers) | Advisory quota evidence through `sumctl quota --provider NAME`; no automatic billing or account switching |
| Allowlisted LSP binaries | `pipx:basedpyright` 1.40.1 and `go:golang.org/x/tools/gopls` 0.23.0 from mise; `mise run setup` links them into `.local/bin` once; project Grok, Cursor, and Codex PostToolUse hooks run `bin/lsp-ensure` (fail-open, empty stdout) which calls `sumctl lsp ensure` for those missing binaries only; existing links are never rewritten |
| Pinned codegraph per checkout (`sumctl graph`) | `@colbymchenry/codegraph` 1.5.0 from the mise pin, run in CLI mode once in every checkout sum creates (task, coordinator verification, self-development) after its Git identity is validated; one `.codegraph/` index local to that checkout, kept out of `git status` through the repository-local exclude; exact commands and the source fallback in the brief; bounded retries, bounded concurrency, no daemon, no global configuration |
| Offline tests and a demo | Test behavior without model credentials, a real Herdr installation, or GitHub writes |
| Explicit live smoke test | Validate the real Herdr API in an isolated named session |
| Grok Bot packs | Native recipes under [`grok-bots/sum/`](../grok-bots/sum/) (coordinator) and [`grok-bots/square/`](../grok-bots/square/) (backup agent). Install with [`GROK_SUM.md`](../GROK_SUM.md) and [`GROK_SQUARE.md`](../GROK_SQUARE.md). Index: [`grok-bots/README.md`](../grok-bots/README.md) |

The helper is called `sumctl` to avoid shadowing the Unix `sum` command. Normally you talk to the harness, not the helper.

## State and communication

Private state lives in `.sum/` and is ignored by Git. `.sum/context.json` records the coordinator owner and `.sum/sessions/` the registered panes and roles. Optional `.sum/preferences.md` and `.sum/projects.md` hold local preferences and project notes; `.sum/projects.json` is the registry of enrolled projects (host/owner/repo, verified remote, canonical path, enrollment metadata), while their clones live outside the state under the Git-ignored `projects/` directory. `.sum/coordinator/` holds the coordinator's contract revisions and refresh receipts. `.sum/machine.json` maps each host's stable identity to the hostnames it was seen using, so records written before that identity existed keep resolving; it is not part of a backup. Each task stores its brief, base SHA, branch/worktree, pane bindings, questions, answers, report, an append-only `evidence` list (reports, structured handoffs, reviewer findings, coordinator verification, exact PR observations), the bound reviewer endpoint, the last reconciled PR identity, a `versions.json` sidecar with brief revisions, and an optional `environment.json` sidecar with the task-local environment record. File updates are locked and atomically replaced on one local machine.

Workers use the exact commands in their generated brief. The core interaction is:

```sh
./bin/sumctl inbox --live
./bin/sumctl show TASK_ID
./bin/sumctl answer TASK_ID QUESTION_ID --text 'Keep both endpoints for one release.'
```

`show` returns the whole record and keeps its shape. `context` is the bounded read beside it:

```sh
./bin/sumctl context TASK_ID                              # outline: counts, outstanding decisions, latest handoff, cursor
./bin/sumctl context TASK_ID --role worker|reviewer|coordinator   # the sections and short contract that role needs
./bin/sumctl context TASK_ID --section decisions --after 20 --limit 20   # stable pages with total/omitted/next_after
./bin/sumctl context TASK_ID --section brief --revision r1 # a recorded brief revision, hash-verified, never rewritten
./bin/sumctl context TASK_ID --since CURSOR               # what changed since the `cursor` of an earlier read
./bin/sumctl notes TASK_ID --text 'finding'               # the one optional task-local notes.md (a claim; secrets refused)
./bin/sumctl help [TOPIC]                                  # command discovery without the full manual
```

Sections: `outline`, `brief`, `decisions`, `handoff`, `evidence`, `execution`, `environment`, `update`, `returns`, `notes`.


Prose fields are bounded (`--max-chars`, 0 for all) and credential-shaped text is redacted in the view, never in the record. Outstanding decisions are listed in full on every read regardless of paging. Worker prose, handoffs, and notes are labelled claims; only `verify` and `pr reconcile` records are verification evidence. Skills reach agents as explicit file references (path, size, hash) to read when needed, not as an assumed skill standard. Nothing here calls a model or reads a worker's checkout.

A question is saved **before** notification. A notification is attempted only after an idle/done preflight; a busy, absent, blocked, or unverifiable recipient leaves it pending. A successful send means *submitted, not acknowledged*. Saved answers stay visible until the worker marks them applied, or until the coordinator closes one whose worker attempt is already released with `answer TASK QUESTION --close --reason TEXT`; that records `closed-unapplied` with who, when, and why, never `applied`.

Every pending return is derived from the records themselves: an open question and an unverified report are owed to the parent, an unapplied answer and a requested brief revision to the worker. Nothing has to be acknowledged for it to stay listed, and nothing but a later record (an answer, `resolve`, `answer --close`, `verify`, a PR observation, `brief adopt`) closes it. The per-task `returns.json` sidecar keeps only notification state, so a legacy helper rewriting `task.json` cannot erase it.
Each task write, `inbox --live`, a coordinator `init`, `bind`, and the explicit `sumctl pump` run one synchronous pass: open returns are grouped by their current recipient identity and each recipient gets at most one fixed notice naming the record IDs and commands (never question or report prose). A pending return is sent once; a known failure (busy, absent, wrong checkout, not registered) is retried on later passes up to three times and then shows `stalled`; a timeout after a possible submission or an interrupted pass stays `uncertain` and is never re-sent by itself. `sumctl notice TASK_ID --to parent|worker` is the explicit single retry for those. `show` and `inbox` carry the `returns` view; the old single `notice` field mirrors the latest attempt for existing readers.

There is no retry loop while you are away. Run a rundown to find pending returns and workers that stopped without a report. Native `idle`/`done` is not task completion, and a worker's report is not verified success.



To create a task manually:

```sh
cp templates/task.md /tmp/my-task.md
# Edit the brief, then from inside a Herdr pane:
./bin/sumctl dispatch --repo /absolute/path/to/repo \
  --brief /tmp/my-task.md --harness codex --approved
```

`--approved` records the caller's assertion of approval; it is not a security boundary. `prepare` creates the record/worktree without launching; `start TASK_ID` starts that prepared task once. An uncertain launch is retained for inspection and cannot simply be started again.
