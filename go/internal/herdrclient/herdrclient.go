package herdrclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func run(herdrPath string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, herdrPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	name := filepath.Base(herdrPath)
	if ctxErr := ctx.Err(); ctxErr == context.DeadlineExceeded {
		return "", fmt.Errorf("%s: timed out after %s; its effect is unknown", name, timeout)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = strings.TrimSpace(stdout.String())
			}
			if len(detail) > 4000 {
				detail = detail[len(detail)-4000:]
			}
			return "", fmt.Errorf("%s exited %d: %s", name, exitErr.ExitCode(), detail)
		}
		return "", fmt.Errorf("%s: %s", name, err)
	}
	return stdout.String(), nil
}

func Call(herdrPath, session string, timeout time.Duration, args ...string) (any, error) {
	if !sessionNamePattern.MatchString(session) {
		return nil, fmt.Errorf("invalid session name")
	}
	options := args
	for i, a := range args {
		if a == "--" {
			options = args[:i]
			break
		}
	}
	for _, a := range options {
		if a == "--session" || strings.HasPrefix(a, "--session=") {
			return nil, fmt.Errorf("do not override sum's explicit Herdr session inside command arguments")
		}
	}
	fullArgs := append([]string{"--session", session}, args...)
	stdout, err := run(herdrPath, timeout, fullArgs...)
	if err != nil {
		return nil, err
	}
	value, err := ordjson.Decode([]byte(stdout))
	if err != nil {
		preview := stdout
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("Herdr did not return JSON: %s", preview)
	}
	if obj, ok := value.(*ordjson.Object); ok {
		if errValue, has := obj.Get("error"); has && errValue != nil && errValue != false {
			encoded, marshalErr := json.Marshal(errValue)
			if marshalErr == nil {
				return nil, fmt.Errorf("Herdr: %s", encoded)
			}
		}
		if resultValue, has := obj.Get("result"); has {
			return resultValue, nil
		}
	}
	return value, nil
}

func Version(herdrPath string) (string, string, error) {
	stdout, err := run(herdrPath, 20*time.Second, "--version")
	if err != nil {
		return "", "", err
	}
	found := strings.TrimSpace(stdout)
	matched := regexp.MustCompile(`(?i)^herdr[ \t]+(\d+\.\d+\.\d+)$`).FindStringSubmatch(found)
	if matched == nil {
		return "", "", fmt.Errorf("Herdr did not report one exact stable semantic version; found %q. Run mise run setup; do not silently mix CLI contracts.", found)
	}
	return matched[1], found, nil
}

func EnsureVersion(herdrPath, pinned string) (string, error) {
	version, found, err := Version(herdrPath)
	if err != nil {
		return "", err
	}
	if version != pinned {
		return "", fmt.Errorf("This MVP is pinned to Herdr %s; found %q. Run mise run setup; do not silently mix CLI contracts.", pinned, found)
	}
	return found, nil
}
