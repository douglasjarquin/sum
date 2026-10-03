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

Before writing, `factory merge` runs a live preflight that re-reads the PR identity, state, candidate head, mergeability, conflict status, and required checks. It stops immediately if the PR identity, state, repository, base, or candidate changed; GitHub reports a conflict or blocked/behind state; mergeability is missing, unsupported, or `UNKNOWN` with a non-`UNKNOWN` merge state; the live checks are missing, failing, pending, unavailable, or for another candidate; the observation is malformed or fails; or the repository authorization or factory lane changed during preflight. It also stops if GitHub still reports the PR as a draft after accepting the ready request.

Only `mergeable=UNKNOWN` with `mergeStateStatus=UNKNOWN` is retried, for at most three fresh preflight attempts two seconds apart. If mergeability is still unknown, the command exits successfully with `status: deferred`, `pending: true`, and `merged: false` (plus the task, repository, PR number, candidate, and note). Inspect the PR, then rerun `factory merge` explicitly when it is ready; the command does not retry the merge automatically.

After a passing preflight, the command marks the PR ready (`gh pr ready`), observes it again, and squash-merges only if the live gates and authority still pass, matching the candidate head. A draft cannot merge, so promotion is part of the authorized merge.
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
