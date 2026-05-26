package daemon

import (
	"encoding/json"
	"testing"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
)

func TestTryHandleRemoteWSCommand_BootstrapWorkspaceReservesInitialSessionPTYRoute(t *testing.T) {
	d := newDaemonForTest(t)
	endpoint, err := d.store.AddEndpoint("remote-host", "ssh-target", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil)

	msg := &protocol.BootstrapWorkspaceMessage{
		Cmd:        protocol.CmdBootstrapWorkspace,
		ID:         "remote-workspace",
		EndpointID: protocol.Ptr(endpoint.ID),
		InitialSession: protocol.BootstrapWorkspaceInitialSession{
			ID: "remote-initial-session",
		},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	client := &wsClient{send: make(chan outboundMessage, 1)}

	if !d.tryHandleRemoteWSCommand(client, protocol.CmdBootstrapWorkspace, msg, raw) {
		t.Fatal("remote bootstrap was not handled by endpoint routing")
	}
	if endpointID, ok := d.hubManager.EndpointIDForPTYTarget(msg.InitialSession.ID); !ok || endpointID != endpoint.ID {
		t.Fatalf("EndpointIDForPTYTarget(initial session) = (%q, %v), want (%q, true)", endpointID, ok, endpoint.ID)
	}
}
