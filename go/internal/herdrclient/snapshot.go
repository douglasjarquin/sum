package herdrclient

import (
	"context"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

// Snapshot is one operation's view of Herdr agents: at most one `agent list` per session, shared by every reader in
// that operation. It lives only as long as the operation, is never persisted, and is not mutation authority: a
// caller about to write re-observes the one recipient it writes to.
type Snapshot struct {
	ctx      context.Context
	herdr    func() (string, error)
	timeout  time.Duration
	sessions map[string]*sessionList
	tripped  map[string]string
	calls    int
	started  time.Time
}

type sessionList struct {
	agents *ordjson.Object
	err    error
}

// NewSnapshot starts an operation's snapshot. Every Herdr call it makes runs under ctx (the operation's budget) and
// timeout; herdr resolves the executable once, lazily.
func NewSnapshot(ctx context.Context, herdr func() (string, error), timeout time.Duration) *Snapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	var path string
	var pathErr error
	resolved := false
	lookup := func() (string, error) {
		if !resolved {
			path, pathErr = herdr()
			resolved = true
		}
		return path, pathErr
	}
	return &Snapshot{ctx: ctx, herdr: lookup, timeout: timeout, sessions: map[string]*sessionList{}, tripped: map[string]string{}, started: time.Now()}
}

// Context is the operation context every call runs under.
func (sn *Snapshot) Context() context.Context { return sn.ctx }

// Herdr is the resolved herdr executable.
func (sn *Snapshot) Herdr() (string, error) { return sn.herdr() }

// Listed reports whether this operation already holds session's agent list (or its failure).
func (sn *Snapshot) Listed(session string) bool {
	_, ok := sn.sessions[session]
	return ok
}

func (sn *Snapshot) list(session string) *sessionList {
	if row, ok := sn.sessions[session]; ok {
		return row
	}
	row := &sessionList{}
	sn.sessions[session] = row
	path, err := sn.herdr()
	if err != nil {
		row.err = err
		return row
	}
	sn.calls++
	listed, err := CallContext(sn.ctx, path, session, sn.timeout, "agent", "list")
	if err != nil {
		row.err = err
		return row
	}
	var items []any
	if obj, ok := listed.(*ordjson.Object); ok {
		if v, has := obj.Get("agents"); has {
			items, _ = v.([]any)
		}
	} else if arr, ok := listed.([]any); ok {
		items = arr
	}
	row.agents = ordjson.NewObject()
	for _, raw := range items {
		agent, _ := raw.(*ordjson.Object)
		if agent == nil {
			continue
		}
		if id, _ := agent.Get("pane_id"); id != nil {
			if pane, ok := id.(string); ok {
				row.agents.Set(pane, agent)
			}
		}
	}
	return row
}

// ListError is the error session's agent list failed with in this operation, or nil.
func (sn *Snapshot) ListError(session string) error {
	return sn.list(session).err
}

// Agent returns pane's agent from session's snapshot. An agent missing from a listed session is agent_not_found
// without its own lookup; one listed without a cwd gets one `agent get`.
func (sn *Snapshot) Agent(session, pane string) (*ordjson.Object, error) {
	row := sn.list(session)
	if row.err != nil {
		return nil, fmt.Errorf("agent list for session %s failed: %v", session, row.err)
	}
	raw, ok := row.agents.Get(pane)
	if !ok {
		return nil, fmt.Errorf("agent_not_found (absent from the session's agent snapshot)")
	}
	agent, _ := raw.(*ordjson.Object)
	if AgentCwd(agent) != "" {
		return agent, nil
	}
	got, err := sn.Call(session, sn.timeout, "agent", "get", pane)
	if err != nil {
		return nil, err
	}
	return UnwrapAgent(got), nil
}

// Call makes one counted Herdr call in session under the operation context.
func (sn *Snapshot) Call(session string, timeout time.Duration, args ...string) (any, error) {
	path, err := sn.herdr()
	if err != nil {
		return nil, err
	}
	sn.calls++
	return CallContext(sn.ctx, path, session, timeout, args...)
}

// Trip marks session unavailable for the rest of the operation.
func (sn *Snapshot) Trip(session, reason string) {
	if _, done := sn.tripped[session]; !done {
		sn.tripped[session] = reason
	}
}

// Tripped returns why session was marked unavailable in this operation, if it was.
func (sn *Snapshot) Tripped(session string) (string, bool) {
	reason, ok := sn.tripped[session]
	return reason, ok
}

// Calls counts the Herdr invocations this snapshot made.
func (sn *Snapshot) Calls() int { return sn.calls }

// Sessions counts the sessions this operation listed.
func (sn *Snapshot) Sessions() int { return len(sn.sessions) }

// Elapsed is the time since the operation started.
func (sn *Snapshot) Elapsed() time.Duration { return time.Since(sn.started) }

// UnwrapAgent returns the agent object inside a Herdr `agent get` result.
func UnwrapAgent(value any) *ordjson.Object {
	obj, _ := value.(*ordjson.Object)
	if obj == nil {
		return nil
	}
	if nested, ok := obj.Get("agent"); ok {
		if inner, is := nested.(*ordjson.Object); is {
			return inner
		}
	}
	return obj
}

// AgentCwd is the agent's working directory as Herdr reports it.
func AgentCwd(agent *ordjson.Object) string {
	if agent == nil {
		return ""
	}
	for _, key := range []string{"cwd", "working_directory"} {
		if v, _ := agent.Get(key); v != nil {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// AgentStatus is the agent's lifecycle status as Herdr reports it, or "unknown".
func AgentStatus(agent *ordjson.Object) string {
	if agent == nil {
		return "unknown"
	}
	for _, key := range []string{"agent_status", "status"} {
		if v, _ := agent.Get(key); v != nil {
			if s := fmt.Sprint(v); s != "" {
				return s
			}
		}
	}
	return "unknown"
}
