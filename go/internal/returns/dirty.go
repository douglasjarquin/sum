package returns

import (
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
)

func MarkDirty(s *store.Store) error {
	path := filepath.Join(s.Home, "hook", "dirty")
	file, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = file.WriteAt([]byte{1}, 0)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
