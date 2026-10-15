package pipeline

import (
	"strings"
	"testing"
)

const stampTestMarker = "<!-- sum:0.1.0@0123456789abcdef0123456789abcdef01234567 -->"

func TestStampedBody_appendsTheMarkerAfterTheRenderedBody(t *testing.T) {
	body := "## Summary\n\ndo the thing\n"
	want := "## Summary\n\ndo the thing\n\n" + stampTestMarker + "\n"
	if got := stampedBody(body, stampTestMarker); got != want {
		t.Fatalf("stampedBody =\n%q\nwant\n%q", got, want)
	}
	if got := stampedBody("", "<!-- sum:0.1.0 -->"); got != "<!-- sum:0.1.0 -->\n" {
		t.Fatalf("stampedBody on an empty body = %q", got)
	}
}

func TestStampedBlock_putsTheMarkerInsideTheMarkedSpan(t *testing.T) {
	task := taskFrom(t, `{"schema": 1, "id": "t-a", "brief": "b", "evidence": []}`)

	block := stampedBlock(Render(Derive(task), task), stampTestMarker)

	if !strings.HasPrefix(block, MarkerStart+"\n"+stampTestMarker+"\n## Pipeline\n") {
		t.Fatalf("stamped block =\n%s", block)
	}
	spliced := splice("## Summary\n\nHuman prose.\n", nil, block)
	at := mustFindBlock(t, spliced)
	span := spliced[at.start:at.end]
	if !strings.Contains(span, stampTestMarker) || strings.Count(span, "<!-- sum:") != 1 {
		t.Fatalf("the marker is not exactly once inside the span:\n%s", spliced)
	}
	if strings.Contains(spliced[:at.start]+spliced[at.end:], "<!-- sum:") {
		t.Fatalf("the marker leaked outside the span:\n%s", spliced)
	}
	if normalize(span) != normalize(block) {
		t.Fatalf("a second publish of the same block would not be unchanged")
	}
}

func TestStampedBlock_noMarkerLeavesTheBlockAlone(t *testing.T) {
	task := taskFrom(t, `{"schema": 1, "id": "t-a", "brief": "b", "evidence": []}`)
	block := Render(Derive(task), task)
	if got := stampedBlock(block, ""); got != block {
		t.Fatalf("stampedBlock rewrote the block without a marker")
	}
}
