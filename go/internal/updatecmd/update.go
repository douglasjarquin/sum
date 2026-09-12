package updatecmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Status(s *store.Store) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	current := filepath.Join(root, ".local", "current")
	target, _ := os.Readlink(current)
	result := ordjson.NewObject()
	result.Set("installation", root)
	result.Set("current", current)
	result.Set("target", target)
	listed, listErr := release.List(s)
	if listErr == nil {
		result.Set("releases", listed)
	}
	result.Set("note", "Default and active runtime. Staging never activates; apply switches the default in one rename.")
	return result, nil
}

func Check(s *store.Store, runtimeRoot, ref string, noFetch bool) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	if !noFetch {
		_, _ = proc.Run([]string{"git", "-C", root, "fetch", "origin"}, "", 120*time.Second, false, nil)
	}
	if ref == "" {
		ref = "origin/HEAD"
	}
	shaOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", "--verify", ref + "^{commit}", "--"}, "", 30*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("installation", root)
	result.Set("ref", ref)
	result.Set("sha", strings.TrimSpace(shaOut.Stdout))
	result.Set("note", "Check only; nothing was staged or activated.")
	return result, nil
}

func Stage(s *store.Store, ctx *ordjson.Object, ref string, noFetch bool) (*ordjson.Object, error) {
	checked, err := Check(s, "", ref, noFetch)
	if err != nil {
		return nil, err
	}
	sha := ""
	if v, ok := checked.Get("sha"); ok {
		sha, _ = v.(string)
	}
	staged, err := release.Stage(s, sha)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("check", checked)
	result.Set("release", staged)
	return result, nil
}

func Apply(s *store.Store, ctx *ordjson.Object, ref string, noFetch bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("update apply is a coordinator-only activation of a staged release; stage the release first with `release stage` or `update stage`, then apply from the installation helper.")
}

func Rollback(s *store.Store, ctx *ordjson.Object, to string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("update rollback is coordinator-only and requires a previously activated runtime.")
}

func Recover(s *store.Store, ctx *ordjson.Object, generation string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	state := filepath.Join(root, ".local", "activation.json")
	if _, err := os.Stat(state); err != nil {
		return nil, fmt.Errorf("No interrupted activation is recorded for generation %s.", generation)
	}
	return nil, fmt.Errorf("update recover requires a matching generation in activation.json.")
}
