package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/douglasjarquin/sum/go/internal/cli"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRoot(referenceHelper(), os.Stdout, os.Stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		errorValue := ordjson.NewObject()
		errorValue.Set("error", err.Error())
		payload, marshalErr := ordjson.MarshalCompact(errorValue)
		if marshalErr != nil {
			fmt.Fprintln(os.Stderr, `{"error": "sumctl-go failed"}`)
		} else {
			fmt.Fprintln(os.Stderr, string(payload))
		}
		os.Exit(1)
	}
}

func referenceHelper() string {
	if value := os.Getenv("SUM_PYTHON_HELPER"); value != "" {
		return value
	}
	if value, err := filepath.Abs("bin/sumctl"); err == nil {
		if _, statErr := os.Stat(value); statErr == nil {
			return value
		}
	}
	return ""
}
