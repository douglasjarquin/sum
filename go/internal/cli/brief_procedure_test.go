package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/procedure"
)

// newRuntimeLab is a policy lab whose runtime is a disposable copy of this checkout's helper and
// skills, so a test can remove or change the worker procedure without touching the repository.
// Paths contain spaces on purpose.
func newRuntimeLab(t *testing.T, withProcedure bool) *demoLab {
	t.Helper()
	root, _ := repoReference(t)
	base := filepath.Join(t.TempDir(), "lab base")
	runtime := filepath.Join(base, "run time")
	copyFile := func(rel string, mode os.FileMode) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(runtime, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	copyFile("bin/sumctl", 0o755)
	copyFile(".local/bin/sumctl", 0o755)
	copyFile("skills/sum-delivery/SKILL.md", 0o644)
	copyFile("COORDINATOR.md", 0o644)
	copyFile(".agents/skills/verify/references/engineering-principles.md", 0o644)
	if withProcedure {
		copyFile("skills/sum-worker/SKILL.md", 0o644)
		for _, src := range procedure.Sources[1:] {
			copyFile(src.Path, 0o644)
		}
	}
	home := filepath.Join(base, "state home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &demoLab{t: t, root: runtime, helper: filepath.Join(runtime, "bin", "sumctl"), home: home, base: base, env: demoEnv(t, root, base)}
	d.setEnv("FAKE_PARENT_CWD", runtime)
	if role := asString(d.ctl(true, "init")["role"]); role != "coordinator" {
		t.Fatalf("init role = %q", role)
	}
	return d
}

func (d *demoLab) writeProcedure(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(d.root, "skills", "sum-worker", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (d *demoLab) setPaneStatus(t *testing.T, pane, status string) {
	t.Helper()
	path := filepath.Join(d.base, "fake", "state.json")
	var state map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	asMap(asMap(state["panes"])[pane])["agent_status"] = status
	out, _ := json.Marshal(state)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (d *demoLab) prompts(t *testing.T, pane string) []string {
	t.Helper()
	var out []string
	for _, call := range herdrCalls(t, d.base) {
		if len(call) >= 4 && call[0] == "agent" && call[1] == "prompt" && call[2] == pane {
			out = append(out, call[3])
		}
	}
	return out
}

func workerProcedureRows(t *testing.T, d *demoLab, taskID, pane string) []any {
	t.Helper()
	view := d.ctlPane(pane, true, "context", taskID, "--role", "worker", "--section", "environment")
	return asSlice(asMap(view["environment"])["procedure"])
}

func TestDispatchPinsTheWorkerProcedureAndLaunchesFromIt(t *testing.T) {
	d := newRuntimeLab(t, true)
	repo := policyProject(t, d.base, "pinned", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID, pane := asString(task["id"]), asString(task["pane"])
	brief, err := os.ReadFile(asString(task["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	pinned := pinnedProcedure(t, string(brief))
	if !strings.HasPrefix(pinned, filepath.Join(d.home, "tasks", taskID, "procedure", "sum-worker-")) {
		t.Fatalf("pinned procedure %s is not a task resource", pinned)
	}
	source, _ := os.ReadFile(filepath.Join(d.root, "skills", "sum-worker", "SKILL.md"))
	if got, _ := os.ReadFile(pinned); string(got) != string(source) {
		t.Fatal("pinned procedure differs from the runtime source")
	}
	if strings.Contains(string(brief), string(source[:200])) {
		t.Fatal("brief embeds the procedure body")
	}
	launch := d.prompts(t, pane)
	if len(launch) != 1 || !strings.Contains(launch[0], asString(task["brief_path"])) || !strings.Contains(launch[0], "every required worker procedure file") {
		t.Fatalf("launch prompt = %v", launch)
	}
	rows := workerProcedureRows(t, d, taskID, pane)
	if len(rows) != len(procedure.Sources) || asMap(rows[0])["ok"] != true || asString(asMap(rows[0])["path"]) != pinned || asString(asMap(rows[0])["load"]) != procedure.Required {
		t.Fatalf("worker context procedure = %v", rows)
	}
	// Action-scoped files are pinned too, but listed with their condition, never inlined or required.
	for i, src := range procedure.Sources[1:] {
		row := asMap(rows[i+1])
		if asString(row["name"]) != src.Name || asString(row["load"]) != procedure.OnDemand || row["ok"] != true {
			t.Fatalf("on-demand row %d = %v", i+1, row)
		}
		if !strings.Contains(string(brief), "- Read when "+src.When+": `"+asString(row["path"])+"`") {
			t.Fatalf("brief does not list %s with its condition:\n%s", src.Name, brief)
		}
		body, _ := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(src.Path)))
		if got, _ := os.ReadFile(asString(row["path"])); string(got) != string(body) {
			t.Fatalf("pinned %s differs from its runtime source", src.Name)
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		if strings.Contains(string(brief), lines[len(lines)-1]) {
			t.Fatalf("brief inlines on-demand %s", src.Name)
		}
	}
	if strings.Count(string(brief), "- Required before any other step: ") != 1 {
		t.Fatalf("brief requires more than the core:\n%s", brief)
	}
	// The worker's own reads name only the pinned copies: no live runtime sum-worker beside them.
	skills := asMap(asMap(d.ctlPane(pane, true, "context", taskID, "--role", "worker", "--section", "environment")["environment"])["skills"])
	if files := asSlice(skills["files"]); len(files) != 0 {
		t.Fatalf("worker context still offers live skill files %v", files)
	}
	initRows := asSlice(d.ctlPane(pane, true, "init")["procedure"])
	if len(initRows) != len(procedure.Sources) || asString(asMap(initRows[0])["path"]) != pinned {
		t.Fatalf("worker init procedure = %v", initRows)
	}

	// The procedure survives the runtime copy disappearing: a recovered worker reads it from the task.
	if err := os.Remove(filepath.Join(d.root, "skills", "sum-worker", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if rows := workerProcedureRows(t, d, taskID, pane); asMap(rows[0])["ok"] != true {
		t.Fatalf("procedure after the runtime copy is gone = %v", rows)
	}
}

func TestDispatchRefusesARuntimeWithoutTheProcedure(t *testing.T) {
	d := newRuntimeLab(t, false)
	repo := policyProject(t, d.base, "missing", map[string]string{"README.md": "x\n"})
	before := len(herdrCalls(t, d.base))
	refused := d.ctl(false, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	errText := asString(refused["error"])
	if !strings.Contains(errText, "skills/sum-worker/SKILL.md is missing") || !strings.Contains(errText, "no brief was written") {
		t.Fatalf("dispatch without procedure = %v", refused)
	}
	for _, call := range herdrCalls(t, d.base)[before:] {
		if len(call) > 1 && (call[0] == "worktree" || call[0] == "workspace" || (call[0] == "agent" && call[1] == "start")) {
			t.Fatalf("refused dispatch still called Herdr: %v", call)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(d.home, "tasks")); len(entries) != 0 {
		t.Fatalf("refused dispatch recorded tasks: %v", entries)
	}
}

func TestStartRefusesATamperedProcedureUntilItIsRepinned(t *testing.T) {
	d := newRuntimeLab(t, true)
	repo := policyProject(t, d.base, "tampered", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID := asString(task["id"])
	brief, err := os.ReadFile(asString(task["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	pinned := pinnedProcedure(t, string(brief))
	if err := os.WriteFile(pinned, []byte("# tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(herdrCalls(t, d.base))
	refused := d.ctl(false, "start", taskID)
	if !strings.Contains(asString(refused["error"]), "No worker was started") || !strings.Contains(asString(refused["error"]), "does not match its recorded sha256") {
		t.Fatalf("start with tampered procedure = %v", refused)
	}
	for _, call := range herdrCalls(t, d.base)[before:] {
		if len(call) > 1 && call[0] == "agent" && (call[1] == "start" || call[1] == "prompt") {
			t.Fatalf("refused start called Herdr: %v", call)
		}
	}
	listed := d.ctl(true, "brief", "list", taskID)
	if asMap(asSlice(listed["revisions"])[0])["ok"] != false {
		t.Fatalf("tampered revision still ok: %v", listed["revisions"])
	}
	if regen := d.ctl(false, "brief", "regenerate", taskID); !strings.Contains(asString(regen["error"]), "never overwritten") {
		t.Fatalf("regenerate over a tampered file = %v", regen)
	}
	if got, _ := os.ReadFile(pinned); string(got) != "# tampered\n" {
		t.Fatal("the tampered file was rewritten")
	}

	if err := os.Rename(pinned, pinned+".aside"); err != nil {
		t.Fatal(err)
	}
	d.ctl(true, "brief", "regenerate", taskID)
	for _, raw := range asSlice(d.ctl(true, "brief", "list", taskID)["revisions"]) {
		if rev := asMap(raw); rev["ok"] != true {
			t.Fatalf("revision %v after re-pinning: %v", rev["id"], rev["error"])
		}
	}
	started := d.ctl(true, "start", taskID)
	if asString(started["status"]) == "" {
		t.Fatalf("start after re-pinning = %v", started)
	}
}

func TestDecisionOnlyRefreshReadsThroughBoundedContext(t *testing.T) {
	d := newRuntimeLab(t, true)
	repo := policyProject(t, d.base, "decisions", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID, pane := asString(task["id"]), asString(task["pane"])
	ask := func(key string) string {
		d.ctlPane(pane, true, "ask", taskID, "--key", key, "--text", "Keep "+key+"?")
		questions := asSlice(d.ctl(true, "show", taskID)["questions"])
		return asString(asMap(questions[len(questions)-1])["id"])
	}
	d.ctl(true, "answer", taskID, ask("punctuation"), "--text", "Yes.")
	d.setPaneStatus(t, pane, "idle")
	before := len(d.prompts(t, pane))
	d.ctl(true, "refresh", "request", "--task", taskID)
	var refresh string
	for _, p := range d.prompts(t, pane)[before:] {
		if strings.HasPrefix(p, "sum refresh "+taskID) {
			refresh = p
		}
	}
	match := regexp.MustCompile(`Only recorded decisions changed since your active revision r1: at your next safe point read them with (.*? --section decisions), then run (.*? brief adopt ` + taskID + ` r2) and continue`).FindStringSubmatch(refresh)
	if match == nil {
		t.Fatalf("decision-only refresh message = %q", refresh)
	}
	out, err := exec.Command("sh", "-c", match[1]).Output()
	if err != nil || !strings.Contains(string(out), "punctuation") {
		t.Fatalf("context command %q: %v\n%s", match[1], err, out)
	}
	d.ctlPane(pane, true, "brief", "adopt", taskID, "r2")

	// A procedure change staged but never adopted keeps a later decision from reading as decision-only.
	d.writeProcedure(t, "# sum-worker\nchanged procedure\n")
	d.ctl(true, "brief", "regenerate", taskID)
	d.ctl(true, "brief", "request", taskID, "r3")
	d.ctl(true, "answer", taskID, ask("naming"), "--text", "greet.")
	d.setPaneStatus(t, pane, "idle")
	before = len(d.prompts(t, pane))
	second := d.ctl(true, "refresh", "request", "--task", taskID)
	refresh = ""
	for _, p := range d.prompts(t, pane)[before:] {
		if strings.HasPrefix(p, "sum refresh "+taskID) {
			refresh = p
		}
	}
	if !strings.Contains(refresh, "brief revision r4 is requested") || strings.Contains(refresh, "Only recorded decisions changed") || !strings.Contains(refresh, "read `") || !strings.Contains(refresh, "r4.md` completely") {
		t.Fatalf("refresh after an unadopted procedure change = %q\n%v", refresh, second)
	}
}

func TestBusyWorkerGetsDecisionOnlyWordingFromTheReturnNotice(t *testing.T) {
	d := newRuntimeLab(t, true)
	repo := policyProject(t, d.base, "busy", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID, pane := asString(task["id"]), asString(task["pane"])
	d.ctlPane(pane, true, "ask", taskID, "--key", "punctuation", "--text", "Keep it?")
	qid := asString(asMap(asSlice(d.ctl(true, "show", taskID)["questions"])[0])["id"])
	d.ctl(true, "answer", taskID, qid, "--text", "Yes.")
	d.ctlPane(pane, true, "resolve", taskID, qid)
	d.setPaneStatus(t, pane, "working")
	row := asMap(asSlice(d.ctl(true, "refresh", "request", "--task", taskID)["targets"])[0])
	if asString(row["revision"]) != "r2" {
		t.Fatalf("refresh row = %v", row)
	}
	d.setPaneStatus(t, pane, "idle")
	before := len(d.prompts(t, pane))
	d.ctl(true, "pump")
	var notice string
	for _, p := range d.prompts(t, pane)[before:] {
		if strings.HasPrefix(p, "sum returns for the worker") {
			notice = p
		}
	}
	want := "brief revision r2 is requested and only recorded decisions changed; read them with "
	if !strings.Contains(notice, want) || !strings.Contains(notice, "context "+taskID+" --role worker --section decisions") || !strings.Contains(notice, "Read the durable record with ") || !strings.Contains(notice, "context "+taskID+" --role worker.") {
		t.Fatalf("worker notice = %q", notice)
	}
}

func TestBackupCarriesThePinnedProcedure(t *testing.T) {
	d := newRuntimeLab(t, true)
	repo := policyProject(t, d.base, "backup", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID := asString(task["id"])
	brief, err := os.ReadFile(asString(task["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	pinned := pinnedProcedure(t, string(brief))
	dest := filepath.Join(d.base, "records.tar.gz")
	d.ctl(true, "backup", dest)
	listing, err := exec.Command("tar", "-tzf", dest).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "state/tasks/" + taskID + "/procedure/" + filepath.Base(pinned)
	if !strings.Contains(string(listing), want) {
		t.Fatalf("backup lacks %s:\n%s", want, listing)
	}
}

func TestStartLaunchesFromTheAdoptedRevision(t *testing.T) {
	d := newRuntimeLab(t, true)
	repo := policyProject(t, d.base, "relaunch", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID, pane := asString(task["id"]), asString(task["pane"])
	d.writeProcedure(t, "# sum-worker\nrevised procedure\n")
	staged := d.ctl(true, "brief", "regenerate", taskID)
	r2 := asString(asMap(staged["revision"])["path"])
	if !strings.HasSuffix(r2, filepath.Join("briefs", "r2.md")) {
		t.Fatalf("regenerate staged %v", staged["revision"])
	}
	d.ctl(true, "brief", "request", taskID, "r2")
	d.ctlPane(pane, true, "brief", "adopt", taskID, "r2")
	before := len(d.prompts(t, pane))
	d.ctl(true, "start", taskID)
	launch := d.prompts(t, pane)[before:]
	if len(launch) != 1 || !strings.Contains(launch[0], r2) || strings.Contains(launch[0], asString(task["brief_path"])) {
		t.Fatalf("launch prompt after adopting r2 = %v, want %s", launch, r2)
	}
	body, err := os.ReadFile(r2)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(pinnedProcedure(t, string(body))); string(got) != "# sum-worker\nrevised procedure\n" {
		t.Fatalf("r2 names a procedure that is not the revised one: %q", got)
	}
}
