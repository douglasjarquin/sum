package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func jsonInt(n int) json.Number {
	return json.Number(strconv.Itoa(n))
}

func TestOpen_initializesEmptyStoreWithoutWriting(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	s, err := Open(home)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if s.Home != home {
		t.Fatalf("home = %q, want %q", s.Home, home)
	}
	if _, statErr := ordjson.ReadFile(filepath.Join(home, "state.json")); statErr == nil {
		t.Fatal("Open must not create state.json")
	}
}

func TestInitThenSaveThenReadTask_roundTripsAndBumpsUpdatedAt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	taskID := "t-0123456789ab"
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(Schema))
	task.Set("id", taskID)
	task.Set("status", "prepared")
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := task.Get("updated_at"); !ok {
		t.Fatal("save did not set updated_at")
	}

	read, err := s.ReadTask(taskID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	status, _ := read.Get("status")
	if status != "prepared" {
		t.Fatalf("status = %v, want prepared", status)
	}
}

func TestTaskPath_rejectsInvalidTaskID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := s.TaskPath("not-an-id"); err == nil {
		t.Fatal("expected invalid task ID to be rejected")
	}
}

func TestAllTasks_returnsTasksSortedByID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, id := range []string{"t-bbbbbbbbbbbb", "t-aaaaaaaaaaaa"} {
		task := ordjson.NewObject()
		task.Set("schema", jsonInt(Schema))
		task.Set("id", id)
		if err := s.SaveTask(task); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	tasks, err := s.AllTasks()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
	first, _ := tasks[0].Get("id")
	if first != "t-aaaaaaaaaaaa" {
		t.Fatalf("first task id = %v, want t-aaaaaaaaaaaa", first)
	}
}

func TestLock_excludesASecondLockerUntilReleased(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		second, err := Open(s.Home)
		if err != nil {
			acquired <- err
			return
		}
		secondUnlock, err := second.Lock()
		if err != nil {
			acquired <- err
			return
		}
		defer secondUnlock()
		acquired <- nil
	}()

	select {
	case err := <-acquired:
		t.Fatalf("second locker acquired the lock while the first still held it (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second locker failed after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second locker never acquired the lock after release")
	}
}

func TestDeliveryLockContextGivesUpAtTheCallersDeadline(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	held, err := s.DeliveryLock()
	if err != nil {
		t.Fatal(err)
	}
	// The contender is another process's Store; the same Store would refuse the second acquisition as out of order.
	other, err := Open(s.Home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := other.DeliveryLockContext(ctx); !errors.Is(err, ErrDeliveryLockBusy) {
		t.Fatalf("err = %v, want ErrDeliveryLockBusy", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("bounded acquisition waited %s", elapsed)
	}
	if err := held(); err != nil {
		t.Fatal(err)
	}
	unlock, err := other.DeliveryLockContext(context.Background())
	if err != nil {
		t.Fatalf("free lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestLockOrderRefusesInversionInsteadOfDeadlocking(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := [3]string{"m", "lab", "w1:p1"}, [3]string{"m", "lab", "w2:p1"}
	cases := []struct {
		name  string
		first func() (func() error, error)
		then  func() (func() error, error)
	}{
		{"recipient under state", s.Lock, func() (func() error, error) { return s.RecipientLock(context.Background(), a) }},
		{"compat under recipient", func() (func() error, error) { return s.RecipientLock(context.Background(), a) }, func() (func() error, error) { return s.DeliveryShared(context.Background()) }},
		{"compat under state", s.Lock, s.DeliveryLock},
		{"second recipient", func() (func() error, error) { return s.RecipientLock(context.Background(), a) }, func() (func() error, error) { return s.RecipientLock(context.Background(), b) }},
		{"state under state", s.Lock, s.Lock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unlock, err := tc.first()
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := tc.then()
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, ErrLockOrder) {
					t.Fatalf("err = %v, want ErrLockOrder", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("out-of-order acquisition blocked instead of refusing")
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
	// The documented order succeeds, and every rank is released afterwards.
	shared, err := s.DeliveryShared(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := s.RecipientLock(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	for _, unlock := range []func() error{state, recipient, shared} {
		if err := unlock(); err != nil {
			t.Fatal(err)
		}
	}
	unlock, err := s.DeliveryLock()
	if err != nil {
		t.Fatalf("after releasing everything, the compat lock = %v", err)
	}
	unlock()
}

func TestRecipientLocksAreIndependentAndBounded(t *testing.T) {
	home := t.TempDir()
	one, _ := Open(home)
	two, _ := Open(home)
	a, b := [3]string{"m", "lab", "w1:p1"}, [3]string{"m", "lab", "w2:p1"}
	heldShared, err := one.DeliveryShared(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	heldA, err := one.RecipientLock(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	otherShared, err := two.DeliveryShared(ctx)
	if err != nil {
		t.Fatalf("a second shared holder = %v, want it to coexist", err)
	}
	otherB, err := two.RecipientLock(ctx, b)
	if err != nil {
		t.Fatalf("an unrelated recipient = %v, want it free", err)
	}
	otherB()
	started := time.Now()
	if _, err := two.RecipientLock(ctx, a); !errors.Is(err, ErrRecipientBusy) {
		t.Fatalf("held recipient = %v, want ErrRecipientBusy", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("bounded recipient acquisition waited %s", elapsed)
	}
	otherShared()
	ended, stop := context.WithCancel(context.Background())
	stop()
	if _, err := two.DeliveryLockContext(ended); !errors.Is(err, ErrDeliveryLockBusy) {
		t.Fatalf("exclusive compat while a shared holder remains = %v, want busy", err)
	}
	heldA()
	heldShared()
	unlock, err := two.DeliveryLockContext(ended)
	if err != nil {
		t.Fatalf("free exclusive compat on one try = %v", err)
	}
	unlock()
	if one.RecipientLockPath(a) == one.RecipientLockPath(b) || one.RecipientLockPath(a) != two.RecipientLockPath(a) {
		t.Fatal("recipient lock paths must be one per endpoint")
	}
}
