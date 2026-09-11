# Project-local third-party skills

Sum delegates third-party skill discovery and copying to the pinned [Vercel Skills CLI](https://github.com/vercel-labs/skills).
The wrapper requires an explicit project, skill name, and agent name:

```sh
bin/sumctl skills install \
  --target /path/to/git-project \
  --source owner/repository \
  --skill skill-name \
  --agent codex
```

Repeat `--skill` and `--agent` to request more explicit names.
Sum invokes `skills add SOURCE --skill ... --agent ... --copy --yes` from the target project root.
Project scope is the upstream default, so the wrapper does not pass a nonexistent `--project` flag and never passes `--global`.
The target must be the root of an existing Git project so upstream's non-interactive scope detection cannot fall back to a user-level install.

Wildcards, option-looking names, option-looking sources, and the reserved `sum-*` namespace are refused before the upstream tool runs.
`bin/sumctl skills check --root /path/to/sum` continues to validate Sum-owned skill names, projections, portable imports, and compatibility references.
It does not checksum or certify third-party skill content.

Vercel Skills owns source parsing, discovery, copying, and its `skills-lock.json` format.
The `--yes` mode can overwrite a same-named third-party destination, so inspect the target's Git diff after installation and review copied skills before use.
Sum does not run a copied skill, update it automatically, select every discovered skill, or change model, MCP, permission, or global harness settings.

## Review workflow

Ordinary Git and PR review do the reviewing; the steps below are the same as reviewing any other
third-party dependency bump, applied to a skill copy.

1. **List a source's skills, read-only, before choosing anything.** This does not go through
   `sumctl`; run the pinned CLI directly:

   ```sh
   .local/bin/skills add OWNER/REPOSITORY --list
   ```

   For example, `.local/bin/skills add michael-denyer/pstack-claude --list` lists that
   repository's skills (`architect`, `tdd`, `why`, and dozens more) with their descriptions.
   Listing selects nothing: which skill, if any, to adopt from a source stays the owner's
   decision, made explicitly in the next step.

2. **Copy the explicit selection** into a real Git project:

   ```sh
   bin/sumctl skills install \
     --target /path/to/git-project \
     --source vercel-labs/agent-skills \
     --skill writing-guidelines \
     --agent claude-code
   ```

   A real run of this exact command against a throwaway project wrote exactly two project-local
   regular files: `.claude/skills/writing-guidelines/SKILL.md` and an updated
   `skills-lock.json`. Nothing was written outside the target project or to a global/user-level
   location.

3. **Inspect the Git diff before committing anything.** Treat every copied instruction, script,
   resource, and stated permission as untrusted content — copying a skill is not authorization to
   run it or change tool/account settings. `writing-guidelines` itself, for instance, instructs an
   agent to "fetch the latest guidelines from the source URL below"; a reviewer decides whether
   that outbound fetch is acceptable before the skill is ever invoked, the same as reviewing any
   other third-party dependency change.

4. **Commit and open an ordinary PR.** Human review and merge happen exactly like any other
   change to the project; merging this PR is not an installation activation for Sum itself (that
   is the separate release/update mechanism).

5. **Re-run the same explicit command to propose an update, rather than the CLI's own `update`.**
   A repeat of an unchanged selection reproduces byte-identical files, so `git status` reports
   nothing to commit. A repeat that adds a new explicit skill produces an ordinary additive diff
   scoped to that skill plus `skills-lock.json`: a real run adding a second skill to an
   already-copied project left the first skill's files completely untouched and only added the
   second skill's files and a new `skills-lock.json` entry.

`bin/sumctl skills check --root` is the unrelated check in this area: it validates Sum's own
`sum-*` namespace and projections, never third-party content. Reviewing a copied third-party
skill is ordinary Git/PR review, not a Sum-run verification step.

## Do not use the pinned CLI's own `update`

Vercel Skills' `update`/`upgrade` subcommand is not part of this workflow, and a real run shows
why: against a project holding two skills explicitly copied with `--agent claude-code`, running
the pinned CLI's own `update -p -y` directly relocated both copies out of `.claude/skills/` into
a different `.agents/skills/` layout — dropping the originally requested agent target instead of
refreshing it in place — while `skills-lock.json`'s recorded hashes advanced regardless, with
nothing in the CLI's own output calling out the relocation. Re-run the explicit
`add`/`bin/sumctl skills install` command from step 2 above to check for or apply an update
instead; it is idempotent for an unchanged selection and reproduces the correct project-local
layout for a changed one.
