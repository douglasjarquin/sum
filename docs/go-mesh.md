# Sum-owned Go Mesh

`bin/herdr-mesh` starts the Sum-owned Herdr Mesh MCP server.

The server is the cgo-free binary built from `go/cmd/herdr-mesh` and staged as `.local/bin/herdr-mesh`.

The wrapper selects that binary from the checkout, or from `.local/current` once a release is the default.

The root Cobra command starts the MCP stdio server with no extra subcommand.

`--help` and `--version` are metadata-only paths and never open Sum state or Herdr.

The MCP server uses the pinned official Go SDK v1.6.1 for protocol framing, capabilities, request cancellation, and stdio lifecycle.

The ten tool names, schemas, defaults, bounded reads and waits, prompt uncertainty, and result meanings are owned by `go/internal/mesh`.

Each operation validates the calling Herdr environment, explicit session, registered pane, Sum instance, and developer read-only boundary before invoking the verified Herdr CLI.

Setup and release staging install this binary.

`patches/herdr-mesh/` is not the live server.
A helper from before this cutover still copies those files when it stages this tree.
Keep them until that helper is no longer the default.
A bundle staged by that helper names the Go binary `herdr-mesh-go`.
`bin/herdr-mesh` execs that name when `.local/bin/herdr-mesh` is absent.
