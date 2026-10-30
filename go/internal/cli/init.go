package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/lifecycle"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
	"github.com/spf13/cobra"
)

var validInitRoles = map[string]bool{"coordinator": true, "worker": true, "developer": true, "reviewer": true}

func (o *rootOptions) addInitCommand(root *cobra.Command) {
	o.addCoordinatorCommand(root)
	var role, task, coreConfigHome, harness string
	var argv []string
	var reclaim bool
	cmd := &cobra.Command{
		Use:  "init",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if role != "" && !validInitRoles[role] {
				return fmt.Errorf("invalid init arguments")
			}
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			marker, markerErr := roleinit.DevelopmentMarker(o.runtimeRoot)
			if markerErr != nil {
				return markerErr
			}
			developerOnly := role == "developer" || (role == "" && task == "" && (marker != nil || (os.Getenv("SUM_ROLE") != "worker" && os.Getenv("SUM_ROLE") != "reviewer")))
			if !st.Designated() && developerOnly {
				view, initErr := roleinit.Init(o.runtimeRoot, st, nil, role, task)
				if initErr != nil {
					return initErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			ctx, ctxErr := store.Context(o.runtimeRoot)
			if ctxErr != nil && st.Designated() && role == "coordinator" && os.Getenv("SUM_CORE_ID") == "" {
				session, sessionErr := store.SessionFromEnv()
				if sessionErr != nil {
					return sessionErr
				}
				bootstrapCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				ctx, ctxErr = roleinit.BootstrapContext(bootstrapCtx, os.Getenv("HC_SOCKET"), session, os.Getenv("HC_TERMINAL_ID"), st, o.runtimeRoot)
				cancel()
			}
			if ctxErr != nil && (st.Designated() || role == "worker" || task != "") {
				return fmt.Errorf("%s", capitalizeHerdrContext(ctxErr.Error()))
			}
			if !st.Designated() {
				view, initErr := roleinit.Init(o.runtimeRoot, st, ctx, role, task)
				if initErr != nil {
					return initErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			view, initErr := roleinit.InitDesignated(roleinit.DesignatedOpts{
				RuntimeRoot:    o.runtimeRoot,
				SumctlPath:     o.sumctlPath(),
				Store:          st,
				Ctx:            ctx,
				Role:           role,
				Task:           task,
				Reclaim:        reclaim,
				CoreConfigHome: coreConfigHome, Harness: harness, Argv: argv,
			})
			if initErr != nil {
				return initErr
			}
			actualRole, _ := view.Get("role")
			endpointValue, _ := view.Get("endpoint")
			caller, _ := endpointValue.(*ordjson.Object)
			if actualRole == "coordinator" {
				activationCtx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				view.Set("hook_activation", hookstatus.AutoEnableAfterInit(activationCtx, st, caller, o.runtimeRoot, o.sumctlPath()))
				cancel()
				tasks, err := st.AllTasks()
				if err != nil {
					return err
				}
				view.Set("contract", versions.ContractState(st))
				view.Set("maintenance", lifecycle.Pending(st, o.sumctlPath(), tasks))
				pendingCleanup := make([]any, 0)
				for _, task := range tasks {
					if pending := cleanup.Pending(task); pending != nil {
						row := ordjson.NewObject()
						id, _ := task.Get("id")
						row.Set("task", id)
						for _, key := range pending.Keys() {
							value, _ := pending.Get(key)
							row.Set(key, value)
						}
						pendingCleanup = append(pendingCleanup, row)
					}
				}
				view.Set("cleanup_pending", pendingCleanup)
				hook, err := hookstatus.SummaryOf(st, tasks)
				if err != nil {
					return err
				}
				view.Set("hook", hook)
				view.Set("metadata", metadata.Summary(st))
			}
			if actualRole == "coordinator" || actualRole == "worker" {
				endpoint := store.EndpointFromContext(caller)
				roleName, _ := actualRole.(string)
				pending, pumpErr := returns.Pump(st, returns.PumpOpts{Ctx: caller, RuntimeRoot: o.runtimeRoot, SumctlPath: o.sumctlPath(), CoreID: endpoint.CoreID, Session: endpoint.Session, Inline: true, CallerVerifiedRole: roleName, CallerVerifiedTask: endpoint.Task, Parent: cmd.Context()})
				if pumpErr != nil {
					view.Set("returns_error", pumpErr.Error())
				} else {
					view.Set("returns", pending)
				}
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "")
	cmd.Flags().StringVar(&task, "task", "", "")
	cmd.Flags().BoolVar(&reclaim, "reclaim", false, "")
	cmd.Flags().StringVar(&coreConfigHome, "hc-config-home", "", "")
	cmd.Flags().StringVar(&harness, "harness", "codex", "")
	cmd.Flags().StringArrayVar(&argv, "arg", nil, "")
	root.AddCommand(cmd)
}
