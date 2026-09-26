// Package fixture writes deterministic factory/task homes for digest tests and the integrated replay.
// It writes saved records only: task.json files and the factory registry, never a checkout or a Herdr pane.
package fixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Project is a recorded canonical project identity.
type Project struct {
	Host, Owner, Repo string
}

// Key is the canonical project key the overview groups by (host omitted for github.com).
func (p Project) Key() string {
	if p.Host == "" || p.Host == "github.com" {
		return p.Owner + "/" + p.Repo
	}
	return p.Host + "/" + p.Owner + "/" + p.Repo
}

// Evidence is one saved evidence record; Fields carries kind-specific keys (result, verdict, run_id, ...).
type Evidence struct {
	ID        string
	Kind      string
	Source    string
	At        string
	Candidate string
	Fields    map[string]any
}

// PR is a saved `pr reconcile` observation on the task.
type PR struct {
	Number      int
	URL         string
	State       string
	HeadSHA     string
	ObservedAt  string
	MergeCommit string
}

type Question struct {
	ID, Status, Text, CreatedAt string
}

type Cleanup struct {
	State, At string
}

// Task describes one task.json. Candidate is the reported candidate; Report=true records a worker report on it.
type Task struct {
	ID        string
	Project   Project
	Status    string
	Candidate string
	Report    bool
	ReportAt  string
	Evidence  []Evidence
	PR        *PR
	Cleanup   *Cleanup
	Questions []Question
	Extra     map[string]any
}

type Lane struct {
	Issue     int
	Task      string
	State     string
	ClaimedAt string
}

// Factory is one registry entry (factory.json "projects" → Name).
type Factory struct {
	Name       string
	Enabled    bool
	Lanes      int
	Held       []Lane
	LastTickAt string
	NextTickAt string
}

// Record renders the task.json object.
func (t Task) Record() map[string]any {
	evidence := []any{}
	for _, e := range t.Evidence {
		row := map[string]any{"schema": 1, "id": e.ID, "kind": e.Kind, "source": e.Source, "at": e.At, "candidate": e.Candidate, "brief_revision": nil}
		for k, v := range e.Fields {
			row[k] = v
		}
		evidence = append(evidence, row)
	}
	questions := []any{}
	for _, q := range t.Questions {
		questions = append(questions, map[string]any{"id": q.ID, "status": q.Status, "text": q.Text, "created_at": q.CreatedAt})
	}
	record := map[string]any{
		"schema": 1, "id": t.ID, "status": t.Status, "repository": "/w/" + t.ID, "kind": "ship", "brief": "do the thing", "brief_path": "brief.md",
		"project":   map[string]any{"host": t.Project.Host, "owner": t.Project.Owner, "repo": t.Project.Repo},
		"questions": questions, "attention": []any{}, "evidence": evidence, "report": nil,
	}
	if t.Report {
		record["report"] = map[string]any{"text": "done", "submitted_at": t.ReportAt, "candidate": t.Candidate, "brief_revision": nil}
	}
	if t.PR != nil {
		var mergeCommit any
		if t.PR.MergeCommit != "" {
			mergeCommit = t.PR.MergeCommit
		}
		merged := t.PR.State == "merged" && mergeCommit != nil
		record["pr"] = map[string]any{
			"identity":        map[string]any{"number": t.PR.Number, "url": t.PR.URL, "head_sha": t.PR.HeadSHA, "head_branch": "task/" + t.ID, "base_branch": "main", "draft": false},
			"state":           t.PR.State,
			"draft":           false,
			"observed_at":     t.PR.ObservedAt,
			"findings":        []any{},
			"merge_commit":    mergeCommit,
			"complete":        merged,
			"merged_for_task": merged,
		}
	}
	if t.Cleanup != nil {
		record["cleanup"] = map[string]any{"schema": 1, "state": t.Cleanup.State, "at": t.Cleanup.At, "blockers": []any{}, "history": []any{}}
	}
	for k, v := range t.Extra {
		record[k] = v
	}
	return record
}

// WriteTask writes tasks/ID/task.json under home.
func WriteTask(home string, t Task) error {
	path := filepath.Join(home, "tasks", t.ID, "task.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(t.Record(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// WriteFactory writes the registry with the given projects.
func WriteFactory(home string, factories []Factory) error {
	projects := map[string]any{}
	for _, f := range factories {
		held := []any{}
		for _, l := range f.Held {
			lane := map[string]any{"issue": l.Issue, "state": l.State, "claimed_at": l.ClaimedAt, "claim": map[string]any{"host": "fixture", "pane": "w-c:p1", "label": "sum-claimed"}}
			if l.Task != "" {
				lane["task"] = l.Task
			}
			held = append(held, lane)
		}
		lanes := f.Lanes
		if lanes == 0 {
			lanes = 1
		}
		var last, next any
		if f.LastTickAt != "" {
			last = f.LastTickAt
		}
		if f.NextTickAt != "" {
			next = f.NextTickAt
		}
		projects[f.Name] = map[string]any{
			"name": f.Name, "enabled": f.Enabled, "lanes": lanes,
			"ready":        map[string]any{"kind": "issues", "label": "ready", "roadmap_issue": nil, "project_number": nil, "ready_option": "Ready"},
			"idle_seconds": 300, "strict_cleanup": false, "skip": []any{}, "enabled_at": "2026-01-01T00:00:00+00:00",
			"lanes_held": held, "last_tick_at": last, "next_tick_at": next,
		}
	}
	raw, err := json.MarshalIndent(map[string]any{"schema": 1, "projects": projects}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "factory.json"), append(raw, '\n'), 0o600)
}

// WriteState writes state.json with the installation instance the digest cursor binds to.
func WriteState(home, instance string) error {
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		return err
	}
	raw := fmt.Sprintf("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-01-01T00:00:00+00:00\", \"instance\": %q}\n", instance)
	return os.WriteFile(filepath.Join(home, "state.json"), []byte(raw), 0o600)
}

var (
	A = Project{Host: "github.com", Owner: "a", Repo: "repo"}
	B = Project{Host: "github.com", Owner: "b", Repo: "repo"}
	G = Project{Host: "ghe.example.com", Owner: "b", Repo: "repo"}
)

// Standard is the shared layout: three canonical projects (two with duplicate short names), two enrolled
// factories with one lane each, standalone tasks, and archived tasks with retained PR and cleanup evidence.
//
//	t-a1aaaaaaaaaa a/repo   factory lane held (issue 41): reported, verified, review approved, PR #7 open
//	t-a2aaaaaaaaaa a/repo   archived: PR #5 observed merged (merge commit), cleanup complete, no lane
//	t-a3aaaaaaaaaa a/repo   archived: the same PR #5 observed merged under a URL alias (case/host spelling)
//	t-b1bbbbbbbbbb b/repo   factory lane gated (issue 43): open question (a human decision), PR #9 open
//	t-b2bbbbbbbbbb b/repo   standalone: PR #7 open (same number as a/repo #7, a distinct PR)
//	t-d1dddddddddd ghe.example.com/b/repo standalone: worker report only
//	t-c1cccccccccc no project: running, nothing recorded
type Standard struct {
	Home     string
	Instance string
	Tasks    []Task
	Factory  []Factory
}

func StandardTasks() []Task {
	return []Task{
		{ID: "t-a1aaaaaaaaaa", Project: A, Status: "running", Candidate: "c1a1", Report: true, ReportAt: "2026-02-01T10:00:00+00:00",
			Evidence: []Evidence{
				{ID: "e-a1-hand", Kind: "handoff", Source: "worker", At: "2026-02-01T09:59:00+00:00", Candidate: "c1a1"},
				{ID: "e-a1-ver", Kind: "verification", Source: "coordinator", At: "2026-02-01T10:30:00+00:00", Candidate: "c1a1", Fields: map[string]any{"result": "pass", "run_id": "r-1", "certifies": "c1a1"}},
				{ID: "e-a1-rev", Kind: "review", Source: "coordinator", At: "2026-02-01T11:00:00+00:00", Candidate: "c1a1", Fields: map[string]any{"verdict": "approve"}},
			},
			PR: &PR{Number: 7, URL: "https://github.com/a/repo/pull/7", State: "open", HeadSHA: "c1a1", ObservedAt: "2026-02-01T11:20:00+00:00"}},
		{ID: "t-a2aaaaaaaaaa", Project: A, Status: "archived", Candidate: "c1a2", Report: true, ReportAt: "2026-01-20T10:00:00+00:00",
			PR:      &PR{Number: 5, URL: "https://github.com/a/repo/pull/5", State: "merged", HeadSHA: "c1a2", ObservedAt: "2026-01-21T09:00:00+00:00", MergeCommit: "m5m5m5"},
			Cleanup: &Cleanup{State: "complete", At: "2026-01-21T09:30:00+00:00"}},
		{ID: "t-a3aaaaaaaaaa", Project: A, Status: "archived", Candidate: "c1a2",
			PR:      &PR{Number: 5, URL: "https://GitHub.com/A/Repo/pull/5/", State: "merged", HeadSHA: "c1a2", ObservedAt: "2026-01-21T09:05:00+00:00", MergeCommit: "m5m5m5"},
			Cleanup: &Cleanup{State: "complete", At: "2026-01-21T09:31:00+00:00"}},
		{ID: "t-b1bbbbbbbbbb", Project: B, Status: "running", Candidate: "c1b1", Report: true, ReportAt: "2026-02-02T10:00:00+00:00",
			Questions: []Question{{ID: "q-compat", Status: "open", Text: "The migration requires a backward-compatibility decision. Preserve the old endpoint for one release?", CreatedAt: "2026-02-02T10:05:00+00:00"}},
			PR:        &PR{Number: 9, URL: "https://github.com/b/repo/pull/9", State: "open", HeadSHA: "c1b1", ObservedAt: "2026-02-02T09:50:00+00:00"}},
		{ID: "t-b2bbbbbbbbbb", Project: B, Status: "running", Candidate: "c1b2",
			PR: &PR{Number: 7, URL: "https://github.com/b/repo/pull/7", State: "open", HeadSHA: "c1b2", ObservedAt: "2026-02-02T08:00:00+00:00"}},
		{ID: "t-d1dddddddddd", Project: G, Status: "running", Candidate: "c1g1", Report: true, ReportAt: "2026-02-03T10:00:00+00:00"},
		{ID: "t-c1cccccccccc", Status: "running", Extra: map[string]any{"project": nil, "repository": nil}},
	}
}

func StandardFactories() []Factory {
	return []Factory{
		{Name: "a/repo", Enabled: true, Held: []Lane{{Issue: 41, Task: "t-a1aaaaaaaaaa", State: "running", ClaimedAt: "2026-02-01T09:00:00+00:00"}}, LastTickAt: "2026-02-01T09:00:00+00:00"},
		{Name: "b/repo", Enabled: true, Held: []Lane{{Issue: 43, Task: "t-b1bbbbbbbbbb", State: "gated", ClaimedAt: "2026-02-02T09:00:00+00:00"}}, LastTickAt: "2026-02-02T12:00:00+00:00", NextTickAt: "2026-02-02T12:05:00+00:00"},
	}
}

// WriteStandard writes the standard layout into home (created if needed).
func WriteStandard(home string) (Standard, error) {
	out := Standard{Home: home, Instance: "inst-fixture", Tasks: StandardTasks(), Factory: StandardFactories()}
	if err := WriteState(home, out.Instance); err != nil {
		return out, err
	}
	for _, t := range out.Tasks {
		if err := WriteTask(home, t); err != nil {
			return out, err
		}
	}
	return out, WriteFactory(home, out.Factory)
}
