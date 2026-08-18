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
	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const pluginDriverCallTimeout = 30 * time.Second
const pluginDeliverMessageTimeout = 15 * time.Second

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
	OK         bool              `json:"ok"`
	ActiveRuns []activePluginRun `json:"active_runs,omitempty"`
}

// activePluginRun hands one live run back to a driver that has just
// (re)registered. Seq is the run's report cursor, and a replacement driver
// process cannot work without it: reports are ordered by a strictly-increasing
// seq per run, so a driver that restarted its own counter has every report
// discarded. It is sent as a plain number rather than omitempty so a driver can
// tell "the cursor is zero" from "this daemon does not send cursors".
type activePluginRun struct {
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Seq       uint64          `json:"seq"`
}

type pluginDriverSpawnParams struct {
	// Agent names which of the plugin's registered drivers this launch is for.
	// A plugin may register more than one — attn-pi registers both a PTY-backed
	// `pi` and a conversation `nisse` — and without it the plugin cannot tell
	// which one attn is asking to launch.
	Agent         string                    `json:"agent"`
	SessionID     string                    `json:"session_id"`
	RunID         string                    `json:"run_id"`
	CWD           string                    `json:"cwd"`
	Label         string                    `json:"label,omitempty"`
	Yolo          bool                      `json:"yolo,omitempty"`
	Model         string                    `json:"model,omitempty"`
	Effort        string                    `json:"effort,omitempty"`
	InitialPrompt string                    `json:"initial_prompt,omitempty"`
	Metadata      json.RawMessage           `json:"metadata,omitempty"`
	Instructions  *pluginLaunchInstructions `json:"instructions,omitempty"`
	// The promoted auto-mode config, for a driver that advertises `auto_mode`.
	// It is the exact JSON shape plugins/attn-pi/automode/config.ts parses, so a
	// driver forwards it to the session rather than translating it. Config
	// changes reach new sessions only; a live session is not refreshed.
	AutoMode *automode.Config `json:"auto_mode,omitempty"`
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
	// OnlyIfUnknown is a driver answering the question the daemon asked by
	// declaring `unknown`: this is what the agent says it is, use it if you
	// still have nothing. Applied unconditionally it would restamp
	// `state_since` and re-open a settled turn on every reconnect.
	OnlyIfUnknown bool `json:"only_if_unknown,omitempty"`
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

type pluginDeliverMessageParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Text      string `json:"text"`
}

type pluginDeliverMessageResult struct {
	OK bool `json:"ok"`
}

// One call a driver's agent refused under auto mode. No seq: a denial is an
// append, not a declaration about the session's current state, so nothing later
// can overtake it.
type pluginReportAutoModeDenialParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Tool      string `json:"tool"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	Rule      string `json:"rule"`
	// When the session refused it, RFC 3339. Empty falls back to arrival.
	At string `json:"at"`
}

type pluginClassifyStopParams struct {
	SessionID     string `json:"session_id"`
	RunID         string `json:"run_id"`
	AssistantText string `json:"assistant_text"`
}

type pluginClassifyStopResult struct {
	Verdict string `json:"verdict"`
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
		"resume":              {},
		"yolo":                {},
		"initial_prompt":      {},
		"classifier":          {},
		"state_reporting":     {},
		"pending_approval":    {},
		"message_delivery":    {},
		"model_pin":           {},
		"effort_pin":          {},
		"launch_instructions": {},
		"conversation":        {},
		"auto_mode":           {},
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
		d.logf("plugin driver registered plugin=%s agent=%s", plugin.name, normalizePluginAgent(params.Agent))
		d.publishSettingsFact(FactPluginDriverRegistered, plugin.name)
		active := d.store.ListAgentDriverRuns(plugin.name)
		runs := make([]activePluginRun, 0, len(active))
		for _, run := range active {
			item := activePluginRun{SessionID: run.SessionID, RunID: run.RunID, Seq: run.Seq}
			if json.Valid([]byte(run.Metadata)) {
				item.Metadata = json.RawMessage(run.Metadata)
			}
			runs = append(runs, item)
		}
		return pluginDriverRegisterResult{OK: true, ActiveRuns: runs}, true, nil
	case "session.report_state":
		var params pluginReportStateParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_state params: %w", err)
		}
		if err := validatePluginReportedState(params); err != nil {
			return nil, true, err
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{State: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
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
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
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
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedMetadata(params)
		}
		return struct{}{}, true, nil
	case "session.report_automode_denial":
		var params pluginReportAutoModeDenialParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_automode_denial params: %w", err)
		}
		if strings.TrimSpace(params.Action) == "" {
			return nil, true, errors.New("session.report_automode_denial action is required")
		}
		if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
			return nil, true, err
		}
		d.notePluginDriverReport(params.SessionID)
		if err := d.recordAutoModeDenial(params); err != nil {
			return nil, true, err
		}
		return struct{}{}, true, nil
	default:
		return nil, false, nil
	}
}

func (d *Daemon) authorizePluginSessionReport(plugin *pluginConnection, sessionID, runID string) error {
	sessionID = strings.TrimSpace(sessionID)
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("unknown session %q", sessionID)
	}
	cursor := d.store.GetAgentDriverRun(sessionID)
	if cursor.PluginName != plugin.name || cursor.RunID != strings.TrimSpace(runID) {
		return fmt.Errorf("plugin %q does not own active run %q for session %q", plugin.name, strings.TrimSpace(runID), sessionID)
	}
	return nil
}

// handlePluginClassifyStop backs the attn.classify_stop plugin->daemon method:
// a text-in/verdict-out stop classification service for drivers whose agent
// declares its own state (rather than attn scraping or classifying it). It
// validates and authorizes synchronously so malformed or unowned requests
// fail fast, then classifies on a goroutine and sends the JSON-RPC result
// itself. This must not block the caller: handlePluginMethod runs on the
// plugin connection's synchronous read loop, and the classifier LLM call can
// take 30+ seconds — running it inline would stall every other driver.*
// request and state report on this plugin connection.
func (d *Daemon) handlePluginClassifyStop(plugin *pluginConnection, msg jsonRPCMessage) {
	var params pluginClassifyStopParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, fmt.Sprintf("decode attn.classify_stop params: %v", err)))
		return
	}
	text := strings.TrimSpace(params.AssistantText)
	if text == "" {
		_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "assistant_text is required"))
		return
	}
	if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
		_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	session := d.store.Get(params.SessionID)
	id := msg.ID
	go func() {
		verdict, err := d.runClassifier(session, text, 30*time.Second)
		if err != nil {
			verdict = protocol.StateUnknown
		}
		_ = plugin.send(jsonRPCResult(id, pluginClassifyStopResult{Verdict: verdict}))
	}()
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
	// Before the ordering check, not after: a report the cursor discards is still
	// a driver speaking for this session, which is all the silence alarm asks.
	d.notePluginDriverReport(params.SessionID)
	state := strings.TrimSpace(params.State)
	if params.OnlyIfUnknown {
		if session := d.store.Get(params.SessionID); session == nil || session.State != protocol.SessionStateUnknown {
			return false
		}
		d.logf("plugin driver restates session=%s run=%s as %s after unknown", params.SessionID, params.RunID, state)
	}
	if !d.applyState(sessionStateChange{
		sessionID: params.SessionID,
		state:     state,
		cause: pluginReport{
			runID: params.RunID,
			seq:   params.Seq,
		},
		origin: stateOrigin{source: stateSourcePluginDriver, detail: params.RunID},
	}) {
		d.logf("plugin state report discarded: session=%s run=%s seq=%d state=%s", params.SessionID, params.RunID, params.Seq, state)
		return false
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
	d.applyPluginReportedState(pluginReportStateParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       params.Seq,
		State:     strings.TrimSpace(params.Verdict),
	})
}

func (d *Daemon) applyPluginReportedMetadata(params pluginReportMetadataParams) bool {
	d.notePluginDriverReport(params.SessionID)
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
	if d.pluginExits == nil {
		d.pluginExits = make(map[string]ptybackend.ExitInfo)
	}
	d.pluginLaunching[sessionID] = pluginSessionLaunch{PluginName: pluginName, RunID: runID}
	delete(d.pluginReports, sessionID)
	delete(d.pluginExits, sessionID)
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

// queueHostReportDuringLaunch is the same holding pen for a conversation
// session's declared state, which arrives on the host's envelope stream rather
// than over a plugin connection. The run id is what it matches on: it is minted
// per launch and handed to exactly one host, so it identifies the reporter as
// precisely as the plugin connection does for the JSON-RPC drivers.
func (d *Daemon) queueHostReportDuringLaunch(sessionID string, params pluginReportStateParams) bool {
	d.pluginDriverMu.Lock()
	defer d.pluginDriverMu.Unlock()
	launch, ok := d.pluginLaunching[sessionID]
	if !ok || launch.RunID == "" || launch.RunID != strings.TrimSpace(params.RunID) {
		return false
	}
	d.pluginReports[sessionID] = append(d.pluginReports[sessionID], pendingPluginReport{State: &params})
	return true
}

func (d *Daemon) queueExitDuringPluginLaunch(info ptybackend.ExitInfo) bool {
	d.pluginDriverMu.Lock()
	defer d.pluginDriverMu.Unlock()
	launch, ok := d.pluginLaunching[info.ID]
	if !ok || info.LifecycleID == "" || launch.RunID != info.LifecycleID {
		return false
	}
	// The exit is deferred, not delivered: finishPluginSessionLaunch replays it
	// once the launch it raced is done. Say so, because between here and there
	// the session looks alive to everything that reads the store.
	d.logf("deferring plugin PTY exit until launch completes: session=%s run=%s", info.ID, info.LifecycleID)
	d.pluginExits[info.ID] = info
	return true
}

func (d *Daemon) supersededExitDuringPluginLaunch(info ptybackend.ExitInfo) bool {
	d.pluginDriverMu.Lock()
	defer d.pluginDriverMu.Unlock()
	launch, ok := d.pluginLaunching[info.ID]
	return ok && info.LifecycleID != "" && launch.RunID != info.LifecycleID
}

func (d *Daemon) finishPluginSessionLaunch(sessionID string, success bool) *ptybackend.ExitInfo {
	d.pluginDriverMu.Lock()
	reports := append([]pendingPluginReport(nil), d.pluginReports[sessionID]...)
	exit, exited := d.pluginExits[sessionID]
	delete(d.pluginReports, sessionID)
	delete(d.pluginExits, sessionID)
	delete(d.pluginLaunching, sessionID)
	d.pluginDriverMu.Unlock()
	if !success {
		return nil
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
	if exited {
		return &exit
	}
	return nil
}

// abortPluginSessionLaunch closes plugin-owned resources created by a successful
// driver.spawn/driver.resume response when attn fails before a durable active
// run exists. In particular, a driver may already have staged a prompt or
// credential before PTY spawn or session persistence fails.
func (d *Daemon) abortPluginSessionLaunch(sessionID, reason string) {
	d.pluginDriverMu.Lock()
	launch, ok := d.pluginLaunching[sessionID]
	delete(d.pluginReports, sessionID)
	delete(d.pluginExits, sessionID)
	delete(d.pluginLaunching, sessionID)
	d.pluginDriverMu.Unlock()
	if !ok {
		return
	}
	d.notifyPluginDriverSessionClosed(launch.PluginName, sessionID, launch.RunID, reason, nil, "")
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

// closePluginDriverSession tells the plugin that owns this session's run that
// the run is over. Both ways out without a notification are silent to the
// plugin, so they say so in the log: a plugin that never hears its run closed
// leaks whatever it allocated for it, and the only evidence is here.
func (d *Daemon) closePluginDriverSession(sessionID, reason string, exitCode *int, signal string) {
	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("plugin session close skipped: session=%s reason=%s no longer in store", sessionID, reason)
		return
	}
	run := d.store.EndAgentDriverRun(sessionID)
	if run.RunID == "" {
		d.logf("plugin session close skipped: session=%s reason=%s has no active driver run", sessionID, reason)
		return
	}
	d.notifyPluginDriverSessionClosed(run.PluginName, sessionID, run.RunID, reason, exitCode, signal)
}

func (d *Daemon) notifyPluginDriverSessionClosed(pluginName, sessionID, runID, reason string, exitCode *int, signal string) {
	plugin := d.ensurePluginRegistry().get(pluginName)
	if plugin == nil {
		d.logf("plugin session close notification dropped: plugin=%s session=%s run=%s owner disconnected", pluginName, sessionID, runID)
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
			d.logf("plugin session close notification failed: plugin=%s session=%s run=%s err=%v", pluginName, sessionID, runID, err)
			return
		}
		// The one line that separates "the daemon never sent it" from "the
		// plugin never acted on it" when a close goes missing.
		d.logf("plugin session close notified: plugin=%s session=%s run=%s reason=%s", pluginName, sessionID, runID, params.Reason)
	}()
}

func (d *Daemon) resolvePluginDriverLaunch(reg pluginDriverRegistration, params pluginDriverSpawnParams, resume bool) (pluginDriverSpawnResult, error) {
	if params.Yolo && !reg.Capabilities["yolo"] {
		return pluginDriverSpawnResult{}, fmt.Errorf("agent %q does not support yolo launches", reg.Agent)
	}
	if params.Model != "" && !reg.Capabilities["model_pin"] {
		return pluginDriverSpawnResult{}, fmt.Errorf("agent %q does not support model pins", reg.Agent)
	}
	if params.Effort != "" && !reg.Capabilities["effort_pin"] {
		return pluginDriverSpawnResult{}, fmt.Errorf("agent %q does not support effort pins", reg.Agent)
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

// deliverDoorbellViaPluginDriver delivers prompt in-band through session's
// active plugin driver run when that run's registered driver declares
// message_delivery, reporting delivered=true so typeDoorbell never falls back
// to PTY typing for it (a typed fallback would reintroduce the splice risk
// in-band delivery removes). delivered=false means the caller should use the
// PTY paste path instead.
func (d *Daemon) deliverDoorbellViaPluginDriver(session *protocol.Session, prompt string) (bool, error) {
	cursor := d.store.GetAgentDriverRun(session.ID)
	if cursor.PluginName == "" {
		return false, nil
	}
	driver, ok := d.ensurePluginRegistry().driver(string(session.Agent))
	if !ok || driver.PluginName != cursor.PluginName || !driver.Capabilities["message_delivery"] {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pluginDeliverMessageTimeout)
	defer cancel()
	var result pluginDeliverMessageResult
	params := pluginDeliverMessageParams{SessionID: session.ID, RunID: cursor.RunID, Text: prompt}
	if err := d.callPlugin(ctx, cursor.PluginName, "driver.deliver_message", params, &result); err != nil {
		return true, fmt.Errorf("deliver message via plugin %q: %w", cursor.PluginName, err)
	}
	if !result.OK {
		return true, fmt.Errorf("plugin %q declined message delivery for session %s", cursor.PluginName, session.ID)
	}
	return true, nil
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
