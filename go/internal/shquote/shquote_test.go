package shquote

import "testing"

func TestQuote_matchesPythonShlexQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"plain", "plain"},
		{"a/b-c.d_e:f,g@h%i+j=k", "a/b-c.d_e:f,g@h%i+j=k"},
		{"has space", "'has space'"},
		{"has'quote", `'has'"'"'quote'`},
	}
	for _, tc := range cases {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCommandFor_joinsWithHomeFlag(t *testing.T) {
	got := CommandFor("/bin/sumctl", "/tmp/dir with spaces", "metadata", "enable")
	want := "/bin/sumctl --home '/tmp/dir with spaces' metadata enable"
	if got != want {
		t.Fatalf("CommandFor = %q, want %q", got, want)
	}
}
