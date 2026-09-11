package pyrepr

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func Repr(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case bool:
		if v {
			return "True"
		}
		return "False"
	case json.Number:
		return string(v)
	case string:
		return reprString(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = Repr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ordjson.Object:
		parts := make([]string, 0, v.Len())
		for _, k := range v.Keys() {
			val, _ := v.Get(k)
			parts = append(parts, reprString(k)+": "+Repr(val))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func StrList(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = reprString(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func reprString(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case byte(r) == quote && r < 128:
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}
