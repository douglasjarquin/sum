package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/douglasjarquin/sum/go/internal/cli"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRoot(helperPath(), os.Stdout, os.Stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		errorValue := ordjson.NewObject()
		errorValue.Set("error", err.Error())
		var payload []byte
		var marshalErr error
		if outputFormatFromArgs(os.Args[1:]) == "json" {
			payload, marshalErr = ordjson.MarshalCompact(errorValue)
		} else {
			payload, marshalErr = ordjson.MarshalTOON(errorValue)
		}
		if marshalErr != nil {
			fmt.Fprintln(os.Stderr, "error: sumctl failed")
		} else {
			fmt.Fprintln(os.Stderr, string(payload))
		}
		os.Exit(1)
	}
}

func outputFormatFromArgs(args []string) string {
	format := "toon"
	for i := 0; i < len(args); i++ {
		if args[i] == "--format" && i+1 < len(args) {
			format = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--format=") {
			format = strings.TrimPrefix(args[i], "--format=")
		}
	}
	return format
}

func helperPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	if root := os.Getenv("SUM_INSTALL_ROOT"); root != "" {
		return filepath.Join(root, "bin", "sumctl")
	}
	return ""
}
