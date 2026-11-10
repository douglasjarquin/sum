// Package proc runs a child process to completion with both streams captured as text and an optional time bound.
package proc

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ErrTimeout reports that the process outlived its bound and was killed; whatever it did before that is unknown.
var ErrTimeout = errors.New("process exceeded its time bound")

type Options struct {
	Dir     string
	Stdin   io.Reader     // nil inherits this process's stdin.
	Timeout time.Duration // Zero waits without a bound.
}

type Result struct {
	Code           int // The exit status, or the negated signal number when a signal ended the process.
	Stdout, Stderr string
}

// Run returns an error only when the process could not start or timed out; a non-zero exit is a Result.
func Run(argv []string, opts Options) (Result, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = opts.Dir
	cmd.Stdin = opts.Stdin
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	var expired <-chan time.Time
	if opts.Timeout > 0 {
		timer := time.NewTimer(opts.Timeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case <-done:
	case <-expired:
		// Only the child is killed. Its streams are not waited for, since a descendant can hold them open long after.
		_ = cmd.Process.Kill()
		return Result{}, ErrTimeout
	}
	code := cmd.ProcessState.ExitCode()
	if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		code = -int(status.Signal())
	}
	return Result{Code: code, Stdout: text(stdout.Bytes()), Stderr: text(stderr.Bytes())}, nil
}

// NotFound reports whether Run failed because the program does not exist.
func NotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

var newlines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// text decodes captured output as UTF-8 with every line ending normalised to "\n".
func text(data []byte) string {
	return newlines.Replace(strings.ToValidUTF8(string(data), "�"))
}
