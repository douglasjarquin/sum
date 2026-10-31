package hctest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
)

func TestNativeLifecycleAndInput(t *testing.T) {
	f := hctest.New(t)
	c := f.Client()
	ctx := context.Background()
	run, err := c.Run(ctx, hcclient.RunParams{Argv: []string{"fake"}, Labels: map[string]string{"sum.task": "one"}, OutliveParent: true, NoNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	if run.Address != "t1" || run.Status != nil {
		t.Fatalf("unknown launch inferred ready: %#v", run)
	}
	f.SetStatus(run.Address, "blocked")
	_, err = c.Send(ctx, hcclient.SendParams{Terminal: run.Address, Text: "do not answer"})
	var ce *hcclient.Error
	if !errors.As(err, &ce) || ce.Effect != hcclient.Refused {
		t.Fatalf("blocked send: %v", err)
	}
	if len(f.Inputs(run.Address)) != 0 {
		t.Fatal("blocked input buffered")
	}
	f.SetStatus(run.Address, "done")
	sent, err := c.Send(ctx, hcclient.SendParams{Terminal: run.Address, Text: "next"})
	if err != nil || sent.Started != nil {
		t.Fatalf("native send: %#v %v", sent, err)
	}
	if got := f.Inputs(run.Address); len(got) != 1 || got[0] != "next" {
		t.Fatal(got)
	}
}
func TestLostResponsePreservesEffect(t *testing.T) {
	f := hctest.New(t)
	c := f.Client()
	f.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	_, err := c.Run(context.Background(), hcclient.RunParams{Argv: []string{"fake"}})
	var ce *hcclient.Error
	if !errors.As(err, &ce) || ce.Effect != hcclient.Uncertain {
		t.Fatal(err)
	}
	if len(f.Terminals()) != 1 {
		t.Fatal("lost response erased native effect")
	}
}
func TestScopedEventsAndForeground(t *testing.T) {
	f := hctest.New(t)
	c := f.Client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	one, _ := c.Run(ctx, hcclient.RunParams{Argv: []string{"fake"}, Labels: map[string]string{"sum.task": "one"}})
	two, _ := c.Run(ctx, hcclient.RunParams{Argv: []string{"fake"}, Labels: map[string]string{"sum.task": "two"}})
	s, err := c.Subscribe(ctx, hcclient.SubscribeParams{Labels: map[string]string{"sum.task": "one"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.Snapshot.Terminals) != 1 || s.Snapshot.Terminals[0].Address != one.Address {
		t.Fatal(s.Snapshot)
	}
	f.SetStatus(two.Address, "working")
	f.SetStatus(one.Address, "done")
	event, err := s.Next()
	if err != nil || event.Method != "event" {
		t.Fatalf("%#v %v", event, err)
	}
	f.SetForeground(one.Address, hcclient.Foreground{PID: 45, Name: "shell", Argv: []string{"sh"}, Cwd: "/tmp"})
	got, err := c.Get(ctx, one.Address)
	if err != nil || got.Foreground.PID != 45 {
		t.Fatalf("%#v %v", got, err)
	}
	f.Miss()
	for {
		n, e := s.Next()
		if e != nil {
			t.Fatal(e)
		}
		if n.Method == "missed" {
			break
		}
	}
	f.DisconnectSubscribers()
	if _, err = s.Next(); err == nil {
		t.Fatal("disconnect ignored")
	}
}
func TestParentExitAndNonKeptIdentity(t *testing.T) {
	f := hctest.New(t)
	c := f.Client()
	ctx := context.Background()
	p, _ := c.Run(ctx, hcclient.RunParams{Argv: []string{"parent"}})
	kept, _ := c.Run(ctx, hcclient.RunParams{Argv: []string{"worker"}, Parent: p.Address, OutliveParent: true})
	dependent, _ := c.Run(ctx, hcclient.RunParams{Argv: []string{"worker"}, Parent: p.Address})
	f.Exit(p.Address, 0)
	a, _ := c.Get(ctx, kept.Address)
	b, _ := c.Get(ctx, dependent.Address)
	if a.State != "live" || b.State != "finished" {
		t.Fatalf("%s %s", a.State, b.State)
	}
	f.Exit(kept.Address, 0)
	a, _ = c.Get(ctx, kept.Address)
	if a.State != "finished" || a.Status != nil || a.Foreground != nil {
		t.Fatal("finished worker became shell")
	}
}

func TestTemplateRunAttestsNativeShellCommand(t *testing.T) {
	f := hctest.New(t)
	f.SetTemplates([]hcclient.TemplateInfo{{Name: "sum-qualified", Command: "'/codex' '--no-daemon'"}})
	f.SetTemplateSubmit("sum-qualified", "tab")
	c := f.Client()
	v, err := c.Run(context.Background(), hcclient.RunParams{Argv: []string{"sum-qualified", "pointer"}, OutliveParent: true, NoNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.Template == nil || *v.Template != "sum-qualified" || v.Submit == nil || *v.Submit != "tab" || len(v.Argv) != 3 || v.Argv[0] != "sh" || v.Argv[2] != "'/codex' '--no-daemon'" {
		t.Fatalf("template attestation: %#v", v)
	}
}
