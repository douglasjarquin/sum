---
name: maintain-verification
description: Keep a repository's VERIFY.md and feature maps honest when behavior changes - audit stale tasks, paths, links, and coverage claims, find the features a change touches, record a no-map-change rationale for internal changes, and keep authored map edits separate from run results. Works in any clone without sum or Herdr.
---
# Maintain verification

A feature map rots the moment the app changes.
This skill is the upkeep pass for a repository standardized with `VERIFY.md`, `mise run verify`, and feature maps (see `.agents/skills/verify/SKILL.md`).
It is portable: Git, mise, Python 3.11+, and the files in this directory. No sum, Herdr, or path outside the repository.

## When

- On every behavior-changing task, before you report: the approved intent, the code diff, the existing feature IDs, the tests, and the map entries must agree.
- When the audit is requested, or a run record shows `not_exercised` rows nobody can explain.

## Audit

```sh
python3 .agents/skills/maintain-verification/scripts/verify_audit.py --base <merge-base>
```

It reads text and Git and runs nothing. Findings fail it until resolved:

| Finding | Meaning |
| --- | --- |
| `placeholder` | a `TODO(verify)` is still in the contract or a map: it is a draft |
| `stale-task` | `mise run NAME` is mentioned but this repository defines no such task (or no `verify` task at all) |
| `stale-path` | a backticked repository path no longer exists (Git-ignored runtime paths and absolute or route paths are not checked) |
| `unlinked-map` / `missing-link` | a map file the index does not link, or an index link to nothing |
| `coverage-claim` | an `automated` row names no test or command, or names one that is missing or contains no test |
| `duplicate-id` / `bad-driver` | a scenario ID defined twice, or a Driver cell that is neither `automated` nor `manual` |
| `unmapped-change` | with `--base`: a changed source file that no map references and no `--rationale` explains |

Notes are informational: `affected-map` lists the maps whose references changed since the base (re-read their entry points, variants, expected states, and coverage), `policy-changed` lists verification policy files the root's independent review must inspect, and `proof` says whether the newest run record still matches the current contract and maps (`current`, `stale`, or `none`).
Selectors and prose are not validated; only a live drive proves them.

The record is written to `<artifacts>/audit/<audit-id>.json` with `authored` (contract and map hashes, scenarios, references) separate from `runs` (the newest run record). Reference it by path.

## Update the map

- Update the entrypoints, variants, expected states, and coverage of the features the change affects. Preserve unaffected entries and useful history; do not regenerate a file to change one row.
- A purely internal change needs an explicit rationale, recorded with `--rationale 'why no map row changes'` so the audit record carries it, and repeated in the task report or PR description.
- Never delete a failing scenario, weaken an assertion, or flip a row to `automated` because the candidate touched the map. A candidate that changes `VERIFY.md`, tasks, or maps is flagged `requires_root_review` by the runner and cannot certify itself.
- Authored map changes are one thing; run results are another. The proof is `python3 .agents/skills/verify/scripts/verify_run.py --base <merge-base>` plus `verify_capture.py` evidence for the rows you drove, not the edit.
- The scaffold (`create-verification`) stays idempotent: re-running it keeps every user edit and custom task and reports conflicts instead of overwriting. Use it to add a new feature file, not to rewrite existing ones.

## Boundaries

- Edit only the verification files: `VERIFY.md`, the maps, and the helpers under `.agents/skills/`. A behavior the map describes that the app no longer does is either doc drift (fix the map) or a product regression (report it); never change product code from this pass.
- Changes go through the repository's ordinary PR and human-merge path. Do not write into other clones, and do not schedule a periodic agent routine.
- Respect production and session boundaries: never clone personal auth or browser profiles, drive the user's active app instance, change permissions, or kill by process name. Clean up only what your run created and keep the proof artifacts.

## Attribution

Adapted from the Cursor pstack `maintain-verification-skill` (MIT); see `../create-verification/references/ATTRIBUTION.md`.
