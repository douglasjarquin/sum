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

func runRaw(herdrPath string, timeout time.Duration, args ...string) (stdout, stderr string, code int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, herdrPath, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	name := filepath.Base(herdrPath)
	if ctx.Err() == context.DeadlineExceeded {
		return outBuf.String(), errBuf.String(), -1, fmt.Errorf("%s: timed out after %s; its effect is unknown", name, timeout)
	}
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, fmt.Errorf("%s: %s", name, runErr)
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

func run(herdrPath string, timeout time.Duration, args ...string) (string, error) {
	stdout, stderr, code, err := runRaw(herdrPath, timeout, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		name := filepath.Base(herdrPath)
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if len(detail) > 4000 {
			detail = detail[len(detail)-4000:]
		}
		return "", fmt.Errorf("%s exited %d: %s", name, code, detail)
	}
	return stdout, nil
}

func ErrorCode(stderr string) string {
	value, err := ordjson.Decode([]byte(stderr))
	if err != nil {
		return ""
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return ""
	}
	errValue, _ := obj.Get("error")
	errObj, ok := errValue.(*ordjson.Object)
	if !ok {
		return ""
	}
	code, _ := errObj.Get("code")
	s, _ := code.(string)
	return s
}

func Observe(herdrPath, session string, timeout time.Duration, args ...string) (any, string, error) {
	if !sessionNamePattern.MatchString(session) {
		return nil, "", fmt.Errorf("Invalid session name.")
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
			return nil, "", fmt.Errorf("Do not override sum's explicit Herdr session inside command arguments.")
		}
	}
	fullArgs := append([]string{"--session", session}, args...)
	stdout, stderr, code, err := runRaw(herdrPath, timeout, fullArgs...)
	if err != nil {
		return nil, "", err
	}
	if code != 0 {
		if herdrCode := ErrorCode(stderr); herdrCode != "" {
			return nil, herdrCode, nil
		}
		name := filepath.Base(herdrPath)
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if len(detail) > 300 {
			detail = detail[len(detail)-300:]
		}
		label := strings.Join(args[:min(2, len(args))], " ")
		return nil, "", fmt.Errorf("%s %s exited %d: %s", name, label, code, detail)
	}
	value, err := decodeHerdr(stdout)
	if err != nil {
		return nil, "", err
	}
	return value, "", nil
}

func CallRaw(herdrPath, session string, timeout time.Duration, args ...string) (string, error) {
	if !sessionNamePattern.MatchString(session) {
		return "", fmt.Errorf("Invalid session name.")
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
			return "", fmt.Errorf("Do not override sum's explicit Herdr session inside command arguments.")
		}
	}
	fullArgs := append([]string{"--session", session}, args...)
	return run(herdrPath, timeout, fullArgs...)
}

func Call(herdrPath, session string, timeout time.Duration, args ...string) (any, error) {
	stdout, err := CallRaw(herdrPath, session, timeout, args...)
	if err != nil {
		return nil, err
	}
	return decodeHerdr(stdout)
}

func decodeHerdr(stdout string) (any, error) {
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
