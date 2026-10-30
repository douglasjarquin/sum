package roleinit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var ErrDesignated = fmt.Errorf("designated installation: use native role initialization")

func Init(root string, s *store.Store, ctx *ordjson.Object, requestedRole, requestedTask string) (*ordjson.Object, error) {
	if requestedRole == "worker" && requestedTask == "" {
		return nil, fmt.Errorf("--role worker needs --task TASK_ID.")
	}
	if s.Designated() {
		return nil, ErrDesignated
	}

	endpoint := store.EndpointFromContext(ctx)
	hint, err := InstallationHint(root)
	if err != nil {
		return nil, err
	}

	var task *ordjson.Object
	var verdict *incarnation.Verdict
	var taskStore *store.Store
	if hint != "" && endpoint.Schema == 2 && endpoint.Task != "" && (endpoint.Role == "worker" || endpoint.Role == "reviewer") {
		hintStore, openErr := store.Open(hint)
		if openErr != nil {
			return nil, openErr
		}
		return InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: hintStore, Ctx: ctx, Role: requestedRole, Task: requestedTask})
	}
	if requestedRole == "worker" || requestedRole == "reviewer" {
		return nil, fmt.Errorf("no verified native task attempt names this checkout and caller")
	}

	role := "developer"
	var taskID any
	if task != nil {
		role = "worker"
		taskID, _ = task.Get("id")
	}
	if requestedRole == "coordinator" {
		return nil, fmt.Errorf("%s is not a sum installation (no state.json from setup). A checkout alone grants no coordinator authority; run mise run setup in the designated installation", s.Home)
	}

	marker, err := DevelopmentMarker(root)
	if err != nil {
		return nil, err
	}

	result := ordjson.NewObject()
	result.Set("role", role)
	result.Set("home", s.Home)
	result.Set("installation", false)
	result.Set("task", taskID)
	if hint != "" {
		result.Set("installation_home", hint)
	} else {
		result.Set("installation_home", nil)
	}
	result.Set("registered", false)
	result.Set("endpoint", ctx)
	if marker != nil {
		result.Set("development", marker)
	} else {
		result.Set("development", nil)
	}
	result.Set("procedure", roleProcedure(root, taskStore, role, task, nil))
	result.Set("note", note(task, marker))
	if verdict != nil {
		result.Set("incarnation", incarnationView(verdict, "worker", "", nil))
		if !verdict.Verified {
			result.Set("note", earlierOccupantNote("worker", *verdict))
		}
	}
	return result, nil
}

// earlierOccupantNote explains a recorded role refused because this pane's occupant is not the recorded one.
func earlierOccupantNote(role string, v incarnation.Verdict) string {
	return fmt.Sprintf("This pane's recorded %s role belongs to an earlier occupant (%s: %s), so it is a developer now. %s Role bookkeeping is not an OS-level sandbox.", role, v.Outcome, v.Reason, incarnation.Recovery(role, v.Outcome))
}

// judgeWorker judges the calling pane against the worker registration the installation recorded for task.
func judgeWorker(root string, s *store.Store, task *ordjson.Object, endpoint store.Endpoint) incarnation.Verdict {
	registration, err := s.Registration(endpoint)
	if err != nil || registration == nil {
		return incarnation.Verdict{Outcome: incarnation.Unrecorded, Reason: "no current native registration"}
	}
	_, terminal, err := app.ObserveNative(endpoint)
	if err != nil {
		return incarnation.Verdict{Outcome: incarnation.Unobservable, Reason: err.Error()}
	}
	receipt, err := app.NativeReceipt(registration)
	if err != nil {
		return incarnation.Verdict{Outcome: incarnation.Unrecorded, Reason: err.Error()}
	}
	recorded, _ := registration.Get("incarnation")
	return incarnation.JudgeNative(recorded, endpoint.Native(), terminal, receipt)
}

func note(task, marker *ordjson.Object) string {
	if task != nil {
		return "Dispatched worker checkout: follow your brief and the required files under `procedure`; do not initialize a coordinator."
	}
	base := "Development checkout: read the developer procedure under `procedure`; modify and test sum here only. No coordinator initialization, dispatch, production setup, or instance-wide updates."
	if marker == nil {
		return base
	}
	installationValue, _ := marker.Get("installation")
	installation, _ := installationValue.(string)
	return base + " Tests use temporary --home state and a named lab HC session; the installed helper at " +
		filepath.Join(installation, "bin", "sumctl") + " owns any parent-task callbacks."
}

func InstallationHint(root string) (string, error) {
	out, err := proc.Run([]string{"git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir"}, "", 0, true, nil)
	if err != nil {
		return "", nil
	}
	common := strings.TrimSpace(out.Stdout)
	if filepath.Base(common) != ".git" {
		return "", nil
	}
	parent := filepath.Dir(common)
	if resolveOrSelf(parent) == resolveOrSelf(root) {
		return "", nil
	}
	home := filepath.Join(parent, ".sum")
	if info, statErr := os.Stat(filepath.Join(home, "state.json")); statErr != nil || info.IsDir() {
		return "", nil
	}
	return home, nil
}

func resolveOrSelf(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func matchingTask(s *store.Store, endpoint store.Endpoint) (*ordjson.Object, error) {
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	return matchingTaskIn(s, tasks, endpoint)
}

// matchingTaskIn finds the unarchived task whose recorded pane is endpoint among tasks already read.
func matchingTaskIn(s *store.Store, tasks []*ordjson.Object, endpoint store.Endpoint) (*ordjson.Object, error) {
	for _, task := range tasks {
		status, _ := task.Get("status")
		if status == "archived" {
			continue
		}
		key := "worker_endpoint"
		if endpoint.Role == "reviewer" {
			key = "reviewer"
		}
		value, _ := task.Get(key)
		recorded, _ := value.(*ordjson.Object)
		if recorded == nil {
			continue
		}
		matches, err := s.Matches(recorded, endpoint)
		if err != nil {
			return nil, err
		}
		if matches {
			return task, nil
		}
	}

	return nil, nil
}

func DevelopmentMarker(root string) (*ordjson.Object, error) {
	path := filepath.Join(root, ".sum", "dev.json")
	if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
		return nil, nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", path)
	}
	schemaOK := false
	if schema, has := obj.Get("schema"); has {
		if number, isNum := schema.(json.Number); isNum {
			if n, convErr := number.Int64(); convErr == nil && n == 1 {
				schemaOK = true
			}
		}
	}
	kind, _ := obj.Get("kind")
	if !schemaOK || kind != "development" {
		return nil, fmt.Errorf("unrecognized development marker %s; inspect it before continuing", path)
	}
	return obj, nil
}
