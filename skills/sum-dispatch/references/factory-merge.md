# Coordinator procedure: factory merge

Part of `sum-dispatch`, read from the factory lane procedure. Merge a factory PR only at high confidence, or leave a human gate and free the lane when later issues do not depend on it.

Coordinator only, after `sum-deliver` has independently verified and reviewed the candidate and `pipeline run` has published the gate table and evidence block.

Standing merge authorization is only `douglasjarquin/remainder`, `cofactorworks/nicebaas`, and `cofactorworks/ilovethatphoto`, after independent verification/review and green required checks on the exact candidate.
Do not widen that list.
Deployment, DNS, billing, and public-launch decisions stay owner-approved.

## Merge-check

```sh
./bin/sumctl factory merge-check TASK_ID
```

`confidence: high` means every check passed: authorized repo, delivery closure, independent review approve on this candidate SHA, every pipeline stage pass/skipped (lint and CI may be not_declared), a real comparison.json inside the checkout whose verdict is `red-green` or `before-after` bound to this SHA, and fewer than three recorded CI repair failures.
`after-only`, `before-also-passes`, and any other verdict are a human gate.

`factory merge` also refuses unless the task occupies a factory lane (or still carries `sum-claimed` from this installation).

`confidence: human-gate` means pause.
Add GitHub label `sum-gated`, comment why, and:

```sh
./bin/sumctl factory release owner/repo --issue N --reason gated
```

If the next issue does not depend on this one, add `--continue` so the lane frees.
Otherwise the lane stays occupied.

Human-gate when evidence could not be captured, verification is not a pass, or CI is still red after three repair attempts.
Repair CI with `repair send` (`in-scope`) up to three times, then gate.

## Merge

Only when merge-check is `high`:

```sh
./bin/sumctl factory merge TASK_ID
```

Before writing, `factory merge` runs a live preflight that re-reads the PR identity, state, candidate head, mergeability, conflict status, and live checks (required checks first, falling back to all checks when none are required). No reported checks count as CI `not_declared`, which the gates allow; an unreadable check list is unavailable and stops the merge. It stops immediately if the PR identity, state, repository, base, or candidate changed; GitHub reports a conflict or blocked/behind state; mergeability is missing, unsupported, or `UNKNOWN` with a non-`UNKNOWN` merge state; checks are failing, pending, unavailable, or for another candidate; the observation is malformed or fails; saved gates, review, confidence, or PR identity/candidate changed; or the repository authorization or factory lane changed. It also stops if GitHub still reports the PR as a draft after accepting the ready request.

Only `mergeable=UNKNOWN` with `mergeStateStatus=UNKNOWN` is retried. The three fresh preflight attempts, two seconds apart, are shared across the pre-ready and post-ready observations. Exhausting that shared budget returns exit 0 with `status: deferred`, `pending: true`, and `merged: false` (plus the task, repository, PR number, candidate, and note). Deferral can happen after `gh pr ready`, leaving the PR ready and unmerged. It can also carry the generic note `factory merge deferred: mergeability could not be confirmed`. Inspect the PR, then rerun `factory merge` explicitly when it is ready; the command does not retry the merge automatically.

After a passing preflight, the command revalidates saved gates, review, confidence, PR identity and candidate, repository authorization, and lane ownership before marking the PR ready (`gh pr ready`). It observes the PR again and repeats that authority revalidation before squash-merging the candidate head. A draft cannot merge, so promotion is part of the authorized merge.
If the merge request itself has an uncertain result, observe the PR before any retry.
Then observe the merge (`pr reconcile` / cleanup inspection) and run guarded cleanup as `sum-deliver` already describes.

```sh
./bin/sumctl factory release owner/repo --issue N --reason merged
```

`--strict-cleanup` factories release only after `cleanup --apply` succeeds.

## Next

When the lane is free, `factory tick` once.
If `idle`, return control.
Do not poll inside this pane.
