package environment

import (
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"testing"
)

func TestNativeProcessPreservesProgramAndForegroundBinding(t *testing.T) {
	pid := uint32(100)
	terminal := hcclient.Terminal{PID: &pid, Argv: []string{"npm", "run", "dev"}, Cwd: "/tmp/project", Foreground: &hcclient.Foreground{PID: 101, Name: "node", Argv: []string{"node", "server.js"}, Cwd: "/tmp/project"}}
	record := nativeProcess(terminal)
	if intField(record, "pid") != 100 || intField(record, "foreground_pid") != 101 || len(listField(record, "argv")) != 3 || len(listField(record, "foreground_argv")) != 2 {
		t.Fatal("native process binding lost", record)
	}
	terminal.Foreground = nil
	record = nativeProcess(terminal)
	if intField(record, "foreground_pid") != 0 {
		t.Fatal("missing foreground fabricated")
	}
}
