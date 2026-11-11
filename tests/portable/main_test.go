package skilltest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sum-verify-bin-")
	if err != nil {
		panic(err)
	}
	toolBin = filepath.Join(dir, "sum-verify")
	build := exec.Command("go", "build", "-o", toolBin, "./cmd/sum-verify")
	build.Dir = filepath.Join("..", "..", "tools", "verify")
	if out, err := build.CombinedOutput(); err != nil {
		panic("build sum-verify: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
