package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type StatusCallback func(info protocol.EndpointInfo)
type SessionsChangedCallback func()
type RawEventCallback func(data []byte)

type VersionMismatchError struct {
	RemoteVersion string
	LocalVersion  string
}

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("protocol mismatch: remote=%s local=%s", e.RemoteVersion, e.LocalVersion)
}

// BinaryMismatchError is returned by consumeRemote when the connection closes
// while in binary_mismatch state, so the caller can apply the same slow-retry
// policy as VersionMismatchError instead of the normal fast reconnect.
type BinaryMismatchError struct {
	Message string
}

func (e *BinaryMismatchError) Error() string { return e.Message }

const (
	settingProjectsDirectory = "projects_directory"
	settingPTYBackendMode    = "pty_backend_mode"
	settingTailscaleEnabled  = "tailscale_enabled"
	settingTailscaleStatus   = "tailscale_status"
	settingTailscaleURL      = "tailscale_url"
	settingTailscaleDomain   = "tailscale_domain"
	settingTailscaleAuthURL  = "tailscale_auth_url"
	settingTailscaleError    = "tailscale_error"
)

type endpointRuntime struct {
	record store.EndpointRecord
	info   protocol.EndpointInfo

	cancel           context.CancelFunc
	conn             *websocket.Conn
	cmd              *exec.Cmd
	writeMu          sync.Mutex
	pendingRemoteWeb *pendingRemoteWebAction
	pendingBootstrap bool

	sessions   map[string]protocol.Session
	workspaces map[string]protocol.Workspace
}

type pendingRemoteWebAction struct {
	desiredEnabled bool
	done           chan error
}

type pendingSessionRoute struct {
	endpointID string
	expiresAt  time.Time
}

type browserControlResult struct {
	data string
	err  error
}

type pendingBrowserControl struct {
	endpointID string
	done       chan browserControlResult
}

type Manager struct {
	store        *store.Store
	bootstrapper *Bootstrapper
	onStatus     StatusCallback
	onSessions   SessionsChangedCallback
	onRawEvent   RawEventCallback
	logf         func(format string, args ...interface{})

	mu              sync.RWMutex
	runtimes        map[string]*endpointRuntime
	pending         map[string]pendingSessionRoute
	browserControls map[string]pendingBrowserControl
	ctx             context.Context
	cancel          context.CancelFunc
	started         bool
}

func NewManager(
	endpointStore *store.Store,
	onStatus StatusCallback,
	onSessions SessionsChangedCallback,
	onRawEvent RawEventCallback,
	logf func(format string, args ...interface{}),
) *Manager {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	m := &Manager{
		store:           endpointStore,
		bootstrapper:    NewBootstrapper(logf),
		onStatus:        onStatus,
		onSessions:      onSessions,
		onRawEvent:      onRawEvent,
		logf:            logf,
		runtimes:        make(map[string]*endpointRuntime),
		pending:         make(map[string]pendingSessionRoute),
		browserControls: make(map[string]pendingBrowserControl),
	}
	for _, record := range endpointStore.ListEndpoints() {
		m.runtimes[record.ID] = &endpointRuntime{
			record:     record,
			info:       infoFromRecord(record),
			sessions:   make(map[string]protocol.Session),
			workspaces: make(map[string]protocol.Workspace),
		}
	}
	return m
}

func infoFromRecord(record store.EndpointRecord) protocol.EndpointInfo {
	info := protocol.EndpointInfo{
		ID:        record.ID,
		Name:      record.Name,
		SshTarget: record.SSHTarget,
		Status:    "disconnected",
		Enabled:   protocol.Ptr(record.Enabled),
	}
	if strings.TrimSpace(record.Profile) != "" {
		info.Profile = protocol.Ptr(record.Profile)
	}
	return info
}

func (m *Manager) Start(parent context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return
	}
	m.ctx, m.cancel = context.WithCancel(parent)
	m.started = true
	for id, runtime := range m.runtimes {
		if runtime.record.Enabled {
			m.startRuntimeLocked(id)
		}
	}
}

func (m *Manager) Stop() {
	changed := false
	shutdownTargets := make([]isolatedShutdownTarget, 0)
	seenTargets := make(map[string]struct{})
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	for _, runtime := range m.runtimes {
		changed = changed || len(runtime.sessions) > 0
		if target := isolatedRemoteShutdownTarget(runtime.record); target.Target != "" {
			key := target.Target + "|" + target.Profile
			if _, exists := seenTargets[key]; !exists {
				seenTargets[key] = struct{}{}
				shutdownTargets = append(shutdownTargets, target)
			}
		}
		m.stopRuntimeLocked(runtime)
	}
	m.started = false
	m.mu.Unlock()
	m.stopIsolatedRemoteDaemons(shutdownTargets)
	if changed {
		go m.publishSessionsChanged()
	}
}

func (m *Manager) List() []protocol.EndpointInfo {
	records := m.store.ListEndpoints()
	out := make([]protocol.EndpointInfo, 0, len(records))

	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, record := range records {
		info := infoFromRecord(record)
		if runtime, ok := m.runtimes[record.ID]; ok {
			info = runtime.info
			info.Name = record.Name
			info.SshTarget = record.SSHTarget
			info.Enabled = protocol.Ptr(record.Enabled)
			if strings.TrimSpace(record.Profile) != "" {
				info.Profile = protocol.Ptr(record.Profile)
			} else {
				info.Profile = nil
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *Manager) AddEndpoint(name, sshTarget, profile string) (*store.EndpointRecord, error) {
	name = strings.TrimSpace(name)
	sshTarget = strings.TrimSpace(sshTarget)
	profile = strings.TrimSpace(profile)
	if name == "" {
		return nil, fmt.Errorf("endpoint name is required")
	}
	if sshTarget == "" {
		return nil, fmt.Errorf("ssh target is required")
	}

	record, err := m.store.AddEndpoint(name, sshTarget, profile)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.runtimes[record.ID] = &endpointRuntime{
		record:     *record,
		info:       infoFromRecord(*record),
		sessions:   make(map[string]protocol.Session),
		workspaces: make(map[string]protocol.Workspace),
	}
	if m.started && record.Enabled {
		m.startRuntimeLocked(record.ID)
	}
	m.mu.Unlock()

	m.publishStatus(record.ID)
	return record, nil
}

// BootstrapEndpoint requests an explicit bootstrap (install binary + start daemon) for the
// given endpoint.  The flag is consumed at the top of runEndpointLoop so it fires once on
// the next loop iteration.  Upgrade must be intentional — this is the only path that calls
// EnsureRemoteReady when the remote daemon is already running.
func (m *Manager) BootstrapEndpoint(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, ok := m.runtimes[id]
	if !ok {
		return fmt.Errorf("endpoint %s not found", id)
	}
	rt.pendingBootstrap = true
	m.stopRuntimeLocked(rt)
	if m.started && rt.record.Enabled {
		m.startRuntimeLocked(id)
	}
	return nil
}

func (m *Manager) consumeBootstrapFlag(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, ok := m.runtimes[id]
	if !ok {
		return false
	}
	v := rt.pendingBootstrap
	rt.pendingBootstrap = false
	return v
}

func (m *Manager) UpdateEndpoint(id string, update store.EndpointUpdate) (*store.EndpointRecord, error) {
	record, err := m.store.UpdateEndpoint(id, update)
	if err != nil {
		return nil, err
	}

	changed := false
	m.mu.Lock()
	runtime, ok := m.runtimes[id]
	if !ok {
		runtime = &endpointRuntime{
			sessions:   make(map[string]protocol.Session),
			workspaces: make(map[string]protocol.Workspace),
		}
		m.runtimes[id] = runtime
	}
	runtime.record = *record
	info := runtime.info
	info.ID = record.ID
	info.Name = record.Name
	info.SshTarget = record.SSHTarget
	info.Enabled = protocol.Ptr(record.Enabled)
	if strings.TrimSpace(record.Profile) != "" {
		info.Profile = protocol.Ptr(record.Profile)
	} else {
		info.Profile = nil
	}
	if info.Status == "" {
		info.Status = "disconnected"
	}
	runtime.info = info
	changed = len(runtime.sessions) > 0
	m.stopRuntimeLocked(runtime)
	if m.started && record.Enabled {
		m.startRuntimeLocked(id)
	}
	m.mu.Unlock()

	if changed {
		m.publishSessionsChanged()
	}
	m.publishStatus(id)
	return record, nil
}

func (m *Manager) RemoveEndpoint(id string) error {
	changed := false
	var shutdownTarget isolatedShutdownTarget
	m.mu.Lock()
	if runtime, ok := m.runtimes[id]; ok {
		changed = len(runtime.sessions) > 0
		shutdownTarget = isolatedRemoteShutdownTarget(runtime.record)
		m.stopRuntimeLocked(runtime)
		delete(m.runtimes, id)
	}
	m.mu.Unlock()
	if shutdownTarget.Target != "" {
		m.stopIsolatedRemoteDaemons([]isolatedShutdownTarget{shutdownTarget})
	}
	if changed {
		m.publishSessionsChanged()
	}
	return m.store.RemoveEndpoint(id)
}

type isolatedShutdownTarget struct {
	Target  string
	Profile string
}

func isolatedRemoteShutdownTarget(record store.EndpointRecord) isolatedShutdownTarget {
	if !remoteHarnessCleanupEnabled() {
		return isolatedShutdownTarget{}
	}
	target := strings.TrimSpace(record.SSHTarget)
	if target == "" {
		return isolatedShutdownTarget{}
	}
	return isolatedShutdownTarget{Target: target, Profile: strings.TrimSpace(record.Profile)}
}

func (m *Manager) stopIsolatedRemoteDaemons(targets []isolatedShutdownTarget) {
	if m == nil || m.bootstrapper == nil || len(targets) == 0 || !remoteHarnessCleanupEnabled() {
		return
	}
	for _, target := range targets {
		if target.Target == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := m.bootstrapper.StopRemoteDaemon(ctx, target.Target, target.Profile)
		cancel()
		if err != nil {
			m.logf("remote harness daemon cleanup failed for %s: %v", target.Target, err)
		}
	}
}

func (m *Manager) startRuntimeLocked(id string) {
	runtime, ok := m.runtimes[id]
	if !ok || !runtime.record.Enabled || m.ctx == nil {
		return
	}
	if runtime.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	runtime.cancel = cancel
	go m.runEndpointLoop(ctx, id)
}

func (m *Manager) stopRuntimeLocked(runtime *endpointRuntime) {
	if runtime == nil {
		return
	}
	if runtime.pendingRemoteWeb != nil {
		select {
		case runtime.pendingRemoteWeb.done <- fmt.Errorf("endpoint disconnected"):
		default:
		}
		runtime.pendingRemoteWeb = nil
	}
	if runtime.cancel != nil {
		runtime.cancel()
		runtime.cancel = nil
	}
	if runtime.conn != nil {
		_ = runtime.conn.Close(websocket.StatusNormalClosure, "")
		runtime.conn = nil
	}
	if runtime.cmd != nil {
		killAndReap(runtime.cmd)
		runtime.cmd = nil
	}
	runtime.sessions = make(map[string]protocol.Session)
	runtime.workspaces = make(map[string]protocol.Workspace)
	m.clearPendingRoutesLocked(runtime.record.ID)
	zero := 0
	runtime.info.SessionCount = protocol.Ptr(zero)
}

func (m *Manager) runEndpointLoop(ctx context.Context, id string) {
	backoff := time.Second
	for {
		record, ok := m.recordFor(id)
		if !ok {
			return
		}

		// Explicit bootstrap requested (e.g. user clicked Sync) — install binary and start daemon.
		if m.consumeBootstrapFlag(id) {
			m.updateStatus(id, "bootstrapping", "Installing remote binary", nil, nil)
			bootCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			err := m.bootstrapper.EnsureRemoteReady(bootCtx, record.SSHTarget, record.Profile)
			cancel()
			if err != nil {
				m.updateStatus(id, "error", err.Error(), nil, nil)
				if !sleepOrDone(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
		}

		// Try to connect; if it fails the daemon is not running — bootstrap and retry.
		m.updateStatus(id, "connecting", "Connecting to remote daemon", nil, nil)
		conn, cmd, err := connectViaSSH(ctx, record.SSHTarget, config.WSAuthToken(), record.Profile)
		if err != nil {
			// Daemon appears down — bootstrap then try once more.
			m.updateStatus(id, "bootstrapping", "Checking remote platform", nil, nil)
			bootCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			bootErr := m.bootstrapper.EnsureRemoteReady(bootCtx, record.SSHTarget, record.Profile)
			cancel()
			if bootErr != nil {
				m.updateStatus(id, "error", bootErr.Error(), nil, nil)
				if !sleepOrDone(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
			m.updateStatus(id, "connecting", "Connecting to remote daemon", nil, nil)
			conn, cmd, err = connectViaSSH(ctx, record.SSHTarget, config.WSAuthToken(), record.Profile)
			if err != nil {
				m.updateStatus(id, "error", err.Error(), nil, nil)
				if !sleepOrDone(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
		}

		// Declare the workspace_sessions capability before anything else: the
		// remote daemon rejects every gated command (register_workspace,
		// spawn_session, forwarded client payloads, ...) from a connection
		// that never sent client_hello, closing it with a policy violation.
		// publishConnectionAndSendHello holds writeMu across publishing the
		// connection and sending the hello so ForwardEndpointCommand can never
		// win the race and write before it.
		connected := false
		consumeErr := m.publishConnectionAndSendHello(ctx, id, conn, cmd)
		if consumeErr == nil {
			connected, consumeErr = m.consumeRemote(ctx, id, conn)
		}
		m.clearConnection(id)
		if m.clearRemoteSessions(id) {
			m.publishSessionsChanged()
		}
		m.clearRemoteWorkspaceLayouts(id)

		if ctx.Err() != nil {
			return
		}

		var versionErr *VersionMismatchError
		if errors.As(consumeErr, &versionErr) {
			// Version mismatch — surface to user instead of auto-bootstrapping.
			status, message := versionMismatchStatus(versionErr)
			m.updateStatus(id, status, message, nil, nil)
			// Sleep longer before retrying; the user needs to click Sync.
			if !sleepOrDone(ctx, 30*time.Second) {
				return
			}
			continue
		}

		var binaryErr *BinaryMismatchError
		if errors.As(consumeErr, &binaryErr) {
			// Binary mismatch — connection dropped while in mismatch state.
			// Keep the binary_mismatch status and use the same slow-retry policy
			// so the banner stays visible and the user can click Sync.
			m.updateStatus(id, "binary_mismatch", binaryErr.Message, nil, nil)
			if !sleepOrDone(ctx, 30*time.Second) {
				return
			}
			continue
		}

		if connected {
			m.updateStatus(id, "disconnected", "Disconnected; reconnecting", nil, nil)
			// Reset backoff after a successful session — it was healthy.
			backoff = time.Second
		} else if consumeErr != nil {
			m.updateStatus(id, "error", consumeErr.Error(), nil, nil)
		}
		if !sleepOrDone(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

// versionMismatchStatus returns the status string and human-readable message for a
// VersionMismatchError.  "version_mismatch" means the remote is older (safe to sync
// by upgrading it).  "version_ahead" means the remote is newer (syncing would downgrade it).
func versionMismatchStatus(e *VersionMismatchError) (string, string) {
	remoteN, remoteErr := strconv.Atoi(e.RemoteVersion)
	localN, localErr := strconv.Atoi(e.LocalVersion)
	if remoteErr == nil && localErr == nil && remoteN > localN {
		return "version_ahead", fmt.Sprintf(
			"remote v%s is ahead of this client v%s — sync will downgrade it",
			e.RemoteVersion, e.LocalVersion,
		)
	}
	return "version_mismatch", fmt.Sprintf(
		"remote v%s, this client v%s — remote needs update",
		e.RemoteVersion, e.LocalVersion,
	)
}

// sendClientHello identifies the hub connection to the remote daemon. The
// hello is fire-and-forget (the daemon never replies to it), but without the
// workspace_sessions capability it declares, the daemon rejects every gated
// command later forwarded over this connection.
func sendClientHello(ctx context.Context, conn *websocket.Conn) error {
	payload, err := json.Marshal(protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "hub",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
	})
	if err != nil {
		return fmt.Errorf("marshal client_hello: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("send client_hello: %w", err)
	}
	return nil
}

func (m *Manager) consumeRemote(ctx context.Context, id string, conn *websocket.Conn) (bool, error) {
	connected := false
	// activeStatus and activeMsg track the status established on initial_state so
	// that subsequent session events preserve it rather than blindly writing
	// "connected".  For a binary_mismatch connection the WebSocket stays alive
	// but the endpoint must remain non-"connected" throughout.
	activeStatus := "connected"
	activeMsg := "Connected"
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if activeStatus == "binary_mismatch" {
				return connected, &BinaryMismatchError{Message: activeMsg}
			}
			if connected {
				return true, err
			}
			return false, err
		}

		var peek struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(data, &peek); err != nil {
			if connected {
				continue
			}
			return false, err
		}

		switch peek.Event {
		case protocol.EventInitialState:
			var msg protocol.InitialStateMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				return connected, err
			}
			if remoteProtocol := strings.TrimSpace(protocol.Deref(msg.ProtocolVersion)); remoteProtocol != "" && remoteProtocol != protocol.ProtocolVersion {
				return false, &VersionMismatchError{RemoteVersion: remoteProtocol, LocalVersion: protocol.ProtocolVersion}
			}
			changed := m.replaceRemoteSessions(id, msg.Sessions)
			m.replaceRemoteWorkspaces(id, msg.Workspaces)
			caps := capabilitiesFromInitialState(&msg)
			sessionCount := int32(len(msg.Sessions))
			if fingerMismatch, fingerMsg := fingerprintMismatch(msg.SourceFingerprint); fingerMismatch {
				activeStatus = "binary_mismatch"
				activeMsg = fingerMsg
			}
			m.updateStatus(id, activeStatus, activeMsg, caps, &sessionCount)
			if changed {
				m.publishSessionsChanged()
			}
			connected = true
		case protocol.EventSettingsUpdated:
			var msg protocol.SettingsUpdatedMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			m.handleRemoteSettingsUpdated(id, &msg)
		case protocol.EventSessionsUpdated:
			var msg protocol.SessionsUpdatedMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			changed := m.replaceRemoteSessions(id, msg.Sessions)
			sessionCount := int32(len(msg.Sessions))
			m.updateStatus(id, activeStatus, activeMsg, nil, &sessionCount)
			if changed {
				m.publishSessionsChanged()
			}
		case protocol.EventSessionRegistered, protocol.EventSessionStateChanged, protocol.EventSessionTodosUpdated:
			var msg struct {
				Session *protocol.Session `json:"session"`
			}
			if err := json.Unmarshal(data, &msg); err != nil || msg.Session == nil {
				continue
			}
			changed, sessionCount := m.upsertRemoteSession(id, *msg.Session)
			countValue := int32(sessionCount)
			m.updateStatus(id, activeStatus, activeMsg, nil, &countValue)
			if changed {
				m.publishSessionsChanged()
			}
		case protocol.EventSessionUnregistered:
			var msg struct {
				Session *protocol.Session `json:"session"`
			}
			if err := json.Unmarshal(data, &msg); err != nil || msg.Session == nil {
				continue
			}
			changed, sessionCount := m.removeRemoteSession(id, msg.Session.ID)
			countValue := int32(sessionCount)
			m.updateStatus(id, activeStatus, activeMsg, nil, &countValue)
			if changed {
				m.publishSessionsChanged()
			}
		case protocol.EventWorkspaceLayout, protocol.EventWorkspaceLayoutUpdated:
			var msg struct {
				WorkspaceLayout *protocol.WorkspaceLayout `json:"workspace_layout"`
			}
			if err := json.Unmarshal(data, &msg); err != nil || msg.WorkspaceLayout == nil {
				continue
			}
			m.upsertRemoteWorkspaceLayout(id, *msg.WorkspaceLayout)
			m.publishRawEvent(data)
		case protocol.EventWorkspaceRegistered, protocol.EventWorkspaceStateChanged:
			var msg struct {
				Workspace *protocol.Workspace `json:"workspace"`
			}
			if err := json.Unmarshal(data, &msg); err != nil || msg.Workspace == nil {
				continue
			}
			m.upsertRemoteWorkspace(id, *msg.Workspace)
			m.publishRawEvent(data)
		case protocol.EventWorkspaceUnregistered:
			var msg struct {
				Workspace *protocol.Workspace `json:"workspace"`
			}
			if err := json.Unmarshal(data, &msg); err != nil || msg.Workspace == nil {
				continue
			}
			m.removeRemoteWorkspace(id, msg.Workspace.ID)
			m.publishRawEvent(data)
		case protocol.EventBrowserControlResponse:
			m.resolveBrowserControl(id, data)
		default:
			if forwardsRawEvent(peek.Event) {
				m.logRemoteRawEvent(id, peek.Event, data)
				m.publishRawEvent(data)
			}
		}
	}
}

func (m *Manager) logRemoteRawEvent(endpointID, event string, data []byte) {
	if m.logf == nil {
		return
	}

	switch event {
	case protocol.EventPtyOutput:
		var msg struct {
			ID   *string `json:"id"`
			Seq  *int    `json:"seq"`
			Data *string `json:"data"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			m.logf("remote raw event relay: endpoint=%s event=%s decode_err=%v", endpointID, event, err)
			return
		}
		m.logf(
			"remote pty_output relay: endpoint=%s id=%s seq=%d base64_bytes=%d",
			endpointID,
			protocol.Deref(msg.ID),
			protocol.Deref(msg.Seq),
			len(protocol.Deref(msg.Data)),
		)
	case protocol.EventAttachResult:
		var msg struct {
			ID      *string `json:"id"`
			Success bool    `json:"success"`
			LastSeq *int    `json:"last_seq"`
			Cols    *int    `json:"cols"`
			Rows    *int    `json:"rows"`
			Error   *string `json:"error"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			m.logf("remote raw event relay: endpoint=%s event=%s decode_err=%v", endpointID, event, err)
			return
		}
		m.logf(
			"remote attach_result relay: endpoint=%s id=%s success=%v last_seq=%d size=%dx%d error=%q",
			endpointID,
			protocol.Deref(msg.ID),
			msg.Success,
			protocol.Deref(msg.LastSeq),
			protocol.Deref(msg.Cols),
			protocol.Deref(msg.Rows),
			protocol.Deref(msg.Error),
		)
	}
}

func forwardsRawEvent(event string) bool {
	switch event {
	case protocol.EventBranchesResult,
		protocol.EventRecentLocationsResult,
		protocol.EventBrowseDirectoryResult,
		protocol.EventInspectPathResult,
		protocol.EventCreateWorktreeResult,
		protocol.EventDeleteWorktreeResult,
		protocol.EventGetDefaultBranchResult,
		protocol.EventFetchRemotesResult,
		protocol.EventListRemoteBranchesResult,
		protocol.EventEnsureRepoResult,
		protocol.EventGitStatusUpdate,
		protocol.EventFileDiffResult,
		protocol.EventGetRepoInfoResult,
		protocol.EventSpawnResult,
		protocol.EventAttachResult,
		protocol.EventPtyOutput,
		protocol.EventPtyDesync,
		protocol.EventSessionExited,
		protocol.EventWorkspaceLayoutActionResult,
		protocol.EventWorkspaceTileContent,
		protocol.EventMarkdownAnnotationsGetResult,
		protocol.EventMarkdownAnnotationsSaveResult,
		protocol.EventMarkdownAnnotationsClearResult,
		protocol.EventMarkdownAnnotationsSubmitResult,
		protocol.EventCommandError:
		return true
	default:
		return false
	}
}

func (m *Manager) RemoteSessions() []protocol.Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, runtime := range m.runtimes {
		total += len(runtime.sessions)
	}
	if total == 0 {
		return nil
	}

	out := make([]protocol.Session, 0, total)
	for _, runtime := range m.runtimes {
		for _, session := range runtime.sessions {
			out = append(out, session)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if protocol.Deref(out[i].EndpointID) == protocol.Deref(out[j].EndpointID) {
			if out[i].Directory == out[j].Directory {
				return out[i].ID < out[j].ID
			}
			return out[i].Directory < out[j].Directory
		}
		return protocol.Deref(out[i].EndpointID) < protocol.Deref(out[j].EndpointID)
	})
	return out
}

func (m *Manager) RemoteWorkspaces() []protocol.Workspace {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, runtime := range m.runtimes {
		total += len(runtime.workspaces)
	}
	if total == 0 {
		return nil
	}

	out := make([]protocol.Workspace, 0, total)
	for _, runtime := range m.runtimes {
		for _, workspace := range runtime.workspaces {
			out = append(out, workspace)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *Manager) RemoteWorkspace(workspaceID string) *protocol.Workspace {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, runtime := range m.runtimes {
		if workspace, ok := runtime.workspaces[workspaceID]; ok {
			copy := workspace
			if workspace.Layout != nil {
				layoutCopy := *workspace.Layout
				layoutCopy.Panes = append([]protocol.WorkspaceLayoutPane(nil), workspace.Layout.Panes...)
				copy.Layout = &layoutCopy
			}
			return &copy
		}
	}
	return nil
}

func (m *Manager) EndpointIDForSession(sessionID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for endpointID, runtime := range m.runtimes {
		if _, ok := runtime.sessions[sessionID]; ok {
			return endpointID, true
		}
	}
	if pending, ok := m.pendingSessionRouteLocked(sessionID, time.Now()); ok {
		return pending.endpointID, true
	}
	return "", false
}

func (m *Manager) EndpointIDForWorkspace(workspaceID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for endpointID, runtime := range m.runtimes {
		if _, ok := runtime.workspaces[workspaceID]; ok {
			return endpointID, true
		}
	}
	return "", false
}

func (m *Manager) RemoteSession(sessionID string) *protocol.Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, runtime := range m.runtimes {
		if session, ok := runtime.sessions[sessionID]; ok {
			copy := session
			if len(session.Todos) > 0 {
				copy.Todos = append([]string(nil), session.Todos...)
			}
			return &copy
		}
	}
	return nil
}

func (m *Manager) ForgetSession(sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	delete(m.pending, sessionID)
	for _, runtime := range m.runtimes {
		runtimeChanged := false
		if runtime.sessions != nil {
			if _, ok := runtime.sessions[sessionID]; ok {
				delete(runtime.sessions, sessionID)
				runtimeChanged = true
				changed = true
			}
		}
		if runtimeChanged {
			count := len(runtime.sessions)
			runtime.info.SessionCount = protocol.Ptr(count)
		}
	}

	return changed
}

func (m *Manager) EndpointIDForPTYTarget(targetID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for endpointID, runtime := range m.runtimes {
		if _, ok := runtime.sessions[targetID]; ok {
			return endpointID, true
		}
		for _, layout := range runtime.workspaces {
			if layout.Layout == nil {
				continue
			}
			for _, pane := range layout.Layout.Panes {
				if protocol.Deref(pane.RuntimeID) == targetID {
					return endpointID, true
				}
			}
		}
	}
	if pending, ok := m.pendingSessionRouteLocked(targetID, time.Now()); ok {
		return pending.endpointID, true
	}
	return "", false
}

func (m *Manager) EndpointIDForPath(targetPath string) (string, bool) {
	targetPath = normalizeRoutePath(targetPath)
	if targetPath == "" {
		return "", false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	for endpointID, runtime := range m.runtimes {
		for _, session := range runtime.sessions {
			if normalizeRoutePath(session.Directory) == targetPath {
				return endpointID, true
			}
			if normalizeRoutePath(protocol.Deref(session.MainRepo)) == targetPath {
				return endpointID, true
			}
		}
	}
	return "", false
}

func (m *Manager) ReservePendingSessionRoute(endpointID, sessionID string) {
	if strings.TrimSpace(endpointID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[sessionID] = pendingSessionRoute{
		endpointID: endpointID,
		expiresAt:  time.Now().Add(30 * time.Second),
	}
}

func (m *Manager) HasEndpoint(endpointID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.runtimes[endpointID]
	return ok
}

func (m *Manager) HasConfiguredEndpoints() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.runtimes) > 0
}

func (m *Manager) ForwardPTYCommand(ctx context.Context, targetID string, payload []byte) error {
	endpointID, ok := m.EndpointIDForPTYTarget(targetID)
	if !ok {
		return fmt.Errorf("pty target not found: %s", targetID)
	}
	return m.ForwardEndpointCommand(ctx, endpointID, payload)
}

func (m *Manager) ForwardEndpointCommand(ctx context.Context, endpointID string, payload []byte) error {
	m.mu.RLock()
	runtime, ok := m.runtimes[endpointID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("endpoint not found: %s", endpointID)
	}
	conn := runtime.conn
	m.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("endpoint not connected: %s", endpointID)
	}

	runtime.writeMu.Lock()
	defer runtime.writeMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, payload)
}

func (m *Manager) ForwardBrowserControl(
	ctx context.Context,
	endpointID string,
	msg protocol.BrowserControlMessage,
) (string, error) {
	requestID := strings.TrimSpace(protocol.Deref(msg.RequestID))
	if requestID == "" {
		return "", fmt.Errorf("browser control request id is required")
	}
	msg.RequestID = protocol.Ptr(requestID)
	payload, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal browser control request: %w", err)
	}

	pending := pendingBrowserControl{
		endpointID: endpointID,
		done:       make(chan browserControlResult, 1),
	}
	m.mu.Lock()
	if _, exists := m.browserControls[requestID]; exists {
		m.mu.Unlock()
		return "", fmt.Errorf("browser control request already pending: %s", requestID)
	}
	m.browserControls[requestID] = pending
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if current, ok := m.browserControls[requestID]; ok && current.done == pending.done {
			delete(m.browserControls, requestID)
		}
		m.mu.Unlock()
	}()

	if err := m.ForwardEndpointCommand(ctx, endpointID, payload); err != nil {
		return "", err
	}

	select {
	case result := <-pending.done:
		return result.data, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (m *Manager) resolveBrowserControl(endpointID string, payload []byte) {
	var msg protocol.BrowserControlResponseMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		return
	}

	m.mu.RLock()
	pending, ok := m.browserControls[requestID]
	m.mu.RUnlock()
	if !ok || pending.endpointID != endpointID {
		return
	}

	result := browserControlResult{data: protocol.Deref(msg.Data)}
	if !msg.Success {
		errMsg := strings.TrimSpace(protocol.Deref(msg.Error))
		if errMsg == "" {
			errMsg = "remote browser control failed"
		}
		result.err = errors.New(errMsg)
	}
	select {
	case pending.done <- result:
	default:
	}
}

func (m *Manager) replaceRemoteSessions(id string, sessions []protocol.Session) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return false
	}
	next := make(map[string]protocol.Session, len(sessions))
	for _, session := range sessions {
		tagged := tagRemoteSession(id, session)
		next[tagged.ID] = tagged
	}
	if sessionsEqual(runtime.sessions, next) {
		return false
	}
	runtime.sessions = next
	return true
}

func (m *Manager) upsertRemoteSession(id string, session protocol.Session) (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return false, 0
	}
	if runtime.sessions == nil {
		runtime.sessions = make(map[string]protocol.Session)
	}
	tagged := tagRemoteSession(id, session)
	delete(m.pending, tagged.ID)
	if existing, ok := runtime.sessions[tagged.ID]; ok && sessionsMatch(existing, tagged) {
		return false, len(runtime.sessions)
	}
	runtime.sessions[tagged.ID] = tagged
	return true, len(runtime.sessions)
}

func (m *Manager) removeRemoteSession(id, sessionID string) (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok || runtime.sessions == nil {
		return false, 0
	}
	if _, ok := runtime.sessions[sessionID]; !ok {
		return false, len(runtime.sessions)
	}
	delete(runtime.sessions, sessionID)
	delete(m.pending, sessionID)
	return true, len(runtime.sessions)
}

func (m *Manager) clearRemoteSessions(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok || len(runtime.sessions) == 0 {
		if ok {
			m.clearPendingRoutesLocked(id)
		}
		return false
	}
	m.clearPendingRoutesLocked(id)
	runtime.sessions = make(map[string]protocol.Session)
	zero := 0
	runtime.info.SessionCount = protocol.Ptr(zero)
	return true
}

func (m *Manager) pendingSessionRouteLocked(sessionID string, now time.Time) (pendingSessionRoute, bool) {
	pending, ok := m.pending[sessionID]
	if !ok {
		return pendingSessionRoute{}, false
	}
	if !pending.expiresAt.IsZero() && now.After(pending.expiresAt) {
		delete(m.pending, sessionID)
		return pendingSessionRoute{}, false
	}
	return pending, true
}

func (m *Manager) clearPendingRoutesLocked(endpointID string) {
	if endpointID == "" {
		return
	}
	for sessionID, pending := range m.pending {
		if pending.endpointID == endpointID {
			delete(m.pending, sessionID)
		}
	}
}

func (m *Manager) replaceRemoteWorkspaces(id string, workspaces []protocol.Workspace) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return false
	}
	next := make(map[string]protocol.Workspace, len(workspaces))
	for _, workspace := range workspaces {
		workspace.EndpointID = protocol.Ptr(id)
		next[workspace.ID] = workspace
	}
	if workspaceLayoutsEqual(runtime.workspaces, next) {
		return false
	}
	runtime.workspaces = next
	return true
}

func (m *Manager) upsertRemoteWorkspaceLayout(id string, workspace protocol.WorkspaceLayout) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return false
	}
	if runtime.workspaces == nil {
		runtime.workspaces = make(map[string]protocol.Workspace)
	}
	current, ok := runtime.workspaces[workspace.WorkspaceID]
	if !ok {
		return false
	}
	if current.Layout != nil && workspaceLayoutsMatch(*current.Layout, workspace) {
		return false
	}
	current.Layout = &workspace
	runtime.workspaces[workspace.WorkspaceID] = current
	return true
}

func (m *Manager) upsertRemoteWorkspace(id string, workspace protocol.Workspace) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return false
	}
	if runtime.workspaces == nil {
		runtime.workspaces = make(map[string]protocol.Workspace)
	}
	workspace.EndpointID = protocol.Ptr(id)
	runtime.workspaces[workspace.ID] = workspace
	return true
}

func (m *Manager) removeRemoteWorkspace(id, workspaceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok || runtime.workspaces == nil {
		return false
	}
	if _, ok := runtime.workspaces[workspaceID]; !ok {
		return false
	}
	delete(runtime.workspaces, workspaceID)
	return true
}

func (m *Manager) clearRemoteWorkspaceLayouts(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok || len(runtime.workspaces) == 0 {
		return false
	}
	runtime.workspaces = make(map[string]protocol.Workspace)
	return true
}

func (m *Manager) publishSessionsChanged() {
	if m.onSessions != nil {
		m.onSessions()
	}
}

func (m *Manager) publishRawEvent(data []byte) {
	if m.onRawEvent == nil || len(data) == 0 {
		return
	}
	cloned := append([]byte(nil), data...)
	m.onRawEvent(cloned)
}

func tagRemoteSession(endpointID string, session protocol.Session) protocol.Session {
	tagged := session
	tagged.EndpointID = protocol.Ptr(endpointID)
	if len(session.Todos) > 0 {
		tagged.Todos = append([]string(nil), session.Todos...)
	}
	return tagged
}

func sessionsEqual(left, right map[string]protocol.Session) bool {
	if len(left) != len(right) {
		return false
	}
	for id, leftSession := range left {
		rightSession, ok := right[id]
		if !ok || !sessionsMatch(leftSession, rightSession) {
			return false
		}
	}
	return true
}

func sessionsMatch(left, right protocol.Session) bool {
	return left.ID == right.ID &&
		left.Label == right.Label &&
		left.Agent == right.Agent &&
		left.Directory == right.Directory &&
		protocol.Deref(left.EndpointID) == protocol.Deref(right.EndpointID) &&
		protocol.Deref(left.Branch) == protocol.Deref(right.Branch) &&
		protocol.Deref(left.IsWorktree) == protocol.Deref(right.IsWorktree) &&
		protocol.Deref(left.MainRepo) == protocol.Deref(right.MainRepo) &&
		left.State == right.State &&
		left.StateSince == right.StateSince &&
		left.StateUpdatedAt == right.StateUpdatedAt &&
		protocol.Deref(left.NeedsReviewAfterLongRun) == protocol.Deref(right.NeedsReviewAfterLongRun) &&
		protocol.Deref(left.TicketUnread) == protocol.Deref(right.TicketUnread) &&
		protocol.Deref(left.NudgeFiresAt) == protocol.Deref(right.NudgeFiresAt) &&
		strings.Join(left.Todos, "\x00") == strings.Join(right.Todos, "\x00") &&
		left.LastSeen == right.LastSeen
}

func workspaceLayoutsEqual(left, right map[string]protocol.Workspace) bool {
	if len(left) != len(right) {
		return false
	}
	for id, leftWorkspace := range left {
		rightWorkspace, ok := right[id]
		if !ok || leftWorkspace.ID != rightWorkspace.ID || leftWorkspace.Title != rightWorkspace.Title ||
			leftWorkspace.Directory != rightWorkspace.Directory || leftWorkspace.Status != rightWorkspace.Status {
			return false
		}
		if (leftWorkspace.Layout == nil) != (rightWorkspace.Layout == nil) {
			return false
		}
		if leftWorkspace.Layout != nil && !workspaceLayoutsMatch(*leftWorkspace.Layout, *rightWorkspace.Layout) {
			return false
		}
	}
	return true
}

func workspaceLayoutsMatch(left, right protocol.WorkspaceLayout) bool {
	if left.WorkspaceID != right.WorkspaceID ||
		left.ActivePaneID != right.ActivePaneID ||
		left.LayoutJson != right.LayoutJson ||
		protocol.Deref(left.UpdatedAt) != protocol.Deref(right.UpdatedAt) ||
		len(left.Panes) != len(right.Panes) {
		return false
	}
	for i := range left.Panes {
		if left.Panes[i] != right.Panes[i] {
			return false
		}
	}
	return true
}

func capabilitiesFromInitialState(msg *protocol.InitialStateMessage) *protocol.EndpointCapabilities {
	if msg == nil {
		return nil
	}
	settings := msg.Settings
	agents, _ := agentsAvailableFromSettings(settings)

	caps := &protocol.EndpointCapabilities{
		ProtocolVersion: protocol.Deref(msg.ProtocolVersion),
		AgentsAvailable: agents,
	}
	if value := strings.TrimSpace(fmt.Sprint(settings[settingProjectsDirectory])); value != "" && value != "<nil>" {
		caps.ProjectsDirectory = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(fmt.Sprint(settings[settingPTYBackendMode])); value != "" && value != "<nil>" {
		caps.PtyBackendMode = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(protocol.Deref(msg.DaemonInstanceID)); value != "" {
		caps.DaemonInstanceID = protocol.Ptr(value)
	}
	return mergeCapabilitiesFromSettings(caps, settings)
}

func agentsAvailableFromSettings(settings map[string]interface{}) ([]string, bool) {
	agents := make([]string, 0, 4)
	foundAvailability := false
	for key, value := range settings {
		if !strings.HasSuffix(key, "_available") {
			continue
		}
		foundAvailability = true
		agent := strings.TrimSuffix(key, "_available")
		if strings.TrimSpace(agent) != "" && truthySetting(value) {
			agents = append(agents, agent)
		}
	}
	sort.Strings(agents)
	return agents, foundAvailability
}

func truthySetting(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func stringSetting(value interface{}) string {
	trimmed := strings.TrimSpace(fmt.Sprint(value))
	if trimmed == "" || trimmed == "<nil>" {
		return ""
	}
	return trimmed
}

func mergeCapabilitiesFromSettings(existing *protocol.EndpointCapabilities, settings map[string]interface{}) *protocol.EndpointCapabilities {
	var caps protocol.EndpointCapabilities
	if existing != nil {
		caps = *existing
		if existing.AgentsAvailable != nil {
			caps.AgentsAvailable = append([]string(nil), existing.AgentsAvailable...)
		}
	}

	if settings == nil {
		return &caps
	}
	if agents, ok := agentsAvailableFromSettings(settings); ok {
		caps.AgentsAvailable = agents
	}
	if value := stringSetting(settings[settingProjectsDirectory]); value != "" {
		caps.ProjectsDirectory = protocol.Ptr(value)
	}
	if value := stringSetting(settings[settingPTYBackendMode]); value != "" {
		caps.PtyBackendMode = protocol.Ptr(value)
	}
	if _, ok := settings[settingTailscaleEnabled]; ok {
		enabled := truthySetting(settings[settingTailscaleEnabled])
		caps.TailscaleEnabled = protocol.Ptr(enabled)
	}
	if value := stringSetting(settings[settingTailscaleStatus]); value != "" {
		caps.TailscaleStatus = protocol.Ptr(value)
	} else {
		caps.TailscaleStatus = nil
	}
	if value := stringSetting(settings[settingTailscaleURL]); value != "" {
		caps.TailscaleURL = protocol.Ptr(value)
	} else {
		caps.TailscaleURL = nil
	}
	if value := stringSetting(settings[settingTailscaleDomain]); value != "" {
		caps.TailscaleDomain = protocol.Ptr(value)
	} else {
		caps.TailscaleDomain = nil
	}
	if value := stringSetting(settings[settingTailscaleAuthURL]); value != "" {
		caps.TailscaleAuthURL = protocol.Ptr(value)
	} else {
		caps.TailscaleAuthURL = nil
	}
	if value := stringSetting(settings[settingTailscaleError]); value != "" {
		caps.TailscaleError = protocol.Ptr(value)
	} else {
		caps.TailscaleError = nil
	}
	return &caps
}

func shouldResolveRemoteWebAction(pending *pendingRemoteWebAction, msg *protocol.SettingsUpdatedMessage, caps *protocol.EndpointCapabilities) bool {
	if pending == nil || msg == nil {
		return false
	}
	if strings.TrimSpace(protocol.Deref(msg.ChangedKey)) != settingTailscaleEnabled {
		return false
	}
	if msg.Success != nil && !protocol.Deref(msg.Success) {
		return true
	}
	if caps == nil {
		return false
	}
	return protocol.Deref(caps.TailscaleEnabled) == pending.desiredEnabled
}

func remoteWebActionResult(desiredEnabled bool, msg *protocol.SettingsUpdatedMessage, caps *protocol.EndpointCapabilities) error {
	if msg != nil && msg.Success != nil && !protocol.Deref(msg.Success) {
		return fmt.Errorf("%s", strings.TrimSpace(protocol.Deref(msg.Error)))
	}
	if caps == nil {
		return fmt.Errorf("remote daemon did not report tailscale status")
	}
	status := strings.TrimSpace(protocol.Deref(caps.TailscaleStatus))
	errorText := strings.TrimSpace(protocol.Deref(caps.TailscaleError))
	enabled := protocol.Deref(caps.TailscaleEnabled)
	if desiredEnabled {
		if !enabled {
			return fmt.Errorf("remote web setting did not stick")
		}
		if status == "running" {
			return nil
		}
		if errorText != "" {
			return fmt.Errorf("%s", errorText)
		}
		if status == "" {
			return fmt.Errorf("remote daemon did not report tailscale status")
		}
		return fmt.Errorf("remote web is %s", status)
	}
	if enabled {
		return fmt.Errorf("remote web setting is still enabled")
	}
	if status == "error" && errorText != "" {
		return fmt.Errorf("%s", errorText)
	}
	return nil
}

func (m *Manager) handleRemoteSettingsUpdated(id string, msg *protocol.SettingsUpdatedMessage) {
	var (
		info    protocol.EndpointInfo
		pending *pendingRemoteWebAction
		result  error
	)

	m.mu.Lock()
	runtime, ok := m.runtimes[id]
	if ok {
		info = runtime.info
		info.Capabilities = mergeCapabilitiesFromSettings(runtime.info.Capabilities, msg.Settings)
		runtime.info = info
		if shouldResolveRemoteWebAction(runtime.pendingRemoteWeb, msg, info.Capabilities) {
			pending = runtime.pendingRemoteWeb
			result = remoteWebActionResult(pending.desiredEnabled, msg, info.Capabilities)
			runtime.pendingRemoteWeb = nil
		}
	}
	m.mu.Unlock()

	if !ok {
		return
	}
	if m.onStatus != nil {
		m.onStatus(info)
	}
	if pending != nil {
		select {
		case pending.done <- result:
		default:
		}
	}
}

func (m *Manager) SetEndpointRemoteWeb(ctx context.Context, endpointID string, enabled bool) error {
	payload, err := json.Marshal(protocol.SetSettingMessage{
		Cmd:   protocol.CmdSetSetting,
		Key:   settingTailscaleEnabled,
		Value: fmt.Sprintf("%t", enabled),
	})
	if err != nil {
		return err
	}

	pending := &pendingRemoteWebAction{
		desiredEnabled: enabled,
		done:           make(chan error, 1),
	}

	m.mu.Lock()
	runtime, ok := m.runtimes[endpointID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("endpoint not found: %s", endpointID)
	}
	if runtime.conn == nil {
		m.mu.Unlock()
		return fmt.Errorf("endpoint not connected: %s", endpointID)
	}
	if runtime.pendingRemoteWeb != nil {
		m.mu.Unlock()
		return fmt.Errorf("remote web update already in progress")
	}
	runtime.pendingRemoteWeb = pending
	m.mu.Unlock()

	if err := m.ForwardEndpointCommand(ctx, endpointID, payload); err != nil {
		m.mu.Lock()
		runtime, ok := m.runtimes[endpointID]
		if ok && runtime.pendingRemoteWeb == pending {
			runtime.pendingRemoteWeb = nil
		}
		m.mu.Unlock()
		return err
	}

	select {
	case err := <-pending.done:
		return err
	case <-ctx.Done():
		m.mu.Lock()
		runtime, ok := m.runtimes[endpointID]
		if ok && runtime.pendingRemoteWeb == pending {
			runtime.pendingRemoteWeb = nil
		}
		m.mu.Unlock()
		return ctx.Err()
	}
}

// publishConnectionAndSendHello publishes the runtime's connection and sends
// the client_hello over it atomically with respect to ForwardEndpointCommand.
// It holds runtime.writeMu from before the connection becomes visible (under
// m.mu) until after the hello write completes, so a forwarded command can
// never observe the published connection and win the write race against the
// hello: ForwardEndpointCommand always reads runtime.conn under m.mu first,
// so it either sees the old (nil or previous) connection, or it sees this
// connection but then blocks on writeMu until the hello has been sent.
func (m *Manager) publishConnectionAndSendHello(ctx context.Context, id string, conn *websocket.Conn, cmd *exec.Cmd) error {
	m.mu.Lock()
	runtime, ok := m.runtimes[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("endpoint not found: %s", id)
	}
	runtime.conn = conn
	runtime.cmd = cmd
	runtime.writeMu.Lock()
	m.mu.Unlock()
	defer runtime.writeMu.Unlock()

	return sendClientHello(ctx, conn)
}

func (m *Manager) clearConnection(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return
	}
	if runtime.conn != nil {
		_ = runtime.conn.Close(websocket.StatusNormalClosure, "")
		runtime.conn = nil
	}
	if runtime.cmd != nil {
		killAndReap(runtime.cmd)
		runtime.cmd = nil
	}
}

func (m *Manager) recordFor(id string) (store.EndpointRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return store.EndpointRecord{}, false
	}
	return runtime.record, true
}

func (m *Manager) updateStatus(id, status, message string, caps *protocol.EndpointCapabilities, sessionCount *int32) {
	m.mu.Lock()
	runtime, ok := m.runtimes[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	info := runtime.info
	info.ID = runtime.record.ID
	info.Name = runtime.record.Name
	info.SshTarget = runtime.record.SSHTarget
	info.Enabled = protocol.Ptr(runtime.record.Enabled)
	if strings.TrimSpace(runtime.record.Profile) != "" {
		info.Profile = protocol.Ptr(runtime.record.Profile)
	} else {
		info.Profile = nil
	}
	info.Status = status
	if message != "" {
		info.StatusMessage = protocol.Ptr(message)
	} else {
		info.StatusMessage = nil
	}
	if caps != nil {
		info.Capabilities = caps
	}
	if sessionCount != nil {
		count := int(*sessionCount)
		info.SessionCount = protocol.Ptr(count)
	}
	runtime.info = info
	m.mu.Unlock()

	if m.onStatus != nil {
		m.onStatus(info)
	}
}

func (m *Manager) publishStatus(id string) {
	m.mu.RLock()
	runtime, ok := m.runtimes[id]
	if !ok {
		m.mu.RUnlock()
		return
	}
	info := runtime.info
	m.mu.RUnlock()
	if m.onStatus != nil {
		m.onStatus(info)
	}
}

func nextBackoff(current time.Duration) time.Duration {
	if current < time.Second {
		return time.Second
	}
	current *= 2
	if current > 30*time.Second {
		return 30 * time.Second
	}
	return current
}

func normalizeRoutePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func sleepOrDone(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// fingerprintMismatch returns true (and a human-readable message) when the remote
// daemon was built from a different source than this client.
//
// Rules:
//   - If the local fingerprint is unknown (dev build, no ldflags) — skip; we can't
//     make any claims about what "our" binary is.
//   - If the local fingerprint is known but the remote is absent/unknown — mismatch:
//     the running daemon is either an old binary that pre-dates source_fingerprint
//     tracking, or a dev build.
//   - If both are known and equal — no mismatch.
//   - If both are known and differ — mismatch.
func fingerprintMismatch(remoteFingerprint *string) (bool, string) {
	local := normalizeFingerprint(buildinfo.SourceFingerprint)
	if local == "" {
		return false, ""
	}
	remote := normalizeFingerprint(protocol.Deref(remoteFingerprint))
	if local == remote {
		return false, ""
	}
	shortLocal := shortFingerprint(local)
	if remote == "" {
		return true, fmt.Sprintf("remote binary (unknown) differs from this client (%s) — click Sync to update", shortLocal)
	}
	shortRemote := shortFingerprint(remote)
	return true, fmt.Sprintf("remote binary (%s) differs from this client (%s) — click Sync to update", shortRemote, shortLocal)
}

func normalizeFingerprint(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "unknown" {
		return ""
	}
	return v
}

func shortFingerprint(v string) string {
	if len(v) > 12 {
		return v[:12]
	}
	return v
}
