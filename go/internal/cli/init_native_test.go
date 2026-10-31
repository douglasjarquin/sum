package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/spf13/cobra"
)

func TestNativeDevelopmentInitDoesNotObserveInheritedWorker(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".sum")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "dev.json"), []byte(`{"schema":1,"kind":"development"}`), 0600); err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	f.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live"})
	for key, value := range map[string]string{"SUM_ROLE": "worker", "SUM_TASK": "t-aaaaaaaaaaaa", "SUM_ATTEMPT": "attempt", "SUM_BINDING_GENERATION": "1", "SUM_SESSION": "developer-lab", "HC_SOCKET": f.Socket, "SUM_CORE_ID": f.CoreID, "HC_TERMINAL_ID": "t1"} {
		t.Setenv(key, value)
	}
	var output bytes.Buffer
	command := &cobra.Command{Use: "sumctl"}
	command.SetOut(&output)
	options := rootOptions{runtimeRoot: root, home: home}
	options.addInitCommand(command)
	command.SetArgs([]string{"init"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "developer") {
		t.Fatalf("output %s", output.String())
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("developer init observed inherited runtime: %v", calls)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "dev.json" {
		t.Fatalf("developer init wrote state: %v", entries)
	}
}
