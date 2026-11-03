package hcboot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

const stepTimeout = 30 * time.Second

type statusDoc struct {
	Live   int `json:"live"`
	Holder *struct {
		Current bool `json:"current"`
		PID     int  `json:"pid"`
	} `json:"holder"`
}

func EnsureRoute(runtimeRoot string, route Route, listening bool) *ordjson.Object {
	hc := filepath.Join(runtimeRoot, ".local", "bin", "hc")
	info, err := os.Stat(hc)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return nil
	}
	report := ordjson.NewObject()
	report.Set("ok", false)
	if err := route.Validate(); err != nil {
		report.Set("error", err.Error())
		return report
	}
	status, err := route.readStatus(hc, runtimeRoot)
	if err != nil {
		if listening {
			report.Set("action", "status")
			report.Set("error", err.Error())
			return report
		}
		if _, startErr := route.run(hc, runtimeRoot, "--json", "server", "start"); startErr != nil {
			report.Set("action", "start")
			report.Set("error", startErr.Error())
			return report
		}
		status, err = route.readStatus(hc, runtimeRoot)
		if err != nil {
			report.Set("action", "start")
			report.Set("error", err.Error())
			return report
		}
		if ready(status) {
			return readyReport("started", status)
		}
		report.Set("action", "start")
		report.Set("error", "HC core started without a current holder")
		return report
	}
	if ready(status) {
		return readyReport("ready", status)
	}
	report.Set("action", "holder-missing")
	report.Set("live", status.Live)
	report.Set("error", "the default HC core has no current holder; sum does not restart the user's core. Repair it with hc or Herdr, then launch again")
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

func (route Route) readStatus(hc, runtimeRoot string) (statusDoc, error) {
	var status statusDoc
	result, err := route.run(hc, runtimeRoot, "--json", "server", "status")
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

func (route Route) run(hc, runtimeRoot string, args ...string) (proc.Result, error) {
	return proc.Run(append([]string{hc}, args...), runtimeRoot, stepTimeout, true, route.environment())
}

func childEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "SUM_SESSION", "SUM_CORE_ID", "SUM_HOME", "SUM_ROLE", "SUM_ATTEMPT", "SUM_TASK", "SUM_BINDING_GENERATION":
			continue
		default:
			env = append(env, entry)
		}
	}
	return env
}
