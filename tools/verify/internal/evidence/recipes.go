package evidence

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/proc"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

var cliSpec = args.Spec{Trailing: true, Options: []args.Option{
	{Name: "--expect-exit", Values: 1, Int: true, Default: "0"},
	{Name: "--expect-text", Values: 1},
	{Name: "--timeout", Values: 1, Int: true, Default: "120"},
}}

var httpSpec = args.Spec{Positionals: []string{"method", "url"}, Options: []args.Option{
	{Name: "--data", Values: 1},
	{Name: "--expect-status", Values: 1, Int: true},
	{Name: "--expect-text", Values: 1},
	{Name: "--timeout", Values: 1, Int: true, Default: "10"},
}}

var httpURL = regexp.MustCompile(`^https?://`)

// visualProof says why a text recipe carries no screenshot.
func (c *capture) visualProof(recipe string) string {
	if c.kind == "nonvisual" {
		return "not-applicable"
	}
	return "not-captured (" + recipe + " recipe)"
}

// writeText stores recorded text, naming the file as redacted when redacting it found anything new since the count was `before`.
func (c *capture) writeText(stem, text string, before int) (string, error) {
	name := stem + ".txt"
	if c.redact.Count() > before {
		name = stem + ".redacted.txt"
	}
	path := filepath.Join(c.dir, name)
	return path, os.WriteFile(path, []byte(text), 0o644)
}

// textMedia is the media entry of a transcript or response file.
func textMedia(path, role string) ([]any, error) {
	info, err := InspectMedia(path)
	if err != nil {
		return nil, err
	}
	return []any{withRole(info, role, "redacted", strings.HasSuffix(path, ".redacted.txt"))}, nil
}

func environmentNames() []any {
	var names []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}
	sort.Strings(names)
	return value.Strings(names[:min(len(names), 200)])
}

func (c *capture) cli(opts args.Parsed) ([]any, error) {
	command := opts.Rest
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		return nil, usage("cli needs -- COMMAND [ARGS...]")
	}
	cwd, _ := os.Getwd()
	c.record.Set("steps", []any{value.NewObject("action", "run", "command", value.Strings(command), "cwd", cwd)})
	value.Obj(c.record.Get("environment")).Set("env_names", environmentNames()) // Names only; values are never recorded.
	c.record.Set("visual_proof", c.visualProof("cli"))
	timeout := opts.Int("--timeout")
	result, err := proc.Run(command, proc.Options{Timeout: time.Duration(timeout) * time.Second})
	switch {
	case err == proc.ErrTimeout:
		return nil, blocked(fmt.Sprintf("command exceeded %ds; recorded as blocked, not failed", timeout))
	case proc.NotFound(err):
		return nil, blocked("command not found: " + command[0])
	case err != nil:
		return nil, blocked(fmt.Sprintf("command could not start: %v", err))
	}
	before := c.redact.Count()
	transcript := fmt.Sprintf("$ %s\n--- stdout ---\n%s\n--- stderr ---\n%s\n--- exit %d ---\n", strings.Join(command, " "), clip(result.Stdout), clip(result.Stderr), result.Code)
	path, err := c.writeText("transcript", c.redact.Redact(transcript), before)
	if err != nil {
		return nil, err
	}
	c.record.Set("observations", value.NewObject("exit", result.Code, "stdout", c.redact.Redact(clip(result.Stdout)), "stderr", c.redact.Redact(clip(result.Stderr))))
	expectExit := opts.Int("--expect-exit")
	c.assert(fmt.Sprintf("exit == %d", expectExit), result.Code == expectExit, result.Code)
	for _, expected := range opts.Strings("--expect-text") {
		c.assert("stdout contains "+value.Repr(expected), strings.Contains(result.Stdout, expected))
	}
	return textMedia(path, "transcript")
}

// httpRequest sends one request. A response of any status is returned with its body; a request that got no response returns a null
// status and the reason.
func httpRequest(method, url string, data any, timeout int) (status any, body string, failure any, err error) {
	if !httpURL.MatchString(url) {
		return nil, "", nil, usage("http needs an http(s):// URL, got " + value.Repr(url))
	}
	var payload io.Reader
	if text := value.Str(data); text != "" {
		payload = strings.NewReader(text)
	}
	request, err := http.NewRequest(strings.ToUpper(method), url, payload)
	if err != nil {
		return nil, "", err.Error(), nil
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: time.Duration(timeout) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err.Error(), nil
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, "", err.Error(), nil
	}
	return response.StatusCode, pycompat.DecodeUTF8(raw), nil, nil
}

func (c *capture) http(opts args.Parsed) ([]any, error) {
	method, url := strings.ToUpper(opts.Positionals[0]), opts.Positionals[1]
	c.record.Set("steps", []any{value.NewObject("action", "http", "method", method, "url", url, "data", opts.Optional("--data"))})
	c.record.Set("visual_proof", c.visualProof("http"))
	status, body, failure, err := httpRequest(method, url, opts.Optional("--data"), opts.Int("--timeout"))
	if err != nil {
		return nil, err
	}
	errorLine := ""
	if failure != nil {
		errorLine = "error: " + value.Str(failure)
	}
	before := c.redact.Count()
	path, err := c.writeText("response", c.redact.Redact(fmt.Sprintf("%s %s\nstatus: %s\n%s\n--- body ---\n%s\n", method, url, value.Text(status), errorLine, clip(body))), before)
	if err != nil {
		return nil, err
	}
	c.record.Set("observations", value.NewObject("status", status, "body", c.redact.Redact(clip(body)), "error", failure))
	if failure != nil {
		return nil, blocked("request failed: " + value.Str(failure))
	}
	if opts.Has("--expect-status") {
		expected := opts.Int("--expect-status")
		c.assert(fmt.Sprintf("status == %d", expected), status == expected, status)
	}
	for _, expected := range opts.Strings("--expect-text") {
		c.assert("body contains "+value.Repr(expected), strings.Contains(body, expected))
	}
	return textMedia(path, "response")
}
