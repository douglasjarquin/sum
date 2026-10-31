package returns

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"strings"
	"testing"
	"time"
)

func TestPromptFailureKeepsPossibleDeliveryUncertain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault hctest.Fault
		want  string
	}{{"response lost after input", hctest.Fault{LoseResponse: true}, "uncertain"}, {"timeout after request write", hctest.Fault{Delay: time.Second}, "uncertain"}, {"native pre-input refusal", hctest.Fault{RefuseKind: "blocked"}, "not-delivered"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := hctest.New(t)
			f.SetTerminal(hcclient.Terminal{Address: "t1", State: "live", Status: &hcclient.Status{State: "idle"}})
			f.QueueFault("terminal.send", tc.fault)
			client := f.Client()
			client.Timeout = 20 * time.Millisecond
			_, err := client.Send(context.Background(), hcclient.SendParams{Terminal: "t1", Text: "hello", If: "idle"})
			if err == nil {
				t.Fatal("expected error")
			}
			state, detail := promptFailure(err)
			if state != tc.want {
				t.Fatalf("effect %s (%s)", state, detail)
			}
			if state == "uncertain" && !strings.Contains(detail, "not retried by itself") {
				t.Fatal("missing uncertainty explanation")
			}
		})
	}
	if state, _ := promptFailure(&unreachableError{state: "pending-unreachable", msg: "different machine"}); state != "not-delivered" {
		t.Fatal("unreachable recipient had possible input effect")
	}
}
