package plugins

import (
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

// A plugin runtime process (the driver a plugin's manifest points at, e.g.
// attn-pi's bundled runtime) is a daemon child with the same failure mode as a
// conversation host: a daemon that dies without running its shutdown — SIGKILL,
// a crash — leaves it running, reparented to init, findable through nothing but
// a durable record. The supervisor writes a procreap entry per spawned process;
// `attn profile clean` reaps from it before removing the data dir.

// RuntimeRegistryDir is where a data dir keeps its plugin runtime process
// records. Distinct from PluginDir (<dataDir>/plugins), which holds installed
// plugin code.
func RuntimeRegistryDir(dataDir string) string {
	return filepath.Join(dataDir, "plugin-runtime", "registry")
}

// runtimeTerminationGrace is how long a driver gets to exit after SIGTERM
// before the reap escalates to SIGKILL. Drivers are bun/executable processes
// whose default SIGTERM disposition is to exit immediately (the attn-pi runtime
// carries no TERM handler); 3s is a tripwire far past that, matching the
// conversation-host grace.
const runtimeTerminationGrace = 3 * time.Second

// ReapRuntimeProcesses shuts down every plugin runtime process registered under
// a profile's data dir and returns one result per registry entry. Callers that
// are about to remove a data dir must reap first — deleting the registry
// strands any process still alive.
func ReapRuntimeProcesses(dataDir string) []procreap.ReapResult {
	return procreap.ReapDir(RuntimeRegistryDir(dataDir), runtimeTerminationGrace)
}
