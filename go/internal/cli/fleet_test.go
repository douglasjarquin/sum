package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/release"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/updatecmd"
)

var fleetRoles = []string{
	"busy-tool-call", "dirty-checkout", "open-question", "answered-unapplied", "pending-report", "old-runtime-client",
	"closed-parent", "unknown-worker", "failed-refresh", "ignores-refresh", "cooperative-a", "cooperative-b",
}

type fleetLab struct {
	*demoLab
	source string
}

type checkoutSnap struct {
	head, branch, porcelain string
}

func TestFleetTwelveWorkers(t *testing.T) {
	f := newFleetLab(t)
	settings := f.ctl(true, "settings", "set", "--global", "12", "--per-repository", "1")
	if settings["previous"] != nil {
		t.Fatalf("previous capacity=%v", settings["previous"])
	}
	tasks := map[string]map[string]any{}
	byID := map[string]string{}
	brief := f.brief()
	for _, role := range fleetRoles {
		task := f.ctl(true, "dispatch", "--repo", f.project(role), "--brief", brief, "--harness", "codex", "--approved")
		address := workerAddress(task)
		if address == "" {
			t.Fatalf("%s has no native endpoint", role)
		}
		f.ctlPane(address, true, "init")
		task = f.readTask(asString(task["id"]))
		if asString(task["status"]) != "running" {
			t.Fatalf("%s not receipt-adopted", role)
		}
		limits := asMap(asMap(task["admission"])["limits"])
		if asInt(limits["global"]) != 12 || asInt(limits["per_repository"]) != 1 {
			t.Fatalf("%s admission=%v", role, task["admission"])
		}
		tasks[role] = task
		byID[asString(task["id"])] = role
	}
	if asInt(asMap(f.ctl(true, "settings", "show")["occupied"])["global"]) != 12 {
		t.Fatal("twelve active attempts not counted")
	}
	refused := f.ctl(false, "prepare", "--repo", f.project("thirteenth"), "--brief", brief, "--harness", "codex", "--approved")
	if !strings.Contains(asString(refused["error"]), "12 of 12 global execution slots") {
		t.Fatalf("thirteenth=%v", refused)
	}
	for _, task := range tasks {
		f.native.SetStatus(workerAddress(task), "idle")
	}
	f.native.SetStatus("t1", "working")
	f.native.SetStatus(workerAddress(tasks["busy-tool-call"]), "working")
	wip := filepath.Join(asString(tasks["dirty-checkout"]["worktree"]), "wip.txt")
	if err := os.WriteFile(wip, []byte("uncommitted work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	openQ := asMap(f.ctlPane(workerAddress(tasks["open-question"]), true, "ask", asString(tasks["open-question"]["id"]), "--key", "open", "--text", "Open question?")["question"])
	answered := asMap(f.ctlPane(workerAddress(tasks["answered-unapplied"]), true, "ask", asString(tasks["answered-unapplied"]["id"]), "--key", "answered", "--text", "Answered question?")["question"])
	f.ctl(true, "answer", asString(tasks["answered-unapplied"]["id"]), asString(answered["id"]), "--text", "Yes.")
	f.ctlPane(workerAddress(tasks["pending-report"]), true, "report", asString(tasks["pending-report"]["id"]), "--text", "Candidate ready; not verified.")
	closedID := asString(tasks["closed-parent"]["id"])
	closed := f.readTask(closedID)
	asMap(closed["parent"])["address"] = "tzz"
	f.writeTask(closedID, closed)
	orphan := f.ctlPane(workerAddress(tasks["closed-parent"]), true, "ask", closedID, "--key", "orphan", "--text", "Parent gone?")
	if asString(asMap(orphan["notice"])["status"]) == "submitted" {
		t.Fatal("absent parent reported submitted")
	}
	unknown := workerAddress(tasks["unknown-worker"])
	stopNativeRecoveryTerminal(t, f.demoLab, unknown)
	client := f.native.Client()
	if _, err := client.Remove(context.Background(), unknown); err != nil {
		t.Fatal(err)
	}
	var qualifiedFailedRefresh hcclient.Terminal
	for _, terminal := range f.native.Terminals() {
		if terminal.Address == workerAddress(tasks["failed-refresh"]) {
			qualifiedFailedRefresh = terminal
			bad := "return"
			terminal.Submit = &bad
			f.native.SetTerminal(terminal)
		}
	}
	records := map[string][]string{}
	for role, task := range tasks {
		records[role] = questionKeys(f.readTask(asString(task["id"])))
	}
	checkouts := f.checkouts(tasks)
	callsBefore := len(f.native.Calls())
	live := f.ctl(true, "status", "--live")
	if live["live"] != true || len(asSlice(live["tasks"])) != 12 {
		t.Fatalf("live status=%v", live)
	}
	assertFleetReadOnly(t, f.native.Calls()[callsBefore:])
	skill, err := os.ReadFile(filepath.Join(f.root, "skills/sum-work/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	sha1 := f.commitUpstream("skills/sum-work/SKILL.md", string(skill)+"\nUpdate one: reread decisions before continuing.\n")
	f.stageFixture(sha1)
	selectedBefore := f.currentPath()
	qualificationCalls := len(f.native.Calls())
	refusedQualification := f.ctl(false, "update", "apply", "--ref", sha1, "--no-fetch")
	if !strings.Contains(asString(refusedQualification["error"]), "automatic delivery qualification") || f.currentPath() != selectedBefore {
		t.Fatalf("unqualified current worker changed runtime selection: %v", refusedQualification)
	}
	assertFleetReadOnly(t, f.native.Calls()[qualificationCalls:])
	// Restore exact qualified input before activation; refresh failure is introduced only at its own delivery boundary.
	f.native.SetTerminal(qualifiedFailedRefresh)
	applied := f.ctl(true, "update", "apply", "--ref", sha1, "--no-fetch")
	if applied["changed"] != true || asString(asMap(applied["default"])["sha"]) != sha1 {
		t.Fatalf("apply one=%v", applied)
	}
	unqualifiedRefresh := qualifiedFailedRefresh
	wrongSubmit := "return"
	unqualifiedRefresh.Submit = &wrongSubmit
	f.native.SetTerminal(unqualifiedRefresh)
	start := len(f.native.Calls())
	first := f.ctl(true, "refresh", "request")
	rows := f.targetRows(first, byID)
	if asString(asMap(first["runtime"])["sha"]) != sha1 {
		t.Fatalf("refresh runtime=%v", first["runtime"])
	}
	for _, role := range fleetRoles {
		state := asString(rows[role]["state"])
		if role == "busy-tool-call" {
			if state != "pending-busy" {
				t.Fatalf("busy state=%v", rows[role])
			}
		} else if role == "unknown-worker" {
			if state != "unreachable" {
				t.Fatalf("gone state=%v", rows[role])
			}
		} else if role == "failed-refresh" {
			if state != "pending-unreachable" {
				t.Fatalf("qualification refusal=%v", rows[role])
			}
		} else if state != "submitted-unconfirmed" {
			t.Fatalf("%s refresh=%v", role, rows[role])
		}
		if asString(rows[role]["revision"]) != "r2" {
			t.Fatalf("%s revision=%v", role, rows[role])
		}
	}
	if asString(rows["coordinator"]["state"]) != "pending-busy" {
		t.Fatalf("coordinator refresh=%v", rows["coordinator"])
	}
	sends := 0
	for _, call := range f.native.Calls()[start:] {
		if call.Method == "terminal.run" || call.Method == "terminal.read" || call.Method == "terminal.kill" || call.Method == "terminal.interrupt" {
			t.Fatalf("refresh lifecycle effect=%v", call)
		}
		if call.Method == "terminal.send" {
			sends++
			text := asString(call.Params["text"])
			for _, private := range []string{"Open question?", "Answered question?", "Yes.", "Candidate ready", "Parent gone?"} {
				if strings.Contains(text, private) {
					t.Fatalf("refresh leaked task prose %q", private)
				}
			}
		}
	}
	if sends != 9 {
		t.Fatalf("refresh sends=%d, want nine eligible workers", sends)
	}
	// The refused refresh records no receipt; later activations again require a currently qualified recipient.
	f.native.SetTerminal(qualifiedFailedRefresh)
	oldRuntime := f.readVersions(asString(tasks["old-runtime-client"]["id"]))
	if asString(oldRuntime["active"]) != "r1" || asString(oldRuntime["requested"]) != "r2" {
		t.Fatalf("native submission adopted old client=%v", oldRuntime)
	}
	f.ctl(true, "answer", asString(tasks["open-question"]["id"]), asString(openQ["id"]), "--text", "Answered after update one.")
	f.ctlPane(workerAddress(tasks["answered-unapplied"]), true, "resolve", asString(tasks["answered-unapplied"]["id"]), asString(answered["id"]))
	f.ctlPane(workerAddress(tasks["cooperative-a"]), true, "report", asString(tasks["cooperative-a"]["id"]), "--text", "Reported during update.")
	more := asMap(f.ctlPane(workerAddress(tasks["busy-tool-call"]), true, "ask", asString(tasks["busy-tool-call"]["id"]), "--key", "busy", "--text", "Asked while busy?")["question"])
	for _, role := range []string{"dirty-checkout", "cooperative-a", "cooperative-b"} {
		f.ctlPane(workerAddress(tasks[role]), true, "brief", "adopt", asString(tasks[role]["id"]), "r2")
	}
	if asInt(asMap(f.ctl(true, "refresh", "status")["counts"])["confirmed"]) != 3 {
		t.Fatal("matching adoption receipts did not confirm exactly three tasks")
	}
	agents, err := os.ReadFile(filepath.Join(f.root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	f.commitUpstream("AGENTS.md", string(agents)+"\nUpdate two.\n")
	sha2 := f.commitUpstream("skills/sum-work/SKILL.md", string(skill)+"\nUpdate two: reread decisions before continuing.\n")
	f.stageFixture(sha2)
	f.ctl(true, "update", "apply", "--ref", sha2, "--no-fetch")
	// Stop the real helper at an observed native send boundary, after durable
	// refresh intent is recorded. Retrying must preserve ambiguity without replay.
	target := tasks["cooperative-b"]
	initialInputs := len(f.native.Inputs(workerAddress(target)))
	entered, release := f.native.HoldNext("terminal.send", workerAddress(target))
	command := exec.Command(f.helper, "--format", "json", "--home", f.home, "refresh", "request")
	command.Env = f.env
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		release()
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("refresh never reached native boundary: %s", output.String())
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	release()
	deadline := time.NewTimer(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer tick.Stop()
	for len(f.native.Inputs(workerAddress(target))) == initialInputs {
		select {
		case <-deadline.C:
			t.Fatal("held native send did not finish its observable effect")
		case <-tick.C:
		}
	}

	for role, task := range tasks {
		want := append([]string{}, records[role]...)
		if role == "busy-tool-call" {
			want = append(want, "busy")
		}
		if strings.Join(questionKeys(f.readTask(asString(task["id"]))), ",") != strings.Join(want, ",") {
			t.Fatalf("%s lost questions during interruption", role)
		}
	}
	sentBefore := len(f.native.Inputs(workerAddress(target)))
	recovered := f.ctl(true, "refresh", "request")
	rows = f.targetRows(recovered, byID)
	if len(f.native.Inputs(workerAddress(target))) != sentBefore {
		t.Fatal("interrupted native refresh was blindly replayed")
	}
	for _, role := range fleetRoles {
		if asString(rows[role]["revision"]) != "r3" {
			t.Fatalf("%s did not recover r3=%v", role, rows[role])
		}
	}
	stale := f.ctlPane(workerAddress(target), false, "brief", "adopt", asString(target["id"]), "r2")
	if !strings.Contains(asString(stale["error"]), "not the requested revision") {
		t.Fatalf("stale adoption=%v", stale)
	}
	for _, role := range []string{"dirty-checkout", "cooperative-a", "cooperative-b"} {
		f.ctlPane(workerAddress(tasks[role]), true, "brief", "adopt", asString(tasks[role]["id"]), "r3")
	}
	rolled := f.ctl(true, "update", "rollback")
	if asString(asMap(rolled["default"])["sha"]) != sha1 {
		t.Fatalf("rollback=%v", rolled)
	}
	third := f.ctl(true, "refresh", "request")
	rows = f.targetRows(third, byID)
	for _, role := range fleetRoles {
		if asString(rows[role]["revision"]) != "r4" {
			t.Fatalf("%s rollback brief=%v", role, rows[role])
		}
	}
	versions := f.readVersions(asString(tasks["cooperative-a"]["id"]))
	pins := map[string]string{}
	for _, raw := range asSlice(versions["revisions"]) {
		revision := asMap(raw)
		for _, raw := range asSlice(asMap(revision["policy"])["procedure"]) {
			procedure := asMap(raw)
			if asString(procedure["name"]) == "sum-work" {
				pins[asString(revision["id"])] = asString(procedure["path"])
			}
		}
	}
	if pins["r2"] == "" || pins["r4"] != pins["r2"] || pins["r3"] == pins["r2"] {
		t.Fatalf("immutable procedure pins=%v", pins)
	}
	rolledBrief, err := os.ReadFile(asString(rows["cooperative-a"]["path"]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rolledBrief), "reread decisions before continuing") {
		t.Fatal("brief copied mutable procedure body instead of pinning it")
	}
	procedureText, err := os.ReadFile(pinnedProcedure(t, string(rolledBrief)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(procedureText), "Update one") || strings.Contains(string(procedureText), "Update two") {
		t.Fatal("rollback did not restore the immutable update-one procedure")
	}

	for _, raw := range asSlice(f.ctl(true, "brief", "list", asString(tasks["cooperative-a"]["id"]))["revisions"]) {
		if asMap(raw)["ok"] != true {
			t.Fatalf("historical brief unusable=%v", raw)
		}
	}
	f.ctl(true, "refresh", "adopt", "--coordinator", "r3")
	for _, role := range []string{"dirty-checkout", "cooperative-a", "cooperative-b"} {
		f.ctlPane(workerAddress(tasks[role]), true, "brief", "adopt", asString(tasks[role]["id"]), "r4")
	}
	f.verifyActivationRecovery(tasks, sha1, sha2)
	f.ctl(true, "answer", asString(tasks["busy-tool-call"]["id"]), asString(more["id"]), "--text", "Answered after recovered activation.")
	f.ctlPane(workerAddress(tasks["cooperative-b"]), true, "report", asString(tasks["cooperative-b"]["id"]), "--text", "Reported after recovered activation.")
	pending := tasks["pending-report"]
	stopNativeRecoveryTerminal(t, f.demoLab, workerAddress(pending))
	worker := asMap(asMap(f.readTask(asString(pending["id"]))["execution"])["worker"])
	f.ctl(true, "execution", "park", asString(pending["id"]), "--attempt", asString(worker["id"]))
	f.ctl(true, "archive", asString(pending["id"]), "--acknowledge")
	late := f.ctl(true, "prepare", "--repo", f.project("late"), "--brief", brief, "--harness", "codex", "--approved")
	if asInt(asMap(asMap(late["admission"])["occupied_before"])["global"]) != 11 {
		t.Fatalf("released reservation not reusable=%v", late["admission"])
	}
	lateText, err := os.ReadFile(asString(late["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lateText), filepath.Join(f.root, "bin", "sumctl")) || filepath.Base(f.currentPath()) != sha1 {
		t.Fatal("late work did not retain the verified default helper")
	}

	if !checkoutEqual(f.checkouts(tasks), checkouts) {
		t.Fatal("rolling updates altered task checkouts")
	}
	if body, err := os.ReadFile(wip); err != nil || string(body) != "uncommitted work\n" {
		t.Fatal("dirty work was lost", err)
	}
	for _, role := range []string{"pending-report", "cooperative-a"} {
		if asString(asMap(f.readTask(asString(tasks[role]["id"]))["report"])["brief_revision"]) != "r1" {
			t.Fatal("historical report was rewritten to adopted revision")
		}
	}

	for role, want := range map[string]string{"open-question": "answered", "answered-unapplied": "applied", "closed-parent": "open", "busy-tool-call": "answered"} {
		questions := asSlice(f.readTask(asString(tasks[role]["id"]))["questions"])
		if len(questions) != 1 || asString(asMap(questions[0])["status"]) != want {
			t.Fatalf("%s final question state=%v", role, questions)
		}
	}
	if asString(f.readTask(asString(pending["id"]))["status"]) != "archived" {
		t.Fatal("released reported task was not archived")
	}
	if asString(asMap(f.readTask(asString(tasks["cooperative-b"]["id"]))["report"])["brief_revision"]) != "r4" {
		t.Fatal("new report lost current revision")
	}
	runCount := 0
	for _, call := range f.native.Calls() {
		if call.Method == "terminal.run" {
			runCount++
		}
		if call.Method == "terminal.kill" || call.Method == "terminal.interrupt" || call.Method == "terminal.read" {
			t.Fatalf("update stopped unrelated execution=%v", call)
		}
	}
	if runCount != 12 {
		t.Fatalf("updates relaunched workers=%d native launches", runCount)
	}
	f.native.AssertNoForce(t)
}

func assertFleetReadOnly(t *testing.T, calls []hctest.Call) {
	t.Helper()
	for _, call := range calls {
		if call.Method != "hello" && call.Method != "terminal.get" && call.Method != "terminal.list" && call.Method != "templates.list" {
			t.Fatalf("observational status caused native effect=%v", call)
		}
	}
}

func newFleetLab(t *testing.T) *fleetLab {
	t.Helper()
	source, _ := repoReference(t)
	base := t.TempDir()
	install := filepath.Join(base, "sum install dir")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}

	install, err := filepath.EvalSymlinks(install)
	if err != nil {
		t.Fatal(err)
	}
	names, err := exec.Command("git", "-C", source, "ls-files", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Split(strings.TrimSpace(string(names)), "\n") {
		from := filepath.Join(source, name)
		info, err := os.Lstat(from)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			continue
		}
		to := filepath.Join(install, name)
		if err = os.MkdirAll(filepath.Dir(to), 0755); err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(from)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(link, to); err != nil {
				t.Fatal(err)
			}
		} else {
			body, err := os.ReadFile(from)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(to, body, info.Mode()); err != nil {
				t.Fatal(err)
			}
		}
	}

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", install}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "sum test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("config", "gc.auto", "0")
	git("add", ".")
	git("commit", "-q", "-m", "installation")
	origin := filepath.Join(base, "sum install dir origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init origin: %v\n%s", err, out)
	}
	git("remote", "add", "origin", origin)
	git("push", "-q", "-u", "origin", "main")
	git("remote", "set-head", "origin", "main")

	srcBin := filepath.Join(source, ".local", "bin")
	dstBin := filepath.Join(install, ".local", "bin")
	if err := os.MkdirAll(dstBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sumctl"} {
		src := filepath.Join(srcBin, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("plant %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dstBin, name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	goBin := pinnedGo(t)
	d := newNativeDemoLabAt(t, install, filepath.Join(install, "bin", "sumctl"), base, filepath.Join(install, ".sum"))
	d.setEnv("SUM_GO_BIN", goBin)
	d.setEnv("SUM_SESSION", "sum-test")
	d.setEnv("FAKE_SESSION", "sum-test")
	if dir := filepath.Dir(goBin); dir != "." && dir != "" {
		d.setEnv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for _, e := range d.env {
		key, value, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "SUM_") || strings.HasPrefix(key, "HC_") || strings.HasPrefix(key, "HERDR_") || strings.HasPrefix(key, "FAKE_") || key == "PATH" {
			t.Setenv(key, value)
		}
	}

	hctest.InstallRuntime(t, install)
	f := &fleetLab{demoLab: d, source: source}
	if asString(f.ctl(true, "init")["role"]) != "coordinator" {
		t.Fatal("fleet init")
	}
	return f
}

func pinnedGo(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("SUM_GO_BIN"); v != "" {
		return v
	}
	out, err := exec.Command("mise", "where", "go").Output()
	if err == nil {
		p := filepath.Join(strings.TrimSpace(string(out)), "bin", "go")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	found, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go not found: %v", err)
	}
	return found
}

func (f *fleetLab) git(cwd string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fleetLab) project(name string) string {
	f.t.Helper()
	repo := filepath.Join(f.base, "projects", name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.git(repo, "init", "-b", "main")
	f.git(repo, "config", "user.name", "sum test")
	f.git(repo, "config", "user.email", "test@example.invalid")
	f.git(repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(name+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(repo, "add", ".")
	f.git(repo, "commit", "-q", "-m", "fixture")
	origin := filepath.Join(f.base, "remotes", name+".git")
	f.git(f.base, "init", "-q", "--bare", "-b", "main", origin)
	f.git(repo, "remote", "add", "origin", origin)
	f.git(repo, "push", "-q", "-u", "origin", "main")
	f.git(repo, "remote", "set-head", "origin", "main")
	return repo
}

func (f *fleetLab) brief() string {
	f.t.Helper()
	path := filepath.Join(f.base, "brief.md")
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, []byte("Do the approved thing.\n"), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	return path
}

func (f *fleetLab) commitUpstream(rel, text string) string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, rel), []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(f.root, "add", rel)
	f.git(f.root, "commit", "-q", "-m", rel)
	f.git(f.root, "push", "-q", "origin", "main")
	return f.git(f.root, "rev-parse", "HEAD")
}

func (f *fleetLab) checkouts(tasks map[string]map[string]any) map[string]checkoutSnap {
	f.t.Helper()
	out := map[string]checkoutSnap{}
	for _, task := range tasks {
		wt := asString(task["worktree"])
		out[asString(task["id"])] = checkoutSnap{
			head:      f.git(wt, "rev-parse", "HEAD"),
			branch:    f.git(wt, "branch", "--show-current"),
			porcelain: f.git(wt, "status", "--porcelain", "--untracked-files=all"),
		}
	}
	return out
}

func (f *fleetLab) readTask(id string) map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, "tasks", id, "task.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(data, &task); err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f *fleetLab) writeTask(id string, task map[string]any) {
	f.t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "tasks", id, "task.json"), data, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fleetLab) readVersions(id string) map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, "tasks", id, "versions.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var vers map[string]any
	if err := json.Unmarshal(data, &vers); err != nil {
		f.t.Fatal(err)
	}
	return vers
}

func (f *fleetLab) targetRows(view map[string]any, byID map[string]string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, item := range asSlice(view["targets"]) {
		row := asMap(item)
		key := byID[asString(row["task"])]
		if key == "" {
			key = asString(row["target"])
		}
		out[key] = row
	}
	return out
}

func (f *fleetLab) currentPath() string {
	f.t.Helper()
	return currentRuntimePath(f.root)
}

func currentRuntimePath(root string) string {
	link := filepath.Join(root, ".local", "current")
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		return resolved
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return target
	}
	return abs
}

func questionKeys(task map[string]any) []string {
	var keys []string
	for _, item := range asSlice(task["questions"]) {
		keys = append(keys, asString(asMap(item)["key"]))
	}
	return keys
}

func checkoutEqual(a, b map[string]checkoutSnap) bool {
	if len(a) != len(b) {
		return false
	}
	for id, got := range a {
		want, ok := b[id]
		if !ok || got != want {
			return false
		}
	}
	return true
}

func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil {
		ra = a
	}
	if errB != nil {
		rb = b
	}
	return ra == rb
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// pinnedProcedure is the required worker procedure file a brief names.
func pinnedProcedure(t *testing.T, brief string) string {
	t.Helper()
	match := regexp.MustCompile("- Required before any other step: `([^`]+)`").FindStringSubmatch(brief)
	if match == nil {
		t.Fatalf("brief names no required procedure file:\n%s", brief)
	}
	return match[1]
}

// stageFixture builds a complete immutable unit artifact from an approved lab
// commit. Its synthetic HC contract is explicit fixture evidence; this never
// upgrades an offline unqualified bundle or claims published-artifact proof.
func (f *fleetLab) stageFixture(sha string) {
	f.t.Helper()
	dir := filepath.Join(f.root, ".local", "releases", sha)
	if err := os.MkdirAll(dir, 0755); err != nil {
		f.t.Fatal(err)
	}
	archive, err := exec.Command("git", "-C", f.root, "archive", "--format=tar", sha).Output()
	if err != nil {
		f.t.Fatal(err)
	}
	unpack := exec.Command("tar", "-xf", "-", "-C", dir)
	unpack.Stdin = bytes.NewReader(archive)
	if out, err := unpack.CombinedOutput(); err != nil {
		f.t.Fatal(err, string(out))
	}
	helper, err := os.ReadFile(filepath.Join(f.source, ".local/bin/sumctl"))
	if err != nil {
		f.t.Fatal(err)
	}
	writeReleaseFile(f.t, dir, ".local/bin/sumctl", helper, 0755)
	hctest.InstallRuntime(f.t, dir)
	files := map[string]any{}
	for _, name := range strings.Split(f.git(f.root, "ls-tree", "-r", "--name-only", sha), "\n") {
		hash, err := release.ContentID(filepath.Join(dir, name))
		if err != nil {
			f.t.Fatal(err)
		}
		files[name] = hash
	}
	paths := map[string]any{}
	for _, name := range release.CoreTools {
		link := filepath.Join(dir, ".local/bin", name)
		if name != "hc" {
			binary, err := exec.LookPath(name)
			if err != nil {
				binary, err = exec.LookPath("python3")
			}
			if err != nil {
				f.t.Fatal(err)
			}
			if err = os.Symlink(binary, link); err != nil {
				f.t.Fatal(err)
			}
		}
		value, err := os.Readlink(link)
		if err != nil {
			f.t.Fatal(err)
		}
		paths[name] = value
	}
	provenance, err := os.ReadFile(filepath.Join(dir, ".local/share/hc/provenance.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var hc map[string]any
	if err = json.Unmarshal(provenance, &hc); err != nil {
		f.t.Fatal(err)
	}
	offered, err := release.ReadTargetContract(context.Background(), dir)
	if err != nil {
		f.t.Fatal(err)
	}
	s := nativeTestStore(f.t, f.home)
	state, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	instance, _ := state.(*ordjson.Object).Get("instance")
	host, err := s.Machine()
	if err != nil {
		f.t.Fatal(err)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	inventory := map[string]any{"schema": 1, "dependencies": []any{map[string]any{"id": "sumctl", "source": "go/cmd/sumctl", "version": offered.SumVersion, "checksum": "unit built artifact", "license": "MIT", "platforms": []string{platform}, "requirements": []any{}, "role": "runtime", "owner": "sum", "contracts": map[string]any{}}}}
	manifest := map[string]any{"schema": 1, "kind": "sum-release", "sum_version": offered.SumVersion, "source": map[string]any{"sha": sha, "tree": f.git(f.root, "rev-parse", sha+"^{tree}"), "repository": f.root}, "files": files, "contracts": offered.Contracts, "supports": offered.Supports, "dependencies": map[string]any{"tools": map[string]any{"pins": map[string]any{}, "paths": paths}, "inventory": inventory, "native": map[string]any{"sumctl": map[string]any{"path": ".local/bin/sumctl", "sha256": sha256Hex(helper), "platform": platform}}, "hc": hc}, "staged_at": store.Now(), "staged_by": map[string]any{"machine": host.ID, "installation": f.root, "instance": instance}}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "release.json"), encoded, 0600); err != nil {
		f.t.Fatal(err)
	}
	if _, err = release.VerifyRelease(dir, sha); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fleetLab) verifyActivationRecovery(tasks map[string]map[string]any, previous, candidate string) {
	f.t.Helper()
	writeReleaseFile(f.t, f.root, "local-only.txt", []byte("unmerged\n"), 0644)
	f.git(f.root, "add", "local-only.txt")
	f.git(f.root, "commit", "-q", "-m", "unmerged local commit")
	refused := f.ctl(false, "update", "apply", "--ref", "HEAD", "--no-fetch")
	if !strings.Contains(asString(refused["error"]), "not merged on origin/main") {
		f.t.Fatalf("unmerged apply=%v", refused)
	}
	s := nativeTestStore(f.t, f.home)
	ctx, err := store.Context(f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	priorPath := filepath.Join(f.root, ".local/releases", previous)
	candidatePath := filepath.Join(f.root, ".local/releases", candidate)
	hits := 0
	updatecmd.TestPostCheck = func(_ *store.Store, root string) *ordjson.Object {
		row := ordjson.NewObject()
		if sameFile(currentRuntimePath(root), candidatePath) {
			hits++
			f.ctl(true, "ask", asString(tasks["cooperative-a"]["id"]), "--key", fmt.Sprintf("activation-%d", hits), "--text", "Question during activation?")
			f.ctlPane(workerAddress(tasks["failed-refresh"]), true, "report", asString(tasks["failed-refresh"]["id"]), "--text", fmt.Sprintf("Report during activation %d.", hits))
			row.Set("ok", false)
			row.Set("detail", "fleet candidate-only post-check failure")
		} else {
			row.Set("ok", true)
			row.Set("detail", nil)
		}
		return row
	}
	defer func() { updatecmd.TestPostCheck = nil; updatecmd.TestRecoverPending = nil }()
	updatecmd.TestRecoverPending = func(*store.Store, string, string) (*ordjson.Object, error) {
		return nil, fmt.Errorf("fleet test-only compensation fault")
	}
	if _, err = updatecmd.Apply(s, ctx, candidate, true); err == nil || !strings.Contains(err.Error(), "Recovery also failed") {
		f.t.Fatalf("failed compensation=%v", err)
	}
	pending := asMap(asMap(f.ctl(true, "update", "status")["activation"])["pending"])
	if asString(asMap(pending["to"])["sha"]) != candidate || !sameFile(f.currentPath(), candidatePath) {
		f.t.Fatalf("failed compensation discarded pending selection=%v", pending)
	}
	updatecmd.TestRecoverPending = nil
	if _, err = updatecmd.Recover(s, ctx, asString(pending["generation"])); err != nil {
		f.t.Fatal(err)
	}
	if !sameFile(f.currentPath(), priorPath) {
		f.t.Fatal("recovery did not restore prior compatible artifact")
	}
	if _, err = updatecmd.Apply(s, ctx, candidate, true); err == nil || !strings.Contains(err.Error(), "restored and verified") {
		f.t.Fatalf("verified compensation=%v", err)
	}
	if hits != 2 {
		f.t.Fatalf("candidate failure injections=%d", hits)
	}
	if asMap(f.ctl(true, "update", "status")["activation"])["pending"] != nil {
		f.t.Fatal("successful recovery left pending activation")
	}
	for _, key := range []string{"activation-1", "activation-2"} {
		if !strings.Contains(strings.Join(questionKeys(f.readTask(asString(tasks["cooperative-a"]["id"]))), ","), key) {
			f.t.Fatalf("concurrent callback %s lost", key)
		}
	}
	if asString(asMap(f.readTask(asString(tasks["failed-refresh"]["id"]))["report"])["text"]) != "Report during activation 2." {
		f.t.Fatal("concurrent report lost during compensation")
	}
}
