package inboxview

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/presentation"
)

const hostileText = "\x1b[31mred\x1b[0m\x07 $(touch x)"
const cjkText = "界面设计需要决定颜色方案和字体大小以及边距的处理方式请尽快回复否则无法继续"

func viewSnapshot(t *testing.T) Snapshot {
	t.Helper()
	s := groupedSnapshot(t)
	s.Tasks[1].Items = append(s.Tasks[1].Items, question("t-aaaaaaaaaaaa", "a/repo", "q-5", "open", hostileText))
	s.Tasks[3].Items = []presentation.Item{question("t-dddddddddddd", "", "q-3", "open", cjkText)}
	countSnapshot(&s)
	return s
}

func lines(rendered string) []string {
	return strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
}

func assertBounds(t *testing.T, rendered string, width, height int) {
	t.Helper()
	rows := lines(rendered)
	if len(rows) > height {
		t.Fatalf("%d lines exceed height %d:\n%s", len(rows), height, rendered)
	}
	for _, row := range rows {
		if w := displayWidth(row); w > width {
			t.Fatalf("line width %d exceeds %d: %q", w, width, row)
		}
	}
	for _, forbidden := range []string{"\x1b", "\x07", "\x00"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("control byte reached output: %q", rendered)
		}
	}
}

func TestSanitizeStripsTerminalControls(t *testing.T) {
	for in, want := range map[string]string{
		hostileText:                   "red $(touch x)",
		"a\tb\nc\rd":                  "a b c d",
		"\x1b]0;title\x07x":           "x",
		"\x1b]0;title\x1b\\y":         "y",
		"\u0085\u009fz\u007f":         "z",
		cjkText + " ok":               cjkText + " ok",
		"\x1bZ tail":                  " tail",
		"caf\u00e9 \u2028 \u00a0kept": "caf\u00e9   \u00a0kept",
	} {
		if got := Sanitize(in); got != want {
			t.Fatalf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateMarksAndRespectsWideRunes(t *testing.T) {
	if got := Truncate("界界界界", 5); got != "界界…" || displayWidth(got) != 5 {
		t.Fatalf("truncate wide = %q", got)
	}
	if got := Truncate("abc", 3); got != "abc" {
		t.Fatalf("no-op truncate = %q", got)
	}
	if got := Truncate("abcd", 3); got != "ab…" {
		t.Fatalf("truncate narrow = %q", got)
	}
}

func TestViewStateSurvivesSameScopeRefreshByIdentity(t *testing.T) {
	snapshot := viewSnapshot(t)
	view := NewView(Scope{Home: "/h"}, BuildOverview(snapshot))
	if view.Selected() != "needs-you:"+snapshot.Tasks[1].Items[0].Identity {
		t.Fatalf("initial selection = %q", view.Selected())
	}
	for _, cmd := range []string{"n", "n", "n"} {
		if view.Command(cmd) != ActionNone {
			t.Fatal("navigation must not request I/O")
		}
	}
	if view.Selected() != "project:a/repo" {
		t.Fatalf("after 3 moves = %q", view.Selected())
	}
	view.Command("n")
	if view.Selected() != "t-aaaaaaaaaaaa" {
		t.Fatalf("expanded group must expose its task: %q", view.Selected())
	}
	if view.Command("d") != ActionNone || !view.InDetail() {
		t.Fatal("detail did not open")
	}
	view.Command("n")
	view.Command("n")
	offset := view.Offset()
	if offset != 2 {
		t.Fatalf("detail scroll = %d", offset)
	}
	if view.Command("r") != ActionRefresh {
		t.Fatal("r must ask the caller to reload")
	}
	view.Refresh(BuildOverview(snapshot), nil)
	if !view.InDetail() || view.Selected() != "t-aaaaaaaaaaaa" || view.Offset() != offset || view.Stale() {
		t.Fatalf("same-scope refresh lost state: detail=%v selected=%q offset=%d", view.InDetail(), view.Selected(), view.Offset())
	}
	view.Command("b")
	if view.InDetail() || view.Selected() != "t-aaaaaaaaaaaa" {
		t.Fatalf("back lost selection: %q", view.Selected())
	}
	if view.Command("q") != ActionQuit {
		t.Fatal("q must quit")
	}
	if view.Command("zz") != ActionNone || !strings.Contains(view.Render(80, 24), "unknown command") {
		t.Fatal("unknown command must produce a one-line message")
	}
}

func TestViewRemovedSelectionBecomesExplicitlyUnavailable(t *testing.T) {
	snapshot := viewSnapshot(t)
	view := NewView(Scope{Home: "/h"}, BuildOverview(snapshot))
	for i := 0; i < 4; i++ {
		view.Command("n")
	}
	if view.Selected() != "t-aaaaaaaaaaaa" {
		t.Fatalf("selected %q", view.Selected())
	}
	shrunk := snapshot
	shrunk.Tasks = append([]Task{}, snapshot.Tasks[0], snapshot.Tasks[2], snapshot.Tasks[3], snapshot.Tasks[4])
	countSnapshot(&shrunk)
	view.Refresh(BuildOverview(shrunk), nil)
	rendered := view.Render(80, 24)
	if !view.Unavailable() || view.Selected() != "t-aaaaaaaaaaaa" || !strings.Contains(rendered, "no longer present") {
		t.Fatalf("removed row must be reported, not silently replaced:\n%s", rendered)
	}
	view.Command("b")
	if view.Unavailable() || view.Selected() == "t-aaaaaaaaaaaa" || view.Selected() == "" {
		t.Fatalf("back must pick a present row: %q", view.Selected())
	}
}

func TestViewFailedRefreshKeepsRowsMarksStaleAndDisablesActions(t *testing.T) {
	snapshot := viewSnapshot(t)
	view := NewView(Scope{Home: "/h"}, BuildOverview(snapshot))
	for i := 0; i < 4; i++ {
		view.Command("n")
	}
	view.Command("d")
	fresh := view.Render(140, 42)
	if !strings.Contains(fresh, "sumctl answer t-aaaaaaaaaaaa q-1 --text") || !strings.Contains(fresh, "sumctl context t-aaaaaaaaaaaa") {
		t.Fatalf("detail must show routes:\n%s", fresh)
	}
	view.Refresh(Overview{}, errors.New("read failed: disk"))
	stale := view.Render(140, 42)
	if !view.Stale() || !strings.Contains(stale, "stale: read failed: disk") || !strings.Contains(stale, "t-aaaaaaaaaaaa") {
		t.Fatalf("stale view lost rows or marker:\n%s", stale)
	}
	if strings.Contains(stale, "sumctl ") {
		t.Fatalf("stale view exposed an actionable route:\n%s", stale)
	}
	view.Command("b")
	if strings.Contains(view.Render(140, 42), "sumctl ") {
		t.Fatal("stale list exposed a route")
	}
	view.Refresh(BuildOverview(snapshot), nil)
	if view.Stale() || !strings.Contains(view.Render(140, 42), "a/repo") {
		t.Fatal("successful refresh must clear staleness")
	}
}

func TestViewSetScopeDropsPriorRows(t *testing.T) {
	snapshot := viewSnapshot(t)
	view := NewView(Scope{Home: "/h"}, BuildOverview(snapshot))
	for i := 0; i < 4; i++ {
		view.Command("n")
	}
	view.Command("d")
	view.SetScope(Scope{Home: "/h", Project: "b/repo"})
	rendered := view.Render(80, 24)
	if view.InDetail() || view.Selected() != "" || view.Offset() != 0 || strings.Contains(rendered, "t-aaaaaaaaaaaa") || strings.Contains(rendered, "a/repo") {
		t.Fatalf("scope change carried old rows:\n%s", rendered)
	}
	view.Refresh(Focus(BuildOverview(snapshot), "b/repo"), nil)
	rendered = view.Render(80, 24)
	if !strings.Contains(rendered, "b/repo") || !strings.Contains(rendered, "3 decisions") {
		t.Fatalf("focused view lost global counts:\n%s", rendered)
	}
	view.SetScope(Scope{Home: "/h", Project: "b/repo"})
	if view.Selected() == "" {
		t.Fatal("same scope must keep state")
	}
}

func TestRenderGoldenAtTwoSizes(t *testing.T) {
	snapshot := viewSnapshot(t)
	for _, size := range []struct {
		name          string
		width, height int
	}{{"80x24", 80, 24}, {"140x42", 140, 42}} {
		view := NewView(Scope{Home: "/h"}, BuildOverview(snapshot))
		var got bytes.Buffer
		steps := []string{"", "n", "n", "n", "n", "d", "n", "b", "o", "c", "p"}
		for _, cmd := range steps {
			view.Command(cmd)
			rendered := view.Render(size.width, size.height)
			assertBounds(t, rendered, size.width, size.height)
			got.WriteString("$ " + cmd + "\n" + rendered)
		}
		text := got.String()
		if size.width == 80 && !strings.Contains(text, "的处…") {
			t.Fatal("long text must be truncated with a marker")
		}
		if !strings.Contains(text, "needs-decision") || !strings.Contains(text, "needs-attention") {
			t.Fatal("states must be words")
		}
		if strings.Contains(text, "red") && !strings.Contains(text, "red $(touch x)") {
			t.Fatal("sanitized text should remain readable")
		}
		if size.width == 140 && !strings.Contains(text, cjkText) {
			t.Fatal("full CJK text must be reachable in detail mode")
		}
		path := filepath.Join("testdata", "render-"+size.name+".txt")
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("golden %s missing (%v); rendered:\n%s", path, err, text)
		}
		if string(want) != text {
			t.Fatalf("render differs from %s:\n%s", path, text)
		}
	}
}

func TestRenderScrollsToKeepSelectionVisible(t *testing.T) {
	snapshot := viewSnapshot(t)
	view := NewView(Scope{Home: "/h"}, BuildOverview(snapshot))
	for i := 0; i < 8; i++ {
		view.Command("n")
	}
	rendered := view.Render(80, 8)
	assertBounds(t, rendered, 80, 8)
	if !strings.Contains(rendered, "> ") || !strings.Contains(rendered, "more") {
		t.Fatalf("paged render must keep the selection and announce overflow:\n%s", rendered)
	}
}

func TestRunExplicitRefreshLoop(t *testing.T) {
	snapshot := viewSnapshot(t)
	loads := 0
	load := func() (Overview, error) {
		loads++
		if loads == 2 {
			return Overview{}, errors.New("transient")
		}
		return BuildOverview(snapshot), nil
	}
	var out bytes.Buffer
	if err := Run(strings.NewReader("n\nr\nr\nq\n"), &out, 80, 24, Scope{Home: "/h"}, load); err != nil {
		t.Fatal(err)
	}
	if loads != 3 || !strings.Contains(out.String(), "stale: transient") || !strings.Contains(out.String(), "PROJECTS") {
		t.Fatalf("loads=%d output:\n%s", loads, out.String())
	}
	out.Reset()
	if err := Run(strings.NewReader(""), &out, 80, 24, Scope{Home: "/h"}, load); err != nil {
		t.Fatalf("EOF must end the loop cleanly: %v", err)
	}
}

func TestDetailWrapsLongTextCompletelyAtNarrowWidth(t *testing.T) {
	snapshot := viewSnapshot(t)
	view := NewView(Scope{Home: "/h"}, BuildOverview(snapshot))
	view.Command("n")
	view.Command("n")
	if view.Command("d") != ActionNone || !view.InDetail() {
		t.Fatal("detail from a needs-you row must open its task")
	}
	rendered := view.Render(60, 40)
	assertBounds(t, rendered, 60, 40)
	joined := ""
	rows := lines(rendered)
	body := rows[1 : len(rows)-1]
	for _, row := range body {
		joined += strings.TrimSpace(row)
	}
	if !strings.Contains(joined, cjkText) || strings.Contains(strings.Join(body, "\n"), "…") || strings.Contains(rendered, cjkText) {
		t.Fatalf("detail must carry the full text without a truncation marker:\n%s", rendered)
	}
}
