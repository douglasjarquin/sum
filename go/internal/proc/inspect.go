package proc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

type CWDProcess struct {
	PID int
	CWD string
}

func Descendants(pid int) ([]int, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid")
	}
	out, err := exec.Command("ps", "-ax", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil, fmt.Errorf("process table cannot be inspected: %s", err)
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		child, err1 := strconv.Atoi(fields[0])
		parent, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[parent] = append(children[parent], child)
	}
	seen := map[int]bool{}
	var walk func(int)
	var ids []int
	walk = func(parent int) {
		for _, child := range children[parent] {
			if seen[child] {
				continue
			}
			seen[child] = true
			ids = append(ids, child)
			walk(child)
		}
	}
	walk(pid)
	return ids, nil
}

func ProcessArgv(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid")
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return nil, fmt.Errorf("pid %d argv cannot be inspected: %s", pid, err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, fmt.Errorf("pid %d argv cannot be inspected", pid)
	}
	return strings.Fields(line), nil
}

func ProcessesIn(worktree string, exclude map[int]bool) ([]CWDProcess, error) {
	if worktree == "" {
		return nil, fmt.Errorf("checkout path is missing")
	}
	lsof, err := toolpath.Find("", "lsof")
	if err != nil {
		return nil, err
	}
	out, runErr := Run([]string{lsof, "-a", "-d", "cwd", "-Fpn", "-w"}, "", 30*time.Second, false, nil)
	if runErr != nil && out.Stdout == "" {
		return nil, runErr
	}
	var rows []CWDProcess
	var pid int
	self := os.Getpid()
	for _, line := range strings.Split(out.Stdout, "\n") {
		if strings.HasPrefix(line, "p") {
			fmt.Sscanf(line[1:], "%d", &pid)
		} else if strings.HasPrefix(line, "n") && pid != 0 {
			rows = append(rows, CWDProcess{PID: pid, CWD: line[1:]})
		}
	}
	if len(rows) == 0 {
		detail := strings.TrimSpace(out.Stderr)
		if len(detail) > 200 {
			detail = detail[len(detail)-200:]
		}
		return nil, fmt.Errorf("lsof exited %d without a process table: %s", out.Code, detail)
	}
	roots := []string{worktree}
	if real, err := filepath.EvalSymlinks(worktree); err == nil {
		roots = append(roots, real)
	}
	var inside []CWDProcess
	for _, row := range rows {
		if row.PID == self || exclude[row.PID] {
			continue
		}
		for _, root := range roots {
			if row.CWD == root || strings.HasPrefix(row.CWD, root+"/") {
				inside = append(inside, row)
				break
			}
		}
	}
	return inside, nil
}

func ArgvEqual(recorded any, live []string) bool {
	want := normalizeArgv(recorded)
	got := normalizeArgv(anyStrings(live))
	if want == nil || got == nil || len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

func normalizeArgv(argv any) []string {
	list, ok := argv.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s := fmt.Sprint(item)
		if s == "" {
			return nil
		}
		if i == 0 {
			s = filepath.Base(s)
		}
		out = append(out, s)
	}
	return out
}

func anyStrings(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
