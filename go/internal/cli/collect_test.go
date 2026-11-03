package cli

import (
	"bytes"
	"strings"
	"testing"

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
	for _, budget := range []string{"0s", "-1s", "61s"} {
		root := &cobra.Command{Use: "sumctl", SilenceUsage: true, SilenceErrors: true}
		options := rootOptions{home: t.TempDir()}
		options.addCollectCommand(root)
		root.SetArgs([]string{"collect", "--budget", budget})
		if err := root.Execute(); err == nil {
			t.Fatalf("invalid budget %s accepted", budget)
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
