package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
)

const pluginDriverCallTimeout = 30 * time.Second

type pluginDriverRegistration struct {
	PluginName   string
	Agent        string
	Capabilities map[string]bool
}

type pluginDriverRegisterParams struct {
	Agent        string          `json:"agent"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
}

type pluginDriverRegisterResult struct {
	OK bool `json:"ok"`
}

type pluginDriverSpawnParams struct {
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	CWD       string          `json:"cwd"`
	Label     string          `json:"label,omitempty"`
	Yolo      bool            `json:"yolo,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type pluginDriverSpawnResult struct {
	Argv []string          `json:"argv"`
	Env  map[string]string `json:"env,omitempty"`
	CWD  string            `json:"cwd,omitempty"`
}

type pluginReportStateParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Seq       uint64 `json:"seq"`
	State     string `json:"state"`
}

type pluginReportStopParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Seq       uint64 `json:"seq"`
	Verdict   string `json:"verdict"`
}

type pluginReportMetadataParams struct {
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	Seq       uint64          `json:"seq"`
	Metadata  json.RawMessage `json:"metadata"`
}

type pluginDriverSessionClosedParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Reason    string `json:"reason"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Signal    string `json:"signal,omitempty"`
}

type pluginDriverSessionClosedResult struct {
	OK bool `json:"ok"`
}

type pendingPluginReport struct {
	State    *pluginReportStateParams
	Stop     *pluginReportStopParams
	Metadata *pluginReportMetadataParams
}

type pluginSessionLaunch struct {
	PluginName string
	RunID      string
}

func (r *pluginRegistry) registerDriver(plugin *pluginConnection, params pluginDriverRegisterParams) error {
	agent := normalizePluginAgent(params.Agent)
	if agent == "" {
		return errors.New("driver.register params.agent must contain lowercase letters, numbers, hyphens, or underscores")
	}
	if agent == protocol.AgentShellValue || agentdriver.Get(agent) != nil {
		return fmt.Errorf("agent %q is reserved by attn", agent)
	}
	capabilities, err := validatePluginDriverCapabilities(params.Capabilities)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plugins[plugin.name] != plugin {
		return fmt.Errorf("plugin %q is not connected", plugin.name)
	}
	if existing, exists := r.drivers[agent]; exists && existing.PluginName != plugin.name {
		return fmt.Errorf("agent %q is already registered by plugin %q", agent, existing.PluginName)
	}
	r.drivers[agent] = pluginDriverRegistration{
		PluginName:   plugin.name,
		Agent:        agent,
		Capabilities: capabilities,
	}
	return nil
}

func (r *pluginRegistry) driver(agent string) (pluginDriverRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	driver, ok := r.drivers[strings.TrimSpace(strings.ToLower(agent))]
	return driver, ok
}

func (r *pluginRegistry) registeredDrivers() []pluginDriverRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	drivers := make([]pluginDriverRegistration, 0, len(r.drivers))
	for _, driver := range r.drivers {
		drivers = append(drivers, driver)
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Agent < drivers[j].Agent })
	return drivers
}

func normalizePluginAgent(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	for i, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || (i > 0 && (r == '-' || r == '_'))
		if !valid {
			return ""
		}
	}
	return value
}

func validatePluginDriverCapabilities(values map[string]bool) (map[string]bool, error) {
	allowed := map[string]struct{}{
		"resume":           {},
		"yolo":             {},
		"classifier":       {},
		"state_reporting":  {},
		"pending_approval": {},
	}
	out := make(map[string]bool, len(values))
	for name, enabled := range values {
		name = strings.TrimSpace(strings.ToLower(name))
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("unsupported driver capability %q", name)
		}
		out[name] = enabled
	}
	return out, nil
}

func (d *Daemon) handlePluginDriverMethod(plugin *pluginConnection, msg jsonRPCMessage) (interface{}, bool, error) {
	switch msg.Method {
	case "driver.register":
		var params pluginDriverRegisterParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode driver.register params: %w", err)
		}
		if err := d.ensurePluginRegistry().registerDriver(plugin, params); err != nil {
			return nil, true, err
		}
		d.broadcastSettings("")
		return pluginDriverRegisterResult{OK: true}, true, nil
	case "session.report_state":
		var params pluginReportStateParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_state params: %w", err)
		}
		if err := validatePluginReportedState(params); err != nil {
			return nil, true, err
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{State: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedState(params)
		}
		return struct{}{}, true, nil
	case "session.report_stop":
		var params pluginReportStopParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_stop params: %w", err)
		}
		if err := validatePluginReportedStop(params); err != nil {
			return nil, true, err
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{Stop: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedStop(params)
		}
		return struct{}{}, true, nil
	case "session.report_metadata":
		var params pluginReportMetadataParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_metadata params: %w", err)
		}
		if len(params.Metadata) == 0 || !json.Valid(params.Metadata) {
			return nil, true, errors.New("session.report_metadata metadata must be valid JSON")
		}
		if err := validatePluginReportCursor(params.RunID, params.Seq); err != nil {
			return nil, true, err
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{Metadata: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedMetadata(params)
		}
		return struct{}{}, true, nil
	default:
		return nil, false, nil
	}
}

func (d *Daemon) authorizePluginSessionReport(plugin *pluginConnection, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("unknown session %q", sessionID)
	}
	driver, ok := d.ensurePluginRegistry().driver(string(session.Agent))
	if !ok || driver.PluginName != plugin.name {
		return fmt.Errorf("plugin %q does not own session %q", plugin.name, sessionID)
	}
	return nil
}

func validatePluginReportedState(params pluginReportStateParams) error {
	switch strings.TrimSpace(params.State) {
	case protocol.StateWorking, protocol.StateWaitingInput, protocol.StatePendingApproval, protocol.StateIdle, protocol.StateUnknown:
	default:
		return fmt.Errorf("unsupported session state %q", params.State)
	}
	return validatePluginReportCursor(params.RunID, params.Seq)
}

func validatePluginReportCursor(runID string, seq uint64) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("run_id is required")
	}
	if seq == 0 {
		return errors.New("seq must be greater than zero")
	}
	return nil
}

func (d *Daemon) applyPluginReportedState(params pluginReportStateParams) bool {
	state := strings.TrimSpace(params.State)
	if !d.store.ApplyAgentDriverState(params.SessionID, params.RunID, params.Seq, state) {
		d.logf("plugin state report discarded: session=%s run=%s seq=%d state=%s", params.SessionID, params.RunID, params.Seq, state)
		return false
	}
	switch state {
	case protocol.StateWorking:
		d.markRunStartedIfNeeded(params.SessionID)
	case protocol.StateIdle:
		d.clearLongRunTracking(params.SessionID)
	}
	d.store.Touch(params.SessionID)
	session := d.sessionForBroadcast(d.store.Get(params.SessionID))
	if session != nil {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionStateChanged,
			Session: session,
		})
		d.recomputeAndBroadcastWorkspaceForSession(params.SessionID)
	}
	return true
}

func validatePluginReportedStop(params pluginReportStopParams) error {
	verdict := strings.TrimSpace(params.Verdict)
	switch verdict {
	case protocol.StateIdle, protocol.StateWaitingInput, protocol.StateUnknown:
	default:
		return fmt.Errorf("unsupported stop verdict %q", params.Verdict)
	}
	return validatePluginReportCursor(params.RunID, params.Seq)
}

func (d *Daemon) applyPluginReportedStop(params pluginReportStopParams) {
	if d.applyPluginReportedState(pluginReportStateParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       params.Seq,
		State:     strings.TrimSpace(params.Verdict),
	}) {
		d.clearLongRunTracking(params.SessionID)
	}
}

func (d *Daemon) applyPluginReportedMetadata(params pluginReportMetadataParams) bool {
	if !d.store.ApplyAgentDriverMetadata(params.SessionID, params.RunID, params.Seq, string(params.Metadata)) {
		d.logf("plugin metadata report discarded: session=%s run=%s seq=%d", params.SessionID, params.RunID, params.Seq)
		return false
	}
	return true
}

func (d *Daemon) beginPluginSessionLaunch(sessionID, pluginName, runID string) {
	d.pluginDriverMu.Lock()
	defer d.pluginDriverMu.Unlock()
	if d.pluginLaunching == nil {
		d.pluginLaunching = make(map[string]pluginSessionLaunch)
	}
	if d.pluginReports == nil {
		d.pluginReports = make(map[string][]pendingPluginReport)
	}
	d.pluginLaunching[sessionID] = pluginSessionLaunch{PluginName: pluginName, RunID: runID}
	delete(d.pluginReports, sessionID)
}

func (d *Daemon) queueReportDuringPluginLaunch(plugin *pluginConnection, sessionID string, report pendingPluginReport) bool {
	d.pluginDriverMu.Lock()
	defer d.pluginDriverMu.Unlock()
	launch, ok := d.pluginLaunching[sessionID]
	if !ok || launch.PluginName != plugin.name || launch.RunID != report.runID() {
		return false
	}
	d.pluginReports[sessionID] = append(d.pluginReports[sessionID], report)
	return true
}

func (d *Daemon) finishPluginSessionLaunch(sessionID string, success bool) {
	d.pluginDriverMu.Lock()
	reports := append([]pendingPluginReport(nil), d.pluginReports[sessionID]...)
	delete(d.pluginReports, sessionID)
	delete(d.pluginLaunching, sessionID)
	d.pluginDriverMu.Unlock()
	if !success {
		return
	}
	for _, report := range reports {
		switch {
		case report.State != nil:
			d.applyPluginReportedState(*report.State)
		case report.Stop != nil:
			d.applyPluginReportedStop(*report.Stop)
		case report.Metadata != nil:
			d.applyPluginReportedMetadata(*report.Metadata)
		}
	}
}

func (r pendingPluginReport) runID() string {
	switch {
	case r.State != nil:
		return r.State.RunID
	case r.Stop != nil:
		return r.Stop.RunID
	case r.Metadata != nil:
		return r.Metadata.RunID
	default:
		return ""
	}
}

func (d *Daemon) closePluginDriverSession(sessionID, reason string, exitCode *int, signal string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	runID := d.store.EndAgentDriverRun(sessionID)
	if runID == "" {
		return
	}
	driver, ok := d.ensurePluginRegistry().driver(string(session.Agent))
	if !ok {
		return
	}
	plugin := d.ensurePluginRegistry().get(driver.PluginName)
	if plugin == nil {
		return
	}
	params := pluginDriverSessionClosedParams{
		SessionID: sessionID,
		RunID:     runID,
		Reason:    strings.TrimSpace(reason),
		ExitCode:  exitCode,
		Signal:    strings.TrimSpace(signal),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), pluginDriverCallTimeout)
		defer cancel()
		var result pluginDriverSessionClosedResult
		if err := plugin.request(ctx, "driver.session_closed", params, &result); err != nil {
			d.logf("plugin session close notification failed: plugin=%s session=%s run=%s err=%v", driver.PluginName, sessionID, runID, err)
		}
	}()
}

func (d *Daemon) resolvePluginDriverLaunch(reg pluginDriverRegistration, params pluginDriverSpawnParams, resume bool) (pluginDriverSpawnResult, error) {
	if params.Yolo && !reg.Capabilities["yolo"] {
		return pluginDriverSpawnResult{}, fmt.Errorf("agent %q does not support yolo launches", reg.Agent)
	}
	if resume && !reg.Capabilities["resume"] {
		return pluginDriverSpawnResult{}, fmt.Errorf("agent %q does not support resume", reg.Agent)
	}
	method := "driver.spawn"
	if resume {
		method = "driver.resume"
	}
	var result pluginDriverSpawnResult
	ctx, cancel := context.WithTimeout(context.Background(), pluginDriverCallTimeout)
	defer cancel()
	if err := d.callPlugin(ctx, reg.PluginName, method, params, &result); err != nil {
		return pluginDriverSpawnResult{}, err
	}
	if len(result.Argv) == 0 || strings.TrimSpace(result.Argv[0]) == "" {
		return pluginDriverSpawnResult{}, fmt.Errorf("plugin %q returned an empty argv", reg.PluginName)
	}
	return result, nil
}

func pluginCommandEnv(values map[string]string) ([]string, error) {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("plugin driver returned invalid environment key %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env, nil
}
