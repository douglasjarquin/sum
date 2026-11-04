# Issue 348 evidence

## Candidate and scope

The candidate is the implementation branch for [issue #348](https://github.com/douglasjarquin/sum/issues/348), based on `9456017f2e4fde8f16bc4f2d1bdd7ba978765eb3`.
Final checked commit identities and hosted CI observations are recorded in the PR and canonical run artifact.
The serving installation was not edited, activated, or migrated.
Obsolete development-branch files were moved to macOS Trash, with historical source also retained in Git.

## Observed local artifacts

| Component | Observed artifact |
| --- | --- |
| HC | 0.1.17; SHA256 b0ad77ab78ca8d9a8b7fb2ddd02c6f23fcbd1fa6cd2dafd39574adb71e96fc4a |
| Herdr app | 0.1.12; SHA256 c5b7f631979405fdae7ca0a05704087af1740fd3e66dbf39a1567e546f291d32 |
| Codex | codex-cli 0.166.0; installed interactive --no-daemon flag inspected |
| Claude Code | 2.1.318; installed CLI only |

Read-only commands inspected `hc --skill`, version/help, run/send/wait/get/read/kill/rm help, app skill/version/help, and harness version/help.
No matching live native recipient or authenticated turn was established.
These observations support writing against the installed command contract; they establish no minimum-version or all-harness guarantee.

## Evidence and limitations

The initial instruction-package test failed because the baseline lacked three of the four core procedures and still shipped bin/sumctl.
The protected Bot/shared factory baseline comparison passed before implementation.
Meaningful retained behavioral coverage includes portable scaffold, verifier, audit, evidence capture/publication, Git fixture isolation, Bot recipes, and Bot invariant grading.
The website retains its Astro/Playwright checks and static route contract.

Every authenticated native scenario in [the acceptance matrix](../ACCEPTANCE.md) is **not-run**: ordinary entry, parentage, background work, test exit arrival, explicit reviewer return, correction/re-review, question/answer, live role isolation, startup uncertainty, busy/blocked/timeout, native missing-prerequisite exercise, resume/shell lifetime, and live cleanup.
Reason: this API conversation has no HC_TERMINAL_ID and cannot receive native input as an addressable lead.
No native launch, send, stop, shared-core restart, invented identity, or fixture substitute was used to conceal that gap.
Issue Phase 1 explicitly permits continuing implementation while leaving this acceptance unrun.
The mixed Claude-lead/Codex-reviewer combination remains a proposed qualification path.

Package preservation and source removal are offline evidence only.
No production cutover, fresh authenticated app workflow, legacy factory execution, or guaranteed unattended recovery is claimed.

## Reproduce

Use [VERIFY.md](../../VERIFY.md) for the canonical offline run and policy review.
Use [native prerequisites](../native-hc.md) and the full [acceptance matrix](../ACCEPTANCE.md) for a later authenticated qualification, recording commands, native addresses, final candidate identity, actual reports, and results.
