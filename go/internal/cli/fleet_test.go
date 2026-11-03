package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/release"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var fleetRoles = []string{
	"busy-tool-call", "dirty-checkout", "open-question", "answered-unapplied", "pending-report", "old-runtime-client",
	"parentless-question", "unknown-worker", "blocked-worker", "unreported-worker", "cooperative-a", "cooperative-b",
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
	brief := f.brief()
	for _, role := range fleetRoles {
		task := f.ctl(true, "dispatch", "--repo", f.project(role), "--brief", brief, "--harness", "codex", "--approved")
		address := workerAddress(task)
		if address == "" || task["parent"] != nil {
			t.Fatalf("%s is not parentless: %v", role, task)
		}
		requireLogicalActor(t, f.home, task["owner"])
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
	f.native.SetStatus(workerAddress(tasks["busy-tool-call"]), "working")
	wip := filepath.Join(asString(tasks["dirty-checkout"]["worktree"]), "wip.txt")
	if err := os.WriteFile(wip, []byte("uncommitted work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	openQ := asMap(f.ctlPane(workerAddress(tasks["open-question"]), true, "ask", asString(tasks["open-question"]["id"]), "--key", "open", "--text", "Open question?")["question"])
	answered := asMap(f.ctlPane(workerAddress(tasks["answered-unapplied"]), true, "ask", asString(tasks["answered-unapplied"]["id"]), "--key", "answered", "--text", "Answered question?")["question"])
	f.ctl(true, "answer", asString(tasks["answered-unapplied"]["id"]), asString(answered["id"]), "--text", "Yes.")
	f.ctlPane(workerAddress(tasks["pending-report"]), true, "report", asString(tasks["pending-report"]["id"]), "--text", "Candidate ready; not verified.")
	f.ctlPane(workerAddress(tasks["parentless-question"]), true, "ask", asString(tasks["parentless-question"]["id"]), "--key", "parentless", "--text", "No native parent is needed.")
	f.native.SetStatus(workerAddress(tasks["blocked-worker"]), "blocked")
	unknown := workerAddress(tasks["unknown-worker"])
	stopNativeRecoveryTerminal(t, f.demoLab, unknown)
	client := f.native.Client()
	if _, err := client.Remove(context.Background(), unknown); err != nil {
		t.Fatal(err)
	}

	before := map[string][]byte{}
	for _, task := range tasks {
		id := asString(task["id"])
		raw, err := os.ReadFile(filepath.Join(f.home, "tasks", id, "task.json"))
		if err != nil {
			t.Fatal(err)
		}
		before[id] = raw
	}
	checkouts := f.checkouts(tasks)
	callsBefore := len(f.native.Calls())
	live := f.ctl(true, "status", "--live")
	if live["live"] != true || len(asSlice(live["tasks"])) != 12 {
		t.Fatalf("live status=%v", live)
	}
	assertFleetReadOnly(t, f.native.Calls()[callsBefore:])
	for role, want := range map[string]string{"busy-tool-call": "working", "blocked-worker": "blocked", "unknown-worker": "absent"} {
		found := false
		for _, raw := range asSlice(live["tasks"]) {
			row := asMap(raw)
			if row["id"] == tasks[role]["id"] {
				found = true
				if state := asString(asMap(row["observed"])["state"]); state != want {
					t.Fatalf("%s observed=%v", role, row["observed"])
				}
			}
		}
		if !found {
			t.Fatalf("%s absent from status", role)
		}
	}

	// The artifact fixture supplies update provenance after worker dispatch; its
	// binary deliberately implements no core lifecycle operations.
	hctest.InstallRuntime(t, f.root)
	skill, err := os.ReadFile(filepath.Join(f.root, "skills/sum-work/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	sha := f.commitUpstream("skills/sum-work/SKILL.md", string(skill)+"\nNext-start instruction update.\n")
	f.stageFixture(sha)
	selected := f.currentPath()
	callsBefore = len(f.native.Calls())
	refused = f.ctl(false, "update", "apply", "--ref", sha, "--no-fetch")
	if !strings.Contains(asString(refused["error"]), "is active") || f.currentPath() != selected {
		t.Fatalf("active fleet changed runtime selection: %v", refused)
	}
	assertFleetReadOnly(t, f.native.Calls()[callsBefore:])
	for id, want := range before {
		got, err := os.ReadFile(filepath.Join(f.home, "tasks", id, "task.json"))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("quiescent refusal rewrote task %s: %v", id, err)
		}
	}
	if !checkoutEqual(f.checkouts(tasks), checkouts) {
		t.Fatal("quiescent refusal altered task checkouts")
	}
	if body, err := os.ReadFile(wip); err != nil || string(body) != "uncommitted work\n" {
		t.Fatal("dirty work was lost", err)
	}

	f.ctl(true, "answer", asString(tasks["open-question"]["id"]), asString(openQ["id"]), "--text", "Continue the approved work.")
	f.ctlPane(workerAddress(tasks["answered-unapplied"]), true, "resolve", asString(tasks["answered-unapplied"]["id"]), asString(answered["id"]))
	pending := tasks["pending-report"]
	stopNativeRecoveryTerminal(t, f.demoLab, workerAddress(pending))
	worker := asMap(asMap(f.readTask(asString(pending["id"]))["execution"])["worker"])
	f.ctl(true, "execution", "park", asString(pending["id"]), "--attempt", asString(worker["id"]))
	f.ctl(true, "archive", asString(pending["id"]), "--acknowledge")
	late := f.ctl(true, "prepare", "--repo", f.project("late"), "--brief", brief, "--harness", "codex", "--approved")
	if asInt(asMap(asMap(late["admission"])["occupied_before"])["global"]) != 11 {
		t.Fatalf("released reservation not reusable=%v", late["admission"])
	}
	runs := 0
	for _, call := range f.native.Calls() {
		if call.Method == "terminal.run" {
			runs++
			if call.Params["parent"] != nil || call.Params["caller"] != nil {
				t.Fatalf("worker creation carried a coordinator parent: %v", call)
			}
		}
		if call.Method == "terminal.kill" || call.Method == "terminal.interrupt" {
			t.Fatalf("fleet operation stopped unrelated execution=%v", call)
		}
	}
	if runs != 12 {
		t.Fatalf("fleet created %d native workers, want12", runs)
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
