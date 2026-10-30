package prepare

import "testing"

func TestNativeNameIsBoundedAttemptIdentity(t *testing.T) {
	a := NativeName("installation", "t-123", "worker", "x-1")
	if len(a) != 32 || a != NativeName("installation", "t-123", "worker", "x-1") {
		t.Fatalf("unstable or unbounded: %s", a)
	}
	for _, b := range []string{NativeName("other", "t-123", "worker", "x-1"), NativeName("installation", "t-124", "worker", "x-1"), NativeName("installation", "t-123", "reviewer", "x-1"), NativeName("installation", "t-123", "worker", "x-2")} {
		if a == b {
			t.Fatal("names collide across authority")
		}
	}
}
