// Package tool holds the few helpers every sum-verify subcommand shares.
package tool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"
)

// Version is the release version, set at build time with -ldflags "-X .../internal/tool.Version=1.2.3".
var Version = "dev"

// UTCNow is the record timestamp format: seconds precision with a Z suffix.
func UTCNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// EncodeJSON renders a record the way every tool prints and stores one: two-space indent, no HTML escaping, trailing newline.
func EncodeJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func WriteJSON(w io.Writer, value any) error {
	data, err := EncodeJSON(value)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
