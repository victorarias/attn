package client

import "github.com/victorarias/attn/internal/protocol"

// The app registry's client surface. Everything goes through the daemon, like
// the document store's and unlike `attn bus`: removing an app has to stop a
// delivery loop before deleting its row, and flipping the enabled bit publishes
// a fact the runtime hears.
//
// The daemon-independent path still exists, deliberately, one level down: an
// app's enabled bit is its bus consumer's, so `attn bus disable app:<name>` is
// the kill switch that works when the daemon does not.

func (c *Client) AppList() (*protocol.AppListResult, error) {
	resp, err := c.send(protocol.AppListMessage{Cmd: protocol.CmdAppList})
	if err != nil {
		return nil, err
	}
	return resp.AppListResult, nil
}

func (c *Client) AppStatus(name string) (*protocol.AppStatusResult, error) {
	resp, err := c.send(protocol.AppStatusMessage{Cmd: protocol.CmdAppStatus, Name: name})
	if err != nil {
		return nil, err
	}
	return resp.AppStatusResult, nil
}

func (c *Client) AppSetEnabled(name string, enabled bool) (*protocol.AppSetEnabledResult, error) {
	resp, err := c.send(protocol.AppSetEnabledMessage{
		Cmd: protocol.CmdAppSetEnabled, Name: name, Enabled: enabled,
	})
	if err != nil {
		return nil, err
	}
	return resp.AppSetEnabledResult, nil
}

func (c *Client) AppRemove(name string) (*protocol.AppRemoveResult, error) {
	resp, err := c.send(protocol.AppRemoveMessage{Cmd: protocol.CmdAppRemove, Name: name})
	if err != nil {
		return nil, err
	}
	return resp.AppRemoveResult, nil
}

// AppApply records a version the caller has already built and points the app at
// it. The build is the caller's — see internal/appbuild — and this is the one
// step of an apply that changes anything.
func (c *Client) AppApply(name, contentHash, declaration, sourcePath string) (*protocol.AppApplyResult, error) {
	msg := protocol.AppApplyMessage{
		Cmd:         protocol.CmdAppApply,
		Name:        name,
		ContentHash: contentHash,
		Declaration: declaration,
	}
	if sourcePath != "" {
		msg.SourcePath = protocol.Ptr(sourcePath)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.AppApplyResult, nil
}

// AppRollback moves an app onto a version it already has. A zero versionID means
// the version applied before the current one.
func (c *Client) AppRollback(name string, versionID int) (*protocol.AppRollbackResult, error) {
	msg := protocol.AppRollbackMessage{Cmd: protocol.CmdAppRollback, Name: name}
	if versionID > 0 {
		msg.VersionID = protocol.Ptr(versionID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.AppRollbackResult, nil
}
