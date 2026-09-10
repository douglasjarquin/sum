package store

import (
	"encoding/json"
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
