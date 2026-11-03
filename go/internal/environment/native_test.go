package environment

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type serviceLab struct {
	s            *store.Store
	hc           *hctest.Fixture
	task, caller *ordjson.Object
	worktree     string
}

func newServiceLab(t *testing.T) *serviceLab {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	hc := hctest.New(t)
	cwd := t.TempDir()
	session, err := s.ClaimCoordinator("", "service conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	caller, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	ep := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: hc.CoreID, Socket: hc.Socket, Session: "sum-test-services", BindingGeneration: 1, Cwd: cwd}
	worker, err := reservations.NewAttempt("worker", session.Owner(), cwd, store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := stringField(worker, "id")
	we := ep
	we.Role = "worker"
	we.Task = "t-abcdef123456"
	we.Attempt = id
	we.Address = "t2"
	we.CreatedMS = 2
	workerPID := uint32(40000003)
	hc.SetTerminal(hcclient.Terminal{Address: we.Address, CreatedMS: we.CreatedMS, Cwd: cwd, State: "live", PID: &workerPID, Foreground: &hcclient.Foreground{PID: int32(workerPID), Name: "worker", Argv: []string{"worker"}, Cwd: cwd}})
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", we.Task)
	task.Set("status", "running")
	task.Set("machine", host.ID)
	task.Set("worktree", cwd)
	task.Set("parent", nil)
	task.Set("owner", session.Owner())
	task.Set("worker_endpoint", we.Object())
	task.Set("execution", reservations.NewExecution(worker))
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	record := emptyEnvironment(we.Task)
	discovery := ordjson.NewObject()
	cmd := ordjson.NewObject()
	cmd.Set("name", "dev")
	cmd.Set("source", "package.json")
	cmd.Set("kind", "script")
	cmd.Set("command", "node server.js")
	discovery.Set("commands", []any{cmd})
	record.Set("discovery", discovery)
	event := ordjson.NewObject()
	event.Set("event", "discover")
	if err = Write(s, record, event); err != nil {
		t.Fatal(err)
	}
	return &serviceLab{s: s, hc: hc, task: task, caller: caller, worktree: cwd}
}
func (l *serviceLab) start() (*ordjson.Object, error) {
	return Start(l.s, StartArgs{Task: stringField(l.task, "id"), Command: "dev"}, l.caller)
}
func TestNativeServiceLaunchKeepsIntentAndIndependentReadiness(t *testing.T) {
	l := newServiceLab(t)
	process := exec.Command("sleep", "60")
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	pid := uint32(process.Process.Pid)
	l.hc.QueueFault("terminal.run", hctest.Fault{RunPID: &pid})
	out, err := l.start()
	if err != nil {
		t.Fatal(err)
	}
	service := objectField(out, "service")
	endpoint := serviceEndpoint(service)
	if endpoint.Validate() != nil || endpoint.Role != "service" || endpoint.Attempt != stringField(service, "id") {
		t.Fatalf("endpoint=%+v", endpoint)
	}
	readiness := objectField(service, "readiness")
	if ready, _ := readiness.Get("ready"); ready != nil {
		t.Fatal("missing OSC or readiness rule invented ready state")
	}
	if objectField(service, "first_message") != nil {
		t.Fatal("plain program synthesized first message")
	}
	for _, call := range l.hc.Calls() {
		if call.Method != "terminal.run" {
			continue
		}
		if call.Params["outlive_parent"] != true || call.Params["keep"] != false || call.Params["rm"] != false || call.Params["no_notify"] != true {
			t.Fatalf("lifetime=%v", call.Params)
		}
		instance, err := l.s.Instance()
		if err != nil {
			t.Fatal(err)
		}
		labels := call.Params["labels"].(map[string]any)
		if labels["sum.installation"] != instance {
			t.Fatal("service installation label is not stable instance", labels)
		}
		argv := call.Params["argv"].([]any)
		if len(argv) != 3 || argv[0] != "npm" || argv[1] != "run" || argv[2] != "dev" {
			t.Fatalf("structured argv=%v", argv)
		}
		if call.Params["cwd"] != l.worktree || call.Params["parent"] != nil || call.Params["caller"] != nil {
			t.Fatal(call.Params)
		}
	}
	record, err := Read(l.s, stringField(l.task, "id"))
	if err != nil || len(listField(record, "services")) != 1 {
		t.Fatalf("launch not durable: %v", err)
	}
}
func TestNativeServiceLostLaunchNeverRetries(t *testing.T) {
	l := newServiceLab(t)
	l.hc.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	if _, err := l.start(); err == nil {
		t.Fatal("lost launch response accepted")
	}
	if _, err := l.start(); err == nil {
		t.Fatal("unresolved intent retried")
	}
	runs := 0
	for _, call := range l.hc.Calls() {
		if call.Method == "terminal.run" {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("runs=%d", runs)
	}
	record, _ := Read(l.s, stringField(l.task, "id"))
	if len(listField(record, "services")) != 1 {
		t.Fatal("lost intent")
	}
}
func (l *serviceLab) savedService(t *testing.T, live bool) (*ordjson.Object, hcclient.Terminal) {
	t.Helper()
	pid := uint32(40000001)
	fg := &hcclient.Foreground{PID: int32(pid), Name: "server", Argv: []string{"server"}, Cwd: l.worktree}
	finished := uint64(9)
	code := int32(0)
	terminal := hcclient.Terminal{Address: "t3", CreatedMS: 3, Cwd: l.worktree, State: "finished", PID: &pid, Foreground: fg, Argv: []string{"server"}, FinishedMS: &finished, ExitCode: &code}
	if live {
		terminal.State = "live"
		terminal.FinishedMS = nil
		terminal.ExitCode = nil
	}
	l.hc.SetTerminal(terminal)
	endpoint := store.EndpointFromContext(objectField(l.task, "worker_endpoint"))
	endpoint.Role = "service"
	endpoint.Task = stringField(l.task, "id")
	endpoint.Attempt = "s-native"
	endpoint.Address = "t3"
	endpoint.CreatedMS = 3
	service := ordjson.NewObject()
	service.Set("id", "s-native")
	service.Set("name", "server")
	service.Set("state", "running")
	service.Set("endpoint", endpoint.Object())
	service.Set("process", nativeProcess(terminal))
	objectField(service, "process").Set("process_group", jsonInt(int(pid)))
	record, _ := Read(l.s, stringField(l.task, "id"))
	record.Set("services", []any{service})
	event := ordjson.NewObject()
	event.Set("event", "test-service")
	if err := Write(l.s, record, event); err != nil {
		t.Fatal(err)
	}
	return service, terminal
}
func TestNativeServiceOwnershipRefusesReplacementsAndLiveExitClaims(t *testing.T) {
	for _, change := range []string{"core", "creation", "pid", "foreground", "argv", "kept-shell", "cwd", "finished-live-pid"} {
		t.Run(change, func(t *testing.T) {
			l := newServiceLab(t)
			service, terminal := l.savedService(t, true)
			switch change {
			case "core":
				endpoint := serviceEndpoint(service)
				endpoint.CoreID = "foreign"
				service.Set("endpoint", endpoint.Object())
			case "creation":
				terminal.CreatedMS++
			case "pid":
				pid := uint32(40000002)
				terminal.PID = &pid
			case "foreground":
				terminal.Foreground.PID++
			case "argv":
				terminal.Argv = []string{"other"}
			case "kept-shell":
				terminal.Keep = true
				terminal.Foreground = &hcclient.Foreground{PID: 999, Argv: []string{"sh"}}
			case "cwd":
				terminal.Cwd = t.TempDir()
			case "finished-live-pid":
				pid := uint32(os.Getpid())
				terminal.PID = &pid
				process := objectField(service, "process")
				process.Set("pid", jsonInt(int(pid)))
				terminal.State = "finished"
				finished := uint64(9)
				code := int32(0)
				terminal.FinishedMS = &finished
				terminal.ExitCode = &code
			}
			l.hc.SetTerminal(terminal)
			view, err := observeService(l.task, service, "")
			if err != nil {
				t.Fatal(err)
			}
			if stringField(view, "ownership") == "owned" {
				t.Fatalf("adopted %s: %v", change, view)
			}
		})
	}
}
func TestNativeServiceStopSendsOneInterruptWithoutEscalationOrRemoval(t *testing.T) {
	l := newServiceLab(t)
	service, _ := l.savedService(t, true)
	for i := 0; i < 2; i++ {
		row, err := stopService(l.s, l.task, service, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if stringField(row, "state") != "stopping" {
			t.Fatal(row)
		}
		record, _ := Read(l.s, stringField(l.task, "id"))
		service, _ = serviceByID(record, "s-native")
	}
	interrupts := 0
	for _, call := range l.hc.Calls() {
		switch call.Method {
		case "terminal.interrupt":
			interrupts++
		case "terminal.kill", "terminal.rm":
			t.Fatalf("normal stop invoked %s", call.Method)
		}
	}
	if interrupts != 1 {
		t.Fatalf("interrupts=%d", interrupts)
	}
}
func TestNativeServiceFinishedExitRetainsHistoryUntilCleanup(t *testing.T) {
	l := newServiceLab(t)
	service, _ := l.savedService(t, false)
	row, err := stopService(l.s, l.task, service, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if stringField(row, "state") != "stopped" {
		t.Fatal(row)
	}
	for _, call := range l.hc.Calls() {
		if call.Method == "terminal.rm" || call.Method == "terminal.kill" || call.Method == "terminal.interrupt" {
			t.Fatalf("finished service effect: %s", call.Method)
		}
	}
}
func TestNativeServiceSurvivingDescendantPreventsExitProof(t *testing.T) {
	l := newServiceLab(t)
	service, _ := l.savedService(t, false)
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	objectField(service, "process").Set("descendants", []any{jsonInt(child.Process.Pid)})
	view, err := observeService(l.task, service, "")
	if err != nil {
		t.Fatal(err)
	}
	if stringField(view, "ownership") == "owned" {
		t.Fatal("surviving child accepted")
	}
}
func TestNativeWaitIdleIsNotServiceExit(t *testing.T) {
	l := newServiceLab(t)
	service, terminal := l.savedService(t, true)
	terminal.Status = &hcclient.Status{State: "idle"}
	l.hc.SetTerminal(terminal)
	endpoint := serviceEndpoint(service)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := l.hc.Client()
	wait, err := client.Wait(ctx, hcclient.WaitParams{Terminal: endpoint.Address})
	if err != nil {
		t.Fatal(err)
	}
	if !wait.Ended {
		t.Fatal("fixture did not exercise native wait ended")
	}
	view, err := observeService(l.task, service, "")
	if err != nil {
		t.Fatal(err)
	}
	if running, _ := view.Get("running"); running != true {
		t.Fatal("idle wait became exit")
	}
}

func TestConcurrentNativeServiceStopClaimsOneInterruptAndReleasesLedger(t *testing.T) {
	l := newServiceLab(t)
	service, _ := l.savedService(t, true)
	entered, release := l.hc.HoldNext("terminal.interrupt", "t3")
	defer release()
	first := make(chan error, 1)
	go func() { _, err := stopService(l.s, l.task, service, 1, ""); first <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("interrupt boundary not reached")
	}
	other, err := store.Open(l.s.Root)
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() { _, err := stopService(other, l.task, service, 1, ""); second <- err }()
	select {
	case err = <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent observation blocked behind interrupt or ledger lock")
	}
	release()
	select {
	case err = <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("first stop did not finish")
	}
	count := 0
	for _, call := range l.hc.Calls() {
		if call.Method == "terminal.interrupt" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent interrupts=%d", count)
	}
}
func TestSharedNativeServiceHasNoTaskStopAuthority(t *testing.T) {
	l := newServiceLab(t)
	service, _ := l.savedService(t, true)
	service.Set("ownership", "shared")
	row, err := stopService(l.s, l.task, service, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if stringField(row, "action") != "refused" {
		t.Fatal(row)
	}
	for _, call := range l.hc.Calls() {
		if call.Method == "terminal.interrupt" || call.Method == "terminal.kill" || call.Method == "terminal.rm" {
			t.Fatal("shared process affected")
		}
	}
}

func TestNativeServiceDetachedGroupSurvivorBlocksExitOutsideCheckout(t *testing.T) {
	command := exec.Command("sh", "-c", "sleep 120 </dev/null >/dev/null 2>&1 & echo $!")
	command.Dir = t.TempDir()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	group := command.Process.Pid
	data, err := io.ReadAll(pipe)
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-group, syscall.SIGKILL)
	process := ordjson.NewObject()
	process.Set("pid", jsonInt(group))
	process.Set("foreground_pid", jsonInt(group))
	process.Set("process_group", jsonInt(group))
	process.Set("descendants", []any{})
	if err = processExited(process); err == nil {
		t.Fatalf("detached writer %d surviving in native process group %d was accepted as exit", child, group)
	}
}

func TestLogicalServiceAuthorityRejectsTransferAndForeignOwnerBeforeHC(t *testing.T) {
	for _, scenario := range []string{"transfer", "foreign-owner"} {
		t.Run(scenario, func(t *testing.T) {
			l := newServiceLab(t)
			if scenario == "transfer" {
				other, err := store.Open(l.s.Root)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = other.ClaimCoordinator("", "new conversation", true); err != nil {
					t.Fatal(err)
				}
			} else {
				owner := objectField(l.task, "owner")
				owner.Set("session_id", "different-session")
				if err := l.s.SaveTask(l.task); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := l.start(); err == nil {
				t.Fatal("unauthorized service launch accepted")
			}
			if _, err := Stop(l.s, StopArgs{Task: stringField(l.task, "id"), Caller: l.caller}); err == nil {
				t.Fatal("unauthorized service stop accepted")
			}
			if calls := l.hc.Calls(); len(calls) != 0 {
				t.Fatalf("unauthorized operation contacted HC: %v", calls)
			}
			record, err := Read(l.s, stringField(l.task, "id"))
			if err != nil || len(listField(record, "services")) != 0 {
				t.Fatalf("unauthorized intent saved: %v", err)
			}
		})
	}
}

func TestLogicalServiceStopUsesRecordedEndpoint(t *testing.T) {
	l := newServiceLab(t)
	l.savedService(t, false)
	out, err := Stop(l.s, StopArgs{Task: stringField(l.task, "id"), Caller: l.caller})
	if err != nil {
		t.Fatal(err)
	}
	services := listField(out, "services")
	if len(services) != 1 {
		t.Fatalf("services=%v", services)
	}
	row, _ := services[0].(*ordjson.Object)
	if stringField(row, "state") != "stopped" {
		t.Fatalf("stop=%v", row)
	}
	for _, call := range l.hc.Calls() {
		if call.Method == "terminal.run" || call.Method == "terminal.interrupt" {
			t.Fatalf("finished service replayed effect: %v", call)
		}
	}
}

func TestServiceNativeWorkerStillRequiresReceiptAndCurrentAttempt(t *testing.T) {
	l := newServiceLab(t)
	endpoint := store.EndpointFromContext(objectField(l.task, "worker_endpoint"))
	l.caller = endpoint.Object()
	if _, err := l.start(); err == nil {
		t.Fatal("unregistered worker accepted")
	}
	if len(l.hc.Calls()) != 0 {
		t.Fatal("unregistered caller reached native launch")
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := client.Get(context.Background(), endpoint.Address)
	if err != nil {
		t.Fatal(err)
	}
	observed := (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: *terminal.PID, Foreground: terminal.Foreground}).Record(store.Now())
	registration, err := l.s.Register(endpoint, "worker", endpoint.Task, observed)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "worker-init", Attempt: endpoint.Attempt})
	registration.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(l.s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(l.task)
	if err != nil {
		t.Fatal(err)
	}
	worker.Set("incarnation", observed)
	worker.Set("native_receipt", receipt)
	if err = l.s.SaveTask(l.task); err != nil {
		t.Fatal(err)
	}
	if _, err = serviceCaller(l.s, l.task, l.caller); err != nil {
		t.Fatal(err)
	}
	worker.Set("id", "x-other-attempt")
	if _, err = serviceCaller(l.s, l.task, l.caller); err == nil {
		t.Fatal("stale worker attempt accepted")
	}
}
