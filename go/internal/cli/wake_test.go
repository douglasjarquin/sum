package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/spf13/pflag"
)

// `sumctl wake show|consume|reconcile` (#240a, U6) and the decision priority path (#240b, U7).

func TestWakeCommandsUsageAndAuthority(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "wake missing subcommand", args: []string{"wake"}, usage: true},
		{name: "show extra positional", args: []string{"wake", "show", "extra"}, usage: true},
		{name: "show unknown flag", args: []string{"wake", "show", "--unexpected"}, usage: true, unknown: true},
		{name: "show missing --recipient value", args: []string{"wake", "show", "--recipient"}, usage: true},
		{name: "consume without --boundary", args: []string{"wake", "consume"}, usage: true},
		{name: "consume extra positional", args: []string{"wake", "consume", "--boundary", "x", "extra"}, usage: true},
		{name: "consume unknown flag", args: []string{"wake", "consume", "--boundary", "x", "--unexpected"}, usage: true, unknown: true},
		{name: "reconcile extra positional", args: []string{"wake", "reconcile", "extra"}, usage: true},
		{name: "reconcile unknown flag", args: []string{"wake", "reconcile", "--unexpected"}, usage: true, unknown: true},
		{name: "consume needs a Herdr pane", args: []string{"wake", "consume", "--boundary", "x"}, domainErr: "Herdr pane"},
		{name: "reconcile needs a Herdr pane", args: []string{"wake", "reconcile"}, domainErr: "Herdr pane"},
		{name: "consume from a non-coordinator pane", args: []string{"wake", "consume", "--boundary", "x"}, herdr: true, domainErr: "boundary token is not verifiable"},
		{name: "reconcile from a non-coordinator pane", args: []string{"wake", "reconcile"}, herdr: true, domainErr: "registered coordinator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			if tc.herdr {
				herdrEnv(t, home)
				t.Setenv("HERDR_PANE_ID", "w-other:p1")
			} else {
				clearHerdrEnv(t)
			}
			before := snapshotHome(t, home)
			stdout, err := runCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected a failure, stdout=%s", stdout)
			}
			msg := err.Error()
			switch {
			case tc.usage:
				usage := strings.Contains(msg, "unknown flag") || strings.Contains(msg, "accepts 0 arg") || strings.Contains(msg, "flag needs an argument") ||
					strings.Contains(msg, "required flag") || strings.Contains(msg, "command is required") || strings.Contains(msg, "unrecognized arguments") || strings.Contains(msg, "unknown command")
				if !usage {
					t.Fatalf("err = %v, want a usage failure", err)
				}
				if tc.unknown && !strings.Contains(msg, "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
			default:
				if !strings.Contains(msg, tc.domainErr) {
					t.Fatalf("err = %v, want %q", err, tc.domainErr)
				}
			}
			if snapshotHome(t, home) != before {
				t.Fatal("a refused wake command wrote under the state home")
			}
		})
	}
}

func TestWakeHelpMatchesImplementedCommands(t *testing.T) {
	for _, topic := range []string{"wake", "wake-show", "wake-consume", "wake-reconcile"} {
		assertStdoutGolden(t, t.TempDir(), []string{"help", topic}, "help-"+topic)
	}
	var out, errOut bytes.Buffer
	root := NewRoot("sumctl", &out, &errOut)
	for _, sub := range []string{"show", "consume", "reconcile"} {
		cmd, _, err := root.Find([]string{"wake", sub})
		if err != nil || cmd == nil || cmd.Name() != sub {
			t.Fatalf("command wake %s: %v", sub, err)
		}
		view, err := helpview.View("", "wake-"+sub)
		if err != nil {
			t.Fatal(err)
		}
		listed := map[string]bool{}
		rawArgs, _ := view.Get("arguments")
		for _, raw := range rawArgs.([]any) {
			name, _ := raw.(*ordjson.Object).Get("name")
			listed[name.(string)] = true
		}
		cmd.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden || flag.Name == "format" || flag.Name == "help" {
				return
			}
			if !listed["--"+flag.Name] {
				t.Errorf("help catalog topic wake-%s omits registered flag --%s", sub, flag.Name)
			}
		})
	}
}

// The three commands end to end in a coordinator home with native event delivery disabled: a routine arrival with no
// hook event stays inspectable (inbox, wake show) and nothing wakes anyone until an explicit pass; a worker-side
// notice opens the episode; `wake show` is write-free and hands out the boundary; consume records exactly it, repeats
// idempotently, and reconcile then has nothing to do.
func TestWakeCommandsEndToEndWithTheHookDisabled(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	owner := readJSON(t, filepath.Join(home, "context.json"))
	if fmt.Sprint(owner["wake_protocol"]) != "1" {
		t.Fatalf("init did not adopt the wake protocol: %v", owner)
	}
	writeHookHealth(t, home, `{"schema": 1, "enabled": false, "plugin_id": null, "events": 0, "handled": 0, "ignored": 0, "errors": [], "last_event": null, "last_error": null}`)
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	// The arrival: a worker's open question, saved with no hook event to carry it.
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", host, host, home))

	empty := decodeObject(t, mustCLI(t, home, "wake", "show"))
	if rows := asSlice(empty["recipients"]); len(rows) != 0 {
		t.Fatalf("wake show before any episode = %v, want no sidecar", rows)
	}
	inbox := decodeObject(t, mustCLI(t, home, "inbox"))
	if !strings.Contains(fmt.Sprint(inbox), "t-aaaaaaaaaaaa") {
		t.Fatalf("inbox does not list the arrival: %v", inbox)
	}
	hook := decodeObject(t, mustCLI(t, home, "hook", "status"))
	if fmt.Sprint(hook["enabled"]) != "false" || hook["degraded"] != true {
		t.Fatalf("hook status = %v, want disabled", hook)
	}

	// The explicit route from the worker's side: one bounded pass that opens the episode. No timer ran.
	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	notice := decodeObject(t, mustCLI(t, home, "notice", "t-aaaaaaaaaaaa", "--to", "parent"))
	rows := asSlice(asMap(notice["returns"])["recipients"])
	if len(rows) != 1 || asString(asMap(rows[0])["state"]) != "submitted" {
		t.Fatalf("notice = %v, want one submitted prompt", notice)
	}
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")

	before := snapshotHome(t, home)
	shown := decodeObject(t, mustCLI(t, home, "wake", "show"))
	if snapshotHome(t, home) != before {
		t.Fatal("wake show wrote under the state home")
	}
	rows = asSlice(shown["recipients"])
	if len(rows) != 1 {
		t.Fatalf("wake show = %v", shown)
	}
	entry := asMap(rows[0])
	if entry["outstanding"] != true || asString(asMap(entry["episode"])["phase"]) != "submitted" || asString(entry["boundary"]) == "" {
		t.Fatalf("entry = %v", entry)
	}
	included := asSlice(entry["included"])
	if len(included) != 1 || asString(asMap(included[0])["task"]) != "t-aaaaaaaaaaaa" || asString(asMap(included[0])["id"]) != "question:q-aaaa" {
		t.Fatalf("included = %v", included)
	}
	if len(asSlice(entry["uncoalesced_legacy_prompts"])) != 0 {
		t.Fatalf("an episode's own prompt was reported as uncoalesced: %v", entry["uncoalesced_legacy_prompts"])
	}
	key := asString(entry["key"])
	filtered := decodeObject(t, mustCLI(t, home, "wake", "show", "--recipient", key))
	if len(asSlice(filtered["recipients"])) != 1 {
		t.Fatalf("wake show --recipient %s = %v", key, filtered)
	}
	if none := decodeObject(t, mustCLI(t, home, "wake", "show", "--recipient", "nosuchkey")); len(asSlice(none["recipients"])) != 0 {
		t.Fatalf("unknown recipient key listed something: %v", none)
	}

	token := asString(entry["boundary"])
	consumed := decodeObject(t, mustCLI(t, home, "wake", "consume", "--boundary", token))
	if asString(consumed["result"]) != "consumed" || fmt.Sprint(consumed["covered_count"]) != "1" {
		t.Fatalf("consume = %v", consumed)
	}
	// The question is still open and still the user's to answer: consumption answered nothing.
	task := readJSON(t, filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"))
	if asString(asMap(asSlice(task["questions"])[0])["status"]) != "open" {
		t.Fatalf("consume changed the question: %v", task["questions"])
	}
	afterConsume := snapshotHome(t, home)
	repeated := decodeObject(t, mustCLI(t, home, "wake", "consume", "--boundary", token))
	if asString(repeated["result"]) != "repeated" || snapshotHome(t, home) != afterConsume {
		t.Fatalf("repeat = %v (wrote: %v)", repeated, snapshotHome(t, home) != afterConsume)
	}
	reconciled := decodeObject(t, mustCLI(t, home, "wake", "reconcile"))
	if asString(reconciled["action"]) != "none" || asString(reconciled["after"]) != "consumed" {
		t.Fatalf("reconcile after consume = %v", reconciled)
	}
	byKey := decodeObject(t, mustCLI(t, home, "wake", "reconcile", "--recipient", key))
	if asString(byKey["action"]) != "none" {
		t.Fatalf("reconcile --recipient = %v", byKey)
	}
	if bad, err := runCLI(t, home, "wake", "reconcile", "--recipient", "../escape"); err == nil || !strings.Contains(err.Error(), "sidecar key") {
		t.Fatalf("reconcile with a path as key = %v %v", bad, err)
	}
	shownAfter := decodeObject(t, mustCLI(t, home, "wake", "show"))
	entryAfter := asMap(asSlice(shownAfter["recipients"])[0])
	if entryAfter["outstanding"] != false || fmt.Sprint(entryAfter["receipts_count"]) != "1" || asString(entryAfter["boundary"]) != "" {
		t.Fatalf("entry after consume = %v", entryAfter)
	}
}

// A second question behind the submitted routine episode reaches the coordinator once through a priority prompt
// (#240b): the notice row says so, the routine episode is untouched, and `wake show` accounts for the delivery under
// priority_prompts.
func TestWakePriorityDecisionEndToEnd(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", host, host, home))
	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	routine := decodeObject(t, mustCLI(t, home, "notice", "t-aaaaaaaaaaaa", "--to", "parent"))
	rows := asSlice(asMap(routine["returns"])["recipients"])
	if len(rows) != 1 || asString(asMap(rows[0])["state"]) != "submitted" || asMap(rows[0])["priority"] != nil {
		t.Fatalf("routine notice = %v", routine)
	}
	// The fake marks a prompted pane working; the coordinator finishes its turn before the next arrival. The
	// worker's next question is saved and its own routine (not forced) pass finds the episode outstanding.
	setFakePaneStatus(t, home, "w-parent:p1", "idle")
	asked := decodeObject(t, mustCLI(t, home, "ask", "t-aaaaaaaaaaaa", "--key", "second", "--text", "Which branch?"))
	qid := asString(asMap(asked["question"])["id"])
	rows = asSlice(asMap(asMap(asked["notice"])["returns"])["recipients"])
	if len(rows) != 1 {
		t.Fatalf("ask notice = %v", asked)
	}
	entry := asMap(rows[0])
	if asString(entry["state"]) != "submitted" || entry["priority"] != true || asString(asMap(entry["wake"])["admission"]) != "coalesced" {
		t.Fatalf("priority row = %v", entry)
	}
	decisions := asSlice(entry["decisions"])
	if len(decisions) != 1 || asString(asMap(decisions[0])["id"]) != "question:"+qid || asString(asMap(decisions[0])["revision"]) != "second" {
		t.Fatalf("decisions = %v", decisions)
	}
	if coalesced := asSlice(entry["coalesced_obligations"]); len(coalesced) != 1 || asString(asMap(coalesced[0])["id"]) != "question:q-aaaa" {
		t.Fatalf("coalesced_obligations = %v", entry["coalesced_obligations"])
	}
	last := fakeLastPrompt(t, home, "w-parent:p1")
	if sent := fakePrompts(t, home); len(sent) != 2 || !strings.Contains(last, "Decision(s) need you") || strings.Contains(last, "q-aaaa") {
		t.Fatalf("prompts = %v, last = %q; want one priority prompt naming only the new question", sent, last)
	}
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")
	shown := decodeObject(t, mustCLI(t, home, "wake", "show"))
	wake := asMap(asSlice(shown["recipients"])[0])
	if asString(asMap(wake["episode"])["phase"]) != "submitted" || fmt.Sprint(wake["generation"]) != "1" {
		t.Fatalf("the priority prompt changed the routine episode: %v", wake["episode"])
	}
	// Both decisions are recorded: the first was named by the routine episode, the second by the priority prompt.
	recorded := map[string]map[string]any{}
	for _, raw := range asSlice(wake["priority_prompts"]) {
		recorded[asString(asMap(raw)["id"])] = asMap(raw)
	}
	second := recorded["question:"+qid]
	if len(recorded) != 2 || second == nil || asString(second["state"]) != "submitted" || asString(second["delivery"]) != asString(entry["delivery"]) || asString(second["revision"]) != "second" {
		t.Fatalf("priority_prompts = %v", wake["priority_prompts"])
	}
	if len(asSlice(wake["uncoalesced_legacy_prompts"])) != 0 {
		t.Fatalf("the priority prompt was reported as uncoalesced: %v", wake["uncoalesced_legacy_prompts"])
	}
	if included := asSlice(wake["included"]); len(included) != 2 {
		t.Fatalf("included = %v, want both open questions still owed", included)
	}
}

// fakeLastPrompt is the text the fake Herdr last accepted for pane.
func fakeLastPrompt(t *testing.T, home, pane string) string {
	t.Helper()
	var state map[string]any
	data, err := os.ReadFile(filepath.Join(home, "fake-herdr", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return asString(asMap(asMap(state["panes"])[pane])["last_prompt"])
}

// setFakePaneStatus edits the fake Herdr's store as a settled or busy occupant would appear.
func setFakePaneStatus(t *testing.T, home, pane, status string) {
	t.Helper()
	path := filepath.Join(home, "fake-herdr", "state.json")
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

func mustCLI(t *testing.T, home string, args ...string) string {
	t.Helper()
	out, err := runCLI(t, home, args...)
	if err != nil {
		t.Fatalf("sumctl %v: %v\n%s", args, err, out)
	}
	return out
}

// snapshotHome fingerprints every file under home so a read can be proven write-free.
func snapshotHome(t *testing.T, home string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || strings.HasPrefix(strings.TrimPrefix(path, home), string(os.PathSeparator)+"fake-herdr") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %x\n", path, info.Size(), raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
