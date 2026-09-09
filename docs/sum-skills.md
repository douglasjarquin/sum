# Explicit Git skill delivery

Sum can inspect and install selected skills from a Git repository without treating the repository as executable code.

The first interface requires a repository, an explicit Git ref, and an exact relative skill directory.

Inspect one selection without writing a target worktree:

```sh
bin/sumctl skills inspect \
  --repository /path/to/plugins \
  --ref <full-commit-or-ref> \
  --path pstack/skills/<skill-name>
```

Install one or more selections into an existing external worktree:

```sh
bin/sumctl skills install \
  --target /path/to/worktree \
  --selection /path/to/plugins <full-commit> pstack/skills/<skill-name>
```

Repeat `--selection` for additional exact directories from the same or another repository.

Use `--route .claude/skills` when the target harness reads that native route.

The default route is `.agents/skills`.

Each install resolves the ref once, records the full commit, and stores upstream bytes under `.sum-skills/snapshots/` with a content-hashed selection lock at `.sum-skills/selection-lock.json`.

The native discovery entry is a symlink to that immutable snapshot, so Sum-owned `sum-*` directories and upstream names have separate ownership.

The installer preserves relative resources and executable modes, copies root license or notice files into the snapshot, and refuses missing resources, unsafe symlinks, unsupported file types, malformed frontmatter, denied transports, duplicate names, reserved `sum-` names, and destination overwrites.

One install invocation accepts at most 32 MiB of selected resources, with an 8 MiB per-file limit and a 64 MiB temporary-repository limit for remote sources.

Governing-license expansion is limited to 64 files, and offline checks refuse non-regular or oversized selection locks, oversized or over-aggregate snapshot content, and overlarge snapshot trees.

Local repository paths are stored as canonical absolute locators, and each lock update must satisfy the same record and byte limits as the next read.

The installer never invokes a skill, grants permissions, enables MCP or model settings, changes global Cursor or Claude configuration, follows submodules, or runs upstream hooks and scripts.

Use `bin/sumctl skills check --root /path/to/worktree` to validate installed hashes without network access.

This first version does not infer dependencies from prose, parse browser URLs, install an entire plugin, generate aliases, or rewrite native names.
