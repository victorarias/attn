package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

const (
	settingProjectsDirectory = "projects_directory"
	settingPTYBackendMode    = "pty_backend_mode"
)

type endpointRuntime struct {
	record store.EndpointRecord
	info   protocol.EndpointInfo

	cancel context.CancelFunc
	conn   *websocket.Conn
	cmd    *exec.Cmd
}

type Manager struct {
	store        *store.Store
	bootstrapper *Bootstrapper
	onStatus     StatusCallback
	logf         func(format string, args ...interface{})

	mu       sync.RWMutex
	runtimes map[string]*endpointRuntime
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
}

func NewManager(endpointStore *store.Store, onStatus StatusCallback, logf func(format string, args ...interface{})) *Manager {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	m := &Manager{
		store:        endpointStore,
		bootstrapper: NewBootstrapper(logf),
		onStatus:     onStatus,
		logf:         logf,
		runtimes:     make(map[string]*endpointRuntime),
	}
	for _, record := range endpointStore.ListEndpoints() {
		m.runtimes[record.ID] = &endpointRuntime{
			record: record,
			info:   infoFromRecord(record),
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	for _, runtime := range m.runtimes {
		m.stopRuntimeLocked(runtime)
	}
	m.started = false
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
		record: *record,
		info:   infoFromRecord(*record),
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

	m.mu.Lock()
	runtime, ok := m.runtimes[id]
	if !ok {
		runtime = &endpointRuntime{}
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
	m.stopRuntimeLocked(runtime)
	if m.started && record.Enabled {
		m.startRuntimeLocked(id)
	}
	m.mu.Unlock()

	m.publishStatus(id)
	return record, nil
}

func (m *Manager) RemoveEndpoint(id string) error {
	m.mu.Lock()
	if runtime, ok := m.runtimes[id]; ok {
		m.stopRuntimeLocked(runtime)
		delete(m.runtimes, id)
	}
	m.mu.Unlock()
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
			caps := capabilitiesFromInitialState(&msg)
			sessionCount := int32(len(msg.Sessions))
			m.updateStatus(id, "connected", "Connected", caps, &sessionCount)
			connected = true
		case protocol.EventSessionsUpdated:
			var msg protocol.SessionsUpdatedMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			sessionCount := int32(len(msg.Sessions))
			m.updateStatus(id, "connected", "Connected", nil, &sessionCount)
		}
	}
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
	return caps
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
