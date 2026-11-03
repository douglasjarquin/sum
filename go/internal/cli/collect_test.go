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
