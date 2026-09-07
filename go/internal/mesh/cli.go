package mesh

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const Version = "0.1.0"

func NewRoot(out, errOut io.Writer, run func(context.Context) error) *cobra.Command {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	root := &cobra.Command{
		Use:           "herdr-mesh",
		Short:         "Sum-owned Herdr Mesh MCP server",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context())
		},
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetVersionTemplate("herdr-mesh " + Version + "\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(nil)
	return root
}

func Execute(ctx context.Context, out, errOut io.Writer, run func(context.Context) error) error {
	if run == nil {
		return fmt.Errorf("MCP server runner is required")
	}
	return NewRoot(out, errOut, run).ExecuteContext(ctx)
}
