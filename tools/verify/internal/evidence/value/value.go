// Package value holds JSON values that keep their key order, with the truthiness, equality, and text forms the evidence records were defined with.
//
// A decoded or built value is nil, bool, string, a number (json.Number, int, or Float), []any, or *Object.
package value

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Object is a JSON object whose keys stay in the order they were first set, so a record reads the same every time it is written.
type Object struct {
	keys   []string
	fields map[string]any
}

// NewObject builds an object from alternating keys and values.
func NewObject(pairs ...any) *Object {
	o := &Object{fields: map[string]any{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// Set stores a value; a key that already exists keeps its position.
func (o *Object) Set(key string, v any) {
	if _, ok := o.fields[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.fields[key] = v
}

// Get returns nil for a missing key and for a nil object, so lookups chain through absent nesting.
func (o *Object) Get(key string) any {
	if o == nil {
		return nil
	}
	return o.fields[key]
}

func (o *Object) Has(key string) bool {
	if o == nil {
		return false
	}
	_, ok := o.fields[key]
	return ok
}

func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return o.keys
}

func (o *Object) Len() int {
	return len(o.Keys())
}

// Clone copies the top level only.
func (o *Object) Clone() *Object {
	c := NewObject()
	for _, key := range o.Keys() {
		c.Set(key, o.fields[key])
	}
	return c
}

func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	buf.WriteByte('{')
	for i, key := range o.Keys() {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := enc.Encode(key); err != nil {
			return nil, err
		}
		buf.Truncate(buf.Len() - 1) // Encode ends each value with a newline.
		buf.WriteByte(':')
		if err := enc.Encode(o.fields[key]); err != nil {
			return nil, err
		}
		buf.Truncate(buf.Len() - 1)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Float is a number written with a fractional part even when it is whole (10.0), which keeps it distinct from a count in the record.
type Float float64

func (f Float) String() string {
	text := strconv.FormatFloat(float64(f), 'f', -1, 64)
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}

func (f Float) MarshalJSON() ([]byte, error) {
	return []byte(f.String()), nil
}

// Decode parses one JSON document, keeping object key order and the exact text of every number.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decode(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func decode(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return nil, errors.New("empty or truncated JSON")
		}
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	if delim == '{' {
		obj := NewObject()
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decode(dec)
			if err != nil {
				return nil, err
			}
			obj.Set(key.(string), v)
		}
		_, err := dec.Token()
		return obj, err
	}
	list := []any{}
	for dec.More() {
		v, err := decode(dec)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	_, err = dec.Token()
	return list, err
}

// Obj returns the object, or nil when v is anything else.
func Obj(v any) *Object {
	o, _ := v.(*Object)
	return o
}

// List returns the array, or nil when v is anything else.
func List(v any) []any {
	l, _ := v.([]any)
	return l
}

// Str returns the string, or "" when v is anything else.
func Str(v any) string {
	s, _ := v.(string)
	return s
}

// Strings wraps a string slice as a JSON array that is never null.
func Strings(items []string) []any {
	list := make([]any, len(items))
	for i, item := range items {
		list[i] = item
	}
	return list
}

// Number reads any numeric value.
func Number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	case Float:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// Truthy is false for null, false, zero, and anything empty.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case *Object:
		return t.Len() > 0
	}
	n, ok := Number(v)
	return !ok || n != 0
}

// Equal compares two values by content: numbers by magnitude, objects regardless of key order.
func Equal(a, b any) bool {
	if x, ok := Number(a); ok {
		y, ok := Number(b)
		return ok && x == y
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case *Object:
		y, ok := b.(*Object)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for _, key := range x.Keys() {
			if !y.Has(key) || !Equal(x.Get(key), y.Get(key)) {
				return false
			}
		}
		return true
	}
	return false
}

// Text is the value as it appears inside a sentence: a string as itself, null as None, booleans capitalised.
func Text(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return t
	case json.Number:
		return t.String()
	case Float:
		return t.String()
	case []any, *Object:
		return Repr(v)
	}
	return fmt.Sprint(v)
}

// Repr is Text with strings quoted, the form findings and assertions use to show exactly what a value was.
func Repr(v any) string {
	switch t := v.(type) {
	case string:
		return quote(t)
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			parts[i] = Repr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Object:
		parts := make([]string, 0, t.Len())
		for _, key := range t.Keys() {
			parts = append(parts, quote(key)+": "+Repr(t.Get(key)))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return Text(v)
}

// quote wraps a string in single quotes, or double quotes when it holds a single quote and no double quote, escaping what would not print.
func quote(s string) string {
	q := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		q = '"'
	}
	var b strings.Builder
	b.WriteRune(q)
	for _, r := range s {
		switch {
		case r == q || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsPrint(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(q)
	return b.String()
}
