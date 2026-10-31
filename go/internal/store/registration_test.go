package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
)

// The reference values are hashlib.sha256(f"{machine}\n{session}\n{pane}".encode()).hexdigest()[:16].
func TestRegistrationKey_matchesThePythonReferenceHash(t *testing.T) {
	for _, tc := range []struct {
		endpoint Endpoint
		want     string
	}{
		{Endpoint{Machine: "m1", Session: "s1", Pane: "p1"}, "293c980b4e6c2341"},
		{Endpoint{Machine: "m-0123456789abcdef0123456789abcdef", Session: "s1", Pane: "p1"}, "10227e7c9283413e"},
	} {
		if got := RegistrationKey(tc.endpoint); got != tc.want {
			t.Fatalf("RegistrationKey(%+v) = %q, want %q", tc.endpoint, got, tc.want)
		}
	}
}

func TestRegistration_keepsLegacyRecordsHistorical(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	legacy := Endpoint{Machine: "dev", Session: "s1", Pane: "w-worker:p1"}
	path := filepath.Join(s.Sessions, RegistrationKey(legacy)+".json")
	original := []byte(`{"schema":1,"role":"worker","machine":"dev","session":"s1","pane":"w-worker:p1"}`)
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Register(legacy, "worker", "task", nil); err == nil {
		t.Fatal("legacy pane granted HC authority")
	}
	current := nativeEndpoint("dev", "t7", "worker")
	found, err := s.Registration(current)
	if err != nil || found != nil {
		t.Fatalf("legacy registration adopted: %v %v", found, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("historical registration was rewritten")
	}
}
func nativeEndpoint(machine, address, role string) Endpoint {
	return Endpoint{Schema: 2, Backend: "hc", Machine: machine, CoreID: "core-a", Session: "s1", Socket: "/tmp/scratch.sock", Address: address, Role: role, Attempt: "attempt-one", BindingGeneration: 1, CreatedMS: 100, Cwd: "/tmp/x"}
}

func TestRegistration_ignoresASessionFileKeyedByAnotherHostsName(t *testing.T) {
	home := t.TempDir()
	s, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Pin("0123456789abcdef0123456789abcdef", "here"))
	other := Endpoint{Machine: "elsewhere", Session: "s1", Pane: "w-parent:p1"}
	path := filepath.Join(home, "sessions", RegistrationKey(other)+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema": 1, "role": "coordinator", "machine": "elsewhere", "session": "s1", "pane": "w-parent:p1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.Registration(Endpoint{Machine: host.ID, Session: "s1", Pane: "w-parent:p1"})
	if err != nil || found != nil {
		t.Fatalf("Registration = %v, %v; another host's session must not resolve here", found, err)
	}
}

func TestDesignated_falseUntilInitializedAndFalseForADevCheckout(t *testing.T) {
	home := t.TempDir()
	s, err := Open(home)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if s.Designated() {
		t.Fatal("empty store must not be designated")
	}
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !s.Designated() {
		t.Fatal("initialized store must be designated")
	}
	if err := os.WriteFile(home+"/dev.json", []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.Designated() {
		t.Fatal("a store with dev.json must never be designated")
	}
}

func TestOwner_nilWhenAbsent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	owner, err := s.Owner()
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	if owner != nil {
		t.Fatalf("owner = %v, want nil", owner)
	}
}

func TestRegisterThenRegistration_roundTripsAndPreservesRegisteredAt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	endpoint := nativeEndpoint("m1", "t1", "developer")

	first, err := s.Register(endpoint, "developer", nil, nil)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	role, _ := first.Get("role")
	if role != "developer" {
		t.Fatalf("role = %v, want developer", role)
	}
	firstRegisteredAt, _ := first.Get("registered_at")

	read, err := s.Registration(endpoint)
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if read == nil {
		t.Fatal("registration = nil, want the record just written")
	}

	second, err := s.Register(endpoint, "developer", nil, nil)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	secondRegisteredAt, _ := second.Get("registered_at")
	if secondRegisteredAt != firstRegisteredAt {
		t.Fatalf("registered_at changed on re-registration: %v -> %v", firstRegisteredAt, secondRegisteredAt)
	}
	role, _ = second.Get("role")
	if role != "developer" {
		t.Fatalf("role after re-register = %v, want developer", role)
	}
}

func TestRegistration_rejectsIdentityMismatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	endpoint := nativeEndpoint("m1", "t1", "developer")
	if _, err := s.Register(endpoint, "developer", nil, nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	path := s.Sessions + "/" + RegistrationKey(endpoint) + ".json"
	corrupted := `{"schema":1,"key":"x","role":"developer","task":null,"machine":"other","session":"s1","pane":"p1"}`
	if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Registration(endpoint); err == nil {
		t.Fatal("expected an identity mismatch error")
	}
}

func TestRegistrations_returnsAllSortedByFilename(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := s.Register(nativeEndpoint("m1", "t1", "developer"), "developer", nil, nil); err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if _, err := s.Register(nativeEndpoint("m2", "t2", "worker"), "worker", nil, nil); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	all, err := s.Registrations()
	if err != nil {
		t.Fatalf("registrations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(registrations) = %d, want 2", len(all))
	}
}

func TestNativeRegistrationRequiresCurrentBinding(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	endpoint := nativeEndpoint("m1", "t7", "worker")
	endpoint.Task = "t-0123456789ab"
	if _, err = s.Register(endpoint, "worker", endpoint.Task, nil); err != nil {
		t.Fatal(err)
	}
	next := endpoint
	next.Attempt = "attempt-two"
	next.BindingGeneration = 2
	if _, err = s.Registration(next); err == nil {
		t.Fatal("passive lookup inherited old authority")
	}
	if _, err = s.Register(next, "worker", next.Task, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Registration(endpoint); err == nil {
		t.Fatal("old attempt inherited replacement binding")
	}
	other := next
	other.CoreID = "core-b"
	found, err := s.Registration(other)
	if err != nil || found != nil {
		t.Fatalf("same address on other core resolved %v %v", found, err)
	}
}
