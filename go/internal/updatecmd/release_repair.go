package updatecmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func RepairReleaseTemplateContract(s *store.Store, sha string) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	for _, path := range []string{filepath.Join(root, ".local"), filepath.Join(root, ".local", "update.lock")} {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("release repair activation paths must not be symlinks: %s", path)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return nil, statErr
		}
	}
	unlock, err := activationLock(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	unlockState, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlockState()
	state, err := readActivationState(s, root)
	if err != nil {
		return nil, err
	}
	if state != nil {
		if pending, _ := state.Get("pending"); pending != nil {
			return nil, fmt.Errorf("an activation is pending; recover its exact generation before repairing release metadata")
		}
	}
	return release.RepairTemplateContract(root, sha)
}
