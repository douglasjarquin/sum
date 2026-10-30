package metadata

import (
	"bytes"
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type metadataLab struct {
	t             *testing.T
	s             *store.Store
	f             *hctest.Fixture
	owner, worker store.Endpoint
	task          *ordjson.Object
	meta          *ordjson.Object
}

func newMetadataLab(t *testing.T) *metadataLab {
	t.Helper()
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(physical, "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	l := &metadataLab{t: t, s: s, f: f, meta: emptyMetadata()}
	l.owner = store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t0", Role: "coordinator", Attempt: "coord-1", BindingGeneration: 1, CreatedMS: 100, Cwd: physical}
	owner := l.bind(l.owner)
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	l.worker = l.owner
	l.worker.Role = "worker"
	l.worker.Task = "t-aaaaaaaaaaaa"
	l.worker.Attempt = "worker-1"
	l.worker.Address = "t1"
	l.worker.CreatedMS = 101
	l.worker.Cwd = t.TempDir()
	l.bind(l.worker)
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(2))
	task.Set("id", l.worker.Task)
	task.Set("status", "running")
	task.Set("machine", host.ID)
	task.Set("repository", physical)
	task.Set("worktree", l.worker.Cwd)
	task.Set("worker_endpoint", l.worker.Object())
	task.Set("parent", l.owner.Object())
	l.task = task
	l.save()
	l.meta.Set("enabled", true)
	l.write()
	return l
}
func (l *metadataLab) bind(e store.Endpoint) *ordjson.Object {
	l.t.Helper()
	pid := uint32(e.CreatedMS)
	terminal := hcclient.Terminal{Address: e.Address, State: "live", CreatedMS: e.CreatedMS, Cwd: e.Cwd, PID: &pid, Foreground: &hcclient.Foreground{PID: int32(pid), Name: "codex"}, Status: &hcclient.Status{State: "working"}, Labels: map[string]string{"unrelated": "preserved"}}
	l.f.SetTerminal(terminal)
	var task any
	if e.Task != "" {
		task = e.Task
	}
	record, err := l.s.Register(e, e.Role, task, incarnation.NativeEvidence{Endpoint: e.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now()))
	if err != nil {
		l.t.Fatal(err)
	}
	kind := map[string]string{"worker": "worker-init", "coordinator": "coordinator-init"}[e.Role]
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: kind, Attempt: e.Attempt})
	record.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(l.s.Sessions, store.RegistrationKey(e)+".json"), record); err != nil {
		l.t.Fatal(err)
	}
	return record
}
func (l *metadataLab) save() {
	l.t.Helper()
	if err := l.s.SaveTask(l.task); err != nil {
		l.t.Fatal(err)
	}
}
func (l *metadataLab) write() {
	l.t.Helper()
	if err := writeMetadata(l.s, l.meta); err != nil {
		l.t.Fatal(err)
	}
}
func (l *metadataLab) run() *ordjson.Object {
	l.t.Helper()
	result := newPass(l.s, l.meta, "", false).run(nil, "test")
	l.write()
	return result
}
func (l *metadataLab) labels(address string) map[string]string {
	l.t.Helper()
	client := l.f.Client()
	terminal, err := client.Get(context.Background(), address)
	if err != nil {
		l.t.Fatal(err)
	}
	return terminal.Labels
}
func (l *metadataLab) question(status string) {
	q := ordjson.NewObject()
	q.Set("id", "q1")
	q.Set("status", status)
	q.Set("key", "choice")
	q.Set("created_at", store.Now())
	q.Set("answered_at", store.Now())
	l.task.Set("questions", []any{q})
	l.save()
}
func TestProjectionCountsOnlyHumanDecisionsAndShowsUnknownCoverage(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		t.Run(fmt.Sprint(damaged), func(t *testing.T) {
			l := newMetadataLab(t)
			l.question("open")
			if damaged {
				if err := os.MkdirAll(filepath.Join(l.s.Tasks, "t-bbbbbbbbbbbb"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(l.s.Tasks, "t-bbbbbbbbbbbb", "task.json"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			l.run()
			label := l.labels("t0")["sum.inbox"]
			if !strings.Contains(label, "1 decision") {
				t.Fatalf("label %q", label)
			}
			if strings.Contains(label, "unknown") != damaged {
				t.Fatalf("coverage %q", label)
			}
			before := len(l.f.Calls())
			l.run()
			if len(l.f.Calls()) != before {
				t.Fatal("unchanged projection contacted runtime")
			}
		})
	}
}
func TestProjectionRoutineInspectionAndPassiveStates(t *testing.T) {
	for _, tc := range []struct{ name, status, want string }{{"decision", "open", "needs-decision"}, {"answer", "answered", "answer-pending"}, {"running", "applied", "running"}} {
		t.Run(tc.name, func(t *testing.T) {
			l := newMetadataLab(t)
			l.question(tc.status)
			l.run()
			if got := l.labels("t1")["sum.state"]; got != tc.want {
				t.Fatalf("state %q want %q", got, tc.want)
			}
			task, _ := l.s.ReadTask(l.worker.Task)
			if status, _ := task.Get("status"); status != "running" {
				t.Fatal("projection changed task lifecycle")
			}
		})
	}
}
func TestProjectionSkipsWithoutCapabilityAndWritesOnlyVerifiedPanes(t *testing.T) {
	l := newMetadataLab(t)
	l.f.SetForeground("t1", hcclient.Foreground{PID: 999, Name: "shell"})
	l.run()
	if value := l.labels("t1")["sum.state"]; value != "" {
		t.Fatal("unverified process got task labels")
	}
	l.f.QueueFault("terminal.label", hctest.Fault{RefuseKind: "config"})
	l.question("open")
	l.meta.Set("root", nil)
	result := l.run()
	if root := asObject(get(result, "root")); get(root, "outcome") != "failed" {
		t.Fatalf("native capability refusal %v", root)
	}
}
func TestInboxTokenKeepsUnknownAndDecisionsWithinNativeLimit(t *testing.T) {
	snapshot := inboxview.Snapshot{Complete: false}
	snapshot.Counts.Decisions = 123456
	snapshot.Counts.Coordinator = 99999
	snapshot.Counts.Worker = 99999
	token := inboxToken(snapshot)
	if !strings.Contains(token, "unknown") || !strings.Contains(token, "123456 decisions") || len(token) > tokenMax {
		t.Fatalf("token %q", token)
	}
}
func TestSourceDerivesFromInstanceNotHomeBasename(t *testing.T) {
	a := newMetadataLab(t)
	b := newMetadataLab(t)
	one, err := Source(a.s)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Source(b.s)
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("two instances share label source")
	}
	again, err := Source(a.s)
	if err != nil || again != one {
		t.Fatal("source was unstable")
	}
}
func TestProjectionLockBusySkipsWithoutStateWriteOrHerdrCall(t *testing.T) {
	l := newMetadataLab(t)
	before, err := os.ReadFile(Path(l.s))
	if err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(filepath.Join(l.s.Home, Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err = syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(holder.Fd()), syscall.LOCK_UN)
	old := lockWait
	lockWait = 20 * time.Millisecond
	defer func() { lockWait = old }()
	calls := len(l.f.Calls())
	result := After(l.s, "", nil, "test")
	if get(result, "degraded") != true {
		t.Fatal("busy metadata lock was not exposed")
	}
	after, err := os.ReadFile(Path(l.s))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || len(l.f.Calls()) != calls {
		t.Fatal("busy projection wrote state or contacted runtime")
	}
}
func TestProjectionBudgetExhaustedSkipsRemainingTasks(t *testing.T) {
	l := newMetadataLab(t)
	old := passBudget
	passBudget = time.Nanosecond
	defer func() { passBudget = old }()
	result := l.run()
	rows := asList(get(result, "tasks"))
	if len(rows) != 1 || get(asObject(rows[0]), "outcome") != "skipped-budget" {
		t.Fatalf("budget result %v", result)
	}
}
func TestProjectionRefusedReleaseStaysPendingUntilCleared(t *testing.T) {
	l := newMetadataLab(t)
	l.run()
	prior := asObject(get(asObject(get(l.meta, "resources")), l.worker.Task))
	l.f.QueueFault("terminal.label", hctest.Fault{RefuseKind: "config"})
	p := newPass(l.s, l.meta, "", false)
	if err := p.clearNative(context.Background(), prior); err == nil {
		t.Fatal("native clear refusal was swallowed")
	}
	if err := p.clearNative(context.Background(), prior); err != nil {
		t.Fatal(err)
	}
	if l.labels("t1")["sum.state"] != "" {
		t.Fatal("labels not cleared on retry")
	}
	if l.labels("t1")["unrelated"] != "preserved" {
		t.Fatal("unrelated label cleared")
	}
}
func TestProjectionRebindClearsPreviousPaneAndWritesNew(t *testing.T) {
	l := newMetadataLab(t)
	l.run()
	replacement := l.worker
	replacement.Address = "t2"
	replacement.Attempt = "worker-2"
	replacement.BindingGeneration++
	replacement.CreatedMS++
	l.bind(replacement)
	l.task.Set("worker_endpoint", replacement.Object())
	l.save()
	l.run()
	if l.labels("t1")["sum.state"] != "" || l.labels("t2")["sum.state"] == "" {
		t.Fatal("rebind left old labels or did not project replacement")
	}
}
