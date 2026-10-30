package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// TestMeasureCoordinationPasses records successful stdout bytes, wall time, task reads, and helper/HC/gh calls of the fast coordination
// commands over synthesized homes of 1, 12, and 100 tasks (one third archived; active tasks mixing open questions,
// unapplied answers, reports, open PRs, and merged cleanup-pending tasks), with healthy fakes and with a slow
// dependency. It runs only when SUM_MEASURE_OUT names an output directory; SUM_MEASURE_BINS adds binaries to compare
// as name=path[,name=path] (the candidate helper is always measured). Counts of task reads and subprocesses come from
// one strace'd run per cell when strace is available. The integrated 12-worker replay (TestQuietCoordinationReplay)
// records its own prompt, byte and call measurements over records it produced through real CLI operations; when it
// wrote replay.json into the same SUM_MEASURE_OUT, raw.json points at it so the two are read together.
func TestMeasureCoordinationPasses(t *testing.T) {
	out := os.Getenv("SUM_MEASURE_OUT")
	if out == "" {
		t.Skip("set SUM_MEASURE_OUT to record coordination measurements")
	}
	root, _ := repoReference(t)
	binaries := [][2]string{{"candidate", filepath.Join(root, ".local", "bin", "sumctl")}}
	for _, spec := range strings.Split(os.Getenv("SUM_MEASURE_BINS"), ",") {
		if name, path, ok := strings.Cut(strings.TrimSpace(spec), "="); ok {
			binaries = append(binaries, [2]string{name, path})
		}
	}
	samples := envInt("SUM_MEASURE_SAMPLES", 11)
	slowSamples := envInt("SUM_MEASURE_SLOW_SAMPLES", 3)
	_, straceErr := exec.LookPath("strace")
	type cell struct {
		Binary              string    `json:"binary"`
		Tasks               int       `json:"tasks"`
		Active              int       `json:"active"`
		Scenario            string    `json:"scenario"`
		Command             string    `json:"command"`
		Samples             []float64 `json:"wall_ms"`
		P50                 *float64  `json:"p50_ms"`
		P95                 *float64  `json:"p95_ms"`
		Max                 *float64  `json:"max_ms"`
		TaskReads           *int      `json:"task_reads"`
		Subprocess          *int      `json:"subprocesses"`
		HCCalls             int       `json:"hc_calls"`
		GHCalls             int       `json:"gh_calls"`
		OutputBytes         []int     `json:"stdout_bytes"`
		Unavailable         string    `json:"unavailable,omitempty"`
		ExitCodes           []int     `json:"exit_codes"`
		Deferred            int       `json:"deferred"`
		StraceNotes         string    `json:"strace,omitempty"`
		SlowRequestObserved bool      `json:"slow_native_request_observed"`
	}
	var cells []cell
	commands := [][]string{{"init"}, {"inbox", "--live"}, {"pump"}, {"status"}, {"status", "--compact"}, {"inbox", "--compact"}, {"inbox", "--compact", "--live"}}
	scenarios := []string{"healthy", "slow"}
	for _, bin := range binaries {
		for _, n := range []int{1, 12, 100} {
			for _, scenario := range scenarios {
				for _, command := range commands {
					count := samples
					if scenario == "slow" {
						count = slowSamples
						if bin[0] != "candidate" && n == 100 {
							count = 1
						}
					}
					fx := newMeasureFixture(t, root, n, scenario)
					c := cell{Binary: bin[0], Tasks: n, Active: fx.active, Scenario: scenario, Command: strings.Join(command, " ")}
					for i := 0; i < count; i++ {
						run := fx.fresh(t)
						started := time.Now()
						code, stdout := run.exec(bin[1], "", command...)
						c.ExitCodes = append(c.ExitCodes, code)
						if code != 0 {
							if bin[0] == "candidate" {
								t.Fatalf("candidate %s failed with exit %d; failed output is not a measurement: %s", c.Command, code, run.stderr)
							}
							c.Unavailable = "a sample failed or the comparison binary does not support this command; failed output excluded"
							break
						}
						c.Samples = append(c.Samples, float64(time.Since(started).Microseconds())/1000)
						c.OutputBytes = append(c.OutputBytes, len([]byte(stdout)))
						if i == 0 {
							c.HCCalls = len(run.native.Calls())
							c.GHCalls = measureLines(filepath.Join(run.base, "fake-gh", "calls.jsonl"))
							c.Deferred = measureDeferred(t, stdout)
							if scenario == "slow" {
								for _, call := range run.native.Calls() {
									if call.Method == "terminal.get" {
										c.SlowRequestObserved = true
										break
									}
								}
							}
						}
					}
					if len(c.Samples) > 0 && straceErr == nil {
						run := fx.fresh(t)
						trace := filepath.Join(run.base, "strace.txt")
						code, _ := run.exec(bin[1], trace, command...)
						if code != 0 {
							t.Fatalf("instrumented %s %s failed with exit %d", bin[0], c.Command, code)
						}
						reads, subprocesses := parseStrace(t, trace)
						c.TaskReads, c.Subprocess = &reads, &subprocesses
					} else {
						if straceErr != nil {
							c.StraceNotes = "strace unavailable; task reads and subprocesses not counted"
						} else {
							c.StraceNotes = "no successful samples; trace not run"
						}
					}
					if len(c.Samples) > 0 {
						p50, p95, max := percentile(c.Samples, 50), percentile(c.Samples, 95), percentile(c.Samples, 100)
						c.P50, c.P95, c.Max = &p50, &p95, &max
					}
					cells = append(cells, c)
					t.Logf("%s n=%d %s %-12s p50=%sms p95=%sms reads=%s subprocs=%s hc=%d gh=%d deferred=%d exits=%v",
						c.Binary, c.Tasks, c.Scenario, c.Command, measureFloat(c.P50), measureFloat(c.P95), measureCount(c.TaskReads), measureCount(c.Subprocess), c.HCCalls, c.GHCalls, c.Deferred, c.ExitCodes)
					t.Logf("stdout bytes=%v unavailable=%s", c.OutputBytes, c.Unavailable)
				}
			}
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	replay := "not recorded in this directory: run TestQuietCoordinationReplay with the same SUM_MEASURE_OUT; it measures its own 12-task home"
	if _, err := os.Stat(filepath.Join(out, "replay.json")); err == nil {
		replay = filepath.Join(out, "replay.json")
	}
	raw, err := json.MarshalIndent(map[string]any{"schema": 2, "generated_at": time.Now().UTC().Format(time.RFC3339), "cells": cells, "replay": replay, "scope": "native socket fixtures and synthetic schema2 task workload; no model, provider or installation state", "counts": "HC/gh counts describe the first successful sample per cell; strace counts use one additional instrumented run", "slow_dependency": "the first terminal.get request is delayed six seconds; actual observation is recorded per cell", "host_load": "uncontrolled shared development host; descriptive samples, not latency guarantees"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "raw.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var table bytes.Buffer
	fmt.Fprintln(&table, "| Binary | Tasks (active) | Scenario | Command | Samples | p50 ms | p95 ms | Task reads | Subprocesses | HC calls | gh calls | Deferred | Stdout bytes | Unavailable |")
	fmt.Fprintln(&table, "| --- | ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |")
	for _, c := range cells {
		fmt.Fprintf(&table, "| %s | %d (%d) | %s | `%s` | %d | %s | %s | %s | %s | %d | %d | %d | %v | %s |\n",
			c.Binary, c.Tasks, c.Active, c.Scenario, c.Command, len(c.Samples), measureFloat(c.P50), measureFloat(c.P95), measureCount(c.TaskReads), measureCount(c.Subprocess), c.HCCalls, c.GHCalls, c.Deferred, c.OutputBytes, c.Unavailable)
	}
	if err := os.WriteFile(filepath.Join(out, "table.md"), table.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type measureFixture struct {
	root     string
	tasks    int
	scenario string
	active   int
}

type measureRun struct {
	base   string
	home   string
	env    []string
	native *hctest.Fixture
	stderr string
}

// newMeasureFixture describes a schema-two native workload. Every sample builds
// an independent generation and socket fixture before the timed command starts.
func newMeasureFixture(t *testing.T, root string, n int, scenario string) *measureFixture {
	t.Helper()
	active := 0
	for i := 0; i < n; i++ {
		if i%3 != 2 {
			active++
		}
	}
	return &measureFixture{root: root, tasks: n, scenario: scenario, active: active}
}

func measureWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	object, err := ordjson.FromValue(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(path, object); err != nil {
		t.Fatal(err)
	}
}

func measureTemplateCommand(spec launch.HCTemplate) string {
	quoted := make([]string, len(spec.Argv))
	for i, arg := range spec.Argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

func (f *measureFixture) fresh(t *testing.T) *measureRun {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	native := hctest.New(t)
	config, codexHome := filepath.Join(base, "hc-config"), filepath.Join(base, "codex-home")
	for _, dir := range []string{config, codexHome, filepath.Join(base, "fake-gh"), filepath.Join(base, "home")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	codex := filepath.Join(base, "codex")
	if err = os.WriteFile(codex, []byte("#!/bin/sh\n[ \"$1\" = --version ] || exit 99\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_CODEX_BIN", codex)
	t.Setenv("CODEX_HOME", codexHome)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	templates := map[string]hcclient.TemplateInfo{}
	var coordinator store.Endpoint
	bind := func(address, role, task, attempt, cwd string, created uint64) (store.Endpoint, *ordjson.Object, *ordjson.Object) {
		t.Helper()
		options := launch.HCTemplateOptions{Cwd: cwd, CodexHome: codexHome}
		spec, err := launch.PrepareHCTemplate(f.root, config, "codex", nil, options)
		if err != nil {
			t.Fatal(err)
		}
		command := measureTemplateCommand(spec)
		templates[spec.Name] = hcclient.TemplateInfo{Name: spec.Name, Command: command}
		pid := uint32(100000 + created*10)
		foreground := &hcclient.Foreground{PID: int32(pid + 1), Name: "codex", Argv: spec.Argv, Cwd: cwd}
		name, key := spec.Name, "tab"
		native.SetTerminal(hcclient.Terminal{Address: address, CreatedMS: created, Cwd: cwd, State: "live", PID: &pid, Foreground: foreground, Template: &name, Submit: &key, Argv: []string{"sh", "-c", command}})
		endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: native.CoreID, Session: "sum-test-measure", Socket: native.Socket, Address: address, CreatedMS: created, Cwd: cwd, Role: role, Task: task, Attempt: attempt, BindingGeneration: 1}
		evidence := (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: foreground}).Record(store.Now())
		receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: role + "-init", Attempt: attempt})
		var taskValue any = task
		if task == "" {
			taskValue = nil
		}
		registration, err := s.Register(endpoint, role, taskValue, evidence)
		if err != nil {
			t.Fatal(err)
		}
		provenance, _ := ordjson.FromValue(prepare.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
		registration.Set("native_receipt", receipt)
		registration.Set("native_template", provenance)
		if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
			t.Fatal(err)
		}
		if role == "coordinator" {
			owner := endpoint.Object()
			owner.Set("incarnation", evidence)
			owner.Set("native_receipt", receipt)
			owner.Set("native_template", provenance)
			launchRecord := ordjson.NewObject()
			launchRecord.Set("harness", "codex")
			launchRecord.Set("model", nil)
			launchRecord.Set("reasoning", nil)
			owner.Set("launch", launchRecord)
			if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
				t.Fatal(err)
			}
		}
		return endpoint, evidence, receipt
	}
	coordinator, _, _ = bind("t1", "coordinator", "", "bootstrap", f.root, 1)
	native.SetStatus("t1", "idle")
	measureWriteJSON(t, filepath.Join(s.Home, "settings.json"), map[string]any{"schema": 1, "hook": map[string]any{"enabled": false}})
	repo := filepath.Join(base, "repo")
	for _, argv := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "-c", "user.email=m@example.invalid", "-c", "user.name=m", "commit", "-q", "--allow-empty", "-m", "base"}} {
		if output, err := exec.Command("git", argv...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", argv, err, output)
		}
	}
	active := 0
	for i := 0; i < f.tasks; i++ {
		id, address, attemptID := fmt.Sprintf("t-%012x", i+1), fmt.Sprintf("t%d", i+2), fmt.Sprintf("x-%012x", i+1)
		worktree := filepath.Join(base, "wt", id)
		if err = os.MkdirAll(worktree, 0700); err != nil {
			t.Fatal(err)
		}
		endpoint, evidence, receipt := bind(address, "worker", id, attemptID, worktree, uint64(i+2))
		attempt, err := reservations.NewAttempt("worker", coordinator.Object(), worktree, store.Now(), "running", nil)
		if err != nil {
			t.Fatal(err)
		}
		attempt.Set("id", attemptID)
		attempt.Set("incarnation", evidence)
		attempt.Set("native_receipt", receipt)
		task := map[string]any{"schema": store.Schema, "id": id, "status": "running", "repository": repo, "machine": host.ID, "worker_endpoint": endpoint.Object(), "worktree": worktree, "branch": "sum/" + id,
			"parent": coordinator.Object(), "questions": []any{}, "evidence": []any{}, "report": nil, "notice": nil, "attention": []any{}, "brief": "measure", "base_sha": strings.Repeat("0", 40), "kind": "ship", "execution": reservations.NewExecution(attempt)}
		status := "working"
		if i%3 == 2 {
			task["status"] = "archived"
			task["questions"] = []any{map[string]any{"id": "q-old", "status": "applied", "created_at": "2026-09-05T00:00:00+00:00"}}
			attempt.Set("state", "released")
			native.Exit(address, 0)
		} else {
			kind := active % 5
			active++
			switch kind {
			case 0:
				task["questions"] = []any{map[string]any{"id": "q-1", "status": "open", "text": "Which way?", "created_at": "2026-09-05T00:00:00+00:00"}}
			case 1:
				task["questions"] = []any{map[string]any{"id": "q-1", "status": "answered", "text": "Which way?", "answer": "This way.", "created_at": "2026-09-05T00:00:00+00:00", "answered_at": "2026-09-05T00:01:00+00:00"}}
				status = "idle"
			case 2:
				task["evidence"] = []any{map[string]any{"id": "e-1", "kind": "report", "source": "worker", "endpoint": endpoint.Object(), "at": "2026-09-05T00:02:00+00:00"}}
				task["report"] = map[string]any{"text": "done", "submitted_at": "2026-09-05T00:02:00+00:00"}
			case 3, 4:
				pr := map[string]any{"complete": true, "merged_for_task": kind == 4, "state": "open", "observed_at": "2026-09-05T00:03:00+00:00", "identity": map[string]any{"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7", "head_sha": strings.Repeat("a", 40), "head_branch": "sum/" + id, "base_branch": "main"}, "findings": []any{}}
				if kind == 4 {
					pr["state"] = "merged"
					pr["merge_commit"] = strings.Repeat("b", 40)
				}
				task["pr"] = pr
			}
			native.SetStatus(address, status)
		}
		measureWriteJSON(t, filepath.Join(s.Tasks, id, "task.json"), task)
	}
	nativeTemplates := make([]hcclient.TemplateInfo, 0, len(templates))
	for _, spec := range templates {
		nativeTemplates = append(nativeTemplates, spec)
	}
	native.SetTemplates(nativeTemplates)
	gh := map[string]any{"number": 7, "repository": "douglasjarquin/project", "state": "OPEN", "head_branch": "sum/x", "head_sha": strings.Repeat("a", 40)}
	if f.scenario == "slow" {
		gh["delay"] = 5
		native.QueueFault("terminal.get", hctest.Fault{Delay: 6 * time.Second})
	}
	measureWriteJSON(t, filepath.Join(base, "fake-gh", "pr.json"), gh)
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "SUM_") || strings.HasPrefix(key, "HC_") || strings.HasPrefix(key, "HERDR_") || strings.HasPrefix(key, "FAKE_") || key == "HOME" || key == "CODEX_HOME" {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+filepath.Join(base, "home"), "CODEX_HOME="+codexHome, "SUM_INSTALL_ROOT="+f.root, "SUM_SESSION="+coordinator.Session, "SUM_CORE_ID="+native.CoreID, "HC_SOCKET="+native.Socket, "HC_TERMINAL_ID=t1", "SUM_ROLE=coordinator", "SUM_TASK=", "SUM_ATTEMPT=bootstrap", "SUM_BINDING_GENERATION=1", "SUM_CODEX_BIN="+codex, "SUM_GH_BIN="+filepath.Join(f.root, "tests/fixtures/gh.py"), "FAKE_GH_ROOT="+filepath.Join(base, "fake-gh"))
	return &measureRun{base: base, home: s.Home, env: env, native: native}
}

// exec runs one sumctl command (under strace when trace is set) and returns its exit code and stdout.
func (r *measureRun) exec(bin, trace string, args ...string) (int, string) {
	argv := append([]string{"--home", r.home, "--format", "json"}, args...)
	name := bin
	if trace != "" {
		argv = append([]string{"-f", "-qq", "-e", "trace=execve,clone,clone3,vfork,fork,openat", "-o", trace, bin}, argv...)
		name = "strace"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Env = r.env
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	r.stderr = stderr.String()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		code = -1
	}
	return code, stdout.String()
}

var (
	straceLine  = regexp.MustCompile(`^(\d+) +(.*)$`)
	cloneStart  = regexp.MustCompile(`^(clone3?|vfork|fork)\(`)
	cloneResume = regexp.MustCompile(`^<\.\.\. (clone3?|vfork|fork) resumed>`)
	resultPID   = regexp.MustCompile(`\) += (\d+)$`)
	taskOpen    = regexp.MustCompile(`openat\(.*"[^"]*/tasks/t-[0-9a-f]{12}/task\.json", O_RDONLY`)
)

// parseStrace counts the task.json reads the sumctl process made and the helper processes it started (children
// that exec'd), ignoring what those helpers did themselves. A clone strace split across an unfinished and a resumed
// line is joined before it is classified as a thread or a child.
func parseStrace(t *testing.T, path string) (reads, subprocesses int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	own := map[string]bool{}
	children := map[string]bool{}
	execed := map[string]bool{}
	pending := map[string]string{}
	classify := func(pid, call string) {
		m := resultPID.FindStringSubmatch(call)
		if m == nil || !own[pid] {
			return
		}
		if strings.Contains(call, "CLONE_THREAD") {
			own[m[1]] = true
		} else {
			children[m[1]] = true
		}
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	first := true
	for scanner.Scan() {
		m := straceLine.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		pid, rest := m[1], m[2]
		if first {
			own[pid] = true
			first = false
		}
		switch {
		case cloneStart.MatchString(rest):
			if strings.HasSuffix(rest, "<unfinished ...>") {
				pending[pid] = strings.TrimSuffix(rest, "<unfinished ...>")
			} else {
				classify(pid, rest)
			}
			continue
		case cloneResume.MatchString(rest):
			classify(pid, pending[pid]+rest)
			delete(pending, pid)
			continue
		}
		if own[pid] && taskOpen.MatchString(rest) {
			reads++
		}
		if children[pid] && strings.HasPrefix(rest, "execve(") && !execed[pid] && !strings.Contains(rest, "= -1") {
			execed[pid] = true
			subprocesses++
		}
	}
	return reads, subprocesses
}

func measureLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	return sorted[rank]
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return fallback
}

func measureCount(value *int) string {
	if value == nil {
		return "unavailable"
	}
	return strconv.Itoa(*value)
}
func measureFloat(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.3f", *value)
}
func measureDeferred(t *testing.T, stdout string) int {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(stdout), &value); err != nil {
		t.Fatalf("successful benchmark output is not JSON: %v", err)
	}
	count := 0
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if v["state"] == "deferred" {
				count++
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(value)
	return count
}

func TestMeasureNativeCoordinationFixture(t *testing.T) {
	root, _ := repoReference(t)
	binary := filepath.Join(root, ".local", "bin", "sumctl")
	for _, args := range [][]string{{"init"}, {"inbox", "--live"}, {"pump"}, {"status"}, {"status", "--compact"}, {"inbox", "--compact"}, {"inbox", "--compact", "--live"}} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			run := newMeasureFixture(t, root, 12, "healthy").fresh(t)
			if code, stdout := run.exec(binary, "", args...); code != 0 {
				t.Fatalf("native coordination fixture failed: %d %s", code, run.stderr)
			} else {
				measureDeferred(t, stdout)
			}
			run.native.AssertNoForce(t)
		})
	}
	t.Run("slow-native-request", func(t *testing.T) {
		run := newMeasureFixture(t, root, 1, "slow").fresh(t)
		started := time.Now()
		if code, _ := run.exec(binary, "", "init"); code != 0 {
			t.Fatalf("slow native fixture failed: %d %s", code, run.stderr)
		}
		if time.Since(started) < 6*time.Second {
			t.Fatal("slow scenario did not observe its declared native request delay")
		}
	})
}
