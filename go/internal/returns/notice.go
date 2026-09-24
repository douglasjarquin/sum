package returns

import (
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// legacyReasons names the latest attempt in the legacy `notice` shape, by the kind of return it carried.
var legacyReasons = []struct{ prefix, reason string }{
	{"question:", "a decision is waiting"},
	{"answer:", "an answer has been recorded"},
	{"report:", "a worker report is available"},
	{"review:", "a review verdict is recorded"},
}

const defaultLegacyReason = "saved task state needs attention"

// Notice is the legacy single `notice` view of a task: its latest delivery attempt of a question, answer, report,
// review, or attention return, read from the task's returns sidecar. The sidecar is the only persisted authority; the task
// record's own `notice` field is history an older release wrote, returned unchanged only when the sidecar holds no
// such attempt (or could not be read). Nothing decides delivery from this view.
func Notice(task, returnsObj *ordjson.Object) any {
	if latest := latestNoticeDelivery(returnsObj); latest != nil {
		return legacyNotice(latest)
	}
	persisted, _ := task.Get("notice")
	return persisted
}

// NoticeOf reads the task's returns sidecar and projects Notice from it.
func NoticeOf(s *store.Store, task *ordjson.Object) any {
	id, _ := task.Get("id")
	idStr, _ := id.(string)
	returnsObj, err := ReadReturns(s, idStr)
	if err != nil {
		returnsObj = nil
	}
	return Notice(task, returnsObj)
}

// SetNotice replaces a task view's copied `notice` with the derived one; a record that never carried the field and has
// no attempt to project gets none.
func SetNotice(s *store.Store, task, view *ordjson.Object) {
	notice := NoticeOf(s, task)
	if _, recorded := task.Get("notice"); recorded || notice != nil {
		view.Set("notice", notice)
	}
}

func latestNoticeDelivery(returnsObj *ordjson.Object) *ordjson.Object {
	if returnsObj == nil {
		return nil
	}
	deliveriesValue, _ := returnsObj.Get("deliveries")
	list, _ := deliveriesValue.([]any)
	for i := len(list) - 1; i >= 0; i-- {
		delivery, _ := list[i].(*ordjson.Object)
		if delivery != nil && len(noticeObligations(delivery)) > 0 {
			return delivery
		}
	}
	return nil
}

// noticeObligations lists a delivery's obligation ids other than brief-refresh requests.
func noticeObligations(delivery *ordjson.Object) []string {
	value, _ := delivery.Get("obligations")
	list, _ := value.([]any)
	var ids []string
	for _, raw := range list {
		if id, ok := raw.(string); ok && !strings.HasPrefix(id, "refresh:") {
			ids = append(ids, id)
		}
	}
	return ids
}

func legacyNotice(delivery *ordjson.Object) *ordjson.Object {
	state, _ := delivery.Get("state")
	reasonText, _ := delivery.Get("reason")
	status, errText := "pending", reasonText
	switch state {
	case "submitted":
		status, errText = "submitted-not-acknowledged", nil
	case "uncertain":
		status = "uncertain"
	case "in-flight":
		status, errText = "uncertain", interruptedReason
	}
	at, _ := delivery.Get("finished_at")
	if at == nil {
		at, _ = delivery.Get("at")
	}
	recipientValue, _ := delivery.Get("recipient")
	recipientObj, _ := recipientValue.(*ordjson.Object)
	recipient := routeValue(recipientObj, "recipient")
	id, _ := delivery.Get("id")
	notice := ordjson.NewObject()
	notice.Set("at", at)
	notice.Set("recipient", recipient)
	notice.Set("reason", legacyReason(noticeObligations(delivery)))
	notice.Set("status", status)
	notice.Set("delivery", id)
	if errText != nil && errText != "" {
		notice.Set("error", errText)
	}
	return notice
}

func legacyReason(ids []string) string {
	for _, kind := range legacyReasons {
		for _, id := range ids {
			if strings.HasPrefix(id, kind.prefix) {
				return kind.reason
			}
		}
	}
	return defaultLegacyReason
}
