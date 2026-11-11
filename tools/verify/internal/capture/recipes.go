package capture

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/proc"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

// A recipe drives the feature once, fills in what it observed, and returns the expectations that were not met.
type recipe func(record *value.Object) (unmet []string)

type request struct {
	Method string  `json:"method"`
	URL    string  `json:"url"`
	Data   *string `json:"data"`
}

// commandRecipe parses `run [flags] -- COMMAND [ARGS...]`: run the command and record stdout, stderr, and the exit code.
func commandRecipe(args []string) (run recipe, exit int, done bool) {
	var expect expectation
	var expectExit int
	fs := flag.NewFlagSet("sum-verify capture run", flag.ContinueOnError)
	fs.IntVar(&expectExit, "expect-exit", 0, "")
	expect.register(fs, 120)
	command, exit, done := pycompat.ParseFlags(fs, args, usage, false)
	if done {
		return nil, exit, true
	}
	if len(command) == 0 {
		return nil, pycompat.UsageError(fs, usage, "run needs -- COMMAND [ARGS...]"), true
	}
	return func(record *value.Object) []string {
		record.Set("command", command)
		record.Set("expectations", append([]string{fmt.Sprintf("exit == %d", expectExit)}, expect.contains("stdout")...))
		result, err := proc.Run(command, proc.Options{Timeout: time.Duration(expect.timeout) * time.Second})
		switch {
		case errors.Is(err, proc.ErrTimeout):
			record.Set("exit", nil)
			record.Set("timed_out", true)
			return []string{fmt.Sprintf("timed out after %ds", expect.timeout)}
		case err != nil:
			record.Set("exit", nil)
			record.Set("error", err.Error())
			record.Set("timed_out", false)
			return []string{"command not found: " + command[0]}
		}
		record.Set("exit", result.Code)
		record.Set("stdout", pycompat.ClipNoting(result.Stdout, limit))
		record.Set("stderr", pycompat.ClipNoting(result.Stderr, limit))
		record.Set("timed_out", false)
		var unmet []string
		if result.Code != expectExit {
			unmet = append(unmet, fmt.Sprintf("exit was %d, expected %d", result.Code, expectExit))
		}
		return append(unmet, expect.lacking("stdout", result.Stdout)...)
	}, 0, false
}

// httpRecipe parses `http [flags] METHOD URL`: send one request and record the status and body.
func httpRecipe(args []string) (run recipe, exit int, done bool) {
	var expect expectation
	var data *string
	var expectStatus *int
	fs := flag.NewFlagSet("sum-verify capture http", flag.ContinueOnError)
	fs.Func("data", "", func(value string) error {
		data = &value
		return nil
	})
	fs.Func("expect-status", "", func(value string) error {
		status, err := strconv.Atoi(value)
		expectStatus = &status
		return err
	})
	expect.register(fs, 10)
	positional, exit, done := pycompat.ParseFlags(fs, args, usage, true)
	if done {
		return nil, exit, true
	}
	if len(positional) != 2 {
		return nil, pycompat.UsageError(fs, usage, "http needs METHOD URL"), true
	}
	method, url := strings.ToUpper(positional[0]), positional[1]
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, pycompat.UsageError(fs, usage, "http needs an http(s):// URL, got %s; read the URL the service printed, not the runner's echo line", pycompat.Repr(url)), true
	}
	return func(record *value.Object) []string {
		record.Set("request", request{Method: method, URL: url, Data: data})
		expectations := []string{}
		if expectStatus != nil && *expectStatus != 0 {
			expectations = append(expectations, fmt.Sprintf("status == %d", *expectStatus))
		}
		record.Set("expectations", append(expectations, expect.contains("body")...))

		var unmet []string
		status, text := "None", ""
		response, body, err := send(method, url, data, time.Duration(expect.timeout)*time.Second)
		if err != nil {
			record.Set("error", err.Error())
			record.Set("status", nil)
			unmet = append(unmet, fmt.Sprintf("request failed: %v", err))
		} else {
			status, text = strconv.Itoa(response.StatusCode), pycompat.DecodeUTF8(body)
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				record.Set("headers", describingHeaders(response.Header))
			}
			record.Set("status", response.StatusCode)
		}
		record.Set("body", pycompat.ClipNoting(text, limit))
		if expectStatus != nil && status != strconv.Itoa(*expectStatus) {
			unmet = append(unmet, fmt.Sprintf("status was %s, expected %d", status, *expectStatus))
		}
		return append(unmet, expect.lacking("body", text)...)
	}, 0, false
}

func send(method, url string, data *string, timeout time.Duration) (*http.Response, []byte, error) {
	var payload io.Reader
	if data != nil && *data != "" {
		payload = strings.NewReader(*data)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return nil, nil, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// Asking for the body as it is stored keeps Content-Length and the recorded text those the service sent.
	request.Header.Set("Accept-Encoding", "identity")
	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, nil, err
	}
	return response, body, nil
}

// describingHeaders keeps the response headers that say what the body is and where it lives.
func describingHeaders(header http.Header) *value.Object {
	kept := value.NewObject()
	for _, name := range []string{"Content-Type", "Content-Length", "Location"} {
		if values := header.Values(name); len(values) > 0 {
			kept.Set(name, values[len(values)-1])
		}
	}
	return kept
}
