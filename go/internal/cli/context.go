package cli

import (
	"github.com/douglasjarquin/sum/go/internal/contextview"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

var validContextRoles = map[string]bool{"worker": true, "reviewer": true, "coordinator": true}

func (o *rootOptions) addContextCommand(root *cobra.Command) {
	var (
		sections  []string
		roles     []string
		sinces    []string
		revisions []string
		kinds     []string
		after     int
		limit     int
		maxChars  int
	)
	cmd := &cobra.Command{
		Use:  "context TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := contextOptions(sections, roles, sinces, revisions, kinds, after, limit, maxChars)
			if err != nil {
				return err
			}
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, viewErr := contextview.View(st, args[0], o.sumctlPath(), opts)
			if viewErr != nil {
				return viewErr
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringArrayVar(&sections, "section", nil, "")
	cmd.Flags().StringArrayVar(&roles, "role", nil, "")
	cmd.Flags().StringArrayVar(&sinces, "since", nil, "")
	cmd.Flags().StringArrayVar(&revisions, "revision", nil, "")
	cmd.Flags().StringArrayVar(&kinds, "kind", nil, "")
	cmd.Flags().IntVar(&after, "after", contextview.DefaultAfter, "")
	cmd.Flags().IntVar(&limit, "limit", contextview.DefaultLimit, "")
	cmd.Flags().IntVar(&maxChars, "max-chars", contextview.DefaultMaxChars, "")
	root.AddCommand(cmd)
}

func contextOptions(sections, roles, sinces, revisions, kinds []string, after, limit, maxChars int) (contextview.Options, error) {
	if len(roles) > 1 {
		return contextview.Options{}, usageError("context", append([]string{"--role"}, roles...))
	}
	if len(sinces) > 1 {
		return contextview.Options{}, usageError("context", append([]string{"--since"}, sinces...))
	}
	if len(revisions) > 1 {
		return contextview.Options{}, usageError("context", append([]string{"--revision"}, revisions...))
	}
	role := ""
	if len(roles) == 1 {
		role = roles[0]
	}
	if role != "" && !validContextRoles[role] {
		return contextview.Options{}, usageError("context", []string{"--role", role})
	}
	since := ""
	if len(sinces) == 1 {
		since = sinces[0]
	}
	revision := ""
	if len(revisions) == 1 {
		revision = revisions[0]
	}
	validSections := map[string]bool{}
	for _, sec := range contextview.ContextSections {
		validSections[sec] = true
	}
	seen := map[string]bool{}
	var cleaned []string
	for _, sec := range sections {
		if !validSections[sec] {
			return contextview.Options{}, usageError("context", []string{"--section", sec})
		}
		if seen[sec] {
			continue
		}
		seen[sec] = true
		cleaned = append(cleaned, sec)
	}
	var cleanedKinds []string
	for _, kind := range kinds {
		if kind != "" {
			cleanedKinds = append(cleanedKinds, kind)
		}
	}
	return contextview.Options{
		Sections: cleaned,
		Role:     role,
		Since:    since,
		Revision: revision,
		Kinds:    cleanedKinds,
		After:    after,
		Limit:    limit,
		MaxChars: maxChars,
	}, nil
}
