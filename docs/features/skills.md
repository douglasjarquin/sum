# Project-local third-party skills

Explicit third-party skill and agent selections are copied into one Git project through the pinned Vercel Skills CLI.
Sum keeps ownership of its reserved `sum-*` namespace and its own projection checks.
Implementation and focused boundary coverage are in `lib/sumctl.py` and `tests/test_skills_cli.py`.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `skills.project-copy` | Installing explicit skill and agent names runs the pinned Vercel CLI from the Git project root with `--copy --yes`, leaving no project skill symlink or user-level skill write | automated: `tests/test_skills_cli.py`; manual: real pinned CLI fixture | CLI transcript |
| `skills.explicit-selection` | Wildcards, option-looking values, non-project targets, and requested `sum-*` names are refused before the third-party CLI runs | automated: `tests/test_skills_cli.py` | offline suite |
| `skills.sum-inventory` | Checking Sum itself still validates its owned names, projections, portable imports, and compatibility references | automated: `tests/test_skill_namespace.py`, `tests/test_setup.py` | offline suite |
