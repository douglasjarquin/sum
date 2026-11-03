package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addInspectCommand(root *cobra.Command) {
	var role, attempt string
	cmd := &cobra.Command{Use: "inspect TASK", Short: "Read an exact saved launch and native output, including unready agents", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Open(o.home)
		if err != nil {
			return err
		}
		var record *ordjson.Object
		var ep store.Endpoint
		var launchRecord *ordjson.Object
		task, err := s.ReadTask(args[0])
		if err != nil {
			return err
		}
		switch role {
		case "worker":
			record, err = reservations.Worker(task)
			if err != nil {
				return err
			}
			ep = store.EndpointFromContext(inspectObject(task, "worker_endpoint"))
			launchRecord = inspectObject(record, "native_launch")
		case "reviewer":
			record = inspectObject(task, "reviewer")
			ep = store.EndpointFromContext(record)
			launchRecord = inspectObject(record, "native_launch")
		default:
			return fmt.Errorf("task inspection requires --role worker or reviewer")
		}
		if ep.Attempt == "" {
			ep.Attempt = inspectString(record, "id")
		}
		if ep.Address != "" {
			if ep.Task != args[0] || ep.Role != role || (role == "worker" && ep.Attempt != inspectString(record, "id")) {
				return fmt.Errorf("saved endpoint does not belong to the selected task role and attempt")
			}
		} else {
			ep.Task, ep.Role = args[0], role
			intent := inspectObject(launchRecord, "intent")
			if intent == nil {
				intent = inspectObject(launchRecord, "params")
			}
			env := inspectObject(intent, "env")
			ep.Socket = inspectString(env, "HC_SOCKET")
			ep.CoreID = inspectString(env, "SUM_CORE_ID")
			ep.Session = inspectString(env, "SUM_SESSION")
		}

		if attempt != "" && attempt != ep.Attempt {
			return fmt.Errorf("saved attempt differs; inspect without --attempt to identify it")
		}
		out := ordjson.NewObject()
		out.Set("task", args[0])
		out.Set("attempt", ep.Attempt)
		stage := inspectString(launchRecord, "stage")
		if stage == "" {
			stage = inspectString(record, "state")
		}
		out.Set("stage", stage)
		out.Set("effect", inspectString(launchRecord, "effect"))
		out.Set("route", func() any {
			v, _ := ordjson.FromValue(map[string]string{"socket": ep.Socket, "core_id": ep.CoreID, "session": ep.Session})
			return v
		}())
		out.Set("endpoint", ep.Object())
		out.Set("launch", launchRecord)
		out.Set("read_only", true)
		group := "execution"
		if role == "reviewer" {
			group = "reviewer"
		}
		action := func(verb string) any {
			args := []string{filepath.Join(o.runtimeRoot, "bin/sumctl"), "--home", s.Root, group, verb}
			if ep.Task != "" {
				args = append(args, ep.Task)
			}
			args = append(args, "--attempt", ep.Attempt)
			value := make([]any, len(args))
			for i, arg := range args {
				value[i] = arg
			}
			return value
		}
		if ep.Attempt != "" {
			out.Set("next", action("reconcile"))
			out.Set("reconcile", action("reconcile"))
			out.Set("redeliver_if_proven_unsent", action("redeliver"))
			if role != "worker" {
				out.Set("recover_if_proven_stopped_or_uncreated", action("recover"))
			} else {
				out.Set("park_if_proven_stopped", action("park"))
			}
		}

		if ep.Address == "" || ep.CoreID == "" {
			out.Set("note", "No returned endpoint is recorded. Saved launch intent is preserved; inspection did not launch or replay input.")
			return emitOrdjson(cmd.OutOrStdout(), out)
		}
		if err := ep.Validate(); err != nil {
			return fmt.Errorf("saved endpoint is invalid: %w", err)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
		terminal, err := client.Get(ctx, ep.Address)
		if err != nil {
			out.Set("observation_error", environment.BoundedView(err.Error(), 2000))
			return emitOrdjson(cmd.OutOrStdout(), out)
		}
		if terminal.CreatedMS != ep.CreatedMS {
			return fmt.Errorf("saved terminal creation changed; output was not read")
		}
		out.Set("terminal", func() any { v, _ := ordjson.FromValue(terminal); return v }())
		output, err := client.Read(ctx, ep.Address, 100)
		if err != nil {
			out.Set("output_error", environment.BoundedView(err.Error(), 2000))
		} else {
			view := environment.BoundedView(output.Text, 12000)
			if truncated, _ := view.Get("truncated"); truncated == true {
				view.Set("note", "Native output truncated to 12000 characters.")
			}
			out.Set("output", view)
		}
		attach, _ := ordjson.FromValue(map[string]any{"argv": []string{filepath.Join(o.runtimeRoot, ".local/bin/hc"), "attach", ep.Address}, "env": map[string]string{"HC_SOCKET": ep.Socket, "HC_SESSION": ep.Session}})
		out.Set("attach", attach)
		return emitOrdjson(cmd.OutOrStdout(), out)
	}}
	cmd.Flags().StringVar(&role, "role", "worker", "worker or reviewer")
	cmd.Flags().StringVar(&attempt, "attempt", "", "expected exact attempt; omitted reads the saved current attempt")
	root.AddCommand(cmd)
}
func inspectObject(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	out, _ := v.(*ordjson.Object)
	return out
}

func inspectString(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}
