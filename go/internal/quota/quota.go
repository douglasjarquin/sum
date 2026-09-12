package quota

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

func Run(runtimeRoot, provider, format string, stdout, stderr io.Writer) (int, error) {
	var argv []string
	if provider == "codex" {
		binary, err := toolpath.Find(runtimeRoot, "remainder")
		if err != nil || !isFile(binary) {
			return 1, fmt.Errorf("Missing remainder for Codex quota. Run mise run setup. quota-axi is not used for this provider.")
		}
		if format == "" {
			format = "toon"
		}
		argv = []string{binary, "--provider", "codex", "--profile", "default", "--format", format}
	} else {
		binary, err := toolpath.Find(runtimeRoot, "quota-axi")
		if err != nil {
			return 1, err
		}
		argv = []string{binary, "--provider", provider}
	}
	result, err := proc.Run(argv, "", 0, false, nil)
	if err != nil && result.Code == 0 {
		return 1, err
	}
	if _, writeErr := io.WriteString(stdout, result.Stdout); writeErr != nil {
		return 1, writeErr
	}
	if _, writeErr := io.WriteString(stderr, result.Stderr); writeErr != nil {
		return 1, writeErr
	}
	code := result.Code
	if code < 0 {
		return 128 + (-code), nil
	}
	return code, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func BinaryName(path string) string {
	return filepath.Base(path)
}
