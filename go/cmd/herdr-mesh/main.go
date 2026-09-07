package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/douglasjarquin/sum/go/internal/mesh"
	"github.com/douglasjarquin/sum/go/internal/meshcmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := meshcmd.NewRoot(os.Stdin, os.Stdout, os.Stderr, mesh.Serve)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "herdr-mesh: %s\n", err)
		os.Exit(1)
	}
}
