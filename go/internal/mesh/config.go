package mesh

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/store"
)

type Config struct {
	HerdrPath string
	StateHome string
	Session   string
	Pane      string
}

func LoadConfig() (Config, error) {
	// One session-identification path for the whole distro. The Mesh server and
	// sumctl run in the same pane and must agree on which session that is; when
	// they disagreed, sumctl resolved a session and the server refused to start.
	session, err := store.SessionFromEnv()
	if err != nil {
		return Config{}, err
	}
	pane := os.Getenv("HERDR_PANE_ID")
	if os.Getenv("HERDR_ENV") != "1" || pane == "" {
		return Config{}, fmt.Errorf("run this command inside a Herdr pane (HERDR_ENV=1 and HERDR_PANE_ID are required)")
	}
	herdr := os.Getenv("SUM_HERDR_BIN")
	if herdr == "" {
		herdr = "herdr"
	}
	home := os.Getenv("SUM_HOME")
	if home == "" {
		root := os.Getenv("SUM_INSTALL_ROOT")
		if root == "" {
			root, err = os.Getwd()
			if err != nil {
				return Config{}, fmt.Errorf("identify working directory: %w", err)
			}
		}
		home = filepath.Join(root, ".sum")
	}
	return Config{HerdrPath: herdr, StateHome: home, Session: session, Pane: pane}, nil
}
