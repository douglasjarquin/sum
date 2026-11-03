package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestUpdatePendingPrintedRecoveryArgvAcceptsOrdinaryClaim(t *testing.T) {
	runtime, s := ordinaryInitLab(t)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "recovery command", false)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", runtime, "init", "-b", "main")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, output)
	}
	root, err := filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "bin", "sumctl")
	generation := strings.Repeat("1", 32)
	instance, err := s.Instance()
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ordjson.FromValue(map[string]any{
		"schema": 1, "installation": root, "home": s.Home, "instance": instance,
		"known_good": map[string]any{"kind": "checkout", "sha": strings.Repeat("a", 40), "path": root},
		"pending": map[string]any{"generation": generation, "recovery": map[string]any{
			"helper": helper, "sha256": strings.Repeat("0", 64), "argv": []any{helper, "--home", s.Home, "update", "recover", "--generation", generation, "--claim", session.Token()},
		}},
	})
	if err := os.MkdirAll(filepath.Join(root, ".local"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := ordjson.WriteFile(filepath.Join(root, ".local", "activation.json"), state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command := NewRoot(helper, &stdout, &stderr)
	command.SetArgs([]string{"--home", s.Home, "update", "rollback", "--claim", session.Token()})
	err = command.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("pending rollback succeeded")
	}
	message := err.Error()
	start, end := strings.Index(message, "`"), strings.LastIndex(message, "`")
	if start < 0 || end <= start {
		t.Fatalf("missing recovery argv: %s", message)
	}
	printed := message[start+1 : end]
	if !strings.Contains(printed, "--claim "+session.Token()) || !strings.Contains(printed, helper) {
		t.Fatalf("incomplete argv: %s", printed)
	}
	parse := exec.Command("sh", "-c", "set -- "+printed+"; printf '%s\\0' \"$@\"")
	output, err := parse.Output()
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	command = NewRoot(argv[0], &stdout, &stderr)
	command.SetArgs(argv[1:])
	err = command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "generation recovery helper is missing") {
		t.Fatalf("printed recovery did not pass parsing and claim validation: %v", err)
	}
}
