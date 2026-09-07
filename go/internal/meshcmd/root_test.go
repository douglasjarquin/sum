package meshcmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHelpDoesNotStartServer(t *testing.T) {
	started := false
	var out, errOut bytes.Buffer
	root := NewRoot(strings.NewReader(""), &out, &errOut, func(context.Context) error {
		started = true
		return nil
	})
	root.SetArgs([]string{"--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if started || !strings.Contains(out.String(), "Sum-owned Herdr Mesh MCP bridge") || errOut.Len() != 0 {
		t.Fatalf("started=%v stdout=%q stderr=%q", started, out.String(), errOut.String())
	}
}

func TestDefaultInvocationSelectsServerMode(t *testing.T) {
	started := false
	root := NewRoot(nil, &bytes.Buffer{}, &bytes.Buffer{}, func(context.Context) error {
		started = true
		return nil
	})
	root.SetArgs(nil)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("default invocation did not select server mode")
	}
}

func TestUnknownModeIsRejectedWithoutStartingServer(t *testing.T) {
	started := false
	var errOut bytes.Buffer
	root := NewRoot(nil, &bytes.Buffer{}, &errOut, func(context.Context) error {
		started = true
		return nil
	})
	root.SetArgs([]string{"--mode", "client"})
	if err := root.ExecuteContext(context.Background()); err == nil || started || !strings.Contains(err.Error(), "only server mode") {
		t.Fatalf("err=%v started=%v stderr=%q", err, started, errOut.String())
	}
}
