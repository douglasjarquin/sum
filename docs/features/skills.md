# Explicit Git skill delivery

Selected upstream skills remain pinned, hash-checked, and separate from Sum-owned wrappers.

Implementation entry points are `lib/skill_content.py`, `lib/skill_source.py`, `lib/skill_snapshot.py`, `lib/skill_install.py`, and the `skills` dispatch in `lib/sumctl.py`.
Regression coverage is in `tests/test_skill_install.py`, `tests/test_skill_install_review.py`, `tests/test_skill_install_regressions.py`, and `tests/test_setup.py`.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `skills.inspect` | Inspecting a repository, ref, and exact relative skill directory reports native metadata and capability limits without writing a target | automated: `tests/test_skill_install.py` | offline suite |
| `skills.install` | Installing one or more selected directories preserves resources and modes, records the selection lock, supports both native routes, and refuses unsafe or colliding inputs | automated: `tests/test_skill_install.py` | offline suite |
| `skills.verify-installed` | Checking a target validates every snapshot hash and projection without fetching the source again | automated: `tests/test_skill_install.py` | offline suite |
