# Verification contract

Small CLI fixture. Verify with `mise run verify`; the portable procedure is `.agents/skills/verify/SKILL.md`.

```verify
entrypoint = "mise run verify"
feature_maps = "docs/features/README.md"
artifacts = ".artifacts/verification"

[requires]
commands = ["python3"]
```

## Setup

Nothing to install beyond Python 3.

## Readiness

`mise tasks ls` lists `verify` from this repository's `mise.toml`.

## Automated checks

| Check | Command | Proves |
| --- | --- | --- |
| Unit and CLI tests | `python3 -m unittest discover -s tests -p 'test_*.py'` | `greet()` and the `hello.py` entrypoint |

## Scenarios

See `docs/features/cli.md`.

## Isolation

Tests write nothing outside the process.

## Artifacts

`.artifacts/verification/<run-id>/run.json` and `verify.log`, Git-ignored.

## Teardown

Nothing runs after the checks.
