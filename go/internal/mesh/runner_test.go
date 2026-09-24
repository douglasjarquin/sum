package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

// fakeHerdrPath writes an executable shell script standing in for herdr; it never reaches a real Herdr session.
func fakeHerdrPath(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func serviceWithFakeHerdr(t *testing.T, body string) Service {
	t.Helper()
	service := newTestService(t, "coordinator", &fakeRunner{})
	service.runner = herdrRunner{path: fakeHerdrPath(t, body), session: "mesh-test"}
	return service
}

const oversized = `printf '{"result":"'; head -c 300000 /dev/zero | tr '\0' 'a'; printf '"}'`

func TestHerdrRunner_jsonPathRejectsOversizedStdout(t *testing.T) {
	service := serviceWithFakeHerdr(t, oversized)
	out, err := service.Call(context.Background(), "herdr_integration_status", json.RawMessage(`{}`))
	if err == nil || out != "" {
		t.Fatalf("out=%d bytes err=%v, want an error and no observation", len(out), err)
	}
	if !errors.Is(err, proc.ErrOutputLimit) {
		t.Fatalf("err=%v, want errors.Is proc.ErrOutputLimit", err)
	}
}

func TestHerdrRunner_textPathMarksTruncation(t *testing.T) {
	service := serviceWithFakeHerdr(t, `head -c 300000 /dev/zero | tr '\0' 'a'`)
	out, err := service.Call(context.Background(), "herdr_pane_read", json.RawMessage(`{"pane_id":"w-test:p2"}`))
	if err != nil {
		t.Fatal(err)
	}
	marker := "\n[output truncated at 262144 bytes]\n"
	if !strings.HasSuffix(out, marker) {
		t.Fatalf("output of %d bytes does not end with the truncation line", len(out))
	}
	if head := strings.TrimSuffix(out, marker); len(head) != 262144 || strings.Trim(head, "a") != "" {
		t.Fatalf("head is %d bytes, want the first 262144 bytes", len(head))
	}
}

func TestHerdrRunner_textPathUnderLimitIsUnchanged(t *testing.T) {
	service := serviceWithFakeHerdr(t, `printf 'visible output\n'`)
	out, err := service.Call(context.Background(), "herdr_pane_read", json.RawMessage(`{"pane_id":"w-test:p2"}`))
	if err != nil || out != "visible output\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestHerdrRunner_nonzeroExitWithEnvelope(t *testing.T) {
	service := serviceWithFakeHerdr(t, `printf '{"error":{"code":"pane_not_found"}}' >&2; exit 1`)
	_, err := service.Call(context.Background(), "herdr_integration_status", json.RawMessage(`{}`))
	if err == nil || err.Error() != `Herdr: {"code":"pane_not_found"}` {
		t.Fatalf("err=%v", err)
	}
}

func TestHerdrRunner_nonzeroExitWithoutEnvelope(t *testing.T) {
	service := serviceWithFakeHerdr(t, `echo nope >&2; exit 2`)
	_, err := service.Call(context.Background(), "herdr_integration_status", json.RawMessage(`{}`))
	if err == nil || err.Error() != "Herdr exited unsuccessfully: exit status 2: nope\n" {
		t.Fatalf("err=%q", err)
	}
}

func TestHerdrRunner_timeoutAndCancelAreUncertain(t *testing.T) {
	runner := herdrRunner{path: fakeHerdrPath(t, `exec sleep 5`), session: "mesh-test"}
	_, err := runner.Run(context.Background(), []string{"agent", "list"}, 300*time.Millisecond, false)
	if err == nil || err.Error() != "herdr: timed out after 300ms; its effect is unknown" || !errors.Is(err, proc.ErrUncertain) {
		t.Fatalf("timeout err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err = runner.Run(ctx, []string{"agent", "list"}, 5*time.Second, false)
	if err == nil || err.Error() != "herdr: canceled by the caller; its effect is unknown" || !errors.Is(err, proc.ErrUncertain) {
		t.Fatalf("cancel err=%v", err)
	}
}
