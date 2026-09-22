package repair

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const testTaskID = "t-aaaaaaaaaaaa"

func decode(t *testing.T, raw string) *ordjson.Object {
	t.Helper()
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		t.Fatal("fixture is not an object")
	}
	return obj
}

func sendOp(id, key, class, reason string) string {
	extra := ""
	if class != "" {
		extra += fmt.Sprintf(`, "class": %q`, class)
	}
	if reason != "" {
		extra += fmt.Sprintf(`, "reason": %q`, reason)
	}
	return fmt.Sprintf(`{"id": %q, "kind": "send", "key": %q, "attempt": "x-aaaaaaaaaaaa", "text": "fix it",
"created_at": "2026-01-01T00:00:00+00:00", "state": "submitted", "pid": 4242%s}`, id, key, extra)
}

func taskWith(t *testing.T, repairs string) *ordjson.Object {
	t.Helper()
	return decode(t, fmt.Sprintf(`{"schema": 1, "id": %q, "questions": [], %s}`, testTaskID, repairs))
}

func ledgerFixture(ops []string, consumed int) string {
	return fmt.Sprintf(`"repairs": {"schema": 1, "default_allowance": 2, "consumed": %d, "operations": [%s], "grants": []}`,
		consumed, strings.Join(ops, ","))
}

func TestLedger_legacyOperationsAllCountAsConsumed(t *testing.T) {
	task := taskWith(t, ledgerFixture([]string{sendOp("r-aaaaaaaaaaa1", "k1", "", ""), sendOp("r-aaaaaaaaaaa2", "k2", "", "")}, 2))
	value, err := Ledger(task)
	if err != nil {
		t.Fatalf("legacy ledger refused: %v", err)
	}
	if got := Allowance(value); got != 2 {
		t.Fatalf("allowance = %d", got)
	}
}

func TestLedger_onlyExpansionOperationsConsume(t *testing.T) {
	task := taskWith(t, ledgerFixture([]string{
		sendOp("r-aaaaaaaaaaa1", "k1", ClassInScope, ""),
		sendOp("r-aaaaaaaaaaa2", "k2", ClassExpansion, "outside the approved brief"),
		sendOp("r-aaaaaaaaaaa3", "k3", ClassInScope, "gate required"),
	}, 1))
	if _, err := Ledger(task); err != nil {
		t.Fatalf("classified ledger refused: %v", err)
	}
}

func TestLedger_refusesInconsistentRecords(t *testing.T) {
	cases := []struct {
		name    string
		repairs string
	}{
		{"consumed below consuming ops", ledgerFixture([]string{sendOp("r-aaaaaaaaaaa1", "k1", ClassExpansion, "out")}, 0)},
		{"consumed above consuming ops", ledgerFixture([]string{sendOp("r-aaaaaaaaaaa1", "k1", ClassInScope, "")}, 1)},
		{"unknown class", ledgerFixture([]string{sendOp("r-aaaaaaaaaaa1", "k1", "bogus", "")}, 1)},
		{"expansion without reason", ledgerFixture([]string{sendOp("r-aaaaaaaaaaa1", "k1", ClassExpansion, "")}, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Ledger(taskWith(t, tc.repairs)); err == nil {
				t.Fatal("malformed ledger was accepted")
			}
		})
	}
}

// Lifecycle: a grant was recorded against an answered question, the worker
// died, and cleanup settled the question. The grant must still match its
// recorded decision so park, cleanup, and archive can run; an open or
// mismatched question still refuses.
func TestLedger_grantMatchesSettledDecision(t *testing.T) {
	grant := `{"question": "q-aaaaaaaaaa", "additional": 1, "text": "add one",
"at": "2026-01-01T00:00:00+00:00", "by": {"machine": "m", "session": "s", "pane": "p"}}`
	repairs := fmt.Sprintf(`"repairs": {"schema": 1, "default_allowance": 2, "consumed": 2,
"operations": [%s], "grants": [%s]}`,
		sendOp("r-aaaaaaaaaaa1", "k1", ClassExpansion, "out")+","+sendOp("r-aaaaaaaaaaa2", "k2", ClassExpansion, "out"),
		grant)
	question := func(status, answer string) string {
		return fmt.Sprintf(`{"id": "q-aaaaaaaaaa", "key": null, "text": "extend?", "status": %q,
"created_at": "2026-01-01T00:00:00+00:00", "answer": %q,
"decision": {"kind": "repair-allowance", "allowance": 2}}`, status, answer)
	}
	task := func(questions string) *ordjson.Object {
		return decode(t, fmt.Sprintf(`{"schema": 1, "id": %q, "questions": [%s], %s}`, testTaskID, questions, repairs))
	}
	for _, status := range []string{"answered", "applied", "settled"} {
		if _, err := Ledger(task(question(status, "add one"))); err != nil {
			t.Fatalf("grant against %s question refused: %v", status, err)
		}
	}
	if _, err := Ledger(task(question("settled", "different text"))); err == nil {
		t.Fatal("grant matched a settled question whose answer differs")
	}
	if _, err := Ledger(task(question("open", "add one"))); err == nil {
		t.Fatal("grant matched an open question")
	}
	if _, err := Ledger(task(`{"id": "q-bbbbbbbbbb", "key": null, "text": "other?", "status": "settled", "answer": "add one", "decision": {"kind": "repair-allowance", "allowance": 2}}`)); err == nil {
		t.Fatal("grant matched an unrelated question")
	}
}

func TestLedger_refusesEmptyReasonField(t *testing.T) {
	op := `{"id": "r-aaaaaaaaaaa1", "kind": "send", "key": "k1", "attempt": "x-aaaaaaaaaaaa", "text": "fix",
"created_at": "2026-01-01T00:00:00+00:00", "state": "submitted", "pid": 4242, "class": "in-scope", "reason": ""}`
	task := taskWith(t, ledgerFixture([]string{op}, 0))
	if _, err := Ledger(task); err == nil {
		t.Fatal("empty reason was accepted")
	}
}

func TestRecordResume_recordsInScopeWithoutCharging(t *testing.T) {
	task := taskWith(t, ledgerFixture([]string{sendOp("r-aaaaaaaaaaa1", "k1", ClassExpansion, "out")}, 1))
	successor := decode(t, `{"id": "x-bbbbbbbbbbbb", "resumes": "x-aaaaaaaaaaaa"}`)
	if err := RecordResume(task, successor); err != nil {
		t.Fatalf("RecordResume: %v", err)
	}
	value, err := Ledger(task)
	if err != nil {
		t.Fatalf("ledger after resume: %v", err)
	}
	consumed, _ := value.Get("consumed")
	if consumed != json.Number("1") {
		t.Fatalf("consumed = %v, want unchanged", consumed)
	}
	ops, _ := value.Get("operations")
	list, _ := ops.([]any)
	if len(list) != 2 {
		t.Fatalf("operations = %v", ops)
	}
	op, _ := list[1].(*ordjson.Object)
	kind, _ := op.Get("kind")
	class, _ := op.Get("class")
	if kind != "resume" || class != ClassInScope {
		t.Fatalf("resume op = %v", op)
	}
	marked, _ := successor.Get("repair")
	if marked == nil || marked == "" {
		t.Fatal("successor was not marked with the resume operation")
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func saveFixture(t *testing.T, st *store.Store, repairs string) *ordjson.Object {
	t.Helper()
	task := decode(t, fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "questions": [], %s}`, testTaskID, repairs))
	dir := filepath.Join(st.Tasks, testTaskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestCheckAllowance_inScopeOperationsNeverExhaust(t *testing.T) {
	st := newStore(t)
	task := saveFixture(t, st, ledgerFixture([]string{
		sendOp("r-aaaaaaaaaaa1", "k1", ClassInScope, ""),
		sendOp("r-aaaaaaaaaaa2", "k2", ClassInScope, ""),
		sendOp("r-aaaaaaaaaaa3", "k3", ClassInScope, ""),
	}, 0))
	if err := CheckAllowance(st, task); err != nil {
		t.Fatalf("in-scope sends were blocked: %v", err)
	}
}

func TestCheckAllowance_expansionExhaustionSavesOneQuestion(t *testing.T) {
	st := newStore(t)
	task := saveFixture(t, st, ledgerFixture([]string{
		sendOp("r-aaaaaaaaaaa1", "k1", ClassExpansion, "out"),
		sendOp("r-aaaaaaaaaaa2", "k2", ClassExpansion, "out"),
	}, 2))
	err := CheckAllowance(st, task)
	if err == nil || !strings.Contains(err.Error(), "allowance is exhausted") {
		t.Fatalf("err = %v, want exhaustion", err)
	}
	saved, readErr := st.ReadTask(testTaskID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	questions, _ := saved.Get("questions")
	list, _ := questions.([]any)
	if len(list) != 1 {
		t.Fatalf("questions = %v", questions)
	}
	q, _ := list[0].(*ordjson.Object)
	status, _ := q.Get("status")
	if status != "open" {
		t.Fatalf("question status = %v", status)
	}
	if err := CheckAllowance(st, saved); err == nil {
		t.Fatal("second exhaustion check passed")
	}
	again, _ := st.ReadTask(testTaskID)
	questions, _ = again.Get("questions")
	list, _ = questions.([]any)
	if len(list) != 1 {
		t.Fatalf("exhaustion saved %d questions, want one", len(list))
	}
}
