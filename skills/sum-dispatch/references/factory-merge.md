# Coordinator procedure: factory merge

Part of `sum-dispatch`, read from the factory lane procedure. Merge a factory PR only at high confidence, or leave a human gate and free the lane when later issues do not depend on it.

Coordinator only, after `sum-delivery` has independently verified and reviewed the candidate and `pipeline run` has published the gate table and evidence block.

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

Marks the PR ready (`gh pr ready`) and squash-merges it in the same action, matching the candidate head.
A draft cannot merge, so promotion is part of the authorized merge.
Then observe the merge (`pr reconcile` / cleanup inspection) and run guarded cleanup as `sum-delivery` already describes.

```sh
./bin/sumctl factory release owner/repo --issue N --reason merged
```

`--strict-cleanup` factories release only after `cleanup --apply` succeeds.

## Next

When the lane is free, `factory tick` once.
If `idle`, return control.
Do not poll inside this pane.
