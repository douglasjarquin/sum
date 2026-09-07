package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/douglasjarquin/sum/go/internal/mesh"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := mesh.Execute(ctx, os.Stdout, os.Stderr, func(ctx context.Context) error {
		config, err := mesh.LoadConfig()
		if err != nil {
			return err
		}
		return mesh.RunMCP(ctx, mesh.NewService(config))
	})
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return
	}
	payload, marshalErr := json.Marshal(map[string]string{"error": err.Error()})
	if marshalErr != nil {
		fmt.Fprintln(os.Stderr, `{"error":"herdr-mesh failed"}`)
		return
	}
	fmt.Fprintln(os.Stderr, string(payload))
	os.Exit(1)
}
