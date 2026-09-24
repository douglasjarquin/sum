package proc

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A long argv survives a narrow terminal width: ps would otherwise cut `args` to $COLUMNS even when piped.
func TestProcessTableKeepsFullArgsUnderNarrowColumns(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	marker := filepath.Join(t.TempDir(), strings.Repeat("deep-directory-name/", 12), "checkout path with spaces")
	// A compound command keeps sh (and its argv, marker included) alive instead of exec-ing sleep.
	cmd := exec.Command("sh", "-c", "sleep 30; :", marker)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	rows, err := ProcessTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.PID == cmd.Process.Pid {
			if !strings.Contains(row.Args, marker) {
				t.Fatalf("args truncated: %q", row.Args)
			}
			return
		}
	}
	t.Fatalf("pid %d not in the process table", cmd.Process.Pid)
}
