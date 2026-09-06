---
name: create-verification
description: Bootstrap or extend a repository's project-local verification - the VERIFY.md contract, a `verify` mise task built from checks the project already declares, a feature-map index with seed feature files, and the vendored verify/maintain skills - then prove the generated instructions by running them once. Works in any clone without sum or Herdr.
---
# Create verification

You are writing for the next agent, who will read the output cold, mid-task, without having seen this repository.
Everything you generate must be something that agent can run.
A contract or map that was never executed is a draft, not completed onboarding.

This skill is portable: it needs Git, mise, Python 3.11+, and the two sibling skills `verify` and `maintain-verification` beside it.
No sum installation, Herdr session, `.sum` directory, or absolute path outside the target repository is involved.
Run every command below from the repository you are standardizing (`--root` accepts any directory inside it).

## 1. Inspect before asking

Run the scaffold in its default inspect mode:

```sh
python3 <path-to-this-skill>/scripts/verify_scaffold.py --root . --json
```

It reports, without writing or running anything: guidance files (README, AGENTS, CONTRIBUTING), the repository's own mise tasks against inherited ones, package scripts, make/just targets, declared containers, readiness endpoints and ports the repository already mentions, CLI entrypoints, HTTP routes found in source, test locations, and which driver capabilities are actually installed (`python3`, `node`, `curl`, `docker`, `chrome-devtools-axi`, `playwright`, `tmux`).
Read the guidance files it lists yourself.
Ask the user only what the repository cannot tell you: which surface is primary when several exist, and where a fixture or seed data comes from when nothing documents it.
Never invent a selector, command, flag, port, or credential; if the repository does not state it, the map says `manual` or carries a placeholder.

If the checkout does not build or its existing checks do not pass as-is, report the exact cause and stop at a draft.
Do not write a recipe around a broken base, and never add a `verify` task that merely prints success.

## 2. Generate

```sh
python3 <path-to-this-skill>/scripts/verify_scaffold.py --root . --write
```

The scaffold creates only what is missing and never overwrites:

- `VERIFY.md` in the shape the `verify` runner validates (`entrypoint = "mise run verify"`, `feature_maps`, `artifacts`, `[requires]`, and the Setup / Readiness / Automated checks / Scenarios / Isolation / Artifacts / Teardown / Policy sections). An existing `VERIFY.md` that declares another `feature_maps` location keeps it; new feature files go beside that index.
- A `verify` task appended to `mise.toml` when the repository has none, composed from the check tasks, package scripts, make targets, or test directory the inspection found. With nothing to reuse it writes no task and reports the problem instead.
- `docs/features/README.md` (or the declared index) labelled `Inventory: incomplete`, plus one file per seed feature: the top three to five real user-facing entrypoints it found, each with a stable ID, an Entry points section, a Scenarios table whose rows the runner reads, Driving it, Expected states and side effects, and Gotchas and manual gaps. Everything it could not observe is an explicit `TODO(verify)` placeholder.
- `.agents/skills/verify/` and `.agents/skills/maintain-verification/` vendored byte for byte, with thin `.claude/skills/<name>` symlinks. Substantive logic stays in the `.agents/skills` copy; the aliases only make native discovery work.
- `.artifacts/` added to `.gitignore`.

An existing file is never rewritten. One without `TODO(verify)` placeholders is yours and is reported `kept`. A draft that still has placeholders but that the inspection would now generate differently (a renamed task, a new route) is reported as a `conflict`: yours is kept and the proposal is written under `.artifacts/verification/scaffold/<stamp>/` so you can compare the two. A newly found feature gets a new file beside the index; the audit then reports it `unlinked-map` until you link it.
Exit code 1 means a problem that needs a person: no reusable check was found, a `verify` task is only inherited from a parent directory, or mise is absent.
Re-running on a completed repository reports every file `unchanged` and writes nothing.

## 3. Fill the map from observation

Replace every `TODO(verify)` with what you observed: the purpose and each user entrypoint, sub-features and variants as extra scenario rows, how to drive it through the real UI/CLI/API, the observable states and side effects, the success / error / cancel / empty / persistence cases that apply (say in prose when one does not), the test or check that covers an `automated` row, and the gaps only a person can check.
A row is `automated` only when its Driver names a test or command that exists and exercises it; a browser or desktop path with no installed driver stays `manual`.
Keep the `Inventory: incomplete` label until a person has inventoried the whole product; a seed map never implies full coverage.

Run the audit until it is clean:

```sh
python3 .agents/skills/maintain-verification/scripts/verify_audit.py
```

## 4. Prove it once

1. Launch in the repository's own isolation: its documented dev task, a temporary data directory, an ephemeral port, never the user's running instance or personal profile.
2. Readiness: `python3 .agents/skills/verify/scripts/verify_run.py --check`, then the readiness steps `VERIFY.md` lists (a health endpoint, a version line).
3. Run the contract: `python3 .agents/skills/verify/scripts/verify_run.py --base <merge-base>`; read the printed record path.
4. Drive one mapped feature end to end through its real entrypoint and capture evidence beside that record:

   ```sh
   python3 .agents/skills/verify/scripts/verify_capture.py --feature <id> http --expect-status 200 GET http://127.0.0.1:<port>/<route>
   python3 .agents/skills/verify/scripts/verify_capture.py --feature <id> run --expect-text 'Hello' -- <command> <args>
   ```

5. Run the Teardown section, then confirm `.artifacts/verification/<run-id>/run.json` and its `evidence/` files still exist. A cleanup that eats the proof fails this step.
6. Fix what failed and run the teardown after every failed iteration so nothing is left running. If the base itself is broken, report the cause; do not paper over it in the contract.

Report the run record path, the evidence paths, which feature you drove, and what stays `manual`.

## 5. Hand over the maintenance loop

Point the reader at `.agents/skills/maintain-verification/SKILL.md`.
Repository changes go through the project's ordinary PR and human-merge path; do not write into other clones and do not schedule an automatic routine.

## Attribution

Adapted from the Cursor pstack `create-verification-skill` and Lauren Tan's `verify-atlas` example (MIT); see `references/ATTRIBUTION.md`.
