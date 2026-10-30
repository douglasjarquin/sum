# Issue 330 verification record

This change uses unmodified published HC 0.1.14.
The readiness workaround, exact release provenance and limits are recorded in [the runtime decision](../decisions/hc-runtime.md).
The implementation remains isolated from the serving installation.
No production update or migration is authorized or claimed.

## Evidence boundaries

| Area | Local evidence | Limit |
| --- | --- | --- |
| Native protocol | Typed socket fixtures and published-HC smoke/identity scripts exercise responses, uncertainty, process identity and teardown. | A fixture response is not an application receipt. |
| Codex readiness | Actual Codex 0.166.0 probes passed a Done follow-up and an ordered Tab submission during a held working turn. | This is no atomic Done predicate; other harnesses remain unqualified. |
| Packaging | Retained checks exercise exact cached artifact, matched schema/manual/provenance, helper capabilities and release concurrency. | Read their exact candidate/log/result; cached staging and focused checks are not production installation or final aggregate proof. |
| Application composition | The full application canary passed through actual HC terminals and actual Sum helper callbacks. | Its scripted TUI is not authenticated Codex; GitHub and the Git destination are private fixtures. |
| Services | Actual published-HC service Start/Stop canary passed readiness, one interrupt, conclusive exit and process death. | No production service was touched. |
| Migration | Generation isolation, historical readers, admission fences and resumable migration contracts have deterministic tests. | No production exclusion lease exists, so existing-installation cutover remains blocked. |
| Verification policy | Candidate-bound audit records distinguish mapped source changes, explicit retirement rationale and policy review. | Every later contract/map edit needs a fresh final audit; changed policy cannot certify itself. |

## Reproduce the native canaries

Use a staged HC executable whose exact published provenance has been checked.
These commands create private cores and scratch XDG directories; they do not connect to the user's default scope.
Successful teardown must report `core_stopped: true`.
Failures retain diagnostic evidence and do not count as passing cleanup.

```sh
python3 scripts/live_smoke.py --help
python3 scripts/live_incarnation.py --help
python3 scripts/live_sum_canary.py \
  --source "$PWD" \
  --hc /absolute/path/to/verified/herdr-core \
  --artifact .artifacts/hc-contract/application-canary.json
```

Run with the repository's pinned Python and Go toolchains.
The full canary records the Sum Go-source digest and compiled helper digest.
Its candidate SHA identifies the disposable application repository, not this Sum branch.
Worker and coordinator verification runs have distinct IDs and certify that disposable candidate.
The independent reviewer performs a separate read-only candidate inspection.
The flow covers assignment, worker initialization, question, recorded answer, worker application, exact-boundary wake consumption, report, reviewer initialization and verdict, coordinator verification, local push, fake publication and guarded cleanup.
It performs no external publication or merge.

## Focused implementation checks

The package race suites cover native client effects, generation fencing, role authority, launch receipts, reviewer kickoff/rebind, delivery concurrency, wake consumption, observer startup/attention, procedure refresh, repair, service ownership, stopped-process proof, cleanup and activation.
Their tests retain the prior business scenarios while replacing retired transport fixtures.
Actual subprocess-based CLI observer tests require the helper to write its own startup receipt; terminal creation alone cannot satisfy readiness.
The Linux file-watcher tests cover startup, atomic health-file replacement, cancellation and setup failure.
They cross-compile locally on macOS; native Linux execution belongs to hosted CI.

The canonical aggregate now includes the Python HC, setup, packaging, operating-file and benchmark contracts alongside the existing Go and web checks.
The generated dependency inventory has a freshness check against its source metadata and exact pins.
The full model-free HC canary is a separate manual scenario, not an implicit result of the offline aggregate.

## Existing-installation prerequisite

The legacy plugin, MCP and helper APIs cannot prove that every old writer and sender has stopped.
Removing configuration, checking process lists or setting a boolean cannot supply that proof.
Migration therefore refuses without an independently enforceable writer/sender exclusion lease.
Physical state generations protect the new ledger from old file handles, but cannot stop old terminal sends.
No hot adoption, legacy automatic rollback, production cutover or fabricated successful migration is part of this evidence.

## Browser and independent-review availability

The browser attempt is recorded in `.artifacts/hc-contract/browser-check.json` as blocked before the first navigation.
The browser access host was unavailable, and the agent-browser CLI fallback was absent; no installation workaround was authorized.
All eight affected routes remain Skip: `/sum/`, `/sum/architecture/`, `/sum/configuration/`, `/sum/docs/`, `/sum/herdr/`, `/sum/install/`, `/sum/skills/` and `/sum/update/`.
No browser interaction, screenshot, visual comparison or console proof was produced.
The owned local preview started and stopped; that establishes no rendered-page acceptance.
Astro typechecking, unit tests, static build and route emission are separate local checks, not substitutes for the skipped browser scenarios.

The configured external Claude ACP review failed before provider initialization and before any reviewed content reached the provider.
No cross-model corroboration was obtained from that attempt.
The in-process source review and adversarial fallback remain attributable local reviews, with their own limits.
The original independent review reported twelve defects.
Each has targeted repair work and focused evidence; closure still requires follow-up independent validation against the integrated candidate rather than assuming a submitted repair is correct.

## Final delivery checks

Use the final candidate's actual run records to report canonical verification, lint, feature-map audit, independent runtime/policy review and hosted CI separately.
A failed or provisional run, an unrun browser scenario and an unavailable external review stay explicit; do not turn focused passing checks or an older helper/source digest into a final passing aggregate.
Use the original review and candidate-bound follow-up record to determine which findings remain open.
Local fixtures, actual native/model-free canaries, authenticated harness qualification, hosted CI, merge, deployment and production acceptance are distinct claims.
No production activation or legacy-writer exclusion lease is supplied by these records.
Only the user decides whether to merge and later activate it.
