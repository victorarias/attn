package daemon

import "github.com/victorarias/attn/internal/config"

// spawnRoutingEnv is the daemon's exact filesystem and endpoint identity. It
// is carried through both PTY runtimes and conversation hosts, then applied as
// the final environment overlay in the child.
func (d *Daemon) spawnRoutingEnv() []string {
	return []string{
		"ATTN_PROFILE=" + config.Profile(),
		"ATTN_DATA_DIR=" + d.dataRoot,
		"ATTN_DB_PATH=" + config.DBPath(),
		"ATTN_SOCKET_PATH=" + d.socketPath,
		"ATTN_WS_PORT=" + config.WSPort(),
		"ATTN_CONFIG_PATH=" + config.ConfigPath(),
		"ATTN_PLUGIN_DIR=" + d.pluginDir,
	}
}
