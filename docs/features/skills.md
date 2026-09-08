# Explicit Git skill delivery

Selected upstream skills remain pinned, hash-checked, and separate from Sum-owned wrappers.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `skills.inspect` | Inspecting a repository, ref, and exact relative skill directory reports native metadata and capability limits without writing a target | automated: `tests/test_skill_install.py` | offline suite |
| `skills.install` | Installing one or more selected directories preserves resources and modes, records the selection lock, supports both native routes, and refuses unsafe or colliding inputs | automated: `tests/test_skill_install.py` | offline suite |
| `skills.verify-installed` | Checking a target validates every snapshot hash and projection without fetching the source again | automated: `tests/test_skill_install.py` | offline suite |
