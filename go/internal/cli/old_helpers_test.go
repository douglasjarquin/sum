package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// supportedFloor is the oldest runtime a current updater accepts as a release target: the first tree on main with
// go/cmd/sumctl/main.go, which release.VerifyRelease requires. The pre-identity check below keeps it relevant: records
// that never gained the stable machine identity can still roll back this far.
const supportedFloor = "11fc3d9"

// TestOlderHelpersOnCandidateRecords runs older sum runtimes against records this candidate wrote, which no longer
// carry a persisted `notice` mirror. By default it builds the supported rollback floor and the merge base with
// origin/main from this checkout's git history (CI checks out full history); it skips, saying why, when that history
// is unavailable. SUM_OLD_HELPERS=name=/path/to/tree[,...] replaces those with prebuilt trees (each with its own
// .local/bin/sumctl), for example to add the machine-identity change 4eb8591.
//
// Two histories per helper. "upgraded": the older helper creates the installation and dispatches (its records),
// the candidate then takes the worker's question, and the installation rolls back to the older helper. "candidate":
// the candidate creates everything, so the records carry the stable machine identity; a helper that predates it is
// refused by the updater there (#213) and is skipped with that reason.
//
// Each older helper must read, ask, report, and answer on those records; it decides delivery from the returns
// sidecar, so a return the candidate already submitted is never treated as pending again, and the candidate's view
// of what the older helper then recorded agrees with it.
func TestOlderHelpersOnCandidateRecords(t *testing.T) {
	trees := olderTrees(t)
	for _, name := range sortedKeys(trees) {
		tree := trees[name]
		helper := filepath.Join(tree, "bin", "sumctl")
		// The updater's own evidence that a release tree resolves the stable machine identity (updatecmd.identityMarker).
		_, markerErr := os.Stat(filepath.Join(tree, "go", "internal", "machine", "machine.go"))
		stable := markerErr == nil
		t.Run(name+"/upgraded", func(t *testing.T) { olderHelperOnCandidateRecords(t, helper, true, stable) })
		t.Run(name+"/candidate", func(t *testing.T) {
			if !stable {
				t.Skip("predates the stable machine identity; the updater refuses it once records carry m- ids (#213)")
			}
			olderHelperOnCandidateRecords(t, helper, false, stable)
		})
	}
}

// olderTrees names the older runtime trees to run: SUM_OLD_HELPERS when set, otherwise the supported floor and the
// merge base built from git history into a temporary directory.
func olderTrees(t *testing.T) map[string]string {
	t.Helper()
	trees := map[string]string{}
	if spec := os.Getenv("SUM_OLD_HELPERS"); spec != "" {
		for _, entry := range strings.Split(spec, ",") {
			name, tree, ok := strings.Cut(strings.TrimSpace(entry), "=")
			if !ok {
				t.Fatalf("SUM_OLD_HELPERS entry %q is not name=path", entry)
			}
			trees[name] = tree
		}
		return trees
	}
	root, _ := repoReference(t)
	revs := map[string]string{"floor-" + supportedFloor: supportedFloor}
	if out, err := exec.Command("git", "-C", root, "merge-base", "HEAD", "origin/main").Output(); err == nil {
		base := strings.TrimSpace(string(out))
		if head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output(); err == nil && strings.TrimSpace(string(head)) != base {
			revs["base-"+base[:7]] = base
		}
	}
	goBin := pinnedGo(t)
	for name, rev := range revs {
		if err := exec.Command("git", "-C", root, "cat-file", "-e", rev+"^{commit}").Run(); err != nil {
			t.Skipf("git history lacks %s (shallow checkout?); set SUM_OLD_HELPERS to run older helpers against candidate records", rev)
		}
		tree := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(tree, 0o755); err != nil {
			t.Fatal(err)
		}
		archive := exec.Command("sh", "-c", `git -C "$1" archive "$2" | tar -x -C "$3"`, "sh", root, rev, tree)
		if out, err := archive.CombinedOutput(); err != nil {
			t.Fatalf("extract %s: %v\n%s", rev, err, out)
		}
		build := exec.Command(goBin, "build", "-trimpath", "-buildvcs=false", "-o", filepath.Join(tree, ".local", "bin", "sumctl"), "./cmd/sumctl")
		build.Dir = filepath.Join(tree, "go")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", rev, err, out)
		}
		trees[name] = tree
	}
	return trees
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func olderHelperOnCandidateRecords(t *testing.T, oldHelper string, olderCreates, stableIdentity bool) {
	d := newOlderLab(t, oldHelper, olderCreates)
	old := func(pane string, args ...string) map[string]any {
		t.Helper()
		out, err := runOlder(d, oldHelper, pane, args...)
		if err != nil {
			t.Fatalf("older helper %v: %v\n%s", args, err, out)
		}
		var view map[string]any
		if err := json.Unmarshal(out, &view); err != nil {
			t.Fatalf("older helper %v: %v\n%s", args, err, out)
		}
		return view
	}
	repo := policyProject(t, d.base, "older-helper", map[string]string{"README.md": "A project.\n"})
	dispatch := d.ctl
	if olderCreates {
		dispatch = func(_ bool, args ...string) map[string]any { return old("", args...) }
	}
	task := dispatch(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	taskID, pane := asString(task["id"]), asString(task["pane"])
	if olderCreates {
		old(pane, "init")
	} else {
		d.ctlPane(pane, true, "init")
	}

	// The candidate delivers the worker's question and records it only in the returns sidecar.
	settleFakePane(t, d.base, "w-parent:p1")
	asked := d.ctlPane(pane, true, "ask", taskID, "--key", "choice", "--text", "Which way?")
	questionID := asString(asMap(asked["question"])["id"])
	if asString(asMap(asked["notice"])["status"]) != "submitted-not-acknowledged" {
		t.Fatalf("candidate ask notice = %v", asked["notice"])
	}
	parentPrompts := len(d.prompts(t, "w-parent:p1"))

	// Reads: the older helper shows the record's own field, which the candidate did not touch.
	var row map[string]any
	for _, item := range asSlice(old("", "status")["tasks"]) {
		if asString(asMap(item)["id"]) == taskID {
			row = asMap(item)
		}
	}
	t.Logf("older status notice for the question the candidate submitted: %v", row["notice"])
	old("", "show", taskID)
	duplicate := old(pane, "ask", taskID, "--key", "choice", "--text", "Which way?")
	if duplicate["duplicate"] != true {
		t.Fatalf("older duplicate ask = %v", duplicate)
	}
	t.Logf("older duplicate-ask notice: %v", duplicate["notice"])

	// A new record from the older helper: its pass reads the candidate's submitted question from the sidecar and
	// prompts the parent once, for the report.
	settleFakePane(t, d.base, "w-parent:p1")
	reported := old(pane, "report", taskID, "--text", "Candidate ready; not verified.")
	state := ""
	for _, recipient := range asSlice(asMap(asMap(reported["notice"])["returns"])["recipients"]) {
		for _, item := range asSlice(asMap(recipient)["obligations"]) {
			if obligation := asMap(item); asString(obligation["id"]) == "question:"+questionID {
				state = asString(asMap(obligation["notification"])["state"])
			}
		}
	}
	if !stableIdentity {
		// A runtime before #202 keys deliveries by the recorded hostname, so it does not recognize an attempt a
		// newer runtime recorded under the stable key. That comes from the delivery key, not the notice mirror: the
		// base with its mirror reads the same, and the mirror was never consulted for the decision.
		t.Logf("pre-identity runtime reads the candidate-submitted question as %q (delivery key, not the notice)", state)
	} else {
		if state != "submitted" {
			t.Fatalf("older helper read the candidate-submitted question as %q, want submitted", state)
		}
		if got := len(d.prompts(t, "w-parent:p1")); got != parentPrompts+1 {
			t.Fatalf("parent prompts = %d, want exactly one more (the report) after %d", got, parentPrompts)
		}
	}

	// The coordinator answers through the older helper; the candidate then derives what that helper recorded.
	settleFakePane(t, d.base, pane)
	answered := old("", "answer", taskID, questionID, "--text", "This way.")
	olderNotice := asMap(answered["notice"])
	derived := asMap(d.ctl(true, "show", taskID)["notice"])
	if asString(derived["status"]) != asString(olderNotice["status"]) || asString(derived["delivery"]) != asString(olderNotice["delivery"]) || asString(derived["recipient"]) != "worker" {
		t.Fatalf("candidate notice %v disagrees with the older helper's own %v", derived, olderNotice)
	}
	if asString(derived["status"]) != "submitted-not-acknowledged" {
		t.Fatalf("answer notice = %v, want submitted to the settled worker", derived)
	}
}

// newOlderLab is newPolicyLab with the coordinator initialized by the older helper when it creates the records.
func newOlderLab(t *testing.T, oldHelper string, olderCreates bool) *demoLab {
	t.Helper()
	if !olderCreates {
		return newPolicyLab(t)
	}
	root, helper := repoReference(t)
	base := t.TempDir()
	home := filepath.Join(base, "state")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &demoLab{t: t, root: root, helper: helper, home: home, base: base, env: demoEnv(t, root, base)}
	// The older helper records its own tree as the coordinator's checkout; the fake parent pane runs there.
	d.setEnv("FAKE_PARENT_CWD", filepath.Dir(filepath.Dir(oldHelper)))
	out, err := runOlder(d, oldHelper, "", "init")
	var view map[string]any
	if err != nil || json.Unmarshal(out, &view) != nil || asString(view["role"]) != "coordinator" {
		t.Fatalf("older init: %v\n%s", err, out)
	}
	return d
}

// runOlder runs an older helper tree as the lab installation's runtime; a helper from before `--format` prints JSON
// by default.
func runOlder(d *demoLab, helper, pane string, args ...string) ([]byte, error) {
	run := func(prefix ...string) ([]byte, error) {
		cmd := exec.Command(helper, append(append(prefix, "--home", d.home), args...)...)
		cmd.Dir = d.root
		// A rolled-back release still serves the same installation, as the release wrapper exports it.
		env := append(append([]string{}, d.env...), "SUM_INSTALL_ROOT="+d.root)
		if pane != "" {
			env = append(env, "HERDR_PANE_ID="+pane)
		}
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if len(bytes.TrimSpace(stdout.Bytes())) == 0 {
			return stderr.Bytes(), err
		}
		return stdout.Bytes(), err
	}
	out, err := run("--format", "json")
	if err != nil && (bytes.Contains(out, []byte("unknown flag: --format")) || bytes.Contains(out, []byte("invalid choice: 'json'"))) {
		return run()
	}
	return out, err
}
