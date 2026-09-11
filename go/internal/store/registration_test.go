package store

import (
	"os"
	"testing"
)

func TestRegistrationKey_matchesThePythonReferenceHash(t *testing.T) {
	got := RegistrationKey(Endpoint{Machine: "m1", Session: "s1", Pane: "p1"})
	want := "293c980b4e6c2341"
	if got != want {
		t.Fatalf("RegistrationKey = %q, want %q", got, want)
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
	endpoint := Endpoint{Machine: "m1", Session: "s1", Pane: "p1", Cwd: "/tmp/x"}

	first, err := s.Register(endpoint, "developer", nil)
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

	second, err := s.Register(endpoint, "worker", "t-0123456789ab")
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	secondRegisteredAt, _ := second.Get("registered_at")
	if secondRegisteredAt != firstRegisteredAt {
		t.Fatalf("registered_at changed on re-registration: %v -> %v", firstRegisteredAt, secondRegisteredAt)
	}
	role, _ = second.Get("role")
	if role != "worker" {
		t.Fatalf("role after re-register = %v, want worker", role)
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
	endpoint := Endpoint{Machine: "m1", Session: "s1", Pane: "p1"}
	if _, err := s.Register(endpoint, "developer", nil); err != nil {
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
	if _, err := s.Register(Endpoint{Machine: "m1", Session: "s1", Pane: "p1"}, "developer", nil); err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if _, err := s.Register(Endpoint{Machine: "m2", Session: "s2", Pane: "p2"}, "worker", nil); err != nil {
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
