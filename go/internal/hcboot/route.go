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

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/pelletier/go-toml/v2"
)

type Route struct {
	Socket     string `json:"socket"`
	Session    string `json:"session"`
	ConfigHome string `json:"config_home"`
	StateHome  string `json:"state_home"`
	RuntimeDir string `json:"runtime_dir,omitempty"`
	CoreID     string `json:"core_id,omitempty"`
}

func Select(socket, session, config string) (Route, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Route{}, err
	}
	fallback := func(key, other string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return other
	}
	if session == "" {
		session = fallback("HC_SESSION", fallback("SUM_SESSION", "default"))
	}
	if filepath.Base(session) != session || session == "." || session == ".." {
		return Route{}, fmt.Errorf("invalid HC session")
	}
	if config == "" {
		config = fallback("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}
	state := fallback("XDG_STATE_HOME", filepath.Join(home, ".local/state"))
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		candidate := "/run/user/" + strconv.Itoa(os.Getuid())
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			runtimeDir = candidate
		}
	}
	if socket == "" {
		socket = os.Getenv("HC_SOCKET")
	}
	if socket == "" {
		var settings struct {
			Socket string `toml:"socket"`
		}
		data, err := os.ReadFile(filepath.Join(config, "herdr-core/config.toml"))
		if err == nil {
			if err = toml.Unmarshal(data, &settings); err != nil {
				return Route{}, err
			}
			socket = settings.Socket
		} else if !os.IsNotExist(err) {
			return Route{}, err
		}
	}
	if socket == "" {
		if runtimeDir != "" {
			socket = filepath.Join(runtimeDir, "herdr-core", session+".sock")
		} else {
			socket = filepath.Join(state, "herdr-core", session, session+".sock")
		}
	}
	if !filepath.IsAbs(socket) || !filepath.IsAbs(config) || !filepath.IsAbs(state) || (runtimeDir != "" && !filepath.IsAbs(runtimeDir)) {
		return Route{}, fmt.Errorf("HC route requires absolute socket and XDG paths")
	}
	return Route{Socket: socket, Session: session, ConfigHome: config, StateHome: state, RuntimeDir: runtimeDir}, nil
}
func (r Route) environment() []string {
	env := childEnv()
	values := map[string]string{"HC_SOCKET": r.Socket, "HC_SESSION": r.Session, "XDG_CONFIG_HOME": r.ConfigHome, "XDG_STATE_HOME": r.StateHome, "XDG_RUNTIME_DIR": r.RuntimeDir}
	var result []string
	for _, entry := range env {
		skip := false
		for key := range values {
			if len(entry) > len(key) && entry[:len(key)+1] == key+"=" {
				skip = true
				break
			}
		}
		if !skip {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		if value != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}
func (r Route) compatibility() error {
	if r.Socket == "" {
		return nil
	}
	if _, err := os.Stat(r.Socket); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err := hcclient.Inspect(ctx, r.Socket)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if r.CoreID != "" && hello.Core.ID != r.CoreID {
		return fmt.Errorf("selected core changed: expected %s observed %s", r.CoreID, hello.Core.ID)
	}
	return nil
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
