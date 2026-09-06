---
name: verify
description: Verify a repository through its project-root VERIFY.md contract - run the canonical `mise run verify`, record candidate-bound evidence, and report manual scenarios truthfully. Works in any clone without sum or Herdr.
---
# Verify

This skill is portable: everything it needs lives inside the repository you are verifying.
It does not require sum, Herdr, a `.sum` directory, or any absolute path outside this checkout.
If your harness has no skill discovery, read `VERIFY.md` at the project root and follow it directly; this file only adds procedure.

## Procedure

1. Resolve the real project root with `git rev-parse --show-toplevel`. Do not assume `.git` is a directory: a linked worktree keeps a `.git` file, and a managed clone may sit under another repository.
2. Read `VERIFY.md` at that root. It names the canonical entrypoint `mise run verify`, setup/readiness/teardown steps, the feature-map index, required automated checks, real-behavior scenarios, isolation prerequisites, and the artifact directory. Follow the setup and readiness steps it lists before running anything.
3. Run the recorded runner from the root:

   ```sh
   python3 .agents/skills/verify/scripts/verify_run.py --check   # contract, maps, task ownership, requirements; runs nothing
   python3 .agents/skills/verify/scripts/verify_run.py           # runs `mise run verify`, writes .artifacts/verification/<run-id>/
   ```

   Pass `--base REF` (for example the branch's merge base) so edits to `VERIFY.md`, mise tasks, or feature maps since that ref are flagged for root review. Add `--scenario ID=pass|fail|blocked|not-applicable[:reason]` for each manual or interactive scenario you actually exercised; unmentioned manual scenarios are recorded as `not-run`.
   While you drive a mapped feature by hand or through its real entrypoint, record what happened beside that record:

   ```sh
   python3 .agents/skills/verify/scripts/verify_capture.py --feature <id> run --expect-text 'Hello' -- <command> <args>
   python3 .agents/skills/verify/scripts/verify_capture.py --feature <id> http --expect-status 200 GET http://127.0.0.1:<port>/<route>
   ```

   Each capture writes `<run-dir>/evidence/NNN-<id>.json` with the exact command or request, the observed output or body, and whether your stated expectations were met (exit 1 when not). It starts and stops nothing; a browser or desktop surface without an installed driver stays a `--scenario` report.
   For a before/after proof of one scenario (screenshots, a screencast, or transcripts from the base and the candidate builds) use `.agents/skills/evidence/`; a map row whose Evidence cell names a screenshot, screencast, or red/green pair is reported by the runner as `required visual evidence ... missing` until a comparison manifest for the candidate exists under the contract's `evidence` directory.
4. Read the printed record path and report from it: outcome, candidate SHA, `dirty`, `provisional`, `certifies`, scenarios by status, `not_exercised`, and `requires_root_review`. Paste the path, not the log.
5. Follow the teardown steps in `VERIFY.md`, then confirm the run directory and its `evidence/` files still exist; cleanup removes what the run started, never the proof.

To standardize a repository that has no `VERIFY.md` yet, use `.agents/skills/create-verification/`; to keep an existing contract and its maps honest after a change, use `.agents/skills/maintain-verification/`.

## What the outcomes mean

- `pass`: `mise run verify` exited 0, mapped automated scenarios passed, build outputs (when declared) are fresh, and every manual scenario you reported passed. `certifies: <sha>` appears only for a clean tree whose policy files were compared with `--base` and did not change; without `--base` the record says `requires_root_review` and certifies nothing.
- `fail`: the entrypoint exited non-zero, a declared build output is stale, or a reported scenario failed. The log stays under the run directory.
- `blocked`: the run could not be trusted - missing or malformed `VERIFY.md`, a missing or inherited `verify` task, a missing required command, or a timeout. Blocked is never a pass.
- `not-run` / `not-applicable` on a scenario: it was not exercised, or it does not apply with the stated reason. A passing aggregate command says nothing about these.

## Rules

- Never edit `VERIFY.md`, feature maps, tests, or tasks to make a run pass. A candidate that changes them is flagged `requires_root_review` and cannot certify itself. The default policy set (`VERIFY.md`, `mise.toml`, `mise-tasks/`, `.agents/skills/verify/`, `.agents/skills/evidence/`, `.agents/skills/create-verification/`, `.agents/skills/maintain-verification/`, and every feature map) cannot be shrunk by the candidate; `policy_files` in the contract only adds paths.
- Run only the task the runner validated as this repository's own. An inherited task from a parent directory belongs to another project.
- A dirty working tree gives a provisional record. Commit first when you need a result bound to an exact SHA.
- Use the repository's existing isolation (temporary directories, lab sessions, ephemeral ports). No production credentials, no shared-data mutation.
- The record is evidence for a human or a root session to review; it is not approval and it does not merge anything.
