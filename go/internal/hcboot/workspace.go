package hcboot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func SelectedRoute(s *store.Store, socket, session, config string) (Route, error) {
	path := filepath.Join(s.Root, "runtime-route.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var route Route
		if err := json.Unmarshal(data, &route); err != nil {
			return route, err
		}
		if err := route.Validate(); err != nil {
			return route, err
		}
		if socket != "" && socket != route.Socket || session != "" && session != route.Session || config != "" && config != route.ConfigHome {
			return Route{}, fmt.Errorf("worker runtime route is already selected in %s; inspect it before changing the live scope", path)
		}
		return route, nil
	}
	if !os.IsNotExist(err) {
		return Route{}, err
	}
	instance, err := s.Instance()
	if err != nil {
		return Route{}, err
	}
	if instance == "" {
		return Route{}, fmt.Errorf("workspace is not initialized; run init before selecting workers")
	}
	prefix := instance
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	if session == "" {
		session = "sum-" + prefix
	}
	base := filepath.Join(s.Root, "runtime", "hc")
	runtimeDir := filepath.Join("/tmp", "sum-hc-"+strconv.Itoa(os.Getuid())+"-"+prefix)
	if socket == "" {
		socket = filepath.Join(runtimeDir, "core.sock")
	}
	if config == "" {
		config = filepath.Join(base, "config")
	}
	route := Route{Socket: socket, Session: session, ConfigHome: config, StateHome: filepath.Join(base, "state"), RuntimeDir: runtimeDir}
	return route, route.Validate()
}

func PrepareWorkspaceRoute(s *store.Store) (Route, error) {
	if err := s.CheckWritable(); err != nil {
		return Route{}, err
	}
	route, err := SelectedRoute(s, "", "", "")
	if err != nil {
		return Route{}, err
	}
	for _, directory := range []string{route.ConfigHome, route.StateHome, route.RuntimeDir} {
		if directory == "" {
			continue
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			return Route{}, err
		}
		if directory == route.RuntimeDir {
			info, err := os.Lstat(directory)
			if err != nil {
				return Route{}, err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !info.IsDir() || info.Mode()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
				return Route{}, fmt.Errorf("worker runtime directory is not private to this user: %s", directory)
			}
		}
	}
	return route, nil
}

func EnsureWorkspaceRoute(s *store.Store, runtimeRoot string) (Route, error) {
	route, err := PrepareWorkspaceRoute(s)
	if err != nil {
		return Route{}, err
	}
	if err := route.compatibility(); err != nil {
		return Route{}, err
	}
	if report := EnsureRoute(runtimeRoot, route); report != nil {
		ok, _ := report.Get("ok")
		if ok != true {
			detail, _ := report.Get("error")
			return Route{}, fmt.Errorf("worker runtime could not be ensured: %v", detail)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err := hcclient.Inspect(ctx, route.Socket)
	if err != nil {
		return Route{}, fmt.Errorf("worker runtime is unavailable: %w; run mise run setup if its pinned HC helper is missing", err)
	}
	if route.CoreID != "" && route.CoreID != hello.Core.ID {
		return Route{}, fmt.Errorf("selected core changed: expected %s observed %s", route.CoreID, hello.Core.ID)
	}
	route.CoreID = hello.Core.ID
	unlock, err := s.Lock()
	if err != nil {
		return Route{}, err
	}
	defer unlock()
	current, err := SelectedRoute(s, "", "", "")
	if err != nil {
		return Route{}, err
	}
	if current.Socket != route.Socket || current.Session != route.Session || current.ConfigHome != route.ConfigHome || current.StateHome != route.StateHome || current.RuntimeDir != route.RuntimeDir || current.CoreID != "" && current.CoreID != route.CoreID {
		return Route{}, fmt.Errorf("worker runtime route changed while ensuring it; inspect runtime-route.json before retrying")
	}
	value, err := ordjson.FromValue(route)
	if err != nil {
		return Route{}, err
	}
	if err := ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), value); err != nil {
		return Route{}, err
	}
	return route, nil
}
