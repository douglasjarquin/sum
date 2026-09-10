package ordjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

type Object struct {
	keys   []string
	values map[string]any
}

func NewObject() *Object {
	return &Object{values: map[string]any{}}
}

func (o *Object) Set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *Object) Get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

func (o *Object) Delete(key string) {
	if _, exists := o.values[key]; !exists {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *Object) Keys() []string {
	return append([]string(nil), o.keys...)
}

func (o *Object) Len() int {
	return len(o.keys)
}

func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return value, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeToken(dec, tok)
}

func decodeToken(dec *json.Decoder, tok json.Token) (any, error) {
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := NewObject()
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, fmt.Errorf("expected string object key")
			}
			val, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			obj.Set(key, val)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []any{}
		for dec.More() {
			val, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %v", delim)
	}
}

const indentUnit = "  "

func MarshalIndent(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeIndent(&buf, value, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func MarshalCompact(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeCompact(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeCompact(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case *Object:
		buf.WriteByte('{')
		for i, k := range v.keys {
			if i > 0 {
				buf.WriteString(", ")
			}
			encodeString(buf, k)
			buf.WriteString(": ")
			if err := encodeCompact(buf, v.values[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := encodeCompact(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case string:
		encodeString(buf, v)
	case json.Number:
		buf.WriteString(string(v))
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	case int:
		buf.WriteString(strconv.Itoa(v))
	case int64:
		buf.WriteString(strconv.FormatInt(v, 10))
	case float64:
		buf.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	default:
		return fmt.Errorf("ordjson: unsupported type %T", value)
	}
	return nil
}

func writeIndent(buf *bytes.Buffer, level int) {
	for range level {
		buf.WriteString(indentUnit)
	}
}

func encodeIndent(buf *bytes.Buffer, value any, level int) error {
	switch v := value.(type) {
	case *Object:
		if v.Len() == 0 {
			buf.WriteString("{}")
			return nil
		}
		buf.WriteString("{\n")
		for i, k := range v.keys {
			writeIndent(buf, level+1)
			encodeString(buf, k)
			buf.WriteString(": ")
			if err := encodeIndent(buf, v.values[k], level+1); err != nil {
				return err
			}
			if i < len(v.keys)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		writeIndent(buf, level)
		buf.WriteByte('}')
	case []any:
		if len(v) == 0 {
			buf.WriteString("[]")
			return nil
		}
		buf.WriteString("[\n")
		for i, item := range v {
			writeIndent(buf, level+1)
			if err := encodeIndent(buf, item, level+1); err != nil {
				return err
			}
			if i < len(v)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		writeIndent(buf, level)
		buf.WriteByte(']')
	case string:
		encodeString(buf, v)
	case json.Number:
		buf.WriteString(string(v))
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	case int:
		buf.WriteString(strconv.Itoa(v))
	case int64:
		buf.WriteString(strconv.FormatInt(v, 10))
	case float64:
		buf.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	default:
		return fmt.Errorf("ordjson: unsupported type %T", value)
	}
	return nil
}

func encodeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			buf.WriteString(`\\`)
		case r == '"':
			buf.WriteString(`\"`)
		case r == '\b':
			buf.WriteString(`\b`)
		case r == '\f':
			buf.WriteString(`\f`)
		case r == '\n':
			buf.WriteString(`\n`)
		case r == '\r':
			buf.WriteString(`\r`)
		case r == '\t':
			buf.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(buf, `\u%04x`, r)
		case r >= 0x20 && r <= 0x7e:
			buf.WriteRune(r)
		case r < 0x10000:
			fmt.Fprintf(buf, `\u%04x`, r)
		default:
			r2 := r - 0x10000
			hi := 0xd800 | ((r2 >> 10) & 0x3ff)
			lo := 0xdc00 | (r2 & 0x3ff)
			fmt.Fprintf(buf, `\u%04x\u%04x`, hi, lo)
		}
	}
	buf.WriteByte('"')
}
