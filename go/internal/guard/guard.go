package guard

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var ReadOnlyCommands = map[string]bool{
	"brief-list": true, "context": true, "doctor": true, "env-show": true, "execution-show": true,
	"graph-config": true, "graph-status": true, "help": true, "hook-status": true, "inbox": true,
	"metadata-snippet": true, "metadata-status": true, "preset-list": true, "preset-show": true,
	"project-list": true, "project-show": true, "quota": true, "refresh-status": true,
	"release-contract": true, "release-list": true, "release-show": true, "settings-show": true,
	"show": true, "skills-check": true, "status": true, "update-status": true,
}

func Candidate(installRoot string, s *store.Store, command string) error {
	if isFile(filepath.Join(installRoot, ".sum", "state.json")) && !isFile(filepath.Join(installRoot, ".sum", "dev.json")) {
		return nil
	}
	protected := map[string]bool{}
	hint, err := roleinit.InstallationHint(installRoot)
	if err != nil {
		return err
	}
	if hint != "" {
		protected[resolve(hint)] = true
	}
	marker, err := roleinit.DevelopmentMarker(installRoot)
	if err != nil {
		return err
	}
	if marker != nil {
		if home, ok := marker.Get("installation_home"); ok {
			if homeStr, isString := home.(string); isString && homeStr != "" {
				protected[resolve(homeStr)] = true
			}
		}
	}
	if protected[resolve(s.Home)] && !ReadOnlyCommands[command] {
		installed := filepath.Join(filepath.Dir(firstKey(protected)), "bin", "sumctl")
		return fmt.Errorf("Refusing `%s`: this helper runs from a development or task checkout (%s) but targets the installation's state %s. Candidate code operates only on lab state (--home under a temporary directory). Parent-task callbacks and installation changes use the installed trusted helper %s.", command, installRoot, s.Home, installed)
	}
	return nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func resolve(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return absolute
}

func firstKey(m map[string]bool) string {
	for k := range m {
		return k
	}
	return ""
}
