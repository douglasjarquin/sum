package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

// processTableTimeout bounds each process-table read. A timed-out, truncated, or failed read is an error, never
// a shorter table: callers keep a reservation held rather than conclude nothing is running.
const processTableTimeout = 30 * time.Second

type CWDProcess struct {
	PID int
	CWD string
}

// BoundProcess is a live process provably bound to a checkout: its cwd is
// inside it, or its argv names the checkout path (for example a detached
// `serve --mcp --path <checkout>` daemon whose cwd lies elsewhere). Bound is
// "cwd" or "argv".
type BoundProcess struct {
	PID   int
	CWD   string
	Bound string
}

func Descendants(pid int) ([]int, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid")
	}
	out, err := Run([]string{"ps", "-ax", "-o", "pid=,ppid="}, "", processTableTimeout, true, nil)
	if err != nil {
		return nil, fmt.Errorf("process table cannot be inspected: %s", err)
	}
	children := map[int][]int{}
	for _, line := range strings.Split(out.Stdout, "\n") {
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
	out, err := Run([]string{"ps", "-p", strconv.Itoa(pid), "-o", "args="}, "", processTableTimeout, true, nil)
	if err != nil {
		return nil, fmt.Errorf("pid %d argv cannot be inspected: %s", pid, err)
	}
	line := strings.TrimSpace(out.Stdout)
	if line == "" {
		return nil, fmt.Errorf("pid %d argv cannot be inspected", pid)
	}
	return strings.Fields(line), nil
}

func cwdProcesses() ([]CWDProcess, error) {
	lsof, err := toolpath.Find("", "lsof")
	if err != nil {
		return nil, err
	}
	out, runErr := Run([]string{lsof, "-a", "-d", "cwd", "-Fpn", "-w"}, "", processTableTimeout, false, nil)
	if runErr != nil {
		return nil, runErr
	}
	var rows []CWDProcess
	var pid int
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
	return rows, nil
}

func checkoutRoots(worktree string) []string {
	roots := []string{worktree}
	if real, err := filepath.EvalSymlinks(worktree); err == nil {
		roots = append(roots, real)
	}
	return roots
}

func pathWithinRoots(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func ProcessesIn(worktree string, exclude map[int]bool) ([]CWDProcess, error) {
	if worktree == "" {
		return nil, fmt.Errorf("checkout path is missing")
	}
	rows, err := cwdProcesses()
	if err != nil {
		return nil, err
	}
	roots := checkoutRoots(worktree)
	self := os.Getpid()
	var inside []CWDProcess
	for _, row := range rows {
		if row.PID == self || exclude[row.PID] {
			continue
		}
		if pathWithinRoots(row.CWD, roots) {
			inside = append(inside, row)
		}
	}
	return inside, nil
}

// ProcessesBoundTo returns live processes provably bound to the checkout: a cwd
// inside it, or an argv field that names the checkout path or a path inside it
// (the `serve --mcp --path <checkout>` form, including the `--path=<checkout>`
// spelling). Cwd-bound rows come first; argv-bound rows repeat their cwd when
// the process table reports one.
func ProcessesBoundTo(worktree string, exclude map[int]bool) ([]BoundProcess, error) {
	if worktree == "" {
		return nil, fmt.Errorf("checkout path is missing")
	}
	rows, err := cwdProcesses()
	if err != nil {
		return nil, err
	}
	roots := checkoutRoots(worktree)
	self := os.Getpid()
	cwdOf := map[int]string{}
	for _, row := range rows {
		cwdOf[row.PID] = row.CWD
	}
	var bound []BoundProcess
	seen := map[int]bool{}
	for _, row := range rows {
		if row.PID == self || exclude[row.PID] {
			continue
		}
		if pathWithinRoots(row.CWD, roots) {
			bound = append(bound, BoundProcess{PID: row.PID, CWD: row.CWD, Bound: "cwd"})
			seen[row.PID] = true
		}
	}
	table, err := Run([]string{"ps", "-ax", "-o", "pid=,args="}, "", processTableTimeout, true, nil)
	if err != nil {
		return nil, fmt.Errorf("process table cannot be inspected: %s", err)
	}
	for _, line := range strings.Split(table.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, convErr := strconv.Atoi(fields[0])
		if convErr != nil || pid <= 0 || pid == self || exclude[pid] || seen[pid] {
			continue
		}
		for _, field := range fields[1:] {
			if pathWithinRoots(field, roots) || pathWithinRoots(argValue(field), roots) {
				bound = append(bound, BoundProcess{PID: pid, CWD: cwdOf[pid], Bound: "argv"})
				seen[pid] = true
				break
			}
		}
	}
	return bound, nil
}

// argValue returns the value half of a `--flag=value` argv field so a daemon
// spelling its checkout as `--path=<checkout>` binds like `--path <checkout>`.
func argValue(field string) string {
	if i := strings.IndexByte(field, '='); i >= 0 {
		return field[i+1:]
	}
	return ""
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
