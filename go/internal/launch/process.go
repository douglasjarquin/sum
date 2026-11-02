package launch

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func sameExecutable(a, b string) bool {
	resolve := func(path string) string {
		if !filepath.IsAbs(path) {
			found, err := exec.LookPath(path)
			if err != nil {
				return ""
			}
			path = found
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return ""
		}
		return resolved
	}
	a, b = resolve(a), resolve(b)
	return a != "" && a == b
}

func LaunchProgramMatches(spec HCTemplate, actual []string) bool {
	if len(actual) == len(spec.Argv) && len(actual) > 0 && sameExecutable(spec.Binary, actual[0]) && slices.Equal(actual[1:], spec.Argv[1:]) {
		return true
	}
	file, err := os.Open(spec.Binary)
	if err != nil {
		return false
	}
	defer file.Close()
	prefix, err := io.ReadAll(io.LimitReader(file, 1024))
	if err != nil {
		return false
	}
	line, _, _ := strings.Cut(string(prefix), "\n")
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	interpreter := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if len(interpreter) == 2 && interpreter[0] == "/usr/bin/env" {
		interpreter = interpreter[1:]
	}
	if len(interpreter) != 1 || len(actual) != len(spec.Argv)+1 || !sameExecutable(interpreter[0], actual[0]) || !sameExecutable(spec.Binary, actual[1]) {
		return false
	}
	return slices.Equal(actual[2:], spec.Argv[1:])
}

func sameDirectory(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	first, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	second, err := filepath.EvalSymlinks(b)
	return err == nil && first == second
}
