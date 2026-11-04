# Optional portable-tool tests

These stdlib-only Go tests exercise the standalone Python verification, scaffold, audit, and evidence tools against disposable repositories.
They build no Sum runtime and are not required for ordinary instruction use.
Run through the root verification task, or use `python3 ../../scripts/fixture_git_env.py -- go test ./...` here.
