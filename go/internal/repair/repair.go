package repair

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const Schema = 1
const DefaultAllowance = 2

var (
	keyPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	operationIDPat  = regexp.MustCompile(`^r-[0-9a-f]{12}$`)
	attemptIDPat    = regexp.MustCompile(`^x-[0-9a-f]{12}$`)
	operationKinds  = map[string]bool{"send": true, "resume": true}
	operationStates = map[string]bool{"reserved": true, "in-flight": true, "submitted": true, "uncertain": true}
	grantStatuses   = map[string]bool{"answered": true, "applied": true}
)

func ValidKey(value string) bool {
	return keyPattern.MatchString(value)
}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func intOf(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case int:
		return int64(t), true
	case int64:
		return t, true
	default:
		return 0, false
	}
}

func timestamp(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return false
	}
	return parsed.Location() != time.Local || parsed.UTC().Equal(parsed)
}

func emptyLedger() *ordjson.Object {
	value := ordjson.NewObject()
	value.Set("schema", jsonInt(Schema))
	value.Set("default_allowance", jsonInt(DefaultAllowance))
	value.Set("consumed", jsonInt(0))
	value.Set("operations", []any{})
	value.Set("grants", []any{})
	return value
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func Ledger(task *ordjson.Object) (*ordjson.Object, error) {
	value, has := task.Get("repairs")
	if !has {
		value = emptyLedger()
		task.Set("repairs", value)
	}
	obj := asObject(value)
	schemaN, schemaOK := intOf(func() any {
		if obj == nil {
			return nil
		}
		v, _ := obj.Get("schema")
		return v
	}())
	allowanceN, allowanceOK := intOf(func() any {
		if obj == nil {
			return nil
		}
		v, _ := obj.Get("default_allowance")
		return v
	}())
	consumedN, consumedOK := intOf(func() any {
		if obj == nil {
			return nil
		}
		v, _ := obj.Get("consumed")
		return v
	}())
	operations := asList(func() any {
		if obj == nil {
			return nil
		}
		v, _ := obj.Get("operations")
		return v
	}())
	grants := asList(func() any {
		if obj == nil {
			return nil
		}
		v, _ := obj.Get("grants")
		return v
	}())
	valid := obj != nil && schemaOK && schemaN == Schema && allowanceOK && allowanceN == DefaultAllowance && consumedOK && operations != nil && grants != nil && consumedN == int64(len(operations))
	if !valid {
		return nil, fmt.Errorf("Malformed repair accounting; corrective work is refused.")
	}
	keys := map[string]bool{}
	ids := map[string]bool{}
	for _, raw := range operations {
		op := asObject(raw)
		kind := asString(func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("kind")
			return v
		}())
		key := asString(func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("key")
			return v
		}())
		state := asString(func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("state")
			return v
		}())
		id := asString(func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("id")
			return v
		}())
		attempt := asString(func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("attempt")
			return v
		}())
		text := asString(func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("text")
			return v
		}())
		created := func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("created_at")
			return v
		}()
		pidN, pidOK := intOf(func() any {
			if op == nil {
				return nil
			}
			v, _ := op.Get("pid")
			return v
		}())
		pair := kind + "\x00" + key
		if op == nil || !ValidKey(key) || !operationKinds[kind] || keys[pair] || !operationStates[state] || !timestamp(created) || !pidOK || pidN <= 0 || id == "" || attempt == "" || text == "" || asString(created) == "" || !operationIDPat.MatchString(id) || !attemptIDPat.MatchString(attempt) || ids[id] {
			return nil, fmt.Errorf("Malformed repair operation; corrective work is refused.")
		}
		keys[pair] = true
		ids[id] = true
	}
	questionsSeen := map[string]bool{}
	questionsValue, _ := task.Get("questions")
	questions := asList(questionsValue)
	for _, raw := range grants {
		grant := asObject(raw)
		additionalN, additionalOK := intOf(func() any {
			if grant == nil {
				return nil
			}
			v, _ := grant.Get("additional")
			return v
		}())
		questionID := asString(func() any {
			if grant == nil {
				return nil
			}
			v, _ := grant.Get("question")
			return v
		}())
		text := asString(func() any {
			if grant == nil {
				return nil
			}
			v, _ := grant.Get("text")
			return v
		}())
		at := func() any {
			if grant == nil {
				return nil
			}
			v, _ := grant.Get("at")
			return v
		}()
		by := asObject(func() any {
			if grant == nil {
				return nil
			}
			v, _ := grant.Get("by")
			return v
		}())
		byOK := by != nil
		if byOK {
			for _, field := range []string{"machine", "session", "pane"} {
				v, _ := by.Get(field)
				s, is := v.(string)
				if !is || s == "" {
					byOK = false
					break
				}
			}
		}
		if grant == nil || !additionalOK || additionalN <= 0 || questionID == "" || text == "" || asString(at) == "" || !timestamp(at) || questionsSeen[questionID] || !byOK {
			return nil, fmt.Errorf("Malformed repair grant; corrective work is refused.")
		}
		var question *ordjson.Object
		for _, qv := range questions {
			q := asObject(qv)
			if q == nil {
				continue
			}
			id, _ := q.Get("id")
			if id == questionID {
				question = q
				break
			}
		}
		decision := asObject(func() any {
			if question == nil {
				return nil
			}
			v, _ := question.Get("decision")
			return v
		}())
		kind, _ := func() (any, bool) {
			if decision == nil {
				return nil, false
			}
			return decision.Get("kind")
		}()
		status := asString(func() any {
			if question == nil {
				return nil
			}
			v, _ := question.Get("status")
			return v
		}())
		answer, _ := func() (any, bool) {
			if question == nil {
				return nil, false
			}
			return question.Get("answer")
		}()
		if question == nil || decision == nil || kind != "repair-allowance" || answer != text || !grantStatuses[status] {
			return nil, fmt.Errorf("Repair grant has no matching recorded decision.")
		}
		questionsSeen[questionID] = true
	}
	return obj, nil
}

func Allowance(value *ordjson.Object) int64 {
	base, _ := intOf(func() any { v, _ := value.Get("default_allowance"); return v }())
	for _, raw := range asList(func() any { v, _ := value.Get("grants"); return v }()) {
		grant := asObject(raw)
		n, _ := intOf(func() any {
			if grant == nil {
				return nil
			}
			v, _ := grant.Get("additional")
			return v
		}())
		base += n
	}
	return base
}

func RefuseActive(task *ordjson.Object, launch bool) error {
	if _, has := task.Get("repairs"); !has {
		return nil
	}
	value, err := Ledger(task)
	if err != nil {
		return err
	}
	worker, workerErr := reservations.Worker(task)
	for _, raw := range asList(func() any { v, _ := value.Get("operations"); return v }()) {
		op := asObject(raw)
		state := asString(func() any { v, _ := op.Get("state"); return v }())
		if launch && state == "reserved" && workerErr == nil {
			repairID, _ := worker.Get("repair")
			opID, _ := op.Get("id")
			if repairID == opID {
				continue
			}
		}
		if state == "reserved" || state == "in-flight" {
			pidN, ok := intOf(func() any { v, _ := op.Get("pid"); return v }())
			if !ok {
				return fmt.Errorf("A corrective instruction is in flight; execution changes are refused.")
			}
			running, runErr := proc.PIDRunning(int(pidN))
			if runErr != nil {
				return runErr
			}
			if running {
				return fmt.Errorf("A corrective instruction is in flight; execution changes are refused.")
			}
		}
	}
	return nil
}

func RefuseDuringCleanup(task *ordjson.Object, action string) error {
	if err := RefuseActive(task, action == "Worker start"); err != nil {
		return err
	}
	recordValue, _ := task.Get("cleanup")
	record := asObject(recordValue)
	if record != nil {
		if intent, ok := record.Get("intent"); ok && intent != nil && intent != false {
			id, _ := task.Get("id")
			return fmt.Errorf("%s is refused because cleanup intent is active for task %v; no execution side effect occurred.", action, id)
		}
	}
	return nil
}

func exhaustion(task *ordjson.Object) error {
	value, err := Ledger(task)
	if err != nil {
		return err
	}
	limit := Allowance(value)
	decisionMatch := func(q *ordjson.Object) bool {
		decision := asObject(func() any { v, _ := q.Get("decision"); return v }())
		if decision == nil {
			return false
		}
		kind, _ := decision.Get("kind")
		allowance, _ := intOf(func() any { v, _ := decision.Get("allowance"); return v }())
		return kind == "repair-allowance" && allowance == limit
	}
	questions := asList(func() any { v, _ := task.Get("questions"); return v }())
	var question *ordjson.Object
	for _, raw := range questions {
		q := asObject(raw)
		if q != nil && decisionMatch(q) {
			question = q
			break
		}
	}
	if question == nil {
		id, err := newQuestionID()
		if err != nil {
			return err
		}
		question = ordjson.NewObject()
		question.Set("id", id)
		question.Set("key", nil)
		question.Set("text", "The controlled repair allowance is exhausted. Decide whether to authorize additional iterations.")
		question.Set("status", "open")
		question.Set("created_at", store.Now())
		question.Set("answer", nil)
		decision := ordjson.NewObject()
		decision.Set("kind", "repair-allowance")
		decision.Set("allowance", jsonInt(int(limit)))
		question.Set("decision", decision)
		task.Set("questions", append(questions, question))
		task.Set("status", "waiting")
		ask.SupersedeAttention(task, "question "+id)
	}
	id, _ := question.Get("id")
	return fmt.Errorf("Repair allowance is exhausted; human decision %v is required.", id)
}

func CheckAllowance(s *store.Store, task *ordjson.Object) error {
	value, err := Ledger(task)
	if err != nil {
		return err
	}
	consumed, _ := intOf(func() any { v, _ := value.Get("consumed"); return v }())
	if consumed >= Allowance(value) {
		err := exhaustion(task)
		if saveErr := s.SaveTask(task); saveErr != nil {
			return saveErr
		}
		return err
	}
	return nil
}

func RecordResume(task, successor *ordjson.Object) error {
	value, err := Ledger(task)
	if err != nil {
		return err
	}
	id, err := newOperationID()
	if err != nil {
		return err
	}
	resumes, _ := successor.Get("resumes")
	operation := ordjson.NewObject()
	operation.Set("id", id)
	operation.Set("kind", "resume")
	operation.Set("key", fmt.Sprintf("resume-%v", resumes))
	attempt, _ := successor.Get("id")
	operation.Set("attempt", attempt)
	operation.Set("text", "Resume the approved task.")
	operation.Set("created_at", store.Now())
	operation.Set("state", "reserved")
	operation.Set("pid", jsonInt(os.Getpid()))
	ops := asList(func() any { v, _ := value.Get("operations"); return v }())
	value.Set("operations", append(ops, operation))
	consumed, _ := intOf(func() any { v, _ := value.Get("consumed"); return v }())
	value.Set("consumed", jsonInt(int(consumed+1)))
	successor.Set("repair", id)
	return nil
}

func newOperationID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "r-" + hex.EncodeToString(buf), nil
}

func newQuestionID() (string, error) {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "q-" + hex.EncodeToString(buf), nil
}

type ExtendArgs struct {
	TaskID     string
	Question   string
	Additional int
	Approved   bool
	Text       string
}

func Extend(s *store.Store, ctx *ordjson.Object, args ExtendArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if !args.Approved || args.Additional <= 0 {
		return nil, fmt.Errorf("A positive additional allowance and --approved actual human decision are required.")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(args.TaskID)
	if err != nil {
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		return nil, err
	}
	value, err := Ledger(task)
	if err != nil {
		return nil, err
	}
	for _, raw := range asList(func() any { v, _ := value.Get("grants"); return v }()) {
		grant := asObject(raw)
		question, _ := grant.Get("question")
		if question == args.Question {
			additional, _ := intOf(func() any { v, _ := grant.Get("additional"); return v }())
			text, _ := grant.Get("text")
			if additional != int64(args.Additional) || text != args.Text {
				return nil, fmt.Errorf("This budget decision already has a different grant.")
			}
			result := ordjson.NewObject()
			result.Set("task", args.TaskID)
			result.Set("grant", grant)
			result.Set("duplicate", true)
			return result, nil
		}
	}
	var question *ordjson.Object
	for _, raw := range asList(func() any { v, _ := task.Get("questions"); return v }()) {
		q := asObject(raw)
		id, _ := q.Get("id")
		if id == args.Question {
			question = q
			break
		}
	}
	status := asString(func() any {
		if question == nil {
			return nil
		}
		v, _ := question.Get("status")
		return v
	}())
	decision := asObject(func() any {
		if question == nil {
			return nil
		}
		v, _ := question.Get("decision")
		return v
	}())
	kind, _ := func() (any, bool) {
		if decision == nil {
			return nil, false
		}
		return decision.Get("kind")
	}()
	allowanceN, _ := intOf(func() any {
		if decision == nil {
			return nil
		}
		v, _ := decision.Get("allowance")
		return v
	}())
	consumed, _ := intOf(func() any { v, _ := value.Get("consumed"); return v }())
	if question == nil || (status != "open" && status != "answered" && status != "applied") || kind != "repair-allowance" || allowanceN != Allowance(value) || consumed < Allowance(value) {
		return nil, fmt.Errorf("A decision for the current exhausted allowance is required.")
	}
	if status != "open" {
		answer, _ := question.Get("answer")
		if answer != args.Text {
			return nil, fmt.Errorf("Explicit confirmation must preserve the already recorded decision.")
		}
	}
	stamp := store.Now()
	by := ordjson.NewObject()
	for _, field := range []string{"machine", "session", "pane"} {
		v, _ := ctx.Get(field)
		by.Set(field, v)
	}
	grant := ordjson.NewObject()
	grant.Set("question", args.Question)
	grant.Set("additional", jsonInt(args.Additional))
	grant.Set("text", args.Text)
	grant.Set("at", stamp)
	grant.Set("by", by)
	if status == "open" {
		question.Set("answer", args.Text)
		question.Set("answered_at", stamp)
		question.Set("status", "answered")
	}
	grants := asList(func() any { v, _ := value.Get("grants"); return v }())
	value.Set("grants", append(grants, grant))
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.TaskID)
	result.Set("grant", grant)
	result.Set("duplicate", false)
	return result, nil
}

type SendArgs struct {
	TaskID       string
	Attempt      string
	Key          string
	Text         string
	RuntimeRoot  string
}

func Send(s *store.Store, ctx *ordjson.Object, args SendArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if !ValidKey(args.Key) {
		return nil, fmt.Errorf("Repair key must be a bounded stable identifier.")
	}
	deliverUnlock, err := s.DeliveryLock()
	if err != nil {
		return nil, err
	}
	defer deliverUnlock()

	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(args.TaskID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		unlock()
		return nil, err
	}
	value, err := Ledger(task)
	if err != nil {
		unlock()
		return nil, err
	}
	for _, raw := range asList(func() any { v, _ := value.Get("operations"); return v }()) {
		op := asObject(raw)
		kind, _ := op.Get("kind")
		key, _ := op.Get("key")
		if kind == "send" && key == args.Key {
			attempt, _ := op.Get("attempt")
			text, _ := op.Get("text")
			if attempt != args.Attempt || text != args.Text {
				unlock()
				return nil, fmt.Errorf("Repair key already identifies another instruction or attempt.")
			}
			id, _ := task.Get("id")
			unlock()
			result := ordjson.NewObject()
			result.Set("task", id)
			result.Set("operation", op)
			result.Set("duplicate", true)
			return result, nil
		}
	}
	if err := CheckAllowance(s, task); err != nil {
		unlock()
		return nil, err
	}
	if err := RefuseDuringCleanup(task, "Repair"); err != nil {
		unlock()
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		unlock()
		return nil, err
	}
	workerID, _ := worker.Get("id")
	workerState, _ := worker.Get("state")
	if workerID != args.Attempt || workerState != "running" {
		unlock()
		return nil, fmt.Errorf("Repair requires the exact running worker attempt.")
	}
	route := returns.ReturnRoute(task, "worker")
	generation, _ := intOf(func() any { v, _ := worker.Get("generation"); return v }())
	worktree, _ := task.Get("worktree")
	worktreeStr, _ := worktree.(string)
	unlock()

	if err := returns.ObserveRecipient(args.RuntimeRoot, route, worktreeStr); err != nil {
		return nil, err
	}

	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(args.TaskID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := RefuseDuringCleanup(current, "Repair"); err != nil {
		unlock()
		return nil, err
	}
	worker, err = reservations.Worker(current)
	if err != nil {
		unlock()
		return nil, err
	}
	endpoint := store.EndpointFromContext(routeToCtx(route))
	registration, err := s.Registration(endpoint)
	if err != nil {
		unlock()
		return nil, err
	}
	workerID, _ = worker.Get("id")
	workerState, _ = worker.Get("state")
	genNow, _ := intOf(func() any { v, _ := worker.Get("generation"); return v }())
	currentRoute := returns.ReturnRoute(current, "worker")
	role, _ := func() (any, bool) {
		if registration == nil {
			return nil, false
		}
		return registration.Get("role")
	}()
	regTask, _ := func() (any, bool) {
		if registration == nil {
			return nil, false
		}
		return registration.Get("task")
	}()
	if workerID != args.Attempt || genNow != generation || workerState != "running" || !sameRoute(currentRoute, route) || registration == nil || role != "worker" || regTask != args.TaskID {
		unlock()
		return nil, fmt.Errorf("Worker identity changed before corrective delivery; nothing sent.")
	}
	value, err = Ledger(current)
	if err != nil {
		unlock()
		return nil, err
	}
	opID, err := newOperationID()
	if err != nil {
		unlock()
		return nil, err
	}
	operation := ordjson.NewObject()
	operation.Set("id", opID)
	operation.Set("kind", "send")
	operation.Set("key", args.Key)
	operation.Set("attempt", args.Attempt)
	operation.Set("text", args.Text)
	operation.Set("created_at", store.Now())
	operation.Set("state", "in-flight")
	operation.Set("pid", jsonInt(os.Getpid()))
	ops := asList(func() any { v, _ := value.Get("operations"); return v }())
	value.Set("operations", append(ops, operation))
	consumed, _ := intOf(func() any { v, _ := value.Get("consumed"); return v }())
	value.Set("consumed", jsonInt(int(consumed+1)))
	if err := s.SaveTask(current); err != nil {
		unlock()
		return nil, err
	}
	unlock()

	herdrPath, err := toolpath.Find(args.RuntimeRoot, "herdr")
	var sendErr error
	if err != nil {
		sendErr = err
	} else {
		session := asString(func() any { v, _ := route.Get("session"); return v }())
		pane := asString(func() any { v, _ := route.Get("pane"); return v }())
		_, sendErr = herdrclient.Call(herdrPath, session, 5*time.Second, "agent", "prompt", pane, args.Text)
	}

	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err = s.ReadTask(args.TaskID)
	if err != nil {
		return nil, err
	}
	value, err = Ledger(current)
	if err != nil {
		return nil, err
	}
	var saved *ordjson.Object
	for _, raw := range asList(func() any { v, _ := value.Get("operations"); return v }()) {
		op := asObject(raw)
		id, _ := op.Get("id")
		if id == opID {
			saved = op
			break
		}
	}
	if saved == nil {
		return nil, fmt.Errorf("Repair operation %s is missing after delivery.", opID)
	}
	if sendErr != nil {
		saved.Set("state", "uncertain")
	} else {
		saved.Set("state", "submitted")
	}
	if err := s.SaveTask(current); err != nil {
		return nil, err
	}
	if sendErr != nil {
		return nil, fmt.Errorf("Corrective delivery is uncertain and remains charged: %s", sendErr)
	}
	result := ordjson.NewObject()
	result.Set("task", args.TaskID)
	result.Set("operation", saved)
	result.Set("duplicate", false)
	return result, nil
}

func routeToCtx(route *ordjson.Object) *ordjson.Object {
	ctx := ordjson.NewObject()
	for _, key := range []string{"machine", "session", "pane", "cwd"} {
		v, _ := route.Get(key)
		ctx.Set(key, v)
	}
	return ctx
}

func sameRoute(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return a == b
	}
	for _, key := range []string{"machine", "session", "pane", "cwd", "recipient", "role"} {
		av, _ := a.Get(key)
		bv, _ := b.Get(key)
		if fmt.Sprint(av) != fmt.Sprint(bv) {
			return false
		}
	}
	return true
}
