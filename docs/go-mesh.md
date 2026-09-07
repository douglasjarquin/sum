# Sum-owned Go Mesh

`go/cmd/herdr-mesh` is the opt-in native Herdr Mesh candidate for issue #41.

The existing `bin/herdr-mesh` entrypoint remains the pinned Node server for connected clients.

`bin/herdr-mesh-go` selects the staged cgo-free Go executable without changing MCP configuration.

The root Cobra command starts the MCP stdio server with no extra subcommand, preserving the configured launch form.

`--help` and `--version` are metadata-only paths and never open Sum state or Herdr.

The MCP server uses the pinned official Go SDK v1.6.1 for protocol framing, capabilities, request cancellation, and stdio lifecycle.

The ten existing tool names, schemas, defaults, bounded reads and waits, prompt uncertainty, and result meanings are owned by `go/internal/mesh`.

Each operation validates the calling Herdr environment, explicit session, registered pane, Sum instance, and developer read-only boundary before invoking the verified Herdr CLI.

The Go candidate is staged beside Node and Python by native artifact packaging; release activation and MCP configuration replacement remain separate decisions.
