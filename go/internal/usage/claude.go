package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	maxLineBytes = 32 << 20
	outcomeNone  = "unrecorded"
	outcomeError = "api_error"
)

func ProjectsDir(getenv func(string) string) string {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home := getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".claude", "projects")
}

func Slug(cwd string) string {
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	var b strings.Builder
	for _, r := range cwd {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func projectDir(projectsDir, cwd string) string {
	return filepath.Join(projectsDir, Slug(cwd))
}

func transcriptPath(projectDir, sessionID string) string {
	return filepath.Join(projectDir, sessionID+".jsonl")
}

func subagentPaths(projectDir, sessionID string) []string {
	matches, _ := filepath.Glob(filepath.Join(projectDir, sessionID, "subagents", "*.jsonl"))
	slices.Sort(matches)
	return matches
}

type line struct {
	Type              string `json:"type"`
	Timestamp         string `json:"timestamp"`
	IsSidechain       bool   `json:"isSidechain"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	Message           struct {
		ID         string  `json:"id"`
		Model      string  `json:"model"`
		StopReason *string `json:"stop_reason"`
		Usage      *struct {
			Input         *int64 `json:"input_tokens"`
			Output        *int64 `json:"output_tokens"`
			CacheCreation *int64 `json:"cache_creation_input_tokens"`
			CacheRead     *int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

var assistantMarker = []byte(`"type":"assistant"`)

func readSource(source *Source) {
	info, err := os.Lstat(source.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if source.Lines > 0 {
				source.Status, source.Reason = StatusPartial, "transcript removed: "+source.Path
			} else {
				source.Status, source.Reason = StatusUnavailable, "transcript not found: "+source.Path
			}
			return
		}
		source.Status, source.Reason = StatusUnavailable, err.Error()
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		source.Status, source.Reason = StatusUnavailable, "transcript is not a regular file: "+source.Path
		return
	}
	if info.Size() < source.Offset {
		*source = Source{Path: source.Path, Subagent: source.Subagent}
	}
	if info.Size() == source.Offset {
		source.Status, source.Reason = StatusComplete, ""
		return
	}
	file, err := os.Open(source.Path)
	if err != nil {
		source.Status, source.Reason = StatusUnavailable, err.Error()
		return
	}
	defer file.Close()
	if _, err := file.Seek(source.Offset, io.SeekStart); err != nil {
		source.Status, source.Reason = StatusUnavailable, err.Error()
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		source.Status, source.Reason = StatusUnavailable, err.Error()
		return
	}
	source.Status, source.Reason = StatusComplete, ""
	seen := map[string]string{}
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			break
		}
		raw := data[:end]
		data = data[end+1:]
		if len(raw) > maxLineBytes {
			source.Status, source.Reason = StatusPartial, fmt.Sprintf("line longer than %d bytes at offset %d", maxLineBytes, source.Offset)
			return
		}
		if bytes.Contains(raw, assistantMarker) {
			var entry line
			if err := json.Unmarshal(raw, &entry); err != nil {
				source.Status, source.Reason = StatusPartial, fmt.Sprintf("malformed line at offset %d", source.Offset)
				return
			}
			if entry.Type == "assistant" {
				fold(source, &entry, seen)
			}
		}
		source.Offset += int64(end + 1)
		source.Lines++
	}
}

func fold(source *Source, entry *line, seen map[string]string) {
	if entry.Timestamp != "" {
		if source.FirstAt == "" || entry.Timestamp < source.FirstAt {
			source.FirstAt = entry.Timestamp
		}
		if entry.Timestamp > source.LastAt {
			source.LastAt = entry.Timestamp
		}
	}
	if source.Invocations.ByOutcome == nil {
		source.Invocations.ByOutcome = map[string]int64{}
	}
	id := entry.Message.ID
	outcome := outcomeNone
	switch {
	case entry.IsAPIErrorMessage:
		outcome = outcomeError
	case entry.Message.StopReason != nil && *entry.Message.StopReason != "":
		outcome = *entry.Message.StopReason
	}
	if id == "" {
		id = fmt.Sprintf("line-%d", source.Offset)
	}
	previous, counted := seen[id]
	if !counted && id == source.LastMessageID {
		previous, counted = source.LastMessageOutcome, true
	}
	if counted {
		if previous != outcome {
			source.Invocations.ByOutcome[previous]--
			if source.Invocations.ByOutcome[previous] <= 0 {
				delete(source.Invocations.ByOutcome, previous)
			}
			source.Invocations.ByOutcome[outcome]++
			if previous == outcomeError {
				source.Invocations.APIErrors--
			}
			if outcome == outcomeError {
				source.Invocations.APIErrors++
			}
			seen[id] = outcome
			source.LastMessageID, source.LastMessageOutcome = id, outcome
		}
		return
	}
	seen[id] = outcome
	source.LastMessageID, source.LastMessageOutcome = id, outcome
	source.Invocations.Total++
	source.Invocations.ByOutcome[outcome]++
	if outcome == outcomeError {
		source.Invocations.APIErrors++
	}
	if entry.IsSidechain || source.Subagent {
		source.Invocations.Subagent++
	}
	if entry.Message.Model != "" && !slices.Contains(source.Models, entry.Message.Model) {
		source.Models = append(source.Models, entry.Message.Model)
		slices.Sort(source.Models)
	}
	if usage := entry.Message.Usage; usage != nil {
		addTokens(&source.Tokens, Tokens{Input: usage.Input, Output: usage.Output, CacheCreation: usage.CacheCreation, CacheRead: usage.CacheRead})
	}
}

func wallMs(first, last string) int64 {
	start, err := time.Parse(time.RFC3339Nano, first)
	if err != nil {
		return 0
	}
	end, err := time.Parse(time.RFC3339Nano, last)
	if err != nil || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}
