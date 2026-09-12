# Project-local third-party skills

Explicit third-party skill and agent selections are copied into one Git project through the pinned Vercel Skills CLI.
Sum keeps ownership of its reserved `sum-*` namespace and its own projection checks.
Implementation and focused boundary coverage are in `go/internal/skills/skills.go` and `go/internal/cli`.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `skills.project-copy` | Installing explicit skill and agent names runs the pinned Vercel CLI from the Git project root with `--copy --yes`, leaving no project skill symlink or user-level skill write | automated: `go/internal/skills/skills.go`; manual: real pinned CLI fixture | CLI transcript |
| `skills.explicit-selection` | Wildcards, option-looking values, non-project targets, and requested `sum-*` names are refused before the third-party CLI runs | automated: `go/internal/skills/skills.go` | offline suite |
| `skills.sum-inventory` | Checking Sum itself still validates its owned names, projections, and portable imports, and refuses leftover unprefixed aliases | automated: `go/internal/skills/skills.go`, `go/internal/skills/skills_test.go`, `tests/test_setup.py` | offline suite |
| `skills.review-workflow` | A real project-local copy from vercel-labs/agent-skills, an unchanged re-review, and an additive second-skill review each produce the diff `docs/sum-skills.md` describes; the pinned CLI's own `update` relocates an `--agent`-scoped copy instead of refreshing it in place | manual: real pinned CLI against a throwaway Git project, documented in `docs/ACCEPTANCE.md` §11 | CLI transcript + Git diff |
