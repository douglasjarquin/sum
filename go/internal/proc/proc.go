package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultTimeout bounds a helper whose caller named no timeout.
	DefaultTimeout = 20 * time.Second
	// DefaultStdoutLimit bounds captured stdout. Stdout is parsed (JSON, process tables, porcelain), so a prefix is
	// a wrong answer: exceeding the limit stops the helper and is an error unless the caller marks stdout FreeText.
	DefaultStdoutLimit = 8 << 20
	// DefaultStderrLimit bounds captured stderr. Stderr is diagnostics, so its tail is kept and overflow is a flag.
	DefaultStderrLimit = 256 << 10
	// PipeGrace is how long the caller waits for the helper's output pipes to close after the helper exits or is
	// stopped. A descendant that inherited them and keeps them open is reported, never waited on indefinitely.
	PipeGrace = 2 * time.Second
)

var (
	// ErrNotStarted means nothing ran: the executable could not be started or the caller's context was already done.
	ErrNotStarted = errors.New("helper did not start")
	// ErrUncertain means the helper may have had an effect that was not observed to completion: it timed out, was
	// canceled, was stopped for output overflow, or left a descendant holding its output. The helper never retries.
	ErrUncertain = errors.New("helper effect is unknown")
	// ErrOutputLimit means stdout exceeded its limit; the retained prefix is not a valid response.
	ErrOutputLimit = errors.New("helper output exceeded its limit")

	errOwnTimeout = errors.New("command timeout")
	errOverflow   = errors.New("stdout limit")
)

// Error is the classified failure of one helper run. Match it with errors.Is against ErrNotStarted, ErrUncertain,
// and ErrOutputLimit; its text keeps the wording callers already print.
type Error struct {
	msg   string
	kinds []error
}

func (e *Error) Error() string   { return e.msg }
func (e *Error) Unwrap() []error { return e.kinds }

func classified(msg string, kinds ...error) error { return &Error{msg: msg, kinds: kinds} }

// Cmd is one owned helper invocation. A nil Env inherits the process environment; a non-nil Env replaces it.
// Zero limits and timeout take the package defaults.
type Cmd struct {
	Argv        []string
	Dir         string
	Env         []string
	Timeout     time.Duration
	Check       bool
	StdoutLimit int
	StderrLimit int
	// FreeText marks stdout as prose rather than structured output: overflow keeps the head and sets
	// StdoutTruncated without stopping the helper or returning an error.
	FreeText bool
}

// Result is what one helper run produced. Code is the exit code, or -1 whenever the outcome is not a completed
// exit (not started, timed out, canceled, stopped, or a descendant still holding the output), so a caller that
// ignores the error never reads success. The held-output message still names the helper's own exit code.
type Result struct {
	Stdout          string
	Stderr          string
	Code            int
	StdoutTruncated bool
	StderrTruncated bool
}

// Detail is the helper's own explanation: trimmed stderr, or trimmed stdout when stderr is blank.
func (r Result) Detail() string {
	if detail := strings.TrimSpace(r.Stderr); detail != "" {
		return detail
	}
	return strings.TrimSpace(r.Stdout)
}

// Run is the compatibility form of RunContext for callers without an operation context: it runs under
// context.Background() with the command's own timeout and the default output limits.
func Run(argv []string, cwd string, timeout time.Duration, check bool, env []string) (Result, error) {
	return RunContext(context.Background(), Cmd{Argv: argv, Dir: cwd, Env: env, Timeout: timeout, Check: check})
}

// RunContext runs one helper under the tighter of ctx and the command timeout, capturing bounded output.
// On deadline or cancel only the direct child is killed (as exec.CommandContext always has); its descendants are
// never signaled. A descendant still holding the output pipes a grace after the child exits or is stopped makes
// the run uncertain; the pipes are then closed, so such a descendant may fail on its next write, but sum sends it
// no signal and never reports it stopped.
func RunContext(ctx context.Context, c Cmd) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(c.Argv) == 0 {
		return Result{Code: -1}, fmt.Errorf("empty command")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	name := filepath.Base(c.Argv[0])
	if ctx.Err() != nil {
		return Result{Code: -1}, classified(fmt.Sprintf("%s: not started, %s", name, callerStop(ctx)), ErrNotStarted)
	}
	withCause, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	runCtx, cancelTimeout := context.WithTimeoutCause(withCause, timeout, errOwnTimeout)
	defer cancelTimeout()

	stdout := &capture{limit: positive(c.StdoutLimit, DefaultStdoutLimit)}
	if !c.FreeText {
		stdout.onOverflow = func() { cancel(errOverflow) }
	}
	stderr := &capture{limit: positive(c.StderrLimit, DefaultStderrLimit), tail: true}
	outR, outW, err := os.Pipe()
	if err != nil {
		return Result{Code: -1}, classified(fmt.Sprintf("%s: %s", name, err), ErrNotStarted)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return Result{Code: -1}, classified(fmt.Sprintf("%s: %s", name, err), ErrNotStarted)
	}
	cmd := exec.CommandContext(runCtx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	if c.Env != nil {
		cmd.Env = c.Env
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	startErr := cmd.Start()
	outW.Close()
	errW.Close()
	if startErr != nil {
		outR.Close()
		errR.Close()
		return Result{Code: -1}, classified(fmt.Sprintf("%s: %s", name, startErr), ErrNotStarted)
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go drain(&readers, outR, stdout)
	go drain(&readers, errR, stderr)
	drained := make(chan struct{})
	go func() { readers.Wait(); close(drained) }()

	waitErr := cmd.Wait()
	stopped := runCtx.Err() != nil
	held := false
	grace := time.NewTimer(PipeGrace)
	if stopped {
		select {
		case <-drained:
		case <-grace.C:
			held = true
		}
	} else {
		select {
		case <-drained:
		case <-grace.C:
			held = true
		case <-runCtx.Done():
			if errors.Is(context.Cause(runCtx), errOverflow) {
				// The drain itself raised the overflow after the helper exited: wait for the pipes as usual.
				select {
				case <-drained:
				case <-grace.C:
					held = true
				}
			} else {
				// The caller's deadline or cancel ends the wait early; the pipes count as held only if still open.
				select {
				case <-drained:
				default:
					held = true
				}
			}
		}
	}
	grace.Stop()
	outR.Close()
	errR.Close()
	<-drained

	result := Result{
		Stdout:          string(stdout.bytes()),
		Stderr:          string(stderr.bytes()),
		Code:            -1,
		StdoutTruncated: stdout.overflowed,
		StderrTruncated: stderr.overflowed,
	}
	if !stopped && cmd.ProcessState != nil {
		result.Code = cmd.ProcessState.ExitCode()
	}
	heldNote := ""
	if held {
		heldNote = "; a descendant still holds its output and was not stopped"
	}

	if stdout.overflowed && !c.FreeText {
		if stopped {
			return result, classified(fmt.Sprintf("%s: stdout exceeded %d bytes and the helper was stopped; its output is incomplete and its effect is unknown%s", name, stdout.limit, heldNote), ErrOutputLimit, ErrUncertain)
		}
		return result, classified(fmt.Sprintf("%s: stdout exceeded %d bytes; its output is incomplete%s", name, stdout.limit, heldNote), ErrOutputLimit)
	}
	if stopped {
		cause := context.Cause(runCtx)
		var msg string
		switch {
		case errors.Is(cause, errOwnTimeout):
			msg = fmt.Sprintf("%s: timed out after %s; its effect is unknown", name, seconds(timeout))
		default:
			msg = fmt.Sprintf("%s: %s; its effect is unknown", name, callerStop(ctx))
		}
		return result, classified(msg+heldNote, ErrUncertain)
	}
	if held {
		exitCode := result.Code
		result.Code = -1
		return result, classified(fmt.Sprintf("%s exited %d but a descendant still holds its output and was not stopped; its output may be incomplete and its effect is unknown", name, exitCode), ErrUncertain)
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return result, classified(fmt.Sprintf("%s: %s", name, waitErr), ErrUncertain)
		}
		if result.Code < 0 {
			return result, classified(fmt.Sprintf("%s: %s; its effect is unknown", name, waitErr), ErrUncertain)
		}
		if c.Check {
			return result, fmt.Errorf("%s exited %d: %s", name, result.Code, tail(result.Detail(), 4000))
		}
	}
	return result, nil
}

// callerStop names why the caller's context ended: its deadline or an explicit cancel.
func callerStop(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "stopped at the caller's deadline"
	}
	return "canceled by the caller"
}

func seconds(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return d.String()
}

func positive(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func drain(wg *sync.WaitGroup, r io.Reader, c *capture) {
	defer wg.Done()
	_, _ = io.Copy(c, r)
}

// capture holds at most limit bytes of one stream while it is read: the head for stdout, the tail for stderr.
// Bytes past the limit are read and discarded so the writer is never blocked on a full pipe.
type capture struct {
	limit      int
	tail       bool
	buf        []byte
	overflowed bool
	onOverflow func()
}

func (c *capture) Write(p []byte) (int, error) {
	n := len(p)
	if c.tail {
		c.buf = append(c.buf, p...)
		if len(c.buf) > c.limit {
			c.overflowed = true
			if len(c.buf) > 2*c.limit {
				c.buf = append(c.buf[:0], c.buf[len(c.buf)-c.limit:]...)
			}
		}
		return n, nil
	}
	if c.overflowed {
		return n, nil
	}
	room := c.limit - len(c.buf)
	if len(p) > room {
		c.buf = append(c.buf, p[:room]...)
		c.overflowed = true
		if c.onOverflow != nil {
			c.onOverflow()
		}
		return n, nil
	}
	c.buf = append(c.buf, p...)
	return n, nil
}

func (c *capture) bytes() []byte {
	if c.tail && len(c.buf) > c.limit {
		return c.buf[len(c.buf)-c.limit:]
	}
	return c.buf
}

// tail keeps the last n bytes of text.
func tail(text string, n int) string {
	if len(text) > n {
		return text[len(text)-n:]
	}
	return text
}

// Terminate sends one SIGTERM for a graceful stop of a process that has no pane
// to interrupt through. Orphaned daemons routinely ignore SIGINT (backgrounded
// processes inherit it ignored), so TERM is the correct signal here; sum never
// sends SIGKILL. A process already gone (ESRCH) is not an error; EPERM and every
// other failure are reported so the caller can keep the reservation held.
func Terminate(pid int) error {
	err := syscall.Kill(pid, syscall.SIGTERM)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
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

// ScrubbedEnv is the environment for a command that runs candidate-controlled code: this pane's Herdr identity
// and sum state home are removed so the candidate's own tooling cannot reach the installation through them.
func ScrubbedEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "HERDR_") || key == "SUM_HOME" || key == "SUM_SESSION" || key == "SUM_INSTALL_ROOT" {
			continue
		}
		env = append(env, entry)
	}
	return env
}
