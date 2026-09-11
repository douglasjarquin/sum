package toolpath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Find(runtimeRoot, name string) (string, error) {
	envName := "SUM_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BIN"
	if override := os.Getenv(envName); override != "" {
		return override, nil
	}
	local := filepath.Join(runtimeRoot, ".local", "bin", name)
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		return local, nil
	}
	if found, err := exec.LookPath(name); err == nil {
		return found, nil
	}
	return "", fmt.Errorf("Missing %s. Run mise run setup.", name)
}
