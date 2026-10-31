package hctest

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"testing"
	"time"
)

func TestHoldNextDoesNotBlockOtherNativeRecipientsAndCloseReleasesWaiter(t *testing.T) {
	f := New(t)
	f.SetTerminal(hcclient.Terminal{Address: "t1", State: "live"})
	f.SetTerminal(hcclient.Terminal{Address: "t2", State: "live"})
	entered, _ := f.HoldNext("terminal.get", "t1")
	done := make(chan error, 1)
	client := f.Client()
	go func() { _, err := client.Get(context.Background(), "t1"); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native boundary was not entered")
	}
	if _, err := client.Get(context.Background(), "t2"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed held connection succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("fixture close leaked a held request")
	}
}

func TestExitRetainsFinishedTimestampInNativeSnapshot(t *testing.T) {
	f := New(t)
	f.SetTerminal(hcclient.Terminal{Address: "t1", State: "live"})
	f.Exit("t1", 0)
	client := f.Client()
	terminal, err := client.Get(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if terminal.FinishedMS == nil {
		t.Fatal("finished event timestamp missing from terminal snapshot")
	}
}

func TestHoldReplyExposesEffectAndCancellationReleasesWaiter(t *testing.T) {
	for _, mode := range []string{"release", "cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			f := New(t)
			entered, release := f.HoldReplyNext("terminal.run", "")
			defer release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := f.Client()
			done := make(chan error, 1)
			go func() {
				_, err := client.Run(ctx, hcclient.RunParams{Argv: []string{"fixture-program"}, Cwd: t.TempDir()})
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("native Run effect not reached")
			}
			snapshot, err := client.List(context.Background(), hcclient.ListParams{All: true})
			if err != nil || len(snapshot.Terminals) != 1 {
				t.Fatalf("effect hidden while response held: %v %v", snapshot, err)
			}
			switch mode {
			case "release":
				release()
			case "cancel":
				cancel()
			case "close":
				f.Close()
			}
			select {
			case err := <-done:
				if (mode == "release") != (err == nil) {
					t.Fatalf("%s result: %v", mode, err)
				}
			case <-time.After(time.Second):
				t.Fatal("held response leaked client")
			}
			f.Close()
		})
	}
}
