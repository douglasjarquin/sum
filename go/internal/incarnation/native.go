package incarnation

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

// NativeEvidence contains only public HC process facts. It never substitutes
// legacy agent_session or HC's private program_started_ms for an init receipt.
type NativeEvidence struct {
	Endpoint   hcclient.Endpoint    `json:"endpoint"`
	PID        uint32               `json:"pid"`
	Foreground *hcclient.Foreground `json:"foreground"`
}
type NativeReceipt struct {
	Endpoint hcclient.Endpoint `json:"endpoint"`
	Kind     string            `json:"kind"`
	Attempt  string            `json:"attempt"`
}

func (e NativeEvidence) Record(at string) *ordjson.Object {
	object, _ := ordjson.FromValue(e)
	object.Set("schema", json.Number("2"))
	object.Set("observed_at", at)
	return object
}

// JudgeNative requires a current binding and matching application bootstrap
// receipt as well as public runtime/process identity. Labels, cwd, environment,
// status, pid alone and native turn counters cannot adopt an attempt.
func JudgeNative(recorded any, current hcclient.Endpoint, observed hcclient.Terminal, receipt *NativeReceipt) Verdict {
	object, ok := recorded.(*ordjson.Object)
	if !ok || object == nil {
		return refused(Unrecorded, "no native binding evidence; explicit bootstrap or handover is required")
	}
	raw, err := ordjson.MarshalCompact(object)
	if err != nil {
		return refused(Unobservable, err.Error())
	}
	var rec NativeEvidence
	if err = json.Unmarshal(raw, &rec); err != nil {
		return refused(Unrecorded, "native binding evidence is malformed")
	}
	if !rec.Endpoint.SameAuthority(current) {
		return refused(Replaced, "core, terminal, task, role, attempt or binding generation differs from the recorded authority")
	}
	if observed.Address != current.Address || observed.CreatedMS != rec.Endpoint.CreatedMS || observed.State != "live" || observed.Keep {
		return refused(Replaced, "the recorded non-kept terminal is absent, finished or replaced")
	}
	if observed.PID == nil || rec.PID == 0 || *observed.PID != rec.PID || observed.Foreground == nil || rec.Foreground == nil {
		return refused(Unobservable, "the recorded terminal program and foreground cannot be verified")
	}
	if !reflect.DeepEqual(observed.Foreground, rec.Foreground) {
		return refused(Replaced, "the terminal foreground process changed; a name or address does not adopt its replacement")
	}
	expectedKind := map[string]string{"worker": "worker-init", "reviewer": "reviewer-init", "coordinator": "coordinator-init", "service": "service-start"}[current.Role]
	if receipt == nil || expectedKind == "" || receipt.Kind != expectedKind || receipt.Attempt != current.Attempt || !receipt.Endpoint.SameAuthority(current) {
		return refused(Unrecorded, "no matching native bootstrap receipt for this current role and attempt")
	}
	return verified(Same, fmt.Sprintf("same native core %s terminal %s, process and receipt-bound attempt", current.CoreID, current.Address))
}
