package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestLogicalTaskActorRequiresCurrentClaimAndMatchingOwner(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ObserveTaskActor(s, ctx)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("owner", session.Owner())
	check := func() error {
		unlock, err := s.Lock()
		if err != nil {
			return err
		}
		defer unlock()
		return CheckTaskRole(s, task, actor, "coordinator")
	}
	if err = check(); err != nil {
		t.Fatal(err)
	}
	other := session.Owner()
	other.Set("session_id", "another-session")
	task.Set("owner", other)
	if err = check(); err == nil {
		t.Fatal("accepted another session's task")
	}
	task.Set("owner", session.Owner())
	next, err := s.ClaimCoordinator("", "replacement", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = check(); !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("observed actor survived transfer: %v", err)
	}
	if _, err = RequireClaim(s, session.Token()); !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("stale claim: %v", err)
	}
	if _, err = RequireClaim(s, next.Token()); err != nil {
		t.Fatal(err)
	}
}

func TestLogicalTaskActorNeedsNoNativeEndpoint(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ObserveTaskActor(s, session.Owner()); !errors.Is(err, store.ErrClaimRequired) {
		t.Fatalf("owner without captured claim: %v", err)
	}
	ctx, err := RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EndpointFromContext(ctx).Validate(); err == nil {
		t.Fatal("logical actor became native endpoint")
	}
	if _, err = ObserveTaskActor(s, ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCoordinatorCannotAuthorizeAfterLogicalRetirement(t *testing.T) {
	l := taskActorLab(t, "coordinator")
	actor := &TaskActor{endpoint: l.endpoint}
	if _, err := ObserveTaskActor(l.store, l.endpoint.Object()); err == nil {
		t.Fatal("native coordinator acquired task authority before retirement")
	}
	if err := RequireCoordinator(l.store, l.endpoint.Object()); err == nil {
		t.Fatal("native coordinator acquired write authority before retirement")
	}
	session, err := l.store.RetireNativeCoordinator("", "ordinary conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireCoordinator(l.store, l.endpoint.Object()); err == nil {
		t.Fatal("retired native coordinator retained write authority")
	}
	if _, err := ObserveTaskActor(l.store, l.endpoint.Object()); err == nil {
		t.Fatal("retired native coordinator acquired a task actor")
	}
	if err := checkActorLocked(l, actor, "coordinator"); err == nil {
		t.Fatal("observed native actor survived retirement")
	}
	ctx, err := RequireClaim(l.store, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireCoordinator(l.store, ctx); err != nil {
		t.Fatal(err)
	}
}

func TestContextInheritedTaskHomeProvenance(t *testing.T) {
	for _, home := range []string{"foreign", "unset", "local"} {
		t.Run(home, func(t *testing.T) {
			root := t.TempDir()
			s, err := store.Open(filepath.Join(root, ".sum"))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"SUM_HOME", "SUM_SESSION", "HC_SESSION", "HC_SOCKET", "HC_TERMINAL_ID", "SUM_CORE_ID"} {
				t.Setenv(name, "")
			}
			t.Setenv("SUM_ROLE", "worker")
			t.Setenv("SUM_TASK", "t-aaaaaaaaaaaa")
			t.Setenv("SUM_ATTEMPT", "missing")
			switch home {
			case "foreign":
				t.Setenv("SUM_HOME", t.TempDir())
			case "local":
				t.Setenv("SUM_HOME", s.Home)
			}
			_, err = Context(s, root, "")
			if home == "local" {
				if err == nil || errors.Is(err, store.ErrClaimRequired) {
					t.Fatalf("local task bypassed native context validation: %v", err)
				}
			} else if !errors.Is(err, store.ErrClaimRequired) {
				t.Fatalf("foreign or absent home did not require a claim: %v", err)
			}
			session, err := s.ClaimCoordinator("", "conversation", false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := Context(s, root, session.Token())
			if err != nil {
				t.Fatalf("explicit claim ignored: %v", err)
			}
			if err := RequireCoordinator(s, ctx); err != nil {
				t.Fatalf("explicit claim lost authority: %v", err)
			}
		})
	}
}
