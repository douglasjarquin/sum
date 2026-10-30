package execution

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"testing"
)

func TestNativeEndpointProcessesNeverBecomeExemptShells(t *testing.T) {
	l := newLab(t)
	reviewer := ordjson.NewObject()
	reviewer.Set("address", "t2")
	reviewer.Set("role", "reviewer")
	reviewer.Set("core_id", l.fixture.CoreID)
	task := ordjson.NewObject()
	task.Set("reviewer", reviewer)
	task.Set("worktree", l.checkout)
	l.plantLsof([]map[string]any{{"pid": 5151, "cwd": l.checkout}})
	excluded := EndpointShells(l.identity(), l.runtime, task)
	if len(excluded) != 0 {
		t.Fatal("non-kept native terminal became an exempt shell")
	}
	inside, err := proc.ProcessesBoundTo(l.checkout, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if len(inside) != 1 || inside[0].PID != 5151 {
		t.Fatal("reviewer checkout writer was hidden")
	}
}
