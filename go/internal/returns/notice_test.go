package returns

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func decodeObject(t *testing.T, raw string) *ordjson.Object {
	t.Helper()
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		t.Fatalf("not an object: %s", raw)
	}
	return obj
}

func sidecar(t *testing.T, deliveries string) *ordjson.Object {
	t.Helper()
	return decodeObject(t, `{"schema": 1, "task": "t-aaaaaaaaaaaa", "deliveries": [`+deliveries+`]}`)
}

func noticeField(t *testing.T, notice any, key string) any {
	t.Helper()
	obj, ok := notice.(*ordjson.Object)
	if !ok {
		t.Fatalf("notice = %#v, want an object", notice)
	}
	v, _ := obj.Get(key)
	return v
}

const parentRecipient = `{"recipient": "parent", "role": "coordinator", "machine": "m-1", "session": "s", "pane": "p", "key": "k"}`

func TestNoticeProjectsTheLatestNonRefreshDelivery(t *testing.T) {
	task := decodeObject(t, `{"id": "t-aaaaaaaaaaaa", "notice": null}`)
	cases := []struct {
		name       string
		deliveries string
		status     string
		reason     string
		err        any
	}{
		{
			name:       "submitted prompt",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "submitted", "via": "prompt", "reason": "notice submitted", "finished_at": "2026-01-01T00:00:05+00:00", "obligations": ["question:q-1"]}`,
			status:     "submitted-not-acknowledged",
			reason:     "a decision is waiting",
		},
		{
			name:       "submitted inline",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "submitted", "via": "inline", "reason": "presented", "finished_at": "2026-01-01T00:00:05+00:00", "obligations": ["report:e-1"]}`,
			status:     "submitted-not-acknowledged",
			reason:     "a worker report is available",
		},
		{
			name:       "known not delivered",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "not-delivered", "via": "prompt", "reason": "Recipient is working; notice remains pending.", "finished_at": "2026-01-01T00:00:05+00:00", "obligations": ["answer:q-1"]}`,
			status:     "pending",
			reason:     "an answer has been recorded",
			err:        "Recipient is working; notice remains pending.",
		},
		{
			name: "stalled after three failures",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "not-delivered", "reason": "busy", "obligations": ["question:q-1"]},
{"id": "d-2", "at": "2026-01-01T00:01:00+00:00", "recipient": ` + parentRecipient + `, "state": "not-delivered", "reason": "busy", "obligations": ["question:q-1"]},
{"id": "d-3", "at": "2026-01-01T00:02:00+00:00", "recipient": ` + parentRecipient + `, "state": "not-delivered", "reason": "busy again", "obligations": ["question:q-1"]}`,
			status: "pending",
			reason: "a decision is waiting",
			err:    "busy again",
		},
		{
			name:       "timed out after possible submission",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "uncertain", "via": "prompt", "reason": "prompt timed out after possible submission", "finished_at": "2026-01-01T00:00:05+00:00", "obligations": ["question:q-1"]}`,
			status:     "uncertain",
			reason:     "a decision is waiting",
			err:        "prompt timed out after possible submission",
		},
		{
			name:       "interrupted in flight",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "in-flight", "obligations": ["question:q-1"]}`,
			status:     "uncertain",
			reason:     "a decision is waiting",
			err:        "an attempt was interrupted before its outcome was recorded; delivery unknown, not retried by itself",
		},
		{
			name:       "attention only",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "submitted", "reason": "ok", "obligations": ["attention:a-1"]}`,
			status:     "submitted-not-acknowledged",
			reason:     "saved task state needs attention",
		},
		{
			name:       "question outranks report and attention in one coalesced delivery",
			deliveries: `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": ` + parentRecipient + `, "state": "submitted", "reason": "ok", "obligations": ["attention:a-1", "question:q-1", "report:e-1"]}`,
			status:     "submitted-not-acknowledged",
			reason:     "a decision is waiting",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notice := Notice(task, sidecar(t, tc.deliveries))
			if got := noticeField(t, notice, "status"); got != tc.status {
				t.Fatalf("status = %v, want %s", got, tc.status)
			}
			if got := noticeField(t, notice, "reason"); got != tc.reason {
				t.Fatalf("reason = %v, want %s", got, tc.reason)
			}
			if got := noticeField(t, notice, "recipient"); got != "parent" {
				t.Fatalf("recipient = %v", got)
			}
			if got := noticeField(t, notice, "error"); got != tc.err {
				t.Fatalf("error = %v, want %v", got, tc.err)
			}
			if got := noticeField(t, notice, "delivery"); got == nil {
				t.Fatal("delivery id missing")
			}
		})
	}
}

func TestNoticeUsesFinishTimeAndTheLatestAttempt(t *testing.T) {
	task := decodeObject(t, `{"id": "t-aaaaaaaaaaaa", "notice": null}`)
	notice := Notice(task, sidecar(t, `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": `+parentRecipient+`, "state": "not-delivered", "reason": "busy", "finished_at": "2026-01-01T00:00:01+00:00", "obligations": ["question:q-1"]},
{"id": "d-2", "at": "2026-01-01T00:05:00+00:00", "recipient": {"recipient": "worker", "role": "worker", "key": "w"}, "state": "submitted", "reason": "ok", "finished_at": "2026-01-01T00:05:02+00:00", "obligations": ["answer:q-1"]}`))
	if got := noticeField(t, notice, "delivery"); got != "d-2" {
		t.Fatalf("delivery = %v, want d-2", got)
	}
	if got := noticeField(t, notice, "at"); got != "2026-01-01T00:05:02+00:00" {
		t.Fatalf("at = %v, want finished_at", got)
	}
	if got := noticeField(t, notice, "recipient"); got != "worker" {
		t.Fatalf("recipient = %v", got)
	}
	inFlight := Notice(task, sidecar(t, `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": `+parentRecipient+`, "state": "in-flight", "obligations": ["question:q-1"]}`))
	if got := noticeField(t, inFlight, "at"); got != "2026-01-01T00:00:00+00:00" {
		t.Fatalf("in-flight at = %v, want the start time", got)
	}
}

func TestNoticeIgnoresRefreshOnlyDeliveries(t *testing.T) {
	task := decodeObject(t, `{"id": "t-aaaaaaaaaaaa", "notice": null}`)
	notice := Notice(task, sidecar(t, `{"id": "d-1", "at": "2026-01-01T00:00:00+00:00", "recipient": `+parentRecipient+`, "state": "submitted", "reason": "ok", "obligations": ["question:q-1"]},
{"id": "d-2", "at": "2026-01-01T00:05:00+00:00", "recipient": {"recipient": "worker", "key": "w"}, "state": "not-delivered", "reason": "busy", "obligations": ["refresh:r2"]}`))
	if got := noticeField(t, notice, "delivery"); got != "d-1" {
		t.Fatalf("delivery = %v, want the question delivery d-1", got)
	}
	if got := Notice(task, sidecar(t, `{"id": "d-2", "at": "2026-01-01T00:05:00+00:00", "recipient": {"recipient": "worker", "key": "w"}, "state": "submitted", "obligations": ["refresh:r2"]}`)); got != nil {
		t.Fatalf("refresh-only sidecar notice = %v, want the persisted null", got)
	}
}

func TestNoticeFallsBackToThePersistedHistoricalField(t *testing.T) {
	persisted := `{"at": "2026-01-01T00:00:00+00:00", "recipient": "parent", "reason": "a decision is waiting", "status": "submitted-not-acknowledged"}`
	task := decodeObject(t, `{"id": "t-aaaaaaaaaaaa", "notice": `+persisted+`}`)
	for name, returnsObj := range map[string]*ordjson.Object{"no deliveries": sidecar(t, ""), "unreadable sidecar": nil} {
		t.Run(name, func(t *testing.T) {
			notice := Notice(task, returnsObj)
			if got := noticeField(t, notice, "status"); got != "submitted-not-acknowledged" {
				t.Fatalf("status = %v", got)
			}
			if got := noticeField(t, notice, "delivery"); got != nil {
				t.Fatalf("delivery = %v, want the persisted value unchanged", got)
			}
		})
	}
	stale := decodeObject(t, `{"id": "t-aaaaaaaaaaaa", "notice": {"at": "2026-01-01T00:00:00+00:00", "recipient": "parent", "reason": "a decision is waiting", "status": "pending", "delivery": "d-old"}}`)
	notice := Notice(stale, sidecar(t, `{"id": "d-new", "at": "2026-01-02T00:00:00+00:00", "recipient": `+parentRecipient+`, "state": "submitted", "reason": "ok", "obligations": ["question:q-1"]}`))
	if got := noticeField(t, notice, "status"); got != "submitted-not-acknowledged" {
		t.Fatalf("a stale persisted notice was preferred over the sidecar: %v", notice)
	}
	if got := Notice(decodeObject(t, `{"id": "t-aaaaaaaaaaaa"}`), sidecar(t, "")); got != nil {
		t.Fatalf("a record without the field = %v, want nil", got)
	}
}
