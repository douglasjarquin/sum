package hookstatus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
)

func TestObserverReconnectsOnlyReadTransportFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"get dropped", &hcclient.Error{Method: "terminal.get", Cause: io.EOF}, true},
		{"list dropped", &hcclient.Error{Method: "terminal.list", Cause: io.ErrUnexpectedEOF}, true},
		{"native unavailable", &hcclient.Error{Method: "terminal.list", RPC: &hcclient.RPCError{Data: json.RawMessage(`{"kind":"unavailable"}`)}}, true},
		{"native notfound", &hcclient.Error{Method: "terminal.get", RPC: &hcclient.RPCError{Data: json.RawMessage(`{"kind":"not_found"}`)}}, false},
		{"wrong core", &hcclient.Error{Method: "terminal.get", Cause: errors.New("core identity, pinned version or method mismatch")}, false},
		{"uncertain send", &hcclient.Error{Method: "terminal.send", Effect: hcclient.Uncertain, Cause: io.EOF}, false},
		{"empty durable file", io.EOF, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := retryNativeObservation(context.Background(), test.err); got != test.retry {
				t.Fatalf("retry = %v", got)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if retryNativeObservation(ctx, &hcclient.Error{Method: "terminal.get", Cause: io.EOF}) {
		t.Fatal("cancellation reconnected")
	}
	if retrySubscription(context.Background(), errors.New("invalid HC notification")) {
		t.Fatal("protocol mismatch reconnected")
	}
}
