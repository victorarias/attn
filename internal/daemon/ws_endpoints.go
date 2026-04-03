package daemon

import (
	"database/sql"
	"errors"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleListEndpointsWS(client *wsClient) {
	d.sendToClient(client, &protocol.EndpointsUpdatedMessage{
		Event:     protocol.EventEndpointsUpdated,
		Endpoints: d.listEndpointInfos(),
	})
}

func (d *Daemon) handleAddEndpointWS(client *wsClient, msg *protocol.AddEndpointMessage) {
	if d.hubManager == nil {
		d.sendEndpointActionResult(client, "add", "", false, "endpoint manager unavailable")
		return
	}
	record, err := d.hubManager.AddEndpoint(msg.Name, msg.SshTarget)
	if err != nil {
		d.sendEndpointActionResult(client, "add", "", false, err.Error())
		return
	}
	d.broadcastEndpointsUpdated()
	d.sendEndpointActionResult(client, "add", record.ID, true, "")
}

func (d *Daemon) handleRemoveEndpointWS(client *wsClient, msg *protocol.RemoveEndpointMessage) {
	if d.hubManager == nil {
		d.sendEndpointActionResult(client, "remove", msg.EndpointID, false, "endpoint manager unavailable")
		return
	}
	if err := d.hubManager.RemoveEndpoint(msg.EndpointID); err != nil {
		d.sendEndpointActionResult(client, "remove", msg.EndpointID, false, err.Error())
		return
	}
	d.broadcastEndpointsUpdated()
	d.sendEndpointActionResult(client, "remove", msg.EndpointID, true, "")
}

func (d *Daemon) handleUpdateEndpointWS(client *wsClient, msg *protocol.UpdateEndpointMessage) {
	if d.hubManager == nil {
		d.sendEndpointActionResult(client, "update", msg.EndpointID, false, "endpoint manager unavailable")
		return
	}

	update := store.EndpointUpdate{
		Name:      msg.Name,
		SSHTarget: msg.SshTarget,
		Enabled:   msg.Enabled,
	}
	record, err := d.hubManager.UpdateEndpoint(msg.EndpointID, update)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			d.sendEndpointActionResult(client, "update", msg.EndpointID, false, "endpoint not found")
			return
		}
		d.sendEndpointActionResult(client, "update", msg.EndpointID, false, err.Error())
		return
	}
	d.broadcastEndpointsUpdated()
	d.sendEndpointActionResult(client, "update", record.ID, true, "")
}

func (d *Daemon) sendEndpointActionResult(client *wsClient, action, endpointID string, success bool, errMsg string) {
	result := &protocol.EndpointActionResultMessage{
		Event:   protocol.EventEndpointActionResult,
		Action:  action,
		Success: success,
	}
	if endpointID != "" {
		result.EndpointID = protocol.Ptr(endpointID)
	}
	if errMsg != "" {
		result.Error = protocol.Ptr(errMsg)
	}
	d.sendToClient(client, result)
}
