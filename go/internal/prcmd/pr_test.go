package prcmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/proc"
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
