package pyrepr

import (
	"encoding/json"
	"testing"
)

func TestRepr_matchesPythonReprForCommonTypes(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{nil, "None"},
		{true, "True"},
		{false, "False"},
		{json.Number("0"), "0"},
		{json.Number("1.5"), "1.5"},
		{"plain", "'plain'"},
		{"has'quote", `"has'quote"`},
		{[]any{json.Number("1"), "two"}, "[1, 'two']"},
	}
	for _, tc := range cases {
		if got := Repr(tc.value); got != tc.want {
			t.Errorf("Repr(%#v) = %s, want %s", tc.value, got, tc.want)
		}
	}
}

func TestStrList_matchesPythonListReprOfStrings(t *testing.T) {
	if got, want := StrList([]string{"a", "b"}), "['a', 'b']"; got != want {
		t.Fatalf("StrList = %s, want %s", got, want)
	}
	if got, want := StrList(nil), "[]"; got != want {
		t.Fatalf("StrList(nil) = %s, want %s", got, want)
	}
}
