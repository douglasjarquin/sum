package roleinit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var ErrDesignated = fmt.Errorf("designated installation: init must run through the Python reference")

func Init(root string, s *store.Store, ctx *ordjson.Object, requestedRole, requestedTask string) (*ordjson.Object, error) {
	if requestedRole == "worker" && requestedTask == "" {
		return nil, fmt.Errorf("--role worker needs --task TASK_ID")
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
	if hint != "" {
		if hintStore, openErr := store.Open(hint); openErr == nil {
			task, err = matchingTask(hintStore, endpoint)
			if err != nil {
				return nil, err
			}
		}
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
	result.Set("note", note(task, marker))
	return result, nil
}

func note(task, marker *ordjson.Object) string {
	if task != nil {
		return "Dispatched worker checkout: follow your brief; do not initialize a coordinator."
	}
	base := "Development checkout: modify and test sum here only. No coordinator initialization, dispatch, production setup, or instance-wide updates."
	if marker == nil {
		return base
	}
	installationValue, _ := marker.Get("installation")
	installation, _ := installationValue.(string)
	return base + " Tests use temporary --home state and a named lab Herdr session; the installed helper at " +
		filepath.Join(installation, "bin", "sumctl") + " owns any parent-task callbacks."
}

func InstallationHint(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", nil
	}
	common := strings.TrimSpace(string(out))
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
	for _, task := range tasks {
		paneValue, hasPane := task.Get("pane")
		pane, _ := paneValue.(string)
		if !hasPane || pane == "" {
			continue
		}
		status, _ := task.Get("status")
		if status == "archived" {
			continue
		}
		machineValue, _ := task.Get("machine")
		sessionValue, _ := task.Get("session")
		if machineValue == endpoint.Machine && sessionValue == endpoint.Session && pane == endpoint.Pane {
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
			if n, convErr := number.Int64(); convErr == nil && n == store.Schema {
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
