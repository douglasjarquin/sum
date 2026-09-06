---
name: sum-delivery
description: Independently inspect worker results, use existing verification tooling, and prepare a reviewable PR without autonomous merging.
---
# Delivery

A worker report is a claim. Read the saved task, its report, relevant changes, and outstanding decisions.
`sumctl show TASK_ID` carries `evidence_view`: every report, handoff, reviewer finding, verification, and PR observation, each bound to a candidate SHA and marked `current` against the checkout HEAD, plus `closure.missing`, the prerequisites still absent.
Worker claims (`source: worker`), reviewer findings (`reviewer`), your own verification (`coordinator`), and GitHub observations (`github`) stay separate records. A legacy prose report appears as unstructured evidence.
`sumctl context TASK_ID --role reviewer` is the bounded handoff for a reviewer you launch: the approved task, the current candidate with its latest handoff, paged evidence, and the delivery skill as an explicit file reference; worker-supplied artifact paths are classified by scope, never opened. `--section evidence --kind review --after N` pages further records; counts and `next_after` say what a page left out.

## Verify

Confirm the checkout, branch, candidate SHA, and actual diff. Confirm the reported verification commands and evidence.
Use the repo's configured MADE/No Mistakes route when available. Otherwise arrange a fresh-context review through a separately launched reviewer or the harness's supported independent-review facility.
When you launch that reviewer yourself, `./bin/sumctl settings show` may name a saved reviewer preset (`reviewer.preset`); `./bin/sumctl preset show NAME` gives the exact harness and argv to start it with. It is a launch shortcut only: it applies solely to a reviewer sum is responsible for starting, never wraps or overrides MADE or the repository's own verification tool, and its absence changes nothing.
The reviewer receives the task contract and candidate, not an instruction to rubber-stamp the worker's summary. Do not have simultaneous writers in the checkout.
If a separate reviewer is unavailable, say independent review was not performed; do not label the result fully reviewed.
Check `sumctl brief list TASK_ID`: the report is bound to a brief revision, and `verification_policy_changed_since` means a later revision changed the worker procedure or brief schema. Review under the current policy before accepting that evidence.
Tie the review to the exact candidate SHA. Re-run required checks for later candidates; review the intervening changes rather than treating the previous SHA's approval as current.
A nit is not automatically a blocker. Limit repair cycles; escalate repeated failure rather than opening an endless review/fix loop.
A reviewer pane saves its findings with `sumctl review TASK_ID --verdict approve|changes-requested|blocked|comment --candidate SHA --file findings.md`; the first such pane becomes the task's recorded reviewer endpoint, and saved findings are the prerequisite for closing that pane later. The worker pane cannot review its own candidate.
Record what you verified yourself with `sumctl verify TASK_ID --candidate SHA --result pass|fail|inconclusive --text '...'`. A newer candidate marks earlier records as not current; it deletes nothing and restarts nothing.

Repository test scripts execute candidate-controlled code. Keep normal sandbox/credential protections. This MVP does not provide a credential-isolated verification runner; use trusted projects and their existing CI/dev container.

## Publish a PR

PR publication belongs to the coordinator by default. Do not merge.
Before any external action, verify `gh auth status` and the exact remote repository. Never change accounts or credentials automatically.
Push the recorded task branch without force. Check for an existing PR for that exact head/base using `gh pr list --state all` before `gh pr create`.
If creation times out, inspect GitHub before retrying; a timeout does not prove the PR was not created. No exactly-once publication claim is made by this MVP.
A closed/merged PR is not permission to create another automatically. Ask when the intended next action is unclear.
Describe the approved intent, changes, test evidence, review status, and limitations. Show the boss the full PR URL and the remaining merge decision.
Then run `sumctl pr reconcile TASK_ID --number N` (optionally `--repo owner/name`). It resolves the task repository's GitHub identity through `gh`, inspects that exact PR, and records repository, number, URL, head repository/branch, base branch, head SHA, state, and the observation time. Findings name every mismatch: a fork head, another branch, a head SHA that is not a recorded candidate, a missing base. `merged_for_task` is true only for a merged PR with a merge commit and no finding; a closed, unmerged, or mismatched PR is never the task's result.
An uncertain lookup (timeout, unknown number) is recorded as uncertain evidence and leaves the last exact observation in place. Reconcile again after every new push. Switching to a different PR number needs `--replace` after inspecting both. A PR from an older task or a legacy prose report is attached the same way, by inspecting the actual PR, never by parsing a URL or matching branch names.
A task whose identity stays incomplete keeps working; it is simply not ready for cleanup.

## Clean up after the merge

Only the boss merges. When they say a PR is merged, run `sumctl cleanup TASK_ID` first: it re-observes that exact PR through `gh`, requires `merged` with a merge commit (closed is not merged), compares the checkout HEAD with the merged head (squash and rebase merges pass; an extra local commit blocks), verifies workspace, pane, checkout, branch, and repository by identity, confirms through `agent get`, `pane process-info`, and an `lsof` cwd scan that the agent and any child in the checkout exited (Herdr `idle`/`done` is not exit), and lists staged, modified, untracked, and non-cache ignored files. Every blocker is named; nothing is removed.
Report blockers to the boss as they are. Do not kill processes, `git clean`, reset, or force anything to clear them; a blocked task stays visibly cleanup-pending and keeps working.
When the inspection is `ready`, run `sumctl cleanup TASK_ID --apply`. It rechecks, saves the intent, removes the workspace and clean checkout with one native `herdr worktree remove` without force, closes a bound reviewer pane that saved findings, verifies the branch survived, and archives the record. Repeating it is a no-op.
`sumctl cleanup TASK_ID --reviewer-only --apply` closes just a reviewer pane with saved findings and an exited agent; the shared implementation checkout is never touched by it.
If the command is interrupted, run it again: it reconciles from the saved intent and Herdr observation rather than repeating or forcing the removal.

Never merge, waive a failing required check, or tear down unfinished work. `sumctl archive --acknowledge` only archives the record after inspection; it deliberately does not stop agents or remove checkouts, and it is not a substitute for `cleanup`.
