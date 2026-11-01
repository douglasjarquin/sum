// Package hcboot starts the local HC core and holder for a designated installation.
// Observation commands do not use it. A bare init does, and it never claims a role.
package hcboot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

const stepTimeout = 30 * time.Second

var addressPattern = regexp.MustCompile(`^t[0-9a-z]+$`)

type statusDoc struct {
	Live   int `json:"live"`
	Holder *struct {
		Current bool `json:"current"`
		PID     int  `json:"pid"`
	} `json:"holder"`
}

// Ensure brings up the pinned local HC core and a current holder.
// A runtime without .local/bin/hc returns nil. The result is a report, not a role.
func Ensure(runtimeRoot string) *ordjson.Object {
	hc := filepath.Join(runtimeRoot, ".local", "bin", "hc")
	info, err := os.Stat(hc)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return nil
	}
	report := ordjson.NewObject()
	report.Set("ok", false)
	status, err := readStatus(hc, runtimeRoot)
	if err != nil {
		if _, startErr := run(hc, runtimeRoot, "--json", "server", "start"); startErr != nil {
			report.Set("action", "start")
			report.Set("error", startErr.Error())
			return report
		}
		status, err = readStatus(hc, runtimeRoot)
		if err != nil {
			report.Set("action", "start")
			report.Set("error", err.Error())
			return report
		}
		if ready(status) {
			return readyReport("started", status)
		}
	}
	if ready(status) {
		return readyReport("ready", status)
	}
	if status.Live > 0 {
		report.Set("action", "blocked")
		report.Set("live", status.Live)
		report.Set("error", "HC holder is missing while live terminals exist; the core was not restarted")
		return report
	}
	if _, err = run(hc, runtimeRoot, "--json", "server", "restart"); err != nil {
		report.Set("action", "restart")
		report.Set("error", err.Error())
		return report
	}
	status, err = readStatus(hc, runtimeRoot)
	if err != nil {
		report.Set("action", "restart")
		report.Set("error", err.Error())
		return report
	}
	if ready(status) {
		return readyReport("restarted", status)
	}
	started, err := run(hc, runtimeRoot, "--json", "run", "true")
	if err != nil {
		report.Set("action", "spawn")
		report.Set("error", err.Error())
		return report
	}
	var launched struct {
		Address string `json:"address"`
	}
	if json.Unmarshal([]byte(started.Stdout), &launched) == nil && addressPattern.MatchString(launched.Address) {
		_, _ = run(hc, runtimeRoot, "rm", launched.Address)
	}
	status, err = readStatus(hc, runtimeRoot)
	if err != nil {
		report.Set("action", "spawn")
		report.Set("error", err.Error())
		return report
	}
	if ready(status) {
		return readyReport("spawned", status)
	}
	report.Set("action", "spawn")
	report.Set("error", "HC core is up and its holder is still missing")
	return report
}

func ready(status statusDoc) bool {
	return status.Holder != nil && status.Holder.Current && status.Holder.PID > 0
}

func readyReport(action string, status statusDoc) *ordjson.Object {
	report := ordjson.NewObject()
	report.Set("ok", true)
	report.Set("action", action)
	report.Set("live", status.Live)
	report.Set("holder_pid", status.Holder.PID)
	return report
}

func readStatus(hc, runtimeRoot string) (statusDoc, error) {
	var status statusDoc
	result, err := run(hc, runtimeRoot, "--json", "server", "status")
	if err != nil {
		return status, err
	}
	if result.Code != 0 {
		return status, fmt.Errorf("hc server status exited %d: %s", result.Code, strings.TrimSpace(result.Detail()))
	}
	if err = json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return status, fmt.Errorf("hc server status: %w", err)
	}
	return status, nil
}

func run(hc, runtimeRoot string, args ...string) (proc.Result, error) {
	return proc.Run(append([]string{hc}, args...), runtimeRoot, stepTimeout, false, childEnv())
}

func childEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "HC_SESSION", "HC_SOCKET", "SUM_SESSION", "SUM_CORE_ID", "SUM_HOME", "SUM_ROLE", "SUM_ATTEMPT", "SUM_TASK", "SUM_BINDING_GENERATION":
			continue
		default:
			env = append(env, entry)
		}
	}
	return env
}
