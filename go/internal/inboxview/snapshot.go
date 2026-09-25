// Package inboxview reads saved presentation facts without operating on the live backend.
package inboxview

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/factory"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/presentation"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

type Gap struct {
	TaskID string `json:"task,omitempty"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Counts struct {
	Tasks     int `json:"tasks"`
	Decisions int `json:"decisions"`
	Worker    int `json:"worker"`
	// Coordinator counts open obligations and inspection work, excluding passive pipeline and lane context.
	Coordinator    int `json:"coordinator"`
	Inspection     int `json:"inspection"`
	Resolved       int `json:"resolved"`
	UnknownSources int `json:"unknown_sources"`
}

type Task struct {
	ID        string              `json:"id"`
	ProjectID string              `json:"project"`
	Record    *ordjson.Object     `json:"-"`
	Items     []presentation.Item `json:"items"`
	Pipeline  *pipeline.Record    `json:"pipeline,omitempty"`
}

type Snapshot struct {
	Tasks    []Task              `json:"tasks"`
	Items    []presentation.Item `json:"items"`
	Gaps     []Gap               `json:"gaps"`
	Counts   Counts              `json:"counts"`
	Complete bool                `json:"complete"`
	Factory  *ordjson.Object     `json:"-"`
}

// Read preserves readable tasks and exposes gaps separately. Counts are known counts;
// Complete is false when an unreadable source could conceal more work.
func Read(s *store.Store) Snapshot {
	out := Snapshot{Tasks: []Task{}, Items: []presentation.Item{}, Gaps: []Gap{}, Complete: true}
	registry, err := project.ReadProjects(s)
	if fileErr := optionalFile(filepath.Join(s.Home, project.ProjectsFile)); fileErr != nil {
		err = fileErr
	}
	if err != nil {
		out.gap("", filepath.Join(s.Home, project.ProjectsFile), err)
		registry = nil
	}
	projectsByPath := projectPaths(registry)
	out.Factory, err = factory.Read(s)
	if err != nil {
		out.gap("", filepath.Join(s.Home, factory.File), err)
	}
	entries, err := os.ReadDir(s.Tasks)
	if err != nil && !os.IsNotExist(err) {
		out.gap("", s.Tasks, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "t-") {
			continue
		}
		id := entry.Name()
		path := filepath.Join(s.Tasks, id, "task.json")
		if _, err := s.TaskPath(id); err != nil {
			out.gap("", path, err)
			continue
		}
		record, err := s.ReadTask(id)
		if err != nil {
			out.gap(id, path, err)
			continue
		}
		row := Task{ID: id, Record: record, Items: []presentation.Item{}}
		row.ProjectID = out.projectID(record, projectsByPath, path)
		clean := out.taskSources(record, path)
		versionPath := filepath.Join(s.Tasks, id, versions.File)
		version, err := versions.ReadVersions(s, clean)
		if fileErr := optionalFile(versionPath); fileErr != nil {
			err = fileErr
		}
		if err == nil {
			err = validateVersions(version)
		}
		if err != nil {
			out.gap(id, versionPath, err)
			version = nil
		}
		returnPath := filepath.Join(s.Tasks, id, returns.File)
		notices, err := returns.ReadReturns(s, id)
		if fileErr := optionalFile(returnPath); fileErr != nil {
			err = fileErr
		}
		if err == nil {
			err = validateDeliveries(notices)
		}
		if err != nil {
			out.gap(id, returnPath, err)
			notices = nil
		}
		obligations := returns.OpenObligationsFromVersions(clean, version)
		byRef := map[string]*ordjson.Object{}
		for _, obligation := range obligations {
			kind := str(obligation, "kind")
			if kind == string(presentation.Question) || kind == string(presentation.Answer) {
				byRef[str(obligation, "ref")] = obligation
				continue
			}
			fact := obligationFact(id, row.ProjectID, obligation, path)
			source := findRecord(clean, kind, str(obligation, "ref"))
			if fact.Source.Candidate == "" {
				fact.Source.Candidate = str(source, "candidate")
			}
			if fact.Source.Revision == "" {
				fact.Source.Revision = str(source, "brief_revision")
			}
			fact.Text = str(source, "text")
			fact.Delivery = deliveryState(clean, notices, obligation)
			row.Items = append(row.Items, presentation.Classify(fact))
		}
		for _, raw := range list(clean, "questions") {
			question := obj(raw)
			qid, state := str(question, "id"), str(question, "status")
			sourceID := "question:" + qid
			revision := str(question, "key")
			fact := presentation.Fact{Source: presentation.Source{TaskID: id, Kind: presentation.Question, ID: sourceID, Revision: revision}, ProjectID: row.ProjectID, State: state, At: str(question, "created_at"), Text: str(question, "text"), Details: []presentation.Detail{{Section: "decisions", Ref: qid, Path: path}}}
			if obligation := byRef[qid]; obligation != nil {
				fact.Delivery = deliveryState(clean, notices, obligation)
			}
			item := presentation.Classify(fact)
			if item.Reason == presentation.UnknownQuestionState {
				out.gap(id, path+"#questions/"+qid, fmt.Errorf("unknown question status %q", state))
			}
			row.Items = append(row.Items, item)
		}
		out.execution(&row, path)
		pipelinePath := filepath.Join(s.Tasks, id, pipeline.File)
		if info, statErr := os.Lstat(pipelinePath); statErr == nil || !os.IsNotExist(statErr) {
			var saved pipeline.Record
			err := optionalFile(pipelinePath)
			if err == nil {
				saved, err = pipeline.Load(s, id)
			}
			if err == nil {
				err = validatePipeline(pipelinePath, id)
			}
			if err != nil {
				out.gap(id, pipelinePath, err)
			} else if info != nil {
				row.Pipeline = &saved
				for _, gate := range saved.Rows {
					row.Items = append(row.Items, presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: id, Kind: presentation.Pipeline, ID: string(gate.Stage), Candidate: saved.Candidate}, ProjectID: row.ProjectID, State: string(gate.Status), At: gate.At, Text: gate.Result, Details: []presentation.Detail{{Section: "pipeline", Ref: string(gate.Stage), Path: pipelinePath}}}))
				}
			}
		}
		out.Tasks = append(out.Tasks, row)
		out.Items = append(out.Items, row.Items...)
	}
	out.factoryItems(s)
	out.Counts.Tasks = len(out.Tasks)
	out.Counts.UnknownSources = len(out.Gaps)
	for _, item := range out.Items {
		switch item.Kind {
		case presentation.Decision:
			out.Counts.Decisions++
		case presentation.Inspection:
			out.Counts.Inspection++
		case presentation.Resolved:
			out.Counts.Resolved++
		}
		if item.Owner == presentation.Worker {
			out.Counts.Worker++
		}
		if item.Owner == presentation.Coordinator {
			out.Counts.Coordinator++
		}
	}
	return out
}

func (s *Snapshot) gap(id, path string, err error) {
	s.Complete = false
	s.Gaps = append(s.Gaps, Gap{TaskID: id, Path: path, Reason: err.Error()})
	s.Items = append(s.Items, presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: id, Kind: presentation.Gap, ID: path}, Text: err.Error(), Details: []presentation.Detail{{Path: path}}}))
}

// optionalFile distinguishes an absent legacy sidecar from an unreadable one.
func optionalFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", path)
	}
	return nil
}

func (s *Snapshot) taskSources(record *ordjson.Object, path string) *ordjson.Object {
	clean := ordjson.NewObject()
	for _, key := range record.Keys() {
		clean.Set(key, field(record, key))
	}
	id := str(record, "id")
	for _, key := range []string{"questions", "evidence", "attention"} {
		raw := field(record, key)
		if raw == nil {
			continue
		}
		records, ok := raw.([]any)
		if !ok {
			s.gap(id, path+"#"+key, fmt.Errorf("%s must be a list", key))
			clean.Set(key, []any{})
			continue
		}
		valid := []any{}
		seen := map[string]bool{}
		for i, raw := range records {
			row := obj(raw)
			if row == nil || str(row, "id") == "" {
				s.gap(id, fmt.Sprintf("%s#%s/%d", path, key, i), fmt.Errorf("%s record needs an object and a string id", key))
				continue
			}
			if seen[str(row, "id")] {
				s.gap(id, fmt.Sprintf("%s#%s/%d", path, key, i), fmt.Errorf("duplicate %s id %q", key, str(row, "id")))
			}
			seen[str(row, "id")] = true
			if text := field(row, "text"); text != nil {
				if _, ok := text.(string); !ok {
					s.gap(id, fmt.Sprintf("%s#%s/%d/text", path, key, i), fmt.Errorf("%s text must be a string", key))
				}
			}
			if key != "questions" && str(row, "kind") == "" {
				s.gap(id, fmt.Sprintf("%s#%s/%d", path, key, i), fmt.Errorf("%s record has no string kind", key))
			}
			if key == "attention" && str(row, "status") != "open" && str(row, "status") != "seen" && str(row, "status") != "superseded" {
				s.gap(id, fmt.Sprintf("%s#%s/%d", path, key, i), fmt.Errorf("unknown attention status %q", str(row, "status")))
			}
			valid = append(valid, row)
		}
		clean.Set(key, valid)
	}
	if raw := field(record, "report"); raw != nil && obj(raw) == nil {
		s.gap(id, path+"#report", fmt.Errorf("report must be an object"))
		clean.Set("report", nil)
	}
	return clean
}

func validateVersions(record *ordjson.Object) error {
	for _, key := range []string{"revisions", "refresh"} {
		if err := objectList(record, key); err != nil {
			return err
		}
	}
	if raw := field(record, "requested"); raw != nil {
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("requested revision must be a string")
		}
	}
	for _, raw := range list(record, "revisions") {
		row := obj(raw)
		if str(row, "id") == "" {
			return fmt.Errorf("revision has no string id")
		}
		switch str(row, "status") {
		case "active", "requested", "superseded", "prepared":
		default:
			return fmt.Errorf("unknown revision status %q", str(row, "status"))
		}
	}
	requested := str(record, "requested")
	if requested != "" {
		found := false
		for _, raw := range list(record, "revisions") {
			row := obj(raw)
			if str(row, "id") == requested && str(row, "status") == "requested" {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("requested revision %q has no requested record", requested)
		}
	}
	return nil
}

func validateDeliveries(record *ordjson.Object) error {
	if err := objectList(record, "deliveries"); err != nil {
		return err
	}
	for _, raw := range list(record, "deliveries") {
		row := obj(raw)
		ids, ok := field(row, "obligations").([]any)
		if !ok || obj(field(row, "recipient")) == nil {
			return fmt.Errorf("delivery needs obligation ids and a recipient object")
		}
		for _, id := range ids {
			if _, ok := id.(string); !ok {
				return fmt.Errorf("delivery obligation id must be a string")
			}
		}
		switch str(row, "state") {
		case "pending", "submitted", "uncertain", "in-flight", "not-delivered", "stalled", "deferred":
		default:
			return fmt.Errorf("unknown delivery state %q", str(row, "state"))
		}
	}
	return nil
}

func objectList(record *ordjson.Object, key string) error {
	raw := field(record, key)
	if raw == nil {
		return nil
	}
	rows, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s must be a list", key)
	}
	for _, row := range rows {
		if obj(row) == nil {
			return fmt.Errorf("%s contains a non-object record", key)
		}
	}
	return nil
}

func obligationFact(id, projectID string, obligation *ordjson.Object, path string) presentation.Fact {
	kind := presentation.SourceKind(str(obligation, "kind"))
	section := "returns"
	switch kind {
	case presentation.Report, presentation.Review:
		section = "evidence"
	case presentation.Refresh:
		section = "update"
	}
	fact := presentation.Fact{Source: presentation.Source{TaskID: id, Kind: kind, ID: str(obligation, "id"), Candidate: str(obligation, "candidate")}, ProjectID: projectID, State: str(obligation, "attention"), At: str(obligation, "since"), Details: []presentation.Detail{{Section: section, Ref: str(obligation, "ref"), Path: path}}}
	if kind == presentation.Refresh {
		fact.Source.Revision = str(obligation, "ref")
	}
	return fact
}

func deliveryState(task, notices, obligation *ordjson.Object) string {
	if notices == nil {
		return "unknown"
	}
	route := returns.ReturnRoute(task, str(obligation, "recipient"))
	key := returns.RouteKey(route)
	if key == nil {
		return "unknown"
	}
	state := str(returns.NotificationState(notices, obligation, key), "state")
	if state == "pending" {
		// A saved attempt to another route may predate a rebind or machine alias.
		// This reader cannot establish that it was received by today's occupant.
		for _, raw := range list(notices, "deliveries") {
			for _, id := range list(obj(raw), "obligations") {
				if id == str(obligation, "id") {
					return "unknown"
				}
			}
		}
	}
	return state
}

func findRecord(task *ordjson.Object, kind, ref string) *ordjson.Object {
	key := "evidence"
	if kind == "attention" {
		key = "attention"
	}
	for _, raw := range list(task, key) {
		row := obj(raw)
		if str(row, "id") == ref {
			return row
		}
	}
	return nil
}

func (s *Snapshot) projectID(task *ordjson.Object, projectsByPath map[string]string, path string) string {
	if raw := field(task, "project"); raw != nil {
		record := obj(raw)
		if record == nil {
			s.gap(str(task, "id"), path+"#project", fmt.Errorf("project must be an object"))
			return ""
		}
		if str(record, "host") != "" || str(record, "owner") != "" || str(record, "repo") != "" {
			identity, err := project.ProjectIdentity(str(record, "host"), str(record, "owner"), str(record, "repo"))
			if err != nil {
				s.gap(str(task, "id"), path+"#project", err)
				return ""
			}
			return identity.Name
		}
		if name := str(record, "name"); name != "" {
			return name
		}
	}
	repository := str(task, "repository")
	if repository == "" {
		return ""
	}
	if name, ok := projectsByPath[filepath.Clean(repository)]; ok {
		return name
	}
	return filepath.Clean(repository)
}

func projectPaths(registry *ordjson.Object) map[string]string {
	byPath := map[string]string{}
	projects := obj(field(registry, "projects"))
	if projects != nil {
		names := append([]string(nil), projects.Keys()...)
		sort.Strings(names)
		for _, name := range names {
			row := obj(field(projects, name))
			if path := str(row, "path"); path != "" {
				path = filepath.Clean(path)
				if _, exists := byPath[path]; !exists {
					byPath[path] = name
				}
			}
		}
	}
	return byPath
}

func (s *Snapshot) execution(row *Task, path string) {
	if execution, err := reservations.GetExecution(row.Record); err != nil {
		s.gap(row.ID, path+"#execution", err)
	} else if execution != nil {
		attempts := append([]*ordjson.Object{execution.Worker}, execution.Verifiers...)
		for _, attempt := range attempts {
			state := str(attempt, "state")
			if state != "uncertain" && state != "starting" && state != "observing" {
				continue
			}
			row.Items = append(row.Items, presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: row.ID, Kind: presentation.Execution, ID: str(attempt, "id"), Revision: fmt.Sprint(field(attempt, "generation"))}, ProjectID: row.ProjectID, State: state, Details: []presentation.Detail{{Section: "execution", Ref: str(attempt, "id"), Path: path}}}))
		}
	}
	raw := field(row.Record, "launch")
	if raw == nil {
		return
	}
	launch := obj(raw)
	if launch == nil {
		s.gap(row.ID, path+"#launch", fmt.Errorf("launch must be an object"))
		return
	}
	observed := obj(field(launch, "observed"))
	if raw := field(launch, "observed"); raw != nil && observed == nil {
		s.gap(row.ID, path+"#launch/observed", fmt.Errorf("launch observation must be an object"))
	}
	state := str(observed, "status")
	switch state {
	case "", "running", "harness-observed", "harness-mismatch", "not-started":
	default:
		s.gap(row.ID, path+"#launch/observed", fmt.Errorf("unknown launch observation status %q", state))
	}
	if state == "running" || state == "harness-observed" {
		return
	}
	if str(row.Record, "status") == "archived" {
		return
	}
	row.Items = append(row.Items, presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: row.ID, Kind: presentation.Execution, ID: "launch"}, ProjectID: row.ProjectID, State: state, Details: []presentation.Detail{{Section: "execution", Path: path}}}))
}

func validatePipeline(path, id string) error {
	raw, err := ordjson.ReadFile(path)
	if err != nil {
		return err
	}
	record := obj(raw)
	if task := str(record, "task"); task != "" && task != id {
		return fmt.Errorf("pipeline task identity mismatch")
	}
	if err := objectList(record, "rows"); err != nil {
		return err
	}
	for _, raw := range list(record, "rows") {
		row := obj(raw)
		if pipeline.Index(pipeline.Stage(str(row, "stage"))) < 0 {
			return fmt.Errorf("unknown pipeline stage %q", str(row, "stage"))
		}
		switch str(row, "status") {
		case "pending", "pass", "fail", "blocked", "skipped", "not_declared":
		default:
			return fmt.Errorf("unknown pipeline status %q", str(row, "status"))
		}
	}
	return nil
}

func (s *Snapshot) factoryItems(store *store.Store) {
	projects := obj(field(s.Factory, "projects"))
	if projects == nil {
		return
	}
	names := append([]string(nil), projects.Keys()...)
	sort.Strings(names)
	for _, name := range names {
		record := obj(field(projects, name))
		path := filepath.Join(store.Home, factory.File) + "#projects/" + name
		if record == nil {
			s.gap("", path, fmt.Errorf("factory project must be an object"))
			continue
		}
		if err := objectList(record, "lanes_held"); err != nil {
			s.gap("", path, err)
			continue
		}
		for _, raw := range list(record, "lanes_held") {
			lane := obj(raw)
			taskID := str(lane, "task")
			if taskID != "" {
				if _, err := store.TaskPath(taskID); err != nil {
					s.gap("", path, fmt.Errorf("invalid factory task reference: %w", err))
					taskID = ""
				}
			}
			issue := fmt.Sprint(field(lane, "issue"))
			state := str(lane, "state")
			switch state {
			case "claimed", "running", "gated":
			default:
				s.gap(taskID, path, fmt.Errorf("unknown factory lane state %q", state))
				continue
			}
			s.Items = append(s.Items, presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: taskID, Kind: presentation.Factory, ID: name + ":" + issue, Revision: str(lane, "claimed_at")}, ProjectID: name, State: state, At: str(lane, "claimed_at"), Details: []presentation.Detail{{Section: "factory", Ref: issue, Path: path}}}))
		}
	}
}

func field(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}
func str(o *ordjson.Object, key string) string { v, _ := field(o, key).(string); return v }
func obj(v any) *ordjson.Object                { o, _ := v.(*ordjson.Object); return o }
func list(o *ordjson.Object, key string) []any { v, _ := field(o, key).([]any); return v }
