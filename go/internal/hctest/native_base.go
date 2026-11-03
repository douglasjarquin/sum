package hctest

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

func BuildNativeBaseHelper(t testing.TB, root string) string {
	t.Helper()
	const sha = "8a4300c646cb6dcea8ca6f19f9199268b3adb6e3"
	materialized := t.TempDir()
	archivePath := filepath.Join(materialized, "base.tar")
	_, err := proc.Run([]string{"git", "-C", root, "archive", "--format=tar", "--output", archivePath, sha, "go"}, "", 30*time.Second, true, proc.ScrubbedEnv())
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	reader := tar.NewReader(archive)
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
	output, err := proc.Run([]string{filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-buildvcs=false", "-o", helper, "./cmd/sumctl"}, filepath.Join(materialized, "go"), 900*time.Second, true, append(proc.ScrubbedEnv(), "GOTOOLCHAIN=local", "CGO_ENABLED=0"))
	if err != nil {
		t.Fatalf("build immutable base helper %s: %v\n%s", sha, err, output.Detail())
	}
	version, err := proc.Run([]string{helper, "--format", "json", "release-contract"}, "", 20*time.Second, true, proc.ScrubbedEnv())
	if err != nil {
		t.Fatal(err)
	}
	var offered struct {
		Supports struct {
			StateSchema []int `json:"state_schema"`
		} `json:"supports"`
	}
	if err := json.Unmarshal([]byte(version.Stdout), &offered); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(offered.Supports.StateSchema) != "[2]" {
		t.Fatalf("base %s is no longer the native-only helper: %s", sha, version.Stdout)
	}
	return helper
}
