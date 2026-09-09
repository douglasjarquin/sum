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
