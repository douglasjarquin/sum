# Verification contract

This file is the single repository-local verification convention for sum.
It works in an ordinary clone with Git, mise, and Python 3.11 or newer; no sum installation, HC session, or absolute path outside this checkout is required.
The portable procedure and the runner that records evidence live in `.agents/skills/verify/` (alias `.claude/skills/verify`); its shared engineering rubric is `.agents/skills/verify/references/engineering-principles.md`.
`.agents/skills/evidence/` captures before/after proof of one scenario from the base and candidate builds; `.agents/skills/create-verification/` scaffolds this convention in another repository and `.agents/skills/maintain-verification/` audits it after a change.
A harness without skill discovery follows this file directly.

```verify
entrypoint = "mise run verify"
feature_maps = "docs/features/README.md"
artifacts = ".artifacts/verification"
evidence = ".artifacts/evidence"
task_owner = "."
timeout_seconds = 3600
policy_files = ["COORDINATOR.md", "skills/"]

[requires]
commands = ["git", "mise", "go", "python3", "node", "aube"]
```

## Setup

Run `mise install go python node aube` once so the pinned Go, Python, Node, and aube tools required by the aggregate are available.
Git must also be available from the host.
CI enables only these four tools.
Browser evidence uses the runner's preinstalled Google Chrome through `EVIDENCE_BROWSER`.
The driver launches headless Chrome with container-safe flags (`--no-sandbox`, `--disable-setuid-sandbox`, `--disable-dev-shm-usage`, `--disable-gpu`) so `DevToolsActivePort` works on GitHub-hosted runners and similar environments.
Each capture still uses a fresh ephemeral user-data-dir (`mkdtemp`), never the operator's real browser profile; the profile is removed after the job.
For local verification, prefix the runner command with `MISE_ENABLE_TOOLS=go,python,node,aube` so mise does not automatically install unrelated repository tools.

## Readiness

`git rev-parse --show-toplevel` must print this repository's root.
`mise tasks ls` must list `verify` with a source inside this repository; a `verify` task inherited from a parent directory is another project's command and the runner blocks on it.
`python3 .agents/skills/verify/scripts/verify_run.py --check` validates this file, the feature maps, task ownership, and the required commands without running anything.

## Automated checks

`mise run verify` is the canonical aggregate entrypoint.
Each verification role runs that aggregate once for its final candidate; targeted iteration does not require repeating every subcommand before the aggregate.
It runs the Go, top-level Python unittest discovery and web checks, stopping at the first failure without dropping an existing check:

| Check | Command | Proves |
| --- | --- | --- |
| Go CLI, HC, and skill tests | `cd go && go test ./...` | sumctl command contracts, typed HC protocol, endpoint/receipt authority and observer, portable verify/evidence skills, and the offline demo |
| Python HC, setup and packaging tests | `python3 -m unittest discover -s tests -p 'test_*.py'` | native socket fixture characterization, exact-artifact installer/cache/refusal contracts, mocked lab ownership/teardown failures and existing setup/packaging checks; these are offline tests, not actual-model qualification |
| Web site | `aube -C web install --frozen-lockfile`, `aube -C web run check`, `aube -C web run test:unit`, `aube -C web run build`, then a `web/dist` route-emission check | the Astro site installs from the lockfile, typechecks, passes unit tests, and builds all routes into `web/dist` under the `/sum/` base |

Scoped tasks stay available for iteration: `mise run test` (suites only), `mise run lint` (`gofmt -l` and `go vet` over `go/`), `mise run demo`, and the `web:`-prefixed mise tasks.
`mise run web:test` also runs the Playwright end-to-end suite; it needs the browser once from `mise run web:deps`, so it is manual in the feature maps.
`mise run lint` is the project's declared lint task (`mise-tasks/lint`); the delivery pipeline Lint gate runs it.
`mise run test-live` is the explicit isolated pinned-HC smoke test and is not part of the aggregate because it needs the pinned HC binary; `mise run doctor` observes the installation and is not a check.
With `--artifact /path/proof.json`, the native runner retains separate `/path/proof-incarnation.json` and `/path/proof-smoke.json` records and does not overwrite an existing base `/path/proof.json`.
An explicitly selected missing `--hc` binary is a refusal, never a PATH fallback.
Offline Python runner tests validate routing/selection; actual native execution still requires its own candidate-bound evidence.

## Scenarios

The feature maps under `docs/features/` describe the real user journeys and which of them the automated checks exercise.
Rows marked `automated` are covered by `mise run verify`; rows marked `manual` need a real HC session or an authenticated harness and are recorded `not-run` unless the operator reports them with `--scenario`.
A green `mise run verify` is evidence for the automated rows only, never for a scenario that was not exercised.

## Isolation

Every test and the demo create temporary state homes (`--home` under a temporary directory) and named lab HC sessions or the fake HC protocol server.
Never point a check at the live `.sum/` directory or the user's `default` HC session.
No credentials, model calls, or GitHub writes are involved; `SUM_*`, `HC_*` and `HERDR_*` variables inherited from an installation are stripped by the lab code.
The Go tests in `mise run test` and `mise run verify`, and the Python discovery in `mise run verify`, run through `scripts/test_git_env.py` with a private Git global configuration and system configuration disabled.
Each invocation retains its configuration in a unique private `.artifacts/test-git/run-*/` directory, supplies the `Sum Test <sum-test@example.invalid>` fixture identity, disables fixture commit and tag signing, and clears inherited Git configuration, repository selectors, and author/committer identity overrides only in the child environment.
The wrapper preserves the working directory, command arguments, and exit status; Go builds, web checks, and ordinary Git operations retain their existing environment.
For targeted tests, run `cd go && python3 ../scripts/test_git_env.py -- go test ./internal/cli -run TestGraphInit`, or `python3 scripts/test_git_env.py -- python3 -m unittest discover -s tests -p test_git_env.py` from the repository root.

## Artifacts

Each run writes `.artifacts/verification/<run-id>/run.json` (run id, candidate SHA, dirty state, contract and map hashes, commands, times, outcomes, skips with reasons) and `verify.log` (the full entrypoint output, retained on failure).
`.artifacts/verification/latest.json` mirrors the newest record.
Before/after evidence from `.agents/skills/evidence/` lands under `.artifacts/evidence/<run-id>/<scenario>/` as before-SHA and after-SHA directories of originals with a capture manifest, plus a comparison manifest and a derived two-up HTML view; `VERIFY_EVIDENCE_ROOT` moves that root outside a disposable checkout and the skill's `promote` command copies a run out with every hash re-verified.
A feature-map row whose Evidence cell names a screenshot, screencast, or red/green pair is reported by the runner as missing required evidence until such a comparison exists for the candidate SHA.
The browser recipe needs the pinned Node (`mise install`) and a Chromium-family browser already on the machine; `python3 .agents/skills/evidence/scripts/evidence_capture.py capabilities` reports what is available and nothing is downloaded per capture.
Both directories are Git-ignored; reference records by path in reports.

## Teardown

The offline checks leave nothing running.
Native canaries must remove only their exact proven-owned lab terminals/history and scratch state, never the user's core or live installation.
Remove `.artifacts/verification/` and `.artifacts/evidence/` when you no longer need the records; promote an evidence run first when it must outlive the checkout.

## Policy

Edits to this file, `mise.toml`, `mise-tasks/`, `docs/features/`, or the skills under `.agents/skills/verify/`, `.agents/skills/evidence/`, `.agents/skills/create-verification/`, and `.agents/skills/maintain-verification/` are policy changes.
Run the runner with `--base <merge-base>` so such a candidate is flagged `requires_root_review`; it cannot certify its own new standard.
`COORDINATOR.md` and `skills/` are declared as policy files here too: they carry the coordinator core and the role and action procedures that `AGENTS.md` delegates to, so editing them needs the same review as editing `AGENTS.md`.
Without `--base` a run never certifies a SHA, and the contract's optional `policy_files` list can only add paths to that default set.
The coordinator's separate verification and review (`skills/sum-deliver/SKILL.md`) remain in place and are not replaced by this contract.
GitHub Actions runs this same runner once for pull requests targeting `main` and pushes to `main`, comparing the checked-out candidate to the event base revision and retaining `.artifacts/verification/` on success or failure.
CI is an additional gate, not a replacement for worker verification, a fresh root pass, independent review, or human merge.

## HC acceptance limits

The aggregate is offline evidence, not actual harness qualification or production migration proof.
The native lab uses a unique `HC_SESSION` and scratch XDG state/config/runtime paths with inherited terminal identity removed.
Qualified automatic input currently requires unmodified published HC 0.1.14, Codex 0.166.0 and the default keymap with Sum's namespaced Tab template.
Idle uses `if:idle`; observed Done sends without `if` or `force`, so a raced working turn queues the submission for the next turn rather than providing an atomic Done guard.
Native `started`, status, turn counts and completed waits are never application receipts or proof of exit.
Other harnesses and every unrun manual scenario remain gaps in [acceptance](docs/ACCEPTANCE.md).
Existing-installation migration must refuse without enforceable legacy writer/sender exclusion; current legacy APIs cannot establish that prerequisite.
No production cutover or working production exclusion lease is certified by local checks.
Generated dependency inventory freshness is checked with `python3 scripts/generate_dependency_inventory.py --check`; never edit the generated JSON by hand.

The Python discovery step includes the top-level HC fixture, installer and lab modules; feature-map automated claims for those modules rely on this aggregate step.
Native smoke, identity and full scripted application canaries remain explicit manual checks with their own artifacts.
A full model-free Sum composition over published HC with scripted terminals and local strict fake GitHub proves the exercised application wiring, not real Codex reasoning, external publication or production cutover.
