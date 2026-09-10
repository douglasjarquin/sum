package ordjson

import (
	"fmt"
	"os"
	"path/filepath"
)

func ReadFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Cannot read %s: %w", path, err)
	}
	value, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("Cannot read %s: %w", path, err)
	}
	return value, nil
}

func WriteFile(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	encoded, err := MarshalIndent(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".write-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
