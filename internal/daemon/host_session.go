package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// Conversation sessions: attn sessions whose agent runs in a headless host
// process instead of a PTY. The daemon owns the host's lifetime exactly as it
// owns a PTY worker's; what changes is the surface. There are no bytes, no
// grid, and no attach — there is an envelope stream the app draws from and a
// prompt verb going the other way.
//
// A plugin driver opts its agent in by registering the "conversation"
// capability. Everything else about the spawn — argv, env, cwd — comes back
// from the same driver.spawn call a PTY-backed plugin agent uses, so a
// conversation agent is configured, listed, and launched like any other.

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

// liveRuntimeSessionIDs is every session with a runtime behind it, PTY-backed
// or host-backed. Anything that asks "is this session still alive?" — pruning
// dead rows, reconciling workspace panes, tearing everything down — must ask
// this and not the PTY backend alone, or a live conversation session reads as
// abandoned.
func (d *Daemon) liveRuntimeSessionIDs(ctx context.Context) []string {
	var ids []string
	if d.ptyBackend != nil {
		ids = append(ids, d.ptyBackend.SessionIDs(ctx)...)
	}
	return append(ids, d.ensureHostSessions().SessionIDs()...)
}

// hostSessionLogPath keeps a host's own stdout/stderr beside the PTY worker
// logs, one file per session, so a wedged host is diagnosable the same way a
// wedged worker is.
func hostSessionLogPath(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "log", sessionID+".log")
}

// hostSessionStateDir is where a host keeps whatever it persists for the
// session — for pi, its own session file. Under attn's data dir, never the
// agent's own home, so attn owns what it spawned.
func hostSessionStateDir(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "state", sessionID)
}

// loginShellEnvForSpawn is the user's login shell environment, captured now if
// the daemon's pre-warm has not landed yet. The PTY path makes the same
// fallback, for the same reason: the first session of a daemon's life must not
// get a thinner environment than the second.
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

// spawnHostSession starts the host for a conversation session. It takes the
// same launch description the PTY path would have run.
func (d *Daemon) spawnHostSession(opts ptybackend.SpawnOptions) error {
	// A host gets the same environment a PTY agent gets, layered the same way:
	// the daemon's own environment, then the user's login shell on top. Agents
	// read credentials from there — an API key exported in a shell profile is
	// the ordinary way pi is authenticated — and a host launched from the app
	// (which the window server starts with almost no environment) would
	// otherwise fail its first prompt with "no API key found" for a key the
	// user has had set for years.
	env := pty.MergeEnvironment(os.Environ(), d.loginShellEnvForSpawn())
	env = pty.MergeEnvironment(env, opts.ExternalEnv)
	env = pty.MergeEnvironment(env, []string{
		"ATTN_PI_HOST_SESSION_ID=" + opts.ID,
		"ATTN_PI_HOST_SESSION_DIR=" + hostSessionStateDir(opts.ID),
		"ATTN_PI_HOST_CWD=" + opts.CWD,
	})
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
// It is the one fork in the spawn pipeline: everything before it (validation,
// launch intent, the driver's argv) and everything after it (the session row,
// panes, facts) is identical for both kinds.
func (d *Daemon) spawnSessionRuntime(req *spawnRequest, opts ptybackend.SpawnOptions) error {
	if req.hasPluginDriver && req.pluginDriver.Capabilities[pluginDriverConversationCapability] {
		return d.spawnHostSession(opts)
	}
	return d.ptyBackend.Spawn(context.Background(), opts)
}

// killSessionRuntime stops a session's runtime, whichever kind it is. Used by
// the spawn pipeline's rollbacks, where the session may be either.
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

// hostDeclarationKinds are the envelope kinds the daemon reads. Everything else
// a host emits is a rendering the app draws and the daemon only forwards, and
// keeping that list here rather than in a switch is what makes "the daemon
// never keys behavior on a render kind" checkable.
var hostDeclarationKinds = map[string]bool{
	"session_ready": true,
	"run_started":   true,
	"run_settled":   true,
}

// handleHostEvent forwards one envelope to every connected client, and applies
// the state a declaration carries.
//
// The forwarding is a stream, not a state change: it takes the same direct path
// pty_output does rather than riding the event bus.
//
// The state is the other half. Every declaration carries the attn state it puts
// the session in — the host is attn code inside the agent's own loop, so it says
// what the session is doing instead of leaving the daemon to infer it from a run
// boundary or a resolver to guess it from evidence a conversation session does
// not produce. The declaration's own seq is the ordering cursor, which is why a
// verdict-shaped race cannot happen here: a stale envelope carries a stale seq
// and the store's strictly-increasing CAS drops it.
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
	if hostDeclarationKinds[event.Kind] && event.Seq > 0 {
		d.applyHostDeclaredState(event)
	}
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
// takes: the session's end is the session's end, whatever was running it.
func (d *Daemon) handleHostExit(info hostsession.ExitInfo) {
	d.handlePTYExit(ptybackend.ExitInfo{
		ID:          info.SessionID,
		ExitCode:    info.ExitCode,
		Signal:      info.Signal,
		LifecycleID: info.LifecycleID,
	})
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
			// A steer or follow-up left the composer open and the run — as far
			// as this client knows — already running. There is no run to settle
			// and nothing to reopen; the command error is the whole answer.
			return
		}
		// The app closes its composer the moment it sends a prompt, so one that
		// never reached a host has to come back as the run it will never open.
		// seq 0 says this is the daemon's own envelope rather than a point on
		// the host's spine, which is why it cannot collide with one — and why
		// it declares no state: a session with no host is the exit path's to
		// describe, not this one's.
		d.handleHostEvent(hostsession.Event{
			SessionID: sessionID,
			Kind:      "run_settled",
			Body:      map[string]interface{}{"error": "this conversation's agent is no longer running"},
		})
	}
}
