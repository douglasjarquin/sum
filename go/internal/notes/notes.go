package notes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

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
