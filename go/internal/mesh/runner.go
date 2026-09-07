package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultCommandTimeout = 10 * time.Second
	maxCommandOutput      = 8 * 1024 * 1024
)

var sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type CommandResult struct {
	JSON   any
	Stdout string
	Stderr string
}

type Runner struct {
	command   string
	maxOutput int
}

func NewRunner(command string) *Runner {
	return &Runner{command: command, maxOutput: maxCommandOutput}
}

func NewRunnerFromEnv() *Runner {
	command := os.Getenv("HERDR_BIN")
	if command == "" {
		if root := os.Getenv("SUM_INSTALL_ROOT"); root != "" {
			command = filepath.Join(root, "bin", "herdr-scoped")
		}
	}
	return NewRunner(command)
}

func (r *Runner) Run(ctx context.Context, args []string, timeout time.Duration) (CommandResult, error) {
	if r == nil || r.command == "" {
		return CommandResult{}, errors.New("herdr CLI is not configured")
	}
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	if _, err := explicitSession(); err != nil {
		return CommandResult{}, err
	}

	argv := append([]string(nil), args...)
	if filepath.Base(r.command) != "herdr-scoped" {
		for _, arg := range argv {
			if arg == "--session" || strings.HasPrefix(arg, "--session=") {
				return CommandResult{}, errors.New("do not override sum's explicit Herdr session inside command arguments")
			}
		}
		session, _ := explicitSession()
		argv = append([]string{"--session", session}, argv...)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(callCtx, r.command, argv...)
	var stdout, stderr boundedBuffer
	stdout.limit = r.maxOutput
	stderr.limit = r.maxOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return CommandResult{}, fmt.Errorf("herdr command timed out after %s: herdr %s", timeout, strings.Join(args, " "))
		}
		if errors.Is(callCtx.Err(), context.Canceled) {
			return CommandResult{}, callCtx.Err()
		}
		if stdout.exceeded || stderr.exceeded {
			return CommandResult{}, fmt.Errorf("herdr command output exceeded %d bytes", r.maxOutput)
		}
		return CommandResult{}, commandError(args, err, stdout.String(), stderr.String())
	}
	if stdout.exceeded || stderr.exceeded {
		return CommandResult{}, fmt.Errorf("herdr command output exceeded %d bytes", r.maxOutput)
	}

	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	result.JSON = parseJSON(result.Stdout)
	return result, nil
}

func explicitSession() (string, error) {
	session := os.Getenv("SUM_SESSION")
	if session == "" {
		session = os.Getenv("HERDR_SESSION")
	}
	if session == "" {
		const marker = "/sessions/"
		socket := os.Getenv("HERDR_SOCKET_PATH")
		if index := strings.Index(socket, marker); index >= 0 {
			candidate := socket[index+len(marker):]
			candidate = strings.TrimSuffix(candidate, "/herdr.sock")
			session = candidate
		}
	}
	if session == "" || !sessionPattern.MatchString(session) {
		return "", errors.New("cannot identify the Herdr session; set SUM_SESSION to its explicit name; no default-session fallback")
	}
	return session, nil
}

func (r CommandResult) Payload() (any, error) {
	if r.JSON == nil {
		return nil, errors.New("herdr did not return JSON")
	}
	if object, ok := r.JSON.(map[string]any); ok {
		if result, exists := object["result"]; exists {
			return result, nil
		}
	}
	return r.JSON, nil
}

func parseJSON(output string) any {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || (!strings.HasPrefix(line, "{") && !strings.HasPrefix(line, "[")) {
			continue
		}
		var value any
		if json.Unmarshal([]byte(line), &value) == nil {
			return value
		}
	}
	return nil
}

func commandError(args []string, err error, stdout, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if len(detail) > 300 {
		detail = detail[len(detail)-300:]
	}
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("herdr %s failed: %s", strings.Join(args, " "), detail)
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(data), nil
	}
	if len(data) > remaining {
		b.exceeded = true
		_, _ = b.Buffer.Write(data[:remaining])
		return len(data), nil
	}
	return b.Buffer.Write(data)
}
