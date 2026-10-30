package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func SessionFromEnv() (string, error) {
	value := os.Getenv("SUM_SESSION")
	if value == "" {
		value = os.Getenv("HC_SESSION")
	}
	if !sessionNamePattern.MatchString(value) {
		return "", fmt.Errorf("explicit HC session required; set SUM_SESSION to its recorded name")
	}
	return value, nil
}

// Context observes a native local caller. Environment supplies its expected
// route; the existing core must attest the permanent identity before use.
// Legacy HERDR variables never grant authority to the HC runtime.
func Context(root string) (*ordjson.Object, error) {
	session, err := SessionFromEnv()
	if err != nil {
		return nil, err
	}
	socket, core, address := os.Getenv("HC_SOCKET"), os.Getenv("SUM_CORE_ID"), os.Getenv("HC_TERMINAL_ID")
	if !filepath.IsAbs(socket) || core == "" || !regexp.MustCompile(`^t[0-9a-z]+$`).MatchString(address) {
		return nil, fmt.Errorf("run inside the recorded local HC terminal; HC_SOCKET, HC_TERMINAL_ID and SUM_CORE_ID are required")
	}
	c := hcclient.Client{Socket: socket, CoreID: core}
	if _, err = c.Hello(context.Background()); err != nil {
		return nil, fmt.Errorf("verify HC caller core: %w", err)
	}
	terminal, err := c.Get(context.Background(), address)
	if err != nil {
		return nil, fmt.Errorf("verify HC caller terminal: %w", err)
	}
	if terminal.Address != address || terminal.State != "live" || !filepath.IsAbs(terminal.Cwd) {
		return nil, fmt.Errorf("HC caller terminal is not live at its recorded address")
	}
	id, err := machine.ID()
	if err != nil {
		return nil, fmt.Errorf("identify machine: %w", err)
	}
	generation, _ := strconv.ParseUint(os.Getenv("SUM_BINDING_GENERATION"), 10, 64)
	endpoint := Endpoint{Schema: 2, Backend: "hc", Machine: id, CoreID: core, Session: session, Socket: socket, Address: address, Task: os.Getenv("SUM_TASK"), Role: os.Getenv("SUM_ROLE"), Attempt: os.Getenv("SUM_ATTEMPT"), BindingGeneration: generation, CreatedMS: terminal.CreatedMS, Cwd: terminal.Cwd}
	out, err := ordjson.FromValue(endpoint)
	if err != nil {
		return nil, err
	}
	out.Set("at", Now())
	return out, nil
}

func EndpointFromContext(ctx *ordjson.Object) Endpoint {
	if ctx == nil {
		return Endpoint{}
	}
	e := Endpoint{Schema: int(number(ctx, "schema")), Backend: text(ctx, "backend"), Machine: text(ctx, "machine"), CoreID: text(ctx, "core_id"), Session: text(ctx, "session"), Socket: text(ctx, "socket"), Address: text(ctx, "address"), Task: text(ctx, "task"), Role: text(ctx, "role"), Attempt: text(ctx, "attempt"), BindingGeneration: uint64(max(number(ctx, "binding_generation"), 0)), CreatedMS: uint64(max(number(ctx, "created_ms"), 0)), Cwd: text(ctx, "cwd"), Pane: text(ctx, "pane")}
	return e
}
func text(o *ordjson.Object, key string) string { v, _ := o.Get(key); s, _ := v.(string); return s }

func RecordedReviewer(task *ordjson.Object) (*ordjson.Object, bool) {
	if task == nil {
		return nil, false
	}
	v, ok := task.Get("reviewer")
	if !ok {
		return nil, false
	}
	o, ok := v.(*ordjson.Object)
	return o, ok && o != nil
}
