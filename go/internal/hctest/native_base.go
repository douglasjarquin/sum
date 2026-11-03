package hctest

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

func BuildNativeBaseHelper(t testing.TB, root string) string {
	t.Helper()
	const sha = "8a4300c646cb6dcea8ca6f19f9199268b3adb6e3"
	archive, err := exec.Command("git", "-C", root, "archive", "--format=tar", sha, "go").Output()
	if err != nil {
		t.Fatal(err)
	}
	materialized := t.TempDir()
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		path := filepath.Join(materialized, header.Name)
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("unexpected base archive entry %s", header.Name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, os.FileMode(header.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(materialized, ".local", "bin", "sumctl")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-buildvcs=false", "-o", helper, "./cmd/sumctl")
	cmd.Dir = filepath.Join(materialized, "go")
	cmd.Env = append(proc.ScrubbedEnv(), "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build immutable base helper %s: %v\n%s", sha, err, output)
	}
	version := exec.Command(helper, "--format", "json", "release-contract")
	version.Env = proc.ScrubbedEnv()
	raw, err := version.Output()
	if err != nil {
		t.Fatal(err)
	}
	var offered struct {
		Supports struct {
			StateSchema []int `json:"state_schema"`
		} `json:"supports"`
	}
	if err := json.Unmarshal(raw, &offered); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(offered.Supports.StateSchema) != "[2]" {
		t.Fatalf("base %s is no longer the native-only helper: %s", sha, raw)
	}
	return helper
}
