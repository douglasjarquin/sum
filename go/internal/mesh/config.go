package mesh

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type Config struct {
	HerdrPath string
	StateHome string
	Session   string
	Pane      string
}

func LoadConfig() (Config, error) {
	session := os.Getenv("SUM_SESSION")
	if session == "" {
		session = os.Getenv("HERDR_SESSION")
	}
	if session == "" {
		return Config{}, fmt.Errorf("cannot identify the Herdr session; set SUM_SESSION explicitly")
	}
	if !sessionName.MatchString(session) {
		return Config{}, fmt.Errorf("invalid Herdr session name")
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
			var err error
			root, err = os.Getwd()
			if err != nil {
				return Config{}, fmt.Errorf("identify working directory: %w", err)
			}
		}
		home = filepath.Join(root, ".sum")
	}
	return Config{HerdrPath: herdr, StateHome: home, Session: session, Pane: pane}, nil
}
