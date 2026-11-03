package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

var validInitRoles = map[string]bool{"coordinator": true, "worker": true, "developer": true, "reviewer": true}

func (o *rootOptions) addInitCommand(root *cobra.Command) {
	o.addCoordinatorCommand(root)
	var role, task string
	var transfer, retireNative bool
	cmd := &cobra.Command{
		Use:  "init",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if role != "" && !validInitRoles[role] {
				return fmt.Errorf("invalid init arguments")
			}
			blocker := func(kind, detail, next string) error {
				view := ordjson.NewObject()
				view.Set("role", nil)
				view.Set("blocker", kind)
				view.Set("detail", detail)
				view.Set("next", next)
				if err := emitOrdjson(cmd.OutOrStdout(), view); err != nil {
					return err
				}
				return &ExitError{Code: 1}
			}
			st, err := store.Open(o.home)
			if err != nil {
				if strings.Contains(err.Error(), "unsupported state schema") {
					return blocker("legacy-state", "Existing state and development checkouts are preserved at "+o.home+" and "+filepath.Join(o.installRoot, ".sum", "dev")+"/*; live legacy conversion is unavailable.", "Use an independent fresh workspace beside "+o.installRoot+" and run mise run setup there.")
				}
				return err
			}
			marker, err := roleinit.DevelopmentMarker(o.runtimeRoot)
			if err != nil {
				return err
			}
			inheritedTask := os.Getenv("SUM_TASK") != "" || os.Getenv("SUM_ROLE") == "worker" || os.Getenv("SUM_ROLE") == "reviewer"
			taskContext := role == "worker" || role == "reviewer" || task != "" || inheritedTask
			if taskContext {
				if role == "developer" || role == "coordinator" || transfer || retireNative {
					return blocker("task-context", "An explicit task identity cannot claim another role.", "Inspect the recorded task attempt and initialize its required worker or reviewer role.")
				}
				ctx, err := store.Context(o.runtimeRoot)
				if err != nil {
					return blocker("task-context", "The task identity could not be validated.", "Inspect the recorded task attempt; do not initialize coordination from this context.")
				}
				if role == "" {
					roleValue, _ := ctx.Get("role")
					role, _ = roleValue.(string)
				}
				if task == "" {
					taskValue, _ := ctx.Get("task")
					task, _ = taskValue.(string)
				}
				var view *ordjson.Object
				if st.Designated() {
					view, err = roleinit.InitDesignated(roleinit.DesignatedOpts{RuntimeRoot: o.runtimeRoot, SumctlPath: o.sumctlPath(), Store: st, Ctx: ctx, Role: role, Task: task})
				} else {
					view, err = roleinit.Init(o.runtimeRoot, st, ctx, role, task)
				}
				if err != nil {
					return blocker("task-context", "The recorded task attempt refused initialization: "+err.Error(), "Inspect the recorded task attempt before retrying.")
				}
				actualRole, _ := view.Get("role")
				if actualRole == "worker" && st.Designated() {
					endpointValue, _ := view.Get("endpoint")
					caller, _ := endpointValue.(*ordjson.Object)
					endpoint := store.EndpointFromContext(caller)
					pending, pumpErr := returns.Pump(st, returns.PumpOpts{Ctx: caller, RuntimeRoot: o.runtimeRoot, SumctlPath: o.sumctlPath(), CoreID: endpoint.CoreID, Session: endpoint.Session, Inline: true, CallerVerifiedRole: "worker", CallerVerifiedTask: endpoint.Task, Parent: cmd.Context()})
					if pumpErr != nil {
						view.Set("returns_error", pumpErr.Error())
					} else {
						view.Set("returns", pending)
					}
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			if role == "developer" || marker != nil {
				if transfer || retireNative || role == "coordinator" {
					return blocker("development-checkout", "A marked development checkout cannot own coordination.", "Use the installation workspace for coordination; keep developing here.")
				}
				var view *ordjson.Object
				if st.Designated() {
					view, err = roleinit.InitDesignated(roleinit.DesignatedOpts{RuntimeRoot: o.runtimeRoot, Store: st, Role: "developer"})
				} else {
					view, err = roleinit.Init(o.runtimeRoot, st, nil, "developer", "")
				}
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			if hint, err := roleinit.TaskCheckout(o.runtimeRoot, st); err != nil {
				return err
			} else if hint != "" {
				return blocker("task-context", "This checkout belongs to recorded task "+hint+" but no valid task identity was supplied.", "Inspect "+hint+" and use its recorded worker or reviewer launch.")
			}
			owner, err := st.Owner()
			if err != nil {
				return err
			}
			session, err := st.CoordinatorSession()
			if err != nil {
				return err
			}
			token := o.claim
			if owner != nil && session == nil {
				if !retireNative {
					return blocker("retire-native", "This workspace still records a native coordinator.", "After the user confirms retirement, run init --retire-native.")
				}
				if _, err := procedure.Describe(o.runtimeRoot, procedure.Coordinator, "no coordinator role was granted and nothing was retired"); err != nil {
					return err
				}
				session, err = st.RetireNativeCoordinator(token, "agent conversation", transfer)
				if err != nil {
					return blocker("retire-native", err.Error(), "Inspect the recorded native coordinator and retry retirement only after its blockers are settled.")
				}
				token, transfer = session.Token(), false
			}
			view, err := roleinit.InitCoordinator(o.runtimeRoot, o.sumctlPath(), st, token, "agent conversation", transfer)
			if err != nil {
				if errors.Is(err, store.ErrNativeRetirementRequired) {
					return blocker("retire-native", err.Error(), "After the user confirms retirement, run init --retire-native.")
				}
				if errors.Is(err, store.ErrClaimConflict) || errors.Is(err, store.ErrStaleClaim) {
					if view == nil {
						view = ordjson.NewObject()
					}
					view.Set("role", nil)
					view.Set("blocker", "conflict")
					view.Set("detail", err.Error())
					view.Set("next", "Resume this conversation with init --claim TOKEN; use init --transfer only for a user-requested transfer.")
					if err := emitOrdjson(cmd.OutOrStdout(), view); err != nil {
						return err
					}
					return &ExitError{Code: 1}
				}
				return err
			}
			if retireNative {
				raw, _ := view.Get("claim")
				claim, _ := raw.(string)
				if err := retireNativePreferences(st, claim); err != nil {
					return err
				}
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "")
	cmd.Flags().StringVar(&task, "task", "", "")
	cmd.Flags().BoolVar(&transfer, "transfer", false, "")
	cmd.Flags().BoolVar(&retireNative, "retire-native", false, "")
	root.AddCommand(cmd)
}

// retireNativePreferences removes active observer configuration while retaining its health evidence.
func retireNativePreferences(s *store.Store, token string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err = s.CheckClaim(token); err != nil {
		return err
	}
	path := filepath.Join(s.Home, "settings.json")
	raw, err := ordjson.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		settings, ok := raw.(*ordjson.Object)
		if !ok {
			return fmt.Errorf("settings.json is not an object")
		}
		if _, present := settings.Get("hook"); present {
			settings.Delete("hook")
			if err = ordjson.WriteFile(path, settings); err != nil {
				return err
			}
		}
	}
	health := filepath.Join(s.Home, "hook", "health.json")
	if _, err = os.Stat(health); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	retired := filepath.Join(s.Home, "hook", "health.retired-native.json")
	if _, err = os.Stat(retired); err == nil {
		return fmt.Errorf("retired observer health already exists at %s; preserve both records and inspect before retrying", retired)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(health, retired)
}
