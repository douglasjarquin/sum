# Contributing

Change Sum in an independent development checkout or Git worktree on a feature branch.
Never edit, test, or update a serving legacy installation while its workers rely on its source or absolute helper paths.
Inspect Git status and preserve unrelated work before editing.

Read [AGENTS.md](AGENTS.md), [Develop](skills/sum-develop/SKILL.md), and [VERIFY.md](VERIFY.md).
Maintaining Sum uses the current coding environment, not an HC operator session.
Direct source investigation, issue/PR maintenance, implementation, checks, and supported independent review can proceed without `HC_TERMINAL_ID`.
HC is required only for actual HC operations and native acceptance; unavailable integration evidence blocks that scenario, not all repository work.
Ordinary use needs no Sum build or setup; the pinned mise tools are optional repository development dependencies.
Keep instructions short, use native HC directly when operating HC, and avoid replacing removed runtime machinery with scripts or mandatory prose bookkeeping.

Run meaningful targeted checks while iterating and the canonical verifier once against the final committed candidate and merge base.
Verification policy changes require independent review; a candidate cannot certify its own new standard.
Native scenarios require actual authenticated conversation evidence and isolated lifecycle tests, not fixtures or CLI help alone.
Document unrun scenarios honestly in [acceptance](docs/ACCEPTANCE.md).

Respect the [separate Bot product boundary](docs/legacy.md) and historical credits.
Explicit Grok repair tasks may change their named pack sources without changing native HC procedures or protected legacy factory settings.
For issue #354, the implementation scope includes only the affected existing byte/text assertions and the missing Factory enumeration; no new tests, fixtures, or framework are authorized.
Preserve all unaffected immutable-baseline checks and retained code-comment/test policies.
This permission is specific to #354, not general permission for Grok workers to rewrite tests.
For issue #357, the authorized existing-assertion maintenance is limited to maintainer skill discovery, entry/template links, and affected website discovery expectations.
Do not add a test suite, fixture catalog, runtime, or unrelated assertion changes for that routing repair.
Do not manually edit generated output or CHANGELOG files.
Write PR titles as Conventional Commits; the squash-merge title becomes the commit on main.
Release-please generates the `sum-verify` release notes from those titles: a `feat`, `fix`, or `deps` change under `tools/verify/` opens or updates the release PR, and other types stay out of the changelog.
A Go pin bump in `mise.toml` ships a new compiler, so it also sets the `go` line in `tools/verify/go.mod` to the same version (a test enforces this) and uses a `fix(deps):` title; that is what qualifies it for a release.
Merging the release PR tags `sum-verify-vX.Y.Z` and publishes the binaries; never push those tags by hand.
If publishing fails, rerun the Release Please workflow or push to main; either finishes the approved draft.
To correct a merged title before its release, add a `BEGIN_COMMIT_OVERRIDE` block to that PR's body; it takes effect on the next push to main.
Prepare a focused PR with changed behavior, verification, limitations, and actual CI state.
The user decides merging and adoption; neither a branch nor a PR updates a running installation.
