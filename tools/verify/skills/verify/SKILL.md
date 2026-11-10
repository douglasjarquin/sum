---
name: verify
description: Verify a repository through its project-root VERIFY.md contract - run the canonical `mise run verify`, record candidate-bound evidence, and report manual scenarios truthfully. Works in any clone without sum or Herdr.
---
# Verify

This skill is portable: everything it needs lives inside the repository you are verifying.
It does not require sum, Herdr, a `.sum` directory, or any absolute path outside this checkout.
Every command goes through the wrapper `.agents/skills/verify/scripts/sum-verify`, which runs the tool version the project pinned, fetching and checksum-verifying it on first use.
If your harness has no skill discovery, read `VERIFY.md` at the project root and follow it directly; this file only adds procedure.
Use `.agents/skills/verify/references/engineering-principles.md` as the shared review rubric when the project carries it; it adds procedure and does not replace project-owned architecture or checks.

## Procedure

1. Resolve the real project root with `git rev-parse --show-toplevel`. Do not assume `.git` is a directory: a linked worktree keeps a `.git` file, and a managed clone may sit under another repository.
2. Read `VERIFY.md` at that root. It names the canonical entrypoint `mise run verify`, setup/readiness/teardown steps, the feature-map index, required automated checks, real-behavior scenarios, isolation prerequisites, and the artifact directory. Follow the setup and readiness steps it lists before running anything.
3. Run the recorded runner from the root:

   ```sh
   .agents/skills/verify/scripts/sum-verify run --check   # contract, maps, task ownership, requirements; runs nothing
   .agents/skills/verify/scripts/sum-verify run           # runs `mise run verify`, writes .artifacts/verification/<run-id>/
   ```

   Pass `--base REF` (for example the branch's merge base) so edits to `VERIFY.md`, mise tasks, or feature maps since that ref are flagged for root review. Add `--scenario ID=pass|fail|blocked|not-applicable[:reason]` for each manual or interactive scenario you actually exercised; unmentioned manual scenarios are recorded as `not-run`.
   While you drive a mapped feature by hand or through its real entrypoint, record what happened beside that record:

   ```sh
   .agents/skills/verify/scripts/sum-verify capture --feature <id> run --expect-text 'Hello' -- <command> <args>
   .agents/skills/verify/scripts/sum-verify capture --feature <id> http --expect-status 200 GET http://127.0.0.1:<port>/<route>
   ```

   Each capture writes `<run-dir>/evidence/NNN-<id>.json` with the exact command or request, the observed output or body, and whether your stated expectations were met (exit 1 when not). It starts and stops nothing; a browser or desktop surface without an installed driver stays a `--scenario` report.
   For a before/after proof of one scenario (screenshots, a screencast, or transcripts from the base and the candidate builds) use `.agents/skills/evidence/`; a map row whose Evidence cell names a screenshot, screencast, or red/green pair is reported by the runner as `required visual evidence ... missing` until a comparison manifest for the candidate exists under the contract's `evidence` directory.
4. Read the printed record path and report from it: outcome, candidate SHA, `dirty`, scenarios by status, `not_exercised`, and `policy.changed`. Retain the detailed record in task artifacts, not the PR description.
5. Follow the teardown steps in `VERIFY.md`, then confirm the run directory and its `evidence/` files still exist; cleanup removes what the run started, never the proof.

To standardize a repository that has no `VERIFY.md` yet, use `.agents/skills/create-verification/`; to keep an existing contract and its maps honest after a change, use `.agents/skills/maintain-verification/`.

## PR summary

When reporting verification on a PR, use a compact `## Pipeline` table with `Stage`, `Status`, and `Result` columns, matching the category-level format of Sum PR #326.
Use one row per category such as Review, Test, Document, Lint, and CI, plus relevant delivery stages; do not enumerate individual tests, scenario IDs, or execution instances.
Keep Result to a short outcome and an accessible run/report link when available.
Keep the complete check inventory, individual scenario statuses, logs, and candidate metadata in the verification record or task artifacts outside the PR body.
Preserve a `provisional`, `uncompared`, or `needs-root-review` outcome in the short summary.

Use ✅ Passed only when every required check within the category's stated scope passed.
For mixed outcomes use ❌ Failed if any required check failed, otherwise ⛔ Blocked if any is blocked, otherwise ⬜ Not run if any is unrun.
Use ➖ Not applicable only when the whole category does not apply, with a reason.
Summarize the gap without listing every affected scenario.
Keep local automated verification, hosted CI, manual/native acceptance, and production acceptance in separate categories when applicable; an aggregate pass does not establish unexercised acceptance.
A machine-local path is not an accessible PR link; state missing publication briefly rather than pasting the full record.
Verification reporting alone does not authorize creating or editing a PR.

## What the outcomes mean

The outcome is the first line the runner prints and the record's `outcome` field. The first that applies wins:

- `blocked`: the run could not be trusted - missing or malformed `VERIFY.md`, a missing or inherited `verify` task, a missing required command, or a timeout. Blocked is never a passing outcome. Exit 2.
- `fail`: the entrypoint exited non-zero, a declared build output is stale, or a reported scenario failed. The log stays under the run directory. Exit 1.
- `provisional`: the checks passed on a dirty tree, so nothing is bound to a SHA. Commit, then rerun.
- `uncompared`: the checks passed on a clean tree, but no `--base` was given, so policy files were not compared. Rerun with `--base <merge-base>`.
- `needs-root-review`: the checks passed on a clean tree whose policy files changed since `--base`; `policy.changed` lists them. Obtain independent root review.
- `certified`: the checks passed on a clean tree whose policy files match `--base`. The result is bound to the candidate SHA.
- `checked`: `--check` validated the contract, maps, task ownership, and requirements without running anything.

The four outcomes after `fail` mean `mise run verify` exited 0, mapped automated scenarios passed, declared build outputs are fresh, and no reported manual scenario failed; they and `checked` exit 0.
- `not-run` / `not-applicable` on a scenario: it was not exercised, or it does not apply with the stated reason. A passing aggregate command says nothing about these.

## Rules

- Never edit `VERIFY.md`, feature maps, tests, or tasks to make a run pass. A candidate that changes them reports `needs-root-review` and cannot certify itself. The default policy set (`VERIFY.md`, `mise.toml`, `mise-tasks/`, `.agents/skills/verify/`, `.agents/skills/evidence/`, `.agents/skills/create-verification/`, `.agents/skills/maintain-verification/`, and every feature map) cannot be shrunk by the candidate; `policy_files` in the contract only adds paths.
- Run only the task the runner validated as this repository's own. An inherited task from a parent directory belongs to another project.
- A dirty working tree gives a `provisional` outcome. Commit first when you need a result bound to an exact SHA.
- Use the repository's existing isolation (temporary directories, lab sessions, ephemeral ports). No production credentials, no shared-data mutation.
- The record is evidence for a human or a root session to review; it is not approval and it does not merge anything.
