package cli

import (
	"github.com/douglasjarquin/sum/go/internal/doctor"
	sumruntime "github.com/douglasjarquin/sum/go/internal/runtime"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addDoctorCommand(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:  "doctor",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				view := map[string]any{"ok": false, "state_home": o.home, "state_error": err.Error(), "helper": sumruntime.BuildIdentity(), "next": "sumctl history; preserve this installation and create an independent fresh clone. Live legacy migration is unavailable."}
				if emitErr := emitJSON(cmd.OutOrStdout(), view); emitErr != nil {
					return emitErr
				}
				return &ExitError{Code: 1}
			}
			view := doctor.Doctor(o.runtimeRoot, o.installRoot, st)
			if emitErr := emitOrdjson(cmd.OutOrStdout(), view); emitErr != nil {
				return emitErr
			}
			if okValue, _ := view.Get("ok"); okValue != true {
				return &ExitError{Code: 1}
			}
			return nil
		},
	})
}
