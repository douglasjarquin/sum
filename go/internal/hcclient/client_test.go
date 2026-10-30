package hcclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, handle func(net.Conn, *bufio.Reader)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "hc.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); handle(c, bufio.NewReader(c)) }()
		}
	}()
	return path
}
func request(r *bufio.Reader) map[string]any {
	b, _ := r.ReadBytes('\n')
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	return v
}
func reply(c net.Conn, id any, result any) {
	_ = json.NewEncoder(c).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func hello(c net.Conn, r *bufio.Reader, core string) {
	q := request(r)
	reply(c, q["id"], map[string]any{"generation": 1, "version": "0.1.14", "core": map[string]string{"id": core, "name": "scratch"}, "methods": []string{"terminal.run", "terminal.get", "terminal.send", "events.subscribe"}, "features": []string{"events", "status"}, "framed": true})
}
func TestNativeResults(t *testing.T) {
	socket := serve(t, func(c net.Conn, r *bufio.Reader) {
		hello(c, r, "core-a")
		q := request(r)
		p := q["params"].(map[string]any)
		if _, ok := p["force"]; ok {
			t.Error("force must never be sent")
		}
		switch q["method"] {
		case "terminal.run":
			reply(c, q["id"], map[string]any{"address": "t7", "state": "live", "status": map[string]string{"state": "done"}, "created_ms": 3, "labels": map[string]string{}, "argv": []string{"codex"}, "cwd": "/tmp", "screen": "exact", "cols": 80, "rows": 24})
		case "terminal.send":
			reply(c, q["id"], map[string]any{"turn": 2, "status": "done", "started": nil})
		case "terminal.get":
			if _, ok := p["caller"]; ok {
				t.Error("passive get carried caller")
			}
			reply(c, q["id"], map[string]any{"address": "t7", "state": "live", "created_ms": 3, "labels": map[string]string{}, "argv": []string{"codex"}, "cwd": "/tmp", "screen": "exact", "cols": 80, "rows": 24})
		}
	})
	c := Client{Socket: socket, CoreID: "core-a"}
	run, err := c.Run(context.Background(), RunParams{Argv: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Address != "t7" || run.Status.State != "done" {
		t.Fatalf("flattened native result lost: %#v", run)
	}
	send, err := c.Send(context.Background(), SendParams{Terminal: "t7", Text: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if send.Started != nil || send.Turn != 2 {
		t.Fatalf("native uncertainty lost: %#v", send)
	}
	if _, err = c.Get(context.Background(), "t7"); err != nil {
		t.Fatal(err)
	}
}
func TestIdentityAndLostResponse(t *testing.T) {
	for _, tc := range []struct {
		name, core string
		effect     Effect
	}{{"other core", "core-b", NotSubmitted}, {"lost response", "core-a", Uncertain}} {
		t.Run(tc.name, func(t *testing.T) {
			socket := serve(t, func(c net.Conn, r *bufio.Reader) {
				hello(c, r, tc.core)
				if tc.core == "core-a" {
					request(r)
				}
			})
			c := Client{Socket: socket, CoreID: "core-a"}
			_, err := c.Send(context.Background(), SendParams{Terminal: "t7", Text: "once"})
			var ce *Error
			if !errors.As(err, &ce) || ce.Effect != tc.effect {
				t.Fatalf("effect: %v", err)
			}
		})
	}
}
func TestProtocolFailureAndCancellation(t *testing.T) {
	for _, wire := range []string{"{\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{}}\n", "{bad}\n", "{\"jsonrpc\":\"2.0\""} {
		t.Run(wire, func(t *testing.T) {
			socket := serve(t, func(c net.Conn, r *bufio.Reader) { hello(c, r, "core-a"); request(r); c.Write([]byte(wire)) })
			c := Client{Socket: socket, CoreID: "core-a"}
			_, err := c.Send(context.Background(), SendParams{Terminal: "t7", Text: "once"})
			var ce *Error
			if !errors.As(err, &ce) || ce.Effect != Uncertain {
				t.Fatalf("%v", err)
			}
		})
	}
	socket := serve(t, func(c net.Conn, r *bufio.Reader) { hello(c, r, "core-a"); request(r); r.ReadByte() })
	c := Client{Socket: socket, CoreID: "core-a"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.Send(ctx, SendParams{Terminal: "t7", Text: "once"})
	if err == nil {
		t.Fatal("deadline ignored")
	}
}
func TestEndpointAuthority(t *testing.T) {
	a := Endpoint{Schema: 2, Backend: "hc", CoreID: "a", Address: "t7", Task: "task", Role: "worker", Attempt: "one", BindingGeneration: 1, CreatedMS: 1}
	b := a
	b.CoreID = "b"
	if a.SameAuthority(b) {
		t.Fatal("core collision authorized")
	}
	b = a
	b.Attempt = "two"
	if a.SameAuthority(b) {
		t.Fatal("attempt collision authorized")
	}
	if !a.SameAuthority(a) {
		t.Fatal("self identity refused")
	}
}

func TestRefusalAndBound(t *testing.T) {
	for _, kind := range []string{"blocked", "status_mismatch", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			socket := serve(t, func(c net.Conn, r *bufio.Reader) {
				hello(c, r, "core-a")
				q := request(r)
				_ = json.NewEncoder(c).Encode(map[string]any{"jsonrpc": "2.0", "id": q["id"], "error": map[string]any{"code": -32000, "message": kind, "data": map[string]string{"kind": kind, "hint": "native diagnostic"}}})
			})
			c := Client{Socket: socket, CoreID: "core-a"}
			_, err := c.Send(context.Background(), SendParams{Terminal: "t7", Text: "once"})
			var ce *Error
			want := Refused
			if kind == "unavailable" {
				want = Uncertain
			}
			if !errors.As(err, &ce) || ce.Effect != want || ce.RPC == nil {
				t.Fatalf("%v", err)
			}
		})
	}
	socket := serve(t, func(c net.Conn, r *bufio.Reader) {
		hello(c, r, "core-a")
		request(r)
		c.Write([]byte(strings.Repeat("x", 1025) + "\n"))
	})
	c := Client{Socket: socket, CoreID: "core-a", MaxResponseBytes: 1024}
	_, err := c.Send(context.Background(), SendParams{Terminal: "t7", Text: "once"})
	var ce *Error
	if !errors.As(err, &ce) || ce.Effect != Uncertain {
		t.Fatalf("%v", err)
	}
	c.Socket = filepath.Join(t.TempDir(), "absent")
	_, err = c.Send(context.Background(), SendParams{})
	if !errors.As(err, &ce) || ce.Effect != NotSubmitted {
		t.Fatalf("%v", err)
	}
}
func TestSubscriptionIndependent(t *testing.T) {
	socket := serve(t, func(c net.Conn, r *bufio.Reader) {
		hello(c, r, "core-a")
		q := request(r)
		if q["method"] == "events.subscribe" {
			reply(c, q["id"], map[string]any{"seq": 1, "terminals": []any{}})
			for _, method := range []string{"event", "missed", "heartbeat"} {
				_ = json.NewEncoder(c).Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": map[string]any{"seq": 2}})
			}
			r.ReadByte()
		} else {
			reply(c, q["id"], map[string]any{"turn": 1, "started": false})
		}
	})
	c := Client{Socket: socket, CoreID: "core-a"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := c.Subscribe(ctx, SubscribeParams{Labels: map[string]string{"sum.installation": "scratch"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = c.Send(ctx, SendParams{Terminal: "t7", Text: "next"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"event", "missed", "heartbeat"} {
		n, err := s.Next()
		if err != nil || n.Method != want {
			t.Fatalf("%#v %v", n, err)
		}
	}
	cancel()
	if _, err = s.Next(); err == nil {
		t.Fatal("subscription ignored cancellation")
	}
}

func TestReadOnlyDiscoveryDoesNotAuthorizeEffects(t *testing.T) {
	socket := serve(t, func(c net.Conn, r *bufio.Reader) {
		hello(c, r, "discovered-core")
		if q := request(r); len(q) > 0 {
			t.Error("discovery sent an operation")
		}
	})
	h, err := Inspect(context.Background(), socket)
	if err != nil || h.Core.ID != "discovered-core" {
		t.Fatalf("%#v %v", h, err)
	}
	c := Client{Socket: socket}
	_, err = c.Send(context.Background(), SendParams{Terminal: "t1b", Text: "no"})
	var ce *Error
	if !errors.As(err, &ce) || ce.Effect != NotSubmitted {
		t.Fatalf("missing expected identity allowed effects: %v", err)
	}
}

func TestTemplatesAreObservedOnSelectedCore(t *testing.T) {
	socket := serve(t, func(c net.Conn, r *bufio.Reader) {
		q := request(r)
		reply(c, q["id"], HelloResult{Generation: 1, Version: "0.1.14", Core: CoreInfo{ID: "core-a", Name: "scratch"}, Methods: []string{"templates.list"}, Features: []string{}})
		q = request(r)
		reply(c, q["id"], map[string]any{"templates": []map[string]any{{"name": "sum-codex-bound", "description": "qualified", "command": "'/codex' '--no-daemon'", "error": nil}}})
	})
	c := Client{Socket: socket, CoreID: "core-a"}
	v, err := c.Templates(context.Background())
	if err != nil || len(v.Templates) != 1 || v.Templates[0].Name != "sum-codex-bound" {
		t.Fatalf("%#v %v", v, err)
	}
}
