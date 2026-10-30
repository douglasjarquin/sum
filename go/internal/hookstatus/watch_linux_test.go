//go:build linux

package hookstatus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinuxWatchReadyThenAtomicHealthReplacement(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dirty, ready := make(chan struct{}, 1), make(chan struct{})
	stopped := make(chan error, 1)
	go func() { stopped <- watchDirty(ctx, directory, dirty, ready) }()
	select {
	case <-ready:
	case err := <-stopped:
		t.Fatalf("watch setup: %v", err)
	case <-time.After(time.Second):
		t.Fatal("watch did not signal startup readiness")
	}
	// Stage outside the watched directory so MOVED_TO is the only change event.
	staged := filepath.Join(t.TempDir(), "health")
	if err := os.WriteFile(staged, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(directory, "health.json")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dirty:
	case err := <-stopped:
		t.Fatalf("watch stopped: %v", err)
	case <-time.After(time.Second):
		t.Fatal("atomic health replacement was not observed")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("watch cancellation leaked")
	}
}

func TestLinuxWatchSetupFailureDoesNotAdvertiseReady(t *testing.T) {
	ready := make(chan struct{})
	err := watchDirty(context.Background(), filepath.Join(t.TempDir(), "absent"), make(chan struct{}, 1), ready)
	if err == nil {
		t.Fatal("missing path accepted")
	}
	select {
	case <-ready:
		t.Fatal("failed watch advertised readiness")
	default:
	}
}
