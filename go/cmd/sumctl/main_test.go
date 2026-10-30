package main

import (
	"bytes"
	"encoding/json"
	"github.com/douglasjarquin/sum/go/internal/store"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompiledEntrypointCancellationExitsOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal semantics differ on Windows")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "sumctl")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, ".")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}

	stableHome := filepath.Join(dir, "state")
	state, err := store.Open(stableHome)
	if err != nil {
		t.Fatal(err)
	}
	if err = state.Init(); err != nil {
		t.Fatal(err)
	}
	snapshot := func(t *testing.T) map[string]string {
		t.Helper()
		files := map[string]string{}
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				files[path+string(os.PathSeparator)] = "directory"
				return nil
			}
			if path == binary {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(body)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	var environment []string
	for _, pair := range os.Environ() {
		if strings.HasPrefix(pair, "SUM_") || strings.HasPrefix(pair, "HC_") || strings.HasPrefix(pair, "HERDR_") {
			continue
		}
		environment = append(environment, pair)
	}
	for _, scenario := range []struct{ name, home, format string }{
		{"physical-generation-toon", state.Home, ""},
		{"physical-generation-json", state.Home, "json"},
		{"uninitialized-toon", filepath.Join(dir, "uninitialized"), ""},
		{"uninitialized-json", filepath.Join(dir, "uninitialized"), "json"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			before := snapshot(t)
			args := []string{"--home", scenario.home}
			if scenario.format != "" {
				args = append(args, "--format", scenario.format)
			}
			args = append(args, "help")
			command := exec.Command(binary, args...)
			command.Env = environment
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("help failed: %v\n%s", err, stderr.String())
				}
			case <-time.After(5 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Fatal("compiled helper did not exit")
			}
			if stderr.Len() != 0 {
				t.Fatalf("help wrote stderr: %s", stderr.String())
			}
			if scenario.format == "json" {
				var view map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
					t.Fatalf("invalid JSON help: %v\n%s", err, stdout.String())
				}
				if _, ok := view["commands"]; !ok {
					t.Fatalf("JSON help missing commands: %s", stdout.String())
				}
			} else if !strings.Contains(stdout.String(), "commands:") {
				t.Fatalf("TOON help missing commands: %s", stdout.String())
			}
			if after := snapshot(t); !reflect.DeepEqual(before, after) {
				t.Fatalf("help changed isolated state: before=%v after=%v", before, after)
			}
		})
	}
}
