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
