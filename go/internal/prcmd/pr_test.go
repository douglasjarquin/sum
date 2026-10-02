package prcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// fakeGh writes an executable shell script standing in for gh; it never reaches GitHub.
func fakeGh(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestViewPRParsesStdoutOnly(t *testing.T) {
	gh := fakeGh(t, `echo 'A new release of gh is available: 2.0 -> 3.0' >&2
printf '{"number":7,"state":"OPEN","headRefOid":"%s"}' "$*"`)
	data, err := viewPR(gh, t.TempDir(), "owner/repo", 7)
	if err != nil {
		t.Fatalf("stderr noise broke the observation: %v", err)
	}
	if data["number"] != float64(7) || data["state"] != "OPEN" {
		t.Fatalf("data=%v", data)
	}
	want := "pr view 7 --json " + prViewFields + " --repo owner/repo"
	if data["headRefOid"] != want {
		t.Fatalf("argv=%q, want %q", data["headRefOid"], want)
	}
}

func TestViewPRNonzeroExitIsUncertain(t *testing.T) {
	gh := fakeGh(t, `echo 'GraphQL: Could not resolve to a PullRequest' >&2; exit 1`)
	_, err := viewPR(gh, t.TempDir(), "", 7)
	if err == nil || err.Error() != "PR observation for #7 is uncertain: GraphQL: Could not resolve to a PullRequest" {
		t.Fatalf("err=%v", err)
	}
}

func TestViewPRRejectsIncompleteJSON(t *testing.T) {
	gh := fakeGh(t, `printf '{"number":7,'`)
	data, err := viewPR(gh, t.TempDir(), "", 7)
	if err == nil || data != nil || err.Error() != `gh did not return JSON: {"number":7,` {
		t.Fatalf("data=%v err=%v", data, err)
	}
}

func TestViewPRRejectsOversizedStdout(t *testing.T) {
	gh := fakeGh(t, `printf '{"url":"'; head -c 9437184 /dev/zero | tr '\0' 'a'; printf '"}'`)
	data, err := viewPR(gh, t.TempDir(), "", 7)
	if err == nil || data != nil || !errors.Is(err, proc.ErrOutputLimit) {
		t.Fatalf("data of %d keys err=%v, want an output-limit error", len(data), err)
	}
	if !strings.HasPrefix(err.Error(), "PR observation for #7 is uncertain: gh: stdout exceeded") {
		t.Fatalf("err=%v", err)
	}
}

func observeInto(t *testing.T, taskJSON, ghJSON string) *ordjson.Object {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	value, err := ordjson.Decode([]byte(taskJSON))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := value.(*ordjson.Object)
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	ctx := ordjson.NewObject()
	ctx.Set("machine", "m-test")
	ctx.Set("session", "sum-test")
	ctx.Set("pane", "w-parent:p1")
	raw, err := ordjson.Decode([]byte(ghJSON))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := raw.(*ordjson.Object)
	fields := map[string]any{}
	for _, k := range data.Keys() {
		v, _ := data.Get(k)
		fields[k] = v
	}
	pr, _, err := recordObservation(st, ctx, "t-aaaaaaaaaaaa", fields)
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func ghView(state, headSHA, mergeCommit string) string {
	mc := "null"
	if mergeCommit != "" {
		mc = fmt.Sprintf(`{"oid": %q}`, mergeCommit)
	}
	return fmt.Sprintf(`{"number": 7, "url": "https://github.com/o/r/pull/7", "state": %q,
		"headRefOid": %q, "headRefName": "sum/t-aaaaaaaaaaaa", "baseRefName": "main",
		"headRepository": {"name": "r"}, "headRepositoryOwner": {"login": "o"}, "isCrossRepository": false,
		"mergedAt": null, "mergeCommit": %s, "isDraft": false, "statusCheckRollup": []}`, state, headSHA, mc)
}

func observationTask(candidate, extraEvidence string) string {
	return fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "reported",
		"report": {"candidate": %q}, "evidence": [%s]}`, candidate, extraEvidence)
}

func verificationEvidence(source, candidate, result string) string {
	return fmt.Sprintf(`{"kind": "verification", "source": %q, "candidate": %q, "result": %q, "id": "e-verify0000"}`,
		source, candidate, result)
}

func TestRecordObservation_squashMergeWithMatchingHead(t *testing.T) {
	head := strings.Repeat("a", 40)
	pr := observeInto(t, observationTask(head, ""), ghView("MERGED", head, strings.Repeat("b", 40)))
	if merged, _ := pr.Get("merged_for_task"); merged != true {
		t.Fatalf("squash-merged PR with matching head: pr = %v", pr)
	}
	if findings := asList(func() any { v, _ := pr.Get("findings"); return v }()); len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
}

func TestRecordObservation_mergedHeadVerifiedByCoordinator(t *testing.T) {
	head := strings.Repeat("a", 40)
	stale := strings.Repeat("9", 40)
	evidence := verificationEvidence("coordinator", head, "pass")
	pr := observeInto(t, observationTask(stale, evidence), ghView("MERGED", head, strings.Repeat("b", 40)))
	if merged, _ := pr.Get("merged_for_task"); merged != true {
		t.Fatalf("merged PR whose head the coordinator verified: pr = %v", pr)
	}
	if findings := asList(func() any { v, _ := pr.Get("findings"); return v }()); len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
	notes := asList(func() any { v, _ := pr.Get("notes"); return v }())
	if len(notes) != 1 || !strings.Contains(fmt.Sprint(notes[0]), "is not the recorded candidate") {
		t.Fatalf("notes = %v, want the explained mismatch recorded", notes)
	}
}

func TestRecordObservation_unexplainedMismatchStillBlocks(t *testing.T) {
	head := strings.Repeat("a", 40)
	stale := strings.Repeat("9", 40)
	for name, evidence := range map[string]string{
		"no verification":     "",
		"worker verify":       verificationEvidence("worker", head, "pass"),
		"coordinator failed":  verificationEvidence("coordinator", head, "fail"),
		"verify on other sha": verificationEvidence("coordinator", stale, "pass"),
	} {
		t.Run(name, func(t *testing.T) {
			pr := observeInto(t, observationTask(stale, evidence), ghView("MERGED", head, strings.Repeat("b", 40)))
			if merged, _ := pr.Get("merged_for_task"); merged == true {
				t.Fatalf("unexplained mismatch was accepted: pr = %v", pr)
			}
			findings := asList(func() any { v, _ := pr.Get("findings"); return v }())
			want := fmt.Sprintf("PR head %s is not the recorded candidate %s", head, stale)
			if len(findings) != 1 || findings[0] != want {
				t.Fatalf("findings = %v, want %q", findings, want)
			}
		})
	}
}

func TestRecordObservation_closedPRIsNotMerged(t *testing.T) {
	head := strings.Repeat("a", 40)
	pr := observeInto(t, observationTask(head, ""), ghView("CLOSED", head, ""))
	if merged, _ := pr.Get("merged_for_task"); merged == true {
		t.Fatalf("closed PR counted as merged: pr = %v", pr)
	}
	if state, _ := pr.Get("state"); state != "closed" {
		t.Fatalf("state = %v, want closed", state)
	}
}

func TestObservedVisibilityReadsStdoutOnly(t *testing.T) {
	gh := fakeGh(t, `echo 'warning: something' >&2; printf '{"visibility":"PUBLIC"}'`)
	got, err := observedVisibility(gh, t.TempDir(), "owner/repo")
	if err != nil || got != "public" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	failing := fakeGh(t, `echo nope >&2; exit 1`)
	if _, err := observedVisibility(failing, t.TempDir(), "owner/repo"); err == nil || !strings.Contains(err.Error(), "could not report the visibility of owner/repo") {
		t.Fatalf("err=%v", err)
	}
}
