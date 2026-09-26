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

	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
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
	candidateDelivery := asString(asMap(asked["notice"])["delivery"])

	// #240a: the wake sidecar is opaque to the older helper. A candidate-created home has an outstanding episode from
	// that ask; an older-created home is not adopted (its owner record predates the protocol), so the candidate
	// prompted legacy-uncoalesced and wrote no sidecar, and one is planted so the older helper meets the file either
	// way. The older helper must leave it byte-identical and leave the candidate's submitted attempt as it is.
	wakePath := candidateWakeSidecar(t, d, olderCreates, asked)
	wakeBefore := readFileString(t, wakePath)

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

	olderDelivery := ""
	for _, recipient := range asSlice(asMap(asMap(reported["notice"])["returns"])["recipients"]) {
		if asString(asMap(recipient)["state"]) == "submitted" {
			olderDelivery = asString(asMap(recipient)["delivery"])
		}
	}
	if readFileString(t, wakePath) != wakeBefore {
		t.Fatalf("the older helper changed the wake sidecar %s", wakePath)
	}
	if state := deliveryState(t, d.home, taskID, candidateDelivery); state != "submitted" {
		t.Fatalf("the candidate's submitted attempt %s reads %q after the older helper's pass, want submitted", candidateDelivery, state)
	}
	if !stableIdentity {
		t.Logf("pre-identity runtime recorded its own prompt under the hostname key; the candidate cannot attribute it to this recipient")
	} else {
		if olderDelivery == "" {
			t.Fatalf("older helper report recorded no submitted delivery: %v", reported["notice"])
		}
		entry := candidateWakeEntry(t, d)
		uncoalesced := asSlice(entry["uncoalesced_legacy_prompts"])
		found := false
		for _, item := range uncoalesced {
			if asString(asMap(item)["delivery"]) == olderDelivery {
				found = true
			}
		}
		if !found {
			t.Fatalf("candidate wake show does not report the older helper's prompt %s as uncoalesced: %v", olderDelivery, uncoalesced)
		}
		if asString(asMap(entry["episode"])["phase"]) != "submitted" {
			t.Fatalf("the episode changed under the older helper: %v", entry["episode"])
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

// candidateWakeSidecar returns the path of the coordinator's wake sidecar: the one the candidate's ask left (its row
// says so), or, in an older-created home that is not adopted, one planted with an outstanding episode.
func candidateWakeSidecar(t *testing.T, d *demoLab, olderCreates bool, asked map[string]any) string {
	t.Helper()
	st, err := store.Open(d.home)
	if err != nil {
		t.Fatal(err)
	}
	var wakeField any
	for _, recipient := range asSlice(asMap(asMap(asked["notice"])["returns"])["recipients"]) {
		wakeField = asMap(recipient)["wake"]
	}
	entries, err := returns.ListWakes(st)
	if err != nil {
		t.Fatal(err)
	}
	if !olderCreates {
		if len(entries) != 1 || entries[0].Wake == nil || !entries[0].Wake.IsOutstanding() {
			t.Fatalf("candidate ask left wake entries %v, want one outstanding episode (row wake %v)", entries, wakeField)
		}
		return entries[0].Path
	}
	if asString(wakeField) != returns.WakeLegacy || len(entries) != 0 {
		t.Fatalf("older-created home: row wake = %v, sidecars = %v; want legacy-uncoalesced and none", wakeField, entries)
	}
	owner, err := st.Owner()
	if err != nil || owner == nil {
		t.Fatalf("owner: %v %v", owner, err)
	}
	machineValue, _ := owner.Get("machine")
	w, err := returns.NewWake(st, [3]string{asString(machineValue), "sum-test", "w-parent:p1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Prepare("w-planted", []returns.WakeRef{{Task: "t-planted", ID: "report:r1"}})
	w.Episode.Phase = returns.WakeSubmitted
	w.Episode.Delivery = "d-planted"
	if err := returns.WriteWake(st, w); err != nil {
		t.Fatal(err)
	}
	return st.WakePath(w.Endpoint())
}

func candidateWakeEntry(t *testing.T, d *demoLab) map[string]any {
	t.Helper()
	rows := asSlice(d.ctl(true, "wake", "show")["recipients"])
	if len(rows) != 1 {
		t.Fatalf("candidate wake show = %v", rows)
	}
	return asMap(rows[0])
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// deliveryState reads one delivery's recorded state from the task's returns sidecar.
func deliveryState(t *testing.T, home, taskID, deliveryID string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "tasks", taskID, returns.File))
	if err != nil {
		t.Fatal(err)
	}
	var sidecar map[string]any
	if err := json.Unmarshal(raw, &sidecar); err != nil {
		t.Fatal(err)
	}
	for _, item := range asSlice(sidecar["deliveries"]) {
		if asString(asMap(item)["id"]) == deliveryID {
			return asString(asMap(item)["state"])
		}
	}
	return ""
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
