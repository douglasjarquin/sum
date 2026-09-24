package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

// stdoutLimit bounds what one Herdr call may return to the bridge.
const stdoutLimit = 256 * 1024

type commandResult struct {
	stdout string
	stderr string
	// truncated reports that free-text stdout was cut at stdoutLimit; structured stdout never truncates.
	truncated bool
}

// commandRunner runs one Herdr call. Structured (freeText false) stdout past the bound is an error; free-text stdout
// keeps its head and reports truncated.
type commandRunner interface {
	Run(ctx context.Context, args []string, timeout time.Duration, freeText bool) (commandResult, error)
}

type herdrRunner struct {
	path    string
	session string
}

func (r herdrRunner) Run(parent context.Context, args []string, timeout time.Duration, freeText bool) (commandResult, error) {
	res, err := proc.RunContext(parent, proc.Cmd{
		Argv:        append([]string{r.path, "--session", r.session}, args...),
		Timeout:     timeout,
		StdoutLimit: stdoutLimit,
		FreeText:    freeText,
	})
	result := commandResult{stdout: res.Stdout, stderr: res.Stderr, truncated: res.StdoutTruncated}
	if err != nil {
		return result, err
	}
	if res.Code != 0 {
		return result, remoteError([]byte(res.Stderr), fmt.Errorf("exit status %d", res.Code))
	}
	return result, nil
}

func remoteError(stderr []byte, cause error) error {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(stderr, &envelope) == nil && len(envelope.Error) > 0 {
		return fmt.Errorf("Herdr: %s", envelope.Error)
	}
	return fmt.Errorf("Herdr exited unsuccessfully: %w: %s", cause, string(stderr))
}
