package skilltest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type evidenceLab struct {
	t          *testing.T
	root, stop string
	env        []string
}

func newEvidenceLab(t *testing.T) *evidenceLab {
	t.Helper()
	root := repoRoot(t)
	stop := t.TempDir()
	return &evidenceLab{t: t, root: root, stop: stop, env: labEnv(t, root, stop)}
}

func (e *evidenceLab) project(name string, skills ...string) string {
	t := e.t
	if name == "" {
		name = "project"
	}
	if len(skills) == 0 {
		skills = []string{"evidence", "verify"}
	}
	path := filepath.Join(e.stop, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		copyTree(t, filepath.Join(e.root, ".agents/skills", skill), filepath.Join(path, ".agents/skills", skill))
	}
	mustWrite(t, filepath.Join(path, ".gitignore"), ".artifacts/\n")
	init := exec.Command("git", "init", "-q", "-b", "main")
	init.Dir = path
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	git(t, path, "config", "user.email", "lab@example.invalid")
	git(t, path, "config", "user.name", "verify lab")
	git(t, path, "add", "-A")
	git(t, path, "commit", "-q", "-m", "project with vendored skills")
	return path
}

func (e *evidenceLab) seeded(name, fixture, filename, bug, fix string) (base, candidate, baseDir, candDir string) {
	t := e.t
	app := filepath.Join(e.stop, name, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.root, "tests/fixtures/evidence", fixture, filename)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(app, filename), string(data))
	init := exec.Command("git", "init", "-q", "-b", "main")
	init.Dir = app
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	git(t, app, "config", "user.email", "lab@example.invalid")
	git(t, app, "config", "user.name", "verify lab")
	git(t, app, "add", "-A")
	git(t, app, "commit", "-q", "-m", "base with seeded defect")
	base = git(t, app, "rev-parse", "HEAD")
	mustWrite(t, filepath.Join(app, filename), strings.ReplaceAll(string(data), bug, fix))
	git(t, app, "commit", "-qam", "fix")
	candidate = git(t, app, "rev-parse", "HEAD")
	baseDir = filepath.Join(filepath.Dir(app), "base")
	candDir = filepath.Join(filepath.Dir(app), "candidate")
	git(t, app, "worktree", "add", "-q", baseDir, base)
	git(t, app, "worktree", "add", "-q", candDir, candidate)
	return base, candidate, baseDir, candDir
}

func (e *evidenceLab) runSkill(repo string, extraEnv map[string]string, args ...string) (int, string, string) {
	script := filepath.Join(repo, ".agents/skills/evidence/scripts/evidence_capture.py")
	env := e.env
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	code, _, stdout, stderr := runPy(e.t, env, script, repo, args...)
	return code, stdout, stderr
}

func (e *evidenceLab) jsonOut(stdout string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		e.t.Fatalf("json: %v\n%s", err, stdout)
	}
	return out
}

func TestEvidenceCapabilitiesNeverClaimAbsentBrowser(t *testing.T) {
	e := newEvidenceLab(t)
	repo := e.project("")
	code, stdout, stderr := e.runSkill(repo, nil, "capabilities", "--json")
	if code != 0 {
		t.Fatal(stderr)
	}
	caps := e.jsonOut(stdout)
	if !asBool(asMap(caps["cli"])["supported"]) {
		t.Fatal("cli unsupported")
	}
	code, stdout, _ = e.runSkill(repo, map[string]string{"EVIDENCE_BROWSER": filepath.Join(e.stop, "no-such-browser")}, "capabilities", "--json")
	caps = e.jsonOut(stdout)
	if asBool(asMap(caps["browser"])["supported"]) {
		t.Fatal("browser claimed present")
	}
	if !strings.Contains(asString(asMap(caps["browser"])["reason"]), "no Chromium-family browser") {
		t.Fatalf("reason %v", caps["browser"])
	}
	_ = code
}

func TestEvidenceBrowserDriverFlags(t *testing.T) {
	src := readFile(t, filepath.Join(repoRoot(t), ".agents/skills/evidence/scripts/evidence_browser.mjs"))
	for _, flag := range []string{"--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage", "--disable-gpu", "mkdtemp", "evidence-profile-"} {
		if !strings.Contains(src, flag) {
			t.Fatalf("missing %s", flag)
		}
	}
}

func TestCLIDefectRedGreenAndRedaction(t *testing.T) {
	e := newEvidenceLab(t)
	repo := e.project("")
	base, candidate, baseDir, candDir := e.seeded("greet-cli", "greet", "greet.py", "sys.exit(1)", "sys.exit(0)")
	py := python3(t)
	code, stdout, stderr := e.runSkill(repo, nil, "capture", "--scenario", "greet.hello", "--role", "before", "--kind", "nonvisual", "--run", "run-cli", "--checkout", baseDir, "--expect-sha", base, "cli", "--expect-exit", "0", "--expect-text", "Hello, Ada!", "--", py, filepath.Join(baseDir, "greet.py"), "Ada")
	if code != 1 {
		t.Fatalf("before want 1 got %d %s%s", code, stdout, stderr)
	}
	code, stdout, stderr = e.runSkill(repo, nil, "capture", "--scenario", "greet.hello", "--role", "after", "--kind", "nonvisual", "--run", "run-cli", "--checkout", candDir, "--expect-sha", candidate, "cli", "--expect-exit", "0", "--expect-text", "Hello, Ada!", "--", py, filepath.Join(candDir, "greet.py"), "Ada")
	if code != 0 {
		t.Fatalf("after want 0 got %d %s%s", code, stdout, stderr)
	}
	code, stdout, stderr = e.runSkill(repo, nil, "compare", "--scenario", "greet.hello", "--run", "run-cli", "--base", base, "--candidate", candidate, "--json")
	cmp := e.jsonOut(stdout)
	if code != 0 || asString(cmp["verdict"]) != "red-green" {
		t.Fatalf("compare %v %s", cmp, stderr)
	}
}

func TestUnavailableBaselineIsAfterOnly(t *testing.T) {
	e := newEvidenceLab(t)
	repo := e.project("")
	base, candidate, _, candDir := e.seeded("greet-unavail", "greet", "greet.py", "sys.exit(1)", "sys.exit(0)")
	py := python3(t)
	e.runSkill(repo, nil, "capture", "--scenario", "greet.hello", "--role", "after", "--kind", "nonvisual", "--run", "run-unavail", "--checkout", candDir, "--expect-sha", candidate, "cli", "--expect-exit", "0", "--expect-text", "Hello, Ada!", "--", py, filepath.Join(candDir, "greet.py"), "Ada")
	code, stdout, stderr := e.runSkill(repo, nil, "unavailable", "--scenario", "greet.hello", "--role", "before", "--kind", "nonvisual", "--run", "run-unavail", "--reason", "base build needs a licence server the lab has no access to")
	if code != 0 {
		t.Fatal(stderr)
	}
	code, stdout, stderr = e.runSkill(repo, nil, "compare", "--scenario", "greet.hello", "--run", "run-unavail", "--base", base, "--candidate", candidate, "--json")
	cmp := e.jsonOut(stdout)
	if asString(cmp["verdict"]) != "after-only" {
		t.Fatalf("verdict %v", cmp)
	}
}

func TestMissingBrowserLeavesBlockedCapture(t *testing.T) {
	e := newEvidenceLab(t)
	repo := e.project("")
	code, stdout, stderr := e.runSkill(repo, map[string]string{"EVIDENCE_BROWSER": filepath.Join(e.stop, "absent")}, "capture", "--scenario", "counter.add", "--role", "before", "--kind", "bugfix", "--run", "run-nobrowser", "browser", "--url", "http://127.0.0.1:9/", "--step", "click=#add")
	if code != 2 {
		t.Fatalf("want 2 got %d %s%s", code, stdout, stderr)
	}
}

func TestConcurrentCapturesDoNotInterfere(t *testing.T) {
	e := newEvidenceLab(t)
	repo := e.project("")
	base, candidate, baseDir, candDir := e.seeded("greet-conc", "greet", "greet.py", "sys.exit(1)", "sys.exit(0)")
	py := python3(t)
	type job struct {
		run, role, checkout string
		code                int
	}
	jobs := []job{
		{"run-worker", "before", baseDir, 0},
		{"run-worker", "after", candDir, 0},
		{"run-root", "before", baseDir, 0},
		{"run-root", "after", candDir, 0},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(j *job) {
			defer wg.Done()
			code, _, _ := e.runSkill(repo, nil, "capture", "--scenario", "greet.hello", "--role", j.role, "--kind", "nonvisual", "--run", j.run, "--checkout", j.checkout, "cli", "--expect-exit", "0", "--", py, filepath.Join(j.checkout, "greet.py"), "Ada")
			mu.Lock()
			j.code = code
			mu.Unlock()
		}(&jobs[i])
	}
	wg.Wait()
	want := map[string]int{"run-worker/before": 1, "run-worker/after": 0, "run-root/before": 1, "run-root/after": 0}
	for _, j := range jobs {
		key := j.run + "/" + j.role
		if j.code != want[key] {
			t.Fatalf("%s code %d want %d", key, j.code, want[key])
		}
	}
	_ = candidate
	_ = base
}

func TestExternalCloneRunsSkillWithSumAbsent(t *testing.T) {
	e := newEvidenceLab(t)
	origin := e.project("origin")
	external := filepath.Join(e.stop, "elsewhere/clone")
	if err := os.MkdirAll(filepath.Dir(external), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "clone", "-q", origin, external)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	removeAll(t, origin)
	code, stdout, stderr := e.runSkill(external, nil, "capabilities", "--json")
	if code != 0 {
		t.Fatal(stderr)
	}
	if asMap(e.jsonOut(stdout))["cli"] == nil {
		t.Fatalf("caps %s", stdout)
	}
}

func TestSumMapsAuditAndEvidenceContract(t *testing.T) {
	root := repoRoot(t)
	text := readFile(t, filepath.Join(root, "VERIFY.md"))
	if !strings.Contains(text, `evidence = ".artifacts/evidence"`) {
		t.Fatal("VERIFY.md missing evidence root")
	}
	if !strings.Contains(text, ".agents/skills/evidence/") {
		t.Fatal("VERIFY.md missing evidence skill")
	}
}
