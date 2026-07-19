package ptyworker

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/launchcontract"
)

func TestWriteAndReadRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry", "sess-1.json")
	entry := NewRegistryEntry("d-123", "sess-1", 111, 222, "/tmp/sock", "shell", "/tmp", "token")
	entry.OwnerPID = 333
	entry.OwnerStartedAt = "2026-02-11T00:00:00Z"
	entry.OwnerNonce = "nonce-123"
	entry.UnattendedLaunch = launchcontract.UnattendedLaunchSpec{
		Agent: "codex", Model: "gpt-test", Effort: "high",
		ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAutoReview,
		DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
	}
	if err := WriteRegistryAtomic(path, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}
	got, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("ReadRegistry() error: %v", err)
	}
	if got.SessionID != entry.SessionID {
		t.Fatalf("session_id = %q, want %q", got.SessionID, entry.SessionID)
	}
	if got.DaemonInstanceID != entry.DaemonInstanceID {
		t.Fatalf("daemon_instance_id = %q, want %q", got.DaemonInstanceID, entry.DaemonInstanceID)
	}
	if got.ControlToken != entry.ControlToken {
		t.Fatalf("control_token = %q, want %q", got.ControlToken, entry.ControlToken)
	}
	if got.OwnerPID != entry.OwnerPID {
		t.Fatalf("owner_pid = %d, want %d", got.OwnerPID, entry.OwnerPID)
	}
	if got.OwnerStartedAt != entry.OwnerStartedAt {
		t.Fatalf("owner_started_at = %q, want %q", got.OwnerStartedAt, entry.OwnerStartedAt)
	}
	if got.OwnerNonce != entry.OwnerNonce {
		t.Fatalf("owner_nonce = %q, want %q", got.OwnerNonce, entry.OwnerNonce)
	}
	if !reflect.DeepEqual(got.UnattendedLaunch, entry.UnattendedLaunch) {
		t.Fatalf("unattended launch = %#v, want %#v", got.UnattendedLaunch, entry.UnattendedLaunch)
	}
}
