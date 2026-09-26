package ordjson

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FromValue re-encodes any JSON-marshalable value (typically a struct) as an ordered Object.
func FromValue(v any) (*Object, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	decoded, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	obj, ok := decoded.(*Object)
	if !ok {
		return nil, fmt.Errorf("%T did not encode as a JSON object", v)
	}
	return obj, nil
}

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
