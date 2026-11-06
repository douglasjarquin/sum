# Open-issue audit

Audited on 2026-11-06 against `main` at `d55b355`, after merged PR #355.
The user assigned this session the repository developer role and authorized issue closure and unfinished implementation.
No production Bot, backup scope, account, shared core, or deployment was changed.

## Dispositions

| Issue | Finding | Disposition |
| --- | --- | --- |
| [#354](https://github.com/douglasjarquin/sum/issues/354) | PR #355 delivers the scoped Grok instruction repairs and evidence; the issue permits source completion with live acceptance explicitly outstanding | Closed completed; no live deployment or backup readiness claimed |
| [#330](https://github.com/douglasjarquin/sum/issues/330) | Its runtime/observer/receipt design was deliberately superseded by #348 and removed in PR #349 | Closed not planned, as superseded; historical acceptance was not certified |
| [#335](https://github.com/douglasjarquin/sum/issues/335) | Runtime repairs landed before #348 removed those components | Closed not planned, as superseded; remaining current native acceptance stays with #348 |
| [#348](https://github.com/douglasjarquin/sum/issues/348) | Instruction refactor merged, but authenticated native acceptance remains unrun | Open; actual HC/app/harness round trip, correction/review, uncertainty, isolation, and lifecycle evidence still required |
| [#323](https://github.com/douglasjarquin/sum/issues/323) | No CE configuration, current example, or house-rules pack existed | Implemented in this candidate; issue remains open pending merge |
| [#322](https://github.com/douglasjarquin/sum/issues/322) | Readable static HTML existed, but discovery, semantic snippets, explicit cost information, and structured metadata were incomplete | Scored below; remediation in this candidate, hosted read-back pending deployment |

## Compound Engineering configuration

The installed CE 3.36.0 setup health check initially reported the missing example configuration, no team defaults, and unignored scratch state.
The updated health check reports `Project config healthy`, `Artifact root: docs/ (from config.yaml)`, native implementation, a current example, and `pack house-rules`.
The optional `agent-browser` executable is absent; the existing Playwright browser provides this repository's required browser verification.
No optional tool installation or machine-wide configuration changes are needed.

The team configuration deliberately keeps Markdown artifacts under `docs/`, uses the current implementation harness, disables automatic cross-provider review, skips routine scoping confirmation, and enables normal PR CI follow-up.
It pins no model, account, permission mode, external engine, or shell command.
Automatic cross-provider review being off does not waive independent review requirements in `VERIFY.md` or explicit user-selected review.
Local overrides and scratch are ignored, and no local override is created.
The example is copied byte-for-byte from the installed plugin template.
The house-rules pack covers the actual instructions, portable tools, and Astro site rather than the removed Go runtime.

## AgentReady scope and method

Reference: [AgentReady v2.0, October 2026](https://agentready.org/llms.txt), fetched on 2026-11-06, and the [first-party guide](https://agentready.org/).
The audited surface is the public static documentation site at `https://douglasjarquin.github.io/sum/`.
Sum is an instruction distribution, with no hosted transactional product, public API, sign-in, checkout, or operational web UI.
The Act criteria are therefore inapplicable to this website; inventing a Sum CLI or MCP server would contradict its current product boundary.
Native HC functionality belongs to the separately installed upstream tool and is qualified through `docs/ACCEPTANCE.md`.

The score is an explicit local rubric, not an official AgentReady or Ora score.
Each applicable criterion has equal weight: Pass = 1, Partial = 0.5, Fail = 0; N/A is excluded.
Optional applicable criteria remain in the denominator so unimplemented MAYs remain visible.
The baseline scores **6.5/13 (50.0%)** and the remediated local build scores **11/13 (84.6%)** after verification.
All three applicable MUSTs pass the sampled checks; all eight applicable SHOULDs pass after remediation.
The two unimplemented MAYs account for the remaining gap.
Local build results do not establish that the changes have deployed.

| Criterion | Strength | Before | After | Evidence or applicability |
| --- | --- | --- | --- | --- |
| AR-FIND-01 | MUST | Pass | Pass | Homepage returned 200 and readable HTML with `Claude-User/1.0`; origin `/robots.txt` returned 404, not a disallow policy; no origin bot settings were changed |
| AR-FIND-02 | SHOULD | Pass | Pass | First-party `/sum/docs/` and seven linked documentation sections |
| AR-FIND-03 | SHOULD | Fail | Pass | Baseline `/sum/sitemap.xml` returned 404; official Astro integration emits linked `/sum/sitemap-index.xml` and a nine-route `/sum/sitemap-0.xml` |
| AR-FIND-04 | MAY | Fail | Fail | No AI Catalog; optional and not added |
| AR-READ-01 | MUST | Pass | Pass | Static HTML contains the answers; Playwright also reads the candidate with JavaScript disabled |
| AR-READ-02 | MUST | Pass | Pass | Hosted missing path returned 404; local preview missing path also checked; no application throttler exists to exercise a 429 |
| AR-READ-03 | SHOULD | Partial | Pass | Homepage links docs/start and now a visible cost/license anchor |
| AR-READ-04 | SHOULD | Pass | Pass | Start, HC prerequisites, procedures, verification, and adoption answers are in HTML |
| AR-READ-05 | SHOULD | Partial | Pass | All three website command blocks now use `pre`/`code` with `language-sh`; source Markdown retains language-tagged fences |
| AR-READ-06 | SHOULD | N/A | N/A | No hosted auth, API, or tool protocol requiring `.well-known` discovery; optional catalog is scored separately |
| AR-READ-07 | SHOULD | Fail | Pass | Linked `/sum/llms.txt` describes canonical docs, source, prerequisites, and qualification limits |
| AR-READ-08 | SHOULD | Fail | Pass | Shared layout emits truthful SoftwareSourceCode JSON-LD; essential product/cost information is also visible text |
| AR-READ-09 | MAY | Fail | Fail | No website Markdown mirror; GitHub source documents remain linked, but are not counted as mirrors |
| AR-READ-10 | Not defined | N/A | N/A | Referenced by the old issue checklist but absent from fetched v2.0; no invented requirement |
| AR-READ-11 | SHOULD | Partial | Pass | Homepage states MIT, no paid plan, and separate agent/infrastructure costs; no fabricated pricing tier |
| AR-ACT-10 | MUST | N/A | N/A | Documentation-only site; no product action surface |
| AR-ACT-01 | SHOULD | N/A | N/A | No website authentication |
| AR-ACT-02 | SHOULD | N/A | N/A | No public hosted product API |
| AR-ACT-03 | SHOULD | N/A | N/A | No website tool operations requiring MCP |
| AR-ACT-04 | MAY | N/A | N/A | No MCP server to advertise with an emerging server card |
| AR-ACT-05 | MAY | N/A | N/A | No operational app UI to embed |
| AR-ACT-06 | SHOULD | N/A | N/A | Instructions are consumed directly; no Sum SDK or CLI product |
| AR-ACT-07 | SHOULD | N/A | N/A | No agent-operable website actions |
| AR-ACT-08 | MAY | N/A | N/A | No payment or checkout |
| AR-ACT-09 | MAY | N/A | N/A | No hosted natural-language question service |

Reachability is sampled evidence, not proof about every crawler network.
GitHub Pages access logs and platform throttling controls are unavailable to this developer session, so verified crawler-IP acceptance and actual provider 429/Retry-After responses remain unobserved.
An absent root robots file permits crawling under the [Robots Exclusion Protocol](https://www.rfc-editor.org/rfc/rfc9309#section-2.3.1.3).
A project-path `/sum/robots.txt` would not control the origin crawl policy, so no misleading project-local robots file is added.
Likewise the optional origin-root AI Catalog cannot be honestly advertised by shipping an unrelated project-path file.

## Verification and evidence

Before evidence uses the baseline build and actual hosted responses: homepage 200, `llms.txt` 404, `sitemap.xml` 404, missing route 404, and origin robots 404.
The baseline browser capture records no linked `llms.txt` and no JSON-LD.
Candidate browser checks cover the published index, the sitemap index and child URLs, no-JavaScript content, canonical metadata, and real missing-route status.
All nine sitemap page URLs must retain `/sum/`, and the 404 must not be indexed.
Screenshots cover the same desktop home before/after, plus candidate mobile and dark mode.
Raw captures, check logs, and the complete prescribed-check inventory are retained under `.artifacts/issue-audit/`; local paths are not published evidence URLs.
The PR records actual aggregate, lint, browser, independent review, and hosted CI outcomes for its candidate.
No passing static result closes #348 or qualifies a live Grok deployment.
