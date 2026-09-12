package ordjson

import (
	"testing"

	"github.com/douglasjarquin/go-toon"
)

func TestDecodeThenMarshalIndent_preservesInsertionOrder(t *testing.T) {
	input := `{"b": 1, "a": 2, "c": {"z": 1, "y": 2}}`
	value, err := Decode([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	obj, ok := value.(*Object)
	if !ok {
		t.Fatalf("value = %T, want *Object", value)
	}
	if got, want := obj.Keys(), []string{"b", "a", "c"}; !equalStrings(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	encoded, err := MarshalIndent(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "{\n  \"b\": 1,\n  \"a\": 2,\n  \"c\": {\n    \"z\": 1,\n    \"y\": 2\n  }\n}"
	if string(encoded) != want {
		t.Fatalf("encoded =\n%s\nwant\n%s", encoded, want)
	}
}

func TestMarshalTOON_roundTripPreservesKeysAndIsSmallerThanJSON(t *testing.T) {
	obj := NewObject()
	obj.Set("role", "coordinator")
	tasks := []any{}
	for i := 0; i < 3; i++ {
		row := NewObject()
		row.Set("id", "task-"+string(rune('a'+i)))
		row.Set("status", "running")
		row.Set("ok", true)
		tasks = append(tasks, row)
	}
	obj.Set("tasks", tasks)
	jsonBytes, err := MarshalIndent(obj)
	if err != nil {
		t.Fatal(err)
	}
	toonBytes, err := MarshalTOON(obj)
	if err != nil {
		t.Fatal(err)
	}
	if len(toonBytes) >= len(jsonBytes) {
		t.Fatalf("TOON %d bytes is not smaller than JSON %d bytes\n%s", len(toonBytes), len(jsonBytes), toonBytes)
	}
	decoded, err := toon.Decode(toonBytes)
	if err != nil {
		t.Fatal(err)
	}
	if decoded == nil {
		t.Fatal("empty decode")
	}
}

func TestMarshalIndent_emptyContainersStayInline(t *testing.T) {
	obj := NewObject()
	obj.Set("list", []any{})
	obj.Set("obj", NewObject())
	encoded, err := MarshalIndent(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "{\n  \"list\": [],\n  \"obj\": {}\n}"
	if string(encoded) != want {
		t.Fatalf("encoded =\n%s\nwant\n%s", encoded, want)
	}
}

func TestMarshalIndent_escapesNonASCIILikePythonEnsureASCII(t *testing.T) {
	obj := NewObject()
	input := "caf" + string(rune(0xe9)) + " " + string(rune(0x01)) + " \n \" \\ " + string(rune(0x1d11e))
	obj.Set("s", input)
	encoded, err := MarshalIndent(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "{\n  \"s\": \"caf\\u00e9 \\u0001 \\n \\\" \\\\ \\ud834\\udd1e\"\n}"
	if string(encoded) != want {
		t.Fatalf("encoded =\n%s\nwant\n%s", encoded, want)
	}
}

func TestSet_updatesInPlaceWithoutReordering(t *testing.T) {
	obj := NewObject()
	obj.Set("a", 1)
	obj.Set("b", 2)
	obj.Set("a", 3)
	if got, want := obj.Keys(), []string{"a", "b"}; !equalStrings(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	value, _ := obj.Get("a")
	if value != 3 {
		t.Fatalf("a = %v, want 3", value)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
