package machine

import (
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"testing"
)

func TestSameEndpointRequiresNativeAuthority(t *testing.T) {
	host := Identity{ID: "host", aliases: []string{"legacy-host"}}
	base := hcclient.Endpoint{Schema: 2, Backend: "hc", Machine: "host", CoreID: "core", Address: "t1", Socket: "/tmp/hc.sock", Session: "lab", Task: "task", Role: "worker", Attempt: "attempt", BindingGeneration: 1, CreatedMS: 5}
	object := func(e hcclient.Endpoint) *ordjson.Object {
		o, err := ordjson.FromValue(e)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	if !host.SameEndpoint(object(base), object(base)) {
		t.Fatal("same native binding rejected")
	}
	for name, mutate := range map[string]func(*hcclient.Endpoint){
		"machine":    func(e *hcclient.Endpoint) { e.Machine = "other-host" },
		"core":       func(e *hcclient.Endpoint) { e.CoreID = "other" },
		"address":    func(e *hcclient.Endpoint) { e.Address = "t2" },
		"task":       func(e *hcclient.Endpoint) { e.Task = "other" },
		"role":       func(e *hcclient.Endpoint) { e.Role = "reviewer" },
		"attempt":    func(e *hcclient.Endpoint) { e.Attempt = "other" },
		"generation": func(e *hcclient.Endpoint) { e.BindingGeneration++ },
		"creation":   func(e *hcclient.Endpoint) { e.CreatedMS++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if host.SameEndpoint(object(base), object(changed)) {
				t.Fatal("different binding authorized")
			}
		})
	}
	alias := base
	alias.Machine = "legacy-host"
	if !host.SameEndpoint(object(base), object(alias)) {
		t.Fatal("known machine alias lost canonical identity")
	}
	route := base
	route.Socket = "/tmp/moved.sock"
	route.Session = "renamed"
	if !host.SameEndpoint(object(base), object(route)) {
		t.Fatal("routing changed authority")
	}
	old := ordjson.NewObject()
	old.Set("machine", "host")
	old.Set("session", "lab")
	old.Set("pane", "p1")
	if host.SameEndpoint(old, old) {
		t.Fatal("historical pane grants native authority")
	}
}
