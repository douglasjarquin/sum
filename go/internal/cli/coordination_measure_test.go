package cli

import (
	"bufio"
	"bytes"
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

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// TestMeasureCoordinationPasses records wall time, task reads, and helper/Herdr/gh calls of the fast coordination
// commands over synthesized homes of 1, 12, and 100 tasks (one third archived; active tasks mixing open questions,
// unapplied answers, reports, open PRs, and merged cleanup-pending tasks), with healthy fakes and with a slow
// dependency. It runs only when SUM_MEASURE_OUT names an output directory; SUM_MEASURE_BINS adds binaries to compare
// as name=path[,name=path] (the candidate helper is always measured). Counts of task reads and subprocesses come from
// one strace'd run per cell when strace is available.
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
		Binary      string    `json:"binary"`
		Tasks       int       `json:"tasks"`
		Active      int       `json:"active"`
		Scenario    string    `json:"scenario"`
		Command     string    `json:"command"`
		Samples     []float64 `json:"wall_ms"`
		P50         float64   `json:"p50_ms"`
		P95         float64   `json:"p95_ms"`
		Max         float64   `json:"max_ms"`
		TaskReads   int       `json:"task_reads"`
		Subprocess  int       `json:"subprocesses"`
		HerdrCalls  int       `json:"herdr_calls"`
		GHCalls     int       `json:"gh_calls"`
		ExitCodes   []int     `json:"exit_codes"`
		Deferred    int       `json:"deferred"`
		StraceNotes string    `json:"strace,omitempty"`
	}
	var cells []cell
	commands := [][]string{{"init"}, {"inbox", "--live"}, {"pump"}, {"status"}}
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
						c.Samples = append(c.Samples, float64(time.Since(started).Microseconds())/1000)
						c.ExitCodes = append(c.ExitCodes, code)
						if i == 0 {
							c.HerdrCalls = measureLines(filepath.Join(run.base, "fake", "calls.jsonl"))
							c.GHCalls = measureLines(filepath.Join(run.base, "fake-gh", "calls.jsonl"))
							c.Deferred = strings.Count(stdout, "state: deferred")
						}
					}
					if straceErr == nil {
						run := fx.fresh(t)
						trace := filepath.Join(run.base, "strace.txt")
						run.exec(bin[1], trace, command...)
						c.TaskReads, c.Subprocess = parseStrace(t, trace)
					} else {
						c.StraceNotes = "strace unavailable; task reads and subprocesses not counted"
					}
					c.P50, c.P95, c.Max = percentile(c.Samples, 50), percentile(c.Samples, 95), percentile(c.Samples, 100)
					cells = append(cells, c)
					t.Logf("%s n=%d %s %-12s p50=%.0fms p95=%.0fms reads=%d subprocs=%d herdr=%d gh=%d deferred=%d exits=%v",
						c.Binary, c.Tasks, c.Scenario, c.Command, c.P50, c.P95, c.TaskReads, c.Subprocess, c.HerdrCalls, c.GHCalls, c.Deferred, c.ExitCodes)
				}
			}
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(map[string]any{"schema": 1, "generated_at": time.Now().UTC().Format(time.RFC3339), "cells": cells}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "raw.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var table bytes.Buffer
	fmt.Fprintln(&table, "| Binary | Tasks (active) | Scenario | Command | Samples | p50 ms | p95 ms | Task reads | Subprocesses | Herdr calls | gh calls | Deferred |")
	fmt.Fprintln(&table, "| --- | ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, c := range cells {
		fmt.Fprintf(&table, "| %s | %d (%d) | %s | `%s` | %d | %.0f | %.0f | %d | %d | %d | %d | %d |\n",
			c.Binary, c.Tasks, c.Active, c.Scenario, c.Command, len(c.Samples), c.P50, c.P95, c.TaskReads, c.Subprocess, c.HerdrCalls, c.GHCalls, c.Deferred)
	}
	if err := os.WriteFile(filepath.Join(out, "table.md"), table.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type measureFixture struct {
	root     string
	template string
	scenario string
	active   int
}

type measureRun struct {
	base string
	home string
	env  []string
}

// newMeasureFixture plants a coordinator home with n tasks. Every third task is archived; active tasks cycle through
// an open question (owed to the coordinator), an unapplied answer (owed to an idle worker), a submitted report, a
// recorded open PR, and a merged PR with cleanup pending. Worker panes exist in the fake Herdr.
func newMeasureFixture(t *testing.T, root string, n int, scenario string) *measureFixture {
	t.Helper()
	template := t.TempDir()
	home := filepath.Join(template, "state")
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	parent := ordjson.NewObject()
	parent.Set("machine", host.ID)
	parent.Set("session", "sum-test")
	parent.Set("pane", "w-parent:p1")
	parent.Set("cwd", root)
	owner := ordjson.NewObject()
	for _, k := range parent.Keys() {
		v, _ := parent.Get(k)
		owner.Set(k, v)
	}
	owner.Set("role", "coordinator")
	owner.Set("claimed_at", "2026-09-05T00:00:00+00:00")
	if err := ordjson.WriteFile(filepath.Join(home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(store.EndpointFromContext(parent), "coordinator", nil); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(template, "repo")
	for _, argv := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "-c", "user.email=m@example.invalid", "-c", "user.name=m", "commit", "-q", "--allow-empty", "-m", "base"}} {
		if out, err := exec.Command("git", argv...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", argv, err, out)
		}
	}
	panes := map[string]any{}
	active := 0
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t-%012x", i+1)
		pane := fmt.Sprintf("w%d:p1", i+1)
		worktree := filepath.Join(template, "wt", id)
		if err := os.MkdirAll(worktree, 0o700); err != nil {
			t.Fatal(err)
		}
		archived := i%3 == 2
		kind := active % 5
		task := map[string]any{
			"schema": 1, "id": id, "status": "running", "repository": repo, "machine": host.ID, "session": "sum-test",
			"pane": pane, "workspace": strings.Split(pane, ":")[0], "worktree": worktree, "branch": "sum/" + id,
			"parent": map[string]any{"machine": host.ID, "session": "sum-test", "pane": "w-parent:p1", "cwd": root},
			"questions": []any{}, "evidence": []any{}, "report": nil, "notice": nil, "attention": []any{}, "brief": "measure",
			"base_sha": strings.Repeat("0", 40), "kind": "ship",
			"execution": map[string]any{"schema": 1, "verifiers": []any{}, "worker": map[string]any{
				"id": fmt.Sprintf("x-%012x", i+1), "kind": "worker", "state": "released", "generation": 1,
				"owner": map[string]any{"machine": host.ID, "session": "sum-test", "pane": pane}, "checkout": worktree,
				"created_at": "2026-09-05T00:00:00+00:00", "updated_at": "2026-09-05T00:00:00+00:00", "observations": []any{}}},
		}
		status := "working"
		if archived {
			task["status"] = "archived"
			task["questions"] = []any{map[string]any{"id": "q-old", "status": "applied", "created_at": "2026-09-05T00:00:00+00:00"}}
		} else {
			active++
			switch kind {
			case 0:
				task["questions"] = []any{map[string]any{"id": "q-1", "status": "open", "text": "Which way?", "created_at": "2026-09-05T00:00:00+00:00"}}
			case 1:
				task["questions"] = []any{map[string]any{"id": "q-1", "status": "answered", "text": "Which way?", "answer": "This way.", "created_at": "2026-09-05T00:00:00+00:00", "answered_at": "2026-09-05T00:01:00+00:00"}}
				status = "idle"
			case 2:
				task["evidence"] = []any{map[string]any{"id": "e-1", "kind": "report", "source": "worker", "at": "2026-09-05T00:02:00+00:00"}}
				task["report"] = map[string]any{"text": "done", "submitted_at": "2026-09-05T00:02:00+00:00"}
			case 3, 4:
				pr := map[string]any{"complete": true, "merged_for_task": kind == 4, "state": "open", "observed_at": "2026-09-05T00:03:00+00:00",
					"identity": map[string]any{"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7", "head_sha": strings.Repeat("a", 40), "head_branch": "sum/" + id, "base_branch": "main"},
					"findings": []any{}}
				if kind == 4 {
					pr["state"] = "merged"
					pr["merge_commit"] = strings.Repeat("b", 40)
				}
				task["pr"] = pr
			}
			panes[pane] = map[string]any{"pane_id": pane, "cwd": worktree, "workspace_id": strings.Split(pane, ":")[0], "agent_status": status, "agent": "claude", "created": true}
			if _, err := s.Register(store.Endpoint{Machine: host.ID, Session: "sum-test", Pane: pane, Cwd: worktree}, "worker", id); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(home, "tasks", id), 0o700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "tasks", id, "task.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"fake", "fake-gh", "fake-lsof", "fake-codegraph"} {
		if err := os.MkdirAll(filepath.Join(template, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := json.Marshal(map[string]any{"panes": panes, "workspaces": map[string]any{}})
	if err := os.WriteFile(filepath.Join(template, "fake", "state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	gh := map[string]any{"number": 7, "repository": "douglasjarquin/project", "state": "OPEN", "head_branch": "sum/x", "head_sha": strings.Repeat("a", 40)}
	if scenario == "slow" {
		gh["delay"] = 5
	}
	ghRaw, _ := json.Marshal(gh)
	if err := os.WriteFile(filepath.Join(template, "fake-gh", "pr.json"), ghRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(template, "fake-lsof", "cwds.json"), []byte(`{"processes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &measureFixture{root: root, template: template, scenario: scenario, active: active}
}

// fresh copies the template into a new directory so every sample starts from the same records.
func (f *measureFixture) fresh(t *testing.T) *measureRun {
	t.Helper()
	base := filepath.Join(t.TempDir(), "run")
	if out, err := exec.Command("cp", "-a", f.template, base).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	env := demoEnv(t, f.root, base)
	env = append(env, "FAKE_PARENT_STATUS=idle", "FAKE_SESSION=sum-test")
	if f.scenario == "slow" {
		env = append(env, "FAKE_OBSERVE_DELAY=6")
	}
	return &measureRun{base: base, home: filepath.Join(base, "state"), env: env}
}

// exec runs one sumctl command (under strace when trace is set) and returns its exit code and stdout.
func (r *measureRun) exec(bin, trace string, args ...string) (int, string) {
	argv := append([]string{"--home", r.home}, args...)
	name := bin
	if trace != "" {
		argv = append([]string{"-f", "-qq", "-e", "trace=execve,clone,clone3,vfork,fork,openat", "-o", trace, bin}, argv...)
		name = "strace"
	}
	cmd := exec.Command(name, argv...)
	cmd.Env = r.env
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}
	err := cmd.Run()
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
