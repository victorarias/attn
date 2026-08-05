package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// resolveSpawnParent decides the satellite link a newly spawned shell carries:
// the agent session it was split from.
//
// The link is always an agent, never another shell. Splitting a shell off a
// shell resolves to that shell's own agent, so a satellite is a sibling of the
// agent rather than a descendant of the terminal it happened to be opened
// beside — which matches the geometry, where both shells land in the same
// workspace. Storing the immediate base instead would build chains that have to
// be walked at read time and that break in the middle when one link closes.
//
// It resolves to nothing — an ordinary workspace-scoped shell — when there is no
// base, when the base is gone, or when the resolved agent lives in a different
// workspace than the shell being spawned. That last case is the ⌘N terminal
// aimed somewhere else: it is not beside the agent, so it is not its satellite.
//
// Only shells get a parent. An agent session is never a satellite of anything.
func (d *Daemon) resolveSpawnParent(spawnedFrom, workspaceID string, isShell bool) string {
	if d == nil || d.store == nil || !isShell {
		return ""
	}
	baseID := strings.TrimSpace(spawnedFrom)
	if baseID == "" {
		return ""
	}
	base := d.store.Get(baseID)
	if base == nil {
		return ""
	}
	parentID := base.ID
	if string(base.Agent) == protocol.AgentShellValue {
		parentID = strings.TrimSpace(protocol.Deref(base.ParentSessionID))
		if parentID == "" {
			return ""
		}
	}
	parent := d.store.Get(parentID)
	if parent == nil {
		return ""
	}
	if strings.TrimSpace(parent.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return ""
	}
	return parent.ID
}
