# Real-host acceptance

Do these in a throwaway repository and an isolated Herdr session before trusting a large task.
The offline demo is not a substitute for this check.

## 1. Bootstrap

Run setup twice. Both runs should succeed without replacing unrelated MCP entries or touching global harness/account settings. The setup MCP handshake should discover ten tools.
Run `sumctl doctor` inside the intended Herdr pane. Confirm the expected session, pinned version, and selected harness executable. Authenticate that harness and GitHub yourself.

## 2. First task

Create a small Git repository with a trivial function and a failing unit test. Launch a harness from the sum directory and say:

> Delegate this fix to [the chosen harness]. Work in a separate checkout, run the test, and report the candidate SHA. Do not create a PR or merge anything. Before implementing, ask me whether to preserve the existing public function name.

Confirm a distinct worktree/branch, a saved question, and the actual primary checkout remaining unchanged.
Answer the question, confirm it remains visible until applied, and inspect the final report and diff.
Record whether MCP or CLI was used, harness version, Herdr version, and any trust prompts encountered.

## 3. Busy or closed coordinator

Repeat while the coordinator is occupied, then while it is closed. The question must remain in `sumctl inbox`; prompt delivery may remain pending.
Reopen the coordinator, run doctor, bind the task's parent to the new pane, and perform a rundown. There must be no duplicate worker and no invented answer.

## 4. Non-cooperative worker

Ask a worker deliberately to print a question without calling `sumctl ask`. Run a rundown. The coordinator should inspect the idle/blocked worker and save the question.
This measures the attended fallback. Instant unattended capture is NOT a passing criterion claimed by this MVP.

## 5. Uncertain startup

Use a fresh harness trust/authentication prompt. A failed startup must preserve the task and pane, report uncertainty, and refuse an automatic second start. Inspect and resolve the actual prompt rather than creating another agent.

## 6. Cross-harness contract

Repeat the same small task with a different worker harness and, separately, a different coordinator. Compare question capture, result reporting, trust behavior, and startup instructions. Do not label every harness supported merely because two executables launch.

## 7. Backup

Create a records backup. Inspect its manifest: worktree code is explicitly excluded. Extract into a new directory, inspect records via `sumctl --home`, and verify that another machine's bindings are not automatically used.

## Record results

For each real task, record all human-labeled questions, which were saved by workers, which were found during rundown, which were missed, and time until attention. Also record unnecessary attention items and manual pane inspections.
A useful result is fewer manual inspections and no forgotten decisions in the tested cases, not merely a successful command exit.
