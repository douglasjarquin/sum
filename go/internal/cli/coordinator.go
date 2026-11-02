package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcboot"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"time"
)

func (o *rootOptions) addCoordinatorCommand(root *cobra.Command) {
	var options roleinit.CoordinatorStartOpts
	group := &cobra.Command{Use: "coordinator"}
	command := &cobra.Command{Use: "start", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := o.openStore("coordinator start")
		if err != nil {
			return err
		}
		if owner, err := s.Owner(); err != nil {
			return err
		} else if owner != nil {
			return fmt.Errorf("coordinator already recorded; inspect its exact endpoint before another start")
		}
		route, err := selectedHCRoute(s, options.Socket, options.Session, options.CoreConfigHome)
		if err != nil {
			return err
		}
		services := hcboot.EnsureRoute(o.runtimeRoot, route)
		if services != nil {
			if ok, _ := services.Get("ok"); ok != true {
				if err := emitOrdjson(cmd.OutOrStdout(), services); err != nil {
					return err
				}
				return &ExitError{Code: 1}
			}
		}
		observe, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
		hello, err := hcclient.Inspect(observe, route.Socket)
		cancel()
		if err != nil {
			return err
		}
		route.CoreID = hello.Core.ID
		unlock, err := s.Lock()
		if err != nil {
			return err
		}
		savedRoute, _ := ordjson.FromValue(route)
		writeErr := ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), savedRoute)
		unlock()
		if writeErr != nil {
			return writeErr
		}
		options.Socket, options.Session, options.CoreConfigHome = route.Socket, route.Session, route.ConfigHome
		options.Launch.PresetSet = cmd.Flags().Changed("preset")
		options.RuntimeRoot = o.runtimeRoot
		options.SumctlPath = o.sumctlPath()
		view, err := roleinit.StartCoordinator(s, options)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}}
	command.Flags().StringVar(&options.DesktopSpace, "space", "", "existing desktop space ID; placement remains separate from authority")
	command.Flags().StringVar(&options.Socket, "socket", "", "")
	command.Flags().StringVar(&options.Session, "session", "", "")
	command.Flags().StringVar(&options.CoreConfigHome, "hc-config-home", "", "")
	command.Flags().StringVar(&options.Launch.Harness, "harness", "codex", "")
	command.Flags().StringVar(&options.Launch.Model, "model", "", "")
	command.Flags().StringVar(&options.Launch.Reasoning, "reasoning", "", "")
	command.Flags().StringVar(&options.Launch.Preset, "preset", "", "")
	command.Flags().StringArrayVar(&options.Launch.Extra, "arg", nil, "")
	command.Flags().BoolVar(&options.Launch.PermissionOverride, launch.PermissionOverrideFlag[2:], false, "")
	group.AddCommand(command)
	var attempt string
	recover := &cobra.Command{Use: "recover", Short: "Archive a proven uncreated coordinator attempt without launching or sending", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := o.openStore("coordinator recover")
		if err != nil {
			return err
		}
		view, err := roleinit.RecoverCoordinator(s, attempt)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}}
	recover.Flags().StringVar(&attempt, "attempt", "", "exact failed launch attempt")
	group.AddCommand(recover)
	var openingAttempt string
	redeliver := &cobra.Command{Use: "redeliver", Short: "Deliver a proven-unsent opening to the original coordinator attempt", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := o.openStore("coordinator redeliver")
		if err != nil {
			return err
		}
		view, err := roleinit.RedeliverCoordinator(s, openingAttempt)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}}
	redeliver.Flags().StringVar(&openingAttempt, "attempt", "", "exact created coordinator attempt")
	group.AddCommand(redeliver)
	var reconcileAttempt string
	reconcile := &cobra.Command{Use: "reconcile", Short: "Corroborate a lost creation response without replaying input", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := o.openStore("coordinator reconcile")
		if err != nil {
			return err
		}
		view, err := roleinit.ReconcileCoordinator(s, reconcileAttempt)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}}
	reconcile.Flags().StringVar(&reconcileAttempt, "attempt", "", "exact coordinator attempt")
	group.AddCommand(reconcile)
	root.AddCommand(group)
}

func selectedHCRoute(s *store.Store, socket, session, config string) (hcboot.Route, error) {
	path := filepath.Join(s.Root, "runtime-route.json")
	if socket == "" && session == "" && config == "" {
		data, err := os.ReadFile(path)
		if err == nil {
			var route hcboot.Route
			if err = json.Unmarshal(data, &route); err != nil {
				return route, err
			}
			if route.Socket == "" || route.Session == "" || route.ConfigHome == "" {
				return route, fmt.Errorf("saved HC route is incomplete")
			}
			return route, nil
		}
		if !os.IsNotExist(err) {
			return hcboot.Route{}, err
		}
	}
	route, err := hcboot.Select(socket, session, config)
	if err != nil {
		return route, err
	}
	return route, nil
}
