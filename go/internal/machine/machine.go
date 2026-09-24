// Package machine names the host sum runs on. Herdr pane IDs are unique only
// within one Herdr server, so every recorded endpoint carries a machine
// component, and that component must stay the same when the host is renamed
// and differ between hosts.
//
// The identity is derived from the operating system's own machine identifier
// (/etc/machine-id on Linux, the platform UUID on macOS), hashed with a
// sum-specific key so the raw identifier is never stored. The hostname is read
// only for display and to recognise records written before this identity
// existed.
package machine

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

// File is the per-state-home alias table: the legacy hostname identities this
// host has been observed using, keyed by the stable identity that observed them.
const File = "machine.json"

const derivationKey = "sum machine identity v1"

var linuxIDPaths = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

var platformUUIDPattern = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

type pin struct{ raw, hostname string }

var (
	pinMu  sync.Mutex
	pinned *pin

	darwinOnce sync.Once
	darwinRaw  string
)

// Pin replaces the host's raw machine identifier and hostname until the
// returned function runs. It exists for tests, which simulate a renamed host or
// a different host in-process; production code never calls it.
func Pin(raw, hostname string) (restore func()) {
	pinMu.Lock()
	previous := pinned
	pinned = &pin{raw: raw, hostname: hostname}
	pinMu.Unlock()
	return func() {
		pinMu.Lock()
		pinned = previous
		pinMu.Unlock()
	}
}

func current() *pin {
	pinMu.Lock()
	defer pinMu.Unlock()
	return pinned
}

// Hostname is the host's current name, for display and legacy recognition only.
func Hostname() (string, error) {
	if p := current(); p != nil {
		return p.hostname, nil
	}
	return os.Hostname()
}

// ID is this host's stable identity: the same across renames, different
// between hosts whose operating systems carry different machine identifiers.
func ID() (string, error) {
	if p := current(); p != nil {
		return derive(p.raw), nil
	}
	raw, err := osRaw()
	if err != nil {
		return "", err
	}
	return derive(raw), nil
}

func derive(raw string) string {
	mac := hmac.New(sha256.New, []byte(raw))
	mac.Write([]byte(derivationKey))
	return "m-" + hex.EncodeToString(mac.Sum(nil))[:32]
}

func osRaw() (string, error) {
	if runtime.GOOS == "darwin" {
		darwinOnce.Do(func() { darwinRaw = darwinPlatformUUID() })
		if darwinRaw == "" {
			return "", fmt.Errorf("cannot read the platform UUID from ioreg")
		}
		return darwinRaw, nil
	}
	for _, path := range linuxIDPaths {
		if data, err := os.ReadFile(path); err == nil {
			if raw := strings.TrimSpace(string(data)); raw != "" {
				return raw, nil
			}
		}
	}
	return generatedRaw()
}

func darwinPlatformUUID() string {
	cmd := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
	done := make(chan struct{})
	var out []byte
	go func() {
		out, _ = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return ""
	}
	if m := platformUUIDPattern.FindSubmatch(out); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

// generatedRaw covers a Linux host with no machine identifier (some minimal
// containers). The value is created once under the user's home directory,
// outside any sum installation, so copying an installation or restoring a
// backup never carries it to another host.
func generatedRaw() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no operating-system machine ID and no home directory: %w", err)
	}
	path := filepath.Join(home, ".local", "state", "sum", "machine-id")
	if data, err := os.ReadFile(path); err == nil {
		if raw := strings.TrimSpace(string(data)); raw != "" {
			return raw, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		data, readErr := os.ReadFile(path)
		if readErr != nil || strings.TrimSpace(string(data)) == "" {
			return "", fmt.Errorf("unreadable %s", path)
		}
		return strings.TrimSpace(string(data)), nil
	}
	if err != nil {
		return "", err
	}
	raw := hex.EncodeToString(buf)
	_, writeErr := handle.WriteString(raw + "\n")
	if closeErr := handle.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return "", writeErr
	}
	return raw, nil
}

// Identity is this host as seen from one state home: its stable ID, its
// current hostname, and the legacy hostname values that provably name it there.
type Identity struct {
	ID       string
	Hostname string
	aliases  []string
}

// Local resolves this host's identity for the state home at home without
// writing anything.
//
// A legacy hostname names this host only when this host observed itself using
// it: it is the current hostname, or Record stored it under this ID in this
// home. A hostname already recorded under a different ID belongs to that host,
// which keeps a cloned machine that kept its hostname from adopting the
// original's legacy records. The table is excluded from backups, and entries
// keyed by another host's ID confer nothing here.
func Local(home string) (Identity, error) {
	id, err := ID()
	if err != nil {
		return Identity{}, fmt.Errorf("identify machine: %w", err)
	}
	hostname, err := Hostname()
	if err != nil {
		return Identity{}, fmt.Errorf("identify machine: %w", err)
	}
	table, err := readTable(home)
	if err != nil {
		return Identity{}, err
	}
	identity := Identity{ID: id, Hostname: hostname}
	for _, name := range append(aliasesOf(table, id), hostname) {
		if name != "" && !containsString(identity.aliases, name) && !claimedElsewhere(table, id, name) {
			identity.aliases = append(identity.aliases, name)
		}
	}
	return identity, nil
}

// Record stores the current hostname under this host's ID in the state home at
// home, so records written under that name before the stable identity existed
// keep resolving after later renames. It writes only into an initialised state
// home, under its own lock so it never contends with the store lock a caller
// may already hold, and never claims a name recorded under another ID.
func Record(home string) error {
	if info, err := os.Stat(filepath.Join(home, "state.json")); err != nil || info.IsDir() {
		return nil
	}
	identity, err := Local(home)
	if err != nil {
		return err
	}
	if !containsString(identity.aliases, identity.Hostname) {
		return nil
	}
	handle, err := os.OpenFile(filepath.Join(home, ".machine.lock"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
	table, err := readTable(home)
	if err != nil {
		return err
	}
	names := aliasesOf(table, identity.ID)
	if containsString(names, identity.Hostname) || claimedElsewhere(table, identity.ID, identity.Hostname) {
		return nil
	}
	list := make([]any, 0, len(names)+1)
	for _, name := range names {
		list = append(list, name)
	}
	table.Set(identity.ID, append(list, identity.Hostname))
	doc := ordjson.NewObject()
	doc.Set("schema", json.Number("1"))
	doc.Set("aliases", table)
	return ordjson.WriteFile(filepath.Join(home, File), doc)
}

// Is reports whether a recorded machine value names this host.
func (m Identity) Is(recorded any) bool {
	value, _ := recorded.(string)
	return value != "" && m.Canonical(value) == m.ID
}

// Canonical maps a legacy hostname that provably names this host to the stable
// ID and leaves every other value unchanged.
func (m Identity) Canonical(recorded any) string {
	value, _ := recorded.(string)
	if containsString(m.aliases, value) {
		return m.ID
	}
	return value
}

// Same reports whether two recorded machine values name the same host.
func (m Identity) Same(a, b any) bool {
	return m.Canonical(a) == m.Canonical(b)
}

// SameEndpoint reports whether two recorded endpoints name the same pane: the
// same machine, Herdr session, and pane ID.
func (m Identity) SameEndpoint(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return false
	}
	field := func(o *ordjson.Object, key string) string {
		v, _ := o.Get(key)
		s, _ := v.(string)
		return s
	}
	return m.Same(field(a, "machine"), field(b, "machine")) &&
		field(a, "session") == field(b, "session") &&
		field(a, "pane") == field(b, "pane")
}

// Legacy lists the hostname values that name this host in records written
// before the stable identity, for lookups keyed by the machine value.
func (m Identity) Legacy() []string {
	return append([]string(nil), m.aliases...)
}

func readTable(home string) (*ordjson.Object, error) {
	path := filepath.Join(home, File)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return ordjson.NewObject(), nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", path)
	}
	aliases, _ := obj.Get("aliases")
	table, ok := aliases.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("%s has no aliases object", path)
	}
	return table, nil
}

func aliasesOf(table *ordjson.Object, id string) []string {
	raw, _ := table.Get(id)
	list, _ := raw.([]any)
	names := make([]string, 0, len(list))
	for _, item := range list {
		if name, ok := item.(string); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}

func claimedElsewhere(table *ordjson.Object, id, name string) bool {
	for _, other := range table.Keys() {
		if other != id && containsString(aliasesOf(table, other), name) {
			return true
		}
	}
	return false
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
