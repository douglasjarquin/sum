package pycompat

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Object is a JSON object that keeps its keys in insertion order, as a record built up key by key always has.
// A key set again keeps its first position.
type Object struct {
	keys   []string
	values map[string]any
}

func (o *Object) Set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *Object) Get(key string) (value any, ok bool) {
	value, ok = o.values[key]
	return value, ok
}

func (o *Object) Keys() []string {
	return o.keys
}

func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := enc.Encode(key); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := enc.Encode(o.values[key]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Seconds is an elapsed time rounded to the millisecond and always written with a decimal point, as a Python float is.
type Seconds float64

func Since(start time.Time) Seconds {
	return Seconds(math.Round(time.Since(start).Seconds()*1000) / 1000)
}

func (s Seconds) MarshalJSON() ([]byte, error) {
	text := strconv.FormatFloat(float64(s), 'f', -1, 64)
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return []byte(text), nil
}

// Stamp is the form every record id takes: a sortable UTC timestamp and n random bytes in hex.
func Stamp(n int) string {
	random := make([]byte, n)
	_, _ = rand.Read(random) // crypto/rand.Read never fails.
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random)
}

// ExitCode is subprocess's returncode: the exit status, or the negated signal number when a signal ended the child.
func ExitCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -int(status.Signal())
	}
	return state.ExitCode()
}
