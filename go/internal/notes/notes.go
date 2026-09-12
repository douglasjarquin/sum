package notes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const File = "notes.md"

var entryPattern = regexp.MustCompile(`(?m)^## (\S+) (.+)$`)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

// Path ports `notes_path`.
func Path(s *store.Store, taskID string) (string, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(taskPath, File), nil
}

// State ports `notes_state`: the one optional task-local notes artifact, parsed into timestamped entries.
func State(s *store.Store, taskID string, limit int) (*ordjson.Object, error) {
	path, err := Path(s, taskID)
	if err != nil {
		return nil, err
	}
	row := ordjson.NewObject()
	row.Set("path", path)
	row.Set("present", false)
	row.Set("ok", true)
	row.Set("entries", []any{})
	row.Set("bytes", jsonInt(0))

	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		row.Set("ok", false)
		row.Set("error", fmt.Sprintf("%s is a symlink; notes must be a regular file inside the task record and were not followed.", path))
		return row, nil
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		row.Set("note", "No notes artifact. `sumctl notes TASK_ID --text ...` creates it when an investigation needs one.")
		return row, nil
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		row.Set("present", true)
		row.Set("ok", false)
		row.Set("error", fmt.Sprintf("notes unreadable: %s", readErr))
		return row, nil
	}
	raw := string(data)
	matches := entryPattern.FindAllStringSubmatch(raw, -1)
	entries := make([]any, 0, len(matches))
	for _, m := range matches {
		entry := ordjson.NewObject()
		entry.Set("at", m[1])
		entry.Set("by", m[2])
		entries = append(entries, entry)
	}
	row.Set("present", true)
	row.Set("bytes", jsonInt(len(data)))
	row.Set("entries", entries)
	row.Set("content", environment.BoundedView(raw, limit))
	row.Set("authority", environment.ClaimNote)
	return row, nil
}

const maxText = 256 * 1024

func Add(s *store.Store, taskID, text string, endpoint *ordjson.Object) (*ordjson.Object, error) {
	if text == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("Text must not be empty.")
	}
	if len([]byte(text)) > maxText {
		return nil, fmt.Errorf("Text exceeds %d bytes; use a concise report and reference artifacts.", maxText)
	}
	if _, n := environment.Redact(text); n > 0 {
		return nil, fmt.Errorf("The note contains credential-shaped text (token, key, or password). Notes are backed up with the records; reference where a value lives instead.")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	id, _ := task.Get("id")
	idString, _ := id.(string)
	role := endpointRole(task, endpoint)
	if role == "" {
		role = "unattributed"
	}
	path, err := Path(s, idString)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; refusing to write through it.", path)
	}
	existing := fmt.Sprintf("# Notes for %s\n\nAgent-written working notes: claims, not approval or verification evidence.\n", idString)
	if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		existing = string(data)
	}
	who := role + " (no pane)"
	if endpoint != nil {
		pane, _ := endpoint.Get("pane")
		who = fmt.Sprintf("%s %v", role, pane)
	}
	entry := fmt.Sprintf("\n## %s %s\n\n%s\n", store.Now(), who, strings.TrimRightFunc(text, unicode.IsSpace))
	combined := existing + entry
	if len([]byte(combined)) > maxText {
		return nil, fmt.Errorf("Notes would exceed %d bytes; summarize and reference an artifact by path instead.", maxText)
	}
	if err := writeAtomic(path, combined); err != nil {
		return nil, err
	}
	state, err := State(s, taskID, 0)
	if err != nil {
		return nil, err
	}
	entriesValue, _ := state.Get("entries")
	entries, _ := entriesValue.([]any)
	bytesValue, _ := state.Get("bytes")
	result := ordjson.NewObject()
	result.Set("task", taskID)
	pathValue, _ := state.Get("path")
	result.Set("path", pathValue)
	result.Set("entries", jsonInt(len(entries)))
	result.Set("bytes", bytesValue)
	result.Set("by", who)
	result.Set("authority", environment.ClaimNote)
	return result, nil
}

func endpointRole(task, endpoint *ordjson.Object) string {
	if endpoint == nil {
		return ""
	}
	if matchesIdentity(task, endpoint) {
		if pane, ok := task.Get("pane"); ok && pane != nil && pane != "" {
			return "worker"
		}
	}
	if parentValue, ok := task.Get("parent"); ok {
		if parent, isObj := parentValue.(*ordjson.Object); isObj && identitiesEqual(parent, endpoint) {
			return "coordinator"
		}
	}
	if reviewerValue, ok := task.Get("reviewer"); ok {
		if reviewer, isObj := reviewerValue.(*ordjson.Object); isObj && identitiesEqual(reviewer, endpoint) {
			return "reviewer"
		}
	}
	return "other"
}

func matchesIdentity(task, endpoint *ordjson.Object) bool {
	return identitiesEqual(task, endpoint)
}

func identitiesEqual(a, b *ordjson.Object) bool {
	for _, field := range []string{"machine", "session", "pane"} {
		av, _ := a.Get(field)
		bv, _ := b.Get(field)
		if av != bv {
			return false
		}
	}
	return true
}

func writeAtomic(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".notes-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
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
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
