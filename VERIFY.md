# Verification contract

This optional developer contract verifies Sum's instruction package, portable tools, excluded Bot pack preservation, and website.
Ordinary instruction use needs none of these tools or checks at startup.
The portable tool is the `sum-verify` binary, built from `tools/verify/` and driven through `.agents/skills/verify/scripts/sum-verify`; `.agents/skills/evidence/` covers optional evidence capture.

```verify
entrypoint = "mise run verify"
feature_maps = "docs/features/README.md"
artifacts = ".artifacts/verification"
evidence = ".artifacts/evidence"
task_owner = "."
timeout_seconds = 3600
policy_files = ["skills/", "tools/verify/"]

[requires]
commands = ["git", "mise", "go", "python3", "node", "aube"]
```

## Setup

Use the pinned tools with `mise install go python node aube` for repository development.
No Sum runtime is built or installed.
Inside this repository the wrapper builds `sum-verify` from source with the pinned Go; adopting projects run a pinned release binary instead.

## Readiness

Run from this repository root; `mise tasks ls` must resolve verify here.
`.agents/skills/verify/scripts/sum-verify run --check` checks prerequisites without executing the aggregate.
Limit mise to relevant tools with `MISE_ENABLE_TOOLS=go,python,node,aube`.

## Automated checks

Each verification role runs the canonical aggregate once for its final candidate; targeted iteration is separate.
Use `MISE_ENABLE_TOOLS=go,python,node,aube .agents/skills/verify/scripts/sum-verify run --base <merge-base>` after committing.

| Check | Command | Proves |
| --- | --- | --- |
| Portable tools | `cd tools/verify && go test ./...`, `cd tests/portable && python3 ../../scripts/fixture_git_env.py -- go test ./...` | Scaffold, verifier, audit, capture, evidence, publication, and packaging behavior of the built `sum-verify` in disposable projects, without a Sum runtime |
| Package and Bot contracts | `python3 scripts/fixture_git_env.py -- python3 -m unittest discover -s tests -p 'test_*.py'` | Four native procedures plus maintainer discovery, entry packaging, protected immutable-baseline bytes, Bot recipe/invariant tests, Git isolation, and active local links |
| Website | `aube -C web install --frozen-lockfile`, `aube -C web run check`, `aube -C web run test:unit`, `aube -C web run build`, route assertions | Dependency lock, Astro/type checks, URL helper tests, static routes under /sum/ |

`mise run lint` checks formatting and vets the `sum-verify` module and the standalone Go test module.
`mise run test` runs retained Go/Python suites for iteration.
`mise run web:test` runs website unit and Playwright E2E checks; `mise run web:deps` installs its optional Chromium browser once.
GitHub Actions uses the same canonical runner and separately runs the website E2E build workflow.
A passing hosted job is not authenticated native workflow acceptance.

## Scenarios

The maps under docs/features/ distinguish automated package/tool behavior from manual native conversations.
Rows marked manual remain not-run unless actually exercised and reported with --scenario.
[docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) defines all issue acceptance scenarios and [issue evidence](docs/verification/issue-348.md) records actual limitations.
Static instructions, CLI help, model-free fixtures, and native done status cannot prove native messages reached an authenticated lead.
Do not infer qualification from version strings.

## Isolation

Tests use disposable repositories and ephemeral processes/ports.
The Git fixture wrapper gives child test commands a private unsigned fixture identity and retained config under .artifacts/test-git/.
No live Sum installation, credentials, shared HC core, or user's default HC session is used.
For native lifecycle exercises follow upstream isolated-session guidance with a distinct HC_SESSION and scratch XDG paths.
Preserve reports/output before exact owned-process cleanup; do not remove unfinished work or restart shared cores.

## Teardown

Offline checks leave no intended long-running services.
Retain run logs and helper reports before removing any exact task-owned temporary process.

## Artifacts

Each run records its outcome, candidate SHA, dirty state, command, scenario statuses, policy changes, and unrun scenarios under .artifacts/verification/ with a retained verify.log.
Before/after evidence lives under .artifacts/evidence/ when explicitly captured.

## Policy

A dirty-tree run reports `provisional`; a policy-changing candidate reports `needs-root-review`, requires independent root review, and cannot certify its own new standard.
Changes to VERIFY.md, mise tasks, feature maps, portable verification skills, the `sum-verify` source, CI, AGENTS.md, or declared skills policy must be reviewed independently against --base.
Base policy paths stay protected even when the candidate removes them.
A changed map is an authored coverage claim, not execution evidence.
Use the existing maintenance audit to check stale paths/tasks/coverage; no mandatory runtime delivery gate or second full local run is added.
The user retains merge and independent-adoption authority.
