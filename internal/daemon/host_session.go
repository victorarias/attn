package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// Conversation sessions: the agent runs in a headless host process instead of a
// PTY — no bytes, grid, or attach, just an envelope stream out and a prompt verb
// in. A plugin driver opts in via the "conversation" capability.

// pluginDriverConversationCapability is the driver capability that routes an
// agent's sessions to the host runtime instead of the PTY backend.
const pluginDriverConversationCapability = "conversation"

func (d *Daemon) ensureHostSessions() *hostsession.Manager {
	d.hostSessionsMu.Lock()
	defer d.hostSessionsMu.Unlock()
	if d.hostSessions == nil {
		d.hostSessions = hostsession.New(d.logf, d.handleHostEvent, d.handleHostExit)
	}
	return d.hostSessions
}

// isConversationAgent reports whether sessions for this agent run in a host.
func (d *Daemon) isConversationAgent(agent string) bool {
	driver, ok := d.ensurePluginRegistry().driver(agent)
	return ok && driver.Capabilities[pluginDriverConversationCapability]
}

// isHostSession reports whether a live host owns this session right now.
func (d *Daemon) isHostSession(sessionID string) bool {
	return d.ensureHostSessions().Has(sessionID)
}

// liveRuntimeSessionIDs is every session with a runtime behind it, PTY- or
// host-backed. Liveness checks must ask this, never the PTY backend alone.
func (d *Daemon) liveRuntimeSessionIDs(ctx context.Context) []string {
	var ids []string
	if d.ptyBackend != nil {
		ids = append(ids, d.ptyBackend.SessionIDs(ctx)...)
	}
	return append(ids, d.ensureHostSessions().SessionIDs()...)
}

// hostSessionLogPath keeps a host's stdout/stderr beside the PTY worker logs,
// one file per session.
func hostSessionLogPath(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "log", sessionID+".log")
}

// hostSessionStateDir is under attn's data dir, never the agent's own home.
func hostSessionStateDir(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "state", sessionID)
}

// hostSessionStateDirHoldsConversation reports whether this session's own dir
// already holds a pi session file.
//
// It answers the same question the host asks itself at startup, and the two
// have to agree: the host consults `ATTN_NISSE_RESUME_FILE` only when its dir
// is empty, so this is what tells a first launch (which will fork) from a
// relaunch (which continues its own history and ignores the resume file
// entirely). Anything checking the resume file has to check this first, or a
// revive of a long-established conversation starts depending on a file it will
// never open.
func hostSessionStateDirHoldsConversation(sessionID string) bool {
	entries, err := os.ReadDir(hostSessionStateDir(sessionID))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			return true
		}
	}
	return false
}

// loginShellEnvForSpawn is the user's login shell environment, captured now if
// the pre-warm has not landed, so the first session matches the second.
func (d *Daemon) loginShellEnvForSpawn() []string {
	if cached := d.cachedLoginShellEnv(); len(cached) > 0 {
		return cached
	}
	shell := pty.GetUserLoginShell()
	if shell == "" {
		return nil
	}
	env, err := pty.ReadLoginShellEnv(shell)
	if err != nil {
		d.logf("host spawn: failed to capture login shell env from %s: %v", shell, err)
		return nil
	}
	return env
}

// spawnHostSession starts the host for a conversation session.
func (d *Daemon) spawnHostSession(opts ptybackend.SpawnOptions) error {
	// pi scans ~/.agents/skills, which until now only the codex driver's
	// PrepareLaunch wrote — so a machine without codex gave a delegated
	// conversation agent repo guidance and no attn skill. Best-effort: a missing
	// skill is a poorer agent, not a session that must refuse to start.
	if synced, err := agentdriver.EnsureAgentsSkillInstalled(); err != nil {
		d.logf("host spawn: failed to ensure the attn skill under ~/.agents: %v", err)
	} else if !synced {
		d.logf("host spawn: skipping user-global attn skill sync for profile %q", config.ProfileLabel())
	}
	// Daemon env, then login shell on top: credentials live in shell profiles and
	// an app-launched host would otherwise fail its first prompt with "no API key".
	env := pty.MergeEnvironment(os.Environ(), d.loginShellEnvForSpawn())
	env = pty.MergeEnvironment(env, opts.ExternalEnv)
	// The agent's own tools run as grandchildren of this host, and `attn` is how
	// they report — a delegated agent comments on its ticket by shelling out.
	// Two things have to be true for that to land anywhere: the `attn` they find
	// must be the one that spawned them, and it must know which session is
	// speaking. Neither is inherited. Without the first, a session on a
	// non-production profile resolves the production app off the login shell's
	// PATH; without the second, `attn ticket comment` has no session to attribute
	// and the delegation reports as nobody. This is the PTY path's identity block
	// (see buildSpawnEnv in internal/pty/manager.go) — the two runtimes owe the
	// agent the same environment.
	env = launchenv.WithActiveAttnFirst(env, launchenv.ActiveAttnExecutable())
	hostEnv := []string{
		"ATTN_INSIDE_APP=1",
		"ATTN_DAEMON_MANAGED=1",
		"ATTN_SESSION_ID=" + opts.ID,
		"ATTN_AGENT=" + opts.Agent,
		"ATTN_NISSE_SESSION_ID=" + opts.ID,
		"ATTN_NISSE_SESSION_DIR=" + hostSessionStateDir(opts.ID),
		"ATTN_NISSE_CWD=" + opts.CWD,
	}
	// A resume is a starting condition, not a mode: the host forks the named
	// file into this session's own state dir the first time it starts, and every
	// later start of the same session continues what is already there. Passing it
	// on a revive too is what covers the host that died before writing anything.
	if resume := strings.TrimSpace(opts.ResumeConversationFile); resume != "" {
		hostEnv = append(hostEnv, "ATTN_NISSE_RESUME_FILE="+resume)
	}
	env = pty.MergeEnvironment(env, hostEnv)
	routingEnv := opts.DaemonEnv
	if len(routingEnv) == 0 {
		routingEnv = d.spawnRoutingEnv()
	}
	env = pty.MergeEnvironment(env, routingEnv)
	cwd := strings.TrimSpace(opts.ExternalCWD)
	if cwd == "" {
		cwd = opts.CWD
	}
	return d.ensureHostSessions().Spawn(hostsession.SpawnOptions{
		SessionID:    opts.ID,
		LifecycleID:  opts.LifecycleID,
		Command:      opts.ExternalCommand,
		Env:          env,
		CWD:          cwd,
		LogPath:      hostSessionLogPath(opts.ID),
		RegistryPath: hostsession.RegistryPath(config.DataDir(), opts.ID),
	})
}

// spawnSessionRuntime starts whichever runtime this session's agent asked for.
func (d *Daemon) spawnSessionRuntime(req *spawnRequest, opts ptybackend.SpawnOptions) error {
	opts.DaemonEnv = d.spawnRoutingEnv()
	if req.hasPluginDriver && req.pluginDriver.Capabilities[pluginDriverConversationCapability] {
		return d.spawnHostSession(opts)
	}
	return d.ptyBackend.Spawn(context.Background(), opts)
}

// killSessionRuntime stops a session's runtime, whichever kind it is.
func (d *Daemon) killSessionRuntime(sessionID string) error {
	if d.isHostSession(sessionID) {
		if err := d.ensureHostSessions().Kill(sessionID); err != nil && !errors.Is(err, hostsession.ErrNotFound) {
			return err
		}
		return nil
	}
	return d.ptyBackend.Kill(context.Background(), sessionID, syscall.SIGTERM)
}

// removeSessionRuntime drops a session's runtime record. A host has none to
// drop: Kill already returned with the process group gone.
func (d *Daemon) removeSessionRuntime(sessionID string) error {
	if d.isHostSession(sessionID) {
		return nil
	}
	return d.ptyBackend.Remove(context.Background(), sessionID)
}

// hostStateDeclarationKinds are the envelope kinds that MOVE the session and so
// carry the state to apply; everything else a host emits is forwarded only. A
// tool boundary is deliberately absent: applyState restamps `state_since`, so
// re-applying `working` per tool call would reset "working for 4m" repeatedly.
var hostStateDeclarationKinds = map[string]bool{
	"session_ready": true,
	"run_started":   true,
	"run_settled":   true,
}

// handleHostEvent forwards one envelope to every connected client and applies
// the state a declaration carries. The forwarding is a stream, not a state
// change: pty_output's direct path, not the bus. The declaration's own seq is
// the ordering cursor, so a stale envelope loses to the store's increasing CAS.
func (d *Daemon) handleHostEvent(event hostsession.Event) {
	d.wsHub.BroadcastValue(&protocol.AgentEventMessage{
		Event: protocol.EventAgentEvent,
		ID:    event.SessionID,
		Seq:   event.Seq,
		Kind:  event.Kind,
		Body:  event.Body,
	})
	// seq 0 is the daemon's own envelope, minted off the host's spine (see
	// handleAgentPrompt). It exists to unstick a client, not to describe a
	// session, so it declares nothing.
	if hostStateDeclarationKinds[event.Kind] && event.Seq > 0 {
		d.applyHostDeclaredState(event)
	}
	if event.Kind == "model_changed" && event.Seq > 0 {
		d.handleHostModelChanged(event)
	}
}

// handleHostModelChanged records a mid-session model switch where a relaunch
// will read it.
//
// A conversation session can come back — a crash, a reload, a machine restart —
// and it comes back through the stored launch intent, which carries the model
// the spawn pinned. Without this, a session switched to a different model at
// 10am silently reverts to the pinned one the first time it is revived, and the
// conversation continues on a model the user stopped using hours ago.
//
// This is deliberately not a state declaration. `applyState` restamps
// `state_since` on every apply, so routing a model switch through it would reset
// "working for 4m" for something that did not move the session at all.
func (d *Daemon) handleHostModelChanged(event hostsession.Event) {
	model, _ := event.Body["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		// A refusal: pi kept the model it had. Nothing to record — the envelope's
		// job there is to correct the client that snapped its picker early.
		return
	}
	intent, ok := d.store.LaunchIntent(event.SessionID)
	if !ok || intent.Model == model {
		return
	}
	intent.Model = model
	d.store.SetLaunchIntent(event.SessionID, intent)
}

// applyHostDeclaredState routes one declaration's state through the daemon's
// only persisted-state door.
//
// It travels as a plugin report because that is exactly what it is: an agent
// whose driver declares its own state, reporting it under the run cursor the
// spawn opened. What differs from the JSON-RPC drivers is only the pipe it
// arrived on. Reusing the cause buys the ordered CAS, the resolver veto, and
// the recovery rules that already exist for declared state, instead of a second
// set that would have to be kept in agreement with them.
func (d *Daemon) applyHostDeclaredState(event hostsession.Event) {
	state, ok := event.Body["state"].(string)
	if !ok {
		// Not every declaration has to move the session — but in this protocol
		// version every one of them does, so a missing state is a host/daemon
		// disagreement worth naming rather than a silent no-op.
		d.logf("host session %s: %s declaration carries no state", event.SessionID, event.Kind)
		return
	}
	state = strings.TrimSpace(state)
	params := pluginReportStateParams{
		SessionID: event.SessionID,
		RunID:     event.LifecycleID,
		Seq:       uint64(event.Seq),
		State:     state,
	}
	if err := validatePluginReportedState(params); err != nil {
		d.logf("host session %s: rejected %s declaration: %v", event.SessionID, event.Kind, err)
		return
	}
	// The host is quick: `session_ready` regularly beats the spawn's own commit,
	// which is where the run cursor is opened. Queue it there exactly as a
	// plugin's report is queued, or the session's first state is lost and it
	// sits in `launching` until its first run.
	if d.queueHostReportDuringLaunch(event.SessionID, params) {
		return
	}
	d.applyPluginReportedState(params)
}

// handleHostExit routes a dead host into the same exit path a dead PTY worker
// takes, then applies `recoverable` — the conversation's whole history is pi's
// session file under attn's data dir, so a dead host left everything a
// replacement needs, and Reload is what the user should see instead of a
// session that reads as finished. Only when the exit really ended that runtime:
// a reload owns its own teardown, and a closed session has no row left to move.
func (d *Daemon) handleHostExit(info hostsession.ExitInfo) {
	if !d.handlePTYExit(ptybackend.ExitInfo{
		ID:          info.SessionID,
		ExitCode:    info.ExitCode,
		Signal:      info.Signal,
		LifecycleID: info.LifecycleID,
	}) {
		return
	}
	if d.store.Get(info.SessionID) == nil {
		return
	}
	d.applyState(sessionStateChange{
		sessionID: info.SessionID,
		state:     string(protocol.SessionStateRecoverable),
		cause:     hostExitRecovery{},
	})
}

// reloadConversationSession is Reload for a session whose runtime is a host.
//
// It is the same two moves the PTY path makes — end the old runtime, run the
// stored launch intent again — with the halves a conversation does not have
// removed. There is no geometry to reconstruct (a host has no grid, so the
// pipeline's required cols/rows are a placeholder), no resume flag to rebuild
// (the replacement host reopens the session file on its own), and no in-place
// respawn (the host manager holds one process per session, so the old one has
// to be gone before the new one is asked for).
//
// The kill is suppressed the same way the PTY reload suppresses its own: a
// reload owns the teardown, so the exit must not broadcast `session_exited`,
// must not mark the session `recoverable`, and must not race the respawn's
// `launching` with a state applied after it. What the suppressed exit skips and
// this path still owes the plugin driver is the close notification for the run
// that just ended, which is why it is sent here by hand.
func (d *Daemon) reloadConversationSession(session *protocol.Session) error {
	sessionID := session.ID
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return errors.New("no stored launch intent")
	}
	killed := false
	if d.isHostSession(sessionID) {
		d.markReloading(sessionID)
		if err := d.ensureHostSessions().Kill(sessionID); err != nil && !errors.Is(err, hostsession.ErrNotFound) {
			d.clearReloading(sessionID)
			return err
		}
		killed = true
		// The suppressed exit did not run the driver's close, and the spawn below
		// opens a new run cursor over the old one.
		d.closePluginDriverSession(sessionID, "reloaded", nil, "")
	}
	// A host draws nothing, so this geometry is never consulted; the spawn
	// pipeline only refuses a non-positive one.
	spawnMsg, policy := buildStoredIntentSpawn(session, intent, 80, 24)
	if rejection := d.runSpawnPipeline(spawnMsg, policy); rejection != nil {
		d.clearReloading(sessionID)
		if killed {
			// The old host is gone and its exit was suppressed, so nothing else
			// will ever say so. Run the exit now: a session that reads as live
			// over a host that is not is the one outcome this must never leave
			// behind, and `recoverable` is both true and the way back.
			d.handleHostExit(hostsession.ExitInfo{SessionID: sessionID, ExitCode: 1})
		}
		return rejection.reason()
	}
	// Success. The killed host's exit consumes the flag; the grace timer in
	// executePreparedSessionReload's counterpart exists for the same reason here
	// — an exit that never arrives must not suppress a later, unrelated one.
	time.AfterFunc(reloadStuckFlagGrace, func() { d.clearReloading(sessionID) })
	d.publishFact(FactSessionRespawned, sessionID, nil)
	return nil
}

// deliverToHostSession lands a message in a conversation session's agent.
//
// It is the conversation half of message delivery, and the reason a doorbell
// for one of these sessions types nothing: there is no composer to paste into
// and no Enter to send, only a verb down a pipe the daemon already owns. A
// steer is the right default for every nudge — it is read at the agent's next
// turn boundary rather than after everything it had planned to do — and it is
// safe on an idle session, where the host opens a run for it instead.
func (d *Daemon) deliverToHostSession(sessionID string, how hostsession.Delivery, text string) error {
	return d.ensureHostSessions().Deliver(sessionID, how, text)
}

// hostDeliveryFor maps the app's delivery request onto a host verb. An absent
// or unknown mode is a plain prompt: that is what every client before this
// protocol version meant, and what the composer means when no run is open.
func hostDeliveryFor(mode string) hostsession.Delivery {
	switch hostsession.Delivery(strings.TrimSpace(mode)) {
	case hostsession.DeliverySteer:
		return hostsession.DeliverySteer
	case hostsession.DeliveryFollowUp:
		return hostsession.DeliveryFollowUp
	default:
		return hostsession.DeliveryPrompt
	}
}

// handleAgentPrompt answers the agent_prompt command.
func (d *Daemon) handleAgentPrompt(client *wsClient, msg *protocol.AgentPromptMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	text := strings.TrimSpace(msg.Text)
	if sessionID == "" {
		d.sendCommandError(client, protocol.CmdAgentPrompt, "agent_prompt is missing a session id")
		return
	}
	if text == "" {
		d.sendCommandError(client, protocol.CmdAgentPrompt, "agent_prompt is missing text")
		return
	}
	how := hostDeliveryFor(protocol.Deref(msg.Mode))
	if err := d.deliverToHostSession(sessionID, how, text); err != nil {
		d.logf("agent_prompt (%s) for session %s failed: %v", how, sessionID, err)
		d.sendCommandError(client, protocol.CmdAgentPrompt, "no live conversation host for session "+sessionID)
		if how != hostsession.DeliveryPrompt {
			// A steer or follow-up left the composer open and no run to settle;
			// the command error is the whole answer.
			return
		}
		// The app closed its composer on send, so a prompt that never reached a
		// host comes back as the run it will never open; seq 0 marks a daemon
		// envelope, and it declares no state because a session with no host is
		// the exit path's to describe.
		d.handleHostEvent(hostsession.Event{
			SessionID: sessionID,
			Kind:      "run_settled",
			Body:      map[string]interface{}{"error": "this conversation's agent is no longer running"},
		})
	}
}

// handleAgentToolDetail answers the agent_tool_detail command.
//
// Nothing is returned here. The host writes its answer onto the same envelope
// stream every other rendering travels on, addressed by the call id, so the
// card that asked draws when it lands — and so does an identical card open in
// another window. A session with no live host is the only failure this can
// name, and it names it rather than leaving a card spinning forever.
func (d *Daemon) handleAgentToolDetail(client *wsClient, msg *protocol.AgentToolDetailMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	callID := strings.TrimSpace(msg.CallID)
	if sessionID == "" || callID == "" {
		d.sendCommandError(client, protocol.CmdAgentToolDetail, "agent_tool_detail needs a session id and a call id")
		return
	}
	if err := d.ensureHostSessions().ToolDetail(sessionID, callID, protocol.Deref(msg.Full)); err != nil {
		d.logf("agent_tool_detail for session %s call %s failed: %v", sessionID, callID, err)
		d.sendCommandError(client, protocol.CmdAgentToolDetail, "no live conversation host for session "+sessionID)
	}
}

// handleAgentAttach answers the agent_attach command: a client that has no
// picture of a conversation asks the host for one.
//
// Nothing comes back on this client's socket. The host answers on the envelope
// stream every other rendering travels on, and that answer is a broadcast — the
// snapshot is the host's own transcript, which is a superset of what any client
// holds, so replacing with it can only ever move a client forward. That is what
// makes "a second window shows the same conversation" true by construction
// rather than by two clients happening to have seen the same bytes.
func (d *Daemon) handleAgentAttach(client *wsClient, msg *protocol.AgentAttachMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	if sessionID == "" {
		d.sendCommandError(client, protocol.CmdAgentAttach, "agent_attach is missing a session id")
		return
	}
	if err := d.ensureHostSessions().Snapshot(sessionID); err != nil {
		d.logf("agent_attach for session %s failed: %v", sessionID, err)
		d.sendCommandError(client, protocol.CmdAgentAttach, "no live conversation host for session "+sessionID)
	}
}

// handleAgentHistory answers the agent_history command: a client scrolled to the
// top of what it holds and asks for the conversation behind it.
//
// The page comes back on the envelope stream, broadcast like the snapshot,
// addressed by the anchor item it pages before rather than by a request id. Two
// windows sitting at the same place in a long conversation therefore cost one
// read, and a window standing somewhere else recognises the anchor as not its
// own and ignores the page. A host that died mid-scroll answers nothing; the
// command error is what stops the spinner.
func (d *Daemon) handleAgentHistory(client *wsClient, msg *protocol.AgentHistoryMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	before := strings.TrimSpace(msg.Before)
	if sessionID == "" || before == "" {
		d.sendCommandError(client, protocol.CmdAgentHistory, "agent_history needs a session id and a before cursor")
		return
	}
	if err := d.ensureHostSessions().History(sessionID, before); err != nil {
		d.logf("agent_history for session %s before %s failed: %v", sessionID, before, err)
		d.sendCommandError(client, protocol.CmdAgentHistory, "no live conversation host for session "+sessionID)
	}
}

// handleAgentSetModel answers the agent_set_model command.
//
// Nothing is decided here: the host asks pi to switch and reports the model
// actually in force in a `model_changed` envelope, which is also what rewrites
// the launch intent (see handleHostModelChanged). A refusal — a model this
// machine has no credentials for — comes back the same way, so a picker that
// snapped to the new value corrects itself from the host's answer rather than
// from a guess made here.
func (d *Daemon) handleAgentSetModel(client *wsClient, msg *protocol.AgentSetModelMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	model := strings.TrimSpace(msg.Model)
	if sessionID == "" || model == "" {
		d.sendCommandError(client, protocol.CmdAgentSetModel, "agent_set_model needs a session id and a model")
		return
	}
	if err := d.ensureHostSessions().SetModel(sessionID, model); err != nil {
		d.logf("agent_set_model for session %s to %s failed: %v", sessionID, model, err)
		d.sendCommandError(client, protocol.CmdAgentSetModel, "no live conversation host for session "+sessionID)
	}
}

// handleAgentClearQueue answers the agent_clear_queue command.
//
// Like the detail fetch, the visible result comes back on the envelope stream:
// the host clears pi's queues and pi says what is left, which is what empties
// the strip. Reporting success here would only promise something this daemon
// has not seen happen.
func (d *Daemon) handleAgentClearQueue(client *wsClient, msg *protocol.AgentClearQueueMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	if sessionID == "" {
		d.sendCommandError(client, protocol.CmdAgentClearQueue, "agent_clear_queue is missing a session id")
		return
	}
	if err := d.ensureHostSessions().ClearQueue(sessionID); err != nil {
		d.logf("agent_clear_queue for session %s failed: %v", sessionID, err)
		d.sendCommandError(client, protocol.CmdAgentClearQueue, "no live conversation host for session "+sessionID)
	}
}
