package mesh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func parse(data json.RawMessage, target any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid tool arguments: multiple JSON values")
		}
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func jsonText(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode tool result: %w", err)
	}
	return string(data), nil
}

func trim(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 300 {
		return string([]rune(value)[:300])
	}
	return value
}

func fallback(value, alternative string) string {
	if value == "" {
		return alternative
	}
	return value
}

func validTarget(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > 200 {
		return false
	}
	for index, runeValue := range value {
		if unicode.IsSpace(runeValue) || (index == 0 && runeValue == '-') {
			return false
		}
	}
	return true
}
