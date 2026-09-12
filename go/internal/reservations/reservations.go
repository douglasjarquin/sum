package reservations

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pyrepr"
)

const Schema = 1

var heldStates = map[string]bool{"held": true, "observing": true, "starting": true, "running": true, "uncertain": true}
var allStates = map[string]bool{"held": true, "observing": true, "starting": true, "running": true, "uncertain": true, "released": true}
var kinds = map[string]bool{"worker": true, "verifier": true}
var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

type FormatError struct{ msg string }

func (e *FormatError) Error() string { return e.msg }

func formatError(format string, args ...any) error {
	return &FormatError{msg: fmt.Sprintf(format, args...)}
}

func isPID(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i > 0
}

func isTimestamp(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

func isArgv(v any) bool {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return false
	}
	for _, item := range list {
		s, ok := item.(string)
		if !ok || s == "" {
			return false
		}
	}
	return true
}

func nonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && s != ""
}

func isOccupant(value any, kind string) bool {
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return false
	}
	if kind == "worker" {
		want := []string{"machine", "session", "pane", "checkout", "harness", "name", "shell_pid", "pid", "argv"}
		if !sameKeySet(obj, want) {
			return false
		}
		for _, field := range []string{"machine", "session", "pane", "checkout"} {
			v, _ := obj.Get(field)
			if !nonEmptyString(v) {
				return false
			}
		}
		harness, _ := obj.Get("harness")
		if harness != nil {
			if _, ok := harness.(string); !ok {
				return false
			}
		}
		name, _ := obj.Get("name")
		if name != nil {
			if _, ok := name.(string); !ok {
				return false
			}
		}
		shellPid, _ := obj.Get("shell_pid")
		if shellPid != nil && !isPID(shellPid) {
			return false
		}
		pid, _ := obj.Get("pid")
		if pid != nil && !isPID(pid) {
			return false
		}
		argv, _ := obj.Get("argv")
		if argv != nil && !isArgv(argv) {
			return false
		}
		return true
	}
	want := []string{"machine", "pid", "argv", "checkout"}
	if !sameKeySet(obj, want) {
		return false
	}
	machine, _ := obj.Get("machine")
	pid, _ := obj.Get("pid")
	argv, _ := obj.Get("argv")
	checkout, _ := obj.Get("checkout")
	return nonEmptyString(machine) && isPID(pid) && isArgv(argv) && nonEmptyString(checkout)
}

func sameKeySet(obj *ordjson.Object, want []string) bool {
	if obj.Len() != len(want) {
		return false
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, k := range obj.Keys() {
		if !wantSet[k] {
			return false
		}
	}
	return true
}

func isObservation(value any) bool {
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return false
	}
	at, _ := obj.Get("at")
	if !nonEmptyString(at) {
		return false
	}
	outcome, _ := obj.Get("outcome")
	if !nonEmptyString(outcome) {
		return false
	}
	if pid, has := obj.Get("pid"); has && pid != nil && !isPID(pid) {
		return false
	}
	if argv, has := obj.Get("argv"); has && !isArgv(argv) {
		return false
	}
	for _, field := range []string{"reason", "checkout", "pane", "workspace", "descendant_error"} {
		if v, has := obj.Get(field); has && v != nil {
			if _, ok := v.(string); !ok {
				return false
			}
		}
	}
	if v, has := obj.Get("checkout_present"); has {
		if _, ok := v.(bool); !ok {
			return false
		}
	}
	return true
}

func attempt(value any, expectedKind string) (*ordjson.Object, error) {
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, formatError("%s reservation is not an object", expectedKind)
	}
	required := []string{"id", "kind", "state", "generation", "owner", "checkout", "created_at", "updated_at", "observations"}
	var missing []string
	for _, r := range required {
		if _, has := obj.Get(r); !has {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, formatError("%s reservation lacks %s", expectedKind, pyrepr.StrList(missing))
	}
	idValue, _ := obj.Get("id")
	id, idIsString := idValue.(string)
	if !idIsString || len(id) < 2 || id[:2] != "x-" {
		return nil, formatError("%s reservation has an invalid id", expectedKind)
	}
	kindValue, _ := obj.Get("kind")
	stateValue, _ := obj.Get("state")
	kind, kindIsString := kindValue.(string)
	state, stateIsString := stateValue.(string)
	if !kindIsString || !stateIsString || kind != expectedKind || !allStates[state] {
		return nil, formatError("%s reservation has an invalid kind or state", expectedKind)
	}
	generationValue, _ := obj.Get("generation")
	generation, genOK := generationValue.(json.Number)
	if !genOK {
		return nil, formatError("%s reservation has an invalid generation", expectedKind)
	}
	genInt, genErr := generation.Int64()
	if genErr != nil || genInt < 1 {
		return nil, formatError("%s reservation has an invalid generation", expectedKind)
	}
	ownerValue, _ := obj.Get("owner")
	owner, ownerOK := ownerValue.(*ordjson.Object)
	validOwner := ownerOK && sameKeySet(owner, []string{"machine", "session", "pane"})
	if validOwner {
		machine, _ := owner.Get("machine")
		session, _ := owner.Get("session")
		pane, _ := owner.Get("pane")
		if !nonEmptyString(machine) || !nonEmptyString(session) {
			validOwner = false
		}
		if pane != nil {
			if s, ok := pane.(string); !ok || s == "" {
				validOwner = false
			}
		}
	}
	observationsValue, _ := obj.Get("observations")
	observationsList, obsListOK := observationsValue.([]any)
	if !validOwner || !obsListOK {
		return nil, formatError("%s reservation has invalid owner or observations", expectedKind)
	}
	checkoutValue, _ := obj.Get("checkout")
	if checkoutValue != nil {
		if s, ok := checkoutValue.(string); !ok || s == "" {
			return nil, formatError("%s reservation has an invalid checkout", expectedKind)
		}
	}
	for _, field := range []string{"created_at", "updated_at"} {
		v, _ := obj.Get(field)
		if !isTimestamp(v) {
			return nil, formatError("%s reservation has an invalid %s", expectedKind, field)
		}
	}
	if candidateValue, has := obj.Get("candidate"); has && candidateValue != nil {
		s, ok := candidateValue.(string)
		if !ok || !sha40.MatchString(s) {
			return nil, formatError("%s reservation has an invalid candidate", expectedKind)
		}
	}
	if occupantValue, has := obj.Get("occupant"); has {
		if !isOccupant(occupantValue, expectedKind) {
			return nil, formatError("%s reservation has an invalid occupant", expectedKind)
		}
	}
	for _, row := range observationsList {
		if !isObservation(row) {
			return nil, formatError("%s reservation has an invalid observation", expectedKind)
		}
	}
	for _, field := range []string{"operation_pid", "observer_pid"} {
		if v, has := obj.Get(field); has && !isPID(v) {
			return nil, formatError("%s reservation has an invalid %s", expectedKind, field)
		}
	}
	return obj, nil
}

type Execution struct {
	Worker    *ordjson.Object
	Verifiers []*ordjson.Object
}

func GetExecution(task *ordjson.Object) (*Execution, error) {
	executionValue, has := task.Get("execution")
	if !has {
		return nil, nil
	}
	obj, ok := executionValue.(*ordjson.Object)
	schemaOK := false
	if ok {
		if schema, hasSchema := obj.Get("schema"); hasSchema {
			if num, isNum := schema.(json.Number); isNum {
				if n, convErr := num.Int64(); convErr == nil && n == Schema {
					schemaOK = true
				}
			}
		}
	}
	if !ok || !schemaOK || !sameKeySet(obj, []string{"schema", "worker", "verifiers"}) {
		return nil, formatError("execution record has an invalid shape")
	}
	workerValue, _ := obj.Get("worker")
	worker, err := attempt(workerValue, "worker")
	if err != nil {
		return nil, err
	}
	verifiersValue, _ := obj.Get("verifiers")
	verifiersList, ok := verifiersValue.([]any)
	if !ok {
		return nil, formatError("verifier reservations are not a list")
	}
	verifiers := make([]*ordjson.Object, 0, len(verifiersList))
	for _, v := range verifiersList {
		verifier, err := attempt(v, "verifier")
		if err != nil {
			return nil, err
		}
		verifiers = append(verifiers, verifier)
	}
	ids := map[string]bool{}
	workerID, _ := worker.Get("id")
	ids[fmt.Sprint(workerID)] = true
	for _, v := range verifiers {
		id, _ := v.Get("id")
		key := fmt.Sprint(id)
		if ids[key] {
			return nil, formatError("execution record contains duplicate attempt ids")
		}
		ids[key] = true
	}
	return &Execution{Worker: worker, Verifiers: verifiers}, nil
}

func Held(task *ordjson.Object) ([]*ordjson.Object, error) {
	execution, err := GetExecution(task)
	if err != nil {
		return nil, err
	}
	if execution == nil {
		status, _ := task.Get("status")
		if status == "archived" {
			return []*ordjson.Object{}, nil
		}
		id, _ := task.Get("id")
		legacy := ordjson.NewObject()
		legacy.Set("id", fmt.Sprintf("legacy:%v", id))
		legacy.Set("kind", "worker")
		legacy.Set("state", "held")
		return []*ordjson.Object{legacy}, nil
	}
	var held []*ordjson.Object
	rows := append([]*ordjson.Object{execution.Worker}, execution.Verifiers...)
	for _, row := range rows {
		state, _ := row.Get("state")
		if s, ok := state.(string); ok && heldStates[s] {
			held = append(held, row)
		}
	}
	if held == nil {
		held = []*ordjson.Object{}
	}
	return held, nil
}

func NewAttempt(kind string, owner *ordjson.Object, checkout any, stamp string, state string, candidate any) (*ordjson.Object, error) {
	if state == "" {
		state = "held"
	}
	if !kinds[kind] || !allStates[state] {
		return nil, formatError("invalid new reservation")
	}
	id, err := newID("x-")
	if err != nil {
		return nil, err
	}
	attempt := ordjson.NewObject()
	attempt.Set("id", id)
	attempt.Set("kind", kind)
	attempt.Set("state", state)
	attempt.Set("generation", json.Number("1"))
	attempt.Set("owner", cloneObject(owner))
	attempt.Set("checkout", checkout)
	attempt.Set("candidate", candidate)
	attempt.Set("created_at", stamp)
	attempt.Set("updated_at", stamp)
	attempt.Set("observations", []any{})
	return attempt, nil
}

func NewExecution(worker *ordjson.Object) *ordjson.Object {
	value := ordjson.NewObject()
	value.Set("schema", json.Number(fmt.Sprint(Schema)))
	value.Set("worker", worker)
	value.Set("verifiers", []any{})
	return value
}

func Worker(task *ordjson.Object) (*ordjson.Object, error) {
	execution, err := GetExecution(task)
	if err != nil {
		return nil, err
	}
	if execution == nil {
		return nil, formatError("legacy task has no adoptable worker reservation")
	}
	return execution.Worker, nil
}

func Transition(task *ordjson.Object, attemptID, state, stamp string, observation *ordjson.Object, expectedGeneration *int64) (*ordjson.Object, error) {
	if !allStates[state] {
		return nil, formatError("invalid reservation transition state")
	}
	execution, err := GetExecution(task)
	if err != nil {
		return nil, err
	}
	if execution == nil {
		return nil, formatError("legacy task has no execution reservation")
	}
	rows := append([]*ordjson.Object{execution.Worker}, execution.Verifiers...)
	var matches []*ordjson.Object
	for _, row := range rows {
		id, _ := row.Get("id")
		if id == attemptID {
			matches = append(matches, row)
		}
	}
	if len(matches) != 1 {
		return nil, formatError("attempt %s is not current", attemptID)
	}
	row := matches[0]
	if expectedGeneration != nil {
		genValue, _ := row.Get("generation")
		gen, ok := genValue.(json.Number)
		n, convErr := gen.Int64()
		if !ok || convErr != nil || n != *expectedGeneration {
			return nil, formatError("attempt %s changed during observation", attemptID)
		}
	}
	row.Set("state", state)
	genValue, _ := row.Get("generation")
	gen, _ := genValue.(json.Number)
	n, _ := gen.Int64()
	row.Set("generation", json.Number(fmt.Sprint(n+1)))
	row.Set("updated_at", stamp)
	if observation != nil {
		obsValue, _ := row.Get("observations")
		list, _ := obsValue.([]any)
		if len(list) > 19 {
			list = list[len(list)-19:]
		}
		list = append(list, cloneObject(observation))
		row.Set("observations", list)
	}
	return row, nil
}

func ReplaceWorker(task *ordjson.Object, attempt *ordjson.Object) error {
	execution, err := GetExecution(task)
	if err != nil {
		return err
	}
	if execution == nil {
		return formatError("legacy task has no execution reservation")
	}
	value, _ := task.Get("execution")
	obj, _ := value.(*ordjson.Object)
	obj.Set("worker", attempt)
	task.Set("execution", obj)
	return nil
}

func AddVerifier(task *ordjson.Object, attempt *ordjson.Object) error {
	execution, err := GetExecution(task)
	if err != nil {
		return err
	}
	if execution == nil {
		return formatError("legacy task has no execution reservation")
	}
	value, _ := task.Get("execution")
	obj, _ := value.(*ordjson.Object)
	verifiersValue, _ := obj.Get("verifiers")
	list, _ := verifiersValue.([]any)
	obj.Set("verifiers", append(list, attempt))
	task.Set("execution", obj)
	return nil
}

func newID(prefix string) (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}

func cloneObject(obj *ordjson.Object) *ordjson.Object {
	if obj == nil {
		return nil
	}
	cloned := ordjson.NewObject()
	for _, key := range obj.Keys() {
		value, _ := obj.Get(key)
		cloned.Set(key, value)
	}
	return cloned
}
