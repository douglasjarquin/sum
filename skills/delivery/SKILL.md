---
name: sum-delivery
description: Independently inspect worker results, use existing verification tooling, and prepare a reviewable PR without autonomous merging.
---
# Delivery

A worker report is a claim. Read the saved task, its report, relevant changes, and outstanding decisions.

## Verify

Confirm the checkout, branch, candidate SHA, and actual diff. Confirm the reported verification commands and evidence.
Use the repo's configured MADE/No Mistakes route when available. Otherwise arrange a fresh-context review through a separately launched reviewer or the harness's supported independent-review facility.
The reviewer receives the task contract and candidate, not an instruction to rubber-stamp the worker's summary. Do not have simultaneous writers in the checkout.
If a separate reviewer is unavailable, say independent review was not performed; do not label the result fully reviewed.
Tie the review to the exact candidate SHA. Re-run required checks for later candidates; review the intervening changes rather than treating the previous SHA's approval as current.
A nit is not automatically a blocker. Limit repair cycles; escalate repeated failure rather than opening an endless review/fix loop.

Repository test scripts execute candidate-controlled code. Keep normal sandbox/credential protections. This MVP does not provide a credential-isolated verification runner; use trusted projects and their existing CI/dev container.

## Publish a PR

PR publication belongs to the coordinator by default. Do not merge.
Before any external action, verify `gh auth status` and the exact remote repository. Never change accounts or credentials automatically.
Push the recorded task branch without force. Check for an existing PR for that exact head/base using `gh pr list --state all` before `gh pr create`.
If creation times out, inspect GitHub before retrying; a timeout does not prove the PR was not created. No exactly-once publication claim is made by this MVP.
A closed/merged PR is not permission to create another automatically. Ask when the intended next action is unclear.
Describe the approved intent, changes, test evidence, review status, and limitations. Show the boss the full PR URL and the remaining merge decision.
Record the PR URL in a fresh task report with `sumctl report` so it survives a conversation reset.

Never merge, waive a failing required check, or tear down unfinished work. `sumctl archive --acknowledge` only archives the record after inspection; it deliberately does not stop agents or remove checkouts.
