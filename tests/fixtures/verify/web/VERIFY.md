# Verification contract

Web/service fixture: a built static page served by a tiny HTTP server. Verify with `mise run verify`; the portable procedure is `.agents/skills/verify/SKILL.md`.

```verify
entrypoint = "mise run verify"
feature_maps = "docs/features/README.md"
artifacts = ".artifacts/verification"

[requires]
commands = ["python3"]

[freshness]
inputs = ["src"]
outputs = ["dist"]
```

## Setup

Nothing to install beyond Python 3. `mise run build` renders `src/` into `dist/`.

## Readiness

`mise tasks ls` lists `build` and `verify` from this repository's `mise.toml`. No port is reserved; the check picks an ephemeral loopback port.

## Automated checks

| Check | Command | Proves |
| --- | --- | --- |
| Build, serve, fetch | `python3 check.py` | The built page is served and rendered; the server is stopped afterwards |

## Scenarios

See `docs/features/web.md`.

## Isolation

Loopback only, ephemeral port, no data store.

## Artifacts

`.artifacts/verification/<run-id>/run.json` and `verify.log`, Git-ignored. `dist/` is a build output, not an artifact.

## Teardown

`check.py` terminates the server it started.
