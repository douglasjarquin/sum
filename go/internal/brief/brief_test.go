package brief

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

const procedureBody = "# sum-worker\n\nPROCEDURE-BODY-MARKER: follow the approved task.\n"

type lab struct {
	s       *store.Store
	runtime string
	sumctl  string
	task    *ordjson.Object
	taskDir string
}

func newLab(t *testing.T, withProcedure bool) *lab {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state home")
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(t.TempDir(), "run time")
	if withProcedure {
		writeProcedure(t, runtime, procedureBody)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("brief", "do the thing")
	task.Set("repository", "owner/repo")
	task.Set("worktree", "/tmp/work tree")
	task.Set("base_sha", "0123456789abcdef0123456789abcdef01234567")
	task.Set("branch", "sum-dev/x")
	task.Set("kind", "ship")
	task.Set("harness", "codex")
	task.Set("questions", []any{})
	taskDir, err := s.TaskPath("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return &lab{s: s, runtime: runtime, sumctl: filepath.Join(runtime, "bin", "sumctl"), task: task, taskDir: taskDir}
}

// writeProcedure writes the required core with body and each on-demand source with a fixed body.
func writeProcedure(t *testing.T, runtime, body string) {
	t.Helper()
	for _, src := range procedure.Sources[1:] {
		extra := filepath.Join(runtime, filepath.FromSlash(src.Path))
		if err := os.MkdirAll(filepath.Dir(extra), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(extra, []byte("# "+src.Name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(runtime, "skills", "sum-worker", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (l *lab) writeInitial(t *testing.T) string {
	t.Helper()
	path, err := WriteInitial(l.s, l.runtime, l.sumctl, l.task)
	if err != nil {
		t.Fatal(err)
	}
	l.task.Set("brief_path", path)
	if err := l.s.SaveTask(l.task); err != nil {
		t.Fatal(err)
	}
	return readText(t, path)
}

func (l *lab) setQuestions(t *testing.T, questions ...[4]string) {
	t.Helper()
	var rows []any
	for _, q := range questions {
		row := ordjson.NewObject()
		row.Set("id", q[0])
		row.Set("key", q[1])
		row.Set("status", q[2])
		if q[3] != "" {
			row.Set("answer", q[3])
		} else {
			row.Set("answer", nil)
		}
		rows = append(rows, row)
	}
	l.task.Set("questions", rows)
	if err := l.s.SaveTask(l.task); err != nil {
		t.Fatal(err)
	}
}

func (l *lab) regenerate(t *testing.T) (string, *ordjson.Object) {
	t.Helper()
	view, err := Regenerate(l.s, l.runtime, l.sumctl, "t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	rev, _ := view.Get("revision")
	path, _ := rev.(*ordjson.Object).Get("path")
	return readText(t, path.(string)), view
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var headingPattern = regexp.MustCompile(`(?m)^#{1,2} .*$`)

func headings(text string) []string {
	return headingPattern.FindAllString(text, -1)
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func TestInitialAndRegeneratedBriefsShareOneTemplate(t *testing.T) {
	l := newLab(t, true)
	initial := l.writeInitial(t)
	l.setQuestions(t,
		[4]string{"q-open000001", "scope", "open", ""},
		[4]string{"q-answer0001", "punctuation", "answered", "Keep it."},
		[4]string{"q-closed0001", "later", "closed-unapplied", "No longer needed."},
		[4]string{"q-applied001", "naming", "applied", "Use greet."},
	)
	regenerated, view := l.regenerate(t)

	if !reflect.DeepEqual(headings(initial), headings(regenerated)) {
		t.Fatalf("section headings differ:\ninitial     %v\nregenerated %v", headings(initial), headings(regenerated))
	}
	want := []string{"# sum worker brief — t-aaaaaaaaaaaa", "## Approved task", "## Execution contract", "## Verification contract", "## Code graph", "## Delivered runtime", "## Brief revision", "## Recorded decisions", "## Return channel", "## Worker procedure"}
	if !reflect.DeepEqual(headings(initial), want) {
		t.Fatalf("headings = %v, want %v", headings(initial), want)
	}

	// Legitimate differences are data: revision id, change summary, and decisions.
	if !strings.Contains(initial, "- Revision: `r1`") || strings.Contains(initial, "Changes since") || !strings.Contains(initial, "No decisions recorded yet.") {
		t.Fatalf("initial brief revision or decisions wrong:\n%s", initial)
	}
	if !strings.Contains(regenerated, "- Revision: `r2`") || !strings.Contains(regenerated, "- Changes since `r1`: decision q-open000001 recorded (open)") {
		t.Fatalf("regenerated brief lacks its revision or change summary:\n%s", regenerated)
	}
	for _, line := range []string{
		"- `q-open000001` (scope): open; no decision recorded yet.",
		"- `q-answer0001` (punctuation): answered, not yet applied: Keep it.",
		"- `q-closed0001` (later): closed by the coordinator, never applied: No longer needed.",
		"- `q-applied001` (naming): applied: Use greet.",
	} {
		if !strings.Contains(regenerated, line) {
			t.Fatalf("regenerated brief lacks %q:\n%s", line, regenerated)
		}
	}
	if duplicate, _ := view.Get("duplicate"); duplicate != false {
		t.Fatalf("regenerate duplicate = %v", duplicate)
	}

	// Outside the revision and decision sections the two briefs are identical text.
	strip := func(text string) string {
		text = regexp.MustCompile("(?s)## Brief revision.*?## Return channel").ReplaceAllString(text, "")
		return text
	}
	if strip(initial) != strip(regenerated) {
		t.Fatalf("briefs differ outside revision data:\n--- initial\n%s\n--- regenerated\n%s", strip(initial), strip(regenerated))
	}
}

func TestBriefReferencesPinnedProcedureInsteadOfCopyingIt(t *testing.T) {
	l := newLab(t, true)
	initial := l.writeInitial(t)
	l.setQuestions(t, [4]string{"q-answer0001", "punctuation", "answered", "Keep it."})
	regenerated, _ := l.regenerate(t)

	sha := sha256Hex(procedureBody)
	pinned := filepath.Join(l.taskDir, "procedure", "sum-worker-"+sha[:16]+".md")
	if readText(t, pinned) != procedureBody {
		t.Fatalf("pinned procedure content differs")
	}
	for name, text := range map[string]string{"initial": initial, "regenerated": regenerated} {
		if strings.Contains(text, "PROCEDURE-BODY-MARKER") {
			t.Fatalf("%s brief embeds the procedure body", name)
		}
		ref := "- Required before any other step: `" + pinned + "` (`sum-worker`, " + strconv.Itoa(len(procedureBody)) + " bytes, sha256 `" + sha + "`)."
		if !strings.Contains(text, ref) {
			t.Fatalf("%s brief lacks %q:\n%s", name, ref, text)
		}
		if strings.Contains(text, filepath.Join(l.runtime, "skills")) {
			t.Fatalf("%s brief points at the mutable runtime copy:\n%s", name, text)
		}
		if len(text) > 12000 {
			t.Fatalf("%s brief is %d bytes; the procedure should not ride along", name, len(text))
		}
	}
	versions := readText(t, filepath.Join(l.taskDir, "versions.json"))
	if !strings.Contains(versions, `"path": "procedure/sum-worker-`+sha[:16]+`.md"`) {
		t.Fatalf("versions.json does not record the task-relative procedure path:\n%s", versions)
	}
}

func TestReturnCommandsSurviveShellParsingWithSpaces(t *testing.T) {
	l := newLab(t, true)
	text := l.writeInitial(t)
	blocks := regexp.MustCompile("(?s)```sh\n(.*?)\n```").FindAllStringSubmatch(text, -1)
	if len(blocks) != 5 {
		t.Fatalf("return channel blocks = %d, want ask, context, show, resolve, report", len(blocks))
	}
	contextIndex := strings.Index(text, blocks[1][1])
	showIndex := strings.Index(text, blocks[2][1])
	if !strings.Contains(blocks[1][1], " context t-aaaaaaaaaaaa --role worker") || contextIndex > showIndex {
		t.Fatalf("bounded context is not the first read: %v", blocks)
	}
	for _, block := range blocks {
		out, err := exec.Command("sh", "-c", "set -- "+block[1]+"; printf '%s\\n' \"$@\"").Output()
		if err != nil {
			t.Fatalf("shell cannot parse %q: %v", block[1], err)
		}
		argv := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if argv[0] != l.sumctl || argv[1] != "--home" || argv[2] != l.s.Home {
			t.Fatalf("parsed %q as %q", block[1], argv)
		}
	}
}

func TestMissingProcedurePublishesNothing(t *testing.T) {
	l := newLab(t, false)
	if _, err := WriteInitial(l.s, l.runtime, l.sumctl, l.task); err == nil || !strings.Contains(err.Error(), "skills/sum-worker/SKILL.md is missing") || !strings.Contains(err.Error(), "no brief was written") {
		t.Fatalf("WriteInitial without procedure = %v", err)
	}
	for _, name := range []string{"brief.md", "versions.json", "procedure"} {
		if _, err := os.Lstat(filepath.Join(l.taskDir, name)); err == nil {
			t.Fatalf("refused WriteInitial left %s", name)
		}
	}

	writeProcedure(t, l.runtime, procedureBody)
	l.writeInitial(t)
	if err := os.Remove(filepath.Join(l.runtime, "skills", "sum-worker", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	l.setQuestions(t, [4]string{"q-answer0001", "k", "answered", "Yes."})
	if _, err := Regenerate(l.s, l.runtime, l.sumctl, "t-aaaaaaaaaaaa"); err == nil || !strings.Contains(err.Error(), "no brief was written") {
		t.Fatalf("Regenerate without procedure = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(l.taskDir, "briefs")); err == nil {
		t.Fatal("refused Regenerate wrote a revision")
	}
}

func TestLeftoverWorkerAliasIsNotAProcedure(t *testing.T) {
	l := newLab(t, false)
	leftover := filepath.Join(l.runtime, "skills", "worker", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("# leftover worker alias\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteInitial(l.s, l.runtime, l.sumctl, l.task); err == nil {
		t.Fatal("WriteInitial accepted the leftover skills/worker alias as the procedure")
	}
}

func TestProcedureChangeIsSummarizedAndVerificationAffecting(t *testing.T) {
	l := newLab(t, true)
	l.writeInitial(t)
	writeProcedure(t, l.runtime, procedureBody+"Update two.\n")
	text, view := l.regenerate(t)
	rev, _ := view.Get("revision")
	affected, _ := rev.(*ordjson.Object).Get("verification_affected")
	if affected != true || !strings.Contains(text, "worker procedure changed: ") {
		t.Fatalf("procedure change not summarized (affected=%v):\n%s", affected, text)
	}
	old := filepath.Join(l.taskDir, "procedure", "sum-worker-"+sha256Hex(procedureBody)[:16]+".md")
	if readText(t, old) != procedureBody {
		t.Fatal("the earlier pinned procedure changed")
	}
}

// A revision pinned before the procedure split (one required row) stays valid, and moving that task to the
// split procedure is an explicit, verification-affecting revision the worker must request and adopt.
func TestSplittingTheProcedureIsAnExplicitRevisionAndKeepsEarlierRows(t *testing.T) {
	l := newLab(t, true)
	saved := procedure.Sources
	procedure.Sources = saved[:1]
	l.writeInitial(t)
	procedure.Sources = saved
	versionsObj, err := versions.ReadVersions(l.s, l.task)
	if err != nil {
		t.Fatal(err)
	}
	r1 := versions.ActiveRevision(versionsObj)
	r1Policy, _ := r1.Get("policy")
	r1Rows := procedure.Rows(r1Policy.(*ordjson.Object))
	if len(r1Rows) != 1 || procedure.Verify(l.taskDir, r1Rows) != nil {
		t.Fatalf("pre-split rows = %v", r1Rows)
	}

	text, view := l.regenerate(t)
	rev, _ := view.Get("revision")
	affected, _ := rev.(*ordjson.Object).Get("verification_affected")
	if affected != true || !strings.Contains(text, "worker procedure resources changed") {
		t.Fatalf("procedure split not summarized (affected=%v):\n%s", affected, text)
	}
	if strings.Count(text, "- Read when ") != len(procedure.Sources)-1 || strings.Count(text, "- Required before any other step: ") != 1 {
		t.Fatalf("regenerated brief does not list one required and the on-demand files:\n%s", text)
	}
	versionsObj, _ = versions.ReadVersions(l.s, l.task)
	if active := versions.ActiveRevision(versionsObj); active == nil || fmt.Sprint(func() any { v, _ := active.Get("id"); return v }()) != "r1" {
		t.Fatalf("regenerate changed the active revision: %v", active)
	}
	if err := procedure.Verify(l.taskDir, r1Rows); err != nil {
		t.Fatalf("pre-split rows after the split: %v", err)
	}
}
