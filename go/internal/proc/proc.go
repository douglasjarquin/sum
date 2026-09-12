package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const DefaultTimeout = 20 * time.Second

type Result struct {
	Stdout string
	Stderr string
	Code   int
}

func Run(argv []string, cwd string, timeout time.Duration, check bool, env []string) (Result, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("empty command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	name := filepath.Base(argv[0])
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		seconds := int(timeout / time.Second)
		return result, fmt.Errorf("%s: timed out after %ds; its effect is unknown", name, seconds)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Code = exitErr.ExitCode()
			if check {
				detail := trimDetail(result.Stderr, result.Stdout)
				return result, fmt.Errorf("%s exited %d: %s", name, result.Code, detail)
			}
			return result, nil
		}
		return result, fmt.Errorf("%s: %s", name, err)
	}
	return result, nil
}

func trimDetail(stderr, stdout string) string {
	detail := stderr
	if len(bytes.TrimSpace([]byte(detail))) == 0 {
		detail = stdout
	}
	detail = string(bytes.TrimSpace([]byte(detail)))
	if len(detail) > 4000 {
		return detail[len(detail)-4000:]
	}
	return detail
}

func PIDRunning(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if errors.Is(err, syscall.EPERM) {
		return false, fmt.Errorf("Reservation operation cannot be inspected; reservation remains held.")
	}
	return false, err
}
