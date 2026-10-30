// Package hctest supplies a native generation-one HC socket fixture.
// It models the published API boundary, never a legacy Herdr CLI.
package hctest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
)

type Call struct {
	Method string
	Params map[string]any
}

// Fault affects the next matching request. A lost response happens after its
// native effect; RefuseKind rejects before any input or launch effect.
type Fault struct {
	Delay        time.Duration
	LoseResponse bool
	RefuseKind   string
	FirstMessage *hcclient.FirstMessage
	SendResult   *hcclient.SendResult
}
type subscriber struct {
	params   hcclient.SubscribeParams
	messages chan any
	conn     net.Conn
}
type terminal struct {
	hcclient.Terminal
	outlive bool
	inputs  []string
}
type requestHold struct {
	method, address string
	entered         chan struct{}
	release         chan struct{}
	once            sync.Once
}
type Fixture struct {
	templateConfigHome string
	holds              []*requestHold
	closeSignal        chan struct{}
	Socket             string
	CoreID             string
	mu                 sync.Mutex
	listener           net.Listener
	connections        map[net.Conn]bool
	subscribers        map[*subscriber]bool
	terminals          map[string]*terminal
	faults             map[string][]Fault
	calls              []Call
	templates          []hcclient.TemplateInfo
	templateSubmits    map[string]string
	seq                uint64
	next               uint64
	closed             bool
	wg                 sync.WaitGroup
}

var methods = []string{"hello", "terminal.run", "terminal.get", "terminal.list", "terminal.send", "terminal.read", "terminal.wait", "terminal.label", "terminal.kill", "terminal.interrupt", "terminal.rm", "events.subscribe", "templates.list"}

func New(t testing.TB) *Fixture {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sum-hc-")
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{closeSignal: make(chan struct{}), Socket: filepath.Join(dir, "hc.sock"), CoreID: "test-" + filepath.Base(dir), connections: map[net.Conn]bool{}, subscribers: map[*subscriber]bool{}, terminals: map[string]*terminal{}, faults: map[string][]Fault{}}
	f.listener, err = net.Listen("unix", f.Socket)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close(); os.RemoveAll(dir) })
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, e := f.listener.Accept()
			if e != nil {
				return
			}
			f.mu.Lock()
			if f.closed {
				f.mu.Unlock()
				c.Close()
				return
			}
			f.connections[c] = true
			f.wg.Add(1)
			f.mu.Unlock()
			go f.serve(c)
		}
	}()
	return f
}
func (f *Fixture) Client() hcclient.Client {
	return hcclient.Client{Socket: f.Socket, CoreID: f.CoreID}
}
func (f *Fixture) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	close(f.closeSignal)
	f.listener.Close()
	for c := range f.connections {
		c.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}
func clone[T any](v T) T         { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
func (f *Fixture) Calls() []Call { f.mu.Lock(); defer f.mu.Unlock(); return clone(f.calls) }
func (f *Fixture) Inputs(address string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t := f.terminals[address]; t != nil {
		return append([]string(nil), t.inputs...)
	}
	return nil
}
func (f *Fixture) Terminals() []hcclient.Terminal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshotLocked(hcclient.SubscribeParams{})
}
func (f *Fixture) QueueFault(method string, fault Fault) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults[method] = append(f.faults[method], clone(fault))
}
func (f *Fixture) SetTerminal(t hcclient.Terminal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.Labels == nil {
		t.Labels = map[string]string{}
	}
	if t.Argv == nil {
		t.Argv = []string{}
	}
	f.terminals[t.Address] = &terminal{Terminal: clone(t)}
	f.eventLocked(t.Address, "created", t)
}
func (f *Fixture) SetStatus(address, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.terminals[address]
	if t == nil {
		panic("unknown fixture terminal " + address)
	}
	ended := t.Status != nil && t.Status.State == "working" && state != "working"
	if ended {
		t.Turn++
	}
	if state == "" {
		t.Status = nil
	} else {
		t.Status = &hcclient.Status{State: state}
	}
	f.eventLocked(address, "status", map[string]any{"status": t.Status, "records": map[string]hcclient.Status{}, "reported": t.Status != nil})
	if ended {
		f.eventLocked(address, "turn", map[string]any{"turn": t.Turn, "state": state})
	}
}
func (f *Fixture) SetForeground(address string, foreground hcclient.Foreground) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.terminals[address]
	if t == nil {
		panic("unknown fixture terminal " + address)
	}
	fg := clone(foreground)
	t.Foreground = &fg
	f.eventLocked(address, "foreground", t.Foreground)
}
func (f *Fixture) Exit(address string, code int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exitLocked(address, code, "exited")
}
func (f *Fixture) exitLocked(address string, code int32, reason string) {
	t := f.terminals[address]
	if t == nil || t.State != "live" {
		return
	}
	t.State = "finished"
	t.Status = nil
	t.Foreground = nil
	t.PID = nil
	t.ExitCode = &code
	t.Reason = &reason
	f.eventLocked(address, "status", map[string]any{"status": nil, "records": map[string]hcclient.Status{}, "reported": false})
	f.eventLocked(address, "finished", map[string]any{"reason": reason, "exit_code": code, "signal": nil, "finished_ms": f.seq})
	for a, child := range f.terminals {
		if child.Parent != nil && *child.Parent == address && !child.outlive {
			f.exitLocked(a, code, "parent_exit")
		}
	}
}
func match(t hcclient.Terminal, p hcclient.SubscribeParams) bool {
	if p.Terminal != "" && p.Terminal != t.Address {
		return false
	}
	if p.ChildrenOf != "" && (t.Parent == nil || *t.Parent != p.ChildrenOf) {
		return false
	}
	for k, v := range p.Labels {
		if t.Labels[k] != v {
			return false
		}
	}
	return true
}
func (f *Fixture) snapshotLocked(p hcclient.SubscribeParams) []hcclient.Terminal {
	out := []hcclient.Terminal{}
	for _, t := range f.terminals {
		if match(t.Terminal, p) {
			out = append(out, clone(t.Terminal))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}
func (f *Fixture) eventLocked(address, kind string, data any) {
	f.seq++
	t := f.terminals[address]
	for s := range f.subscribers {
		if t != nil && match(t.Terminal, s.params) {
			f.notifyLocked(s, "event", map[string]any{"seq": f.seq, "type": kind, "terminal": address, "data": data})
		}
	}
}
func (f *Fixture) notifyLocked(s *subscriber, method string, params any) {
	v := clone(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	select {
	case s.messages <- v:
	default:
		s.conn.Close()
		delete(f.subscribers, s)
	}
}
func (f *Fixture) Miss() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.subscribers {
		f.notifyLocked(s, "missed", map[string]any{"seq": f.seq, "terminals": f.snapshotLocked(s.params)})
	}
}
func (f *Fixture) Heartbeat() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.subscribers {
		f.notifyLocked(s, "heartbeat", map[string]any{"seq": f.seq})
	}
}
func (f *Fixture) DisconnectSubscribers() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.subscribers {
		s.conn.Close()
		delete(f.subscribers, s)
	}
}
func rpcError(kind string) map[string]any {
	return map[string]any{"code": -32000, "message": kind, "data": map[string]string{"kind": kind, "hint": "native fixture refusal"}}
}
func decode[T any](m map[string]any) T {
	b, _ := json.Marshal(m)
	var v T
	_ = json.Unmarshal(b, &v)
	return v
}
func (f *Fixture) serve(c net.Conn) {
	defer f.wg.Done()
	defer func() {
		f.mu.Lock()
		delete(f.connections, c)
		for s := range f.subscribers {
			if s.conn == c {
				delete(f.subscribers, s)
			}
		}
		f.mu.Unlock()
		c.Close()
	}()
	r := bufio.NewScanner(c)
	r.Buffer(make([]byte, 4096), 2<<20)
	enc := json.NewEncoder(c)
	for r.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  map[string]any  `json:"params"`
		}
		if json.Unmarshal(r.Bytes(), &req) != nil {
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, Call{Method: req.Method, Params: clone(req.Params)})
		var fault Fault
		if q := f.faults[req.Method]; len(q) > 0 {
			fault = q[0]
			f.faults[req.Method] = q[1:]
		}
		var hold *requestHold
		address, _ := req.Params["terminal"].(string)
		for i, candidate := range f.holds {
			if candidate.method == req.Method && (candidate.address == "" || candidate.address == address) {
				hold = candidate
				f.holds = append(f.holds[:i], f.holds[i+1:]...)
				break
			}
		}
		f.mu.Unlock()
		if hold != nil {
			close(hold.entered)
			select {
			case <-hold.release:
			case <-f.closeSignal:
				return
			}
		}
		if fault.Delay > 0 {
			timer := time.NewTimer(fault.Delay)
			select {
			case <-timer.C:
			case <-f.closeSignal:
				timer.Stop()
				return
			}
		}
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		var result any
		var refusal string
		var sub *subscriber
		if fault.RefuseKind != "" {
			refusal = fault.RefuseKind
		} else if req.JSONRPC != "2.0" {
			refusal = "invalid_request"
		} else {
			result, refusal, sub = f.executeLocked(req.Method, req.Params, fault, c)
		}
		if refusal != "" {
			response["error"] = rpcError(refusal)
		} else {
			response["result"] = result
		}
		f.mu.Unlock()
		if fault.LoseResponse {
			return
		}
		if enc.Encode(response) != nil {
			return
		}
		if sub != nil { // Plain subscribe dedicates the connection to notifications.
			done := make(chan struct{})
			go func() { defer close(done); _, _ = rReader(r, c) }()
			for {
				select {
				case v := <-sub.messages:
					if enc.Encode(v) != nil {
						return
					}
				case <-done:
					return
				}
			}
		}
	}
}

// Reading detects a disconnected subscriber without a fixture polling goroutine.
func rReader(_ *bufio.Scanner, c net.Conn) (int, error) { var b [1]byte; return c.Read(b[:]) }
func (f *Fixture) executeLocked(method string, p map[string]any, fault Fault, c net.Conn) (any, string, *subscriber) {
	address, _ := p["terminal"].(string)
	t := f.terminals[address]
	switch method {
	case "templates.list":
		v, _ := f.nativeTemplatesLocked()
		return hcclient.TemplatesResult{Templates: v}, "", nil
	case "hello":
		return hcclient.HelloResult{Generation: 1, Version: "0.1.14", Core: hcclient.CoreInfo{ID: f.CoreID, Name: "sum-test"}, Framed: true, Methods: methods, Features: []string{"framed", "events", "status", "shell"}}, "", nil
	case "terminal.run":
		args := decode[hcclient.RunParams](p)
		f.next++
		address = fmt.Sprintf("t%d", f.next)
		for f.terminals[address] != nil {
			f.next++
			address = fmt.Sprintf("t%d", f.next)
		}
		labels := args.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		argv := args.Argv
		if argv == nil {
			argv = []string{}
		}
		value := hcclient.Terminal{Address: address, Labels: labels, Argv: argv, Cwd: args.Cwd, State: "live", CreatedMS: f.next, Screen: "exact", Cols: 80, Rows: 24, Keep: args.Keep, RM: args.RM, Notify: !args.NoNotify, DieWithParent: !args.OutliveParent}

		if len(args.Argv) > 0 {
			templates, submits := f.nativeTemplatesLocked()
			for _, template := range templates {
				if template.Name == args.Argv[0] {
					if template.Error != nil {
						return nil, "config", nil
					}
					name := template.Name
					value.Template = &name
					value.Argv = []string{"sh", "-c", template.Command}
					if submit, ok := submits[name]; ok {
						key := submit
						value.Submit = &key
					}
					break
				}
			}
		}
		if args.Parent != "" {
			value.Parent = &args.Parent
		}
		if args.Name != "" {
			for _, existing := range f.terminals {
				if existing.State == "live" && existing.Name != nil && *existing.Name == args.Name {
					return nil, "name_taken", nil
				}
			}
			value.Name = &args.Name
		}
		f.terminals[address] = &terminal{Terminal: value, outlive: args.OutliveParent}
		f.eventLocked(address, "created", value)
		return hcclient.RunResult{Terminal: value, FirstMessage: fault.FirstMessage}, "", nil
	case "terminal.list":
		a := decode[hcclient.ListParams](p)
		all := f.snapshotLocked(hcclient.SubscribeParams{Labels: a.Labels})
		out := []hcclient.Terminal{}
		var hidden uint64
		for _, v := range all {
			if v.State == "live" || a.All {
				out = append(out, v)
			} else {
				hidden++
			}
		}
		return hcclient.ListResult{Terminals: out, HiddenFinished: hidden}, "", nil
	case "events.subscribe":
		a := decode[hcclient.SubscribeParams](p)
		s := &subscriber{params: a, messages: make(chan any, 256), conn: c}
		f.subscribers[s] = true
		return hcclient.SubscribeResult{Seq: f.seq, Terminals: f.snapshotLocked(a)}, "", s
	}
	if t == nil {
		return nil, "not_found", nil
	}
	switch method {
	case "terminal.get":
		return clone(t.Terminal), "", nil
	case "terminal.send":
		if t.State != "live" {
			return nil, "finished", nil
		}
		if t.Status != nil && t.Status.State == "blocked" {
			return nil, "blocked", nil
		}
		guard, _ := p["if"].(string)
		state := ""
		if t.Status != nil {
			state = t.Status.State
		}
		if guard != "" && guard != state {
			return nil, "status_mismatch", nil
		}
		text, _ := p["text"].(string)
		t.inputs = append(t.inputs, text)
		v := hcclient.SendResult{Turn: t.Turn}
		if state != "" {
			v.Status = &state
		}
		if fault.SendResult != nil {
			v = clone(*fault.SendResult)
		}
		return v, "", nil
	case "terminal.read":
		return hcclient.ReadResult{Text: strings.Join(t.inputs, "\n"), Complete: true, How: "screen"}, "", nil
	case "terminal.wait":
		v := hcclient.WaitResult{Terminal: address, Reason: "timeout", Turn: t.Turn, Children: []hcclient.WaitResult{}}
		if t.State == "finished" {
			v.Ended = true
			v.Reason = "exit"
			v.ExitCode = t.ExitCode
		} else if t.Status != nil {
			v.Status = &t.Status.State
			if t.Status.State != "working" {
				v.Ended = true
				v.Reason = "status"
			}
		}
		return v, "", nil
	case "terminal.label":
		a := decode[struct {
			Labels map[string]string `json:"labels"`
		}](p)
		for k, v := range a.Labels {
			if v == "" {
				delete(t.Labels, k)
			} else {
				t.Labels[k] = v
			}
		}
		f.eventLocked(address, "labels", map[string]any{"labels": t.Labels, "record": t.Terminal})
		return clone(t.Terminal), "", nil
	case "terminal.kill":
		f.exitLocked(address, 137, "killed")
		return clone(t.Terminal), "", nil
	case "terminal.interrupt":
		if t.State != "live" {
			return nil, "finished", nil
		}
		outcome := "unknown"
		key := "ctrl+c"
		if t.Status != nil {
			key = "esc"
			outcome = "stopped"
			if t.Status.State == "working" {
				t.Turn++
			}
			t.Status = &hcclient.Status{State: "idle"}
			f.eventLocked(address, "status", map[string]any{"status": t.Status, "records": map[string]hcclient.Status{}, "reported": t.Status != nil})
		}
		return hcclient.InterruptResult{Outcome: outcome, Key: key}, "", nil
	case "terminal.rm":
		if t.State == "live" {
			return nil, "live", nil
		}
		f.eventLocked(address, "removed", map[string]any{})
		delete(f.terminals, address)
		return hcclient.RemoveResult{Removed: address}, "", nil
	}
	return nil, "method_not_found", nil
}

// AssertNoForce checks the input boundary independently from domain receipts.
func (f *Fixture) AssertNoForce(t testing.TB) {
	t.Helper()
	for _, c := range f.Calls() {
		if _, ok := c.Params["force"]; ok {
			t.Errorf("unexpected force in %s", c.Method)
		}
	}
}

func (f *Fixture) SetTemplates(templates []hcclient.TemplateInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.templates = append([]hcclient.TemplateInfo{}, templates...)
}

// SetTemplateSubmit configures the fixture's template file contents separately
// from templates.list, whose upstream result does not expose the submit key.
func (f *Fixture) SetTemplateSubmit(name, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.templateSubmits == nil {
		f.templateSubmits = map[string]string{}
	}
	f.templateSubmits[name] = key
}

// HoldNext synchronizes one native method boundary without holding the fixture
// mutex. Closing the fixture releases every waiter and never executes its effect.
func (f *Fixture) HoldNext(method, address string) (<-chan struct{}, func()) {
	hold := &requestHold{method: method, address: address, entered: make(chan struct{}), release: make(chan struct{})}
	f.mu.Lock()
	f.holds = append(f.holds, hold)
	f.mu.Unlock()
	return hold.entered, func() { hold.once.Do(func() { close(hold.release) }) }
}
