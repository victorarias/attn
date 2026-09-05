package daemon

import (
	"encoding/json"
	"net"

	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleDelegationRoles(conn net.Conn) {
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result := delegationprefs.Active(cfg)
	if len(result.Roles) > 0 || result.Fallback != nil {
		result.Guidance = prompts.DelegationRoutingGuidance(result.Revision)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DelegationRoles: &result})
}
