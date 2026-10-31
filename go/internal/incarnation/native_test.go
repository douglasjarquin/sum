package incarnation

import (
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"testing"
)

func TestNativeAuthorityNeedsMatchingReceiptAndForeground(t *testing.T) {
	pid := uint32(41)
	e := hcclient.Endpoint{Schema: 2, Backend: "hc", Machine: "local", CoreID: "core-a", Address: "t7", Task: "task", Role: "worker", Attempt: "x-one", BindingGeneration: 1, CreatedMS: 100}
	terminal := hcclient.Terminal{Address: "t7", State: "live", CreatedMS: 100, PID: &pid, Foreground: &hcclient.Foreground{PID: 42, Name: "codex", Argv: []string{"codex"}, Cwd: "/task"}}
	receipt := NativeReceipt{Endpoint: e, Kind: "worker-init", Attempt: e.Attempt}
	recorded := NativeEvidence{Endpoint: e, PID: pid, Foreground: terminal.Foreground}.Record("2026-10-30T00:00:00Z")
	if verdict := JudgeNative(recorded, e, terminal, &receipt); !verdict.Verified {
		t.Fatal(verdict)
	}
	if verdict := JudgeNative(recorded, e, terminal, nil); verdict.Verified {
		t.Fatal("missing receipt authorized")
	}
	other := e
	other.CoreID = "core-b"
	if JudgeNative(recorded, other, terminal, &receipt).Verified {
		t.Fatal("same address on another core authorized")
	}
	other = e
	other.Attempt = "x-new"
	if JudgeNative(recorded, other, terminal, &receipt).Verified {
		t.Fatal("changed attempt authorized")
	}
	other = e
	other.CreatedMS++
	if JudgeNative(recorded, other, terminal, &receipt).Verified {
		t.Fatal("changed creation inherited authority")
	}
	renewed := terminal
	renewed.CreatedMS = other.CreatedMS
	renewedRecord := NativeEvidence{Endpoint: other, PID: pid, Foreground: renewed.Foreground}.Record("later")
	if JudgeNative(renewedRecord, other, renewed, &receipt).Verified {
		t.Fatal("stale creation receipt authorized renewed evidence")
	}
	terminal.Foreground = &hcclient.Foreground{PID: 99, Name: "zsh", Cwd: "/task", Argv: []string{"zsh"}}
	if JudgeNative(recorded, e, terminal, &receipt).Verified {
		t.Fatal("kept replacement shell authorized")
	}
}
func TestNativeTerminalWithoutInitIsNotLegacyVerified(t *testing.T) {
	e := hcclient.Endpoint{Schema: 2, Backend: "hc", CoreID: "a", Address: "t1"}
	v := JudgeNative(nil, e, hcclient.Terminal{Address: "t1", State: "live"}, nil)
	if v.Verified || v.Outcome != Unrecorded {
		t.Fatal(v)
	}
}
