package hcboot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/pelletier/go-toml/v2"
)

const DefaultSession = "default"

type Route struct {
	Socket     string `json:"socket"`
	Session    string `json:"session"`
	ConfigHome string `json:"config_home"`
	StateHome  string `json:"state_home"`
	RuntimeDir string `json:"runtime_dir,omitempty"`
	CoreID     string `json:"core_id,omitempty"`
}

func DefaultRoute() (Route, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Route{}, err
	}
	xdg := func(key, fallback string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return fallback
	}
	config := xdg("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	state := xdg("XDG_STATE_HOME", filepath.Join(home, ".local/state"))
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		candidate := "/run/user/" + strconv.Itoa(os.Getuid())
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			runtimeDir = candidate
		}
	}
	socket, err := configuredSocket(config)
	if err != nil {
		return Route{}, err
	}
	if socket == "" {
		if runtimeDir != "" {
			socket = filepath.Join(runtimeDir, "herdr-core", DefaultSession+".sock")
		} else {
			socket = filepath.Join(state, "herdr-core", DefaultSession, DefaultSession+".sock")
		}
	}
	route := Route{Socket: socket, Session: DefaultSession, ConfigHome: config, StateHome: state, RuntimeDir: runtimeDir}
	return route, route.Validate()
}

func configuredSocket(configHome string) (string, error) {
	var settings struct {
		Socket string `toml:"socket"`
	}
	data, err := os.ReadFile(filepath.Join(configHome, "herdr-core/config.toml"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err = toml.Unmarshal(data, &settings); err != nil {
		return "", fmt.Errorf("herdr-core/config.toml: %w", err)
	}
	return settings.Socket, nil
}

func (r Route) same(other Route) bool {
	return r.Socket == other.Socket && r.Session == other.Session && r.ConfigHome == other.ConfigHome && r.StateHome == other.StateHome && r.RuntimeDir == other.RuntimeDir
}

func (r Route) compatibility() (listening bool, hello hcclient.HelloResult, err error) {
	if _, err := os.Stat(r.Socket); os.IsNotExist(err) {
		return false, hello, nil
	} else if err != nil {
		return false, hello, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err = hcclient.Inspect(ctx, r.Socket)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
			return false, hello, nil
		}
		return false, hello, err
	}
	if !contract.HCCompatible(hello.Version, int(hello.Generation)) {
		return true, hello, fmt.Errorf("default HC core at %s runs herdr-core %s; this release requires at least %s and does not restart or replace the user's core", r.Socket, hello.Version, contract.HCVersion)
	}
	if r.CoreID != "" && hello.Core.ID != r.CoreID {
		return true, hello, fmt.Errorf("selected core changed: expected %s observed %s", r.CoreID, hello.Core.ID)
	}
	return true, hello, nil
}

func (r Route) Validate() error {
	if r.Session == "" || filepath.Base(r.Session) != r.Session || r.Session == "." || r.Session == ".." {
		return fmt.Errorf("invalid HC session")
	}
	if !filepath.IsAbs(r.Socket) || !filepath.IsAbs(r.ConfigHome) || !filepath.IsAbs(r.StateHome) || (r.RuntimeDir != "" && !filepath.IsAbs(r.RuntimeDir)) {
		return fmt.Errorf("HC route requires absolute socket and XDG paths")
	}
	return nil
}
