package inboxview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/presentation"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func fixture(t *testing.T) *store.Store {
	t.Helper()
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		if strings.HasPrefix(key, "SUM_") || strings.HasPrefix(key, "HERDR_") {
			t.Setenv(key, "")
		}
	}
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func write(t *testing.T, s *store.Store, path, data string) {
	t.Helper()
	path = filepath.Join(s.Home, path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func bytesOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			out[path] = string(data)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadRetainsHealthyDecisionsAndReportsSourceGaps(t *testing.T) {
	s := fixture(t)
	for _, name := range []string{"gh", "herdr", "herdr-mesh"} {
		write(t, s, "bin/"+name, "#!/bin/sh\nprintf called >> \"$INBOXVIEW_CALL_LOG\"\nexit 1\n")
		if err := os.Chmod(filepath.Join(s.Home, "bin", name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	callLog := filepath.Join(s.Home, "external-calls")
	t.Setenv("INBOXVIEW_CALL_LOG", callLog)
	t.Setenv("PATH", filepath.Join(s.Home, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"waiting","questions":[{"id":"q-open","status":"open","key":"choice","text":"routine update"},{"id":"q-answered","status":"answered"},{"id":"q-unknown","status":"unexpected"}]}`)
	write(t, s, "tasks/t-aaaaaaaaaaaa/versions.json", `{broken`)
	write(t, s, "tasks/t-bbbbbbbbbbbb/task.json", `{broken`)
	before := bytesOf(t, s.Home)
	got := Read(s)
	if len(got.Tasks) != 1 || got.Counts.Decisions != 1 || got.Counts.Worker != 1 || got.Counts.UnknownSources < 3 || got.Complete {
		t.Fatalf("partial snapshot lost facts: %+v", got)
	}
	if got.Tasks[0].Record == nil || len(got.Gaps) < 3 {
		t.Fatal("missing canonical record or source gaps")
	}
	if _, err := s.AllTasks(); err == nil {
		t.Fatal("mutation reader must remain fail-closed")
	}
	if !reflect.DeepEqual(before, bytesOf(t, s.Home)) {
		t.Fatal("read changed saved sources")
	}
	if !reflect.DeepEqual(got, Read(s)) {
		t.Fatal("unchanged sources produced different snapshot")
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("read invoked an external tool: %v", err)
	}
}

func TestReadUsesCanonicalObligationsAndQuestionStates(t *testing.T) {
	s := fixture(t)
	path := "tasks/t-aaaaaaaaaaaa/task.json"
	data := `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"reported","parent":{"pane":"parent"},"questions":[{"id":"q1","key":"one","status":"open"},{"id":"q2","status":"answered"},{"id":"q3","status":"applied"},{"id":"q4","status":"settled"},{"id":"q5","status":"closed-unapplied"}],"evidence":[{"id":"e1","kind":"report","at":"2026-09-25T00:00:00Z","candidate":"sha1"},{"id":"e2","kind":"review","at":"2026-09-25T00:01:00Z","candidate":"sha1","verdict":"reject"}]}`
	write(t, s, path, data)
	got := Read(s)
	counts := map[presentation.Kind]int{}
	for _, item := range got.Items {
		counts[item.Kind]++
	}
	if counts[presentation.Decision] != 1 || counts[presentation.Routine] != 3 || counts[presentation.Resolved] != 3 {
		t.Fatalf("classifications = %v", counts)
	}
	data = strings.Replace(data, `"verdict":"reject"}]`, `"verdict":"reject"},{"id":"e3","kind":"verification","source":"coordinator","candidate":"sha1","at":"2026-09-25T00:02:00Z"}]`, 1)
	write(t, s, path, data)
	for _, item := range Read(s).Items {
		if item.Source.Kind == presentation.Report || item.Source.Kind == presentation.Review {
			t.Fatalf("canonical closing evidence ignored: %+v", item)
		}
	}
}

func TestMalformedVersionsKeepEveryHealthyObligation(t *testing.T) {
	for _, broken := range []string{`{broken`, `{"schema":1,"task":"t-aaaaaaaaaaaa","requested":"r1","revisions":[null]}`, `{"schema":1,"task":"t-aaaaaaaaaaaa","requested":"r1","revisions":[]}`} {
		t.Run(broken, func(t *testing.T) {
			s := fixture(t)
			write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","parent":{"pane":"p"},"questions":[{"id":"q1","status":"open"}],"evidence":[{"id":"e1","kind":"report","at":"1"},{"id":"e2","kind":"review","at":"2","candidate":"sha"}],"attention":[{"id":"a1","kind":"idle","status":"open","at":"3"}]}`)
			write(t, s, "tasks/t-aaaaaaaaaaaa/versions.json", broken)
			got := Read(s)
			kinds := map[presentation.SourceKind]bool{}
			for _, item := range got.Tasks[0].Items {
				kinds[item.Source.Kind] = true
			}
			for _, kind := range []presentation.SourceKind{presentation.Question, presentation.Report, presentation.Review, presentation.Attention} {
				if !kinds[kind] {
					t.Fatalf("lost %s after version failure: %+v", kind, got)
				}
			}
			if got.Complete || len(got.Gaps) != 1 {
				t.Fatalf("version gap not explicit: %+v", got.Gaps)
			}
		})
	}
}

func TestRefreshDeliveryAndStableCanonicalIdentity(t *testing.T) {
	s := fixture(t)
	taskPath := "tasks/t-aaaaaaaaaaaa/task.json"
	task := `{"schema":1,"id":"t-aaaaaaaaaaaa","machine":"m-lab","session":"lab","pane":"worker","parent":{"machine":"m-lab","session":"lab","pane":"parent"},"project":{"host":"git.example.com","owner":"team","repo":"repo"},"questions":[{"id":"q1","key":"choice","status":"open","text":"routine"}],"evidence":[{"id":"e1","kind":"review","candidate":"sha1"}]}`
	write(t, s, taskPath, task)
	write(t, s, "tasks/t-aaaaaaaaaaaa/versions.json", `{"schema":1,"task":"t-aaaaaaaaaaaa","requested":"r1","revisions":[{"id":"r1","status":"requested"}],"refresh":[{"event":"requested","revision":"r1","at":"1"},{"event":"delivery","revision":"r1","state":"submitted-unconfirmed"}]}`)
	key := store.RegistrationKey(store.Endpoint{Machine: "m-lab", Session: "lab", Pane: "parent"})
	write(t, s, "tasks/t-aaaaaaaaaaaa/returns.json", `{"schema":1,"task":"t-aaaaaaaaaaaa","deliveries":[{"id":"d1","state":"submitted","obligations":["question:q1"],"recipient":{"key":"`+key+`"}}]}`)
	first := Read(s)
	find := func(snapshot Snapshot, kind presentation.SourceKind) presentation.Item {
		t.Helper()
		for _, item := range snapshot.Items {
			if item.Source.Kind == kind {
				return item
			}
		}
		t.Fatalf("missing %s", kind)
		return presentation.Item{}
	}
	q, review, refresh := find(first, presentation.Question), find(first, presentation.Review), find(first, presentation.Refresh)
	if !q.Uncertain || q.Kind != presentation.Decision || refresh.Owner != presentation.Worker || !refresh.Uncertain || q.ProjectID != "git.example.com/team/repo" {
		t.Fatalf("wrong facts: %+v %+v", q, refresh)
	}
	if find(Read(s), presentation.Question).Identity != q.Identity {
		t.Fatal("unchanged question identity moved")
	}
	changed := strings.Replace(strings.Replace(task, `"key":"choice"`, `"key":"other"`, 1), `"candidate":"sha1"`, `"candidate":"sha2"`, 1)
	write(t, s, taskPath, changed)
	next := Read(s)
	if find(next, presentation.Question).Identity == q.Identity || find(next, presentation.Review).Identity == review.Identity {
		t.Fatal("new key/candidate retained old identity")
	}
}

func TestPartialSourceShapesAndSavedPipelineFactoryFacts(t *testing.T) {
	s := fixture(t)
	write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","questions":[null,{"id":"q1","status":"open"}],"launch":{"observed":{"status":"not-started"}}}`)
	write(t, s, "tasks/t-aaaaaaaaaaaa/returns.json", `{"schema":1,"task":"t-aaaaaaaaaaaa","deliveries":[null]}`)
	write(t, s, "tasks/t-aaaaaaaaaaaa/pipeline.json", `{"schema":1,"task":"t-aaaaaaaaaaaa","candidate":"sha1","rows":[{"stage":"test","status":"fail","result":"saved failure","at":"1"}]}`)
	write(t, s, "factory.json", `{"schema":1,"projects":{"git.example.com/team/repo":{"lanes_held":[{"task":"t-aaaaaaaaaaaa","issue":239,"state":"gated","claimed_at":"1"}]}}}`)
	before := bytesOf(t, s.Home)
	got := Read(s)
	if got.Counts.Decisions != 1 || got.Counts.UnknownSources != 2 || got.Tasks[0].Pipeline == nil {
		t.Fatalf("lost partial facts: %+v", got)
	}
	kinds := map[presentation.SourceKind]presentation.Item{}
	for _, item := range got.Items {
		if item.Source.Kind == presentation.Question && (item.Delivery != "unknown" || !item.Uncertain) {
			t.Fatalf("malformed returns lost delivery uncertainty: %+v", item)
		}
		if item.Kind == presentation.Inspection {
			kinds[item.Source.Kind] = item
		}
	}
	for _, kind := range []presentation.SourceKind{presentation.Execution, presentation.Pipeline, presentation.Factory} {
		if _, ok := kinds[kind]; !ok {
			t.Fatalf("missing saved %s inspection", kind)
		}
	}
	if !kinds[presentation.Execution].Uncertain || kinds[presentation.Pipeline].Source.Candidate != "sha1" {
		t.Fatal("missing execution uncertainty or pipeline candidate")
	}
	if !reflect.DeepEqual(before, bytesOf(t, s.Home)) {
		t.Fatal("presentation mutated source bytes")
	}
}

func TestDirectorySidecarAndMissingTaskAreGaps(t *testing.T) {
	s := fixture(t)
	write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","questions":[{"id":"q1","status":"open"}]}`)
	for _, path := range []string{"tasks/t-aaaaaaaaaaaa/versions.json", "tasks/t-bbbbbbbbbbbb"} {
		if err := os.MkdirAll(filepath.Join(s.Home, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	got := Read(s)
	if got.Counts.Decisions != 1 || got.Counts.UnknownSources != 2 {
		t.Fatalf("read silently skipped unreadable sources: %+v", got)
	}
}

func TestFIFOSourcesRetainHealthyDecision(t *testing.T) {
	if home := os.Getenv("INBOXVIEW_FIFO_HOME"); home != "" {
		s, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		got := Read(s)
		if got.Complete || got.Counts.Decisions != 1 || got.Counts.UnknownSources != 1 {
			t.Fatalf("FIFO concealed healthy decision or source gap: %+v", got)
		}
		if got.Gaps[0].Path != os.Getenv("INBOXVIEW_FIFO_PATH") || !strings.Contains(got.Gaps[0].Reason, "not a regular file") {
			t.Fatalf("unexpected source gap: %+v", got.Gaps)
		}
		return
	}
	for _, path := range []string{"projects.json", "factory.json", "tasks/t-aaaaaaaaaaaa/task.json", "tasks/t-aaaaaaaaaaaa/versions.json", "tasks/t-aaaaaaaaaaaa/returns.json", "tasks/t-aaaaaaaaaaaa/pipeline.json"} {
		t.Run(path, func(t *testing.T) {
			s := fixture(t)
			write(t, s, "tasks/t-bbbbbbbbbbbb/task.json", `{"schema":1,"id":"t-bbbbbbbbbbbb","questions":[{"id":"q1","status":"open"}]}`)
			if strings.HasPrefix(path, "tasks/") && !strings.HasSuffix(path, "/task.json") {
				write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa"}`)
			}
			fifo := filepath.Join(s.Home, path)
			if err := os.MkdirAll(filepath.Dir(fifo), 0700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestFIFOSourcesRetainHealthyDecision$")
			cmd.Env = append(os.Environ(), "INBOXVIEW_FIFO_HOME="+s.Home, "INBOXVIEW_FIFO_PATH="+fifo)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("snapshot blocked on FIFO; subprocess killed and reaped: %v\n%s", ctx.Err(), output)
			}
			if err != nil {
				t.Fatalf("FIFO snapshot subprocess failed: %v\n%s", err, output)
			}
		})
	}
}

func TestMissingTasksDirectoryCoverage(t *testing.T) {
	s := fixture(t)
	if got := Read(s); !got.Complete || got.Counts.Tasks != 0 || got.Counts.UnknownSources != 0 {
		t.Fatalf("fresh home is not empty and complete: %+v", got)
	}
	write(t, s, "state.json", `{"schema":1}`)
	if err := os.Mkdir(s.Tasks, 0700); err != nil {
		t.Fatal(err)
	}
	if got := Read(s); !got.Complete {
		t.Fatalf("initialized empty home has a gap: %+v", got.Gaps)
	}
	if err := os.Remove(s.Tasks); err != nil {
		t.Fatal(err)
	}
	got := Read(s)
	if got.Complete || got.Counts.UnknownSources != 1 || got.Gaps[0].Path != s.Tasks {
		t.Fatalf("missing initialized tasks directory reported clear: %+v", got)
	}
}

func TestArchivedPipelineRetainsHistoryWithoutCurrentWork(t *testing.T) {
	for _, status := range []string{"fail", "blocked"} {
		t.Run(status, func(t *testing.T) {
			s := fixture(t)
			write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"archived"}`)
			path := "tasks/t-aaaaaaaaaaaa/pipeline.json"
			write(t, s, path, `{"schema":1,"task":"t-aaaaaaaaaaaa","candidate":"sha1","rows":[{"stage":"test","status":"`+status+`","result":"saved failure"}]}`)
			got := Read(s)
			if !got.Complete || got.Counts.Coordinator != 0 || got.Counts.Inspection != 0 || len(got.Items) != 0 || got.Tasks[0].Pipeline == nil {
				t.Fatalf("archived pipeline counted as current work or lost history: %+v", got)
			}
			write(t, s, path, `{broken`)
			got = Read(s)
			if got.Complete || got.Counts.UnknownSources != 1 || got.Gaps[0].Path != filepath.Join(s.Home, path) {
				t.Fatalf("archived pipeline hid source gap: %+v", got)
			}
		})
	}
}

func TestMalformedPipelineRetainsHealthyDecision(t *testing.T) {
	for _, broken := range []string{
		`{broken`,
		`{"schema":1,"task":"t-bbbbbbbbbbbb","rows":[]}`,
		`{"schema":1,"task":"t-aaaaaaaaaaaa","rows":[{"stage":"test","status":"unexpected"}]}`,
	} {
		t.Run(broken, func(t *testing.T) {
			s := fixture(t)
			write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","questions":[{"id":"q1","status":"open"}]}`)
			path := "tasks/t-aaaaaaaaaaaa/pipeline.json"
			write(t, s, path, broken)
			got := Read(s)
			if got.Complete || got.Counts.Decisions != 1 || got.Counts.UnknownSources != 1 || got.Gaps[0].Path != filepath.Join(s.Home, path) || got.Tasks[0].Pipeline != nil {
				t.Fatalf("malformed pipeline concealed decision or gap: %+v", got)
			}
		})
	}
}

func TestUnknownExecutionAndMalformedFactoryCoverage(t *testing.T) {
	s := fixture(t)
	write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","questions":[{"id":"q1","status":"open"}],"launch":{"observed":{"status":"future"}}}`)
	write(t, s, "factory.json", `{"schema":1,"projects":[]}`)
	got := Read(s)
	if got.Complete || got.Counts.UnknownSources != 2 || got.Counts.Decisions != 1 {
		t.Fatalf("unknown source presented as clear: %+v", got)
	}
}

func TestSavedPassingPipelineAndRunningLaneAreContext(t *testing.T) {
	s := fixture(t)
	write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa"}`)
	write(t, s, "tasks/t-aaaaaaaaaaaa/pipeline.json", `{"schema":1,"task":"t-aaaaaaaaaaaa","candidate":"sha1","rows":[{"stage":"test","status":"pass"}]}`)
	write(t, s, "factory.json", `{"schema":1,"projects":{"team/repo":{"lanes_held":[{"task":"t-aaaaaaaaaaaa","issue":239,"state":"running","claimed_at":"1"}]}}}`)
	got := Read(s)
	if !got.Complete || got.Counts.Coordinator != 0 || got.Counts.Inspection != 0 || len(got.Items) == 0 {
		t.Fatalf("saved context became owed action: %+v", got)
	}
}

func TestMalformedDecisionTextKeepsDecisionAndMarksCoverageUnknown(t *testing.T) {
	s := fixture(t)
	write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","questions":[{"id":"q1","status":"open","text":{"unexpected":"object"}}]}`)
	got := Read(s)
	if got.Counts.Decisions != 1 || got.Complete || got.Counts.UnknownSources != 1 {
		t.Fatalf("malformed decision prose silently disappeared: %+v", got)
	}
}

func TestLegacyProjectLookupPreservesDeterministicMatch(t *testing.T) {
	s := fixture(t)
	write(t, s, "projects.json", `{"schema":1,"projects":{"z/repo":{"path":"/lab/repo"},"a/repo":{"path":"/lab/./repo"}}}`)
	write(t, s, "tasks/t-aaaaaaaaaaaa/task.json", `{"schema":1,"id":"t-aaaaaaaaaaaa","repository":"/lab/repo"}`)
	write(t, s, "tasks/t-bbbbbbbbbbbb/task.json", `{"schema":1,"id":"t-bbbbbbbbbbbb","repository":"/lab/unregistered"}`)
	got := Read(s)
	if !got.Complete || got.Tasks[0].ProjectID != "a/repo" || got.Tasks[1].ProjectID != "/lab/unregistered" {
		t.Fatalf("legacy project lookup changed: %+v", got)
	}
}
