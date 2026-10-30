package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"os"
	"path/filepath"
	"strconv"
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
			explicitAuthority := role != "" && role != "developer" || task != "" || reclaim
			developmentOnly := marker != nil && !st.Designated()
			inheritedTaskIdentity := !developmentOnly && (os.Getenv("SUM_TASK") != "" || ((os.Getenv("SUM_ROLE") == "worker" || os.Getenv("SUM_ROLE") == "reviewer") && os.Getenv("SUM_ATTEMPT") != ""))
			emitDeveloper := func() error {
				var view *ordjson.Object
				var initErr error
				if st.Designated() {
					view, initErr = roleinit.InitDesignated(roleinit.DesignatedOpts{RuntimeRoot: o.runtimeRoot, Store: st, Role: "developer"})
				} else {
					view, initErr = roleinit.Init(o.runtimeRoot, st, nil, "developer", "")
				}
				if initErr != nil {
					return initErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			if role == "developer" && (task != "" || reclaim || inheritedTaskIdentity) {
				return fmt.Errorf("Developer initialization cannot adopt a task identity or reclaim coordination; use the required verified role.")
			}
			if !explicitAuthority && !inheritedTaskIdentity {
				if role == "developer" || developmentOnly {
					return emitDeveloper()
				}
				bound, bindingErr := recordedCoordinatorContext(st)
				if bindingErr != nil {
					return bindingErr
				}
				if !bound {
					return emitDeveloper()
				}
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
			if ctxErr != nil {
				if !explicitAuthority && !inheritedTaskIdentity {
					return emitDeveloper()
				}
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

func recordedCoordinatorContext(s *store.Store) (bool, error) {
	if os.Getenv("SUM_ROLE") != "coordinator" || os.Getenv("SUM_TASK") != "" {
		return false, nil
	}
	session, err := store.SessionFromEnv()
	if err != nil {
		return false, nil
	}
	for _, key := range []string{"HC_SOCKET", "HC_TERMINAL_ID", "SUM_CORE_ID", "SUM_ATTEMPT", "SUM_BINDING_GENERATION"} {
		if os.Getenv(key) == "" {
			return false, nil
		}
	}
	owner, err := s.Owner()
	if err != nil {
		return false, err
	}
	var saved store.Endpoint
	pending := owner == nil
	if !pending {
		saved = store.EndpointFromContext(owner)
		if saved.Validate() != nil || saved.Role != "coordinator" {
			return false, nil
		}
		receipt, receiptErr := app.NativeReceipt(owner)
		if receiptErr != nil || receipt.Kind != "coordinator-init" || receipt.Attempt != saved.Attempt || !receipt.Endpoint.SameAuthority(saved.Native()) {
			return false, nil
		}
	} else {
		value, readErr := ordjson.ReadFile(filepath.Join(s.Home, "coordinator-launch.json"))
		if errors.Is(readErr, os.ErrNotExist) {
			return false, nil
		}
		if readErr != nil {
			return false, readErr
		}
		claim, ok := value.(*ordjson.Object)
		if !ok {
			return false, nil
		}
		effect, _ := claim.Get("effect")
		if effect != "claimed" && effect != "created" && effect != "uncertain" {
			return false, nil
		}
		endpointValue, _ := claim.Get("endpoint")
		endpointObject, _ := endpointValue.(*ordjson.Object)
		saved = store.EndpointFromContext(endpointObject)
		if saved.Schema != 2 || saved.Backend != "hc" || saved.Role != "coordinator" || saved.Attempt == "" || saved.BindingGeneration == 0 {
			return false, nil
		}
		intentValue, _ := claim.Get("intent")
		intent, _ := intentValue.(*ordjson.Object)
		if intent == nil {
			return false, nil
		}
		envValue, _ := intent.Get("env")
		launchEnv, _ := envValue.(*ordjson.Object)
		if launchEnv == nil {
			return false, nil
		}
		for key, want := range map[string]string{"SUM_HOME": s.Root, "SUM_CORE_ID": saved.CoreID, "HC_SOCKET": saved.Socket, "SUM_SESSION": saved.Session, "SUM_ROLE": "coordinator", "SUM_ATTEMPT": saved.Attempt, "SUM_BINDING_GENERATION": strconv.FormatUint(saved.BindingGeneration, 10)} {
			actual, _ := launchEnv.Get(key)
			if want == "" || actual != want {
				return false, nil
			}
		}
	}

	host, err := s.Machine()
	if err != nil {
		return false, err
	}
	if !host.Is(saved.Machine) {
		return false, nil
	}
	return saved.Session == session && saved.Socket == os.Getenv("HC_SOCKET") && (saved.Address == os.Getenv("HC_TERMINAL_ID") || (pending && saved.Address == "")) && saved.CoreID == os.Getenv("SUM_CORE_ID") && saved.Attempt == os.Getenv("SUM_ATTEMPT") && strconv.FormatUint(saved.BindingGeneration, 10) == os.Getenv("SUM_BINDING_GENERATION"), nil
}
