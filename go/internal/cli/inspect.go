package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addInspectCommand(root *cobra.Command) {
	var role, attempt string
	cmd := &cobra.Command{Use: "inspect [TASK]", Short: "Read an exact saved launch and native output, including unready agents", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Open(o.home)
		if err != nil {
			return err
		}
		var record *ordjson.Object
		var ep store.Endpoint
		var launchRecord *ordjson.Object
		if len(args) == 0 {
			if role != "coordinator" {
				return fmt.Errorf("task required for worker/reviewer inspection")
			}
			value, err := ordjson.ReadFile(filepath.Join(s.Home, "coordinator-launch.json"))
			if err != nil {
				return err
			}
			record, _ = value.(*ordjson.Object)
			launchRecord = record
			ep = store.EndpointFromContext(inspectObject(record, "endpoint"))
		} else {
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
		}
		if attempt != "" && attempt != ep.Attempt {
			return fmt.Errorf("saved attempt differs; inspect without --attempt to identify it")
		}
		out := ordjson.NewObject()
		out.Set("endpoint", ep.Object())
		out.Set("launch", launchRecord)
		out.Set("read_only", true)
		if ep.Address == "" || ep.CoreID == "" {
			out.Set("note", "No returned endpoint; creation remains ambiguous. Saved intent is preserved; no launch or input was replayed.")
			return emitOrdjson(cmd.OutOrStdout(), out)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
		terminal, err := client.Get(ctx, ep.Address)
		if err != nil {
			out.Set("observation_error", err.Error())
			return emitOrdjson(cmd.OutOrStdout(), out)
		}
		if terminal.CreatedMS != ep.CreatedMS {
			return fmt.Errorf("saved terminal creation changed; output was not read")
		}
		out.Set("terminal", func() any { v, _ := ordjson.FromValue(terminal); return v }())
		output, err := client.Read(ctx, ep.Address, 100)
		if err != nil {
			out.Set("output_error", err.Error())
		} else {
			out.Set("output", output.Text)
		}
		attach, _ := ordjson.FromValue(map[string]any{"argv": []string{filepath.Join(o.runtimeRoot, ".local/bin/hc"), "attach", ep.Address}, "env": map[string]string{"HC_SOCKET": ep.Socket, "HC_SESSION": ep.Session}})
		out.Set("attach", attach)
		return emitOrdjson(cmd.OutOrStdout(), out)
	}}
	cmd.Flags().StringVar(&role, "role", "coordinator", "coordinator, worker or reviewer")
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
