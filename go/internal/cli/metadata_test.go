package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
)

type metaLab struct {
	*demoLab
	task, pane, worktree string
}

func newMetaLab(t *testing.T) *metaLab {
	t.Helper()
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "native-metadata", map[string]string{"README": "base\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	address := workerAddress(task)
	d.ctlPane(address, true, "init")
	return &metaLab{demoLab: d, task: asString(task["id"]), pane: address, worktree: asString(task["worktree"])}
}
func (l *metaLab) run(args ...string) map[string]any { return l.ctl(true, args...) }
func (l *metaLab) enable() map[string]any            { return l.run("metadata", "enable") }
func (l *metaLab) meta() map[string]any {
	return readJSON(l.t, filepath.Join(l.home, "metadata", "state.json"))
}
func (l *metaLab) terminal(address string) hcclient.Terminal {
	l.t.Helper()
	for _, terminal := range l.native.Terminals() {
		if terminal.Address == address {
			return terminal
		}
	}
	l.t.Fatalf("missing terminal %s", address)
	return hcclient.Terminal{}
}
func (l *metaLab) labelCalls() []hctest.Call {
	var out []hctest.Call
	for _, call := range l.native.Calls() {
		if call.Method == "terminal.label" {
			out = append(out, call)
		}
	}
	return out
}

func designatedHomeAt(t *testing.T, home string) string {
	t.Helper()
	return nativeTestStore(t, home).Home
}
func readJSONFile(t *testing.T, path string) map[string]any { return readJSON(t, path) }
func snapshotTree(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func assertTreeUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("records-only command changed ledger files")
	}
}

func TestMetadataEnable_probesAndWritesOnlyChangedTokensWithInstanceSource(t *testing.T) {
	l := newMetaLab(t)
	terminal := l.terminal(l.pane)
	terminal.Labels["unrelated"] = "preserved"
	l.native.SetTerminal(terminal)
	l.enable()
	labels := l.terminal(l.pane).Labels
	if labels["sum.task"] != l.task || labels["sum.state"] == "" || labels["unrelated"] != "preserved" {
		t.Fatalf("native task labels: %v", labels)
	}
	before := len(l.labelCalls())
	l.run("metadata", "sync")
	if len(l.labelCalls()) != before {
		t.Fatal("unchanged sync wrote labels again")
	}
	for _, call := range l.labelCalls() {
		data, ok := call.Params["set"].(map[string]any)
		if !ok {
			continue
		}
		for key := range data {
			if !strings.HasPrefix(key, "sum.") {
				t.Fatalf("unowned label written: %s", key)
			}
		}
	}
}

func TestMetadataDisabled_afterAndSyncWriteNothing(t *testing.T) {
	l := newMetaLab(t)
	before := len(l.labelCalls())
	l.run("ask", l.task, "--text", "Which option?")
	if len(l.labelCalls()) != before {
		t.Fatal("disabled projection wrote labels")
	}
	if result := l.ctl(false, "metadata", "sync"); !strings.Contains(asString(result["error"]), "disabled") {
		t.Fatalf("disabled sync: %v", result)
	}
}

func TestMetadataAfter_projectsChangedFactsOnceAndReadsNever(t *testing.T) {
	l := newMetaLab(t)
	l.enable()
	before := len(l.labelCalls())
	l.run("ask", l.task, "--text", "Which option?")
	if len(l.labelCalls()) <= before {
		t.Fatal("task mutation did not project changed native labels")
	}
	before = len(l.labelCalls())
	for _, args := range [][]string{{"status"}, {"inbox"}, {"metadata", "status"}, {"show", l.task}, {"context", l.task, "--role", "coordinator"}} {
		l.run(args...)
	}
	if len(l.labelCalls()) != before {
		t.Fatal("a records-only read projected labels")
	}
}

func TestMetadataAfter_refusedWriteIsNotCachedAndDomainSucceeds(t *testing.T) {
	l := newMetaLab(t)
	l.enable()
	l.native.QueueFault("terminal.label", hctest.Fault{RefuseKind: "not_allowed"})
	question := l.run("ask", l.task, "--text", "A durable question despite display failure?")
	if asString(asMap(question["question"])["id"]) == "" {
		t.Fatalf("domain question lost: %v", question)
	}
	before := len(l.labelCalls())
	l.run("metadata", "sync")
	if len(l.labelCalls()) <= before {
		t.Fatal("failed native label write was cached as success")
	}
}

func TestMetadataPaneVerification_staleAbsentUnobservable(t *testing.T) {
	for _, kind := range []string{"replaced", "finished", "unobservable"} {
		t.Run(kind, func(t *testing.T) {
			l := newMetaLab(t)
			l.enable()
			terminal := l.terminal(l.pane)
			switch kind {
			case "replaced":
				terminal.CreatedMS++
			case "finished":
				terminal.State = "finished"
			case "unobservable":
				terminal.Foreground = nil
			}
			l.native.SetTerminal(terminal)
			before := len(l.labelCalls())
			l.run("metadata", "sync", "--force")
			for _, call := range l.labelCalls()[before:] {
				if call.Params["terminal"] == l.pane {
					t.Fatalf("%s terminal received label mutation", kind)
				}
			}
		})
	}
}

func TestMetadataTwoHomes_distinctSourcesAndOwnKeysOnly(t *testing.T) {
	first, second := newMetaLab(t), newMetaLab(t)
	first.enable()
	second.enable()
	firstLabels := first.terminal(first.pane).Labels
	second.run("metadata", "disable")
	if !reflect.DeepEqual(first.terminal(first.pane).Labels, firstLabels) {
		t.Fatal("second installation cleared first installation labels")
	}
	if first.meta()["source"] == second.meta()["source"] {
		t.Fatal("installation projection sources collide")
	}
}

func TestMetadataDisable_clearsRecordedKeysAndReportsFailedClears(t *testing.T) {
	l := newMetaLab(t)
	terminal := l.terminal(l.pane)
	terminal.Labels["unrelated"] = "preserved"
	l.native.SetTerminal(terminal)
	l.enable()
	result := l.run("metadata", "disable")
	if result["enabled"] != false {
		t.Fatalf("disable: %v", result)
	}
	labels := l.terminal(l.pane).Labels
	if labels["unrelated"] != "preserved" {
		t.Fatal("disable cleared unrelated label")
	}
	for _, key := range []string{"sum.task", "sum.state", "sum.repo", "sum.rev", "sum.pipeline", "sum.pr"} {
		if _, exists := labels[key]; exists {
			t.Fatalf("owned display label survived disable: %s", key)
		}
	}
}

func TestMetadataDisable_preservesFailedClearForRetry(t *testing.T) {
	l := newMetaLab(t)
	l.enable()
	l.native.QueueFault("terminal.label", hctest.Fault{RefuseKind: "not_allowed"})
	result := l.run("metadata", "disable")
	if len(asSlice(result["pending_clears"])) == 0 {
		t.Fatalf("failed clear erased retry record: %v", result)
	}
}

func TestMetadataInboxOpen_isRefusedBeforeNativeEffects(t *testing.T) {
	l := newMetaLab(t)
	before := snapshotTree(t, l.home)
	labels := len(l.labelCalls())
	runs := len(l.native.Terminals())
	result := l.ctl(false, "metadata", "inbox", "--open")
	if !strings.Contains(asString(result["error"]), "unknown flag") {
		t.Fatalf("native inbox: %v", result)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, l.home))
	if len(l.labelCalls()) != labels || len(l.native.Terminals()) != runs {
		t.Fatal("records-only inbox changed runtime")
	}
}

// Legacy callback transports no longer enter reconciliation. Native event scope
// and application-receipt reconciliation are proved by the observer socket tests.
func TestHookEventLegacyCallbackIsRefusedWithoutEffects(t *testing.T) {
	l := newMetaLab(t)
	before := snapshotTree(t, l.home)
	calls := len(l.native.Calls())
	result := l.ctl(false, "hook", "event", "--plugin-id", "legacy")
	if !strings.Contains(asString(result["error"]), "unknown") {
		t.Fatalf("legacy callback accepted: %v", result)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, l.home))
	if len(l.native.Calls()) != calls {
		t.Fatal("legacy callback contacted native core")
	}
}
