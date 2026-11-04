package hcboot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func cachePath(s *store.Store) string { return filepath.Join(s.Root, "runtime-route.json") }

func SelectedRoute(s *store.Store) (Route, error) {
	route, err := DefaultRoute()
	if err != nil {
		return Route{}, err
	}
	data, err := os.ReadFile(cachePath(s))
	if os.IsNotExist(err) {
		return route, nil
	}
	if err != nil {
		return Route{}, err
	}
	var cached Route
	if err = json.Unmarshal(data, &cached); err != nil {
		return Route{}, fmt.Errorf("runtime-route.json: %w", err)
	}
	if cached.same(route) {
		route.CoreID = cached.CoreID
	}
	return route, nil
}

func PrepareWorkspaceRoute(s *store.Store) (Route, error) {
	if err := s.CheckWritable(); err != nil {
		return Route{}, err
	}
	route, err := SelectedRoute(s)
	if err != nil {
		return Route{}, err
	}
	if err := os.MkdirAll(route.ConfigHome, 0700); err != nil {
		return Route{}, err
	}
	return route, nil
}

func EnsureWorkspaceRoute(s *store.Store) (Route, error) {
	route, err := PrepareWorkspaceRoute(s)
	if err != nil {
		return Route{}, err
	}
	listening, hello, err := route.compatibility()
	if err == nil && !listening {
		err = errors.New("no core is listening")
	}
	if err != nil {
		return Route{}, fmt.Errorf("default HC core is unavailable at %s: %w; open Herdr or run hc server start in a desktop terminal, then launch again", route.Socket, err)
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
	current, err := SelectedRoute(s)
	if err != nil {
		return Route{}, err
	}
	if !current.same(route) || current.CoreID != "" && current.CoreID != route.CoreID {
		return Route{}, fmt.Errorf("default HC route changed while ensuring it; inspect runtime-route.json before retrying")
	}
	value, err := ordjson.FromValue(route)
	if err != nil {
		return Route{}, err
	}
	if err := ordjson.WriteFile(cachePath(s), value); err != nil {
		return Route{}, err
	}
	return route, nil
}
