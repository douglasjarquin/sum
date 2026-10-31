package updatecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func deliveryQualification(s *store.Store, tasks []*ordjson.Object) []any {
	var blocked []any
	check := func(label string, record *ordjson.Object, optional bool) {
		if record == nil {
			return
		}
		endpoint := store.EndpointFromContext(record)
		if err := endpoint.Validate(); err != nil {
			blocked = append(blocked, label+" automatic delivery qualification: "+err.Error())
			return
		}
		client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: returns.ObserveTimeout}
		terminal, err := client.Get(context.Background(), endpoint.Address)
		if err != nil {
			var rpc *hcclient.RPCError
			var detail struct {
				Kind string `json:"kind"`
			}
			if optional && errors.As(err, &rpc) && json.Unmarshal(rpc.Data, &detail) == nil && detail.Kind == "not_found" {
				return
			}
			blocked = append(blocked, label+" automatic delivery qualification: "+err.Error())
			return
		}
		if optional && terminal.State == "finished" {
			return
		}
		if terminal.State != "live" || terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS || terminal.Keep {
			blocked = append(blocked, label+" automatic delivery qualification: native terminal identity changed")
			return
		}
		recipient := returns.NativeRecipient{Endpoint: endpoint, Terminal: terminal}
		if err = recipient.Qualify(record); err != nil {
			blocked = append(blocked, label+" automatic delivery qualification: "+err.Error())
		}
	}
	owner, err := s.Owner()
	if err != nil {
		blocked = append(blocked, "coordinator automatic delivery qualification: "+err.Error())
	} else {
		check("coordinator", owner, false)
	}
	for _, task := range tasks {
		if effectField(task, "status") == "archived" {
			continue
		}
		id := effectField(task, "id")
		endpoint := asObject(func() any { v, _ := task.Get("worker_endpoint"); return v }())
		if endpoint != nil {
			registration, err := s.Registration(store.EndpointFromContext(endpoint))
			if err != nil {
				blocked = append(blocked, fmt.Sprintf("task %s worker automatic delivery qualification: %v", id, err))
			} else if registration == nil {
				check("task "+id+" worker", endpoint, true)
			} else {
				check("task "+id+" worker", registration, true)
			}
		}
		reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }())
		if reviewer != nil && effectField(reviewer, "address") != "" {
			check("task "+id+" reviewer", reviewer, true)
		}
	}

	loaded, err := settings.LoadSettings(s)
	if err != nil {
		blocked = append(blocked, "automatic delivery settings: "+err.Error())
		return blocked
	}
	for _, selected := range []struct {
		name string
		spec *ordjson.Object
	}{{"worker", loaded.Worker}, {"reviewer", loaded.Reviewer}} {
		spec := selected.spec
		if spec == nil {
			continue
		}
		if preset := effectField(spec, "preset"); preset != "" {
			spec = loaded.Presets[preset]
		}
		harness := effectField(spec, "harness")
		if !launch.NativeHarnessSupported(harness) {
			blocked = append(blocked, fmt.Sprintf("selected %s default automatic delivery qualification: harness %s has no qualified native status/input contract", selected.name, harness))
			continue
		}
		var template returns.NativeTemplate
		if owner != nil {
			raw, _ := owner.Get("native_template")
			data, err := ordjson.MarshalCompact(raw)
			if err == nil && json.Unmarshal(data, &template) == nil && template.Options.Cwd != "" {
				var argv []string
				raw, _ := spec.Get("args")
				args, _ := raw.([]any)
				for _, arg := range args {
					if text, ok := arg.(string); ok {
						argv = append(argv, text)
					}
				}
				template.Options.InputEnv = template.Spec.InputEnv
				if err := launch.ValidateHCInputConfig(harness, argv, template.Options); err != nil {
					blocked = append(blocked, fmt.Sprintf("selected %s default automatic delivery qualification: %v", selected.name, err))
				}
			}
		}
	}
	return blocked
}
