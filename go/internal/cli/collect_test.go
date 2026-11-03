package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func TestCollectCommandHelpAndBudget(t *testing.T) {
	var out, stderr bytes.Buffer
	root := NewRoot("sumctl", &out, &stderr)
	root.SetArgs([]string{"help", "collect"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "collect --claim TOKEN") || !strings.Contains(out.String(), "20s") {
		t.Fatalf("collect help: %s", out.String())
	}
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "budget validation", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []string{"0s", "-1s", "61s", "1ms"} {
		root := &cobra.Command{Use: "sumctl", SilenceUsage: true, SilenceErrors: true}
		root.SetOut(&out)
		options := rootOptions{home: s.Root, claim: session.Token()}
		options.addCollectCommand(root)
		root.SetArgs([]string{"collect", "--budget", budget})
		err := root.Execute()
		if budget == "1ms" {
			if err != nil {
				t.Fatalf("valid budget refused: %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "--budget") {
			t.Fatalf("budget %s: expected budget usage error, got %v", budget, err)
		}
	}
}

func TestCollectAcceptsPersistentClaimOnEitherSideOfCommand(t *testing.T) {
	for _, after := range []bool{false, true} {
		runtime, s := ordinaryInitLab(t)
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		session, err := s.ClaimCoordinator("", "flag placement", false)
		if err != nil {
			t.Fatal(err)
		}
		var out, stderr bytes.Buffer
		root := NewRoot(runtime+"/bin/sumctl", &out, &stderr)
		args := []string{"--home", s.Root, "--format", "json"}
		if !after {
			args = append(args, "--claim", session.Token())
		}
		args = append(args, "collect", "--budget", "1ms")
		if after {
			args = append(args, "--claim", session.Token())
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("claim after=%v: %v %s", after, err, stderr.String())
		}
	}
}
