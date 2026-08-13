package client

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/protocol"
)

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

// AppLogs reads the shared runtime's captured output, filtered to one app. The
// name `runtime` means the whole log, which is where a startup failure appears.
func (c *Client) AppLogs(name string, lines int) (*protocol.AppLogsResult, error) {
	msg := protocol.AppLogsMessage{Cmd: protocol.CmdAppLogs, Name: name}
	if lines > 0 {
		msg.Lines = protocol.Ptr(lines)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.AppLogsResult, nil
}

func (c *Client) AppRuntimeStatus() (*protocol.AppRuntimeStatusResult, error) {
	resp, err := c.send(protocol.AppRuntimeStatusMessage{Cmd: protocol.CmdAppRuntimeStatus})
	if err != nil {
		return nil, err
	}
	return resp.AppRuntimeStatusResult, nil
}

func (c *Client) AppRuntimeRestart() (*protocol.AppRuntimeRestartResult, error) {
	resp, err := c.send(protocol.AppRuntimeRestartMessage{Cmd: protocol.CmdAppRuntimeRestart})
	if err != nil {
		return nil, err
	}
	return resp.AppRuntimeRestartResult, nil
}

// AppWatch streams an app's invocations until onInvocation returns false, the
// daemon closes the connection, or stop is closed.
//
// It keeps its own connection rather than going through send: this is the one
// app command whose answer is a stream, and `attn app dev` runs it beside a
// filesystem watcher for as long as the developer is editing.
func (c *Client) AppWatch(name string, stop <-chan struct{}, onInvocation func(protocol.AppInvocationInfo) bool) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return explainConnectError(c.socketPath, err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(protocol.AppWatchMessage{Cmd: protocol.CmdAppWatch, Name: name}); err != nil {
		return fmt.Errorf("send app_watch: %w", err)
	}
	// Closing the connection is what unblocks the decode below; there is no
	// cancellable read on a net.Conn short of a deadline nobody would pick.
	if stop != nil {
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-stop:
				_ = conn.Close()
			case <-done:
			}
		}()
	}

	decoder := json.NewDecoder(conn)
	for {
		var resp protocol.Response
		if err := decoder.Decode(&resp); err != nil {
			return nil
		}
		if !resp.Ok {
			return fmt.Errorf("%s", protocol.Deref(resp.Error))
		}
		if resp.AppWatchResult == nil {
			continue
		}
		if !onInvocation(resp.AppWatchResult.Invocation) {
			return nil
		}
	}
}
