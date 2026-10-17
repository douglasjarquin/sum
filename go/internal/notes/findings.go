package notes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const Schema = 1

type Input struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Explanation string   `json:"explanation"`
	NextStep    string   `json:"next_step"`
	References  []string `json:"references"`
	Attempt     string   `json:"attempt,omitempty"`
	Candidate   string   `json:"candidate,omitempty"`
}

type Event struct {
	Action string          `json:"action"`
	At     string          `json:"at"`
	Actor  *ordjson.Object `json:"actor"`
	Reason string          `json:"reason,omitempty"`
	Target string          `json:"target,omitempty"`
}

type Record struct {
	Schema      int             `json:"schema"`
	ID          string          `json:"id"`
	Key         string          `json:"key"`
	TaskID      string          `json:"task_id"`
	ProjectID   string          `json:"project_id"`
	Author      *ordjson.Object `json:"author"`
	CreatedAt   string          `json:"created_at"`
	Title       string          `json:"title"`
	Explanation string          `json:"explanation"`
	NextStep    string          `json:"next_step"`
	References  []string        `json:"references"`
	Attempt     string          `json:"attempt,omitempty"`
	Candidate   string          `json:"candidate,omitempty"`
	Disposition string          `json:"disposition"`
	LinkState   string          `json:"link_state"`
	LinkTask    string          `json:"link_task,omitempty"`
	History     []Event         `json:"history"`
}

type Gap struct {
	Path  string
	Error string
}

func FindingsPath(s *store.Store) string {
	return filepath.Join(s.Home, "findings")
}

func Capture(s *store.Store, taskID string, in Input, endpoint *ordjson.Object) (Record, bool, error) {
	if in.References == nil {
		in.References = []string{}
	}
	if err := validateInput(in); err != nil {
		return Record{}, false, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return Record{}, false, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return Record{}, false, err
	}
	host, err := s.Machine()
	if err != nil {
		return Record{}, false, err
	}
	role := findingRole(host, task, endpoint)
	if role != "worker" && role != "coordinator" {
		return Record{}, false, fmt.Errorf("Only the registered worker or coordinator can record a finding for this task.")
	}
	projectID, err := taskProjectID(task)
	if err != nil {
		return Record{}, false, err
	}
	id, err := recordID(taskID, in.Key)
	if err != nil {
		return Record{}, false, err
	}
	path, err := findingRecordPath(s, id, true)
	if err != nil {
		return Record{}, false, err
	}
	existing, err := readRecord(path)
	if err == nil {
		if !sameCapture(existing, taskID, projectID, in) {
			return Record{}, false, fmt.Errorf("Finding key %q already exists with different content; use a new key rather than replacing it.", in.Key)
		}
		return existing, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Record{}, false, err
	}
	actor := actorObject(endpoint, role)
	record := Record{
		Schema: Schema, ID: id, Key: in.Key, TaskID: taskID, ProjectID: projectID,
		Author: actor, CreatedAt: store.Now(), Title: in.Title, Explanation: in.Explanation,
		NextStep: in.NextStep, References: append([]string{}, in.References...), Attempt: in.Attempt,
		Candidate: in.Candidate, Disposition: "open", LinkState: "none",
		History: []Event{{Action: "capture", At: store.Now(), Actor: actor}},
	}
	if err := writeRecord(path, record); err != nil {
		return Record{}, false, err
	}
	return record, false, nil
}

func Change(s *store.Store, id, action, reason, targetTask string, endpoint *ordjson.Object) (Record, error) {
	return ChangeExpected(s, id, action, reason, targetTask, "", "", endpoint)
}

func ChangeExpected(s *store.Store, id, action, reason, targetTask, expectedRevision, expectedState string, endpoint *ordjson.Object) (Record, error) {
	unlock, err := s.Lock()
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	path, err := findingRecordPath(s, id, false)
	if err != nil {
		return Record{}, err
	}
	record, err := readRecord(path)
	if err != nil {
		return Record{}, err
	}
	if expectedRevision != "" || expectedState != "" {
		if expectedRevision != Revision(record) || expectedState != ActionState(record) {
			return Record{}, fmt.Errorf("The finding changed since this action was presented. Refresh the inbox and choose again.")
		}
	}
	source, err := s.ReadTask(record.TaskID)
	if err != nil {
		return Record{}, err
	}
	host, err := s.Machine()
	if err != nil {
		return Record{}, err
	}
	if findingRole(host, source, endpoint) != "coordinator" {
		return Record{}, fmt.Errorf("Only the registered coordinator can change a finding disposition or link.")
	}
	if reason == "" || strings.TrimSpace(reason) == "" {
		return Record{}, fmt.Errorf("A reason or outcome is required for a finding disposition.")
	}
	if _, n := environment.Redact(reason); n > 0 {
		return Record{}, fmt.Errorf("The disposition contains credential-shaped text; reference where the value lives instead.")
	}
	if action == "promote" || action == "attach" {
		if record.LinkTask == targetTask && targetTask != "" && linkedAs(record, action) {
			return record, nil
		}
		if record.Disposition != "open" {
			return Record{}, fmt.Errorf("Reopen the finding before changing its linked work.")
		}
		if targetTask == record.TaskID {
			return Record{}, fmt.Errorf("A finding cannot link back to its source task.")
		}
		if err := validateTarget(s, targetTask); err != nil {
			return Record{}, err
		}
		if record.LinkTask != "" {
			return Record{}, fmt.Errorf("Finding already has linked work; reopen and record its prior disposition before linking another target.")
		}
		record.LinkTask = targetTask
		if action == "promote" {
			record.LinkState = "handoff_outstanding"
		} else {
			record.LinkState = "attached"
		}
	} else {
		switch action {
		case "defer":
			record.Disposition = "deferred"
		case "dismiss":
			record.Disposition = "dismissed"
			if record.LinkTask != "" {
				record.LinkState = "settled"
			}
		case "resolve":
			record.Disposition = "resolved"
			if record.LinkTask != "" {
				record.LinkState = "settled"
			}
		case "reopen":
			record.Disposition = "open"
			if record.LinkState == "settled" {
				record.LinkTask = ""
				record.LinkState = "none"
			}
		case "settle":
			if record.LinkTask == "" {
				return Record{}, fmt.Errorf("There is no linked work to settle.")
			}
			record.LinkState = "settled"
			record.Disposition = "resolved"
		default:
			return Record{}, fmt.Errorf("Unknown finding action %q.", action)
		}
	}
	record.History = append(record.History, Event{Action: action, At: store.Now(), Actor: actorObject(endpoint, "coordinator"), Reason: reason, Target: targetTask})
	if err := writeRecord(path, record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func Revision(record Record) string {
	return fmt.Sprint(len(record.History))
}

func ActionState(record Record) string {
	return record.Disposition + ":" + record.LinkState
}

func linkedAs(record Record, action string) bool {
	if action == "promote" && record.LinkState == "handoff_outstanding" || action == "attach" && record.LinkState == "attached" {
		return true
	}
	if record.LinkState != "settled" {
		return false
	}
	for i := len(record.History) - 1; i >= 0; i-- {
		if record.History[i].Action == "promote" || record.History[i].Action == "attach" {
			return record.History[i].Action == action
		}
	}
	return false
}

func Read(s *store.Store, id string) (Record, error) {
	path, err := findingRecordPath(s, id, false)
	if err != nil {
		return Record{}, err
	}
	return readRecord(path)
}

func ReadTask(s *store.Store, taskID string) ([]Record, []Gap) {
	records, gaps := ReadAll(s)
	filtered := make([]Record, 0)
	for _, record := range records {
		if record.TaskID == taskID {
			filtered = append(filtered, record)
		}
	}
	return filtered, gaps
}

func ReadAll(s *store.Store) ([]Record, []Gap) {
	dir := FindingsPath(s)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, []Gap{}
	}
	if err != nil {
		return []Record{}, []Gap{{Path: dir, Error: err.Error()}}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return []Record{}, []Gap{{Path: dir, Error: "findings path must be a regular directory"}}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []Record{}, []Gap{{Path: dir, Error: err.Error()}}
	}
	records := make([]Record, 0, len(entries))
	gaps := make([]Gap, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		record, readErr := readRecord(path)
		if readErr != nil {
			gaps = append(gaps, Gap{Path: path, Error: readErr.Error()})
			continue
		}
		if entry.Name() != record.ID+".json" {
			gaps = append(gaps, Gap{Path: path, Error: "record filename does not match its stable ID"})
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt == records[j].CreatedAt {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt < records[j].CreatedAt
	})
	return records, gaps
}

func recordID(taskID, key string) (string, error) {
	pair := struct {
		TaskID string `json:"task_id"`
		Key    string `json:"key"`
	}{TaskID: taskID, Key: key}
	data, err := json.Marshal(pair)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sameCapture(record Record, taskID, projectID string, in Input) bool {
	left, _ := json.Marshal(struct {
		TaskID  string `json:"task_id"`
		Project string `json:"project_id"`
		Input
	}{taskID, projectID, in})
	right, _ := json.Marshal(struct {
		TaskID  string `json:"task_id"`
		Project string `json:"project_id"`
		Input
	}{record.TaskID, record.ProjectID, Input{Key: record.Key, Title: record.Title, Explanation: record.Explanation, NextStep: record.NextStep, References: record.References, Attempt: record.Attempt, Candidate: record.Candidate}})
	return string(left) == string(right)
}

func validateInput(in Input) error {
	for label, value := range map[string]string{"key": in.Key, "title": in.Title, "explanation": in.Explanation, "next step": in.NextStep} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("Finding %s must not be empty.", label)
		}
		if len(value) > 4096 {
			return fmt.Errorf("Finding %s exceeds 4096 bytes.", label)
		}
		if _, n := environment.Redact(value); n > 0 {
			return fmt.Errorf("Finding %s contains credential-shaped text; reference where the value lives instead.", label)
		}
	}
	if len(in.References) > 32 {
		return fmt.Errorf("A finding may include at most 32 source references.")
	}
	for _, ref := range in.References {
		if strings.TrimSpace(ref) == "" || len(ref) > 4096 {
			return fmt.Errorf("Finding references must be non-empty and at most 4096 bytes.")
		}
		if _, n := environment.Redact(ref); n > 0 {
			return fmt.Errorf("A finding reference contains credential-shaped text; reference where the value lives instead.")
		}
	}
	if in.Attempt != "" && len(in.Attempt) > 256 || in.Candidate != "" && len(in.Candidate) > 256 {
		return fmt.Errorf("Finding attempt and candidate references must be at most 256 bytes.")
	}
	for label, value := range map[string]string{"attempt": in.Attempt, "candidate": in.Candidate} {
		if _, n := environment.Redact(value); n > 0 {
			return fmt.Errorf("Finding %s contains credential-shaped text; reference where the value lives instead.", label)
		}
	}
	return nil
}

func taskProjectID(task *ordjson.Object) (string, error) {
	value, ok := task.Get("project")
	obj, okObj := value.(*ordjson.Object)
	if !ok || !okObj {
		return "", fmt.Errorf("Source task has no project identity; refusing to create an unattributed finding.")
	}
	field := func(key string) string {
		v, _ := obj.Get(key)
		out, _ := v.(string)
		return out
	}
	identity, err := project.ProjectIdentity(field("host"), field("owner"), field("repo"))
	if err != nil {
		return "", err
	}
	return identity.Name, nil
}

func validateTarget(s *store.Store, target string) error {
	if target == "" {
		return fmt.Errorf("A linked task is required.")
	}
	task, err := s.ReadTask(target)
	if err != nil {
		return err
	}
	brief, _ := task.Get("brief")
	if text, _ := brief.(string); strings.TrimSpace(text) == "" {
		return fmt.Errorf("Linked task has no approved brief.")
	}
	return nil
}

func findingRole(host machine.Identity, task, endpoint *ordjson.Object) string {
	if endpoint == nil {
		return ""
	}
	if pane, ok := task.Get("pane"); ok && pane != nil && pane != "" && host.SameEndpoint(task, endpoint) {
		return "worker"
	}
	parent, _ := task.Get("parent")
	if obj, ok := parent.(*ordjson.Object); ok && host.SameEndpoint(obj, endpoint) {
		return "coordinator"
	}
	return "other"
}

func actorObject(endpoint *ordjson.Object, role string) *ordjson.Object {
	actor := ordjson.NewObject()
	for _, key := range []string{"machine", "session", "pane"} {
		value, _ := endpoint.Get(key)
		actor.Set(key, value)
	}
	actor.Set("role", role)
	return actor
}

func findingRecordPath(s *store.Store, id string, create bool) (string, error) {
	if len(id) != 64 {
		return "", fmt.Errorf("Invalid finding ID.")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", fmt.Errorf("Invalid finding ID.")
	}
	dir := FindingsPath(s)
	if create {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("Findings path must be a regular directory.")
	}
	return filepath.Join(dir, id+".json"), nil
}

func readRecord(path string) (Record, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Record{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Record{}, fmt.Errorf("Finding records must be regular files; symlinks are not followed.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("Malformed finding record: %w", err)
	}
	if record.Schema != Schema || record.ID == "" || record.TaskID == "" || record.Key == "" || record.Disposition == "" || record.History == nil {
		return Record{}, fmt.Errorf("Finding record is missing required identity or history fields.")
	}
	expectedID, err := recordID(record.TaskID, record.Key)
	if err != nil || expectedID != record.ID {
		return Record{}, fmt.Errorf("Finding record identity does not match its source task and key.")
	}
	switch record.Disposition {
	case "open", "deferred", "dismissed", "resolved":
	default:
		return Record{}, fmt.Errorf("Finding record has an unknown disposition.")
	}
	switch record.LinkState {
	case "none":
		if record.LinkTask != "" {
			return Record{}, fmt.Errorf("Finding record has a linked task without link state.")
		}
	case "attached", "handoff_outstanding", "settled":
		if record.LinkTask == "" {
			return Record{}, fmt.Errorf("Finding record has link state without a linked task.")
		}
	default:
		return Record{}, fmt.Errorf("Finding record has an unknown link state.")
	}
	if filepath.Base(path) != record.ID+".json" {
		return Record{}, fmt.Errorf("Finding record filename does not match its stable ID.")
	}
	return record, nil
}

func writeRecord(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".finding-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
