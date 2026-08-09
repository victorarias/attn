package hostsession

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/procreap"
)

// The registry is the crash-recovery net: it must exist exactly while the
// process might, so a record appears at spawn and disappears once the host is
// fully gone. The reap semantics themselves are procreap's to test; this
// covers the manager's side of the contract.
func TestSpawnWritesAndExitRemovesTheRegistryEntry(t *testing.T) {
	manager, rec := newManager(t)
	dataDir := t.TempDir()
	registryPath := RegistryPath(dataDir, "s1")
	script := writeScript(t, `
echo '{"session_id":"s1","seq":1,"kind":"session_ready","body":{}}' >&3
while true; do sleep 0.05; done
`)

	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}, RegistryPath: registryPath}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	entry, err := procreap.ReadEntry(registryPath)
	if err != nil {
		t.Fatalf("registry entry not written at spawn: %v", err)
	}
	if entry.ID != "s1" || entry.PID <= 0 {
		t.Fatalf("registry entry is incomplete: %+v", entry)
	}
	if entry.PGID != entry.PID {
		t.Fatalf("host should lead its own process group, entry says pgid %d for pid %d", entry.PGID, entry.PID)
	}
	if entry.ProcessStartTime == "" {
		t.Fatalf("registry entry carries no process start time: %+v", entry)
	}

	if err := manager.Kill("s1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitForExit(t, rec)
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry entry survived the host's exit: %v", err)
	}
}
