package incarnation

import "github.com/douglasjarquin/sum/go/internal/ordjson"

// Session is a historical integration conversation, used only for saved usage attribution.
type Session struct{ Agent, Kind, Value string }

// HistoricalInfo projects saved pre-HC records; it never grants live authority.
type HistoricalInfo struct {
	Terminal string
	Agent    string
	Session  *Session
}

// FromInfo reads a historical record without observing or changing a runtime.
func FromInfo(value any) HistoricalInfo {
	obj := unwrap(value)
	if obj == nil {
		return HistoricalInfo{}
	}
	e := HistoricalInfo{Terminal: str(obj, "terminal_id"), Agent: str(obj, "agent")}
	if session, ok := field(obj, "agent_session").(*ordjson.Object); ok {
		e.Session = parseSession(session)
	}
	return e
}

func unwrap(value any) *ordjson.Object {
	obj, _ := value.(*ordjson.Object)
	if inner, ok := field(obj, "pane").(*ordjson.Object); ok {
		obj = inner
	}
	if inner, ok := field(obj, "agent").(*ordjson.Object); ok {
		obj = inner
	}
	return obj
}

func parseSession(obj *ordjson.Object) *Session {
	s := &Session{Agent: str(obj, "agent"), Kind: str(obj, "kind"), Value: str(obj, "value")}
	if s.Value == "" {
		return nil
	}
	return s
}
