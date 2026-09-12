# Go sumctl

`bin/sumctl` execs the staged cgo-free binary at `.local/bin/sumctl`, built from `go/cmd/sumctl`.

`--version` prints `sum 0.1.0`.
`--home` defaults to `SUM_HOME` or `<installation>/.sum`.
There is no Python CLI and no `SUM_PYTHON_HELPER` compatibility path.

Build:

```
(cd go && go build -trimpath -buildvcs=false -o ../.local/bin/sumctl ./cmd/sumctl)
```
