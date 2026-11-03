package prepare

import (
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestStalledOpeningUsesOriginalCreationRoute(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "stalled test", false)
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	worker, err := reservations.NewAttempt("worker", session.Owner(), checkout, store.Now(), "held", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("id", "t-0123456789ab")
	task.Set("worktree", checkout)
	task.Set("execution", reservations.NewExecution(worker))
	attempt := asString(policyField(worker, "id"))
	params := hcclient.RunParams{Cwd: checkout, Env: map[string]string{"HC_SOCKET": filepath.Join(t.TempDir(), "original.sock"), "SUM_CORE_ID": "original-core", "SUM_SESSION": "original-session", "SUM_TASK": "t-0123456789ab", "SUM_ROLE": "worker", "SUM_ATTEMPT": attempt}}
	value, _ := ordjson.FromValue(params)
	native := ordjson.NewObject()
	native.Set("intent", value)
	worker.Set("native_launch", native)
	// No current native coordinator or valid current worker route is available.
	if err = ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), ordjson.NewObject()); err != nil {
		t.Fatal(err)
	}
	route, err := stalledRoute(s, task, "t-0123456789ab", attempt)
	if err != nil {
		t.Fatal(err)
	}
	if route.Socket != params.Env["HC_SOCKET"] || route.CoreID != "original-core" || route.Session != "original-session" {
		t.Fatal("stalled recovery selected a new route")
	}
	params.Env["SUM_ATTEMPT"] = "different"
	value, _ = ordjson.FromValue(params)
	native.Set("intent", value)
	if _, err = stalledRoute(s, task, "t-0123456789ab", attempt); err == nil {
		t.Fatal("mismatched saved attempt accepted")
	}
}
