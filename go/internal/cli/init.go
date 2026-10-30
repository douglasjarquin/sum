package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
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
