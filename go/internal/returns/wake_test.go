package returns

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
)

func wakeLab(t *testing.T) (*store.Store, [3]string) {
	t.Helper()
	for _, key := range []string{"SUM_HOME", "SUM_NOW", "HERDR_SESSION", "HERDR_PANE_ID", "HERDR_ENV"} {
		t.Setenv(key, "")
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s, [3]string{"m-stable", "lab", "w-root:p1"}
}

func preparedWake(t *testing.T, s *store.Store, endpoint [3]string) *Wake {
	t.Helper()
	w, err := NewWake(s, endpoint, json.RawMessage(`{"terminal":"term-w-root:p1"}`))
	if err != nil {
		t.Fatal(err)
	}
	w.Prepare("w-0001", []WakeRef{{Task: "t-000000000001", ID: "report:r1"}})
	return w
}

func TestReadWakeAbsentMeansNoEpisode(t *testing.T) {
	s, endpoint := wakeLab(t)
	w, status := ReadWake(s, endpoint)
	if w != nil || status.State != WakeAbsent || status.Diagnostic != "" {
		t.Fatalf("absent file: wake = %v, status = %+v", w, status)
	}
}

func TestWriteWakeRoundTripsAndIsAtomic(t *testing.T) {
	s, endpoint := wakeLab(t)
	w := preparedWake(t, s, endpoint)
	if err := WriteWake(s, w); err != nil {
		t.Fatal(err)
	}
	path := s.WakePath(endpoint)
	if !strings.HasSuffix(path, ".wake.json") || filepath.Dir(path) != filepath.Dir(s.RecipientLockPath(endpoint)) {
		t.Fatalf("wake path = %s, want beside the recipient lock", path)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
	got, status := ReadWake(s, endpoint)
	if status.State != WakeOK || got == nil {
		t.Fatalf("status = %+v", status)
	}
	if got.Generation != 1 || got.Episode == nil || got.Episode.ID != "w-0001" || got.Episode.Phase != WakePrepared || len(got.Episode.Claims) != 1 || got.Episode.Claims[0].ID != "report:r1" {
		t.Fatalf("round trip = %+v / %+v", got, got.Episode)
	}
	if got.IsOutstanding() || !got.NeedsReconcile() {
		t.Fatalf("a prepared-only episode must need reconciliation, not be outstanding")
	}
}

func TestWakePhaseTransitions(t *testing.T) {
	s, endpoint := wakeLab(t)
	w := preparedWake(t, s, endpoint)
	for _, tc := range []struct {
		phase       string
		outstanding bool
	}{
		{WakeClaimed, true}, {WakeIntent, true}, {WakeSubmitted, true}, {WakeUncertain, true},
		{WakeNotDelivered, false}, {WakeNotSubmitted, false}, {WakeConsumed, false},
	} {
		w.Episode.Phase = tc.phase
		if got := w.IsOutstanding(); got != tc.outstanding {
			t.Fatalf("phase %s outstanding = %v, want %v", tc.phase, got, tc.outstanding)
		}
		if w.NeedsReconcile() {
			t.Fatalf("phase %s must not need reconciliation", tc.phase)
		}
	}
	// A second episode starts from a closed one with the next generation.
	w.Episode.Phase = WakeNotDelivered
	w.Prepare("w-0002", nil)
	if w.Generation != 2 || w.Episode.ID != "w-0002" || w.Episode.Phase != WakePrepared {
		t.Fatalf("second episode = %d %+v", w.Generation, w.Episode)
	}
	// Receipts stay bounded.
	for i := 0; i < 25; i++ {
		w.AddReceipt(WakeReceipt{Fingerprint: "f", Generation: i, At: "t", Result: "consumed"})
	}
	if len(w.Receipts) != WakeReceiptsBound {
		t.Fatalf("receipts = %d, want %d", len(w.Receipts), WakeReceiptsBound)
	}
}

func TestReadWakeBlocksOnEveryInvalidFile(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, s *store.Store, endpoint [3]string)
		want  string
	}{
		{"malformed", func(t *testing.T, s *store.Store, endpoint [3]string) {
			writeRaw(t, s.WakePath(endpoint), "{not json")
		}, "not valid JSON"},
		{"unreadable", func(t *testing.T, s *store.Store, endpoint [3]string) {
			if os.Getuid() == 0 {
				t.Skip("root reads everything")
			}
			writeRaw(t, s.WakePath(endpoint), "{}")
			if err := os.Chmod(s.WakePath(endpoint), 0o000); err != nil {
				t.Fatal(err)
			}
		}, "cannot be read"},
		{"symlink", func(t *testing.T, s *store.Store, endpoint [3]string) {
			target := filepath.Join(s.Home, "elsewhere.json")
			writeRaw(t, target, "{}")
			if err := os.MkdirAll(filepath.Dir(s.WakePath(endpoint)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, s.WakePath(endpoint)); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
		{"directory", func(t *testing.T, s *store.Store, endpoint [3]string) {
			if err := os.MkdirAll(s.WakePath(endpoint), 0o700); err != nil {
				t.Fatal(err)
			}
		}, "directory"},
		{"unsupported schema", func(t *testing.T, s *store.Store, endpoint [3]string) {
			w := preparedWake(t, s, endpoint)
			w.Schema = 99
			if err := WriteWake(s, w); err != nil {
				t.Fatal(err)
			}
		}, "schema 99"},
		{"wrong installation", func(t *testing.T, s *store.Store, endpoint [3]string) {
			w := preparedWake(t, s, endpoint)
			w.Installation = "another-installation"
			if err := WriteWake(s, w); err != nil {
				t.Fatal(err)
			}
		}, "another installation"},
		{"wrong recipient", func(t *testing.T, s *store.Store, endpoint [3]string) {
			w := preparedWake(t, s, endpoint)
			w.Recipient.Pane = "w-other:p1"
			if err := writeWakeAt(s.WakePath(endpoint), w); err != nil {
				t.Fatal(err)
			}
		}, "another recipient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, endpoint := wakeLab(t)
			tc.plant(t, s, endpoint)
			w, status := ReadWake(s, endpoint)
			if w != nil || status.State != WakeBlocked || !strings.Contains(status.Diagnostic, tc.want) {
				t.Fatalf("wake = %v, status = %+v, want blocked mentioning %q", w, status, tc.want)
			}
			if !strings.Contains(status.Diagnostic, "sumctl wake show") {
				t.Fatalf("diagnostic %q does not name the inspection command", status.Diagnostic)
			}
		})
	}
}

// A directory sync failure after the rename is reported, and the caller reloads the persisted phase under its
// locks instead of assuming nothing committed.
func TestWakeSyncFailureAfterRenameIsReloadedNotAssumedUncommitted(t *testing.T) {
	s, endpoint := wakeLab(t)
	w := preparedWake(t, s, endpoint)
	if err := WriteWake(s, w); err != nil {
		t.Fatal(err)
	}
	old := wakeSyncDir
	wakeSyncDir = func(string) error { return errors.New("injected: directory sync failed") }
	t.Cleanup(func() { wakeSyncDir = old })
	w.Episode.Phase = WakeIntent
	err := WriteWake(s, w)
	var syncErr *WakeSyncError
	if !errors.As(err, &syncErr) {
		t.Fatalf("err = %v, want a WakeSyncError", err)
	}
	persisted, status := ReadWake(s, endpoint)
	if status.State != WakeOK || persisted.Episode.Phase != WakeIntent {
		t.Fatalf("persisted = %+v (%+v), want the renamed intent phase", persisted.Episode, status)
	}
	committed, err := CommitWake(s, w)
	if err != nil || committed == nil || committed.Episode.Phase != WakeIntent {
		t.Fatalf("CommitWake = %+v, %v; want the reloaded intent phase and no error", committed, err)
	}
}

func TestListWakesReportsEveryRecipientFile(t *testing.T) {
	s, endpoint := wakeLab(t)
	if got, err := ListWakes(s); err != nil || len(got) != 0 {
		t.Fatalf("empty home lists %v, %v", got, err)
	}
	w := preparedWake(t, s, endpoint)
	w.Episode.Phase = WakeSubmitted
	if err := WriteWake(s, w); err != nil {
		t.Fatal(err)
	}
	other := [3]string{"m-stable", "lab", "w-other:p1"}
	writeRaw(t, s.WakePath(other), "{broken")
	got, err := ListWakes(s)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %v, %v", got, err)
	}
	var ok, blocked int
	for _, entry := range got {
		switch entry.Status.State {
		case WakeOK:
			ok++
			if !entry.Wake.IsOutstanding() {
				t.Fatalf("submitted episode not outstanding: %+v", entry.Wake.Episode)
			}
		case WakeBlocked:
			blocked++
		}
	}
	if ok != 1 || blocked != 1 {
		t.Fatalf("ok = %d blocked = %d", ok, blocked)
	}
}

func writeRaw(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
