package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestCollectUnreachableCoreNextExecutesSavedRoleInspection(t *testing.T) {
	for _, role := range []string{"worker", "reviewer"} {
		for _, phase := range []string{"get", "wait"} {
			t.Run(role+"/"+phase, func(t *testing.T) {
				runtime, s := ordinaryInitLab(t)
				session, err := s.ClaimCoordinator("", "unreachable runtime", false)
				if err != nil {
					t.Fatal(err)
				}
				machine, err := s.Machine()
				if err != nil {
					t.Fatal(err)
				}
				ep := store.Endpoint{Schema: 2, Backend: "hc", Machine: machine.ID, Socket: filepath.Join(t.TempDir(), "missing.sock"), CoreID: "saved-core", Session: "sum-test-unreachable", Address: "t7", CreatedMS: 7, Task: "t-aaaaaaaaaaaa", Role: role, Attempt: "x-aaaaaaaaaaaa", BindingGeneration: 1, Cwd: t.TempDir()}
				if phase == "wait" {
					f := hctest.New(t)
					ep.Socket, ep.CoreID = f.Socket, f.CoreID
					f.SetTerminal(hcclient.Terminal{Address: ep.Address, CreatedMS: ep.CreatedMS, Cwd: ep.Cwd, State: "live"})
					reached, release := f.HoldNext("terminal.wait", ep.Address)
					t.Cleanup(release)
					stop := make(chan struct{})
					t.Cleanup(func() { close(stop) })
					go func() {
						select {
						case <-reached:
							f.Close()
						case <-stop:
						}
					}()
				}
				record, err := reservations.NewAttempt("worker", session.Owner(), ep.Cwd, store.Now(), "running", nil)
				if err != nil {
					t.Fatal(err)
				}
				record.Set("id", ep.Attempt)
				launch := ordjson.NewObject()
				launch.Set("stage", "running")
				launch.Set("effect", "created")
				record.Set("native_launch", launch)
				task := ordjson.NewObject()
				task.Set("schema", 2)
				task.Set("id", ep.Task)
				task.Set("status", "running")
				task.Set("owner", session.Owner())
				task.Set("machine", machine.ID)
				task.Set("worktree", ep.Cwd)
				if role == "worker" {
					task.Set("worker_endpoint", ep.Object())
					task.Set("execution", reservations.NewExecution(record))
				} else {
					reviewer := ep.Object()
					reviewer.Set("state", "running")
					reviewer.Set("native_launch", launch)
					task.Set("reviewer", reviewer)
				}
				if err := s.SaveTask(task); err != nil {
					t.Fatal(err)
				}
				var out, stderr bytes.Buffer
				helper := filepath.Join(runtime, "bin", "sumctl")
				command := NewRoot(helper, &out, &stderr)
				command.SetArgs([]string{"--home", s.Root, "--format", "json", "collect", "--claim", session.Token(), "--task", ep.Task, "--budget", "1s"})
				started := time.Now()
				if err := command.Execute(); err != nil {
					t.Fatalf("collect: %v %s", err, stderr.String())
				}
				view := decodeCLIMap(t, out.String())
				items, _ := view["items"].([]any)
				if len(items) != 1 || time.Since(started) > time.Second {
					t.Fatalf("unreachable collection: %v", view)
				}
				item := asMap(items[0])
				if item["kind"] != "core-unreachable" {
					t.Fatalf("unreachable item: %v", item)
				}
				printed, _ := item["next"].([]any)
				argv := make([]string, len(printed))
				for i, value := range printed {
					argv[i], _ = value.(string)
				}
				if len(argv) == 0 {
					t.Fatalf("missing next argv: %v", item)
				}
				out.Reset()
				stderr.Reset()
				command = NewRoot(argv[0], &out, &stderr)
				command.SetArgs(append([]string{"--format", "json"}, argv[1:]...))
				if err := command.Execute(); err != nil {
					t.Fatalf("emitted next argv %q failed: %v %s", argv, err, stderr.String())
				}
				inspection := decodeCLIMap(t, out.String())
				if item["role"] != role || asMap(inspection["endpoint"])["address"] != ep.Address || inspection["attempt"] != ep.Attempt || asMap(inspection["launch"])["stage"] != "running" || !strings.Contains(asString(inspection["note"]), "core-unreachable") {
					t.Fatalf("role=%s item=%v inspection=%v", role, item, inspection)
				}
			})
		}
	}
}
