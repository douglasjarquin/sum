package store

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
)

func TestContextUsesObservedWorkerDirectory(t *testing.T) {
	runtimeRoot := t.TempDir()
	worktree := t.TempDir()
	f := hctest.New(t)
	f.SetTerminal(hcclient.Terminal{Address: "t1", State: "live", CreatedMS: 1, Cwd: worktree})
	for key, value := range map[string]string{"SUM_SESSION": "cwd-lab", "SUM_CORE_ID": f.CoreID, "HC_SOCKET": f.Socket, "HC_TERMINAL_ID": "t1", "SUM_ROLE": "worker", "SUM_TASK": "t-aaaaaaaaaaaa", "SUM_ATTEMPT": "attempt", "SUM_BINDING_GENERATION": "1"} {
		t.Setenv(key, value)
	}
	ctx, err := Context(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cwd := EndpointFromContext(ctx).Cwd; cwd != worktree {
		t.Fatalf("caller cwd=%q, want actual worker worktree %q (runtime root is %q)", cwd, worktree, runtimeRoot)
	}
}
