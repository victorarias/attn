package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type StatusCallback func(info protocol.EndpointInfo)
type SessionsChangedCallback func()
type RawEventCallback func(data []byte)

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

	sessions   map[string]protocol.Session
	workspaces map[string]protocol.WorkspaceSnapshot
}

type pendingRemoteWebAction struct {
	desiredEnabled bool
	done           chan error
}

type pendingSessionRoute struct {
	endpointID string
	expiresAt  time.Time
}

type Manager struct {
	store        *store.Store
	bootstrapper *Bootstrapper
	onStatus     StatusCallback
	onSessions   SessionsChangedCallback
	onRawEvent   RawEventCallback
	logf         func(format string, args ...interface{})

	mu       sync.RWMutex
	runtimes map[string]*endpointRuntime
	pending  map[string]pendingSessionRoute
	reviews  map[string]string
	comments map[string]string
	loops    map[string]string
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
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
		store:        endpointStore,
		bootstrapper: NewBootstrapper(logf),
		onStatus:     onStatus,
		onSessions:   onSessions,
		onRawEvent:   onRawEvent,
		logf:         logf,
		runtimes:     make(map[string]*endpointRuntime),
		pending:      make(map[string]pendingSessionRoute),
		reviews:      make(map[string]string),
		comments:     make(map[string]string),
		loops:        make(map[string]string),
	}
	for _, record := range endpointStore.ListEndpoints() {
		m.runtimes[record.ID] = &endpointRuntime{
			record:     record,
			info:       infoFromRecord(record),
			sessions:   make(map[string]protocol.Session),
			workspaces: make(map[string]protocol.WorkspaceSnapshot),
		}
	}
	return m
}

func infoFromRecord(record store.EndpointRecord) protocol.EndpointInfo {
	return protocol.EndpointInfo{
		ID:        record.ID,
		Name:      record.Name,
		SshTarget: record.SSHTarget,
		Status:    "disconnected",
		Enabled:   protocol.Ptr(record.Enabled),
	}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	for _, runtime := range m.runtimes {
		changed = changed || len(runtime.sessions) > 0
		m.stopRuntimeLocked(runtime)
	}
	m.started = false
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

func (m *Manager) AddEndpoint(name, sshTarget string) (*store.EndpointRecord, error) {
	name = strings.TrimSpace(name)
	sshTarget = strings.TrimSpace(sshTarget)
	if name == "" {
		return nil, fmt.Errorf("endpoint name is required")
	}
	if sshTarget == "" {
		return nil, fmt.Errorf("ssh target is required")
	}

	record, err := m.store.AddEndpoint(name, sshTarget)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.runtimes[record.ID] = &endpointRuntime{
		record:     *record,
		info:       infoFromRecord(*record),
		sessions:   make(map[string]protocol.Session),
		workspaces: make(map[string]protocol.WorkspaceSnapshot),
	}
	if m.started && record.Enabled {
		m.startRuntimeLocked(record.ID)
	}
	m.mu.Unlock()

	m.publishStatus(record.ID)
	return record, nil
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
			workspaces: make(map[string]protocol.WorkspaceSnapshot),
		}
		m.runtimes[id] = runtime
	}
	runtime.record = *record
	info := runtime.info
	info.ID = record.ID
	info.Name = record.Name
	info.SshTarget = record.SSHTarget
	info.Enabled = protocol.Ptr(record.Enabled)
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
	m.mu.Lock()
	if runtime, ok := m.runtimes[id]; ok {
		changed = len(runtime.sessions) > 0
		m.stopRuntimeLocked(runtime)
		delete(m.runtimes, id)
	}
	m.mu.Unlock()
	if changed {
		m.publishSessionsChanged()
	}
	return m.store.RemoveEndpoint(id)
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
	if runtime.cmd != nil && runtime.cmd.Process != nil {
		_ = runtime.cmd.Process.Kill()
		_, _ = runtime.cmd.Process.Wait()
		runtime.cmd = nil
	}
	runtime.sessions = make(map[string]protocol.Session)
	runtime.workspaces = make(map[string]protocol.WorkspaceSnapshot)
	m.clearPendingRoutesLocked(runtime.record.ID)
	m.clearRouteCachesLocked(runtime.record.ID)
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

		m.updateStatus(id, "bootstrapping", "Checking remote platform", nil, nil)
		bootCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := m.bootstrapper.EnsureRemoteReady(bootCtx, record.SSHTarget)
		cancel()
		if err != nil {
			m.updateStatus(id, "error", err.Error(), nil, nil)
			if !sleepOrDone(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		m.updateStatus(id, "connecting", "Connecting to remote daemon", nil, nil)
		conn, cmd, err := connectViaSSH(ctx, record.SSHTarget, config.WSAuthToken())
		if err != nil {
			m.updateStatus(id, "error", err.Error(), nil, nil)
			if !sleepOrDone(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		m.setConnection(id, conn, cmd)
		connected, err := m.consumeRemote(ctx, id, conn)
		m.clearConnection(id)
		if m.clearRemoteSessions(id) {
			m.publishSessionsChanged()
		}
		m.clearRemoteWorkspaces(id)

		if ctx.Err() != nil {
			return
		}
		if connected {
			m.updateStatus(id, "disconnected", "Disconnected; reconnecting", nil, nil)
		} else if err != nil {
			m.updateStatus(id, "error", err.Error(), nil, nil)
		}
		if !sleepOrDone(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func (m *Manager) consumeRemote(ctx context.Context, id string, conn *websocket.Conn) (bool, error) {
	connected := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
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
				return false, fmt.Errorf("protocol mismatch: remote=%s local=%s", remoteProtocol, protocol.ProtocolVersion)
			}
			changed := m.replaceRemoteSessions(id, msg.Sessions)
			m.replaceRemoteWorkspaces(id, msg.Workspaces)
			caps := capabilitiesFromInitialState(&msg)
			sessionCount := int32(len(msg.Sessions))
			m.updateStatus(id, "connected", "Connected", caps, &sessionCount)
			if changed {
				m.publishSessionsChanged()
			}
			m.publishWorkspaceSnapshots(msg.Workspaces)
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
			m.updateStatus(id, "connected", "Connected", nil, &sessionCount)
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
			m.updateStatus(id, "connected", "Connected", nil, &countValue)
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
			m.removeRemoteWorkspace(id, msg.Session.ID)
			countValue := int32(sessionCount)
			m.updateStatus(id, "connected", "Connected", nil, &countValue)
			if changed {
				m.publishSessionsChanged()
			}
		case protocol.EventWorkspaceSnapshot, protocol.EventWorkspaceUpdated:
			var msg struct {
				Workspace *protocol.WorkspaceSnapshot `json:"workspace"`
			}
			if err := json.Unmarshal(data, &msg); err != nil || msg.Workspace == nil {
				continue
			}
			m.upsertRemoteWorkspace(id, *msg.Workspace)
			m.publishRawEvent(data)
		default:
			if forwardsRawEvent(peek.Event) {
				m.observeRemoteEvent(id, peek.Event, data)
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
		protocol.EventDeleteBranchResult,
		protocol.EventSwitchBranchResult,
		protocol.EventCreateBranchResult,
		protocol.EventCheckDirtyResult,
		protocol.EventStashResult,
		protocol.EventStashPopResult,
		protocol.EventCheckAttnStashResult,
		protocol.EventCommitWIPResult,
		protocol.EventGetDefaultBranchResult,
		protocol.EventFetchRemotesResult,
		protocol.EventListRemoteBranchesResult,
		protocol.EventEnsureRepoResult,
		protocol.EventGitStatusUpdate,
		protocol.EventFileDiffResult,
		protocol.EventBranchDiffFilesResult,
		protocol.EventGetRepoInfoResult,
		protocol.EventGetReviewStateResult,
		protocol.EventReviewLoopResult,
		protocol.EventReviewLoopUpdated,
		protocol.EventMarkFileViewedResult,
		protocol.EventAddCommentResult,
		protocol.EventUpdateCommentResult,
		protocol.EventResolveCommentResult,
		protocol.EventWontFixCommentResult,
		protocol.EventDeleteCommentResult,
		protocol.EventGetCommentsResult,
		protocol.EventWorkspaceRuntimeExited,
		protocol.EventSpawnResult,
		protocol.EventAttachResult,
		protocol.EventPtyOutput,
		protocol.EventPtyDesync,
		protocol.EventSessionExited,
		protocol.EventWorkspaceActionResult,
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

func (m *Manager) RemoteWorkspaces() []protocol.WorkspaceSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, runtime := range m.runtimes {
		total += len(runtime.workspaces)
	}
	if total == 0 {
		return nil
	}

	out := make([]protocol.WorkspaceSnapshot, 0, total)
	for _, runtime := range m.runtimes {
		for _, workspace := range runtime.workspaces {
			out = append(out, workspace)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SessionID < out[j].SessionID
	})
	return out
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
		if runtime.workspaces != nil {
			if _, ok := runtime.workspaces[sessionID]; ok {
				delete(runtime.workspaces, sessionID)
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
		for _, workspace := range runtime.workspaces {
			for _, pane := range workspace.Panes {
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

func (m *Manager) EndpointIDForReview(reviewID string) (string, bool) {
	reviewID = strings.TrimSpace(reviewID)
	if reviewID == "" {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	endpointID, ok := m.reviews[reviewID]
	return endpointID, ok
}

func (m *Manager) EndpointIDForComment(commentID string) (string, bool) {
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	endpointID, ok := m.comments[commentID]
	return endpointID, ok
}

func (m *Manager) EndpointIDForReviewLoop(loopID string) (string, bool) {
	loopID = strings.TrimSpace(loopID)
	if loopID == "" {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	endpointID, ok := m.loops[loopID]
	return endpointID, ok
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

func (m *Manager) clearRouteCachesLocked(endpointID string) {
	if endpointID == "" {
		return
	}
	for reviewID, current := range m.reviews {
		if current == endpointID {
			delete(m.reviews, reviewID)
		}
	}
	for commentID, current := range m.comments {
		if current == endpointID {
			delete(m.comments, commentID)
		}
	}
	for loopID, current := range m.loops {
		if current == endpointID {
			delete(m.loops, loopID)
		}
	}
}

func (m *Manager) observeRemoteEvent(endpointID, event string, data []byte) {
	switch event {
	case protocol.EventGetReviewStateResult:
		var msg protocol.GetReviewStateResultMessage
		if err := json.Unmarshal(data, &msg); err != nil || !msg.Success || msg.State == nil {
			return
		}
		m.recordReviewRoute(endpointID, msg.State.ReviewID)
	case protocol.EventMarkFileViewedResult:
		var msg protocol.MarkFileViewedResultMessage
		if err := json.Unmarshal(data, &msg); err != nil || !msg.Success {
			return
		}
		m.recordReviewRoute(endpointID, msg.ReviewID)
	case protocol.EventAddCommentResult:
		var msg protocol.AddCommentResultMessage
		if err := json.Unmarshal(data, &msg); err != nil || !msg.Success || msg.Comment == nil {
			return
		}
		m.recordReviewRoute(endpointID, msg.Comment.ReviewID)
		m.recordCommentRoute(endpointID, msg.Comment.ID)
	case protocol.EventGetCommentsResult:
		var msg protocol.GetCommentsResultMessage
		if err := json.Unmarshal(data, &msg); err != nil || !msg.Success {
			return
		}
		for _, comment := range msg.Comments {
			m.recordReviewRoute(endpointID, comment.ReviewID)
			m.recordCommentRoute(endpointID, comment.ID)
		}
	case protocol.EventReviewLoopResult:
		var msg protocol.ReviewLoopResultMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		if msg.ReviewLoopRun != nil {
			m.recordLoopRoute(endpointID, msg.ReviewLoopRun.LoopID)
		}
		if msg.LoopID != nil {
			m.recordLoopRoute(endpointID, *msg.LoopID)
		}
	case protocol.EventReviewLoopUpdated:
		var msg protocol.ReviewLoopUpdatedMessage
		if err := json.Unmarshal(data, &msg); err != nil || msg.ReviewLoopRun == nil {
			return
		}
		m.recordLoopRoute(endpointID, msg.ReviewLoopRun.LoopID)
	}
}

func (m *Manager) recordReviewRoute(endpointID, reviewID string) {
	reviewID = strings.TrimSpace(reviewID)
	if endpointID == "" || reviewID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reviews[reviewID] = endpointID
}

func (m *Manager) recordCommentRoute(endpointID, commentID string) {
	commentID = strings.TrimSpace(commentID)
	if endpointID == "" || commentID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.comments[commentID] = endpointID
}

func (m *Manager) recordLoopRoute(endpointID, loopID string) {
	loopID = strings.TrimSpace(loopID)
	if endpointID == "" || loopID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loops[loopID] = endpointID
}

func (m *Manager) replaceRemoteWorkspaces(id string, workspaces []protocol.WorkspaceSnapshot) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return false
	}
	next := make(map[string]protocol.WorkspaceSnapshot, len(workspaces))
	for _, workspace := range workspaces {
		if runtime.sessions != nil {
			if _, ok := runtime.sessions[workspace.SessionID]; !ok {
				continue
			}
		}
		next[workspace.SessionID] = workspace
	}
	if workspacesEqual(runtime.workspaces, next) {
		return false
	}
	runtime.workspaces = next
	return true
}

func (m *Manager) upsertRemoteWorkspace(id string, workspace protocol.WorkspaceSnapshot) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return false
	}
	if runtime.sessions != nil {
		if _, ok := runtime.sessions[workspace.SessionID]; !ok {
			return false
		}
	}
	if runtime.workspaces == nil {
		runtime.workspaces = make(map[string]protocol.WorkspaceSnapshot)
	}
	if existing, ok := runtime.workspaces[workspace.SessionID]; ok && workspacesMatch(existing, workspace) {
		return false
	}
	runtime.workspaces[workspace.SessionID] = workspace
	return true
}

func (m *Manager) removeRemoteWorkspace(id, sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok || runtime.workspaces == nil {
		return false
	}
	if _, ok := runtime.workspaces[sessionID]; !ok {
		return false
	}
	delete(runtime.workspaces, sessionID)
	return true
}

func (m *Manager) clearRemoteWorkspaces(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok || len(runtime.workspaces) == 0 {
		return false
	}
	runtime.workspaces = make(map[string]protocol.WorkspaceSnapshot)
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

func (m *Manager) publishWorkspaceSnapshots(workspaces []protocol.WorkspaceSnapshot) {
	for _, workspace := range workspaces {
		payload, err := json.Marshal(protocol.WorkspaceSnapshotMessage{
			Event:     protocol.EventWorkspaceSnapshot,
			Workspace: workspace,
		})
		if err != nil {
			continue
		}
		m.publishRawEvent(payload)
	}
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
		protocol.Deref(left.Recoverable) == protocol.Deref(right.Recoverable) &&
		strings.Join(left.Todos, "\x00") == strings.Join(right.Todos, "\x00") &&
		left.LastSeen == right.LastSeen &&
		left.Muted == right.Muted
}

func workspacesEqual(left, right map[string]protocol.WorkspaceSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for id, leftWorkspace := range left {
		rightWorkspace, ok := right[id]
		if !ok || !workspacesMatch(leftWorkspace, rightWorkspace) {
			return false
		}
	}
	return true
}

func workspacesMatch(left, right protocol.WorkspaceSnapshot) bool {
	if left.SessionID != right.SessionID ||
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
	agents := make([]string, 0, 4)
	for _, agent := range []string{"claude", "codex", "copilot", "pi"} {
		key := agent + "_available"
		if truthySetting(settings[key]) {
			agents = append(agents, agent)
		}
	}

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

func (m *Manager) setConnection(id string, conn *websocket.Conn, cmd *exec.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return
	}
	runtime.conn = conn
	runtime.cmd = cmd
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
	if runtime.cmd != nil && runtime.cmd.Process != nil {
		_ = runtime.cmd.Process.Kill()
		_, _ = runtime.cmd.Process.Wait()
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
