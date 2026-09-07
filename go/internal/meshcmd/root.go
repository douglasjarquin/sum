package meshcmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const Version = "0.1.0"

type Server func(context.Context) error

func NewRoot(in io.Reader, out, errOut io.Writer, serve Server) *cobra.Command {
	if in == nil {
		in = io.Reader(nil)
	}
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	var mode string
	root := &cobra.Command{
		Use:                "herdr-mesh-go",
		Short:              "Sum-owned Herdr Mesh MCP bridge",
		Version:            Version,
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if mode != "server" {
				return fmt.Errorf("unsupported mode %q; only server mode is available", mode)
			}
			if serve == nil {
				return fmt.Errorf("server is not configured")
			}
			return serve(cmd.Context())
		},
	}
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetVersionTemplate("herdr-mesh-sum {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(nil)
	root.Flags().StringVar(&mode, "mode", "server", "server mode")
	return root
}
