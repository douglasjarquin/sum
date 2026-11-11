// Package tool holds the few helpers every sum-verify subcommand shares.
package tool

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

// Version is the release version, set at build time with -ldflags "-X .../internal/tool.Version=1.2.3".
var Version = "dev"

// UTCNow is the record timestamp format: seconds precision with a Z suffix.
func UTCNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// StampLayout is the sortable UTC timestamp that record ids and proposal directories start with.
const StampLayout = "20060102T150405Z"

// Stamp is the form every record id takes: a sortable UTC timestamp and n random bytes in hex.
func Stamp(n int) string {
	random := make([]byte, n)
	_, _ = rand.Read(random) // crypto/rand.Read never fails.
	return time.Now().UTC().Format(StampLayout) + "-" + hex.EncodeToString(random)
}

// Since is the time elapsed since start in seconds, rounded to the millisecond.
func Since(start time.Time) value.Float {
	return value.Float(math.Round(time.Since(start).Seconds()*1000) / 1000)
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
