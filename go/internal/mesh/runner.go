package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

type commandResult struct {
	stdout string
	stderr string
}

type commandRunner interface {
	Run(context.Context, []string, time.Duration) (commandResult, error)
}

type herdrRunner struct {
	path    string
	session string
}

func (r herdrRunner) Run(parent context.Context, args []string, timeout time.Duration) (commandResult, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, r.path, append([]string{"--session", r.session}, args...)...)
	stdout, stderr := make([]byte, 0), make([]byte, 0)
	var outBuffer, errBuffer limitedBuffer
	command.Stdout = &outBuffer
	command.Stderr = &errBuffer
	err := command.Run()
	stdout, stderr = outBuffer.Bytes(), errBuffer.Bytes()
	if ctx.Err() != nil {
		return commandResult{stdout: string(stdout), stderr: string(stderr)}, fmt.Errorf("%s: timed out after %s; its effect is unknown", r.path, timeout)
	}
	if err != nil {
		return commandResult{stdout: string(stdout), stderr: string(stderr)}, remoteError(stderr, err)
	}
	return commandResult{stdout: string(stdout), stderr: string(stderr)}, nil
}

type limitedBuffer struct {
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 256 * 1024
	remaining := limit - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			b.data = append(b.data, p[:remaining]...)
		} else {
			b.data = append(b.data, p...)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.data }

func remoteError(stderr []byte, cause error) error {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(stderr, &envelope) == nil && len(envelope.Error) > 0 {
		return fmt.Errorf("Herdr: %s", envelope.Error)
	}
	return fmt.Errorf("Herdr exited unsuccessfully: %w: %s", cause, string(stderr))
}
