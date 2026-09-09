# Verification contract

This file is the single repository-local verification convention for sum.
It works in an ordinary clone with Git, mise, and Python 3.11 or newer; no sum installation, Herdr session, or absolute path outside this checkout is required.
The portable procedure and the runner that records evidence live in `.agents/skills/verify/` (alias `.claude/skills/verify`); `.agents/skills/evidence/` captures before/after proof of one scenario from the base and candidate builds; `.agents/skills/create-verification/` scaffolds this convention in another repository and `.agents/skills/maintain-verification/` audits it after a change.
A harness without skill discovery follows this file directly.

```verify
entrypoint = "mise run verify"
feature_maps = "docs/features/README.md"
artifacts = ".artifacts/verification"
evidence = ".artifacts/evidence"
task_owner = "."
timeout_seconds = 3600

[requires]
commands = ["git", "mise", "go", "python3", "node"]
```

## Setup

Run `mise install go python node` once so the pinned Go, Python, and Node tools required by the aggregate are available.
Git must also be available from the host.
CI enables only these three tools.
Browser evidence uses the runner's preinstalled Google Chrome through `EVIDENCE_BROWSER`, retaining its sandbox.
For local verification, prefix the runner command with `MISE_ENABLE_TOOLS=go,python,node` so mise does not automatically install unrelated repository tools.

## Readiness

`git rev-parse --show-toplevel` must print this repository's root.
`mise tasks ls` must list `verify` with a source inside this repository; a `verify` task inherited from a parent directory is another project's command and the runner blocks on it.
`python3 .agents/skills/verify/scripts/verify_run.py --check` validates this file, the feature maps, task ownership, and the required commands without running anything.

## Automated checks

`mise run verify` is the canonical aggregate entrypoint.
Each verification role runs that aggregate once for its final candidate; targeted iteration does not require repeating every subcommand before the aggregate.
It runs, in order, the existing commands and stops at the first failure:

| Check | Command | Proves |
| --- | --- | --- |
| Offline behavior suites | `python -m unittest discover -s tests -p 'test_*.py'` | Task state, roles, dispatch, environment, evidence, cleanup, projects, the verification runner and its create/maintain skills against fixtures, and the #8 twelve-worker mixed-version regression, against real Git and a strict fake Herdr |
| Mesh command contracts | `node --test tests/mesh.test.mjs` | Herdr Mesh command construction and bounded waits |
| Scripted end-to-end demo | `python scripts/demo.py` | Delegation, a question surviving a busy coordinator, an answer, a branch commit, a report, and a records backup |

Scoped tasks stay available for iteration: `mise run test` (suites only) and `mise run demo`.
`mise run test-live` is the explicit real-Herdr smoke test and is not part of the aggregate because it needs an installed Herdr; `mise run doctor` observes the installation and is not a check.

## Scenarios

The feature maps under `docs/features/` describe the real user journeys and which of them the automated checks exercise.
Rows marked `automated` are covered by `mise run verify`; rows marked `manual` need a real Herdr session or an authenticated harness and are recorded `not-run` unless the operator reports them with `--scenario`.
A green `mise run verify` is evidence for the automated rows only, never for a scenario that was not exercised.

## Isolation

Every test and the demo create temporary state homes (`--home` under a temporary directory) and named lab Herdr sessions or the fake Herdr.
Never point a check at the live `.sum/` directory or the user's `default` Herdr session.
No credentials, model calls, or GitHub writes are involved; `SUM_*` and `HERDR_*` variables inherited from an installation are stripped by the lab code.

## Artifacts

Each run writes `.artifacts/verification/<run-id>/run.json` (run id, candidate SHA, dirty state, contract and map hashes, commands, times, outcomes, skips with reasons) and `verify.log` (the full entrypoint output, retained on failure).
`.artifacts/verification/latest.json` mirrors the newest record.
Before/after evidence from `.agents/skills/evidence/` lands under `.artifacts/evidence/<run-id>/<scenario>/` as before-SHA and after-SHA directories of originals with a capture manifest, plus a comparison manifest and a derived two-up HTML view; `VERIFY_EVIDENCE_ROOT` moves that root outside a disposable checkout and the skill's `promote` command copies a run out with every hash re-verified.
A feature-map row whose Evidence cell names a screenshot, screencast, or red/green pair is reported by the runner as missing required evidence until such a comparison exists for the candidate SHA.
The browser recipe needs the pinned Node (`mise install`) and a Chromium-family browser already on the machine; `python3 .agents/skills/evidence/scripts/evidence_capture.py capabilities` reports what is available and nothing is downloaded per capture.
Both directories are Git-ignored; reference records by path in reports.

## Teardown

The checks leave nothing running.
Remove `.artifacts/verification/` and `.artifacts/evidence/` when you no longer need the records; promote an evidence run first when it must outlive the checkout.

## Policy

Edits to this file, `mise.toml`, `mise-tasks/`, `docs/features/`, or the skills under `.agents/skills/verify/`, `.agents/skills/evidence/`, `.agents/skills/create-verification/`, and `.agents/skills/maintain-verification/` are policy changes.
Run the runner with `--base <merge-base>` so such a candidate is flagged `requires_root_review`; it cannot certify its own new standard.
Without `--base` a run never certifies a SHA, and the contract's optional `policy_files` list can only add paths to that default set.
The coordinator's separate verification and review (`skills/sum-delivery/SKILL.md`) remain in place and are not replaced by this contract.
GitHub Actions runs this same runner once for pull requests targeting `main` and pushes to `main`, comparing the checked-out candidate to the event base revision and retaining `.artifacts/verification/` on success or failure.
CI is an additional gate, not a replacement for worker verification, a fresh root pass, independent review, or human merge.
