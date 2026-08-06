package hostsession

import (
	"path/filepath"

	"github.com/victorarias/attn/internal/procreap"
)

// A host's durable record lives in a procreap registry under the data dir (see
// internal/procreap). The manager's in-memory map is authoritative while the
// daemon lives; the registry exists for when it does not. A daemon that dies
// without running its shutdown — SIGKILL, a crash, a power cut — leaves its
// hosts running, reparented to init, findable through nothing but this record.
// `attn profile clean` reaps from it exactly as it reaps pty-workers from
// theirs, because deleting the data dir destroys the only way those processes
// will ever be found.

// RegistryDir is where a data dir keeps its host records.
func RegistryDir(dataDir string) string {
	return filepath.Join(dataDir, "hosts", "registry")
}

// RegistryPath is where a session's host record lives under a data dir.
func RegistryPath(dataDir, sessionID string) string {
	return filepath.Join(RegistryDir(dataDir), sessionID+".json")
}

// ReapDataDir shuts down every host registered under a profile's data dir and
// returns one result per registry entry.
//
// A host never outlives its daemon on purpose — daemon shutdown kills them all
// — so a live entry here means the daemon died without cleaning up. Teardown
// is cooperative first: SIGTERM, which the host answers by running pi's
// dispose — the only path that reaches its tool subprocesses, which lead their
// own process groups. After terminationGrace the host's group is SIGKILLed as
// a backstop. Callers that are about to remove a data dir must reap first.
func ReapDataDir(dataDir string) []procreap.ReapResult {
	return procreap.ReapDir(RegistryDir(dataDir), terminationGrace)
}
