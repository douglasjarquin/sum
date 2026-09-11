package ordjson

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_matchesPythonAtomicJSONByteForByte(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	libPath := filepath.Join(repoRoot, "lib", "sumctl.py")
	if _, err := os.Stat(libPath); err != nil {
		t.Skipf("reference lib/sumctl.py not found: %v", err)
	}

	eAcute := string(rune(0x00e9))
	musicalSymbol := string(rune(0x1d11e))
	stringValue := strings.Join([]string{
		"caf" + eAcute,
		`\n`,
		`\"`,
		`\\`,
		musicalSymbol,
	}, " ")
	input := `{"b": 1, "a": {"nested": true, "list": [1, 2, "x"]}, "s": "` + stringValue + `"}`

	pythonOut := filepath.Join(t.TempDir(), "python.json")
	cmd := exec.Command("python3", "testdata/atomic_json_ref.py", libPath, pythonOut, input)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, out)
	}
	want, err := os.ReadFile(pythonOut)
	if err != nil {
		t.Fatal(err)
	}

	value, err := Decode([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	goOut := filepath.Join(t.TempDir(), "go.json")
	if err := WriteFile(goOut, value); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(goOut)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(want) {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", got, want)
	}
}
