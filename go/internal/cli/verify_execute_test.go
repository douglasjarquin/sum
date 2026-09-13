package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const verifyRunner = ".agents/skills/verify/scripts/verify_run.py"

type verifyLab struct {
	*demoLab
	repo, baseSHA, taskID, worktree string
}

func newVerifyLab(t *testing.T) *verifyLab {
	t.Helper()
	root, helper := repoReference(t)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "project")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "sum test")
	git("config", "user.email", "test@example.invalid")
	copyTree(t, filepath.Join(root, "tests", "fixtures", "verify", "cli"), repo)
	copyTree(t, filepath.Join(root, ".agents", "skills", "verify"), filepath.Join(repo, ".agents", "skills", "verify"))
	git("add", "-A")
	git("commit", "-m", "standardized fixture")
	baseSHA := git("rev-parse", "HEAD")

	home := filepath.Join(base, "state")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(base, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "tests", "fixtures", "mise.py"), filepath.Join(bin, "mise")); err != nil {
		t.Fatal(err)
	}
	pythonDir := filepath.Dir(mustLookPath(t, "python3"))
	path := strings.Join([]string{bin, pythonDir, os.Getenv("PATH")}, string(os.PathListSeparator))
	if goroot := os.Getenv("GOROOT"); goroot != "" {
		path = strings.Join([]string{bin, pythonDir, filepath.Join(goroot, "bin"), os.Getenv("PATH")}, string(os.PathListSeparator))
	}
	env := demoEnv(t, root, base)
	filtered := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "PATH=") {
			filtered = append(filtered, e)
		}
	}
	env = append(filtered, "PATH="+path)

	brief := filepath.Join(base, "brief.md")
	if err := os.WriteFile(brief, []byte("Add a note and verify the greeting fixture.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &demoLab{t: t, root: root, helper: helper, home: home, base: base, env: env}
	if asString(d.ctl(true, "init")["role"]) != "coordinator" {
		t.Fatal("first init")
	}
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", brief, "--approved")
	taskID := asString(task["id"])
	worktree := asString(task["worktree"])
	if taskID == "" || worktree == "" {
		t.Fatalf("dispatch %v", task)
	}
	return &verifyLab{demoLab: d, repo: repo, baseSHA: baseSHA, taskID: taskID, worktree: worktree}
}

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skip(name + " not on PATH")
	}
	return p
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Name() == "__pycache__" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (v *verifyLab) git(cwd string, args ...string) string {
	v.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		v.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (v *verifyLab) commit(name, text string) string {
	v.t.Helper()
	path := filepath.Join(v.worktree, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		v.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		v.t.Fatal(err)
	}
	v.git(v.worktree, "add", name)
	v.git(v.worktree, "commit", "-m", "change "+name)
	return v.git(v.worktree, "rev-parse", "HEAD")
}

func (v *verifyLab) workerRun(args ...string) map[string]any {
	v.t.Helper()
	cmd := exec.Command(mustLookPath(v.t, "python3"), append([]string{filepath.Join(v.worktree, verifyRunner), "--json", "--base", v.baseSHA}, args...)...)
	cmd.Dir = v.worktree
	cmd.Env = v.env
	out, err := cmd.Output()
	if err != nil {
		stderr := []byte{}
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		v.t.Fatalf("worker run: %v\n%s%s", err, out, stderr)
	}
	var record map[string]any
	if err := json.Unmarshal(out, &record); err != nil {
		v.t.Fatalf("worker run json: %v\n%s", err, out)
	}
	runDir := asString(asMap(record["artifacts"])["run_dir"])
	record["_path"] = filepath.Join(v.worktree, runDir, "run.json")
	return record
}

func (v *verifyLab) artifacts() string {
	v.t.Helper()
	root := filepath.Join(v.worktree, ".artifacts")
	var parts []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		parts = append(parts, rel+" "+info.ModTime().UTC().String())
		return nil
	})
	return strings.Join(parts, "\n")
}

func (v *verifyLab) worktreeCount() int {
	v.t.Helper()
	out := v.git(v.repo, "worktree", "list", "--porcelain")
	return strings.Count(out, "worktree ")
}

func (v *verifyLab) taskFile() map[string]any {
	v.t.Helper()
	data, err := os.ReadFile(filepath.Join(v.home, "tasks", v.taskID, "task.json"))
	if err != nil {
		v.t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(data, &task); err != nil {
		v.t.Fatal(err)
	}
	return task
}

func (v *verifyLab) evidence(out map[string]any) map[string]any {
	v.t.Helper()
	ev := asMap(out["evidence"])
	if ev == nil {
		v.t.Fatalf("missing evidence in %v", out)
	}
	return ev
}

func TestVerifyExecute_standardizedCandidatePassesInSeparateCheckout(t *testing.T) {
	v := newVerifyLab(t)
	sha := v.commit("NOTES.md", "notes\n")
	worker := v.workerRun()
	if asString(worker["outcome"]) != "pass" || asString(worker["certifies"]) != sha {
		t.Fatalf("worker run %v", worker)
	}
	before := v.artifacts()
	beforeTrees := v.worktreeCount()
	out := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute")
	ev := v.evidence(out)
	if asString(ev["source"]) != "coordinator" {
		t.Fatalf("source %v", ev["source"])
	}
	if asString(ev["result"]) != "pass" {
		t.Fatalf("result %v", ev["result"])
	}
	if asString(ev["isolation"]) != "separate-checkout" {
		t.Fatalf("isolation %v", ev["isolation"])
	}
	if asString(ev["run_id"]) == "" || asString(ev["run_id"]) == asString(worker["run_id"]) {
		t.Fatalf("run_id %q worker %q", ev["run_id"], worker["run_id"])
	}
	recordPath := asString(ev["record"])
	marker := filepath.Join("tasks", v.taskID, "verification")
	if !strings.Contains(recordPath, marker) || !strings.HasSuffix(recordPath, string(os.PathSeparator)+"run.json") {
		t.Fatalf("record path %q want under %s", recordPath, marker)
	}
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("run.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(recordPath), "verify.log")); err != nil {
		t.Fatalf("verify.log missing: %v", err)
	}
	kept, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var keptRec map[string]any
	if err := json.Unmarshal(kept, &keptRec); err != nil {
		t.Fatal(err)
	}
	if asString(keptRec["run_id"]) != asString(ev["run_id"]) {
		t.Fatalf("kept run_id %q evidence %q", keptRec["run_id"], ev["run_id"])
	}
	rootPath := asString(ev["root"])
	if rootPath == "" {
		t.Fatal("root missing")
	}
	if resolvedRoot, err := filepath.Abs(rootPath); err == nil {
		if resolvedWork, err := filepath.Abs(v.worktree); err == nil && resolvedRoot == resolvedWork {
			t.Fatal("root is the worker checkout")
		}
	}
	if _, err := os.Stat(rootPath); !os.IsNotExist(err) {
		t.Fatalf("verification checkout still present at %s: %v", rootPath, err)
	}
	if v.artifacts() != before {
		t.Fatalf("worker artifacts changed\nbefore:\n%s\nafter:\n%s", before, v.artifacts())
	}
	if got := v.worktreeCount(); got != beforeTrees {
		t.Fatalf("worktree count %d want %d\n%s", got, beforeTrees, v.git(v.repo, "worktree", "list", "--porcelain"))
	}
}

func TestVerifyExecute_rootRunIdDiffersFromWorkerRunImport(t *testing.T) {
	v := newVerifyLab(t)
	sha := v.commit("NOTES.md", "notes\n")
	worker := v.workerRun()
	imported := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--run", asString(worker["_path"]))
	importedID := asString(v.evidence(imported)["run_id"])
	if importedID != asString(worker["run_id"]) {
		t.Fatalf("imported run_id %q worker %q", importedID, worker["run_id"])
	}
	if asString(v.evidence(imported)["isolation"]) != "task-checkout" {
		t.Fatalf("imported isolation %v", v.evidence(imported)["isolation"])
	}
	executed := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute")
	execID := asString(v.evidence(executed)["run_id"])
	if execID == "" || execID == importedID {
		t.Fatalf("execute run_id %q imported %q", execID, importedID)
	}
	if asString(v.evidence(executed)["isolation"]) != "separate-checkout" {
		t.Fatalf("execute isolation %v", v.evidence(executed)["isolation"])
	}
}

func TestVerifyExecute_refusesWhenCapacityIsHeldAndCreatesNoWorktree(t *testing.T) {
	v := newVerifyLab(t)
	sha := v.commit("capacity.py", "x\n")
	v.ctl(true, "settings", "set", "--global", "1", "--per-repository", "1")
	before := v.git(v.repo, "worktree", "list", "--porcelain")
	out := v.ctl(false, "verify", v.taskID, "--candidate", sha, "--execute")
	errText := asString(out["error"])
	if !strings.Contains(errText, "1 of 1 global execution slots") {
		t.Fatalf("capacity error %q", errText)
	}
	after := v.git(v.repo, "worktree", "list", "--porcelain")
	if after != before {
		t.Fatalf("worktrees changed\nbefore:\n%s\nafter:\n%s", before, after)
	}
	verifiers := asSlice(asMap(v.taskFile()["execution"])["verifiers"])
	if len(verifiers) != 0 {
		t.Fatalf("verifiers %v", verifiers)
	}
}

func TestVerifyExecute_missingRunnerRefusesAndReleasesAfterEmptyBoundary(t *testing.T) {
	// #125: empty-boundary teardown releases. Missing identity would not.
	v := newVerifyLab(t)
	if err := os.Rename(filepath.Join(v.worktree, ".agents"), filepath.Join(v.worktree, "agents-moved")); err != nil {
		t.Fatal(err)
	}
	v.git(v.worktree, "add", "-A")
	v.git(v.worktree, "commit", "-m", "drop runner")
	sha := v.git(v.worktree, "rev-parse", "HEAD")
	beforeTrees := v.worktreeCount()
	out := v.ctl(false, "verify", v.taskID, "--candidate", sha, "--execute")
	errText := asString(out["error"])
	if !strings.Contains(errText, "carries no .agents/skills/verify") {
		t.Fatalf("missing runner error %q", errText)
	}
	if v.worktreeCount() != beforeTrees {
		t.Fatalf("leftover worktree\n%s", v.git(v.repo, "worktree", "list", "--porcelain"))
	}
	verDir := filepath.Join(v.home, "tasks", v.taskID, "verification")
	_ = filepath.Walk(verDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && info.Name() == "checkout" {
			t.Fatalf("leftover checkout %s", path)
		}
		return err
	})
	if len(asSlice(v.taskFile()["evidence"])) != 0 {
		t.Fatalf("evidence %v", v.taskFile()["evidence"])
	}
	execution := asMap(v.taskFile()["execution"])
	verifiers := asSlice(execution["verifiers"])
	if len(verifiers) != 1 {
		t.Fatalf("verifiers %v", verifiers)
	}
	if asString(asMap(verifiers[0])["state"]) != "released" {
		t.Fatalf("verifier state %v, want released after empty-boundary teardown", asMap(verifiers[0])["state"])
	}
	workerState := asString(asMap(execution["worker"])["state"])
	held := map[string]bool{"held": true, "observing": true, "starting": true, "running": true, "uncertain": true}
	if !held[workerState] {
		t.Fatalf("worker occupancy dropped without proof, state %q", workerState)
	}
}

func TestVerifyExecute_nonCoordinatorPaneIsRefused(t *testing.T) {
	v := newVerifyLab(t)
	sha := v.commit("NOTES.md", "notes\n")
	out := v.ctlPane("w-other:p2", false, "verify", v.taskID, "--candidate", sha, "--execute")
	if !strings.Contains(asString(out["error"]), "not the registered coordinator") {
		t.Fatalf("non-coordinator error %q", out["error"])
	}
}

func TestVerifyExecute_unknownSHARefusesAndLeavesNoVerificationDir(t *testing.T) {
	v := newVerifyLab(t)
	out := v.ctl(false, "verify", v.taskID, "--candidate", strings.Repeat("f", 40), "--execute")
	if !strings.Contains(asString(out["error"]), "is not a commit in the task repository") {
		t.Fatalf("unknown sha error %q", out["error"])
	}
	if _, err := os.Stat(filepath.Join(v.home, "tasks", v.taskID, "verification")); !os.IsNotExist(err) {
		t.Fatalf("verification dir exists: %v", err)
	}
}

func TestVerifyExecute_runCheckIsRefusedAndRealRunJSONStillWorks(t *testing.T) {
	v := newVerifyLab(t)
	sha := v.commit("NOTES.md", "notes\n")
	cmd := exec.Command(mustLookPath(t, "python3"), filepath.Join(v.worktree, verifyRunner), "--json", "--check")
	cmd.Dir = v.worktree
	cmd.Env = v.env
	checkOut, err := cmd.Output()
	if err != nil {
		stderr := []byte{}
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("check run: %v\n%s%s", err, checkOut, stderr)
	}
	checkPath := filepath.Join(v.base, "check.json")
	if err := os.WriteFile(checkPath, checkOut, 0o644); err != nil {
		t.Fatal(err)
	}
	refused := v.ctl(false, "verify", v.taskID, "--candidate", sha, "--run", checkPath)
	if !strings.Contains(asString(refused["error"]), "--check record") {
		t.Fatalf("check error %q", refused["error"])
	}
	worker := v.workerRun()
	imported := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--run", asString(worker["_path"]))
	ev := v.evidence(imported)
	if asString(ev["run_id"]) != asString(worker["run_id"]) {
		t.Fatalf("run_id %q worker %q", ev["run_id"], worker["run_id"])
	}
	if asString(ev["isolation"]) != "task-checkout" {
		t.Fatalf("isolation %v", ev["isolation"])
	}
	if asString(ev["result"]) != "pass" {
		t.Fatalf("result %v", ev["result"])
	}
}

func TestVerifyExecute_graphIndexLivesUnderVerificationAndLeavesWithCheckout(t *testing.T) {
	v := newVerifyLab(t)
	metaPath := filepath.Join(v.worktree, ".codegraph", "meta.json")
	workerMeta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	sha := v.commit("NOTES.md", "notes\n")
	out := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute")
	ev := v.evidence(out)
	if asString(ev["result"]) != "pass" || asString(ev["isolation"]) != "separate-checkout" {
		t.Fatalf("evidence %v", ev)
	}
	graph := asMap(ev["graph"])
	if asString(graph["state"]) != "ready" {
		t.Fatalf("graph.state %v", graph["state"])
	}
	indexPath := asString(graph["index_path"])
	marker := filepath.Join("tasks", v.taskID, "verification")
	if !strings.Contains(indexPath, marker) {
		t.Fatalf("index_path %q want under %s", indexPath, marker)
	}
	if _, err := os.Stat(asString(ev["root"])); !os.IsNotExist(err) {
		t.Fatalf("verification checkout still present: %v", err)
	}
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("index still present at %s: %v", indexPath, err)
	}
	gotMeta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMeta) != string(workerMeta) {
		t.Fatalf("worker index changed\nbefore %s\nafter %s", workerMeta, gotMeta)
	}
}
